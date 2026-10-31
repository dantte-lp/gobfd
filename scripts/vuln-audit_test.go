package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec" //nolint:depguard // The current test binary isolates report's process exit and allowlist fixture.
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"text/template"
	"time"
)

func TestSelectedOSVManifestIncludesEffectiveTransitives(t *testing.T) {
	t.Parallel()

	data, count, err := selectedOSVManifest([]byte("\nexample.com/transitive/v2 v2.0.0\nexample.com/direct " +
		"v1.0.0\nexample.com/transitive/v2 v2.0.0\n"))
	if err != nil || count != 2 {
		t.Fatalf("selected module input count=%d error=%v", count, err)
	}
	want := "module example.invalid/gobfd-osv-selected\n\nrequire (\n\t\"example.com/direct\" \"v1.0.0\"\n" +
		"\t\"example.com/transitive/v2\" \"v2.0.0\"\n)\n"
	if string(data) != want {
		t.Fatalf("selected module input = %q, want %q", data, want)
	}
}

func TestSelectedOSVManifestRejectsIncompleteOrAmbiguousGraph(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"", "\n", "LOCAL-REPLACEMENT", "example.com/module", "../replacement v1.0.0", "/replacement v1.0.0",
		"example.com/module (devel)", "example.com/module v1.0.0 unexpected",
		"example.com/module v1.0.0\nexample.com/module v2.0.0\n", strings.Repeat("x", (16<<20)+1),
	} {
		if _, _, err := selectedOSVManifest([]byte(input)); !errors.Is(err, errSelectedModuleGraph) {
			t.Fatalf("invalid graph of %d bytes: got %v", len(input), err)
		}
	}
}

func TestSelectedPackagesEnforceModuleOnlyAndMigrationBoundaries(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"", "golang.org/x/crypto/openpgp", "golang.org/x/crypto/openpgp/packet",
		"github.com/osrg/gobgp/v4/pkg/server", "github.com/docker/distribution/registry/client", "rsc.io/pdf",
		"rsc.io/pdf/reader",
	} {
		if err := checkExcludedPackages([]byte(input)); !errors.Is(err, errSelectedModuleGraph) {
			t.Fatalf("excluded package graph %q: %v", input, err)
		}
	}
	if err := checkExcludedPackages([]byte("crypto/x509\ngolang.org/x/crypto/ssh\ngithub.com/ProtonMail/go-" +
		"crypto/openpgp\ngithub.com/osrg/gobgp/v4/api\n" +
		"github.com/distribution/distribution/v3/registry/client\n")); err != nil {
		t.Fatalf("maintained package identities rejected: %v", err)
	}
}

func TestSelectedModuleTemplateRejectsLocalPathThatLooksVersioned(t *testing.T) {
	t.Parallel()

	formatter, err := template.New("selected-module").Parse(selectedModuleTemplate)
	if err != nil {
		t.Fatal(err)
	}
	module := map[string]any{
		"Main": false, "Path": "example.com/dependency", "Version": "v1.0.0",
		"Replace": map[string]string{"Path": "local v2.0.0", "Version": ""},
	}
	var output bytes.Buffer
	if err := formatter.Execute(&output, module); err != nil {
		t.Fatal(err)
	}
	if _, _, err := selectedOSVManifest(output.Bytes()); !errors.Is(err, errSelectedModuleGraph) {
		t.Fatalf("local replacement rendered as a registry module: %q, %v", output.String(), err)
	}
}

func TestCollectOSVResultRejectsIncompleteExecution(t *testing.T) {
	t.Parallel()

	const output = `{"results":[{"source":{"path":"go.mod"},"packages":[` +
		`{"package":{"name":"golang.org/x/crypto","version":"v0.51.0","ecosystem":"Go"},` +
		`"vulnerabilities":[{"id":"GO-2020-5932"}]}]}]}`
	exitErr := errors.New("scanner process failed")
	for _, test := range []struct {
		name        string
		code        int
		timedOut    bool
		wantFailure bool
	}{
		{name: "vulnerabilities", code: 1},
		{name: "process-failure", code: -1, wantFailure: true},
		{name: "generic-error", code: 127, wantFailure: true},
		{name: "no-packages", code: 128, wantFailure: true},
		{name: "api-error", code: 129, wantFailure: true},
		{name: "invalid-configuration", code: 130, wantFailure: true},
		{name: "timeout-with-findings", code: 1, timedOut: true, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			findings, failures := collectOSVResult(commandResult{
				Stdout: []byte(output), Code: test.code, Err: exitErr, TimedOut: test.timedOut,
			}, "", "runtime", "go.mod", []byte("golang.org/x/crypto v0.51.0\n"))
			if len(findings) != 1 || findings[0].Source != "go.mod (selected module graph)" {
				t.Fatalf("partial finding evidence lost: %+v", findings)
			}
			if (len(failures) != 0) != test.wantFailure {
				t.Fatalf("scanner exit %d: failures=%v, want failure=%t", test.code, failures, test.wantFailure)
			}
		})
	}
}

func TestScannerExitRequiresCompleteResult(t *testing.T) {
	t.Parallel()

	exitErr := errors.New("scanner process failed")
	for _, findingsExit := range []int{0, 1} {
		if unexpectedScannerExit(commandResult{}, 0, findingsExit) {
			t.Fatal("successful empty result rejected")
		}
		if findingsExit > 0 && unexpectedScannerExit(commandResult{Code: findingsExit, Err: exitErr}, 1, findingsExit) {
			t.Fatalf("documented findings exit %d rejected", findingsExit)
		}
		for _, result := range []commandResult{
			{Err: exitErr}, {Code: 2, Err: exitErr}, {Code: findingsExit, Err: exitErr},
		} {
			if !unexpectedScannerExit(result, 0, findingsExit) {
				t.Fatalf("incomplete result accepted: %+v", result)
			}
		}
	}
	for _, code := range []int{1, 2, 3, -1} {
		if !unexpectedScannerExit(commandResult{Code: code, Err: exitErr}, 1, 0) {
			t.Fatalf("govulncheck JSON failure accepted despite findings: exit %d", code)
		}
	}
}

func TestCollectOSVResultRequiresOutputWithoutReportDirectory(t *testing.T) {
	t.Parallel()

	for _, output := range []string{"", " \n", "{"} {
		_, failures := collectOSVResult(commandResult{Stdout: []byte(output)}, "", "runtime", "go.mod",
			[]byte("example.com/module v1.0.0\n"))
		if len(failures) == 0 {
			t.Fatalf("incomplete output %q accepted without a report directory", output)
		}
	}
	_, failures := collectOSVResult(commandResult{Stdout: []byte(`{"results":[]}`)}, "", "runtime", "go.mod",
		[]byte("example.com/module v1.0.0\n"))
	if len(failures) == 0 {
		t.Fatal("empty JSON result accepted for a nonempty selected graph")
	}
}

func TestCollectOSVResultRequiresExactSelectedGoPackageCoverage(t *testing.T) {
	t.Parallel()
	const selection = "example.com/first v1.0.0\nexample.com/second v2.0.0\n"
	const first = `{"package":{"name":"example.com/first","version":"v1.0.0","ecosystem":"Go"}}`
	const second = `{"package":{"name":"example.com/second","version":"v2.0.0","ecosystem":"Go"}}`
	for _, test := range []struct {
		name   string
		output string
	}{
		{"empty results", `{"results":[]}`},
		{"missing results", `{}`},
		{"null results", `{"results":null}`},
		{"empty packages", `{"results":[{"packages":[]}]}`},
		{"missing selected package", `{"results":[{"packages":[` + first + `]}]}`},
		{"duplicate package", `{"results":[{"packages":[` + first + `,` + second + `,` + first + `]}]}`},
		{"duplicate across results", `{"results":[{"packages":[` + first + `,` + second + `]},{"packages":[` +
			second + `]}]}`},
		{"wrong version", `{"results":[{"packages":[` + first +
			`,{"package":{"name":"example.com/second","version":"v2.0.1","ecosystem":"Go"}}]}]}`},
		{"extra package", `{"results":[{"packages":[` + first + `,` + second +
			`,{"package":{"name":"example.com/extra","version":"v1.0.0","ecosystem":"Go"}}]}]}`},
		{"non-Go identity", `{"results":[{"packages":[` + first +
			`,{"package":{"name":"example.com/second","version":"v2.0.0","ecosystem":"npm"}}]}]}`},
		{"missing ecosystem", `{"results":[{"packages":[` + first +
			`,{"package":{"name":"example.com/second","version":"v2.0.0"}}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, failures := collectOSVResult(commandResult{Stdout: []byte(test.output)}, "", "runtime", "go.mod",
				[]byte(selection))
			if len(failures) == 0 {
				t.Fatalf("incomplete or ambiguous selected graph coverage accepted: %s", test.output)
			}
		})
	}
}

func TestCollectOSVResultAcceptsCompleteZeroFindingCoverage(t *testing.T) {
	t.Parallel()
	const output = `{"results":[{"source":{"path":"go.mod"},"packages":[` +
		`{"package":{"name":"example.com/second","version":"v2.0.0","ecosystem":"Go"}},` +
		`{"package":{"name":"example.com/first","version":"v1.0.0","ecosystem":"Go"}}]}]}`
	findings, failures := collectOSVResult(commandResult{Stdout: []byte(output)}, "", "runtime", "go.mod",
		[]byte("example.com/first v1.0.0\nexample.com/second v2.0.0\nexample.com/first v1.0.0\n"))
	if len(findings) != 0 || len(failures) != 0 {
		t.Fatalf("complete zero-finding report rejected: findings=%v failures=%v", findings, failures)
	}
}

func TestCollectOSVResultAcceptsOSVGoVersionsWithoutChangingRawEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, module, selected, scanned string }{
		{"stable", "cel.dev/expr", "v0.25.2", "0.25.2"},
		{
			"pseudo", "cloud.google.com/go/auth", "v0.24.1-0.20261001053825-dbc26066f70a",
			"0.24.1-0.20261001053825-dbc26066f70a",
		},
		{"incompatible", "github.com/DataDog/datadog-go", "v3.2.0+incompatible", "3.2.0+incompatible"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output := `{"results":[{"packages":[{"package":{"name":"` + test.module + `","version":"` + test.scanned +
				`","ecosystem":"Go"},"vulnerabilities":[{"id":"GO-2099-0001"}]}]}]}`
			directory := t.TempDir()
			findings, failures := collectOSVResult(commandResult{Stdout: []byte(output)}, directory, "runtime", "go.mod",
				[]byte(test.module+" "+test.selected+"\n"))
			if len(failures) != 0 || len(findings) != 1 || findings[0].Version != test.scanned {
				t.Fatalf("OSV version rejected or transformed: findings=%v failures=%v", findings, failures)
			}
			data, err := os.ReadFile(filepath.Join(directory, "runtime-osv.json"))
			if err != nil || string(data) != output {
				t.Fatalf("raw OSV version transformed: %q, %v", data, err)
			}
		})
	}
}

func TestCollectOSVResultRejectsMoreThanOneVersionPrefix(t *testing.T) {
	t.Parallel()
	const output = `{"results":[{"packages":[{"package":{"name":"example.com/module","version":"vv1.0.0","e` +
		`cosystem":"Go"}}]}]}`
	_, failures := collectOSVResult(commandResult{Stdout: []byte(output)}, "", "runtime", "go.mod",
		[]byte("example.com/module v1.0.0\n"))
	if len(failures) == 0 {
		t.Fatal("more than one version prefix was normalized")
	}
}

func TestCollectOSVResultPreservesRawReportAndFindingsOnCoverageFailure(t *testing.T) {
	t.Parallel()
	const output = `{"results":[{"source":{"path":"go.mod"},"packages":[` +
		`{"package":{"name":"example.com/first","version":"v1.0.0","ecosystem":"Go"},"vulnerabilities":[{"id":` +
		`"GO-2099-0001"}]}]}]}`
	directory := t.TempDir()
	findings, failures := collectOSVResult(commandResult{Stdout: []byte(output)}, directory, "runtime", "go.mod",
		[]byte("example.com/first v1.0.0\nexample.com/second v2.0.0\n"))
	if len(failures) == 0 || len(findings) != 1 || findings[0].ID != "GO-2099-0001" ||
		findings[0].Source != "go.mod (selected module graph)" {
		t.Fatalf("coverage failure lost findings: findings=%v failures=%v", findings, failures)
	}
	data, err := os.ReadFile(filepath.Join(directory, "runtime-osv.json"))
	if err != nil || string(data) != output {
		t.Fatalf("raw scanner report changed: %q, %v", data, err)
	}
}

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
	findings, _, err := parseOSVScanner(input)
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

func TestPublisherDispositionsRequireFreshArtifactAndAbsenceProof(t *testing.T) {
	t.Parallel()
	evidence := publisherEvidenceFixture(t)
	input := make([]finding, 0, len(publisherDispositions()))
	for id, disposition := range publisherDispositions() {
		input = append(input, finding{
			Scanner: osvScannerName, ID: id,
			Package: disposition.module, Version: disposition.version, Source: "publisher selected graph",
		})
	}
	allowed, unallowed, failures := classifyPublisherFindings(input, evidence, publisherAuditTime())
	if len(allowed) != 7 || len(unallowed) != 0 || len(failures) != 0 || len(allowed["GO-2026-5932"]) != 2 {
		t.Fatalf("publisher classification: allowed=%v unallowed=%v failures=%v", allowed, unallowed, failures)
	}
	if len(input) != 7 || input[0].Source != "publisher selected graph" {
		t.Fatal("publisher classification changed raw findings")
	}
	if len(allowlist) != 2 {
		t.Fatal("publisher decisions broadened the runtime/tool allowlist")
	}
}

func TestPublisherDispositionsRejectIncompleteOrChangedEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*publisherAuditEvidence)
	}{
		{"missing-artifact", func(e *publisherAuditEvidence) { e.buildInfo = nil }},
		{"missing-source-profile", func(e *publisherAuditEvidence) { e.profileSHA256 = "" }},
		{"changed-source-profile", func(e *publisherAuditEvidence) { e.profileSHA256 = strings.Repeat("a", 64) }},
		{"missing-artifact-hash", func(e *publisherAuditEvidence) { e.artifactSHA256 = "" }},
		{"zero-artifact-hash", func(e *publisherAuditEvidence) { e.artifactSHA256 = strings.Repeat("0", 64) }},
		{"invalid-artifact-hash", func(e *publisherAuditEvidence) { e.artifactSHA256 = strings.Repeat("x", 64) }},
		{"wrong-main", func(e *publisherAuditEvidence) { e.buildInfo.Main.Path = "example.com/other" }},
		{"wrong-version", func(e *publisherAuditEvidence) { e.buildInfo.Main.Version = "v2.19.1" }},
		{"wrong-compiler", func(e *publisherAuditEvidence) { e.buildInfo.GoVersion = "go1.26.10" }},
		{"missing-settings", func(e *publisherAuditEvidence) { e.buildInfo.Settings = nil }},
		{"cgo-enabled", func(e *publisherAuditEvidence) { e.buildInfo.Settings[0].Value = "1" }},
		{"missing-graph", func(e *publisherAuditEvidence) { e.selected = nil }},
		{"changed-selected-version", func(e *publisherAuditEvidence) {
			e.selected = bytes.ReplaceAll(e.selected, []byte("v1.55.5"), []byte("v1.55.8"))
		}},
		{"missing-packages", func(e *publisherAuditEvidence) { e.packages = nil }},
		{"partial-package-graph", func(e *publisherAuditEvidence) {
			e.packages = []byte("github.com/goreleaser/goreleaser/v2\n")
		}},
		{"aws-import", func(e *publisherAuditEvidence) {
			e.packages = append(e.packages, []byte("github.com/aws/aws-sdk-go/service/s3/s3crypto\n")...)
		}},
		{"old-acr-import", func(e *publisherAuditEvidence) {
			e.packages = append(e.packages, []byte("github.com/chrismellard/docker-credential-acr-env/pkg/credhelper\n")...)
		}},
		{"etcd-server-import", func(e *publisherAuditEvidence) {
			e.packages = append(e.packages, []byte("go.etcd.io/etcd/server/v3/embed\n")...)
		}},
		{"old-openpgp-import", func(e *publisherAuditEvidence) {
			e.packages = append(e.packages, []byte("golang.org/x/crypto/openpgp/packet\n")...)
		}},
		{"missing-symbol-proof", func(e *publisherAuditEvidence) { e.nativeReport = nil }},
		{"malformed-symbol-proof", func(e *publisherAuditEvidence) { e.nativeReport = []byte("{") }},
		{"missing-symbol-configuration", func(e *publisherAuditEvidence) {
			e.nativeReport = bytes.SplitN(e.nativeReport, []byte("\n"), 2)[1]
		}},
		{"wrong-symbol-level", func(e *publisherAuditEvidence) {
			e.nativeReport = bytes.ReplaceAll(e.nativeReport, []byte("symbol"), []byte("module"))
		}},
		{"wrong-scan-mode", func(e *publisherAuditEvidence) {
			e.nativeReport = bytes.ReplaceAll(e.nativeReport, []byte("binary"), []byte("source"))
		}},
		{"unknown-scanner", func(e *publisherAuditEvidence) {
			e.nativeReport = bytes.ReplaceAll(e.nativeReport, []byte("govulncheck"), []byte("other"))
		}},
		{"stale-scanner-database", func(e *publisherAuditEvidence) {
			e.nativeReport = bytes.ReplaceAll(e.nativeReport, []byte("2026-10-30"), []byte("2026-09-30"))
		}},
		{"changed-artifact-sbom", func(e *publisherAuditEvidence) {
			e.nativeReport = bytes.ReplaceAll(e.nativeReport, []byte("v0.58.0"), []byte("v0.57.0"))
		}},
		{"artifact-contains-aws", func(e *publisherAuditEvidence) {
			e.buildInfo.Deps = append(e.buildInfo.Deps, &debug.Module{Path: "github.com/aws/aws-sdk-go", Version: "v1.55.5"})
		}},
		{"artifact-contains-etcd", func(e *publisherAuditEvidence) {
			e.buildInfo.Deps = append(e.buildInfo.Deps, &debug.Module{Path: "go.etcd.io/etcd/v3", Version: "v3.7.3"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evidence := publisherEvidenceFixture(t)
			test.change(&evidence)
			allowed, _, failures := classifyPublisherFindings(nil, evidence, publisherAuditTime())
			if len(allowed) != 0 || len(failures) == 0 {
				t.Fatalf("incomplete evidence accepted: allowed=%v failures=%v", allowed, failures)
			}
		})
	}
}

func TestPublisherDispositionsRejectUnapprovedFindingsAndExpiry(t *testing.T) {
	t.Parallel()
	for _, item := range []finding{
		{Scanner: osvScannerName, ID: "GO-2099-0001", Package: "github.com/aws/aws-sdk-go", Version: "v1.55.5"},
		{Scanner: osvScannerName, ID: "CVE-2099-0001", Package: "github.com/aws/aws-sdk-go", Version: "v1.55.5"},
		{Scanner: osvScannerName, ID: "GO-2022-0635", Package: "github.com/aws/aws-sdk-go", Version: "v1.55.8"},
		{Scanner: osvScannerName, ID: "GO-2022-0635", Package: "github.com/aws/aws-sdk-go", Version: "vv1.55.5"},
		{
			Scanner: osvScannerName, ID: "GO-2022-0635", Package: "github.com/aws/aws-sdk-go/service/s3/s3crypto",
			Version: "v1.55.5",
		},
		{Scanner: govulncheckName, ID: "GO-2022-0635", Package: "github.com/aws/aws-sdk-go", Version: "v1.55.5"},
		{Scanner: "other", ID: "GO-2026-5932", Package: "golang.org/x/crypto", Version: "v0.58.0"},
		{Scanner: govulncheckName, ID: "GO-2026-5932", Package: "golang.org/x/crypto/openpgp", Version: "v0.58.0"},
		{Scanner: govulncheckName, ID: "GO-2026-5932", Package: "golang.org/x/crypto", Version: "v0.58.0", Reachable: true},
	} {
		_, unallowed, failures := classifyPublisherFindings(
			[]finding{item}, publisherEvidenceFixture(t), publisherAuditTime())
		if len(unallowed[item.ID]) != 1 || len(failures) != 0 {
			t.Fatalf("unapproved finding accepted: %+v; unallowed=%v failures=%v", item, unallowed, failures)
		}
	}
	expired := publisherEvidenceFixture(t)
	expired.nativeReport = bytes.ReplaceAll(expired.nativeReport, []byte("2026-10-30"), []byte("2026-11-30"))
	_, _, failures := classifyPublisherFindings(nil, expired, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if len(failures) == 0 {
		t.Fatal("expired publisher approval accepted")
	}
}

func TestPublisherDispositionAliasesPreserveRawIDsAndExactBounds(t *testing.T) {
	t.Parallel()
	for alias, canonical := range publisherDispositionAliases() {
		disposition := publisherDispositions()[canonical]
		item := finding{Scanner: osvScannerName, ID: alias, Package: disposition.module, Version: disposition.version}
		allowed, unallowed, failures := classifyPublisherFindings(
			[]finding{item}, publisherEvidenceFixture(t), publisherAuditTime())
		if len(allowed[canonical]) == 0 || allowed[canonical][0].ID != alias || len(unallowed) != 0 || len(failures) != 0 {
			t.Fatalf("exact alias lost or rejected: %s allowed=%v unallowed=%v failures=%v", alias, allowed, unallowed, failures)
		}
		item.Version = "v0.0.0"
		_, unallowed, failures = classifyPublisherFindings([]finding{item}, publisherEvidenceFixture(t), publisherAuditTime())
		if len(unallowed[alias]) != 1 || len(failures) != 0 {
			t.Fatalf("alias bypassed exact-version boundary: %s unallowed=%v failures=%v", alias, unallowed, failures)
		}
	}
}

func TestPublisherArtifactAcceptsAuthenticatedLocalRefmtBuildInfo(t *testing.T) {
	t.Parallel()
	evidence := publisherEvidenceFixture(t)
	// Actual Go 1.27 BuildInfo and govulncheck represent a local replacement
	// as (devel); selected Go graph retains its authenticated upstream ancestry.
	evidence.buildInfo.Deps = append(evidence.buildInfo.Deps, &debug.Module{
		Path: "github.com/polydawn/refmt", Version: "v0.90.0",
		Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)"},
	})
	evidence.packages = append(evidence.packages, []byte("github.com/polydawn/refmt/shared\n")...)
	evidence.nativeReport = bytes.Replace(evidence.nativeReport, []byte(`"modules":[`),
		[]byte(`"modules":[{"path":"../refmt-local","version":"(devel)"},`), 1)
	allowed, unallowed, failures := classifyPublisherFindings(nil, evidence, publisherAuditTime())
	if len(failures) != 0 || len(unallowed) != 0 || len(allowed["GO-2026-5932"]) != 1 {
		t.Fatalf("authenticated local component rejected: allowed=%v unallowed=%v failures=%v",
			allowed, unallowed, failures)
	}
	for _, bad := range [][]byte{
		bytes.ReplaceAll(evidence.nativeReport, []byte("../refmt-local"), []byte("../foreign")),
		bytes.ReplaceAll(evidence.nativeReport, []byte("(devel)"), []byte("unknown")),
		bytes.Replace(evidence.nativeReport, []byte(`"path":"../refmt-local","version":"(devel)"`),
			[]byte(`"path":"github.com/polydawn/refmt","version":"v0.90.0"`), 1),
		bytes.Replace(evidence.nativeReport, []byte(`"modules":[`),
			[]byte(`"modules":[{"path":"github.com/polydawn/refmt","version":"v0.90.0"},`), 1),
	} {
		invalid := evidence
		invalid.nativeReport = bad
		if _, _, invalidFailures := classifyPublisherFindings(nil, invalid, publisherAuditTime()); len(invalidFailures) == 0 {
			t.Fatal("unapproved or duplicate local scanner component accepted")
		}
	}
}

func TestPublisherNativeSBOMRequiresExactCompilerStandardLibrary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"missing", func(data []byte) []byte {
			return bytes.Replace(data, []byte(`,{"path":"stdlib","version":"go1.27.3"}`), nil, 1)
		}},
		{"wrong-version", func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"path":"stdlib","version":"go1.27.3"`),
				[]byte(`"path":"stdlib","version":"go1.27.2"`), 1)
		}},
		{"duplicate", func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"modules":[`),
				[]byte(`"modules":[{"path":"stdlib","version":"go1.27.3"},`), 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evidence := publisherEvidenceFixture(t)
			changed := test.change(evidence.nativeReport)
			if bytes.Equal(changed, evidence.nativeReport) {
				t.Fatal("invalid standard-library fixture did not change the native report")
			}
			evidence.nativeReport = changed
			if _, _, failures := classifyPublisherFindings(nil, evidence, publisherAuditTime()); len(failures) == 0 {
				t.Fatal("missing, mismatched or duplicate compiler standard-library component accepted")
			}
		})
	}
}

func TestPublisherArtifactRejectsUnapprovedLocalReplacementMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		module debug.Module
	}{
		{"missing-local-role", debug.Module{Path: "github.com/polydawn/refmt", Version: "v0.90.0"}},
		{"foreign-module", debug.Module{
			Path: "example.com/foreign", Version: "v0.90.0",
			Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)"},
		}},
		{"wrong-ancestry", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.1",
			Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)"},
		}},
		{"foreign-directory", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.0",
			Replace: &debug.Module{Path: "../foreign", Version: "(devel)"},
		}},
		{"missing-devel", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.0",
			Replace: &debug.Module{Path: "../refmt-local"},
		}},
		{"unapproved-sum", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.0",
			Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)", Sum: "unexpected"},
		}},
		{"unapproved-original-sum", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.0", Sum: "unexpected",
			Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)"},
		}},
		{"nested-replacement", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.0",
			Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)", Replace: &debug.Module{Path: "foreign"}},
		}},
		{"remote-alias", debug.Module{
			Path: "github.com/polydawn/refmt", Version: "v0.90.0",
			Replace: &debug.Module{Path: "example.com/foreign", Version: "v0.90.0"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := publisherArtifactModule(&test.module); !errors.Is(err, errPublisherEvidence) {
				t.Fatalf("unapproved local metadata accepted: %+v, %v", test.module, err)
			}
		})
	}
}

func TestPublisherScannerUsesCapturedDescriptorDuringPathReplacementABA(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("publisher descriptor scanner requires Linux procfs")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "publisher")
	if writeErr := os.WriteFile(path, []byte("captured publisher artifact"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	file, identity, before, err := openPublisherArtifact(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if renameErr := os.Rename(path, path+".original"); renameErr != nil {
		t.Fatal(renameErr)
	}
	if writeErr := os.WriteFile(path, []byte("unbound artifact"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := runPublisherScanner(executable, []string{"-test.run=^TestPublisherScannerCapturedDescriptorHelper$"}, file)
	if result.Err != nil || result.Code != 0 {
		t.Fatalf("scanner descriptor read: %v, %s", result.Err, result.Stdout)
	}
	if renameErr := os.Rename(path, path+".replacement"); renameErr != nil {
		t.Fatal(renameErr)
	}
	if renameErr := os.Rename(path+".original", path); renameErr != nil {
		t.Fatal(renameErr)
	}
	after, err := hashPublisherArtifact(file, path, identity)
	if err != nil || after != before {
		t.Fatalf("ABA control failed to restore original: %s, %v", after, err)
	}
}

func TestPublisherScannerCapturedDescriptorHelper(t *testing.T) {
	if flag.NArg() == 0 {
		return
	}
	if flag.NArg() != 1 || flag.Arg(0) != "/proc/self/fd/3" {
		t.Fatalf("scanner received a pathname instead of its inherited artifact descriptor: %v", flag.Args())
	}
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil || string(data) != "captured publisher artifact" {
		t.Fatalf("scanner read the replacement pathname instead of the captured artifact: %q, %v", data, err)
	}
}

func TestPublisherScannerOutputBoundFailsWithoutTruncatingSuccess(t *testing.T) {
	t.Parallel()
	output := publisherOutputBuffer{limit: 4}
	if n, err := output.Write([]byte("full")); n != 4 || err != nil {
		t.Fatalf("bounded write: %d, %v", n, err)
	}
	if n, err := output.Write([]byte("extra")); n != 0 ||
		!errors.Is(err, errPublisherEvidence) || output.String() != "full" {
		t.Fatalf("oversized output did not fail closed: n=%d err=%v data=%q", n, err, output.String())
	}
	copyOutput := publisherOutputBuffer{limit: 4}
	_, err := io.Copy(&copyOutput, io.LimitReader(strings.NewReader("extra"), 5))
	if !errors.Is(err, errPublisherEvidence) || len(copyOutput.Bytes()) > 4 {
		t.Fatalf("process stream copy bypassed output bound: err=%v data=%q", err, copyOutput.String())
	}
}

func TestPublisherSelectedGraphRequiresBothAuthenticatedLocalComponents(t *testing.T) {
	t.Parallel()
	const main = `{"Path":"github.com/dantte-lp/gobfd/tools/goreleaser","Main":true}` + "\n"
	const publisher = `{"Path":"github.com/goreleaser/goreleaser/v2","Version":"v2.19.0",` +
		`"Replace":{"Path":"../source"}}` + "\n"
	const refmt = `{"Path":"github.com/polydawn/refmt","Version":"v0.90.0",` +
		`"Replace":{"Path":"../refmt-local"}}` + "\n"
	const remote = `{"Path":"golang.org/x/net","Version":"v0.61.0",` +
		`"Replace":{"Path":"golang.org/x/net","Version":"v0.62.0"}}`
	input := main + publisher + refmt + remote
	selection, err := publisherSelectedModules([]byte(input), "/private/consumer")
	if err != nil || !bytes.Contains(selection, []byte("golang.org/x/net v0.62.0\n")) ||
		!bytes.Contains(selection, []byte("github.com/goreleaser/goreleaser/v2 v2.19.0\n")) {
		t.Fatalf("authenticated source identities/effective remote version lost: %s, %v", selection, err)
	}
	for _, bad := range []string{
		"", "{", main + publisher, main + refmt, publisher + refmt,
		main + main + publisher + refmt, input + "\n" + remote,
		strings.ReplaceAll(input, "../source", "../foreign"),
		strings.ReplaceAll(input, "github.com/dantte-lp/gobfd/tools/goreleaser", "example.com/other"),
		input + `{"Path":"example.com/foreign","Version":"v1.0.0","Replace":{"Path":"../foreign"}}`,
	} {
		if _, err := publisherSelectedModules([]byte(bad), "/private/consumer"); !errors.Is(err, errPublisherEvidence) {
			t.Fatalf("unverified publisher source layout accepted: %q, %v", bad, err)
		}
	}
}

func TestPublisherArtifactHashRejectsChangedPathAndNonRegularInput(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "publisher")
	if writeErr := os.WriteFile(path, []byte("artifact"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	first, err := hashPublisherArtifact(file, path, info)
	if err != nil || !validPublisherDigest(first) {
		t.Fatalf("valid artifact hash: %q, %v", first, err)
	}
	if renameErr := os.Rename(path, path+".old"); renameErr != nil {
		t.Fatal(renameErr)
	}
	if writeErr := os.WriteFile(path, []byte("replacement"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, err := hashPublisherArtifact(file, path, info); !errors.Is(err, errPublisherEvidence) {
		t.Fatalf("replacement inode accepted: %v", err)
	}
	if _, err := hashPublisherArtifact(file, directory, info); !errors.Is(err, errPublisherEvidence) {
		t.Fatalf("directory accepted as publisher artifact: %v", err)
	}
	if _, _, err := collectPublisherEvidence("", "", ""); !errors.Is(err, errPublisherEvidence) {
		t.Fatalf("missing absolute caller/evidence paths accepted: %v", err)
	}
}

func TestParseGovulncheckPreservesFindingVersion(t *testing.T) {
	t.Parallel()
	findings, err := parseGovulncheck([]byte(`{"finding":{"osv":"GO-2026-5932","trace":[` +
		`{"module":"golang.org/x/crypto","version":"v0.58.0"}]}}`))
	if err != nil || len(findings) != 1 || findings[0].Version != "v0.58.0" {
		t.Fatalf("native finding version lost: findings=%v err=%v", findings, err)
	}
}

func publisherAuditTime() time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) }

func publisherEvidenceFixture(t *testing.T) publisherAuditEvidence {
	t.Helper()
	modules := map[string]string{
		"github.com/goreleaser/goreleaser/v2":                "v2.19.0",
		"github.com/polydawn/refmt":                          "v0.90.0",
		"github.com/osscontainertools/docker-credential-acr": "v1.0.0",
	}
	for _, disposition := range publisherDispositions() {
		modules[disposition.module] = disposition.version
	}
	var selected strings.Builder
	for _, path := range slices.Sorted(maps.Keys(modules)) {
		fmt.Fprintf(&selected, "%s %s\n", path, modules[path])
	}
	info := &debug.BuildInfo{
		GoVersion: "go1.27.3",
		Main:      debug.Module{Path: "github.com/goreleaser/goreleaser/v2", Version: "v2.19.0"},
		Settings:  []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"}},
		Deps: []*debug.Module{
			{Path: "golang.org/x/crypto", Version: "v0.58.0"},
			{Path: "github.com/osscontainertools/docker-credential-acr", Version: "v1.0.0"},
		},
	}
	binaryModules := make([]map[string]string, 0, 2+len(info.Deps))
	for _, module := range append([]*debug.Module{&info.Main}, info.Deps...) {
		binaryModules = append(binaryModules, map[string]string{"path": module.Path, "version": module.Version})
	}
	binaryModules = append(binaryModules, map[string]string{"path": "stdlib", "version": info.GoVersion})
	sbom, err := json.Marshal(map[string]any{
		"SBOM": map[string]any{"go_version": info.GoVersion, "modules": binaryModules},
	})
	if err != nil {
		t.Fatal(err)
	}
	const config = `{"config":{"scanner_name":"govulncheck","scan_mode":"binary","scan_level":"symbol",` +
		`"db":"https://vuln.go.dev","db_last_modified":"2026-10-30T00:00:00Z"}}` + "\n"
	const nativeFinding = "\n" + `{"finding":{"osv":"GO-2026-5932","trace":[` +
		`{"module":"golang.org/x/crypto","version":"v0.58.0"}]}}`
	return publisherAuditEvidence{
		selected: []byte(selected.String()),
		packages: []byte("github.com/goreleaser/goreleaser/v2\ngolang.org/x/crypto/ssh\n" +
			"github.com/osscontainertools/docker-credential-acr/pkg/credhelper\n"),
		artifactSHA256: strings.Repeat("1", 64), profileSHA256: publisherApprovedProfileSHA256, buildInfo: info,
		nativeReport: append([]byte(config), append(sbom, []byte(nativeFinding)...)...),
	}
}

func TestSelectedPackagesIgnoreUnavailableVCS(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":  "module example.invalid/gobfd-vcs-control\n\ngo 1.27\n",
		"main.go": "package main\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.CommandContext(t.Context(), "git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize VCS fixture: %v, %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPROXY", "off")
	control := runGo("list", "-mod=readonly", "-modfile=go.mod", "-deps", "-f", "{{.ImportPath}}", "./...")
	if control.Err == nil || !strings.Contains(control.Stderr, "VCS") {
		t.Fatalf("unavailable VCS control did not fail: %v, %s", control.Err, control.Stderr)
	}
	if err := verifySelectedPackages("go.mod"); err != nil {
		t.Fatalf("package selection requires unrelated VCS metadata: %v", err)
	}
}
