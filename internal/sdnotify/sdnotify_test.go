package sdnotify_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/sdnotify"
)

// createTestSocket creates a temporary Unix datagram socket for testing
// and returns its path and connection. The socket is automatically
// cleaned up when the test finishes.
func createTestSocket(t *testing.T) (string, *net.UnixConn) {
	t.Helper()

	sockPath := filepath.Join(t.TempDir(), "notify.sock")
	addr := &net.UnixAddr{Name: sockPath, Net: "unixgram"}

	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("listen on test socket: %v", err)
	}

	t.Cleanup(func() { conn.Close() })

	return sockPath, conn
}

func TestNotify(t *testing.T) {
	tests := []struct {
		name  string
		state string
	}{
		{"Ready", sdnotify.Ready},
		{"Stopping", sdnotify.Stopping},
		{"Watchdog", sdnotify.Watchdog},
		{"Custom", "STATUS=running"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sockPath, conn := createTestSocket(t)
			t.Setenv("NOTIFY_SOCKET", sockPath)

			sent, err := sdnotify.Notify(tt.state)
			if err != nil {
				t.Fatalf("Notify(%q) error: %v", tt.state, err)
			}
			if !sent {
				t.Fatalf("Notify(%q) sent=false, want true", tt.state)
			}

			buf := make([]byte, 256)
			if dlErr := conn.SetReadDeadline(time.Now().Add(time.Second)); dlErr != nil {
				t.Fatalf("set read deadline: %v", dlErr)
			}

			n, err := conn.Read(buf)
			if err != nil {
				t.Fatalf("read from socket: %v", err)
			}

			if got := string(buf[:n]); got != tt.state {
				t.Errorf("received %q, want %q", got, tt.state)
			}
		})
	}
}

func TestNotifyNoSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	sent, err := sdnotify.Notify(sdnotify.Ready)
	if err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	if sent {
		t.Error("Notify sent=true without NOTIFY_SOCKET, want false")
	}
}

func TestWatchdogEnabled(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))

	interval, err := sdnotify.WatchdogEnabled()
	if err != nil {
		t.Fatalf("WatchdogEnabled error: %v", err)
	}

	want := 30 * time.Second
	if interval != want {
		t.Errorf("WatchdogEnabled = %v, want %v", interval, want)
	}
}

func TestWatchdogEnabledNoPID(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "1000000")
	t.Setenv("WATCHDOG_PID", "")

	interval, err := sdnotify.WatchdogEnabled()
	if err != nil {
		t.Fatalf("WatchdogEnabled error: %v", err)
	}

	want := time.Second
	if interval != want {
		t.Errorf("WatchdogEnabled = %v, want %v", interval, want)
	}
}

func TestWatchdogEnabledUnset(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")

	interval, err := sdnotify.WatchdogEnabled()
	if err != nil {
		t.Fatalf("WatchdogEnabled error: %v", err)
	}

	if interval != 0 {
		t.Errorf("WatchdogEnabled = %v, want 0 (disabled)", interval)
	}
}

func TestWatchdogEnabledWrongPID(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "1000000")
	// Use PID 1 (init) — guaranteed to differ from test process.
	t.Setenv("WATCHDOG_PID", "1")

	interval, err := sdnotify.WatchdogEnabled()
	if err != nil {
		t.Fatalf("WatchdogEnabled error: %v", err)
	}

	if interval != 0 {
		t.Errorf("WatchdogEnabled = %v, want 0 (wrong PID)", interval)
	}
}

func TestWatchdogEnabledInvalidUSEC(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "not-a-number")

	_, err := sdnotify.WatchdogEnabled()
	if err == nil {
		t.Fatal("WatchdogEnabled expected error for invalid WATCHDOG_USEC, got nil")
	}
}

func TestWatchdogEnabledInvalidPID(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "1000000")
	t.Setenv("WATCHDOG_PID", "not-a-pid")

	_, err := sdnotify.WatchdogEnabled()
	if err == nil {
		t.Fatal("WatchdogEnabled expected error for invalid WATCHDOG_PID, got nil")
	}
}
