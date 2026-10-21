package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"runtime/trace"
	"testing"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func startFlightRecorderForTest(t *testing.T) *trace.FlightRecorder {
	t.Helper()

	fr := trace.NewFlightRecorder(trace.FlightRecorderConfig{
		MinAge: flightRecorderMinAge, MaxBytes: flightRecorderMaxBytes,
	})
	if err := fr.Start(); err != nil {
		t.Skipf("flight recorder unavailable: %v", err)
	}
	t.Cleanup(fr.Stop)
	return fr
}

func getStatus(t *testing.T, client *http.Client, url string) (int, int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, len(body)
}

// gobfd-qj0.8.2.3.2: grpc.flight_recorder serves the trace dump on the
// management server to callers with a principal only; it is off by default.
func TestFlightRecorderBehindManagementBoundary(t *testing.T) {
	// Not parallel: runtime/trace allows one active flight recorder.
	fr := startFlightRecorderForTest(t)

	serve := func(cfg config.GRPCConfig) (string, string) {
		t.Helper()
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		t.Cleanup(mgr.Close)
		srv := newGRPCServer(cfg, mgr, nil, nil, newDaemonHealthChecker(), fr, slog.New(slog.DiscardHandler))
		tcp, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen tcp: %v", err)
		}
		serveOn(t, srv, tcp)
		path := filepath.Join(t.TempDir(), "gobfd.sock")
		unixLn, err := listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: path, Mode: "0660"})
		if err != nil {
			t.Fatalf("listenManagementSocket: %v", err)
		}
		serveOn(t, srv, unixLn)
		return "http://" + tcp.Addr().String(), "unix://" + path
	}
	socketClient := func(raw string) (*http.Client, string) {
		t.Helper()
		ep, err := apiclient.ParseEndpoint(raw)
		if err != nil {
			t.Fatalf("ParseEndpoint: %v", err)
		}
		return ep.HTTPClient(), ep.BaseURL
	}

	tcpURL, socketURL := serve(config.GRPCConfig{FlightRecorder: true, UnsafeLoopbackMutations: true})
	client, base := socketClient(socketURL)
	if code, size := getStatus(t, client, base+"/debug/flightrecorder"); code != http.StatusOK || size == 0 {
		t.Fatalf("flight recorder over the socket = %d (%d bytes), want 200 with a trace", code, size)
	}
	if code, _ := getStatus(t, http.DefaultClient, tcpURL+"/debug/flightrecorder"); code != http.StatusForbidden {
		t.Fatalf("flight recorder for an anonymous loopback caller = %d, want 403", code)
	}

	_, disabledSocket := serve(config.GRPCConfig{})
	client, base = socketClient(disabledSocket)
	if code, _ := getStatus(t, client, base+"/debug/flightrecorder"); code != http.StatusNotFound {
		t.Fatalf("flight recorder with grpc.flight_recorder off = %d, want 404", code)
	}
}
