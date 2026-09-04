package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	preflightCommit    = "0123456789abcdef0123456789abcdef01234567"
	preflightTagObject = "89abcdef0123456789abcdef0123456789abcdef"
)

func TestReleasePreflightUsesExactIdentityAndWritesReceipts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runnerTemp := t.TempDir()
	runner := newPreflightRunner(t, nil)
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin"}
	err := ReleasePreflight(context.Background(), ReleasePreflightOptions{
		Root: root, RunnerTemp: runnerTemp, RefName: "v0.6.2", SHA: preflightCommit,
		Repository: "dantte-lp/gobfd", Environment: environment, Runner: runner,
	})
	if err != nil {
		t.Fatalf("ReleasePreflight() error = %v", err)
	}
	want := []specInvocation{
		{name: "git", args: []string{"rev-parse", "HEAD"}, dir: root, env: []string{"PATH=/usr/bin"}},
		{name: "gh", args: []string{"api", "repos/dantte-lp/gobfd/git/ref/tags/v0.6.2"}, dir: root, env: environment},
		{
			name: "gh", args: []string{"api", "repos/dantte-lp/gobfd/git/tags/" + preflightTagObject},
			dir: root, env: environment,
		},
		{name: "gh", args: []string{"api", "repos/dantte-lp/gobfd/git/ref/heads/release/v0.6"}, dir: root, env: environment},
		{name: "gh", args: []string{
			"api", "graphql", "-f", "query=" + releasePreflightGraphQLQuery,
			"-F", "owner=dantte-lp", "-F", "name=gobfd", "-F", "tag=v0.6.2",
		}, dir: root, env: environment},
		{name: "gh", args: []string{
			"api", "--paginate", "/users/dantte-lp/packages/container/gobfd/versions?per_page=100", "--slurp",
		}, dir: root, env: environment},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("preflight calls = %#v, want %#v", runner.calls, want)
	}
	for name, content := range map[string]string{
		"expected-release-commit.txt":     preflightCommit + "\n",
		"expected-release-branch.txt":     "release/v0.6\n",
		"expected-release-tag-object.txt": preflightTagObject + "\n",
	} {
		path := filepath.Join(runnerTemp, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != content {
			t.Errorf("receipt %s = %q, %v", name, data, readErr)
		}
		assertExactMode(t, path, 0o644)
	}
}

func TestReleasePreflightRejectsMalformedIdentityBeforeCommands(t *testing.T) {
	t.Parallel()

	tests := []ReleasePreflightOptions{
		{RefName: "v01.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd"},
		{RefName: "v0.6.2", SHA: "not-a-commit", Repository: "dantte-lp/gobfd"},
		{RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd/extra"},
	}
	for _, options := range tests {
		t.Run(options.RefName+options.Repository, func(t *testing.T) {
			t.Parallel()
			runner := newPreflightRunner(t, nil)
			options.Root = t.TempDir()
			options.RunnerTemp = t.TempDir()
			options.Runner = runner
			if err := ReleasePreflight(context.Background(), options); err == nil {
				t.Fatal("ReleasePreflight() error = nil, want malformed identity rejection")
			}
			if len(runner.calls) != 0 {
				t.Errorf("malformed identity ran %d commands", len(runner.calls))
			}
		})
	}
}

func TestReleasePreflightRejectsExistingReleaseAndVersionTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		releaseResponse string
		packageTags     []string
		want            string
	}{
		{
			name:            "existing draft",
			releaseResponse: `{"data":{"repository":{"release":{"id":"R_1","isDraft":true,"tagName":"v0.6.2"}}}}`,
			want:            "release or draft already exists",
		},
		{name: "plain version", packageTags: []string{"0.6.2"}, want: "versioned OCI tag already exists: 0.6.2"},
		{
			name: "Debian version", packageTags: []string{"0.6.2-debian-trixie"},
			want: "versioned OCI tag already exists: 0.6.2-debian-trixie",
		},
		{
			name: "Oracle Linux version", packageTags: []string{"0.6.2-oraclelinux10"},
			want: "versioned OCI tag already exists: 0.6.2-oraclelinux10",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := newPreflightRunner(t, test.packageTags)
			if test.releaseResponse != "" {
				runner.releaseResponse = test.releaseResponse
			}
			runnerTemp := t.TempDir()
			err := ReleasePreflight(context.Background(), ReleasePreflightOptions{
				Root: t.TempDir(), RunnerTemp: runnerTemp, RefName: "v0.6.2", SHA: preflightCommit,
				Repository: "dantte-lp/gobfd", Runner: runner,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReleasePreflight() error = %v, want %q", err, test.want)
			}
			for _, name := range []string{
				"expected-release-commit.txt", "expected-release-branch.txt", "expected-release-tag-object.txt",
			} {
				path := filepath.Join(runnerTemp, name)
				data, readErr := os.ReadFile(path)
				if readErr != nil || len(data) != 0 {
					t.Errorf("failed preflight receipt %s = %q, %v; want empty", name, data, readErr)
				}
				assertExactMode(t, path, 0o644)
			}
		})
	}
}

func TestReleasePreflightRejectsMalformedGHCRPages(t *testing.T) {
	t.Parallel()

	for name, response := range map[string]string{
		"empty outer pages": `[]`,
		"null page":         `[null]`,
		"null item":         `[[null]]`,
		"missing metadata":  `[[{}]]`,
		"missing container": `[[{"metadata":{}}]]`,
		"missing tags":      `[[{"metadata":{"container":{}}}]]`,
		"null tags":         `[[{"metadata":{"container":{"tags":null}}}]]`,
		"null tag":          `[[{"metadata":{"container":{"tags":[null]}}}]]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runner := newPreflightRunner(t, nil)
			runner.packageResponse = response
			err := ReleasePreflight(context.Background(), ReleasePreflightOptions{
				Root: t.TempDir(), RunnerTemp: t.TempDir(), RefName: "v0.6.2", SHA: preflightCommit,
				Repository: "dantte-lp/gobfd", Runner: runner,
			})
			if err == nil || !strings.Contains(err.Error(), "OCI versions") {
				t.Fatalf("ReleasePreflight() error = %v, want malformed OCI versions rejection", err)
			}
		})
	}
}

func TestReleasePreflightRejectsReceiptSymlinkWithoutFollowingIt(t *testing.T) {
	t.Parallel()

	runnerTemp := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("preserve\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(runnerTemp, "expected-release-branch.txt")); err != nil {
		t.Fatal(err)
	}
	runner := newPreflightRunner(t, nil)
	err := ReleasePreflight(context.Background(), ReleasePreflightOptions{
		Root: t.TempDir(), RunnerTemp: runnerTemp, RefName: "v0.6.2", SHA: preflightCommit,
		Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil {
		t.Fatal("ReleasePreflight() error = nil, want receipt symlink rejection")
	}
	if data, readErr := os.ReadFile(external); readErr != nil || string(data) != "preserve\n" {
		t.Errorf("external receipt target = %q, %v", data, readErr)
	}
	if len(runner.calls) != 0 {
		t.Errorf("receipt symlink validation ran %d commands", len(runner.calls))
	}
}

func TestReleasePreflightRejectsMismatchedRemoteIdentity(t *testing.T) {
	t.Parallel()

	otherCommit := strings.Repeat("a", 40)
	tests := []struct {
		name   string
		mutate func(*preflightRunner, *ReleasePreflightOptions)
		want   string
	}{
		{name: "checkout", mutate: func(_ *preflightRunner, options *ReleasePreflightOptions) {
			options.SHA = otherCommit
		}, want: "checked-out commit"},
		{name: "lightweight tag", mutate: func(runner *preflightRunner, _ *ReleasePreflightOptions) {
			runner.tagRefResponse = fmt.Sprintf(
				`{"ref":"refs/tags/v0.6.2","object":{"type":"commit","sha":%q}}`, preflightCommit,
			)
		}, want: "annotated tag"},
		{name: "case alias tag ref field", mutate: func(runner *preflightRunner, _ *ReleasePreflightOptions) {
			runner.tagRefResponse = fmt.Sprintf(
				`{"ref":"refs/tags/v0.6.2","Ref":"refs/tags/v0.6.2","object":{"type":"tag","sha":%q}}`,
				preflightTagObject,
			)
		}, want: "noncanonical JSON field"},
		{name: "tag target", mutate: func(runner *preflightRunner, _ *ReleasePreflightOptions) {
			runner.tagObjectResponse = fmt.Sprintf(
				`{"sha":%q,"tag":"v0.6.2","object":{"type":"commit","sha":%q}}`,
				preflightTagObject, otherCommit,
			)
		}, want: "annotated tag does not target"},
		{name: "branch head", mutate: func(runner *preflightRunner, _ *ReleasePreflightOptions) {
			runner.branchResponse = fmt.Sprintf(
				`{"ref":"refs/heads/release/v0.6","object":{"type":"commit","sha":%q}}`, otherCommit,
			)
		}, want: "exact release/v0.6 head"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := newPreflightRunner(t, nil)
			options := ReleasePreflightOptions{
				Root: t.TempDir(), RunnerTemp: t.TempDir(), RefName: "v0.6.2", SHA: preflightCommit,
				Repository: "dantte-lp/gobfd", Runner: runner,
			}
			test.mutate(runner, &options)
			if err := ReleasePreflight(context.Background(), options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReleasePreflight() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReleaseGitHubJSONValidatorsRejectCaseAliases(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		data     string
		validate func([]byte) error
	}{
		{
			name: "git ref object",
			data: `{"ref":"refs/tags/v0.6.2","object":{"type":"tag","Type":"tag","sha":"` +
				preflightTagObject + `"}}`,
			validate: validateReleaseGitRefJSON,
		},
		{
			name: "git tag",
			data: `{"sha":"` + preflightTagObject + `","tag":"v0.6.2","Tag":"v0.6.2",` +
				`"object":{"type":"commit","sha":"` + preflightCommit + `"}}`,
			validate: validateReleaseGitTagJSON,
		},
		{
			name:     "GraphQL data",
			data:     `{"data":{"repository":{"release":null}},"Data":{"repository":{"release":null}}}`,
			validate: validateReleaseGraphQLResponseJSON,
		},
		{
			name:     "package metadata",
			data:     `[[{"metadata":{"container":{"tags":[]}},"Metadata":{"container":{"tags":[]}}}]]`,
			validate: validateReleasePackagePagesJSON,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.validate([]byte(test.data)); err == nil || !strings.Contains(err.Error(), "noncanonical JSON field") {
				t.Fatalf("validator error = %v, want noncanonical JSON field", err)
			}
		})
	}
}

type preflightRunner struct {
	t                 *testing.T
	calls             []specInvocation
	tagRefResponse    string
	tagObjectResponse string
	branchResponse    string
	releaseResponse   string
	packageResponse   string
	packageTags       []string
}

func newPreflightRunner(t *testing.T, packageTags []string) *preflightRunner {
	t.Helper()
	return &preflightRunner{
		t: t, packageTags: packageTags,
		tagRefResponse: fmt.Sprintf(
			`{"ref":"refs/tags/v0.6.2","object":{"type":"tag","sha":%q}}`, preflightTagObject,
		),
		tagObjectResponse: fmt.Sprintf(
			`{"sha":%q,"tag":"v0.6.2","object":{"type":"commit","sha":%q}}`,
			preflightTagObject, preflightCommit,
		),
		branchResponse: fmt.Sprintf(
			`{"ref":"refs/heads/release/v0.6","object":{"type":"commit","sha":%q}}`, preflightCommit,
		),
		releaseResponse: `{"data":{"repository":{"release":null}}}`,
		packageResponse: `[[{"metadata":{"container":{"tags":[]}}}]]`,
	}
}

func (r *preflightRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	r.calls = append(r.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir, env: append([]string(nil), spec.Env...),
	})
	if spec.Stdout == nil {
		return errors.New("preflight command lacks captured stdout")
	}
	var output string
	switch {
	case spec.Name == "git" && reflect.DeepEqual(spec.Args, []string{"rev-parse", "HEAD"}):
		output = preflightCommit + "\n"
	case len(spec.Args) == 2 && spec.Args[1] == "repos/dantte-lp/gobfd/git/ref/tags/v0.6.2":
		output = r.tagRefResponse
	case len(spec.Args) == 2 && spec.Args[1] == "repos/dantte-lp/gobfd/git/tags/"+preflightTagObject:
		output = r.tagObjectResponse
	case len(spec.Args) == 2 && spec.Args[1] == "repos/dantte-lp/gobfd/git/ref/heads/release/v0.6":
		output = r.branchResponse
	case len(spec.Args) > 1 && spec.Args[1] == "graphql":
		output = r.releaseResponse
	case containsArgument(spec.Args, "--paginate"):
		output = r.packageResponse
		if len(r.packageTags) > 0 {
			quoted := make([]string, 0, len(r.packageTags))
			for _, tag := range r.packageTags {
				quoted = append(quoted, fmt.Sprintf("%q", tag))
			}
			output = `[[{"metadata":{"container":{"tags":[` + strings.Join(quoted, ",") + `]}}}]]`
		}
	default:
		return fmt.Errorf("unexpected preflight command: %s %q", spec.Name, spec.Args)
	}
	if _, err := io.WriteString(spec.Stdout, output); err != nil {
		r.t.Fatalf("write preflight response: %v", err)
	}
	return nil
}
