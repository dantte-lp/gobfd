package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec" //nolint:depguard // Audit runner invokes pinned scanners with explicit argument vectors.
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const (
	scannerToolsModule     = "-modfile=tools/go.mod"
	scannerTimeout         = 10 * time.Minute
	govulncheckName        = "govulncheck"
	osvScannerName         = "osv-scanner"
	maxSelectedGraphSize   = 16 << 20
	selectedModuleTemplate = "{{if not .Main}}{{with .Replace}}{{if .Version}}{{.Path}} {{.Version}}" +
		"{{else}}LOCAL-REPLACEMENT{{end}}{{else}}{{.Path}} {{.Version}}{{end}}{{end}}"
)

var (
	errAllowEntryIncomplete = errors.New("allowlist entry missing package, owner, reason, or mitigation")
	errAllowEntryExpired    = errors.New("allowlist entry expired")
	errAllowEntryStale      = errors.New("matches no scanner finding; remove the stale entry from scripts/vuln-audit.go")
	errScannerReportEmpty   = errors.New("scanner returned an empty report")
	errSelectedModuleGraph  = errors.New("invalid selected module graph")
	errPublisherEvidence    = errors.New("invalid publisher vulnerability evidence")
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
		Package: "github.com/osrg/gobgp/v4",
		Owner:   "@dantte-lp",
		Expires: "2026-12-31",
		Reason: "Maintainer decisions 2026-10-13 (gobfd-qj0.8.4) and 2026-10-21 (gobfd-qj0.8.4.1, stable v1): " +
			"GHSA-4p9m-8gc4-rw2h lists v4.4.0 as patched and upstream fix 583080a7 is in v4.10.0, " +
			"but the Go vulnerability database lists no fixed v4 version (golang/vulndb#5035, #6562) and " +
			"limits the advisory to github.com/osrg/gobgp/v4/pkg/server, which GoBFD does not import.",
		Mitigation: "Import only the GoBGP v4 api package, keep the reachable govulncheck gate enabled, " +
			"keep GoBGP integration on localhost or a trusted management network, " +
			"and remove this entry once the Go vulnerability database records a fixed v4 version.",
		ModuleOnly: true,
	},
	"GO-2026-5932": {
		Package:    "golang.org/x/crypto",
		Owner:      "maintainers",
		Expires:    "2026-12-31",
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
	Version  string `json:"version"`
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
	reportDir := flag.String("report-dir", "", "write separate raw scanner reports for each audited graph")
	publisherDirectory := flag.String("publisher-directory", "", "verified managed publisher consumer directory")
	publisherArtifact := flag.String("publisher-artifact", "", "managed publisher binary for the prepared consumer")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "vulnerability audit: unexpected arguments: %s\n", strings.Join(flag.Args(), " "))
		os.Exit(2)
	}
	if *publisherDirectory != "" || *publisherArtifact != "" {
		os.Exit(auditPublisher(*publisherDirectory, *publisherArtifact, *reportDir))
	}

	govulnFindings, failures := auditGovulncheck(*reportDir)
	all := slices.Clone(govulnFindings)
	for _, scope := range []struct{ name, manifest string }{
		{"runtime", "go.mod"},
		{"tools", "tools/go.mod"},
		{"gopls", "tools/gopls/go.mod"},
		{"syft", toolprofile.SyftManifest},
	} {
		findings, scopeFailures := auditOSV(*reportDir, scope.name, scope.manifest)
		all = append(all, findings...)
		failures = append(failures, scopeFailures...)
	}

	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(2)
	}

	report(all)
}

func auditGovulncheck(reportDir string) ([]finding, []string) {
	result := runGo("tool", scannerToolsModule, govulncheckName, "-format", "json", "./...")
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
	if unexpectedScannerExit(result, len(findings), 0) {
		failures = append(failures, fmt.Sprintf("govulncheck failed with exit code %d: %v", result.Code, result.Err))
	}
	return findings, failures
}

func auditOSV(reportDir, scope, manifest string) ([]finding, []string) {
	if err := verifySelectedPackages(manifest); err != nil {
		return nil, []string{fmt.Sprintf("verify %s package graph: %v", scope, err)}
	}
	selection := runGo("list", "-mod=readonly", "-modfile="+manifest, "-m", "-f",
		selectedModuleTemplate, "all")
	if selection.Err != nil || selection.TimedOut {
		return nil, []string{fmt.Sprintf("select %s module graph: %v", scope, selection.Err)}
	}
	return auditSelectedOSV(reportDir, scope, manifest, selection.Stdout)
}

func auditSelectedOSV(reportDir, scope, manifest string, selection []byte) ([]finding, []string) {
	input, count, err := selectedOSVManifest(selection)
	if err != nil {
		return nil, []string{fmt.Sprintf("select %s module graph: %v", scope, err)}
	}
	if reportErr := writeScannerReport(reportDir, scope+"-selected-go.mod", input); reportErr != nil {
		return nil, []string{reportErr.Error()}
	}
	directory, err := os.MkdirTemp("", "gobfd-osv-selected-")
	if err != nil {
		return nil, []string{fmt.Sprintf("create selected module scan directory: %v", err)}
	}
	var findings []finding
	var failures []string
	func() {
		defer func() {
			if err := os.RemoveAll(directory); err != nil {
				failures = append(failures, fmt.Sprintf("remove selected module scan directory: %v", err))
			}
		}()
		path := filepath.Join(directory, "go.mod")
		// #nosec G304 -- Fixed basename in the newly created private scan directory.
		if writeErr := os.WriteFile(path, input, 0o600); writeErr != nil {
			failures = []string{fmt.Sprintf("write selected module scan input: %v", writeErr)}
			return
		}
		fmt.Fprintf(os.Stderr, "vulnerability audit: %s selected graph contains %d modules\n", scope, count)
		result := runGo(
			"tool", scannerToolsModule, osvScannerName,
			"scan",
			"--lockfile="+path,
			"--format",
			"json",
			"--all-packages",
			"--no-call-analysis=go",
		)
		printStderr(scope+" "+osvScannerName, result.Stderr)
		findings, failures = collectOSVResult(result, reportDir, scope, manifest, selection)
	}()
	return findings, failures
}

func auditPublisher(directory, artifactPath, reportDir string) int {
	evidence, findings, err := collectPublisherEvidence(directory, artifactPath, reportDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publisher vulnerability audit: %v\n", err)
		return 2
	}
	allowed, unallowed, failures := classifyPublisherFindings(findings, evidence, time.Now().UTC())
	for _, failure := range failures {
		fmt.Fprintln(os.Stderr, failure)
	}
	dispositions := publisherDispositions()
	for _, id := range sortedIDs(allowed) {
		disposition := dispositions[id]
		fmt.Fprintf(os.Stderr, "publisher disposition: %s %s %s %s owner=@dantte-lp expires=%s artifact_sha256=%s\n",
			id, disposition.module, disposition.version, disposition.classification,
			publisherDispositionExpiry, evidence.artifactSHA256)
	}
	for _, id := range sortedIDs(unallowed) {
		for _, item := range unallowed[id] {
			fmt.Fprintf(os.Stderr, "unallowed publisher vulnerability: %s %s %s %s\n",
				id, item.Scanner, item.Package, item.Version)
		}
	}
	if len(failures) != 0 {
		return 2
	}
	if len(unallowed) != 0 {
		return 1
	}
	fmt.Println("publisher vulnerability audit: no unallowed vulnerabilities found")
	return 0
}

func collectPublisherEvidence(directory, artifactPath, reportDir string) (
	_ publisherAuditEvidence, _ []finding, returnErr error,
) {
	var evidence publisherAuditEvidence
	if !filepath.IsAbs(directory) || !filepath.IsAbs(artifactPath) || !filepath.IsAbs(reportDir) {
		return evidence, nil, fmt.Errorf("publisher paths must be absolute: %w", errPublisherEvidence)
	}
	artifact, identity, digest, err := openPublisherArtifact(artifactPath)
	if err != nil {
		return evidence, nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, artifact.Close()) }()
	evidence.artifactSHA256 = digest
	evidence.buildInfo, evidence.profileSHA256, err = readPublisherArtifactMetadata(artifact)
	if err != nil {
		return evidence, nil, err
	}
	evidence.selected, evidence.packages, err = collectPublisherGoGraph(directory, reportDir)
	if err != nil {
		return evidence, nil, err
	}
	evidence.nativeReport, err = collectPublisherNativeReport(artifact, reportDir)
	if err != nil {
		return evidence, nil, err
	}
	findings, failures := auditSelectedOSV(reportDir, "publisher", filepath.Join(directory, "go.mod"), evidence.selected)
	if len(failures) != 0 {
		return evidence, findings, fmt.Errorf("publisher selected scan failed: %s: %w",
			strings.Join(failures, "; "), errPublisherEvidence)
	}
	return evidence, findings, finishPublisherEvidence(artifact, artifactPath, identity, reportDir, evidence)
}

func readPublisherArtifactMetadata(file *os.File) (*debug.BuildInfo, string, error) {
	profile, err := os.ReadFile(toolprofile.GoReleaserSource)
	if err != nil {
		return nil, "", fmt.Errorf("read publisher source profile: %w", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(profile))
	if digest != publisherApprovedProfileSHA256 {
		return nil, "", fmt.Errorf("publisher source profile differs from approval: %w", errPublisherEvidence)
	}
	info, err := buildinfo.Read(file)
	if err != nil {
		return nil, "", fmt.Errorf("read publisher BuildInfo: %w", err)
	}
	return info, digest, nil
}

func finishPublisherEvidence(
	file *os.File, path string, identity os.FileInfo, reportDir string, evidence publisherAuditEvidence,
) error {
	digest, err := hashPublisherArtifact(file, path, identity)
	if err != nil {
		return err
	}
	if digest != evidence.artifactSHA256 {
		return fmt.Errorf("publisher artifact changed during audit: %w", errPublisherEvidence)
	}
	return writePublisherArtifactReports(reportDir, evidence)
}

func openPublisherArtifact(path string) (_ *os.File, _ os.FileInfo, _ string, returnErr error) {
	// #nosec G304 -- cictl supplies its managed ELF; inode checks and repeated digests bind the audit read.
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open publisher artifact: %w", err)
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, file.Close())
		}
	}()
	identity, err := file.Stat()
	if err != nil {
		return nil, nil, "", fmt.Errorf("stat publisher artifact: %w", err)
	}
	if !identity.Mode().IsRegular() {
		return nil, nil, "", fmt.Errorf("publisher artifact is not regular: %w", errPublisherEvidence)
	}
	digest, err := hashPublisherArtifact(file, path, identity)
	if err != nil {
		return nil, nil, "", err
	}
	return file, identity, digest, nil
}

func collectPublisherNativeReport(artifact *os.File, reportDir string) ([]byte, error) {
	resolved := runGo("tool", "-n", scannerToolsModule, govulncheckName)
	if resolved.Err != nil || resolved.TimedOut || resolved.Code != 0 {
		return nil, fmt.Errorf("resolve publisher scanner: %w", errors.Join(resolved.Err, errPublisherEvidence))
	}
	executable := strings.TrimSuffix(string(resolved.Stdout), "\n")
	if !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\r\n") {
		return nil, fmt.Errorf("publisher scanner resolution is not one absolute executable: %w", errPublisherEvidence)
	}
	result := runPublisherScanner(executable, []string{"-mode=binary", "-json"}, artifact)
	printStderr("publisher "+govulncheckName, result.Stderr)
	if err := writeScannerReport(reportDir, "publisher-govulncheck.json", result.Stdout); err != nil {
		return nil, err
	}
	if result.Err != nil || result.TimedOut || result.Code != 0 {
		return nil, fmt.Errorf("publisher symbol scanner failed: %w", errors.Join(result.Err, errPublisherEvidence))
	}
	return result.Stdout, nil
}

// Direct execution preserves the descriptor; a go-tool intermediary need not.
func runPublisherScanner(executable string, args []string, artifact *os.File) commandResult {
	ctx, cancel := context.WithTimeout(context.Background(), scannerTimeout)
	defer cancel()
	// #nosec G204 -- Owning Go resolves the pinned scanner; arguments are fixed and the managed ELF is FD-bound.
	command := exec.CommandContext(ctx, executable, append(slices.Clone(args), "/proc/self/fd/3")...)
	command.ExtraFiles = []*os.File{artifact}
	stdout := publisherOutputBuffer{limit: maxSelectedGraphSize}
	stderr := publisherOutputBuffer{limit: 1 << 20}
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	code := 0
	if err != nil {
		code = 1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		}
	}
	return commandResult{
		Stdout: stdout.Bytes(), Stderr: stderr.String(), Code: code,
		Err: err, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
}

type publisherOutputBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *publisherOutputBuffer) Write(data []byte) (int, error) {
	if len(data) > buffer.limit-buffer.buffer.Len() {
		return 0, fmt.Errorf("publisher scanner output exceeds its bound: %w", errPublisherEvidence)
	}
	written, err := buffer.buffer.Write(data)
	if err != nil {
		return written, fmt.Errorf("write bounded publisher scanner output: %w", err)
	}
	return written, nil
}

func (buffer *publisherOutputBuffer) Bytes() []byte { return buffer.buffer.Bytes() }

func (buffer *publisherOutputBuffer) String() string { return buffer.buffer.String() }

func writePublisherArtifactReports(reportDir string, evidence publisherAuditEvidence) error {
	info, err := json.Marshal(evidence.buildInfo)
	if err != nil {
		return fmt.Errorf("encode publisher BuildInfo: %w", err)
	}
	if reportErr := writeScannerReport(reportDir, "publisher-buildinfo.json", info); reportErr != nil {
		return reportErr
	}
	if reportErr := writeScannerReport(reportDir, "publisher-artifact-sha256.txt",
		[]byte(evidence.artifactSHA256+"\n")); reportErr != nil {
		return reportErr
	}
	receipt, err := json.Marshal(struct {
		ArtifactSHA256      string `json:"artifact_sha256"`
		SourceProfileSHA256 string `json:"source_profile_sha256"`
	}{evidence.artifactSHA256, evidence.profileSHA256})
	if err != nil {
		return fmt.Errorf("encode publisher artifact receipt: %w", err)
	}
	return writeScannerReport(reportDir, "publisher-artifact.json", receipt)
}

func hashPublisherArtifact(file *os.File, path string, identity os.FileInfo) (string, error) {
	current, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("check publisher artifact path: %w", err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(identity, current) {
		return "", fmt.Errorf("publisher artifact inode changed: %w", errPublisherEvidence)
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		return "", fmt.Errorf("seek publisher artifact: %w", seekErr)
	}
	hash := sha256.New()
	const limit = 512 << 20
	count, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return "", fmt.Errorf("hash publisher artifact: %w", err)
	}
	if count == 0 || count > limit {
		return "", fmt.Errorf("publisher artifact size is invalid: %w", errPublisherEvidence)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func collectPublisherGoGraph(directory, reportDir string) ([]byte, []byte, error) {
	environment := []string{"CGO_ENABLED=0", "GOWORK=off", "GOTOOLCHAIN=local"}
	selection := runGoWithEnvironment(environment, "-C", directory, "list", "-mod=readonly", "-m", "-json", "all")
	if selection.Err != nil || selection.TimedOut || selection.Code != 0 {
		return nil, nil, fmt.Errorf("collect publisher modules: %w", errors.Join(selection.Err, errPublisherEvidence))
	}
	if err := writeScannerReport(reportDir, "publisher-selected-modules.json", selection.Stdout); err != nil {
		return nil, nil, err
	}
	selected, err := publisherSelectedModules(selection.Stdout, directory)
	if err != nil {
		return nil, nil, err
	}
	packages := runGoWithEnvironment(environment, "-C", directory, "list", "-mod=readonly", "-deps",
		"-f", "{{.ImportPath}}", toolprofile.GoReleaserModule)
	if packages.Err != nil || packages.TimedOut || packages.Code != 0 {
		return nil, nil, fmt.Errorf("collect publisher packages: %w", errors.Join(packages.Err, errPublisherEvidence))
	}
	if reportErr := writeScannerReport(reportDir, "publisher-packages.txt", packages.Stdout); reportErr != nil {
		return nil, nil, reportErr
	}
	return selected, packages.Stdout, nil
}

//nolint:tagliatelle // The owning Go command exports these module JSON field names.
type publisherGoModule struct {
	Path    string             `json:"Path"`
	Version string             `json:"Version"`
	Main    bool               `json:"Main"`
	Replace *publisherGoModule `json:"Replace"`
}

func publisherSelectedModules(data []byte, directory string) ([]byte, error) {
	if len(data) == 0 || len(data) > maxSelectedGraphSize {
		return nil, errPublisherEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	modules := make(map[string]string)
	local := map[string]string{toolprofile.GoReleaserModule: "../source", toolprofile.RefmtModule: publisherRefmtDirectory}
	mainCount := 0
	for {
		var module publisherGoModule
		if err := decoder.Decode(&module); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse publisher selected module: %w", errors.Join(err, errPublisherEvidence))
		}
		if module.Main {
			if module.Path != toolprofile.GoReleaserConsumer || module.Replace != nil {
				return nil, errPublisherEvidence
			}
			mainCount++
			continue
		}
		var err error
		module, err = publisherEffectiveModule(module, directory, local)
		if err != nil {
			return nil, err
		}
		if _, duplicate := modules[module.Path]; duplicate {
			return nil, fmt.Errorf("duplicate publisher selected module: %w", errPublisherEvidence)
		}
		modules[module.Path] = module.Version
	}
	if mainCount != 1 || len(local) != 0 {
		return nil, fmt.Errorf("publisher consumer source roles are incomplete: %w", errPublisherEvidence)
	}
	var selected strings.Builder
	for _, path := range slices.Sorted(maps.Keys(modules)) {
		fmt.Fprintf(&selected, "%s %s\n", path, modules[path])
	}
	if _, err := selectedOSVModules([]byte(selected.String())); err != nil {
		return nil, errors.Join(err, errPublisherEvidence)
	}
	return []byte(selected.String()), nil
}

func publisherEffectiveModule(
	module publisherGoModule, directory string, local map[string]string,
) (publisherGoModule, error) {
	if module.Replace == nil {
		return module, nil
	}
	if module.Replace.Version != "" {
		if module.Replace.Path != module.Path {
			return module, fmt.Errorf("publisher module path alias is unapproved: %w", errPublisherEvidence)
		}
		return *module.Replace, nil
	}
	target, found := local[module.Path]
	if !found || module.Replace.Path != target && module.Replace.Path != filepath.Clean(filepath.Join(directory, target)) {
		return module, fmt.Errorf("publisher module %s has an unapproved local replacement: %w",
			module.Path, errPublisherEvidence)
	}
	delete(local, module.Path)
	return module, nil
}

func collectOSVResult(result commandResult, reportDir, scope, manifest string, selection []byte) ([]finding, []string) {
	var failures []string
	reportErr := writeScannerReport(reportDir, scope+"-osv.json", result.Stdout)
	if reportErr != nil {
		failures = append(failures, reportErr.Error())
	}
	findings, scanned, parseErr := parseOSVScanner(result.Stdout)
	for index := range findings {
		findings[index].Source = manifest + " (selected module graph)"
	}
	if parseErr != nil {
		failures = append(failures, fmt.Sprintf("%s osv-scanner JSON parse failed: %v", scope, parseErr))
	}
	selected, selectionErr := selectedOSVModules(selection)
	if selectionErr != nil {
		failures = append(failures, fmt.Sprintf("%s osv-scanner selected graph is invalid: %v", scope, selectionErr))
	} else if !maps.EqualFunc(selected, scanned, func(selectedVersion, scannedVersion string) bool {
		return strings.TrimPrefix(selectedVersion, "v") == strings.TrimPrefix(scannedVersion, "v")
	}) {
		failures = append(failures,
			fmt.Sprintf("%s osv-scanner package coverage differs from the selected Go graph: selected=%d scanned=%d",
				scope, len(selected), len(scanned)))
	}
	if result.TimedOut {
		failures = append(failures, scope+" osv-scanner timed out")
	}
	if unexpectedScannerExit(result, len(findings), 1) {
		failures = append(failures,
			fmt.Sprintf("%s osv-scanner failed with exit code %d: %v", scope, result.Code, result.Err))
	}
	return findings, failures
}

func unexpectedScannerExit(result commandResult, findingCount, findingsExit int) bool {
	if result.Err == nil && result.Code == 0 {
		return false
	}
	return findingsExit <= 0 || result.Code != findingsExit || findingCount == 0
}

// Go selects effective versions; OSV's manifest parser must receive every selection.
func selectedOSVManifest(data []byte) ([]byte, int, error) {
	modules, err := selectedOSVModules(data)
	if err != nil {
		return nil, 0, err
	}
	var output strings.Builder
	output.WriteString("module example.invalid/gobfd-osv-selected\n\nrequire (\n")
	for _, path := range slices.Sorted(maps.Keys(modules)) {
		fmt.Fprintf(&output, "\t%q %q\n", path, modules[path])
	}
	output.WriteString(")\n")
	return []byte(output.String()), len(modules), nil
}

func selectedOSVModules(data []byte) (map[string]string, error) {
	if len(data) > maxSelectedGraphSize {
		return nil, errSelectedModuleGraph
	}
	modules := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || strings.ContainsAny(fields[0][:1], ". /\\") || !strings.HasPrefix(fields[1], "v") {
			return nil, errSelectedModuleGraph
		}
		if previous, found := modules[fields[0]]; found && previous != fields[1] {
			return nil, errSelectedModuleGraph
		}
		modules[fields[0]] = fields[1]
	}
	if len(modules) == 0 {
		return nil, errSelectedModuleGraph
	}
	return modules, nil
}

func verifySelectedPackages(manifest string) error {
	pattern := "tool"
	if manifest == "go.mod" {
		pattern = "./..."
	}
	args := []string{"list", "-buildvcs=false", "-mod=readonly", "-modfile=" + manifest}
	var environment []string
	if manifest == toolprofile.SyftManifest {
		args = append(args, "-tags="+toolprofile.SyftBuildTags)
		environment = []string{"CGO_ENABLED=0", "GOWORK=off"}
	}
	args = append(args, "-deps", "-f", "{{.ImportPath}}", pattern)
	result := runGoWithEnvironment(environment, args...)
	if result.Err != nil || result.TimedOut {
		printStderr("selected packages "+manifest, result.Stderr)
		return fmt.Errorf("list selected packages: %w", errors.Join(errSelectedModuleGraph, result.Err))
	}
	return checkExcludedPackages(result.Stdout)
}

func checkExcludedPackages(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || len(data) > maxSelectedGraphSize {
		return errSelectedModuleGraph
	}
	for path := range strings.FieldsSeq(string(data)) {
		for _, excluded := range []string{
			"golang.org/x/crypto/openpgp", "github.com/osrg/gobgp/v4/pkg/server", "github.com/docker/distribution", "rsc.io/pdf",
		} {
			if path == excluded || strings.HasPrefix(path, excluded+"/") {
				return fmt.Errorf("excluded package %s: %w", path, errSelectedModuleGraph)
			}
		}
	}
	return nil
}

func writeScannerReport(reportDir, name string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("write %s: %w", name, errScannerReportEmpty)
	}
	if reportDir == "" {
		return nil
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
	return runGoWithEnvironment(nil, args...)
}

func runGoWithEnvironment(environment []string, args ...string) commandResult {
	fmt.Fprintf(os.Stderr, "vulnerability audit: running go %s\n", strings.Join(args, " "))

	ctx, cancel := context.WithTimeout(context.Background(), scannerTimeout)
	defer cancel()

	// #nosec G204 -- go is fixed and closed callers build argv from pinned scanner versions and fixed manifests.
	cmd := exec.CommandContext(ctx, "go", args...)
	if len(environment) != 0 {
		cmd.Env = append(os.Environ(), environment...)
	}
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
			"%s\x00%s\x00%s\x00%s\x00%s\x00%t",
			item.Scanner,
			item.ID,
			item.Package,
			item.Version,
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
		if item.Version == "" {
			item.Version = frame.Version
		}
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

// These dispositions apply only to the separately measured publisher consumer.
// The maintainer approved the exact scope on 2026-10-31; runtime/tool entries above
// remain independent. Any changed version or missing evidence fails this gate.
const (
	publisherDispositionExpiry     = "2026-11-30"
	publisherApprovedProfileSHA256 = "b627d2e3c4a210a119be66a00457d7e486ca27c3e192a86fba2162fee4a8c450"
	publisherRefmtVersion          = "v0.90.0"
	publisherRefmtDirectory        = "../refmt-local"
	publisherDevelopmentVersion    = "(devel)"
)

type publisherDisposition struct {
	module, version, classification string
}

func publisherDispositions() map[string]publisherDisposition {
	return map[string]publisherDisposition{
		"GO-2022-0635": {"github.com/aws/aws-sdk-go", "v1.55.5", "COMPONENT_NOT_PRESENT"},
		"GO-2022-0646": {"github.com/aws/aws-sdk-go", "v1.55.5", "COMPONENT_NOT_PRESENT"},
		"GO-2026-6225": {
			"github.com/chrismellard/docker-credential-acr-env",
			"v0.0.0-20230304212654-82a0ddb27589", "COMPONENT_NOT_PRESENT",
		},
		// Owning etcd advisories identify 3.4.10/3.3.23 as fixed. GoDB's normal
		// SEMVER range omits that boundary; x/vuln does not read custom_ranges.
		"GO-2024-2528": {"go.etcd.io/etcd/v3", "v3.7.3", "FIXED_SOURCE_RANGE"},
		"GO-2024-2529": {"go.etcd.io/etcd/v3", "v3.7.3", "FIXED_SOURCE_RANGE"},
		"GO-2024-2530": {"go.etcd.io/etcd/v3", "v3.7.3", "FIXED_SOURCE_RANGE"},
		"GO-2026-5932": {"golang.org/x/crypto", "v0.58.0", "VULNERABLE_CODE_NOT_PRESENT"},
	}
}

// Exact aliases come from the same reviewed primary records as the GO IDs.
// Preserve the original ID in each finding and never infer an unknown alias.
func publisherDispositionAliases() map[string]string {
	return map[string]string{
		"CVE-2020-8912": "GO-2022-0635", "GHSA-7f33-f4f5-xwgw": "GO-2022-0635",
		"CVE-2020-8911": "GO-2022-0646", "GHSA-f5pg-7wfw-84q9": "GO-2022-0646",
		"GHSA-j86v-2vjr-fg8f": "GO-2024-2528",
		"GHSA-pm3m-32r3-7mfh": "GO-2024-2529",
		"GHSA-vjg6-93fv-qv64": "GO-2024-2530",
	}
}

func publisherExcluded(path string) bool {
	return slices.ContainsFunc([]string{
		"github.com/aws/aws-sdk-go", "github.com/chrismellard/docker-credential-acr-env",
		"go.etcd.io/etcd", "golang.org/x/crypto/openpgp",
	}, func(prefix string) bool {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	})
}

// The caller collects these outputs from the verified prepared workspace and
// the same inode-bound artifact before classifying any publisher finding.
type publisherAuditEvidence struct {
	selected, packages, nativeReport []byte
	artifactSHA256                   string
	profileSHA256                    string
	buildInfo                        *debug.BuildInfo
}

func classifyPublisherFindings(
	findings []finding, evidence publisherAuditEvidence, now time.Time,
) (map[string][]finding, map[string][]finding, []string) {
	allowed, unallowed := make(map[string][]finding), make(map[string][]finding)
	if err := validatePublisherEvidence(evidence, now); err != nil {
		return allowed, unallowed, []string{err.Error()}
	}
	native, err := parseGovulncheck(evidence.nativeReport)
	if err != nil {
		return allowed, unallowed, []string{err.Error()}
	}
	dispositions, aliases := publisherDispositions(), publisherDispositionAliases()
	for _, item := range append(slices.Clone(findings), native...) {
		id := item.ID
		if canonical, alias := aliases[id]; alias {
			id = canonical
		}
		entry, approved := dispositions[id]
		if !approved || !publisherFindingMatches(item, id, entry) {
			unallowed[item.ID] = append(unallowed[item.ID], item)
			continue
		}
		allowed[id] = append(allowed[id], item)
	}
	return allowed, unallowed, nil
}

func publisherFindingMatches(item finding, canonical string, entry publisherDisposition) bool {
	if item.Package != entry.module || strings.TrimPrefix(item.Version, "v") != strings.TrimPrefix(entry.version, "v") ||
		item.Reachable {
		return false
	}
	if item.Scanner == osvScannerName {
		return true
	}
	// The other six vulnerable components are absent from the delivered ELF;
	// their appearance in a binary report invalidates the disposition.
	return item.Scanner == govulncheckName && canonical == "GO-2026-5932" && item.Source == ""
}

func validatePublisherEvidence(evidence publisherAuditEvidence, now time.Time) error {
	entry := allowEntry{
		Package: toolprofile.GoReleaserModule, Owner: "@dantte-lp", Expires: publisherDispositionExpiry,
		Reason: "Publisher-only maintainer approval on 2026-10-31", Mitigation: "Reject changed or incomplete evidence",
	}
	if err := validateAllowEntry("publisher", entry, now); err != nil {
		return errors.Join(errPublisherEvidence, err)
	}
	if evidence.profileSHA256 != publisherApprovedProfileSHA256 {
		return fmt.Errorf("publisher source profile differs from approval: %w", errPublisherEvidence)
	}
	if !validPublisherDigest(evidence.artifactSHA256) || evidence.buildInfo == nil ||
		evidence.buildInfo.Main.Path != toolprofile.GoReleaserModule || evidence.buildInfo.Main.Version != "v2.19.0" {
		return fmt.Errorf("publisher artifact identity is missing or unapproved: %w", errPublisherEvidence)
	}
	selected, err := selectedOSVModules(evidence.selected)
	if err != nil {
		return errors.Join(errPublisherEvidence, err)
	}
	for _, disposition := range publisherDispositions() {
		if selected[disposition.module] != disposition.version {
			return fmt.Errorf("publisher selected version for %s is unapproved: %w", disposition.module, errPublisherEvidence)
		}
	}
	if selected[toolprofile.GoReleaserModule] != "v2.19.0" || selected[toolprofile.RefmtModule] != publisherRefmtVersion {
		return fmt.Errorf("publisher source component versions differ from approval: %w", errPublisherEvidence)
	}
	if packageErr := validatePublisherPackages(evidence.packages); packageErr != nil {
		return packageErr
	}
	modules, err := publisherArtifactModules(evidence.buildInfo, selected)
	if err != nil {
		return err
	}
	if coverageErr := validatePublisherPackageCoverage(evidence.packages, modules); coverageErr != nil {
		return coverageErr
	}
	return validatePublisherNativeReport(evidence.nativeReport, evidence.buildInfo.GoVersion, modules, now)
}

func validatePublisherPackageCoverage(data []byte, modules map[string]string) error {
	packages := strings.Fields(string(data))
	for module := range modules {
		// govulncheck's exact compiler component has no "stdlib" import path.
		// Its presence/version are required separately by native SBOM equality.
		if module == "stdlib" {
			continue
		}
		if !slices.ContainsFunc(packages, func(path string) bool {
			return path == module || strings.HasPrefix(path, module+"/")
		}) {
			return fmt.Errorf("publisher package proof omits artifact module %s: %w", module, errPublisherEvidence)
		}
	}
	return nil
}

func validPublisherDigest(value string) bool {
	if len(value) != 64 || strings.Trim(value, "0") == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validatePublisherPackages(data []byte) error {
	if len(data) > maxSelectedGraphSize || len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("publisher package graph is missing or oversized: %w", errPublisherEvidence)
	}
	mainPresent := false
	for path := range strings.FieldsSeq(string(data)) {
		mainPresent = mainPresent || path == toolprofile.GoReleaserModule
		if publisherExcluded(path) {
			return fmt.Errorf("publisher imports excluded package %s: %w", path, errPublisherEvidence)
		}
	}
	if !mainPresent {
		return fmt.Errorf("publisher main package is absent: %w", errPublisherEvidence)
	}
	return nil
}

func publisherArtifactModules(info *debug.BuildInfo, selected map[string]string) (map[string]string, error) {
	image := strings.TrimPrefix(toolprofile.CompilerImage, "docker.io/library/golang:")
	compiler, _, found := strings.Cut(image, "-trixie@")
	if !found || info.GoVersion != "go"+compiler {
		return nil, fmt.Errorf("publisher compiler differs from the pinned compiler: %w", errPublisherEvidence)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" {
		return nil, fmt.Errorf("publisher build settings are incomplete: %w", errPublisherEvidence)
	}
	modules := map[string]string{info.Main.Path: info.Main.Version, "stdlib": info.GoVersion}
	for _, dependency := range info.Deps {
		module, err := publisherArtifactModule(dependency)
		if err != nil {
			return nil, err
		}
		if _, exists := modules[module.Path]; exists || module.Version == "" || selected[module.Path] != module.Version {
			return nil, fmt.Errorf("publisher dependency %s differs from selected graph: %w", module.Path, errPublisherEvidence)
		}
		if publisherExcluded(module.Path) {
			return nil, fmt.Errorf("publisher contains excluded component %s: %w", module.Path, errPublisherEvidence)
		}
		modules[module.Path] = module.Version
	}
	if modules["golang.org/x/crypto"] != "v0.58.0" ||
		modules["github.com/osscontainertools/docker-credential-acr"] != "v1.0.0" {
		return nil, fmt.Errorf("publisher crypto/helper identity differs from approval: %w", errPublisherEvidence)
	}
	return modules, nil
}

func publisherArtifactModule(dependency *debug.Module) (debug.Module, error) {
	if dependency == nil {
		return debug.Module{}, fmt.Errorf("publisher dependency is missing: %w", errPublisherEvidence)
	}
	module := *dependency
	// BuildInfo describes this authenticated local component as (devel),
	// while its original identity remains the source ancestry in the Go graph.
	if module.Path == toolprofile.RefmtModule {
		if module.Version != publisherRefmtVersion || module.Sum != "" || module.Replace == nil {
			return module, fmt.Errorf("publisher Refmt source ancestry is unapproved: %w", errPublisherEvidence)
		}
		if module.Replace.Path != publisherRefmtDirectory || module.Replace.Version != publisherDevelopmentVersion ||
			module.Replace.Sum != "" || module.Replace.Replace != nil {
			return module, fmt.Errorf("publisher Refmt local replacement is unapproved: %w", errPublisherEvidence)
		}
		return module, nil
	}
	if module.Replace == nil {
		return module, nil
	}
	if module.Replace.Version == "" || module.Replace.Version == publisherDevelopmentVersion {
		return module, fmt.Errorf("publisher has an unapproved local dependency: %w", errPublisherEvidence)
	}
	if module.Path != module.Replace.Path {
		return module, fmt.Errorf("publisher artifact module path alias: %w", errPublisherEvidence)
	}
	return *module.Replace, nil
}

type publisherNativeConfig struct {
	Scanner  string    `json:"scanner_name"`
	Mode     string    `json:"scan_mode"`
	Level    string    `json:"scan_level"`
	Database string    `json:"db"`
	Modified time.Time `json:"db_last_modified"`
}

func (config publisherNativeConfig) validate(now time.Time) error {
	if config.Scanner != govulncheckName || config.Mode != "binary" || config.Level != "symbol" ||
		config.Database != "https://vuln.go.dev" || config.Modified.IsZero() || config.Modified.After(now) ||
		now.Sub(config.Modified) > 7*24*time.Hour {
		return fmt.Errorf("publisher symbol scanner configuration is unapproved or stale: %w", errPublisherEvidence)
	}
	return nil
}

type publisherNativeSBOM struct {
	GoVersion string `json:"go_version"`
	Modules   []struct {
		Path    string `json:"path"`
		Version string `json:"version"`
	} `json:"modules"`
}

func (sbom publisherNativeSBOM) validate(goVersion string, modules map[string]string) error {
	observed := make(map[string]string, len(sbom.Modules))
	localRefmt := 0
	for _, module := range sbom.Modules {
		if module.Path == toolprofile.RefmtModule {
			return fmt.Errorf("publisher symbol report omits the local Refmt role: %w", errPublisherEvidence)
		}
		if module.Path == publisherRefmtDirectory && module.Version == publisherDevelopmentVersion &&
			modules[toolprofile.RefmtModule] == publisherRefmtVersion {
			localRefmt++
			module.Path, module.Version = toolprofile.RefmtModule, publisherRefmtVersion
		}
		if _, exists := observed[module.Path]; exists {
			return fmt.Errorf("duplicate publisher binary module: %w", errPublisherEvidence)
		}
		observed[module.Path] = module.Version
	}
	if _, containsRefmt := modules[toolprofile.RefmtModule]; containsRefmt && localRefmt != 1 {
		return fmt.Errorf("publisher symbol report has incomplete local Refmt coverage: %w", errPublisherEvidence)
	}
	if sbom.GoVersion != goVersion || !maps.Equal(modules, observed) {
		return fmt.Errorf("publisher symbol report differs from artifact BuildInfo: %w", errPublisherEvidence)
	}
	return nil
}

func validatePublisherNativeReport(data []byte, goVersion string, modules map[string]string, now time.Time) error {
	if len(data) == 0 || len(data) > maxSelectedGraphSize {
		return fmt.Errorf("publisher symbol evidence is missing or oversized: %w", errPublisherEvidence)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	configs, sboms := 0, 0
	for {
		var message struct {
			Config *publisherNativeConfig `json:"config"`
			SBOM   *publisherNativeSBOM   `json:"SBOM"` //nolint:tagliatelle // Upstream govulncheck owns this JSON field.
		}
		if err := decoder.Decode(&message); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("decode publisher symbol report: %w", errors.Join(err, errPublisherEvidence))
		}
		if message.Config != nil {
			configs++
			if err := message.Config.validate(now); err != nil {
				return err
			}
		}
		if message.SBOM != nil {
			sboms++
			if err := message.SBOM.validate(goVersion, modules); err != nil {
				return err
			}
		}
	}
	if configs != 1 || sboms != 1 {
		return fmt.Errorf("publisher symbol report has incomplete identity coverage: %w", errPublisherEvidence)
	}
	return nil
}

func parseOSVScanner(data []byte) ([]finding, map[string]string, error) {
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
		return nil, nil, nil
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, nil, fmt.Errorf("decode osv-scanner JSON: %w", err)
	}

	seen := map[string]finding{}
	modules := make(map[string]string)
	var coverageErr error
	for _, result := range report.Results {
		for _, pkg := range result.Packages {
			coverageErr = errors.Join(coverageErr,
				recordOSVModule(modules, pkg.Package.Name, pkg.Package.Version, pkg.Package.Ecosystem))
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

	return sortedFindings(seen), modules, coverageErr
}

func recordOSVModule(modules map[string]string, name, version, ecosystem string) error {
	var result error
	if ecosystem != "Go" || name == "" || version == "" {
		result = fmt.Errorf("OSV package %q is not a complete Go identity: %w", name, errSelectedModuleGraph)
	}
	if _, duplicate := modules[name]; duplicate {
		result = errors.Join(result, fmt.Errorf("duplicate OSV package %q: %w", name, errSelectedModuleGraph))
	}
	modules[name] = version
	return result
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

	for _, failure := range failures {
		fmt.Fprintln(os.Stderr, failure)
	}
	for _, id := range sortedIDs(unallowed) {
		fmt.Fprintf(os.Stderr, "unallowed vulnerability: %s\n", id)
		for _, item := range unallowed[id] {
			fmt.Fprintf(os.Stderr, "  - %s: %s %s %s\n", item.Scanner, item.Package, item.Version, item.Source)
		}
	}

	if len(failures) > 0 {
		os.Exit(2)
	}
	if len(unallowed) > 0 {
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
	reported := map[string]bool{}

	for _, item := range findings {
		reported[item.ID] = true
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

	// An ID that no scanner reports is stale; an ID reported only as a
	// non-matching finding stays in unallowed and is not hidden here.
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		if !reported[id] {
			failures = append(failures, fmt.Errorf("allowlist entry %s: %w", id, errAllowEntryStale).Error())
		}
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
