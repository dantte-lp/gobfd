package cirunner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUploadReleaseAssetsUsesExactValidatedMatrix(t *testing.T) {
	t.Parallel()

	root, runnerTemp := t.TempDir(), t.TempDir()
	dist := filepath.Join(root, "dist")
	if err := os.Mkdir(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	names := expectedChecksummedArtifactNames("0.6.5")
	checksums := make([]byte, 0, len(names)*128)
	for _, name := range names {
		data := []byte(name)
		if err := os.WriteFile(filepath.Join(dist, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		checksums = append(checksums, formatReleaseSHA256Line(data, name)...)
	}
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), checksums, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(runnerTemp, "expected-checksummed-assets.txt"), renderArtifactNames(names), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	runner := &recordingSpecRunner{}
	if err := UploadReleaseAssets(context.Background(), UploadReleaseAssetsOptions{
		ArtifactRoot: root, RunnerTemp: runnerTemp, RefName: "v0.6.5", Runner: runner,
	}); err != nil {
		t.Fatalf("UploadReleaseAssets() error = %v", err)
	}
	want := make([]string, 0, len(names)+4)
	want = append(want, ghReleaseCommand, "upload", "v0.6.5", filepath.Join(dist, "checksums.txt"))
	for _, name := range names {
		want = append(want, filepath.Join(dist, name))
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "gh" || !slices.Equal(runner.calls[0].args, want) {
		t.Fatalf("release upload calls = %#v, want gh %#v", runner.calls, want)
	}
}
