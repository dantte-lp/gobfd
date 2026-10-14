package apiclient_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dantte-lp/gobfd/internal/apiclient"
)

// gobfd-330.1: clients accept unix:///path, http:// and https:// endpoints;
// a bare host:port keeps meaning http://.
func TestParseEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, wantBase, wantSocket string
		wantErr                  bool
	}{
		{in: "127.0.0.1:50052", wantBase: "http://127.0.0.1:50052"},
		{in: "http://127.0.0.1:50052", wantBase: "http://127.0.0.1:50052"},
		{in: "https://bfd.example:50052", wantBase: "https://bfd.example:50052"},
		{in: "unix:///run/gobfd/gobfd.sock", wantBase: "http://localhost", wantSocket: "/run/gobfd/gobfd.sock"},
		{in: "unix://run/gobfd.sock", wantErr: true},
		{in: "unix:///", wantErr: true},
		{in: "ftp://host:21", wantErr: true},
		{in: "", wantErr: true},
	} {
		ep, err := apiclient.ParseEndpoint(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseEndpoint(%q) = %+v, want error", tc.in, ep)
			}
			continue
		}
		if err != nil || ep.BaseURL != tc.wantBase || ep.SocketPath != tc.wantSocket {
			t.Errorf("ParseEndpoint(%q) = %+v, %v; want base %q socket %q", tc.in, ep, err, tc.wantBase, tc.wantSocket)
		}
	}
}

// The HTTP client of a unix:// endpoint dials the socket path.
func TestEndpointClientDialsUnixSocket(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "api.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ep, err := apiclient.ParseEndpoint("unix://" + path)
	if err != nil {
		t.Fatalf("ParseEndpoint: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ep.BaseURL+"/", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := ep.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET over the socket: %v", err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}
