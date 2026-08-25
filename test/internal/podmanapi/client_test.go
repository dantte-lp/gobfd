package podmanapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/libpod/containers/demo/json":
			_, _ = w.Write([]byte(`{"EffectiveCaps":["CAP_NET_ADMIN","CAP_NET_RAW"]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/images/demo-image/json":
			_, _ = w.Write([]byte(`{"Id":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/images/missing-image/json":
			http.NotFound(w, r)
		case r.Method == http.MethodDelete && r.URL.Path == "/v5.0.0/images/demo-image":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/containers/json":
			_, _ = w.Write([]byte(`[{"Id":"demo-id","Names":["demo"],"Labels":{"io.podman.compose.service":"demo"}}]`))
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
	if unmarshalErr := json.Unmarshal(raw, &inspect); unmarshalErr != nil {
		t.Fatalf("Inspect JSON: %v", unmarshalErr)
	}

	capabilities, err := client.EffectiveCapabilities(context.Background(), "demo")
	if err != nil {
		t.Fatalf("EffectiveCapabilities: %v", err)
	}
	if len(capabilities) != 2 || capabilities[0] != "CAP_NET_ADMIN" || capabilities[1] != "CAP_NET_RAW" {
		t.Fatalf("EffectiveCapabilities = %v", capabilities)
	}

	exists, err := client.ImageExists(context.Background(), "demo-image")
	if err != nil || !exists {
		t.Fatalf("ImageExists(demo-image) = %t, %v", exists, err)
	}
	imageID, err := client.ImageID(context.Background(), "demo-image")
	if err != nil || imageID != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("ImageID(demo-image) = %q, %v", imageID, err)
	}
	exists, err = client.ImageExists(context.Background(), "missing-image")
	if err != nil || exists {
		t.Fatalf("ImageExists(missing-image) = %t, %v", exists, err)
	}
	if removeErr := client.RemoveImage(context.Background(), "demo-image"); removeErr != nil {
		t.Fatalf("RemoveImage: %v", removeErr)
	}

	containers, err := client.Containers(context.Background())
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if len(containers) != 1 || containers[0].ID != "demo-id" {
		t.Fatalf("Containers = %+v", containers)
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

func TestClientExecRejectsStartAndInspectFailures(t *testing.T) {
	tests := map[string]struct {
		startStatus   int
		inspectStatus int
		inspectBody   string
		want          error
		wantDecode    error
		wantTransport bool
		breakPath     string
	}{
		"start status": {startStatus: http.StatusInternalServerError, want: errExecStartStatus},
		"start transport": {
			startStatus: http.StatusOK, breakPath: "/v5.0.0/exec/exec-1/start", wantTransport: true,
		},
		"inspect status": {
			startStatus: http.StatusOK, inspectStatus: http.StatusBadGateway,
			want: errExecInspectStatus,
		},
		"inspect transport": {
			startStatus: http.StatusOK, inspectStatus: http.StatusOK,
			breakPath: "/v5.0.0/exec/exec-1/json", wantTransport: true,
		},
		"inspect JSON": {
			startStatus: http.StatusOK, inspectStatus: http.StatusOK, inspectBody: `{`, wantDecode: io.ErrUnexpectedEOF,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "podman.sock")
			server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == test.breakPath {
					connection, _, hijackErr := w.(http.Hijacker).Hijack()
					if hijackErr == nil {
						_ = connection.Close()
					}
					return
				}
				switch r.URL.Path {
				case "/v5.0.0/containers/demo/exec":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"Id":"exec-1"}`))
				case "/v5.0.0/exec/exec-1/start":
					w.WriteHeader(test.startStatus)
					if test.startStatus == http.StatusOK {
						_, _ = w.Write(appendFrame(nil, 1, "output\n"))
					}
				case "/v5.0.0/exec/exec-1/json":
					w.WriteHeader(test.inspectStatus)
					_, _ = w.Write([]byte(test.inspectBody))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewClient(socket)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			_, err = client.Exec(t.Context(), "demo", []string{"true"})
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("Exec error = %v, want %v", err, test.want)
			}
			if test.wantDecode != nil && !errors.Is(err, test.wantDecode) {
				t.Fatalf("Exec error = %v, want wrapped decode error %v", err, test.wantDecode)
			}
			if test.wantTransport {
				if _, ok := errors.AsType[*url.Error](err); !ok {
					t.Fatalf("Exec error = %v, want wrapped HTTP transport error", err)
				}
			}
		})
	}
}

func TestClientInspectRejectsEmptyAndOversizedSuccessBodies(t *testing.T) {
	for name, test := range map[string]struct {
		body string
		want error
	}{
		"empty":     {body: "", want: errEmptyResponseBody},
		"oversized": {body: strings.Repeat("x", 1<<20+1), want: errResponseBodyTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "podman.sock")
			server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(socket)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if _, err := client.Inspect(t.Context(), "demo"); !errors.Is(err, test.want) {
				t.Fatalf("Inspect error = %v, want wrapped %v", err, test.want)
			}
		})
	}
}

func TestClientVolumeExists(t *testing.T) {
	for name, test := range map[string]struct {
		status  int
		body    string
		want    bool
		wantErr error
	}{
		"present":     {status: http.StatusOK, want: true},
		"absent":      {status: http.StatusNotFound},
		"API failure": {status: http.StatusInternalServerError, body: "failed", wantErr: errVolumeInspectStatus},
		"oversized body": {
			status:  http.StatusInternalServerError,
			body:    strings.Repeat("x", maxResponseBodySize+1),
			wantErr: errResponseBodyTooLarge,
		},
	} {
		t.Run(name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "podman.sock")
			server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/volumes/prometheus-anonymous-volume" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := NewClient(socket)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			exists, err := client.VolumeExists(t.Context(), "prometheus-anonymous-volume")
			if exists != test.want {
				t.Fatalf("VolumeExists = %t, want %t", exists, test.want)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("VolumeExists error = %v, want wrapped %v", err, test.wantErr)
			}
		})
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

func TestClientContainersPreservesEncodingJSONV1Compatibility(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "podman.sock")
	server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/containers/json" {
			http.NotFound(w, r)
			return
		}
		response := append([]byte(`[{"Id":"stale","Id":"current","Names":["bad`), 0xff)
		response = append(response, []byte(`"]}]`)...)
		_, _ = w.Write(response)
	}))
	defer server.Close()

	client, err := NewClient(socket)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	containers, err := client.Containers(context.Background())
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if len(containers) != 1 || containers[0].ID != "current" {
		t.Fatalf("Containers = %+v, want duplicate-key last ID", containers)
	}
	if len(containers[0].Names) != 1 || containers[0].Names[0] != "bad\ufffd" {
		t.Fatalf("Names = %q, want replacement character", containers[0].Names)
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
