package podmanapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDemuxDockerStream(t *testing.T) {
	stream := appendFrame(nil, 1, "stdout\n")
	stream = appendFrame(stream, 2, "stderr\n")

	stdout, stderr, err := DemuxDockerStream(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("DemuxDockerStream: %v", err)
	}
	if stdout != "stdout\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "stderr\n" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestClientExecLogsInspectAndLifecycle(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "podman.sock")
	seen := make(map[string]int)

	server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.Path]++
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/containers/demo/exec":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"exec-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/exec/exec-1/start":
			_, _ = w.Write(appendFrame(nil, 1, "hello\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/exec/exec-1/json":
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/containers/demo/logs":
			_, _ = w.Write(appendFrame(nil, 1, "log\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/containers/demo/json":
			_, _ = w.Write([]byte(`{"State":{"Status":"running"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/containers/demo/pause":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/containers/demo/unpause":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/containers/demo/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/containers/demo/start":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(socket)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	result, err := client.Exec(context.Background(), "demo", []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "hello\n" || result.Stderr != "" || result.ExitCode != 0 {
		t.Fatalf("Exec result = %+v", result)
	}

	logs, err := client.Logs(context.Background(), "demo", 10)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if logs != "log\n" {
		t.Fatalf("Logs = %q", logs)
	}

	raw, err := client.Inspect(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var inspect map[string]any
	if err := json.Unmarshal(raw, &inspect); err != nil {
		t.Fatalf("Inspect JSON: %v", err)
	}

	for name, fn := range map[string]func(context.Context, string) error{
		"Pause":   client.Pause,
		"Unpause": client.Unpause,
		"Stop":    client.Stop,
		"Start":   client.Start,
	} {
		if err := fn(context.Background(), "demo"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	if !client.Exists(context.Background(), "demo") {
		t.Fatal("Exists = false")
	}
	if seen["POST /v5.0.0/containers/demo/exec"] != 1 {
		t.Fatalf("exec create calls = %d", seen["POST /v5.0.0/containers/demo/exec"])
	}
}

func TestClientLogsFallsBackToPlainText(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "podman.sock")
	server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/containers/demo/logs" {
			_, _ = w.Write([]byte("plain log\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewClient(socket)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	logs, err := client.Logs(context.Background(), "demo", 10)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if logs != "plain log\n" {
		t.Fatalf("Logs = %q", logs)
	}
}

func TestNewClientFromEnvironmentUsesPodmanHostSocket(t *testing.T) {
	tmp := t.TempDir()
	socketDir := filepath.Join(tmp, "podman")
	if err := os.Mkdir(socketDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	socket := filepath.Join(socketDir, "podman.sock")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer listener.Close()

	t.Setenv("PODMAN_HOST", "unix://"+socket)
	t.Setenv("XDG_RUNTIME_DIR", tmp)

	client, err := NewClientFromEnvironment()
	if err != nil {
		t.Fatalf("NewClientFromEnvironment: %v", err)
	}
	if client.SocketPath() != socket {
		t.Fatalf("SocketPath = %q, want %q", client.SocketPath(), socket)
	}
}

func appendFrame(dst []byte, stream byte, payload string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	dst = append(dst, header...)
	return append(dst, payload...)
}

func newUnixHTTPServer(t *testing.T, socket string, handler http.Handler) *http.Server {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	return server
}
