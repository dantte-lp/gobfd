package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePRBindsExactMergedIdentityIdempotently(t *testing.T) {
	t.Parallel()
	options, runner := releasePRFixture(t)
	for range 2 {
		if err := VerifyReleasePullRequest(context.Background(), options); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(options.Root, "release-notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Release pull request: [#123](https://github.com/dantte-lp/gobfd/pull/123)."
	if strings.Count(string(data), want) != 1 || runner.calls != 4 {
		t.Fatalf("notes = %q; calls = %d", data, runner.calls)
	}
}

func TestReleasePRRejectsUnreviewedOrUnboundCandidates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*testing.T, ReleasePullRequestOptions, *releasePRRunner)
	}{
		{name: "different commit", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.pulls = strings.ReplaceAll(r.pulls, preflightCommit, preflightTagObject)
		}},
		{name: "open PR", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.pulls = strings.ReplaceAll(r.pulls, `"closed"`, `"open"`)
		}},
		{name: "wrong target", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.pulls = strings.ReplaceAll(r.pulls, `"release/v1.0"`, `"dev"`)
		}},
		{name: "foreign repository", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.pulls = strings.ReplaceAll(r.pulls, `"dantte-lp/gobfd"`, `"other/gobfd"`)
		}},
		{name: "not a release PR", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.pulls = strings.ReplaceAll(r.pulls, "release-please--branches--release/v1.0", "task/unreviewed")
		}},
		{name: "duplicate field", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.pulls = strings.ReplaceAll(r.pulls, `"number":123`, `"number":123,"number":124`)
		}},
		{name: "ambiguous PR", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			entry := strings.TrimSuffix(strings.TrimPrefix(r.pulls, "[["), "]]")
			r.pulls = "[[" + entry + "," + entry + "]]"
		}},
		{name: "missing translated change", edit: func(_ *testing.T, _ ReleasePullRequestOptions, r *releasePRRunner) {
			r.files = strings.ReplaceAll(r.files, "CHANGELOG.ru.md", "other.md")
		}},
		{name: "missing translated heading", edit: func(t *testing.T, o ReleasePullRequestOptions, _ *releasePRRunner) {
			t.Helper()
			writeReleaseVerifyFile(t, o.Root, "CHANGELOG.ru.md", []byte("# Changes\n"))
		}},
		{name: "manifest differs from tag", edit: func(t *testing.T, o ReleasePullRequestOptions, _ *releasePRRunner) {
			t.Helper()
			writeReleaseVerifyFile(t, o.Root, ".release-please-manifest.json", []byte(`{".":"0.6.4"}`))
		}},
		{name: "translation release dates differ", edit: func(t *testing.T, o ReleasePullRequestOptions, _ *releasePRRunner) {
			t.Helper()
			writeReleaseVerifyFile(t, o.Root, "CHANGELOG.ru.md", []byte("## [1.0.0-alpha.1] - 2026-10-29\n"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options, runner := releasePRFixture(t)
			test.edit(t, options, runner)
			if err := VerifyReleasePullRequest(context.Background(), options); err == nil {
				t.Fatal("accepted an unbound release")
			}
			data, err := os.ReadFile(filepath.Join(options.Root, "release-notes.md"))
			if err != nil || string(data) != "reviewed notes\n" {
				t.Fatalf("failed check changed notes: %q, %v", data, err)
			}
		})
	}
}

func releasePRFixture(t *testing.T) (ReleasePullRequestOptions, *releasePRRunner) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"CHANGELOG.md", "CHANGELOG.ru.md"} {
		writeReleaseVerifyFile(t, root, name, []byte("## [1.0.0-alpha.1] - 2026-10-30\n\n### Fixed\n\n- Fix.\n"))
	}
	writeReleaseVerifyFile(t, root, ".release-please-manifest.json", []byte(`{".":"1.0.0-alpha.1"}`))
	writeReleaseVerifyFile(t, root, "release-notes.md", []byte("reviewed notes\n"))
	runner := &releasePRRunner{
		pulls: fmt.Sprintf(`[[{"number":123,"state":"closed","merged_at":"2026-10-30T09:00:00Z",
"merge_commit_sha":%q,"html_url":"https://github.com/dantte-lp/gobfd/pull/123",
"base":{"ref":"release/v1.0","repo":{"full_name":"dantte-lp/gobfd"}},
"head":{"ref":"release-please--branches--release/v1.0"}}]]`, preflightCommit),
		files: `[[{"filename":"CHANGELOG.md","status":"modified"},
{"filename":"CHANGELOG.ru.md","status":"modified"},
{"filename":".release-please-manifest.json","status":"modified"}]]`,
	}
	return ReleasePullRequestOptions{
		Root: root, RefName: "v1.0.0-alpha.1", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	}, runner
}

type releasePRRunner struct {
	pulls, files string
	calls        int
}

func (r *releasePRRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	r.calls++
	if spec.Name != "gh" || len(spec.Args) != 6 || spec.Args[0] != "api" || spec.Args[1] != "--paginate" ||
		spec.Args[3] != "--slurp" || spec.Args[4] != "--header" || spec.Args[5] != "X-GitHub-Api-Version:2022-11-28" {
		return fmt.Errorf("unexpected release PR command: %s %q", spec.Name, spec.Args)
	}
	data := r.pulls
	if strings.Contains(spec.Args[2], "/pulls/123/files?") {
		data = r.files
	}
	if !json.Valid([]byte(data)) {
		return errors.New("invalid release PR fixture")
	}
	_, err := io.WriteString(spec.Stdout, data)
	return err
}
