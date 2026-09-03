package toolbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyJQRunsFixedVersionCommand(t *testing.T) {
	t.Parallel()

	runner := &recordingOutputRunner{output: "jq-1.8.1"}
	if err := verifyJQ(context.Background(), runner); err != nil {
		t.Fatalf("verifyJQ() error = %v", err)
	}
	want := []outputInvocation{{name: "jq", arguments: []string{"--version"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("jq invocations = %#v, want %#v", runner.calls, want)
	}
}

func TestVerifyJQWrapsCommandFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("jq failed")
	err := verifyJQ(context.Background(), &recordingOutputRunner{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("verifyJQ() error = %v, want wrapped command error", err)
	}
	if !strings.Contains(err.Error(), "verify jq runtime") {
		t.Errorf("verifyJQ() error = %q, want operation context", err)
	}
}

func TestPrepareComposeInstallDirectoryRejectsSharedWritableDirectory(t *testing.T) {
	t.Parallel()

	directory := filepath.Join(t.TempDir(), "provider")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create provider directory: %v", err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatalf("set unsafe provider directory mode: %v", err)
	}
	if _, err := prepareComposeInstallDirectory(directory); !errors.Is(err, errUnsafeInstallDirectory) {
		t.Fatalf("prepareComposeInstallDirectory() error = %v, want unsafe-directory error", err)
	}
}

func TestPrepareComposeInstallDirectoryRejectsSymlinkBeforeMutation(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "provider-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create provider directory symlink: %v", err)
	}
	requested := filepath.Join(link, "provider")
	if _, err := prepareComposeInstallDirectory(requested); !errors.Is(err, errUnsafeInstallDirectory) {
		t.Fatalf("prepareComposeInstallDirectory() error = %v, want unsafe-directory error", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "provider")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink target mutation error = %v, want target to remain absent", err)
	}
}

func TestPrepareComposeInstallDirectoryUsesCleanedPath(t *testing.T) {
	t.Parallel()

	trusted := t.TempDir()
	untrusted := t.TempDir()
	untrustedChild := filepath.Join(untrusted, "child")
	if err := os.Mkdir(untrustedChild, 0o700); err != nil {
		t.Fatalf("create untrusted child: %v", err)
	}
	link := filepath.Join(trusted, "link")
	if err := os.Symlink(untrustedChild, link); err != nil {
		t.Fatalf("create hidden provider symlink: %v", err)
	}
	rawPath := link + string(filepath.Separator) + ".." + string(filepath.Separator) + "provider"
	prepared, err := prepareComposeInstallDirectory(rawPath)
	if err != nil {
		t.Fatalf("prepareComposeInstallDirectory() error = %v", err)
	}
	want := filepath.Join(trusted, "provider")
	if prepared != want {
		t.Fatalf("prepared path = %q, want %q", prepared, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("stat prepared directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(untrusted, "provider")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hidden symlink target mutation error = %v, want target to remain absent", err)
	}
}

func TestRemoveComposeTemporaryRefusesReplacement(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "provider")
	if err := os.WriteFile(path, []byte("verified"), 0o600); err != nil {
		t.Fatalf("write original provider: %v", err)
	}
	original, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat original provider: %v", statErr)
	}
	if removeErr := os.Remove(path); removeErr != nil {
		t.Fatalf("remove original provider: %v", removeErr)
	}
	if writeErr := os.WriteFile(path, []byte("replacement"), 0o600); writeErr != nil {
		t.Fatalf("write replacement provider: %v", writeErr)
	}
	root, rootErr := os.OpenRoot(directory)
	if rootErr != nil {
		t.Fatalf("open provider root: %v", rootErr)
	}
	cleanupErr := removeComposeTemporary(root, filepath.Base(path), original)
	if !errors.Is(cleanupErr, errTemporaryFileReplaced) {
		t.Fatalf("removeComposeTemporary() error = %v, want replacement error", cleanupErr)
	}
	if closeErr := root.Close(); closeErr != nil {
		t.Fatalf("close provider root: %v", closeErr)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read replacement provider: %v", readErr)
	}
	if string(contents) != "replacement" {
		t.Fatalf("replacement contents = %q, want preserved replacement", contents)
	}
}

func TestCommandFileOutputExecutesBoundDescriptor(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("descriptor execution uses Linux procfs")
	}
	executable, openErr := os.Open("/bin/echo")
	if openErr != nil {
		t.Fatalf("open echo executable: %v", openErr)
	}
	output, commandErr := commandFileOutput(context.Background(), executable, []string{"bound"}, nil)
	if closeErr := executable.Close(); closeErr != nil {
		t.Fatalf("close echo executable: %v", closeErr)
	}
	if commandErr != nil {
		t.Fatalf("commandFileOutput() error = %v", commandErr)
	}
	if output != "bound" {
		t.Fatalf("commandFileOutput() = %q, want bound", output)
	}
}

type outputInvocation struct {
	name      string
	arguments []string
}

type recordingOutputRunner struct {
	calls  []outputInvocation
	output string
	err    error
}

func (r *recordingOutputRunner) Output(
	_ context.Context,
	name string,
	arguments, _ []string,
) (string, error) {
	r.calls = append(r.calls, outputInvocation{name: name, arguments: append([]string(nil), arguments...)})
	return r.output, r.err
}
