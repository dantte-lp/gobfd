package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func newTransportAPIClient(t *testing.T, sf server.SenderFactory) bfdv1connect.BfdServiceClient {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	rpc := connect.NewServer()
	server.Register(rpc, mgr, sf, logger)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	srv := httptest.NewTestServer(t, mux)
	return bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
}

// RFC 5881 Section 6: an API single-hop session with interface_name binds
// its sender to that interface, as a YAML session does.
func TestAddSessionBindsSingleHopInterface(t *testing.T) {
	t.Parallel()
	sf := &countingSenderFactory{}
	client := newTransportAPIClient(t, sf)
	if _, err := client.AddSession(t.Context(), validAddRequest()); err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	if !slices.Equal(sf.ifNames, []string{"eth0"}) {
		t.Fatalf("sender interfaces = %q, want [eth0]", sf.ifNames)
	}
}

// A multihop API session cannot be bound to an interface (RFC 5883 Section 4.1).
func TestAddSessionRejectsMultiHopInterface(t *testing.T) {
	t.Parallel()
	sf := &countingSenderFactory{}
	client := newTransportAPIClient(t, sf)
	req := validAddRequest()
	req.Type = bfdv1.SessionType_SESSION_TYPE_MULTI_HOP
	if _, err := client.AddSession(t.Context(), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("AddSession(multihop + interface) = %v, want InvalidArgument", err)
	}
	if sf.opens != sf.closes {
		t.Fatalf("sender opens/closes = %d/%d, want equal", sf.opens, sf.closes)
	}
}

// Without interface_name, the zone of a link-local address selects the
// single-hop interface, so the API sender is bound to it (RFC 5881 Section 6).
func TestAddSessionBindsLinkLocalZoneInterface(t *testing.T) {
	t.Parallel()
	sf := &countingSenderFactory{}
	client := newTransportAPIClient(t, sf)
	req := validAddRequest()
	req.InterfaceName = ""
	req.PeerAddress, req.LocalAddress = "fe80::1", "fe80::2%eth0"
	if _, err := client.AddSession(t.Context(), req); err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	if !slices.Equal(sf.ifNames, []string{"eth0"}) {
		t.Fatalf("sender interfaces = %q, want [eth0]", sf.ifNames)
	}
}
