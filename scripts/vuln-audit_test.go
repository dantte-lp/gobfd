package main

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyFindingsHonorsAllowlistExpiry(t *testing.T) {
	t.Parallel()

	entries := map[string]allowEntry{
		"GO-2099-0001": {
			Package:    "example.com/module",
			Owner:      "netops",
			Expires:    "2026-04-30",
			Reason:     "waiting for upstream fix",
			Mitigation: "localhost only",
		},
	}
	findings := []finding{{ID: "GO-2099-0001", Package: "example.com/module"}}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(allowed) != 0 {
		t.Fatalf("allowed = %v, want none", allowed)
	}
	if len(unallowed) != 0 {
		t.Fatalf("unallowed = %v, want none", unallowed)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "expired on 2026-04-30") {
		t.Fatalf("failures = %v, want expiry failure", failures)
	}
}

func TestClassifyFindingsSeparatesAllowedAndUnallowed(t *testing.T) {
	t.Parallel()

	entries := map[string]allowEntry{
		"GO-2099-0001": {
			Package:    "example.com/module",
			Owner:      "netops",
			Expires:    "2026-12-31",
			Reason:     "waiting for upstream fix",
			Mitigation: "localhost only",
		},
	}
	findings := []finding{
		{Scanner: govulncheckName, ID: "GO-2099-0001", Package: "example.com/module"},
		{Scanner: osvScannerName, ID: "GO-2099-0002", Package: "example.com/other"},
	}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	if got := len(allowed["GO-2099-0001"]); got != 1 {
		t.Fatalf("allowed GO-2099-0001 count = %d, want 1", got)
	}
	if got := len(unallowed["GO-2099-0002"]); got != 1 {
		t.Fatalf("unallowed GO-2099-0002 count = %d, want 1", got)
	}
}

func TestClassifyFindingsChecksPackageAndScanner(t *testing.T) {
	t.Parallel()

	entries := map[string]allowEntry{
		"GO-2099-0001": {
			Package:    "example.com/module",
			Owner:      "netops",
			Expires:    "2026-12-31",
			Reason:     "waiting for upstream fix",
			Mitigation: "trusted network only",
		},
	}
	findings := []finding{
		{Scanner: govulncheckName, ID: "GO-2099-0001", Package: "example.com/module/api", Reachable: true},
		{Scanner: osvScannerName, ID: "GO-2099-0001", Package: "example.com/other"},
		{Scanner: "unknown", ID: "GO-2099-0001", Package: "example.com/module"},
	}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	if got := len(allowed["GO-2099-0001"]); got != 1 {
		t.Fatalf("allowed GO-2099-0001 count = %d, want 1", got)
	}
	if got := len(unallowed["GO-2099-0001"]); got != 2 {
		t.Fatalf("unallowed GO-2099-0001 count = %d, want 2", got)
	}
}

func TestClassifyFindingsRejectsReachableModuleOnlyAdvisory(t *testing.T) {
	t.Parallel()

	entries := map[string]allowEntry{
		"GO-2099-0001": {
			Package:    "example.com/module",
			Owner:      "netops",
			Expires:    "2026-12-31",
			Reason:     "affected package is outside the build graph",
			Mitigation: "reject affected package imports",
			ModuleOnly: true,
		},
	}
	findings := []finding{
		{Scanner: osvScannerName, ID: "GO-2099-0001", Package: "example.com/module"},
		{Scanner: govulncheckName, ID: "GO-2099-0001", Package: "example.com/module"},
		{
			Scanner:   govulncheckName,
			ID:        "GO-2099-0001",
			Package:   "example.com/module/affected",
			Source:    "Vulnerable",
			Reachable: true,
		},
	}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	if got := len(allowed["GO-2099-0001"]); got != 2 {
		t.Fatalf("allowed GO-2099-0001 count = %d, want 2 module-only findings", got)
	}
	if got := len(unallowed["GO-2099-0001"]); got != 1 || !unallowed["GO-2099-0001"][0].Reachable {
		t.Fatalf("unallowed GO-2099-0001 = %+v, want reachable finding", unallowed["GO-2099-0001"])
	}
}

func TestParseOSVScannerPreservesEncodingJSONV1Compatibility(t *testing.T) {
	t.Parallel()

	input := append(
		[]byte(`{"results":[{"source":{"path":"go.mod"},"packages":[{"package":{"name":"old","name":"bad`),
		0xff,
	)
	const suffix = `","version":"v1.0.0","ecosystem":"Go"},` +
		`"vulnerabilities":[{"id":"GO-old","id":"GO-new"}]}]}]}`
	input = append(input, []byte(suffix)...)
	findings, err := parseOSVScanner(input)
	if err != nil {
		t.Fatalf("parseOSVScanner: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one", findings)
	}
	if findings[0].ID != "GO-new" || findings[0].Package != "bad\ufffd" {
		t.Fatalf("finding = %+v, want duplicate-key last values with replacement character", findings[0])
	}
}

func TestParseGovulncheckMarksSymbolFindingsReachable(t *testing.T) {
	t.Parallel()

	input := []byte(
		`{"finding":{"osv":"GO-2099-0001","trace":[` +
			`{"module":"example.com/module","package":"example.com/module/affected",` +
			`"function":"Vulnerable"}]}}` + "\n" +
			`{"finding":{"osv":"GO-2099-0001","trace":[` +
			`{"module":"example.com/module"}]}}` + "\n",
	)
	findings, err := parseGovulncheck(input)
	if err != nil {
		t.Fatalf("parseGovulncheck: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want two", findings)
	}
	var reachable, moduleOnly bool
	for _, item := range findings {
		switch item.Package {
		case "example.com/module/affected":
			reachable = item.Reachable && item.Source == "Vulnerable"
		case "example.com/module":
			moduleOnly = !item.Reachable && item.Source == ""
		}
	}
	if !reachable || !moduleOnly {
		t.Fatalf("findings = %+v, want reachable symbol and module-only inventory", findings)
	}
}
