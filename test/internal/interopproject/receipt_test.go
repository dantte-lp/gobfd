package interopproject

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCreatePrivateReceiptPublishesOneCompleteRecord(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "images.json")
	const writers = 8
	var group sync.WaitGroup
	results := make(chan error, writers)
	for range writers {
		group.Go(func() {
			results <- CreatePrivateReceipt(path, []byte(strings.Repeat("complete-record\n", 1024)))
		})
	}
	group.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, os.ErrExist) {
			t.Errorf("losing writer returned %v, want existing-destination error", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful creators = %d, want exactly one", succeeded)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != strings.Repeat("complete-record\n", 1024) {
		t.Fatalf("published receipt is incomplete: size=%d err=%v", len(data), err)
	}
	info, err := os.Lstat(path)
	if err != nil || !IsPrivateReceipt(info) {
		t.Fatalf("published receipt lost private single-link identity: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary receipts remain: count=%d err=%v", len(entries), err)
	}
}

func TestCreatePrivateReceiptPreservesExistingDestination(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "images.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreatePrivateReceipt(path, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing destination accepted: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("existing destination changed: %q err=%v", data, err)
	}
}
