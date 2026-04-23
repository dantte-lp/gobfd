package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"testing"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

const (
	testPeerAddr   = "10.0.0.1"
	testPrefixAddr = "198.51.100.1/32"
)

// captureStdout redirects os.Stdout to a pipe, calls fn, then returns what was
// written. This helper is NOT safe for use with t.Parallel() because it mutates
// the global os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	os.Stdout = old

	return buf.String()
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ---------------------------------------------------------------------------
// handleStateChange tests
// ---------------------------------------------------------------------------

func TestHandleStateChange_UpNotAnnounced(t *testing.T) {
	// UP + not yet announced -> should announce route, return true.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	var result bool
	output := captureStdout(t, func() {
		result = handleStateChange(
			bfdv1.SessionState_SESSION_STATE_UP,
			false, // not announced
			peer, prefix, logger,
		)
	})

	if !result {
		t.Error("handleStateChange(UP, announced=false) returned false, want true")
	}
	want := "announce route 198.51.100.1/32 next-hop self\n"
	if output != want {
		t.Errorf("stdout = %q, want %q", output, want)
	}
}

func TestHandleStateChange_UpAlreadyAnnounced(t *testing.T) {
	// UP + already announced -> idempotent, no output, return true.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	var result bool
	output := captureStdout(t, func() {
		result = handleStateChange(
			bfdv1.SessionState_SESSION_STATE_UP,
			true, // already announced
			peer, prefix, logger,
		)
	})

	if !result {
		t.Error("handleStateChange(UP, announced=true) returned false, want true")
	}
	if output != "" {
		t.Errorf("stdout = %q, want empty (idempotent)", output)
	}
}

func TestHandleStateChange_DownAnnounced(t *testing.T) {
	// DOWN + announced -> should withdraw route, return false.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	var result bool
	output := captureStdout(t, func() {
		result = handleStateChange(
			bfdv1.SessionState_SESSION_STATE_DOWN,
			true, // was announced
			peer, prefix, logger,
		)
	})

	if result {
		t.Error("handleStateChange(DOWN, announced=true) returned true, want false")
	}
	want := "withdraw route 198.51.100.1/32 next-hop self\n"
	if output != want {
		t.Errorf("stdout = %q, want %q", output, want)
	}
}

func TestHandleStateChange_DownNotAnnounced(t *testing.T) {
	// DOWN + not announced -> idempotent, no output, return false.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	var result bool
	output := captureStdout(t, func() {
		result = handleStateChange(
			bfdv1.SessionState_SESSION_STATE_DOWN,
			false, // not announced
			peer, prefix, logger,
		)
	})

	if result {
		t.Error("handleStateChange(DOWN, announced=false) returned true, want false")
	}
	if output != "" {
		t.Errorf("stdout = %q, want empty (idempotent)", output)
	}
}

func TestHandleStateChange_InitState(t *testing.T) {
	// INIT state -> transient, announced unchanged, no output.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	tests := []struct {
		name      string
		announced bool
	}{
		{name: "init while announced", announced: true},
		{name: "init while not announced", announced: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result bool
			output := captureStdout(t, func() {
				result = handleStateChange(
					bfdv1.SessionState_SESSION_STATE_INIT,
					tt.announced,
					peer, prefix, logger,
				)
			})

			if result != tt.announced {
				t.Errorf("handleStateChange(INIT, announced=%v) = %v, want %v",
					tt.announced, result, tt.announced)
			}
			if output != "" {
				t.Errorf("stdout = %q, want empty for INIT state", output)
			}
		})
	}
}

func TestHandleStateChange_AdminDownAnnounced(t *testing.T) {
	// ADMIN_DOWN + announced -> should withdraw route, return false.
	// The source code treats ADMIN_DOWN the same as DOWN.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	var result bool
	output := captureStdout(t, func() {
		result = handleStateChange(
			bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN,
			true, // was announced
			peer, prefix, logger,
		)
	})

	if result {
		t.Error("handleStateChange(ADMIN_DOWN, announced=true) returned true, want false")
	}
	want := "withdraw route 198.51.100.1/32 next-hop self\n"
	if output != want {
		t.Errorf("stdout = %q, want %q", output, want)
	}
}

func TestHandleStateChange_UnspecifiedState(t *testing.T) {
	// UNSPECIFIED state -> transient, announced unchanged, no output.
	logger := newDiscardLogger()
	peer := testPeerAddr
	prefix := testPrefixAddr

	var result bool
	output := captureStdout(t, func() {
		result = handleStateChange(
			bfdv1.SessionState_SESSION_STATE_UNSPECIFIED,
			false,
			peer, prefix, logger,
		)
	})

	if result {
		t.Error("handleStateChange(UNSPECIFIED, announced=false) returned true, want false")
	}
	if output != "" {
		t.Errorf("stdout = %q, want empty for UNSPECIFIED state", output)
	}
}

// ---------------------------------------------------------------------------
// envOrDefault tests
// ---------------------------------------------------------------------------

func TestEnvOrDefault_EnvSet(t *testing.T) {
	key := "TEST_EXABGP_ENV_SET_" + t.Name()
	t.Setenv(key, "custom-value")

	got := envOrDefault(key, "fallback")
	if got != "custom-value" {
		t.Errorf("envOrDefault(%q) = %q, want %q", key, got, "custom-value")
	}
}

func TestEnvOrDefault_EnvUnset(t *testing.T) {
	key := "TEST_EXABGP_ENV_UNSET_" + t.Name()
	os.Unsetenv(key)

	got := envOrDefault(key, "fallback")
	if got != "fallback" {
		t.Errorf("envOrDefault(%q) = %q, want %q", key, got, "fallback")
	}
}
