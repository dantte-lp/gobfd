package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec" //nolint:depguard // The current test binary isolates report's process exit and allowlist fixture.
	"strings"
	"testing"
	"time"
)

func TestVulnerabilityReportDiagnostics(t *testing.T) {
	t.Parallel()

	expired := allowEntry{
		Package: "example.com/module", Owner: "netops", Expires: "2000-01-01",
		Reason: "fixture exception", Mitigation: "fixture isolation",
	}
	valid := expired
	valid.Expires = "9999-12-31"
	known := finding{Scanner: govulncheckName, ID: "GO-2099-0001", Package: expired.Package}
	unknown := finding{
		Scanner: osvScannerName, ID: "GO-2099-0002", Package: "example.com/other",
		Version: "v1.0.0", Source: "fixture source",
	}
	cases := []struct {
		name       string
		entries    map[string]allowEntry
		findings   []finding
		wantExit   int
		wantStderr []string
		wantStdout string
	}{
		{
			name: "expired-and-unallowed", entries: map[string]allowEntry{known.ID: expired},
			findings: []finding{known, unknown}, wantExit: 2,
			wantStderr: []string{
				"allowlist entry GO-2099-0001 expired on 2000-01-01",
				"unallowed vulnerability: GO-2099-0002",
				"osv-scanner: example.com/other v1.0.0 fixture source",
			},
		},
		{
			name: "stale-and-unallowed", entries: map[string]allowEntry{known.ID: valid},
			findings: []finding{unknown}, wantExit: 2,
			wantStderr: []string{
				"allowlist entry GO-2099-0001", "matches no scanner finding",
				"unallowed vulnerability: GO-2099-0002",
			},
		},
		{
			name: "expired-only", entries: map[string]allowEntry{known.ID: expired},
			findings: []finding{known}, wantExit: 2,
			wantStderr: []string{"allowlist entry GO-2099-0001 expired on 2000-01-01"},
		},
		{
			name: "unallowed-only", findings: []finding{unknown}, wantExit: 1,
			wantStderr: []string{"unallowed vulnerability: GO-2099-0002"},
		},
		{
			name: "allowed-only", entries: map[string]allowEntry{known.ID: valid}, findings: []finding{known},
			wantStderr: []string{"allowed vulnerability: GO-2099-0001", "owner=netops expires=9999-12-31"},
			wantStdout: "vulnerability audit: no unallowed vulnerabilities found",
		},
		{name: "no-findings", wantStdout: "vulnerability audit: no vulnerabilities found"},
	}
	const helperCase = "GOBFD_TEST_VULN_REPORT_CASE"
	if name := os.Getenv(helperCase); name != "" {
		for _, tc := range cases {
			if tc.name == name {
				allowlist = tc.entries
				report(tc.findings)
				return
			}
		}
		t.Fatalf("unknown report fixture %q", name)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestVulnerabilityReportDiagnostics$", "-test.timeout=15s")
			command.Env = append(os.Environ(), helperCase+"="+tc.name)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			exitCode := 0
			if err := command.Run(); err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("execute report fixture: %v", err)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tc.wantExit {
				t.Errorf("exit code=%d, want %d; stderr=%s", exitCode, tc.wantExit, stderr.String())
			}
			for _, fragment := range tc.wantStderr {
				if !strings.Contains(stderr.String(), fragment) {
					t.Errorf("stderr missing %q: %s", fragment, stderr.String())
				}
			}
			if tc.wantExit != 0 && strings.Contains(stdout.String(), "vulnerability audit: no ") {
				t.Errorf("failed audit printed success: %s", stdout.String())
			}
			if tc.wantStdout != "" && !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout missing %q: %s", tc.wantStdout, stdout.String())
			}
		})
	}
}

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

func TestClassifyFindingsRejectsStaleAllowlistEntry(t *testing.T) {
	t.Parallel()

	entry := allowEntry{
		Package:    "example.com/module",
		Owner:      "netops",
		Expires:    "2026-12-31",
		Reason:     "affected package is outside the build graph",
		Mitigation: "reject affected package imports",
		ModuleOnly: true,
	}
	entries := map[string]allowEntry{"GO-2099-0001": entry, "GO-2099-0002": entry}
	findings := []finding{{
		Scanner:   govulncheckName,
		ID:        "GO-2099-0002",
		Package:   "example.com/module/affected",
		Source:    "Vulnerable",
		Reachable: true,
	}}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(allowed) != 0 {
		t.Fatalf("allowed = %v, want none", allowed)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "GO-2099-0001") ||
		!strings.Contains(failures[0], "remove the stale entry") {
		t.Fatalf("failures = %v, want one stale-entry failure for GO-2099-0001", failures)
	}
	if got := len(unallowed["GO-2099-0002"]); got != 1 {
		t.Fatalf("unallowed GO-2099-0002 count = %d, want the reachable finding, not a stale entry", got)
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
