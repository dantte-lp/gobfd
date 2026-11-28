package gobgp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/gobgp"
)

func TestOwnershipStoreRoundTripAndDelete(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "nested", "gobgp-ownership.json")
	store, recovered, err := gobgp.OpenOwnershipStore(statePath)
	if err != nil {
		t.Fatalf("OpenOwnershipStore(missing): %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("recovered from missing file = %v, want empty", recovered)
	}

	speaker := gobgp.SpeakerIdentity{
		Target:        "unix:///node/speaker.sock",
		PeerPID:       4242,
		PeerUID:       0,
		SocketDev:     17,
		SocketIno:     991,
		BootID:        "boot-1",
		ProcStartTime: 123456,
		ASN:           65001,
		RouterID:      "198.18.42.10",
	}
	now := time.Date(2026, 11, 27, 4, 21, 16, 0, time.UTC)
	if saveErr := store.Save("198.18.42.11", gobgp.OwnershipReceipt{
		Peer:       "198.18.42.11",
		Speaker:    speaker,
		DisabledAt: now,
	}); saveErr != nil {
		t.Fatalf("Save: %v", saveErr)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("Stat(%s): %v", statePath, err)
	}
	if runtime.GOOS == "linux" && info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %#o, want 0600", info.Mode().Perm())
	}

	reopened, loaded, err := gobgp.OpenOwnershipStore(statePath)
	if err != nil {
		t.Fatalf("OpenOwnershipStore(existing): %v", err)
	}
	got, ok := loaded["198.18.42.11"]
	if !ok {
		t.Fatalf("loaded receipts = %v, missing 198.18.42.11", loaded)
	}
	if !got.Speaker.Matches(speaker) || !got.DisabledAt.Equal(now) {
		t.Fatalf("loaded receipt = %+v, want speaker %+v at %s", got, speaker, now)
	}

	if delErr := reopened.Delete("198.18.42.11"); delErr != nil {
		t.Fatalf("Delete: %v", delErr)
	}
	_, afterDelete, err := gobgp.OpenOwnershipStore(statePath)
	if err != nil {
		t.Fatalf("OpenOwnershipStore(after delete): %v", err)
	}
	if len(afterDelete) != 0 {
		t.Fatalf("receipts after delete = %v, want empty", afterDelete)
	}
}

func TestOwnershipStoreFailsClosedOnCorruptAndUntrustedInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "malformed json", payload: []byte(`{"version":1,"receipts":[`)},
		{name: "unknown field", payload: []byte(`{"version":1,"unexpected":true,"receipts":{}}`)},
		{name: "unsupported version", payload: []byte(`{"version":99,"receipts":{}}`)},
		// Go 1.27 JSON v1-over-v2 compatibility cases: duplicate object members and invalid UTF-8.
		{
			name:    "duplicate json member",
			payload: []byte(`{"version":1,"version":1,"receipts":{}}`),
		},
		{
			name:    "invalid utf8 in json string",
			payload: append([]byte(`{"version":1,"receipts":{"192.0.2.1":{"peer":"`), 0xff, '"', '}', '}', '}'),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			statePath := filepath.Join(t.TempDir(), "gobgp-ownership.json")
			if err := os.WriteFile(statePath, tc.payload, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, _, err := gobgp.OpenOwnershipStore(statePath); err == nil {
				t.Fatalf("OpenOwnershipStore(%s) succeeded, want fail-closed error", tc.name)
			}
		})
	}

	if runtime.GOOS == "linux" {
		t.Run("world writable file rejected", func(t *testing.T) {
			t.Parallel()

			statePath := filepath.Join(t.TempDir(), "gobgp-ownership.json")
			if err := os.WriteFile(statePath, []byte(`{"version":1,"receipts":{}}`), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if err := os.Chmod(statePath, 0o666); err != nil {
				t.Fatalf("Chmod: %v", err)
			}
			if _, _, err := gobgp.OpenOwnershipStore(statePath); err == nil {
				t.Fatal("OpenOwnershipStore accepted world-writable file")
			}
		})

		t.Run("symlink file rejected", func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			target := filepath.Join(dir, "real.json")
			if err := os.WriteFile(target, []byte(`{"version":1,"receipts":{}}`), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			link := filepath.Join(dir, "gobgp-ownership.json")
			if err := os.Symlink(target, link); err != nil {
				t.Fatalf("Symlink: %v", err)
			}
			if _, _, err := gobgp.OpenOwnershipStore(link); err == nil {
				t.Fatal("OpenOwnershipStore accepted symlink state file")
			}
		})
	}
}
