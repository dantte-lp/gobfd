package interopproject

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRFCCaptureIsPrivateAndRunOwned(t *testing.T) {
	t.Parallel()
	c, err := NewProject(t.TempDir(), "gobfd-interop-rfc", "rfc", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	c.images = &composeImageState{Owned: composeImageOwner{Run: strings.Repeat("a", 26)}}
	if prepErr := c.prepareCaptureDirectory(); prepErr != nil {
		t.Fatal(prepErr)
	}
	want := filepath.Join(c.root, "reports/e2e/rfc-compose", "run."+c.images.Owned.Run)
	if c.captureDirectory() != want {
		t.Fatalf("capture path=%s, want %s", c.captureDirectory(), want)
	}
	if valErr := c.validateCaptureDirectory(); valErr != nil {
		t.Fatal(valErr)
	}
	if !strings.Contains(strings.Join(c.commandEnvironment(), "\n"), "GOBFD_RFC_CAPTURE_DIR="+want) {
		t.Fatal("RFC capture directory absent from Compose environment")
	}
	if dupErr := c.prepareCaptureDirectory(); !errors.Is(dupErr, os.ErrExist) {
		t.Fatalf("reused capture directory: %v", dupErr)
	}
}

func TestObservabilityCapturePreservesEvidence(t *testing.T) {
	t.Parallel()
	c := Controller{
		root:   t.TempDir(),
		kind:   "observability",
		stdout: io.Discard,
		images: &composeImageState{Owned: composeImageOwner{Run: strings.Repeat("a", 26)}},
	}
	if prepErr := c.prepareCaptureDirectory(); prepErr != nil {
		t.Fatal(prepErr)
	}
	info, statErr := os.Lstat(c.captureDirectory())
	if statErr != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("capture directory is not private: %v %v", info, statErr)
	}
	path := filepath.Join(c.captureDirectory(), "bfd.pcapng")
	if writeErr := os.WriteFile(path, []byte("earlier evidence"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if dupErr := c.prepareCaptureDirectory(); !errors.Is(dupErr, os.ErrExist) {
		t.Fatalf("repeated directory creation error=%v, want ErrExist", dupErr)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "earlier evidence" {
		t.Fatalf("existing evidence changed: %q %v", data, readErr)
	}
	first := c.captureDirectory()
	c.images.Owned.Run = strings.Repeat("b", 26)
	if secondErr := c.prepareCaptureDirectory(); secondErr != nil {
		t.Fatal(secondErr)
	}
	if c.captureDirectory() == first {
		t.Fatal("different runs share capture storage")
	}
}

func TestObservabilityCaptureRejectsEscapingParent(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	if symErr := os.Symlink(outside, filepath.Join(root, "reports")); symErr != nil {
		t.Fatal(symErr)
	}
	c := Controller{
		root:   root,
		kind:   "observability",
		stdout: io.Discard,
		images: &composeImageState{Owned: composeImageOwner{Run: strings.Repeat("a", 26)}},
	}
	if prepErr := c.prepareCaptureDirectory(); prepErr == nil {
		t.Fatal("capture directory escaped its source root")
	}
	entries, dirErr := os.ReadDir(outside)
	if dirErr != nil || len(entries) != 0 {
		t.Fatalf("outside directory changed: %v %v", entries, dirErr)
	}
}

func TestObservabilityCaptureRejectsReplacedDirectory(t *testing.T) {
	t.Parallel()
	c := Controller{
		root:   t.TempDir(),
		kind:   "observability",
		stdout: io.Discard,
		images: &composeImageState{Owned: composeImageOwner{Run: strings.Repeat("a", 26)}},
	}
	if prepErr := c.prepareCaptureDirectory(); prepErr != nil {
		t.Fatal(prepErr)
	}
	if renErr := os.Rename(c.captureDirectory(), c.captureDirectory()+".original"); renErr != nil {
		t.Fatal(renErr)
	}
	if mkdirErr := os.Mkdir(c.captureDirectory(), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if valErr := c.validateCaptureDirectory(); valErr == nil {
		t.Fatal("same-owner replacement directory accepted for launch")
	}
}
