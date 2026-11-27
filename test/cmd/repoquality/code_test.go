package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadCodeArtifactBoundsAndRejectsSymlinks(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	regular := filepath.Join(directory, "manifest.bin")
	want := "cmd/gobfd/main.go\x00"
	if err := os.WriteFile(regular, []byte(want), 0o600); err != nil {
		t.Fatalf("write regular artifact: %v", err)
	}

	got, err := readCodeArtifact(regular)
	if err != nil {
		t.Fatalf("read regular artifact: %v", err)
	}
	if string(got) != want {
		t.Fatalf("artifact content = %q, want %q", string(got), want)
	}

	symlinkPath := filepath.Join(directory, "symlink.bin")
	if err := os.Symlink(regular, symlinkPath); err != nil {
		t.Fatalf("create symlink artifact: %v", err)
	}
	if _, err := readCodeArtifact(symlinkPath); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("symlink artifact err = %v, want os.ErrInvalid", err)
	}

	for _, invalid := range []string{"", ".", "..", directory + "/"} {
		if _, err := readCodeArtifact(invalid); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("invalid path %q err = %v, want os.ErrInvalid", invalid, err)
		}
	}
}
