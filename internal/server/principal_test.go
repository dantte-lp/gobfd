package server_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dantte-lp/gobfd/internal/server"
)

// gobfd-330.1: a request over the management Unix socket carries the
// kernel-reported peer UID as principal; a TCP request carries none.
func TestConnContextAttachesUnixPeerPrincipal(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := server.PrincipalFromContext(r.Context())
		_, _ = fmt.Fprintf(w, "%s %t", principal, ok)
	})
	get := func(t *testing.T, network, address string) string {
		t.Helper()
		ln, err := (&net.ListenConfig{}).Listen(context.Background(), network, address)
		if err != nil {
			t.Fatalf("listen %s: %v", network, err)
		}
		srv := &http.Server{Handler: handler, ConnContext: server.ConnContext}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })

		client := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
			},
		}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET over %s: %v", network, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	want := fmt.Sprintf("uid:%d true", os.Getuid())
	if got := get(t, "unix", filepath.Join(t.TempDir(), "api.sock")); got != want {
		t.Fatalf("unix principal = %q, want %q", got, want)
	}
	if got := get(t, "tcp", "127.0.0.1:0"); got != " false" {
		t.Fatalf("tcp principal = %q, want none", got)
	}
}
