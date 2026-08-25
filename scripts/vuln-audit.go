package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // Audit runner invokes pinned scanners with explicit argument vectors.
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	govulncheckVersion = "v1.7.0"
	osvScannerVersion  = "v2.5.1"
	scannerTimeout     = 10 * time.Minute
	govulncheckName    = "govulncheck"
	osvScannerName     = "osv-scanner"
)

var (
	errAllowEntryIncomplete = errors.New("allowlist entry missing package, owner, reason, or mitigation")
	errAllowEntryExpired    = errors.New("allowlist entry expired")
	errScannerReportEmpty   = errors.New("scanner returned an empty report")
)

type allowEntry struct {
	Package    string
	Owner      string
	Expires    string
	Reason     string
	Mitigation string
	ModuleOnly bool
}

var allowlist = map[string]allowEntry{
	"GO-2026-4736": {
		Package:    "github.com/osrg/gobgp/v3",
		Owner:      "maintainers",
		Expires:    "2026-09-30",
		Reason:     "GoBGP v3 has no fixed release; v4 migration is tracked by gobfd-qj0.8.2.4.",
		Mitigation: "Keep GoBGP integration on localhost or a trusted management network until upstream ships a fix.",
	},
	"GO-2026-5932": {
		Package:    "golang.org/x/crypto",
		Owner:      "maintainers",
		Expires:    "2026-09-30",
		Reason:     "The advisory affects the unmaintained openpgp package, which is absent from the build dependency graph.",
		Mitigation: "Keep the reachable-package govulncheck gate enabled and reject any openpgp import.",
		ModuleOnly: true,
	},
}

type finding struct {
	Scanner string
	ID      string
	Package string
	Version string
	Source  string
	// Reachable is true when govulncheck reports a vulnerable symbol in the
	// application call graph. Inventory-only scanners cannot establish it.
	Reachable bool
}

type govulnTrace struct {
	Module   string `json:"module"`
	Package  string `json:"package"`
	Function string `json:"function"`
}

type commandResult struct {
	Stdout   []byte
	Stderr   string
	Code     int
	Err      error
	TimedOut bool
}

func main() {
	reportDir := flag.String("report-dir", "", "write separate raw runtime and tools scanner reports")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "vulnerability audit: unexpected arguments: %s\n", strings.Join(flag.Args(), " "))
		os.Exit(2)
	}

	govulnFindings, failures := auditGovulncheck(*reportDir)
	runtimeOSVFindings, runtimeFailures := auditOSV(*reportDir, "runtime", "go.mod")
	toolsOSVFindings, toolsFailures := auditOSV(*reportDir, "tools", "tools/go.mod")
	failures = append(failures, runtimeFailures...)
	failures = append(failures, toolsFailures...)

	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(2)
	}

	all := make([]finding, 0, len(govulnFindings)+len(runtimeOSVFindings)+len(toolsOSVFindings))
	all = append(all, govulnFindings...)
	all = append(all, runtimeOSVFindings...)
	all = append(all, toolsOSVFindings...)
	report(all)
}

func auditGovulncheck(reportDir string) ([]finding, []string) {
	result := runGo("run", "golang.org/x/vuln/cmd/govulncheck@"+govulncheckVersion, "-format", "json", "./...")
	printStderr(govulncheckName, result.Stderr)

	var failures []string
	reportErr := writeScannerReport(reportDir, "runtime-govulncheck.json", result.Stdout)
	if reportErr != nil {
		failures = append(failures, reportErr.Error())
	}
	findings, parseErr := parseGovulncheck(result.Stdout)
	if parseErr != nil {
		failures = append(failures, fmt.Sprintf("govulncheck JSON parse failed: %v", parseErr))
	}
	if result.TimedOut {
		failures = append(failures, "govulncheck timed out")
	}
	if result.Err != nil && len(findings) == 0 {
		failures = append(failures, fmt.Sprintf("govulncheck failed with exit code %d: %v", result.Code, result.Err))
	}
	return findings, failures
}

func auditOSV(reportDir, scope, manifest string) ([]finding, []string) {
	result := runGo(
		"run",
		"github.com/google/osv-scanner/v2/cmd/osv-scanner@"+osvScannerVersion,
		"scan",
		"--lockfile="+manifest,
		"--format",
		"json",
		"--all-packages",
		"--no-call-analysis=go",
	)
	printStderr(scope+" "+osvScannerName, result.Stderr)

	var failures []string
	reportErr := writeScannerReport(reportDir, scope+"-osv.json", result.Stdout)
	if reportErr != nil {
		failures = append(failures, reportErr.Error())
	}
	findings, parseErr := parseOSVScanner(result.Stdout)
	if parseErr != nil {
		failures = append(failures, fmt.Sprintf("%s osv-scanner JSON parse failed: %v", scope, parseErr))
	}
	if result.TimedOut {
		failures = append(failures, scope+" osv-scanner timed out")
	}
	if result.Err != nil && len(findings) == 0 {
		failures = append(failures,
			fmt.Sprintf("%s osv-scanner failed with exit code %d: %v", scope, result.Code, result.Err))
	}
	return findings, failures
}

func writeScannerReport(reportDir, name string, data []byte) error {
	if reportDir == "" {
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("write %s: %w", name, errScannerReportEmpty)
	}
	if err := os.MkdirAll(reportDir, 0o750); err != nil {
		return fmt.Errorf("create scanner report directory %s: %w", reportDir, err)
	}
	path := filepath.Join(reportDir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write scanner report %s: %w", path, err)
	}
	return nil
}

func runGo(args ...string) commandResult {
	fmt.Fprintf(os.Stderr, "vulnerability audit: running go %s\n", strings.Join(args, " "))

	ctx, cancel := context.WithTimeout(context.Background(), scannerTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	code := 0
	if err != nil {
		code = 1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		}
	}

	return commandResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.String(),
		Code:     code,
		Err:      err,
		TimedOut: timedOut,
	}
}

func printStderr(scanner, stderr string) {
	if strings.TrimSpace(stderr) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "vulnerability audit: %s stderr:\n%s", scanner, stderr)
	if !strings.HasSuffix(stderr, "\n") {
		fmt.Fprintln(os.Stderr)
	}
}

func parseGovulncheck(data []byte) ([]finding, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	seen := map[string]finding{}

	for {
		var message struct {
			Finding *struct {
				OSV   string        `json:"osv"`
				Trace []govulnTrace `json:"trace"`
			} `json:"finding"`
		}

		err := decoder.Decode(&message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode govulncheck JSON: %w", err)
		}
		if message.Finding == nil || message.Finding.OSV == "" {
			continue
		}

		item := newGovulnFinding(message.Finding.OSV, message.Finding.Trace)

		key := fmt.Sprintf(
			"%s\x00%s\x00%s\x00%s\x00%t",
			item.Scanner,
			item.ID,
			item.Package,
			item.Source,
			item.Reachable,
		)
		seen[key] = item
	}

	return sortedFindings(seen), nil
}

func newGovulnFinding(id string, trace []govulnTrace) finding {
	item := finding{Scanner: govulncheckName, ID: id}
	for _, frame := range trace {
		if item.Package == "" {
			item.Package = frame.Package
			if item.Package == "" {
				item.Package = frame.Module
			}
		}
		if frame.Function != "" {
			item.Reachable = true
			if item.Source == "" {
				item.Source = frame.Function
			}
		}
	}
	return item
}

func parseOSVScanner(data []byte) ([]finding, error) {
	var report struct {
		Results []struct {
			Source struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"source"`
			Packages []struct {
				Package struct {
					Name      string `json:"name"`
					Version   string `json:"version"`
					Ecosystem string `json:"ecosystem"`
				} `json:"package"`
				Groups []struct {
					IDs []string `json:"ids"`
				} `json:"groups"`
				Vulnerabilities []struct {
					ID string `json:"id"`
				} `json:"vulnerabilities"`
			} `json:"packages"`
		} `json:"results"`
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode osv-scanner JSON: %w", err)
	}

	seen := map[string]finding{}
	for _, result := range report.Results {
		for _, pkg := range result.Packages {
			for _, group := range pkg.Groups {
				for _, id := range group.IDs {
					addOSVFinding(seen, id, pkg.Package.Name, pkg.Package.Version, result.Source.Path)
				}
			}
			for _, vuln := range pkg.Vulnerabilities {
				addOSVFinding(seen, vuln.ID, pkg.Package.Name, pkg.Package.Version, result.Source.Path)
			}
		}
	}

	return sortedFindings(seen), nil
}

func addOSVFinding(seen map[string]finding, id, pkgName, version, source string) {
	if id == "" {
		return
	}
	item := finding{
		Scanner: osvScannerName,
		ID:      id,
		Package: pkgName,
		Version: version,
		Source:  source,
	}
	key := item.Scanner + "\x00" + item.ID + "\x00" + item.Package + "\x00" + item.Version + "\x00" + item.Source
	seen[key] = item
}

func sortedFindings(items map[string]finding) []finding {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	findings := make([]finding, 0, len(keys))
	for _, key := range keys {
		findings = append(findings, items[key])
	}
	return findings
}

func report(findings []finding) {
	allowed, unallowed, failures := classifyFindings(findings, allowlist, time.Now().UTC())

	for _, id := range sortedIDs(allowed) {
		entry := allowlist[id]
		fmt.Fprintf(os.Stderr, "allowed vulnerability: %s (%s) owner=%s expires=%s\n",
			id, entry.Package, entry.Owner, entry.Expires)
		fmt.Fprintf(os.Stderr, "  reason: %s\n", entry.Reason)
		fmt.Fprintf(os.Stderr, "  mitigation: %s\n", entry.Mitigation)
		for _, item := range allowed[id] {
			fmt.Fprintf(os.Stderr, "  - %s: %s %s %s\n", item.Scanner, item.Package, item.Version, item.Source)
		}
	}

	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(2)
	}

	if len(unallowed) > 0 {
		for _, id := range sortedIDs(unallowed) {
			fmt.Fprintf(os.Stderr, "unallowed vulnerability: %s\n", id)
			for _, item := range unallowed[id] {
				fmt.Fprintf(os.Stderr, "  - %s: %s %s %s\n", item.Scanner, item.Package, item.Version, item.Source)
			}
		}
		os.Exit(1)
	}

	if len(allowed) == 0 {
		fmt.Println("vulnerability audit: no vulnerabilities found")
		return
	}

	fmt.Println("vulnerability audit: no unallowed vulnerabilities found")
}

func classifyFindings(
	findings []finding,
	entries map[string]allowEntry,
	now time.Time,
) (map[string][]finding, map[string][]finding, []string) {
	allowed := map[string][]finding{}
	unallowed := map[string][]finding{}
	var failures []string

	for _, item := range findings {
		entry, ok := entries[item.ID]
		if !ok {
			unallowed[item.ID] = append(unallowed[item.ID], item)
			continue
		}
		if err := validateAllowEntry(item.ID, entry, now); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if !findingMatchesEntry(item, entry) {
			unallowed[item.ID] = append(unallowed[item.ID], item)
			continue
		}
		allowed[item.ID] = append(allowed[item.ID], item)
	}

	return allowed, unallowed, failures
}

func findingMatchesEntry(item finding, entry allowEntry) bool {
	if item.Scanner != govulncheckName && item.Scanner != osvScannerName {
		return false
	}
	if entry.ModuleOnly {
		return item.Package == entry.Package && !item.Reachable
	}
	return item.Package == entry.Package || strings.HasPrefix(item.Package, entry.Package+"/")
}

func validateAllowEntry(id string, entry allowEntry, now time.Time) error {
	if entry.Package == "" || entry.Owner == "" || entry.Reason == "" || entry.Mitigation == "" {
		return fmt.Errorf("allowlist entry %s: %w", id, errAllowEntryIncomplete)
	}
	expiry, err := time.Parse(time.DateOnly, entry.Expires)
	if err != nil {
		return fmt.Errorf("allowlist entry %s has invalid expiry %q: %w", id, entry.Expires, err)
	}
	today, err := time.Parse(time.DateOnly, now.UTC().Format(time.DateOnly))
	if err != nil {
		return fmt.Errorf("current date parse failed: %w", err)
	}
	if today.After(expiry) {
		return fmt.Errorf("allowlist entry %s expired on %s: %w", id, entry.Expires, errAllowEntryExpired)
	}
	return nil
}

func sortedIDs(groups map[string][]finding) []string {
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
