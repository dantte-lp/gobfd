package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReleaseArtifactsWritesExactAssetMatrices(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runnerTemp := t.TempDir()
	runner := prepareReleaseArtifactCommit(t, runnerTemp)
	writeGoReleaserArtifactsFixture(t, root, validGoReleaserArtifacts())
	if err := ReleaseArtifacts(context.Background(), ReleaseArtifactsOptions{
		Root: root, RunnerTemp: runnerTemp, RefName: "v0.6.5", SHA: strings.Repeat("a", 40), Runner: runner,
	}); err != nil {
		t.Fatalf("ReleaseArtifacts() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "git" ||
		!slices.Equal(runner.calls[0].args, []string{"rev-parse", "HEAD"}) || runner.calls[0].dir != root {
		t.Errorf("release artifact commit calls = %#v", runner.calls)
	}

	wantChecksummed := strings.Join([]string{
		"gobfd_0.6.5_linux_amd64.deb",
		"gobfd_0.6.5_linux_amd64.rpm",
		"gobfd_0.6.5_linux_amd64.tar.gz",
		"gobfd_0.6.5_linux_amd64.tar.gz.sbom.json",
		"gobfd_0.6.5_linux_arm64.deb",
		"gobfd_0.6.5_linux_arm64.rpm",
		"gobfd_0.6.5_linux_arm64.tar.gz",
		"gobfd_0.6.5_linux_arm64.tar.gz.sbom.json",
	}, "\n") + "\n"
	wantRelease := "checksums.txt\ngobfd-v0.6.5-reports.tar.gz\n" + wantChecksummed +
		"release-evidence-checksums.txt\nrelease-image-digests.txt\n"
	for name, want := range map[string]string{
		"expected-checksummed-assets.txt": wantChecksummed,
		"expected-release-assets.txt":     wantRelease,
	} {
		data, err := os.ReadFile(filepath.Join(runnerTemp, name))
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", name, data, err, want)
		}
		assertExactMode(t, filepath.Join(runnerTemp, name), 0o644)
	}
}

func TestReleaseArtifactsRejectsCommitDriftBeforeManifestUse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runnerTemp := t.TempDir()
	writeGoReleaserArtifactsFixture(t, root, validGoReleaserArtifacts())
	if err := os.WriteFile(
		filepath.Join(runnerTemp, "expected-release-commit.txt"),
		[]byte(strings.Repeat("a", 40)+"\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		_, _ = spec.Stdout.Write([]byte(strings.Repeat("b", 40) + "\n"))
	}}
	if err := ReleaseArtifacts(context.Background(), ReleaseArtifactsOptions{
		Root: root, RunnerTemp: runnerTemp, RefName: "v0.6.5", SHA: strings.Repeat("a", 40), Runner: runner,
	}); err == nil {
		t.Fatal("ReleaseArtifacts() error = nil, want commit drift rejection")
	}
	for _, name := range []string{"expected-checksummed-assets.txt", "expected-release-assets.txt"} {
		if _, err := os.Lstat(filepath.Join(runnerTemp, name)); !os.IsNotExist(err) {
			t.Errorf("%s was published after commit drift: %v", name, err)
		}
	}
}

func TestReleaseArtifactsRejectsInvalidManifest(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func([]goReleaserArtifact) []goReleaserArtifact
	}{
		{name: "duplicate archive", mutate: func(items []goReleaserArtifact) []goReleaserArtifact {
			return append(items, items[0])
		}},
		{name: "missing checksum", mutate: func(items []goReleaserArtifact) []goReleaserArtifact {
			return append(items[:8], items[9:]...)
		}},
		{name: "unexpected package path", mutate: func(items []goReleaserArtifact) []goReleaserArtifact {
			items[2].Path = "dist/unexpected.deb"
			return items
		}},
		{name: "absolute path", mutate: func(items []goReleaserArtifact) []goReleaserArtifact {
			items[0].Path = "/tmp/" + filepath.Base(items[0].Path)
			return items
		}},
		{name: "traversing path", mutate: func(items []goReleaserArtifact) []goReleaserArtifact {
			items[0].Path = "../" + filepath.Base(items[0].Path)
			return items
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			runnerTemp := t.TempDir()
			runner := prepareReleaseArtifactCommit(t, runnerTemp)
			writeGoReleaserArtifactsFixture(t, root, test.mutate(validGoReleaserArtifacts()))
			if err := ReleaseArtifacts(context.Background(), ReleaseArtifactsOptions{
				Root: root, RunnerTemp: runnerTemp, RefName: "v0.6.5", SHA: strings.Repeat("a", 40), Runner: runner,
			}); err == nil {
				t.Fatal("ReleaseArtifacts() error = nil, want exact manifest rejection")
			}
			for _, name := range []string{"expected-checksummed-assets.txt", "expected-release-assets.txt"} {
				if _, err := os.Lstat(filepath.Join(runnerTemp, name)); !os.IsNotExist(err) {
					t.Errorf("%s was published after rejection: %v", name, err)
				}
			}
		})
	}
}

func TestReleaseArtifactsRejectsJSONCompatibilityHazards(t *testing.T) {
	t.Parallel()

	valid, err := json.Marshal(validGoReleaserArtifacts())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{
			name: "duplicate object member",
			data: bytes.Replace(valid, []byte(`"type":"Archive"`), []byte(`"type":"Binary","type":"Archive"`), 1),
		},
		{
			name: "case insensitive field alias",
			data: bytes.Replace(valid, []byte(`"type":"Archive"`), []byte(`"Type":"Archive"`), 1),
		},
		{
			name: "invalid UTF-8",
			data: bytes.Replace(valid, []byte(`"Binary"`), []byte{'"', 0xff, '"'}, 1),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			runnerTemp := t.TempDir()
			runner := prepareReleaseArtifactCommit(t, runnerTemp)
			writeRawGoReleaserArtifactsFixture(t, root, test.data)
			if err := ReleaseArtifacts(context.Background(), ReleaseArtifactsOptions{
				Root: root, RunnerTemp: runnerTemp, RefName: "v0.6.5", SHA: strings.Repeat("a", 40), Runner: runner,
			}); err == nil {
				t.Fatal("ReleaseArtifacts() error = nil, want JSON compatibility rejection")
			}
		})
	}
}

func validGoReleaserArtifacts() []goReleaserArtifact {
	return []goReleaserArtifact{
		{Type: "Archive", Path: "dist/gobfd_0.6.5_linux_amd64.tar.gz", GoOS: "linux", GoArch: "amd64"},
		{Type: "Archive", Path: "dist/gobfd_0.6.5_linux_arm64.tar.gz", GoOS: "linux", GoArch: "arm64"},
		{Type: "Linux Package", Path: "dist/gobfd_0.6.5_linux_amd64.deb", GoOS: "linux", GoArch: "amd64", Extra: goReleaserArtifactExtra{Format: "deb"}},
		{Type: "Linux Package", Path: "dist/gobfd_0.6.5_linux_amd64.rpm", GoOS: "linux", GoArch: "amd64", Extra: goReleaserArtifactExtra{Format: "rpm"}},
		{Type: "Linux Package", Path: "dist/gobfd_0.6.5_linux_arm64.deb", GoOS: "linux", GoArch: "arm64", Extra: goReleaserArtifactExtra{Format: "deb"}},
		{Type: "Linux Package", Path: "dist/gobfd_0.6.5_linux_arm64.rpm", GoOS: "linux", GoArch: "arm64", Extra: goReleaserArtifactExtra{Format: "rpm"}},
		{Type: "SBOM", Path: "dist/gobfd_0.6.5_linux_amd64.tar.gz.sbom.json"},
		{Type: "SBOM", Path: "dist/gobfd_0.6.5_linux_arm64.tar.gz.sbom.json"},
		{Type: "Checksum", Path: "dist/checksums.txt"},
		{Type: "Binary", Path: "dist/gobfd_linux_amd64_v1/gobfd", GoOS: "linux", GoArch: "amd64"},
	}
}

func writeGoReleaserArtifactsFixture(t *testing.T, root string, items []goReleaserArtifact) {
	t.Helper()
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	writeRawGoReleaserArtifactsFixture(t, root, data)
}

func writeRawGoReleaserArtifactsFixture(t *testing.T, root string, data []byte) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "artifacts.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func prepareReleaseArtifactCommit(t *testing.T, runnerTemp string) *recordingSpecRunner {
	t.Helper()
	commit := strings.Repeat("a", 40)
	if err := os.WriteFile(filepath.Join(runnerTemp, "expected-release-commit.txt"), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		_, _ = spec.Stdout.Write([]byte(commit + "\n"))
	}}
}
