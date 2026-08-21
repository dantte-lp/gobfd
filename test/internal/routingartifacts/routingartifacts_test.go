package routingartifacts

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeWritesSuiteArraysAtomically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := filepath.Join(root, "base.json")
	bgp := filepath.Join(root, "bgp.json")
	output := filepath.Join(root, "containers.json")
	writeArtifactFixture(t, base, `[{"Id":"base"}]`+"\n")
	writeArtifactFixture(t, bgp, `[{"Id":"bgp"}]`+"\n")

	err := Merge(output, []Input{{Name: "interop", Path: base}, {Name: "interop-bgp", Path: bgp}})
	if err != nil {
		t.Fatalf("merge routing artifacts: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read merged artifact: %v", err)
	}
	var merged struct {
		Suites map[string][]map[string]string `json:"suites"`
	}
	if decodeErr := json.Unmarshal(data, &merged); decodeErr != nil {
		t.Fatalf("decode merged artifact: %v", decodeErr)
	}
	if got := merged.Suites["interop"][0]["Id"]; got != "base" {
		t.Fatalf("base container ID = %q, want base", got)
	}
	if got := merged.Suites["interop-bgp"][0]["Id"]; got != "bgp" {
		t.Fatalf("BGP container ID = %q, want bgp", got)
	}
	info, err := os.Lstat(output)
	if err != nil {
		t.Fatalf("lstat merged artifact: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("merged artifact mode = %v, want regular 0600", info.Mode())
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(root, ".containers.json.tmp-*"))
	if err != nil {
		t.Fatalf("glob temporary artifacts: %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("atomic merge retained temporary files: %v", temporaryFiles)
	}
}

func TestMergeRejectsInvalidInputWithoutReplacingOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
	}{
		{name: "object", contents: `{}`},
		{name: "null", contents: `null`},
		{name: "multiple documents", contents: "[]\n[]\n"},
		{name: "malformed", contents: `[`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			input := filepath.Join(root, "input.json")
			output := filepath.Join(root, "containers.json")
			writeArtifactFixture(t, input, test.contents)
			writeArtifactFixture(t, output, "preserve\n")

			err := Merge(output, []Input{{Name: "interop", Path: input}})
			if err == nil {
				t.Fatal("invalid JSON input was accepted")
			}
			preserved, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatalf("read preserved output: %v", readErr)
			}
			if string(preserved) != "preserve\n" {
				t.Fatalf("invalid input replaced output: %q", preserved)
			}
		})
	}
}

func TestMergeRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		setup   func(*testing.T, string)
	}{
		{
			name:    "missing",
			wantErr: "lstat input",
			setup:   func(*testing.T, string) {},
		},
		{
			name:    "symlink",
			wantErr: "is a symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := path + ".target"
				writeArtifactFixture(t, target, "[]\n")
				if err := os.Symlink(filepath.Base(target), path); err != nil {
					t.Skipf("symlink fixture unavailable: %v", err)
				}
			},
		},
		{
			name:    "nonregular",
			wantErr: "is not a regular file",
			setup: func(t *testing.T, path string) {
				t.Helper()
				var listenConfig net.ListenConfig
				listener, err := listenConfig.Listen(t.Context(), "unix", path)
				if err != nil {
					t.Skipf("Unix socket fixture unavailable: %v", err)
				}
				t.Cleanup(func() { _ = listener.Close() })
			},
		},
		{
			name:    "oversize",
			wantErr: "limit is",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, bytes.Repeat([]byte{'['}, maxArtifactInputSize+1), 0o600); err != nil {
					t.Fatalf("write oversize input: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			input := filepath.Join(root, "input.json")
			test.setup(t, input)
			err := Merge(filepath.Join(root, "output.json"), []Input{{Name: "interop", Path: input}})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("unsafe input error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestMergeRejectsUnsafeOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := filepath.Join(root, "input.json")
	writeArtifactFixture(t, input, "[]\n")
	target := filepath.Join(root, "target.json")
	writeArtifactFixture(t, target, "preserve\n")
	output := filepath.Join(root, "output.json")
	if err := os.Symlink(filepath.Base(target), output); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}

	err := Merge(output, []Input{{Name: "interop", Path: input}})
	if err == nil || !strings.Contains(err.Error(), "output") || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unsafe output error = %v, want output symlink diagnostic", err)
	}
	preserved, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read output symlink target: %v", readErr)
	}
	if string(preserved) != "preserve\n" {
		t.Fatalf("output symlink target changed: %q", preserved)
	}
}

func TestMergeRejectsNonregularOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := filepath.Join(root, "input.json")
	writeArtifactFixture(t, input, "[]\n")
	output := filepath.Join(root, "output.json")
	if err := os.Mkdir(output, 0o750); err != nil {
		t.Fatalf("create nonregular output fixture: %v", err)
	}

	err := Merge(output, []Input{{Name: "interop", Path: input}})
	if err == nil || !strings.Contains(err.Error(), "output") || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("nonregular output error = %v, want output type diagnostic", err)
	}
}

func TestImageIDArtifactRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tshark-image-id")
	const imageID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := WriteImageID(path, imageID); err != nil {
		t.Fatalf("write image ID artifact: %v", err)
	}
	got, err := ReadImageID(path)
	if err != nil {
		t.Fatalf("read image ID artifact: %v", err)
	}
	if got != imageID {
		t.Fatalf("image ID = %q, want %q", got, imageID)
	}
}

func TestImageIDArtifactRejectsUnsafeOrInvalidData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
	}{
		{name: "short", contents: "abc\n"},
		{name: "uppercase", contents: strings.Repeat("A", 64) + "\n"},
		{name: "missing newline", contents: strings.Repeat("a", 64)},
		{name: "extra line", contents: strings.Repeat("a", 64) + "\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "tshark-image-id")
			writeArtifactFixture(t, path, test.contents)
			if _, err := ReadImageID(path); err == nil {
				t.Fatal("invalid image ID artifact was accepted")
			}
		})
	}
}

func TestImageIDArtifactRejectsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	const imageID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	writeArtifactFixture(t, target, imageID+"\n")
	path := filepath.Join(root, "tshark-image-id")
	if err := os.Symlink(filepath.Base(target), path); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if _, err := ReadImageID(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("image ID symlink read error = %v, want symlink diagnostic", err)
	}
	if err := WriteImageID(path, imageID); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("image ID symlink write error = %v, want symlink diagnostic", err)
	}
}

func TestBoundedInputRejectsRegularReplacementAfterOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "input.json")
	replacement := filepath.Join(root, "replacement.json")
	writeArtifactFixture(t, path, "[]\n")
	writeArtifactFixture(t, replacement, "[]\n")

	_, err := readBoundedInputWithHook(path, maxArtifactInputSize, func() error {
		if renameErr := os.Rename(path, path+".original"); renameErr != nil {
			return renameErr
		}
		return os.Rename(replacement, path)
	})
	if err == nil || !strings.Contains(err.Error(), "changed after open") {
		t.Fatalf("replacement error = %v, want changed-after-open diagnostic", err)
	}
}

func writeArtifactFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write artifact fixture %s: %v", path, err)
	}
}
