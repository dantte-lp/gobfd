package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestVerifyReleaseDraftRevalidatesExactIdentityAndManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	runner := newReleaseVerifyRunner(t, assets, "release notes")
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin", "DOCKER_CONFIG=/docker"}
	if err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: environment, Runner: runner,
	}); err != nil {
		t.Fatalf("VerifyReleaseDraft() error = %v", err)
	}
	if len(runner.calls) != 8 {
		t.Fatalf("release draft verification command count = %d, want 8", len(runner.calls))
	}
	for index, call := range runner.calls {
		wantEnvironment := environment
		if index == 0 {
			wantEnvironment = []string{"PATH=/usr/bin", "DOCKER_CONFIG=/docker"}
		} else if index >= 4 && index <= 6 {
			wantEnvironment = []string{"PATH=/usr/bin", "DOCKER_CONFIG=/docker"}
		}
		if !reflect.DeepEqual(call.env, wantEnvironment) {
			t.Errorf("release draft verification call %d environment = %q, want %q", index, call.env, wantEnvironment)
		}
	}
	wantDraftArgs := []string{
		"release", "view", "v0.6.2", "--repo", "dantte-lp/gobfd", "--json", "isDraft,tagName,body,assets",
	}
	if got := runner.calls[7]; got.name != "gh" || got.dir != root || !reflect.DeepEqual(got.args, wantDraftArgs) {
		t.Errorf("release draft view call = %#v, want gh %q in %s", got, wantDraftArgs, root)
	}
}

func TestVerifyReleaseDraftRejectsReplacedVerifierRoot(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "verifier")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	runner := newReleaseVerifyRunner(t, assets, "release notes")
	runner.afterRun = func(CommandSpec) {
		if len(runner.calls) != 8 {
			return
		}
		if err := os.Rename(root, root+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
	})
	if err == nil || !containsError(err, "release verifier root after verification") {
		t.Fatalf("VerifyReleaseDraft() error = %v, want verifier root replacement rejection", err)
	}
}

func TestVerifyReleaseDraftRejectsChangedEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, string, *releaseVerifyRunner)
		want   string
	}{
		{name: "tag object receipt", mutate: func(t *testing.T, _ string, runnerTemp string, _ *releaseVerifyRunner) {
			writeReleaseVerifyFile(t, runnerTemp, "expected-release-tag-object.txt", []byte(preflightCommit+"\n"))
		}, want: "tag object receipt"},
		{name: "OCI digest receipt", mutate: func(t *testing.T, artifactRoot, _ string, _ *releaseVerifyRunner) {
			changed := strings.Replace(
				string(validReleaseDigestReceipt("0.6.2")), testOCIDigest("1"), testOCIDigest("9"), 2,
			)
			writeReleaseVerifyFile(t, artifactRoot, "release-image-digests.txt", []byte(changed))
		}, want: "OCI digest receipt changed"},
		{name: "draft body", mutate: func(t *testing.T, _ string, _ string, runner *releaseVerifyRunner) {
			runner.releaseView = marshalReleaseDraftView(t, true, "v0.6.2", "other notes", runner.assets)
		}, want: "draft body"},
		{name: "duplicate asset", mutate: func(t *testing.T, _ string, _ string, runner *releaseVerifyRunner) {
			assets := append([]string(nil), runner.assets...)
			assets[len(assets)-1] = assets[0]
			runner.releaseView = marshalReleaseDraftView(t, true, "v0.6.2", "release notes", assets)
		}, want: "draft asset set"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			artifactRoot := t.TempDir()
			runnerTemp := t.TempDir()
			assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
			writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
			runner := newReleaseVerifyRunner(t, assets, "release notes")
			test.mutate(t, artifactRoot, runnerTemp, runner)
			err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
				Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
				RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
				Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
			})
			if err == nil || !containsError(err, test.want) {
				t.Fatalf("VerifyReleaseDraft() error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeReleaseVerifyFixture(t *testing.T, artifactRoot, runnerTemp string, assets []string) {
	t.Helper()
	for name, data := range map[string][]byte{
		"expected-release-commit.txt":     []byte(preflightCommit + "\n"),
		"expected-release-branch.txt":     []byte("release/v0.6\n"),
		"expected-release-tag-object.txt": []byte(preflightTagObject + "\n"),
		"expected-release-assets.txt":     renderArtifactNames(assets),
	} {
		writeReleaseVerifyFile(t, runnerTemp, name, data)
	}
	writeReleaseVerifyFile(t, artifactRoot, "release-notes.md", []byte("release notes\n"))
	writeReleaseVerifyFile(t, artifactRoot, "release-image-digests.txt", validReleaseDigestReceipt("0.6.2"))
}

func writeReleaseVerifyFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type releaseVerifyRunner struct {
	t           *testing.T
	assets      []string
	manifest    int
	releaseView []byte
	calls       []specInvocation
	afterRun    func(CommandSpec)
}

func newReleaseVerifyRunner(t *testing.T, assets []string, body string) *releaseVerifyRunner {
	t.Helper()
	return &releaseVerifyRunner{
		t: t, assets: append([]string(nil), assets...),
		releaseView: marshalReleaseDraftView(t, true, "v0.6.2", body, assets),
	}
}

func (runner *releaseVerifyRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir, env: append([]string(nil), spec.Env...),
	})
	if spec.Stdout == nil {
		return errors.New("release verification command lacks captured stdout")
	}
	var data []byte
	switch {
	case spec.Name == "git" && reflect.DeepEqual(spec.Args, []string{"rev-parse", "HEAD"}):
		data = []byte(preflightCommit + "\n")
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/tags/v0.6.2"}):
		data = []byte(fmt.Sprintf(`{"ref":"refs/tags/v0.6.2","object":{"type":"tag","sha":%q}}`, preflightTagObject))
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/tags/" + preflightTagObject}):
		data = []byte(fmt.Sprintf(
			`{"sha":%q,"tag":"v0.6.2","object":{"type":"commit","sha":%q}}`, preflightTagObject, preflightCommit,
		))
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/heads/release/v0.6"}):
		data = []byte(fmt.Sprintf(`{"ref":"refs/heads/release/v0.6","object":{"type":"commit","sha":%q}}`, preflightCommit))
	case spec.Name == "docker" && runner.manifest < releaseOCIImageCount:
		markers := []string{"1", "1", "3"}
		var err error
		data, err = json.Marshal(validOCIManifestIndex(markers[runner.manifest]))
		if err != nil {
			return err
		}
		runner.manifest++
	case spec.Name == "gh" && len(spec.Args) > 1 && spec.Args[0] == "release" && spec.Args[1] == "view":
		data = runner.releaseView
	default:
		return fmt.Errorf("unexpected release verification command: %s %q", spec.Name, spec.Args)
	}
	_, err := io.Writer(spec.Stdout).Write(data)
	if err == nil && runner.afterRun != nil {
		runner.afterRun(spec)
	}
	return err
}

func marshalReleaseDraftView(t *testing.T, draft bool, tag, body string, assets []string) []byte {
	t.Helper()
	items := make([]map[string]string, 0, len(assets))
	for _, name := range assets {
		items = append(items, map[string]string{"name": name})
	}
	data, err := json.Marshal(map[string]any{
		"isDraft": draft, "tagName": tag, "body": body, "assets": items,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func containsError(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
