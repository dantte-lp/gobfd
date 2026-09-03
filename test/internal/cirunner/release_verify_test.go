package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	runner := newReleaseVerifyRunner(t, assets)
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin", "DOCKER_CONFIG=/docker"}
	if err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: environment, Runner: runner,
	}); err != nil {
		t.Fatalf("VerifyReleaseDraft() error = %v", err)
	}
	if len(runner.calls) != 9 {
		t.Fatalf("release draft verification command count = %d, want 9", len(runner.calls))
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
	downloadDirectory := filepath.Join(runnerTemp, releaseAssetDownloadDirectory)
	wantDownloadArgs := []string{
		"release", "download", "v0.6.2", "--repo", "dantte-lp/gobfd", "--dir", downloadDirectory,
	}
	if got := runner.calls[8]; got.name != "gh" || got.dir != root || !reflect.DeepEqual(got.args, wantDownloadArgs) {
		t.Errorf("release asset download call = %#v, want gh %q in %s", got, wantDownloadArgs, root)
	}
	for _, name := range assets {
		info, err := os.Lstat(filepath.Join(downloadDirectory, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Errorf("downloaded release asset %s info = %#v, error = %v", name, info, err)
		}
	}
	receipt, err := os.ReadFile(filepath.Join(runnerTemp, releaseAssetIdentityReceiptName))
	if err != nil {
		t.Fatalf("read release asset identity receipt: %v", err)
	}
	if !bytes.Contains(receipt, []byte(`"database_id": 1000`)) ||
		!bytes.Contains(receipt, []byte(`"state": "uploaded"`)) {
		t.Fatalf("release asset identity receipt = %s", receipt)
	}
}

func TestVerifyReleaseDraftRejectsInvalidRemoteAssetIdentity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func([]map[string]any)
		want   string
	}{
		{name: "null node id", mutate: func(assets []map[string]any) { assets[0]["id"] = nil }, want: "node ID"},
		{name: "duplicate node id", mutate: func(assets []map[string]any) {
			assets[1]["id"] = assets[0]["id"]
		}, want: "duplicate node ID"},
		{name: "noncanonical API URL", mutate: func(assets []map[string]any) {
			assets[0]["apiUrl"] = "https://example.invalid/assets/1000"
		}, want: "API URL"},
		{name: "duplicate REST id", mutate: func(assets []map[string]any) {
			assets[1]["apiUrl"] = assets[0]["apiUrl"]
		}, want: "duplicate REST"},
		{name: "size drift", mutate: func(assets []map[string]any) { assets[0]["size"] = float64(1) }, want: "size"},
		{name: "digest drift", mutate: func(assets []map[string]any) {
			assets[0]["digest"] = "sha256:" + strings.Repeat("0", 64)
		}, want: "digest"},
		{name: "state drift", mutate: func(assets []map[string]any) { assets[0]["state"] = "new" }, want: "uploaded"},
		{name: "field alias", mutate: func(assets []map[string]any) {
			assets[0]["Name"] = assets[0]["name"]
		}, want: "noncanonical JSON field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			artifactRoot := t.TempDir()
			runnerTemp := t.TempDir()
			assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
			writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
			runner := newReleaseVerifyRunner(t, assets)
			mutateReleaseDraftAssets(t, runner, test.mutate)
			err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
				Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
				RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
				Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
			})
			if err == nil || !containsError(err, test.want) {
				t.Fatalf("VerifyReleaseDraft() error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Lstat(filepath.Join(runnerTemp, releaseAssetIdentityReceiptName)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid remote asset wrote identity receipt: %v", statErr)
			}
			if _, statErr := os.Lstat(filepath.Join(runnerTemp, releaseAssetDownloadDirectory)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid remote asset retained download directory: %v", statErr)
			}
		})
	}
}

func TestVerifyReleaseDraftRejectsDownloadDirectoryCollision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	downloadDirectory := filepath.Join(runnerTemp, releaseAssetDownloadDirectory)
	if err := os.Mkdir(downloadDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(downloadDirectory, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newReleaseVerifyRunner(t, assets)
	err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
	})
	if err == nil || !containsError(err, "download directory collision") {
		t.Fatalf("VerifyReleaseDraft() error = %v, want download directory collision", err)
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Fatalf("pre-existing download directory was changed: %v", statErr)
	}
	if len(runner.calls) != 8 {
		t.Fatalf("command count before collision = %d, want 8", len(runner.calls))
	}
}

func TestVerifyReleaseDraftRejectsReceiptCollisionAndCleansOwnedDownload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	target := filepath.Join(t.TempDir(), "preserve")
	if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(runnerTemp, releaseAssetIdentityReceiptName)
	if err := os.Symlink(target, receiptPath); err != nil {
		t.Fatal(err)
	}
	runner := newReleaseVerifyRunner(t, assets)
	err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
	})
	if err == nil || !containsError(err, "release asset identity receipt") {
		t.Fatalf("VerifyReleaseDraft() error = %v, want receipt collision", err)
	}
	if _, statErr := os.Lstat(filepath.Join(runnerTemp, releaseAssetDownloadDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("receipt collision retained owned download directory: %v", statErr)
	}
	if info, statErr := os.Lstat(receiptPath); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("pre-existing receipt symlink info = %#v, error = %v", info, statErr)
	}
	if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "preserve" {
		t.Fatalf("pre-existing receipt target = %q, error = %v", data, readErr)
	}
}

func TestVerifyReleaseDraftRejectsInvalidDownloadedAssetsAndCleansOwnedDirectory(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, []string)
		want   string
	}{
		{name: "symlink", mutate: func(t *testing.T, directory string, assets []string) {
			t.Helper()
			name := filepath.Join(directory, assets[0])
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(assets[1], name); err != nil {
				t.Fatal(err)
			}
		}, want: "nonempty regular file"},
		{name: "directory", mutate: func(t *testing.T, directory string, assets []string) {
			t.Helper()
			name := filepath.Join(directory, assets[0])
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(name, 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "nonempty regular file"},
		{name: "empty", mutate: func(t *testing.T, directory string, assets []string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(directory, assets[0]), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "nonempty regular file"},
		{name: "oversized", mutate: func(t *testing.T, directory string, assets []string) {
			t.Helper()
			if err := os.Truncate(filepath.Join(directory, assets[0]), releaseArtifactLimit+1); err != nil {
				t.Fatal(err)
			}
		}, want: "bounded nonempty regular file"},
		{name: "missing", mutate: func(t *testing.T, directory string, assets []string) {
			t.Helper()
			if err := os.Remove(filepath.Join(directory, assets[0])); err != nil {
				t.Fatal(err)
			}
		}, want: "exact manifest"},
		{name: "extra", mutate: func(t *testing.T, directory string, _ []string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(directory, "unexpected"), []byte("asset"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "exact manifest"},
		{name: "checksum mismatch", mutate: func(t *testing.T, directory string, _ []string) {
			t.Helper()
			path := filepath.Join(directory, "checksums.txt")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[0] = changedHexByte(data[0])
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "SHA-256 mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			artifactRoot := t.TempDir()
			runnerTemp := t.TempDir()
			assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
			writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
			runner := newReleaseVerifyRunner(t, assets)
			runner.afterDownload = func(directory string) {
				test.mutate(t, directory, assets)
			}
			err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
				Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
				RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
				Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
			})
			if err == nil || !containsError(err, test.want) {
				t.Fatalf("VerifyReleaseDraft() error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Lstat(filepath.Join(runnerTemp, releaseAssetDownloadDirectory)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("owned invalid download directory cleanup error = %v, want not exist", statErr)
			}
		})
	}
}

func TestVerifyReleaseDraftCleansPartialDownloadAfterCommandFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	runner := newReleaseVerifyRunner(t, assets)
	runner.downloadErr = errors.New("injected download failure")
	err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
	})
	if err == nil || !containsError(err, "download exact release assets") {
		t.Fatalf("VerifyReleaseDraft() error = %v, want command failure", err)
	}
	if _, statErr := os.Lstat(filepath.Join(runnerTemp, releaseAssetDownloadDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial download directory cleanup error = %v, want not exist", statErr)
	}
}

func TestVerifyReleaseDraftRejectsReplacedDownloadRootWithoutRemovingReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	runner := newReleaseVerifyRunner(t, assets)
	downloadDirectory := filepath.Join(runnerTemp, releaseAssetDownloadDirectory)
	runner.afterDownload = func(string) {
		if err := os.Rename(downloadDirectory, downloadDirectory+"-owned"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(downloadDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(downloadDirectory, "sentinel"), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
	})
	if err == nil || !containsError(err, "download directory ownership changed") {
		t.Fatalf("VerifyReleaseDraft() error = %v, want download root replacement rejection", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(downloadDirectory, "sentinel")); readErr != nil || string(data) != "preserve" {
		t.Fatalf("replacement sentinel = %q, error = %v", data, readErr)
	}
}

func TestVerifyReleaseDraftRejectsVerifierRootReplacedDuringDownload(t *testing.T) {
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
	runner := newReleaseVerifyRunner(t, assets)
	runner.afterDownload = func(string) {
		if err := os.Rename(root, root+"-owned"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := VerifyReleaseDraft(context.Background(), VerifyReleaseDraftOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: []string{"GH_TOKEN=secret", "PATH=/usr/bin"}, Runner: runner,
	})
	if err == nil || !containsError(err, "release verifier root after release asset download") {
		t.Fatalf("VerifyReleaseDraft() error = %v, want verifier root replacement rejection", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(root, "sentinel")); readErr != nil || string(data) != "preserve" {
		t.Fatalf("replacement verifier sentinel = %q, error = %v", data, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(runnerTemp, releaseAssetDownloadDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("download directory after verifier replacement = %v, want not exist", statErr)
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
	runner := newReleaseVerifyRunner(t, assets)
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
			t.Helper()
			writeReleaseVerifyFile(t, runnerTemp, "expected-release-tag-object.txt", []byte(preflightCommit+"\n"))
		}, want: "tag object receipt"},
		{name: "OCI digest receipt", mutate: func(t *testing.T, artifactRoot, _ string, _ *releaseVerifyRunner) {
			t.Helper()
			changed := strings.Replace(
				string(validReleaseDigestReceipt("0.6.2")), testOCIDigest("1"), testOCIDigest("9"), 2,
			)
			writeReleaseVerifyFile(t, artifactRoot, "release-image-digests.txt", []byte(changed))
		}, want: "OCI digest receipt changed"},
		{name: "draft body", mutate: func(t *testing.T, _ string, _ string, runner *releaseVerifyRunner) {
			t.Helper()
			runner.releaseView = marshalReleaseDraftView(t, "other notes", runner.assets)
		}, want: "draft body"},
		{name: "duplicate asset", mutate: func(t *testing.T, _ string, _ string, runner *releaseVerifyRunner) {
			t.Helper()
			assets := append([]string(nil), runner.assets...)
			assets[len(assets)-1] = assets[0]
			runner.releaseView = marshalReleaseDraftView(t, "release notes", assets)
		}, want: "draft asset set"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			artifactRoot := t.TempDir()
			runnerTemp := t.TempDir()
			assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
			writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
			runner := newReleaseVerifyRunner(t, assets)
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
		"expected-checksummed-assets.txt": renderArtifactNames(expectedChecksummedArtifactNames("0.6.2")),
		"expected-release-assets.txt":     renderArtifactNames(assets),
	} {
		writeReleaseVerifyFile(t, runnerTemp, name, data)
	}
	writeReleaseVerifyFile(t, artifactRoot, "release-notes.md", []byte("release notes\n"))
	digests := validReleaseDigestReceipt("0.6.2")
	report := validReleaseReportsArchive(t)
	checksums := append(
		formatReleaseSHA256Line(report, "gobfd-v0.6.2-reports.tar.gz"),
		formatReleaseSHA256Line(digests, "release-image-digests.txt")...,
	)
	writeReleaseVerifyFile(t, artifactRoot, "release-image-digests.txt", digests)
	writeReleaseVerifyFile(t, artifactRoot, "release-evidence-checksums.txt", checksums)
}

func writeReleaseVerifyFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type releaseVerifyRunner struct {
	t             *testing.T
	assets        []string
	manifest      int
	releaseView   []byte
	calls         []specInvocation
	afterRun      func(CommandSpec)
	afterDownload func(string)
	downloadErr   error
}

func newReleaseVerifyRunner(t *testing.T, assets []string) *releaseVerifyRunner {
	t.Helper()
	return &releaseVerifyRunner{
		t: t, assets: append([]string(nil), assets...),
		releaseView: marshalReleaseDraftView(t, "release notes", assets),
	}
}

func (runner *releaseVerifyRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir, env: append([]string(nil), spec.Env...),
	})
	if spec.Name == "gh" && len(spec.Args) == 7 && slices.Equal(spec.Args[:5], []string{
		"release", "download", "v0.6.2", "--repo", "dantte-lp/gobfd",
	}) && spec.Args[5] == "--dir" {
		writeValidDownloadedReleaseAssets(runner.t, spec.Args[6])
		if runner.afterDownload != nil {
			runner.afterDownload(spec.Args[6])
		}
		return runner.downloadErr
	}
	if spec.Stdout == nil {
		return errors.New("release verification command lacks captured stdout")
	}
	var data []byte
	switch {
	case spec.Name == "git" && reflect.DeepEqual(spec.Args, []string{"rev-parse", "HEAD"}):
		data = []byte(preflightCommit + "\n")
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/tags/v0.6.2"}):
		data = fmt.Appendf(nil, `{"ref":"refs/tags/v0.6.2","object":{"type":"tag","sha":%q}}`, preflightTagObject)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/tags/" + preflightTagObject}):
		data = fmt.Appendf(nil,
			`{"sha":%q,"tag":"v0.6.2","object":{"type":"commit","sha":%q}}`, preflightTagObject, preflightCommit,
		)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/heads/release/v0.6"}):
		data = fmt.Appendf(nil, `{"ref":"refs/heads/release/v0.6","object":{"type":"commit","sha":%q}}`, preflightCommit)
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

func marshalReleaseDraftView(t *testing.T, body string, assets []string) []byte {
	t.Helper()
	items := make([]map[string]any, 0, len(assets))
	dataByName := validReleaseAssetData(t)
	for index, name := range assets {
		digest := sha256.Sum256(dataByName[name])
		items = append(items, map[string]any{
			"apiUrl": fmt.Sprintf("https://api.github.com/repos/dantte-lp/gobfd/releases/assets/%d", 1000+index),
			"digest": fmt.Sprintf("sha256:%x", digest),
			"id":     fmt.Sprintf("RA_test_%02d", index),
			"name":   name,
			"size":   len(dataByName[name]),
			"state":  "uploaded",
		})
	}
	data, err := json.Marshal(map[string]any{
		"isDraft": true, "tagName": "v0.6.2", "body": body, "assets": items,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mutateReleaseDraftAssets(t *testing.T, runner *releaseVerifyRunner, mutate func([]map[string]any)) {
	t.Helper()
	var document struct {
		Assets []map[string]any `json:"assets"`
	}
	if err := json.Unmarshal(runner.releaseView, &document); err != nil {
		t.Fatal(err)
	}
	mutate(document.Assets)
	var root map[string]any
	if err := json.Unmarshal(runner.releaseView, &root); err != nil {
		t.Fatal(err)
	}
	root["assets"] = document.Assets
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	runner.releaseView = data
}

func containsError(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
