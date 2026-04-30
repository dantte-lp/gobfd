package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	govulncheckVersion = "v1.2.0"
	osvScannerVersion  = "v2.3.5"
	scannerTimeout     = 10 * time.Minute
)

var (
	errAllowEntryIncomplete = errors.New("allowlist entry missing package, owner, reason, or mitigation")
	errAllowEntryExpired    = errors.New("allowlist entry expired")
)

type allowEntry struct {
	Package    string
	Owner      string
	Expires    string
	Reason     string
	Mitigation string
}

var allowlist = map[string]allowEntry{
	"GO-2026-4736": {
		Package:    "github.com/osrg/gobgp/v3",
		Owner:      "maintainers",
		Expires:    "2026-07-31",
		Reason:     "GoBGP NEXT_HOP DoS advisory has no fixed version.",
		Mitigation: "Keep GoBGP integration on localhost or a trusted management network until upstream ships a fix.",
	},
}

type finding struct {
	Scanner string
	ID      string
	Package string
	Version string
	Source  string
}

type commandResult struct {
	Stdout   []byte
	Stderr   string
	Code     int
	Err      error
	TimedOut bool
}

func main() {
	var failures []string

	govuln := runGo("run", "golang.org/x/vuln/cmd/govulncheck@"+govulncheckVersion, "-format", "json", "./...")
	printStderr("govulncheck", govuln.Stderr)
	govulnFindings, err := parseGovulncheck(govuln.Stdout)
	if err != nil {
		failures = append(failures, fmt.Sprintf("govulncheck JSON parse failed: %v", err))
	}
	if govuln.TimedOut {
		failures = append(failures, "govulncheck timed out")
	}
	if govuln.Err != nil && len(govulnFindings) == 0 {
		failures = append(failures, fmt.Sprintf("govulncheck failed with exit code %d: %v", govuln.Code, govuln.Err))
	}

	osv := runGo("run", "github.com/google/osv-scanner/v2/cmd/osv-scanner@"+osvScannerVersion, "scan", "-r", "--format", "json", ".")
	printStderr("osv-scanner", osv.Stderr)
	osvFindings, err := parseOSVScanner(osv.Stdout)
	if err != nil {
		failures = append(failures, fmt.Sprintf("osv-scanner JSON parse failed: %v", err))
	}
	if osv.TimedOut {
		failures = append(failures, "osv-scanner timed out")
	}
	if osv.Err != nil && len(osvFindings) == 0 {
		failures = append(failures, fmt.Sprintf("osv-scanner failed with exit code %d: %v", osv.Code, osv.Err))
	}

	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(2)
	}

	all := make([]finding, 0, len(govulnFindings)+len(osvFindings))
	all = append(all, govulnFindings...)
	all = append(all, osvFindings...)
	report(all)
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
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
				OSV   string `json:"osv"`
				Trace []struct {
					Module   string `json:"module"`
					Package  string `json:"package"`
					Function string `json:"function"`
				} `json:"trace"`
			} `json:"finding"`
		}

		err := decoder.Decode(&message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if message.Finding == nil || message.Finding.OSV == "" {
			continue
		}

		item := finding{Scanner: "govulncheck", ID: message.Finding.OSV}
		if len(message.Finding.Trace) > 0 {
			item.Package = message.Finding.Trace[0].Package
			if item.Package == "" {
				item.Package = message.Finding.Trace[0].Module
			}
			item.Source = message.Finding.Trace[0].Function
		}

		key := item.Scanner + "\x00" + item.ID + "\x00" + item.Package + "\x00" + item.Source
		seen[key] = item
	}

	return sortedFindings(seen), nil
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
		return nil, err
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
		Scanner: "osv-scanner",
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
		allowed[item.ID] = append(allowed[item.ID], item)
	}

	return allowed, unallowed, failures
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
