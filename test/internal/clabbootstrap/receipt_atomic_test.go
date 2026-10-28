package clabbootstrap

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLifecycleReceiptReplacesInodeWithoutTruncatingOldRecord(t *testing.T) {
	t.Parallel()
	root, original := atomicReceiptFixture(t)
	old, err := os.Open(receiptPath(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := old.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	before, err := old.Stat()
	if err != nil {
		t.Fatal(err)
	}
	updated := lifecycleReceipt{
		SchemaVersion: 1,
		RunID:         "updated-run",
		Topology:      filepath.Join(root, "owned.clab.yml"),
		Containers:    map[string]string{gobfdContainer: "updated-container"},
	}
	if writeErr := writeLifecycleReceipt(root, updated); writeErr != nil {
		t.Fatalf("update receipt: %v", writeErr)
	}
	retained, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained, original) {
		t.Errorf("old open ownership record changed: got %q, want %q", retained, original)
	}
	after, err := os.Stat(receiptPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) || after.Mode().Perm() != 0o600 {
		t.Errorf("replacement must have a new inode and mode 0600: same=%t mode=%v",
			os.SameFile(before, after), after.Mode())
	}
	loaded, err := loadLifecycleReceipt(root)
	if err != nil {
		t.Fatalf("parse updated receipt: %v", err)
	}
	if loaded.RunID != "updated-run" || loaded.Containers[gobfdContainer] != "updated-container" {
		t.Errorf("stored receipt lost ownership identities: %#v", loaded)
	}
	entries, err := os.ReadDir(runtimeDirectory(root))
	if err != nil || len(entries) != 1 {
		t.Errorf("temporary receipt remains after update: entries=%v err=%v", entries, err)
	}
}

func TestWriteLifecycleReceiptRejectsUnsafeDestinationWithoutChangingRecord(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"parent-mode", "parent-symlink", "file-mode", "file-symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root, original := atomicReceiptFixture(t)
			path := receiptPath(root)
			retained := path
			switch kind {
			case "parent-mode":
				if err := os.Chmod(runtimeDirectory(root), 0o770); err != nil {
					t.Fatal(err)
				}
			case "file-mode":
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				directory := runtimeDirectory(root)
				target := directory + "-foreign"
				if err := os.Rename(directory, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, directory); err != nil {
					t.Fatal(err)
				}
				retained = filepath.Join(target, "lifecycle.json")
			case "file-symlink":
				retained = path + ".foreign"
				if err := os.Rename(path, retained); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(retained, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				retained = path + ".foreign"
				if err := os.Link(path, retained); err != nil {
					t.Fatal(err)
				}
			}
			err := writeLifecycleReceipt(root, lifecycleReceipt{RunID: "must-not-publish"})
			if !errors.Is(err, errLifecycleState) {
				t.Errorf("unsafe receipt accepted: %v", err)
			}
			data, err := os.ReadFile(retained)
			if err != nil || !bytes.Equal(data, original) {
				t.Errorf("failed update changed retained ownership data: data=%q err=%v", data, err)
			}
		})
	}
}

func atomicReceiptFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(runtimeDirectory(root), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"schema_version\":1,\"run_id\":\"original-run\"," +
		"\"containers\":{\"owned\":\"original-container\"}}\n")
	if err := os.WriteFile(receiptPath(root), original, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, original
}
