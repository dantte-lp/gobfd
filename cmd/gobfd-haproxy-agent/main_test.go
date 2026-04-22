package main

import (
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

const (
	testPeerAddr     = "10.0.0.1"
	testResponseDown = "down\n"
)

// ---------------------------------------------------------------------------
// stateMap tests
// ---------------------------------------------------------------------------

func TestStateMap_SetGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		peer  string
		state bfdv1.SessionState
	}{
		{
			name:  "set up state",
			peer:  "10.0.0.1",
			state: bfdv1.SessionState_SESSION_STATE_UP,
		},
		{
			name:  "set down state",
			peer:  "10.0.0.2",
			state: bfdv1.SessionState_SESSION_STATE_DOWN,
		},
		{
			name:  "set admin down state",
			peer:  "192.168.1.1",
			state: bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
			sm.set(tt.peer, tt.state)
			got := sm.get(tt.peer)
			if got != tt.state {
				t.Errorf("get(%q) = %v, want %v", tt.peer, got, tt.state)
			}
		})
	}
}

func TestStateMap_GetUnknownPeer(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	got := sm.get("192.0.2.99")

	// Zero value of SessionState is SESSION_STATE_UNSPECIFIED (0).
	if got != bfdv1.SessionState_SESSION_STATE_UNSPECIFIED {
		t.Errorf("get(unknown) = %v, want SESSION_STATE_UNSPECIFIED", got)
	}
}

func TestStateMap_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	const goroutines = 100
	peer := testPeerAddr

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Half the goroutines write, half read — tests race safety.
	for range goroutines {
		go func() {
			defer wg.Done()
			sm.set(peer, bfdv1.SessionState_SESSION_STATE_UP)
		}()
		go func() {
			defer wg.Done()
			_ = sm.get(peer)
		}()
	}

	wg.Wait()

	// After all writers finish, state should be UP.
	got := sm.get(peer)
	if got != bfdv1.SessionState_SESSION_STATE_UP {
		t.Errorf("after concurrent set, get(%q) = %v, want SESSION_STATE_UP", peer, got)
	}
}

// ---------------------------------------------------------------------------
// handleAgentCheck tests
// ---------------------------------------------------------------------------

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestHandleAgentCheck_StateUp(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	peer := testPeerAddr
	sm.set(peer, bfdv1.SessionState_SESSION_STATE_UP)

	server, client := net.Pipe()
	defer client.Close()

	go handleAgentCheck(server, peer, sm, newDiscardLogger())

	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read from pipe: %v", err)
	}
	got := string(buf[:n])
	want := "up ready\n"
	if got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
}

func TestHandleAgentCheck_StateDown(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	peer := testPeerAddr
	sm.set(peer, bfdv1.SessionState_SESSION_STATE_DOWN)

	server, client := net.Pipe()
	defer client.Close()

	go handleAgentCheck(server, peer, sm, newDiscardLogger())

	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read from pipe: %v", err)
	}
	got := string(buf[:n])
	want := testResponseDown
	if got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
}

func TestHandleAgentCheck_NoState(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	peer := testPeerAddr
	// No state set for this peer — zero value (UNSPECIFIED) maps to "down\n".

	server, client := net.Pipe()
	defer client.Close()

	go handleAgentCheck(server, peer, sm, newDiscardLogger())

	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read from pipe: %v", err)
	}
	got := string(buf[:n])
	want := testResponseDown
	if got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
}

func TestHandleAgentCheck_AdminDown(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	peer := testPeerAddr
	sm.set(peer, bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN)

	server, client := net.Pipe()
	defer client.Close()

	go handleAgentCheck(server, peer, sm, newDiscardLogger())

	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read from pipe: %v", err)
	}
	got := string(buf[:n])
	want := testResponseDown
	if got != want {
		t.Errorf("response for ADMIN_DOWN = %q, want %q", got, want)
	}
}

func TestHandleAgentCheck_BlockedReaderDoesNotHang(t *testing.T) {
	t.Parallel()

	sm := &stateMap{m: make(map[string]bfdv1.SessionState)}
	peer := testPeerAddr
	sm.set(peer, bfdv1.SessionState_SESSION_STATE_UP)

	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		handleAgentCheck(server, peer, sm, newDiscardLogger())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		_ = client.Close()
		<-done
		t.Fatal("handleAgentCheck blocked on a client that did not read")
	}
}

// ---------------------------------------------------------------------------
// loadConfig tests
// ---------------------------------------------------------------------------

func TestLoadConfig_ValidYAML(t *testing.T) {
	t.Parallel()

	content := []byte(`gobfd_addr: "http://10.0.0.1:50052"
backends:
  - peer: "10.0.0.2"
    agent_port: 5555
  - peer: "10.0.0.3"
    agent_port: 5556
`)
	f, err := os.CreateTemp(t.TempDir(), "config-*.yml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_, err = f.Write(content)
	if err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()

	cfg, err := loadConfig(f.Name())
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.GoBFDAddr != "http://10.0.0.1:50052" {
		t.Errorf("GoBFDAddr = %q, want %q", cfg.GoBFDAddr, "http://10.0.0.1:50052")
	}
	if len(cfg.Backends) != 2 {
		t.Fatalf("len(Backends) = %d, want 2", len(cfg.Backends))
	}
	if cfg.Backends[0].Peer != "10.0.0.2" {
		t.Errorf("Backends[0].Peer = %q, want %q", cfg.Backends[0].Peer, "10.0.0.2")
	}
	if cfg.Backends[0].AgentPort != 5555 {
		t.Errorf("Backends[0].AgentPort = %d, want %d", cfg.Backends[0].AgentPort, 5555)
	}
	if cfg.Backends[1].Peer != "10.0.0.3" {
		t.Errorf("Backends[1].Peer = %q, want %q", cfg.Backends[1].Peer, "10.0.0.3")
	}
	if cfg.Backends[1].AgentPort != 5556 {
		t.Errorf("Backends[1].AgentPort = %d, want %d", cfg.Backends[1].AgentPort, 5556)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	t.Parallel()

	content := []byte("{{{{invalid yaml content not valid")
	f, err := os.CreateTemp(t.TempDir(), "bad-config-*.yml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_, err = f.Write(content)
	if err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()

	_, err = loadConfig(f.Name())
	if err == nil {
		t.Error("loadConfig() with invalid YAML: expected error, got nil")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := loadConfig("/nonexistent/path/config.yml")
	if err == nil {
		t.Error("loadConfig() with missing file: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// envOrDefault tests
// ---------------------------------------------------------------------------

func TestEnvOrDefault_EnvSet(t *testing.T) {
	key := "TEST_HAPROXY_ENV_SET_" + t.Name()
	t.Setenv(key, "custom-value")

	got := envOrDefault(key, "fallback")
	if got != "custom-value" {
		t.Errorf("envOrDefault(%q) = %q, want %q", key, got, "custom-value")
	}
}

func TestEnvOrDefault_EnvUnset(t *testing.T) {
	key := "TEST_HAPROXY_ENV_UNSET_" + t.Name()
	// Ensure the key is not set (t.Setenv is not called; os.Unsetenv for safety).
	os.Unsetenv(key)

	got := envOrDefault(key, "fallback")
	if got != "fallback" {
		t.Errorf("envOrDefault(%q) = %q, want %q", key, got, "fallback")
	}
}
