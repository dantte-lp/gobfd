package commands

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

type capabilityTransportHandler struct {
	err error
}

func (h capabilityTransportHandler) ListCapabilities(
	_ context.Context, _ *bfdv1.ListCapabilitiesRequest,
) (*bfdv1.ListCapabilitiesResponse, error) {
	if h.err != nil {
		return nil, h.err
	}
	return &bfdv1.ListCapabilitiesResponse{}, nil
}

func TestCapabilitiesCommandClosesOwnedUnixConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "RPC error", err: connect.NewError(connect.CodeUnavailable, "runtime unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			oldAddr, oldFormat, oldTLS := serverAddr, outputFormat, tlsFiles
			t.Cleanup(func() {
				serverAddr, outputFormat, tlsFiles = oldAddr, oldFormat, oldTLS
			})
			//nolint:usetesting // t.TempDir paths can exceed the Unix socket limit.
			dir, err := os.MkdirTemp("/tmp", "gobfdctl-cap-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if removeErr := os.Remove(dir); removeErr != nil {
					t.Errorf("remove socket directory: %v", removeErr)
				}
			})
			socket := filepath.Join(dir, "api.sock")
			rpc := connect.NewServer()
			bfdv1connect.RegisterCapabilityServiceHandler(rpc, capabilityTransportHandler{err: tc.err})
			mux := http.NewServeMux()
			connecthttp.Mount(mux, rpc)
			srv := httptest.NewUnstartedServer(mux)
			t.Cleanup(srv.Close)
			if closeErr := srv.Listener.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			var lc net.ListenConfig
			listener, err := lc.Listen(ctx, "unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			srv.Listener = listener
			idle, closed := make(chan struct{}, 1), make(chan struct{}, 1)
			srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				switch state {
				case http.StateNew, http.StateActive, http.StateHijacked:
					return
				case http.StateIdle:
					select {
					case idle <- struct{}{}:
					default:
					}
				case http.StateClosed:
					select {
					case closed <- struct{}{}:
					default:
					}
				}
			}
			srv.Start()
			serverAddr, outputFormat, tlsFiles = "unix://"+socket, formatJSON, apiclient.TLSFiles{}
			cmd := capabilitiesCmd()
			cmd.SetContext(ctx)
			var out bytes.Buffer
			cmd.SetOut(&out)
			err = cmd.RunE(cmd, nil)
			if tc.err != nil {
				if connect.CodeOf(err) != connect.CodeUnavailable || out.Len() != 0 {
					t.Fatalf("RPC failure = %v, output = %q", err, out.String())
				}
			} else if err != nil || strings.TrimSpace(out.String()) != "[]" {
				t.Fatalf("RPC success = %v, output = %q", err, out.String())
			}
			select {
			case <-idle:
			case <-ctx.Done():
				t.Fatal("RPC connection did not become idle")
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("capabilities left its owned Unix connection open after RunE")
			}
		})
	}
}
