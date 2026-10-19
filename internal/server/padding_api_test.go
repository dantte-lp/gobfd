package server_test

import (
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestAddSessionPaddedPDUSize(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		size          uint32
		authenticated bool
		invalid       bool
	}{
		{name: "disabled"},
		{name: "minimum", size: 24},
		{name: "maximum", size: 9000},
		{name: "below minimum", size: 23, invalid: true},
		{name: "above maximum", size: 9001, invalid: true},
		{name: "uint16 wrap", size: 65664, invalid: true},
		{name: "uint32 maximum", size: math.MaxUint32, invalid: true},
		{name: "authenticated disabled", authenticated: true},
		{name: "authenticated minimum", size: 26, authenticated: true},
		{name: "authenticated below minimum", size: 25, authenticated: true, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := setupTestServer(t)
			req := validAddRequest()
			req.PaddedPduSize = tt.size
			if tt.authenticated {
				req.AuthType = bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1
				req.AuthKeyId = 7
				req.AuthSecret = []byte("api-padding-test")
			}
			resp, err := client.AddSession(t.Context(), req)
			if tt.invalid {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("AddSession padded size %d: %v, want InvalidArgument", tt.size, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddSession padded size %d: %v", tt.size, err)
			}
			if got := resp.GetSession().GetPaddedPduSize(); got != tt.size {
				t.Errorf("padded PDU target = %d, want %d", got, tt.size)
			}
		})
	}
}

func TestAddSessionRejectsPaddedPDUSizeBeforeSender(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	sf := &countingSenderFactory{}
	rpc := connect.NewServer()
	server.Register(rpc, mgr, sf, logger)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	srv := httptest.NewTestServer(t, mux)
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))

	req := validAddRequest()
	req.PaddedPduSize = 65664
	if _, err := client.AddSession(t.Context(), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("AddSession wrapped target: %v, want InvalidArgument", err)
	}
	if sf.opens != 0 || sf.closes != 0 {
		t.Errorf("invalid target allocated sender: %d opens, %d closes; want 0, 0", sf.opens, sf.closes)
	}
	resp, err := client.ListSessions(t.Context(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(resp.GetSessions()); got != 0 {
		t.Errorf("sessions after invalid target = %d, want 0", got)
	}
}
