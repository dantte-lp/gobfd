package main

import (
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestCapabilityStatusAPIAvailableWithoutRuntime(t *testing.T) {
	t.Parallel()
	client := capabilityStatusClient(t, nil)
	response, err := client.ListCapabilities(t.Context(), new(bfdv1.ListCapabilitiesRequest))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetCapabilities()) != 5 {
		t.Fatalf("capability count %d, want5", len(response.GetCapabilities()))
	}
	for _, capability := range response.GetCapabilities() {
		if capability.GetEnablement() != bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_UNSPECIFIED ||
			capability.GetReadiness() != bfdv1.CapabilityReadiness_CAPABILITY_READINESS_UNSPECIFIED {
			t.Fatalf("missing runtime fabricated status: %v", capability)
		}
	}
	assertCapabilityDocuments(t, response.GetCapabilities())
}

func capabilityStatusClient(t *testing.T, source server.CapabilitySource) bfdv1connect.CapabilityServiceClient {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	srv := newGRPCServer(config.GRPCConfig{}, false, mgr, nil, nil, newDaemonHealthChecker(), nil, logger, source)
	ts := httptest.NewTestServer(t, srv.Handler)
	return bfdv1connect.NewCapabilityServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))
}

func TestCapabilityStatusAPIFollowsReceiverOwnership(t *testing.T) {
	t.Parallel()
	c := newCapabilityRuntimeFixture(t)
	client := capabilityStatusClient(t, c)
	configs := []netio.ListenerConfig{echoListener("192.0.2.2", "eth1")}
	if err := c.listeners.acquire(sourceClaim(sourceEcho), configs); err != nil {
		t.Fatal(err)
	}
	response, err := client.ListCapabilities(t.Context(), new(bfdv1.ListCapabilitiesRequest))
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.GetCapabilities() {
		want := bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY
		if capability.GetKind() == bfdv1.CapabilityKind_CAPABILITY_KIND_ECHO {
			want = bfdv1.CapabilityReadiness_CAPABILITY_READINESS_READY
		}
		if capability.GetReadiness() != want {
			t.Fatalf("wire receiver readiness = %v, want %v", capability, want)
		}
	}
}

func assertCapabilityDocuments(t *testing.T, capabilities []*bfdv1.CapabilityStatus) {
	t.Helper()
	root, err := os.OpenRoot("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	var canonical string
	for _, path := range []string{"README.md", "docs/en/03-configuration.md", "docs/ru/03-configuration.md"} {
		data, readErr := root.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		_, block, found := strings.Cut(string(data), "<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->")
		if !found {
			t.Fatalf("%s has no capability inventory", path)
		}
		block, _, found = strings.Cut(block, "<!-- END GOBFD PREVIEW CAPABILITIES -->")
		if !found || strings.Count(block, "\n|") != len(capabilities)+2 {
			t.Fatalf("%s has incomplete or unbounded capability inventory", path)
		}
		if canonical != "" && canonical != block {
			t.Fatalf("%s differs from the canonical capability inventory", path)
		}
		canonical = block
		for _, capability := range capabilities {
			kind := strings.ToLower(strings.TrimPrefix(capability.GetKind().String(), "CAPABILITY_KIND_"))
			row := fmt.Sprintf("| %s | %d | preview", kind, capability.GetRfc())
			if !strings.Contains(block, row) {
				t.Fatalf("%s differs from API capability %s", path, kind)
			}
		}
	}
}
