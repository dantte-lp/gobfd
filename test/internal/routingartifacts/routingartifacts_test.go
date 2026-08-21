package routingartifacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec" //nolint:depguard // The umask regression executes the current test binary with fixed arguments.
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const restrictiveUmaskChildEnvironment = "GOBFD_ROUTING_ARTIFACT_UMASK_CHILD"

func TestMergeWritesSuiteArraysAtomically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := filepath.Join(root, "base.json")
	bgp := filepath.Join(root, "bgp.json")
	output := filepath.Join(root, "containers.json")
	writeArtifactFixture(t, base, `[{"Id":"base"}]`+"\n")
	writeArtifactFixture(t, bgp, `[{"Id":"bgp"}]`+"\n")

	err := Merge(root, "containers.json", []Input{
		{Name: "interop", Path: "base.json"},
		{Name: "interop-bgp", Path: "bgp.json"},
	})
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

func TestMergeWritesExactModeUnderRestrictiveUmask(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("restrictive-umask artifact mode regression is authoritative on Linux")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current routingartifacts test binary: %v", err)
	}
	command := exec.CommandContext(
		t.Context(),
		"sh",
		"-c",
		`umask 0777; exec "$@"`,
		"--",
		executable,
		"-test.run=^TestMergeWritesExactModeUnderRestrictiveUmaskChild$",
		"-test.v",
	)
	childEnvironment := make([]string, 0, len(os.Environ())+1)
	guardPrefix := restrictiveUmaskChildEnvironment + "="
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, guardPrefix) {
			childEnvironment = append(childEnvironment, variable)
		}
	}
	childEnvironment = append(childEnvironment, guardPrefix+"1")
	command.Env = childEnvironment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run current test binary under restrictive umask: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("restrictive-umask child verified exact mode 0600")) {
		t.Fatalf("restrictive-umask child omitted success marker:\n%s", output)
	}
}

func TestMergeWritesExactModeUnderRestrictiveUmaskChild(t *testing.T) {
	if os.Getenv(restrictiveUmaskChildEnvironment) != "1" {
		t.Skip("helper runs only in the guarded restrictive-umask child process")
	}

	root := t.TempDir()
	writeArtifactFixture(t, filepath.Join(root, "input.json"), "[]\n")
	if err := Merge(root, "output.json", []Input{{Name: "interop", Path: "input.json"}}); err != nil {
		t.Fatalf("merge artifact under restrictive umask: %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "output.json"))
	if err != nil {
		t.Fatalf("lstat restrictive-umask output: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("restrictive-umask output mode = %04o, want 0600", info.Mode().Perm())
	}
	t.Log("restrictive-umask child verified exact mode 0600")
}

func TestReadLimitedInputJoinsReadAndCloseErrors(t *testing.T) {
	t.Parallel()

	input := &injectedReadCloser{readErr: io.ErrUnexpectedEOF, closeErr: os.ErrClosed}
	_, err := readLimitedInput(input, "injected.json", maxArtifactInputSize)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("joined input error = %v, want read and close causes", err)
	}
	for _, diagnostic := range []string{"read bounded input injected.json", "close input injected.json"} {
		if !strings.Contains(err.Error(), diagnostic) {
			t.Fatalf("joined input error = %v, want diagnostic %q", err, diagnostic)
		}
	}
}

type injectedReadCloser struct {
	readErr  error
	closeErr error
}

func (input *injectedReadCloser) Read([]byte) (int, error) {
	return 0, input.readErr
}

func (input *injectedReadCloser) Close() error {
	return input.closeErr
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

			err := Merge(root, "containers.json", []Input{{Name: "interop", Path: "input.json"}})
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
			err := Merge(root, "output.json", []Input{{Name: "interop", Path: "input.json"}})
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

	err := Merge(root, "output.json", []Input{{Name: "interop", Path: "input.json"}})
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

	err := Merge(root, "output.json", []Input{{Name: "interop", Path: "input.json"}})
	if err == nil || !strings.Contains(err.Error(), "output") || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("nonregular output error = %v, want output type diagnostic", err)
	}
}

func TestImageIDArtifactRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tshark-image-id")
	const imageID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	root := filepath.Dir(path)
	if err := WriteImageID(root, filepath.Base(path), imageID); err != nil {
		t.Fatalf("write image ID artifact: %v", err)
	}
	got, err := ReadImageID(root, filepath.Base(path))
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
			if _, err := ReadImageID(filepath.Dir(path), filepath.Base(path)); err == nil {
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
	if _, err := ReadImageID(root, "tshark-image-id"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("image ID symlink read error = %v, want symlink diagnostic", err)
	}
	if err := WriteImageID(root, "tshark-image-id", imageID); err == nil || !strings.Contains(err.Error(), "symlink") {
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

	_, err := readBoundedInputWithHook(root, "input.json", func() error {
		if renameErr := os.Rename(path, path+".original"); renameErr != nil {
			return renameErr
		}
		return os.Rename(replacement, path)
	})
	if err == nil || !strings.Contains(err.Error(), "changed after open") {
		t.Fatalf("replacement error = %v, want changed-after-open diagnostic", err)
	}
}

func TestBoundedInputReadsOneRootedSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactFixture(t, filepath.Join(root, "input.json"), "[]\n")
	data, err := readBoundedInputWithHook(root, "input.json", nil)
	if err != nil {
		t.Fatalf("read rooted snapshot: %v", err)
	}
	if string(data) != "[]\n" {
		t.Fatalf("rooted snapshot = %q, want exact input", data)
	}
}

func TestBoundedInputRejectsSameInodeGrowthAfterOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "input.json")
	writeArtifactFixture(t, path, "[]\n")

	_, err := readBoundedInputWithHook(root, "input.json", func() error {
		return os.Truncate(path, maxArtifactInputSize+1)
	})
	if err == nil || !strings.Contains(err.Error(), "grew beyond") {
		t.Fatalf("growth error = %v, want grew-beyond diagnostic", err)
	}
}

func TestMergeRejectsNonlocalAndSymlinkAncestorPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeArtifactFixture(t, outside, "[]\n")
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o750); err != nil {
		t.Fatalf("create real input directory: %v", err)
	}
	writeArtifactFixture(t, filepath.Join(realDirectory, "input.json"), "[]\n")
	if err := os.Symlink("real", filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "absolute", path: outside},
		{name: "traversal", path: "../outside.json"},
		{name: "symlink ancestor", path: "linked/input.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := Merge(root, "output.json", []Input{{Name: "interop", Path: test.path}})
			if err == nil {
				t.Fatalf("unsafe rooted path %q was accepted", test.path)
			}
		})
	}
}

func TestBoundedInputRejectsAncestorDirectoryReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "suite")
	if err := os.Mkdir(directory, 0o750); err != nil {
		t.Fatalf("create suite directory: %v", err)
	}
	writeArtifactFixture(t, filepath.Join(directory, "input.json"), "[]\n")

	_, err := readBoundedInputWithHook(root, "suite/input.json", func() error {
		if renameErr := os.Rename(directory, filepath.Join(root, "suite-original")); renameErr != nil {
			return renameErr
		}
		return os.Mkdir(directory, 0o750)
	})
	if err == nil || !strings.Contains(err.Error(), "ancestor") {
		t.Fatalf("directory replacement error = %v, want ancestor diagnostic", err)
	}
}

func TestAtomicOutputRejectsSymlinkAncestor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactFixture(t, filepath.Join(root, "input.json"), "[]\n")
	if err := os.Mkdir(filepath.Join(root, "real"), 0o750); err != nil {
		t.Fatalf("create real output directory: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}

	err := Merge(root, "linked/output.json", []Input{{Name: "interop", Path: "input.json"}})
	if err == nil || !strings.Contains(err.Error(), "ancestor") {
		t.Fatalf("symlink output ancestor error = %v, want ancestor diagnostic", err)
	}
}

func TestAtomicOutputRejectsAncestorAndDestinationSwap(t *testing.T) {
	t.Parallel()

	const outputContents = "safe\n"
	tests := []struct {
		name               string
		hook               func(*testing.T, string, string) func() error
		afterSnapshot      bool
		wantPublishedInOld bool
	}{
		{
			name: "ancestor directory replacement",
			hook: func(t *testing.T, root, directory string) func() error {
				t.Helper()
				return func() error {
					if err := os.Rename(directory, filepath.Join(root, "suite-original")); err != nil {
						return err
					}
					return os.Mkdir(directory, 0o750)
				}
			},
		},
		{
			name: "ancestor replacement after snapshot",
			hook: func(t *testing.T, root, directory string) func() error {
				t.Helper()
				return func() error {
					if err := os.Rename(directory, filepath.Join(root, "suite-original")); err != nil {
						return err
					}
					return os.Mkdir(directory, 0o750)
				}
			},
			afterSnapshot:      true,
			wantPublishedInOld: true,
		},
		{
			name: "destination symlink swap",
			hook: func(t *testing.T, root, _ string) func() error {
				t.Helper()
				target := filepath.Join(root, "target")
				writeArtifactFixture(t, target, "preserve\n")
				return func() error {
					return os.Symlink("../target", filepath.Join(root, "suite", "output.json"))
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			directory := filepath.Join(root, "suite")
			if err := os.Mkdir(directory, 0o750); err != nil {
				t.Fatalf("create output directory: %v", err)
			}
			hook := test.hook(t, root, directory)
			var beforeSnapshot func() error
			var beforeRename func() error
			if test.afterSnapshot {
				beforeRename = hook
			} else {
				beforeSnapshot = hook
			}
			err := writeAtomicDataWithHooks(
				root,
				"suite/output.json",
				[]byte(outputContents),
				beforeSnapshot,
				beforeRename,
			)
			if err == nil {
				t.Fatal("output path swap was accepted")
			}
			if test.wantPublishedInOld {
				published := filepath.Join(root, "suite-original", "output.json")
				info, statErr := os.Lstat(published)
				if statErr != nil || !info.Mode().IsRegular() {
					t.Fatalf("post-snapshot fixture did not reach rooted rename: info=%v error=%v", info, statErr)
				}
			}
		})
	}
}

func TestAtomicOutputSafelyReplacesDestinationSwapAfterSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "suite")
	if err := os.Mkdir(directory, 0o750); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	target := filepath.Join(root, "target")
	writeArtifactFixture(t, target, "preserve\n")

	err := writeAtomicDataWithHooks(
		root,
		"suite/output.json",
		[]byte("safe\n"),
		nil,
		func() error {
			return os.Symlink("../target", filepath.Join(directory, "output.json"))
		},
	)
	if err != nil {
		t.Fatalf("descriptor-relative rename rejected safe destination replacement: %v", err)
	}
	published, err := os.ReadFile(filepath.Join(directory, "output.json"))
	if err != nil {
		t.Fatalf("read published output: %v", err)
	}
	if string(published) != "safe\n" {
		t.Fatalf("published output = %q, want safe data", published)
	}
	preserved, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if string(preserved) != "preserve\n" {
		t.Fatalf("destination swap followed symlink target: %q", preserved)
	}
}

func writeArtifactFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write artifact fixture %s: %v", path, err)
	}
}
