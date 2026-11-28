package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strconv"
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
	for _, path := range []string{"README.md", "docs/en/03-configuration.md", "docs/ru/03-configuration.md"} {
		data, readErr := root.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := validateCapabilityDocumentInventory(string(data), capabilities); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func validateCapabilityDocumentInventory(document string, capabilities []*bfdv1.CapabilityStatus) error {
	const (
		begin      = "<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->"
		end        = "<!-- END GOBFD PREVIEW CAPABILITIES -->"
		headerRows = 2
		rowColumns = 5 // Leading/trailing pipes surround three Markdown cells.
	)
	if strings.Count(document, begin) != 1 || strings.Count(document, end) != 1 {
		return errors.New("missing or ambiguous capability inventory")
	}
	_, block, _ := strings.Cut(document, begin)
	block, _, found := strings.Cut(block, end)
	rows := strings.Split(strings.TrimSpace(block), "\n")
	if !found || len(rows) != len(capabilities)+headerRows {
		return errors.New("incomplete or unbounded capability inventory")
	}
	profiles := make(map[string][2]string, len(capabilities))
	for index, row := range rows {
		columns := strings.Split(strings.TrimSpace(row), "|")
		if len(columns) != rowColumns || columns[0] != "" || columns[rowColumns-1] != "" {
			return fmt.Errorf("malformed capability inventory row %q", row)
		}
		if index < headerRows {
			continue
		}
		kind := strings.TrimSpace(columns[1])
		if _, duplicate := profiles[kind]; duplicate {
			return fmt.Errorf("duplicate capability inventory profile %q", kind)
		}
		// RFC and maturity are API data; table headings and qualification prose are translated.
		maturity, _, _ := strings.Cut(columns[3], ";")
		profiles[kind] = [2]string{strings.TrimSpace(columns[2]), strings.TrimSpace(maturity)}
	}
	for _, capability := range capabilities {
		kind := strings.ToLower(strings.TrimPrefix(capability.GetKind().String(), "CAPABILITY_KIND_"))
		maturity := strings.ToLower(strings.TrimPrefix(capability.GetMaturity().String(), "CAPABILITY_MATURITY_"))
		want := [2]string{strconv.FormatUint(uint64(capability.GetRfc()), 10), maturity}
		if got, exists := profiles[kind]; !exists || got != want {
			return fmt.Errorf("capability %s data = %q, want API data %q", kind, got, want)
		}
	}
	return nil
}

func TestCapabilityDocumentInventoryRejectsSemanticDrift(t *testing.T) {
	t.Parallel()

	capabilities := []*bfdv1.CapabilityStatus{{
		Kind: bfdv1.CapabilityKind_CAPABILITY_KIND_ECHO, Rfc: 9747,
		Maturity: bfdv1.CapabilityMaturity_CAPABILITY_MATURITY_PREVIEW,
	}}
	const document = "<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->\n" +
		"| Profile | RFC | Maturity |\n|:---|---:|:---|\n" +
		"| echo | 9747 | preview; incomplete RFC qualification |\n" +
		"<!-- END GOBFD PREVIEW CAPABILITIES -->"
	cases := []struct {
		name     string
		text     string
		wantFail bool
	}{
		{name: "English", text: document},
		{name: "Russian", text: strings.NewReplacer(
			"Profile", "Профиль", "Maturity", "Зрелость",
			"incomplete RFC qualification", "неполная квалификация RFC").Replace(document)},
		{name: "wrong RFC", text: strings.ReplaceAll(document, "9747", "9468"), wantFail: true},
		{name: "wrong profile", text: strings.ReplaceAll(document, "echo", "geneve"), wantFail: true},
		{name: "wrong maturity", text: strings.ReplaceAll(document, "preview;", "stable;"), wantFail: true},
		{name: "duplicate profile", text: strings.ReplaceAll(document,
			"<!-- END", "| echo | 9747 | preview |\n<!-- END"), wantFail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateCapabilityDocumentInventory(tc.text, capabilities); (err != nil) != tc.wantFail {
				t.Fatalf("validateCapabilityDocumentInventory() error = %v, want failure %t", err, tc.wantFail)
			}
		})
	}
}
