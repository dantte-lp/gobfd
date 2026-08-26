// Package dependencyinventory validates the repository's offline supply-chain inventory.
//
//nolint:cyclop,depguard,err113,funlen,gocognit,goconst,lll,modernize,perfsprint,tagliatelle // Validation needs rich diagnostics, bounded discovery, and exact external JSON names.
package dependencyinventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// SchemaID is the canonical inventory schema identifier.
const SchemaID = "https://github.com/dantte-lp/gobfd/docs/supply-chain/dependency-inventory.schema.json"

const maxJSONFileBytes = 16 << 20

const (
	depsDevAPIBase          = "https://api.deps.dev/v3"
	depsDevMaxResponseBytes = 1 << 20
	depsDevWorkers          = 4
	pypiAPIBase             = "https://pypi.org/pypi"
	pypiMaxResponseBytes    = 1 << 20
	pypiWorkers             = 4
)

const (
	currentSchemaVersion  = 2
	githubRepositoryParts = 2
)

// Kind classifies a non-Go dependency or a Go module record.
type Kind string

// Dependency kinds represented by the inventory.
const (
	KindGitHubAction  Kind = "github-action"
	KindGoModule      Kind = "go-module"
	KindInteropDaemon Kind = "interop-daemon"
	KindOCIImage      Kind = "oci-image"
	KindPythonPackage Kind = "python-package"
	KindRemoved       Kind = "removed"
	KindTool          Kind = "tool"
)

// ReviewStatus records whether one assessment dimension has current evidence.
type ReviewStatus string

// Supported assessment review statuses.
const (
	ReviewNotApplicable ReviewStatus = "not-applicable"
	ReviewStale         ReviewStatus = "stale"
	ReviewUnverified    ReviewStatus = "unverified"
	ReviewVerified      ReviewStatus = "verified"
)

// PinStatus records whether a dependency reference is immutable.
type PinStatus string

// Supported immutable-pin statuses.
const (
	PinMutable       PinStatus = "mutable"
	PinNotApplicable PinStatus = "not-applicable"
	PinVerified      PinStatus = "verified"
)

// DecisionStatus records the disposition of a dependency assessment.
type DecisionStatus string

// Supported dependency decisions.
const (
	DecisionAdopted  DecisionStatus = "adopted"
	DecisionDeferred DecisionStatus = "deferred"
	DecisionRemoved  DecisionStatus = "removed"
	DecisionRetained DecisionStatus = "retained"
	DecisionStale    DecisionStatus = "stale"
)

// Options identifies the repository and checked inventory files.
type Options struct {
	Root          string
	InventoryPath string
	SchemaPath    string
}

// Inventory is the complete runtime, tool, and non-Go dependency snapshot.
type Inventory struct {
	Schema         string        `json:"$schema"`
	SchemaVersion  int           `json:"schema_version"`
	AuditedAt      string        `json:"audited_at"`
	GoPackageCount int           `json:"go_package_count"`
	Evidence       []Evidence    `json:"evidence"`
	ModuleGraphs   []ModuleGraph `json:"module_graphs"`
	Components     []Component   `json:"components"`
}

// ModuleGraph records one selected Go module build list.
type ModuleGraph struct {
	ID       string   `json:"id"`
	Manifest string   `json:"manifest"`
	Sum      string   `json:"sum"`
	Modules  []Module `json:"modules"`
}

// Module records one external module in a selected build list.
type Module struct {
	Record

	Path     string `json:"path"`
	Version  string `json:"version"`
	Indirect bool   `json:"indirect"`
}

// Component records one declared non-Go dependency or removed direct requirement.
type Component struct {
	Record

	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
}

// Record contains source, channel, assessment, and immutable-pin evidence.
type Record struct {
	SourceLocations []SourceLocation `json:"source_locations"`
	Coordinates     Coordinates      `json:"coordinates"`
	Baseline        string           `json:"baseline,omitempty"`
	Installed       string           `json:"installed"`
	Target          string           `json:"target,omitempty"`
	DeliveryChannel string           `json:"delivery_channel"`
	Assessment      Assessment       `json:"assessment"`
	RepositoryState RepositoryState  `json:"repository_state"`
	ImmutablePin    ImmutablePin     `json:"immutable_pin"`
}

// Assessment groups the six required evidence dimensions and disposition.
type Assessment struct {
	ChannelCurrent  Review   `json:"channel_current"`
	UpstreamCurrent Review   `json:"upstream_current"`
	ReleaseImpact   Review   `json:"release_impact"`
	Security        Review   `json:"security"`
	License         Review   `json:"license"`
	Decision        Decision `json:"decision"`
}

// RepositoryState separates source-repository health from artifact and release-line state.
type RepositoryState struct {
	RepositoryArchived Review `json:"repository_archived"`
	ArtifactAvailable  Review `json:"artifact_available"`
	ReleaseLineEOL     Review `json:"release_line_eol"`
}

// Coordinates provides canonical source or artifact identity.
type Coordinates struct {
	SourceRepository string `json:"source_repository,omitempty"`
	SourceCommit     string `json:"source_commit,omitempty"`
	PURL             string `json:"purl,omitempty"`
	Digest           string `json:"digest,omitempty"`
}

// SourceLocation ties an inventory record to exact repository text.
type SourceLocation struct {
	Path  string `json:"path"`
	Match string `json:"match"`
}

// Review records status, value, and evidence for one assessment dimension.
type Review struct {
	Status      ReviewStatus `json:"status"`
	Value       string       `json:"value,omitempty"`
	EvidenceIDs []string     `json:"evidence_ids"`
}

// Evidence records a reproducible observation supporting one record review.
type Evidence struct {
	ID             string       `json:"id"`
	Source         string       `json:"source"`
	Subject        string       `json:"subject"`
	Review         string       `json:"review"`
	CommandOrQuery string       `json:"command_or_query"`
	Tool           EvidenceTool `json:"tool"`
	ObservedAt     string       `json:"observed_at"`
	Result         string       `json:"result"`
	Hash           string       `json:"hash"`
}

// EvidenceTool identifies the exact tool that produced an evidence record.
type EvidenceTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ImmutablePin records how a dependency identity is pinned.
type ImmutablePin struct {
	Status PinStatus `json:"status"`
	Kind   string    `json:"kind"`
	Value  string    `json:"value"`
}

// Decision records the accepted, deferred, stale, retained, or removed disposition.
type Decision struct {
	Status           DecisionStatus    `json:"status"`
	Reason           string            `json:"reason"`
	Owner            string            `json:"owner"`
	Tracking         string            `json:"tracking"`
	ReviewBy         string            `json:"review_by,omitempty"`
	LicenseException *LicenseException `json:"license_exception,omitempty"`
}

// LicenseException records bounded ownership for an adopted unverified license.
type LicenseException struct {
	Owner    string `json:"owner"`
	ReviewBy string `json:"review_by"`
	Reason   string `json:"reason"`
	Tracking string `json:"tracking"`
}

// Check validates the committed inventory against the current repository.
func Check(ctx context.Context, options Options) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("check dependency inventory: %w", err)
	}
	root, absErr := filepath.Abs(options.Root)
	if absErr != nil {
		return fmt.Errorf("resolve repository root: %w", absErr)
	}
	if schemaErr := checkSchema(filepath.Join(root, options.SchemaPath)); schemaErr != nil {
		return schemaErr
	}
	inv, err := readInventory(filepath.Join(root, options.InventoryPath))
	if err != nil {
		return err
	}
	if validateErr := Validate(inv, root); validateErr != nil {
		return validateErr
	}
	declared, err := discoverDeclaredComponents(ctx, root)
	if err != nil {
		return err
	}
	if compareErr := compareDeclaredComponents(inv.Components, declared); compareErr != nil {
		return compareErr
	}
	for _, graph := range inv.ModuleGraphs {
		if compareErr := compareModuleGraph(ctx, root, graph); compareErr != nil {
			return compareErr
		}
	}
	packageCount, err := goPackageCount(ctx, root)
	if err != nil {
		return err
	}
	if packageCount != inv.GoPackageCount {
		return fmt.Errorf("go package count = %d, inventory records %d", packageCount, inv.GoPackageCount)
	}
	return nil
}

// Build constructs a deterministic inventory snapshot from the current repository manifests.
// Online provenance is added separately by CollectLicenseEvidence during generation.
func Build(ctx context.Context, root string) (Inventory, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Inventory{}, fmt.Errorf("resolve repository root: %w", err)
	}
	packageCount, err := goPackageCount(ctx, absoluteRoot)
	if err != nil {
		return Inventory{}, err
	}
	inv := Inventory{
		Schema:         SchemaID,
		SchemaVersion:  currentSchemaVersion,
		AuditedAt:      "2026-08-26T00:00:00Z",
		GoPackageCount: packageCount,
	}
	for _, definition := range []struct{ id, manifest, sum string }{
		{"runtime", "go.mod", "go.sum"},
		{"tools", "tools/go.mod", "tools/go.sum"},
	} {
		graph := ModuleGraph{ID: definition.id, Manifest: definition.manifest, Sum: definition.sum}
		identities, parseErr := selectedModuleGraph(ctx, absoluteRoot, graph)
		if parseErr != nil {
			return Inventory{}, fmt.Errorf("parse %s: %w", definition.manifest, parseErr)
		}
		sumData, readErr := os.ReadFile(filepath.Join(absoluteRoot, definition.sum))
		if readErr != nil {
			return Inventory{}, fmt.Errorf("read %s: %w", definition.sum, readErr)
		}
		for _, identity := range identities {
			sources := []SourceLocation{}
			pin := ImmutablePin{
				Status: PinVerified,
				Kind:   "selected-module-version",
				Value:  identity.Path + "@" + identity.Version,
			}
			if sumPin, ok := findGoSumPin(sumData, identity.Path, identity.Version); ok {
				sources = append(sources, SourceLocation{Path: definition.sum, Match: sumPin})
				pin = ImmutablePin{Status: PinVerified, Kind: "go-sum", Value: sumPin}
			}
			record := baseRecord(identity.Version, "https://proxy.golang.org", sources, pin)
			record.Target = identity.Version
			policy := "runtime-direct-audited"
			switch {
			case definition.id == "tools":
				policy = "tools-selected-graph"
			case identity.Indirect:
				policy = "runtime-transitive-selected"
			}
			if definition.id == "runtime" {
				record.Baseline = runtimeBaseline(identity.Path)
			}
			if identity.Path == "github.com/osrg/gobgp/v3" {
				policy = "gobgp-v3-risk"
			}
			record.Coordinates = Coordinates{PURL: goModulePURL(identity.Path, identity.Version)}
			record.Assessment = assessmentTemplate(policy)
			if definition.id == "runtime" && policy != "gobgp-v3-risk" {
				record.Assessment.Security.Value = "runtime vulnerability policy covers " + identity.Path + "@" + identity.Version
			}
			record.RepositoryState = repositoryStateTemplate(policy, pin)
			if definition.id == "tools" {
				switch identity.Path {
				case "github.com/tenntenn/text/transform":
					record.Coordinates.SourceRepository = "https://github.com/tenntenn/text"
					record.Assessment.License = Review{
						Status: ReviewVerified, Value: "MIT", EvidenceIDs: []string{},
					}
					record.Assessment.Decision.LicenseException = nil
				case "github.com/chzyer/logex":
					record.Assessment.License = Review{
						Status:      ReviewNotApplicable,
						Value:       "module does not contribute a package to the selected tool closure and is not distributed",
						EvidenceIDs: []string{},
					}
					record.Assessment.Decision.LicenseException = nil
				}
			}
			if identity.Path == "github.com/ovn-kubernetes/libovsdb" {
				record.Coordinates.SourceRepository = "https://github.com/ovn-kubernetes/libovsdb"
				record.RepositoryState.RepositoryArchived = Review{
					Status: ReviewVerified, Value: "active", EvidenceIDs: []string{},
				}
			}
			module := Module{Record: record, Path: identity.Path, Version: identity.Version, Indirect: identity.Indirect}
			graph.Modules = append(graph.Modules, module)
		}
		sort.Slice(graph.Modules, func(i, j int) bool { return graph.Modules[i].Path < graph.Modules[j].Path })
		inv.ModuleGraphs = append(inv.ModuleGraphs, graph)
	}

	declared, err := discoverDeclaredComponents(ctx, absoluteRoot)
	if err != nil {
		return Inventory{}, err
	}
	for _, item := range declared {
		pin := ImmutablePin{Status: PinVerified, Kind: "ecosystem-version", Value: item.Installed}
		assessment := "tool-version-audited"
		if item.Kind == KindPythonPackage {
			pin = ImmutablePin{
				Status: PinVerified,
				Kind:   "uv-lock-artifact",
				Value:  strings.TrimPrefix(item.ID, "python-package:") + "@" + item.Installed + "#" + item.ArtifactHash,
			}
			assessment = "uv-locked-python-island"
		}
		if item.Kind == KindGitHubAction && githubActionSHA(item.Installed) {
			pin = ImmutablePin{Status: PinVerified, Kind: "git-commit", Value: item.Installed}
			assessment = "github-action-audited"
		}
		if item.Kind == KindOCIImage && ociDigestReference(item.Installed) {
			pin = ImmutablePin{Status: PinVerified, Kind: "oci-digest", Value: item.Installed}
			assessment = "oci-digest-audited"
		} else if item.Kind == KindOCIImage {
			pin = ImmutablePin{Status: PinMutable, Kind: "declared-reference", Value: item.Installed}
			assessment = "mutable-declared-deferred"
		}
		record := baseRecord(item.Installed, deliveryChannel(item.Kind), item.Sources, pin)
		record.Target = item.Installed
		record.Coordinates = componentCoordinates(item)
		record.Assessment = assessmentTemplate(assessment)
		record.RepositoryState = repositoryStateTemplate(assessment, pin)
		if item.Kind == KindGitHubAction {
			record.Assessment.License = Review{
				Status: ReviewVerified, Value: githubActionLicense(actionRepository(item.Installed)), EvidenceIDs: []string{},
			}
			record.RepositoryState.ArtifactAvailable.Value = record.Coordinates.SourceCommit
			record.Assessment.Decision.LicenseException = nil
		}
		inv.Components = append(inv.Components, Component{
			ID: item.ID, Kind: item.Kind,
			Record: record,
		})
	}
	inv.Components = append(inv.Components, specialComponents()...)
	applyOCILicenseEvidence(&inv)
	sort.Slice(inv.Components, func(i, j int) bool { return inv.Components[i].ID < inv.Components[j].ID })
	populateEvidence(&inv)
	return inv, nil
}

type depsDevVersion struct {
	VersionKey struct {
		System  string `json:"system"`
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"versionKey"`
	Licenses []string `json:"licenses"`
	Links    []struct {
		Label string `json:"label"`
		URL   string `json:"url"`
	} `json:"links"`
}

type moduleLicenseIdentity struct {
	Path    string
	Version string
}

type moduleLicenseResult struct {
	Licenses         []string
	SourceRepository string
}

type pythonPackageIdentity struct {
	Name         string
	Version      string
	ArtifactHash string
}

type packageLicenseResult struct {
	License          string
	SourceRepository string
	SourceCommit     string
}

type pypiRelease struct {
	Info struct {
		Name              string            `json:"name"`
		Version           string            `json:"version"`
		LicenseExpression string            `json:"license_expression"`
		License           string            `json:"license"`
		Classifiers       []string          `json:"classifiers"`
		ProjectURLs       map[string]string `json:"project_urls"`
	} `json:"info"`
	URLs []struct {
		Digests struct {
			SHA256 string `json:"sha256"`
		} `json:"digests"`
	} `json:"urls"`
}

type moduleLicenseOverride struct {
	Value            string
	Source           string
	Command          string
	SourceRepository string
	SourceCommit     string
}

type ociLicenseOverride struct {
	SourceRepository string
	SourceCommit     string
	LicenseFile      string
	SPDX             string
	LicenseHash      string
	SecondFile       string
	SecondHash       string
}

// CollectLicenseEvidence resolves exact selected Go module versions through the stable deps.dev v3 API.
// It deliberately leaves records without detected SPDX expressions unverified for explicit resolution.
func CollectLicenseEvidence(ctx context.Context, inv *Inventory) error {
	identities := make([]moduleLicenseIdentity, 0)
	seen := make(map[moduleLicenseIdentity]struct{})
	for _, graph := range inv.ModuleGraphs {
		for _, module := range graph.Modules {
			identity := moduleLicenseIdentity{Path: module.Path, Version: module.Version}
			if _, exists := seen[identity]; exists {
				continue
			}
			seen[identity] = struct{}{}
			identities = append(identities, identity)
		}
	}
	for _, component := range inv.Components {
		identity, ok := declaredToolModule(component.ID, component.Installed)
		if !ok {
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Path == identities[j].Path {
			return identities[i].Version < identities[j].Version
		}
		return identities[i].Path < identities[j].Path
	})

	client := &http.Client{Timeout: 20 * time.Second}
	results := make(map[moduleLicenseIdentity]moduleLicenseResult, len(identities))
	jobs := make(chan moduleLicenseIdentity)
	errCh := make(chan error, len(identities))
	var resultMu sync.Mutex
	var workers sync.WaitGroup
	for range depsDevWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for identity := range jobs {
				result, err := fetchDepsDevLicense(ctx, client, identity)
				if err != nil {
					errCh <- err
					continue
				}
				resultMu.Lock()
				results[identity] = result
				resultMu.Unlock()
			}
		}()
	}
	for _, identity := range identities {
		select {
		case jobs <- identity:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return fmt.Errorf("collect deps.dev licenses: %w", ctx.Err())
		}
	}
	close(jobs)
	workers.Wait()
	close(errCh)
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("collect deps.dev licenses: %w", err)
	}

	for graphIndex := range inv.ModuleGraphs {
		for moduleIndex := range inv.ModuleGraphs[graphIndex].Modules {
			module := &inv.ModuleGraphs[graphIndex].Modules[moduleIndex]
			if module.Assessment.License.Status == ReviewNotApplicable {
				continue
			}
			result := results[moduleLicenseIdentity{Path: module.Path, Version: module.Version}]
			if len(result.Licenses) == 0 {
				if override, ok := exactModuleLicense(module.Path, module.Version); ok {
					module.Assessment.License = Review{Status: ReviewVerified, Value: override.Value, EvidenceIDs: []string{}}
					module.Assessment.Decision.LicenseException = nil
					module.Coordinates.SourceRepository = override.SourceRepository
					module.Coordinates.SourceCommit = override.SourceCommit
					continue
				}
				module.Assessment.License = Review{
					Status:      ReviewUnverified,
					Value:       "deps.dev returned no detected SPDX license expressions for the exact module version",
					EvidenceIDs: []string{},
				}
				continue
			}
			module.Assessment.License = Review{
				Status:      ReviewVerified,
				Value:       strings.Join(result.Licenses, "; "),
				EvidenceIDs: []string{},
			}
			module.Assessment.Decision.LicenseException = nil
			if module.Coordinates.SourceRepository == "" {
				module.Coordinates.SourceRepository = result.SourceRepository
			}
		}
	}
	pypiResults, err := collectPyPILicenses(ctx, inv)
	if err != nil {
		return err
	}
	for componentIndex := range inv.Components {
		component := &inv.Components[componentIndex]
		var result packageLicenseResult
		var ok bool
		switch component.Kind {
		case KindPythonPackage:
			result, ok = pypiResults[pythonPackageIdentity{
				Name: strings.TrimPrefix(component.ID, "python-package:"), Version: component.Installed,
				ArtifactHash: component.Coordinates.Digest,
			}]
		case KindTool:
			if packageName, found := declaredToolPyPIName(component.ID); found {
				result, ok = pypiResultForTool(pypiResults, packageName, component.Installed)
			} else if identity, found := declaredToolModule(component.ID, component.Installed); found {
				moduleResult := results[identity]
				if len(moduleResult.Licenses) > 0 {
					result = packageLicenseResult{
						License: strings.Join(moduleResult.Licenses, "; "), SourceRepository: moduleResult.SourceRepository,
					}
					component.Coordinates.PURL = goModulePURL(identity.Path, identity.Version)
					ok = true
				}
			} else if override, found := exactDeclaredToolLicense(component.ID, component.Installed); found {
				result = packageLicenseResult{
					License: override.Value, SourceRepository: override.SourceRepository, SourceCommit: override.SourceCommit,
				}
				ok = true
			}
		case KindGitHubAction, KindGoModule, KindInteropDaemon, KindOCIImage, KindRemoved:
			continue
		}
		if !ok {
			continue
		}
		component.Assessment.License = Review{Status: ReviewVerified, Value: result.License, EvidenceIDs: []string{}}
		component.Assessment.Decision.LicenseException = nil
		if result.SourceRepository != "" {
			component.Coordinates.SourceRepository = result.SourceRepository
		}
		if result.SourceCommit != "" {
			component.Coordinates.SourceCommit = result.SourceCommit
		}
		if component.Kind == KindTool {
			if packageName, found := declaredToolPyPIName(component.ID); found {
				component.Coordinates.PURL = "pkg:pypi/" + packageName + "@" + component.Installed
			}
		}
	}
	populateEvidence(inv)
	return nil
}

func fetchDepsDevLicense(
	ctx context.Context,
	client *http.Client,
	identity moduleLicenseIdentity,
) (moduleLicenseResult, error) {
	endpoint := depsDevVersionURL(identity.Path, identity.Version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return moduleLicenseResult{}, fmt.Errorf("create deps.dev request for %s@%s: %w", identity.Path, identity.Version, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return moduleLicenseResult{}, fmt.Errorf("query deps.dev for %s@%s: %w", identity.Path, identity.Version, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return moduleLicenseResult{}, fmt.Errorf("query deps.dev for %s@%s: HTTP %s", identity.Path, identity.Version, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, depsDevMaxResponseBytes+1))
	if err != nil {
		return moduleLicenseResult{}, fmt.Errorf("read deps.dev response for %s@%s: %w", identity.Path, identity.Version, err)
	}
	if len(body) > depsDevMaxResponseBytes {
		return moduleLicenseResult{}, fmt.Errorf("deps.dev response for %s@%s exceeds %d bytes", identity.Path, identity.Version, depsDevMaxResponseBytes)
	}
	var version depsDevVersion
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&version); err != nil {
		return moduleLicenseResult{}, fmt.Errorf("decode deps.dev response for %s@%s: %w", identity.Path, identity.Version, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return moduleLicenseResult{}, fmt.Errorf("deps.dev response for %s@%s contains multiple JSON values", identity.Path, identity.Version)
	}
	if version.VersionKey.System != "GO" || version.VersionKey.Name != identity.Path || version.VersionKey.Version != identity.Version {
		return moduleLicenseResult{}, fmt.Errorf("deps.dev identity mismatch for %s@%s", identity.Path, identity.Version)
	}
	licenses := slices.Clone(version.Licenses)
	sort.Strings(licenses)
	licenses = slices.Compact(licenses)
	result := moduleLicenseResult{Licenses: licenses}
	for _, link := range version.Links {
		if link.Label == "SOURCE_REPO" && strings.HasPrefix(link.URL, "https://github.com/") {
			result.SourceRepository = strings.TrimSuffix(link.URL, ".git")
			break
		}
	}
	return result, nil
}

func depsDevVersionURL(path, version string) string {
	return depsDevAPIBase + "/systems/go/packages/" + url.PathEscape(path) + "/versions/" + url.PathEscape(version)
}

func collectPyPILicenses(ctx context.Context, inv *Inventory) (map[pythonPackageIdentity]packageLicenseResult, error) {
	identities := make([]pythonPackageIdentity, 0)
	for _, component := range inv.Components {
		if component.Kind != KindPythonPackage {
			continue
		}
		identities = append(identities, pythonPackageIdentity{
			Name: strings.TrimPrefix(component.ID, "python-package:"), Version: component.Installed,
			ArtifactHash: component.Coordinates.Digest,
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Name != identities[j].Name {
			return identities[i].Name < identities[j].Name
		}
		return identities[i].Version < identities[j].Version
	})

	client := &http.Client{Timeout: 20 * time.Second}
	results := make(map[pythonPackageIdentity]packageLicenseResult, len(identities))
	jobs := make(chan pythonPackageIdentity)
	errCh := make(chan error, len(identities))
	var resultMu sync.Mutex
	var workers sync.WaitGroup
	for range pypiWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for identity := range jobs {
				result, err := fetchPyPILicense(ctx, client, pypiAPIBase, identity)
				if err != nil {
					errCh <- err
					continue
				}
				resultMu.Lock()
				results[identity] = result
				resultMu.Unlock()
			}
		}()
	}
	for _, identity := range identities {
		select {
		case jobs <- identity:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil, fmt.Errorf("collect PyPI licenses: %w", ctx.Err())
		}
	}
	close(jobs)
	workers.Wait()
	close(errCh)
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("collect PyPI licenses: %w", err)
	}
	return results, nil
}

func fetchPyPILicense(
	ctx context.Context,
	client *http.Client,
	apiBase string,
	identity pythonPackageIdentity,
) (packageLicenseResult, error) {
	endpoint := pypiVersionURL(apiBase, identity.Name, identity.Version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return packageLicenseResult{}, fmt.Errorf("create PyPI request for %s@%s: %w", identity.Name, identity.Version, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return packageLicenseResult{}, fmt.Errorf("query PyPI for %s@%s: %w", identity.Name, identity.Version, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return packageLicenseResult{}, fmt.Errorf("query PyPI for %s@%s: HTTP %s", identity.Name, identity.Version, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, pypiMaxResponseBytes+1))
	if err != nil {
		return packageLicenseResult{}, fmt.Errorf("read PyPI response for %s@%s: %w", identity.Name, identity.Version, err)
	}
	if len(body) > pypiMaxResponseBytes {
		return packageLicenseResult{}, fmt.Errorf("PyPI response for %s@%s exceeds %d bytes", identity.Name, identity.Version, pypiMaxResponseBytes)
	}
	if err := rejectDuplicateJSONFields(body); err != nil {
		return packageLicenseResult{}, fmt.Errorf("decode PyPI response for %s@%s: %w", identity.Name, identity.Version, err)
	}
	var release pypiRelease
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&release); err != nil {
		return packageLicenseResult{}, fmt.Errorf("decode PyPI response for %s@%s: %w", identity.Name, identity.Version, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return packageLicenseResult{}, fmt.Errorf("PyPI response for %s@%s contains multiple JSON values", identity.Name, identity.Version)
	}
	if normalizePythonPackageName(release.Info.Name) != identity.Name || release.Info.Version != identity.Version {
		return packageLicenseResult{}, fmt.Errorf("PyPI identity mismatch for %s@%s", identity.Name, identity.Version)
	}
	lockedHash := strings.TrimPrefix(identity.ArtifactHash, "sha256:")
	artifactFound := false
	for _, artifact := range release.URLs {
		if artifact.Digests.SHA256 == lockedHash {
			artifactFound = true
			break
		}
	}
	if !artifactFound {
		return packageLicenseResult{}, fmt.Errorf("PyPI artifact hash mismatch for %s@%s", identity.Name, identity.Version)
	}
	license, ok := normalizedPyPILicense(release.Info.LicenseExpression, release.Info.License, release.Info.Classifiers)
	if !ok {
		if override, found := exactPyPILegacyLicense(identity.Name, identity.Version); found {
			return packageLicenseResult{
				License: override.Value, SourceRepository: override.SourceRepository, SourceCommit: override.SourceCommit,
			}, nil
		}
		return packageLicenseResult{}, fmt.Errorf("PyPI release %s@%s has ambiguous or missing license metadata", identity.Name, identity.Version)
	}
	return packageLicenseResult{License: license, SourceRepository: pyPISourceRepository(release.Info.ProjectURLs)}, nil
}

func pypiVersionURL(apiBase, name, version string) string {
	return strings.TrimSuffix(apiBase, "/") + "/" + url.PathEscape(name) + "/" + url.PathEscape(version) + "/json"
}

func normalizePythonPackageName(name string) string {
	return pythonNameSeparator.ReplaceAllString(strings.ToLower(name), "-")
}

func normalizedPyPILicense(expression, legacy string, classifiers []string) (string, bool) {
	expression = strings.TrimSpace(expression)
	if expression != "" && len(expression) <= 256 && spdxExpression.MatchString(expression) {
		return expression, true
	}
	legacyValues := map[string]string{
		"Apache-2.0": "Apache-2.0", "Apache 2.0": "Apache-2.0", "BSD-2-Clause": "BSD-2-Clause",
		"MIT": "MIT", "MPL-2.0": "MPL-2.0", "PSFL": "PSF-2.0",
		"License :: OSI Approved :: MIT License": "MIT",
	}
	if value, ok := legacyValues[strings.TrimSpace(legacy)]; ok {
		return value, true
	}
	classifierValues := map[string]string{
		"License :: OSI Approved :: Apache Software License":              "Apache-2.0",
		"License :: OSI Approved :: MIT License":                          "MIT",
		"License :: OSI Approved :: Mozilla Public License 2.0 (MPL 2.0)": "MPL-2.0",
		"License :: OSI Approved :: Python Software Foundation License":   "PSF-2.0",
	}
	values := make(map[string]struct{})
	for _, classifier := range classifiers {
		if value, ok := classifierValues[classifier]; ok {
			values[value] = struct{}{}
		}
	}
	if len(values) != 1 {
		return "", false
	}
	for value := range values {
		return value, true
	}
	return "", false
}

func pyPISourceRepository(projectURLs map[string]string) string {
	for _, key := range []string{"Source", "Source Code", "Repository", "Homepage"} {
		value := strings.TrimSuffix(projectURLs[key], "/")
		if strings.HasPrefix(value, "https://github.com/") {
			return value
		}
	}
	return ""
}

func pypiResultForTool(
	results map[pythonPackageIdentity]packageLicenseResult,
	name, version string,
) (packageLicenseResult, bool) {
	for identity, result := range results {
		if identity.Name == name && identity.Version == version {
			return result, true
		}
	}
	return packageLicenseResult{}, false
}

func applyOCILicenseEvidence(inv *Inventory) {
	for index := range inv.Components {
		record := &inv.Components[index].Record
		if record.ImmutablePin.Kind != "oci-digest" {
			continue
		}
		license, ok := exactOCILicense(record.ImmutablePin.Value)
		if !ok {
			continue
		}
		record.Coordinates.SourceRepository = "https://github.com/" + license.SourceRepository
		record.Coordinates.SourceCommit = license.SourceCommit
		if license.SPDX == "" {
			record.Assessment.License = Review{
				Status:      ReviewUnverified,
				Value:       "canonical image build source has no independently resolvable license grant at the pinned commit",
				EvidenceIDs: []string{},
			}
			record.Assessment.Decision.LicenseException = &LicenseException{
				Owner: "maintainers", ReviewBy: "2026-09-30",
				Reason:   "canonical image build-source tree has no license grant at the exact commit",
				Tracking: "gobfd-qj0.8.1.9",
			}
			continue
		}
		value := "canonical-build-source:" + license.SPDX + "; license-source-sha256:" + license.LicenseHash
		if license.SecondFile != "" {
			value += "; second-license-source-sha256:" + license.SecondHash
		}
		record.Assessment.License = Review{Status: ReviewVerified, Value: value, EvidenceIDs: []string{}}
		record.Assessment.Decision.LicenseException = nil
	}
}

func exactOCILicense(reference string) (ociLicenseOverride, bool) {
	licenses := map[string]ociLicenseOverride{
		"docker.io/grafana/grafana:13.2.0@sha256:3fd54ae1214669f8355f065ec9f6445d5279a3d77095ab048ca045685272429b": {
			SourceRepository: "grafana/grafana", SourceCommit: "f681b1359f6a0b8ecb9f2c49a88ac72b75bde73b",
			LicenseFile: "LICENSE", SPDX: "AGPL-3.0-only", LicenseHash: "0d96a4ff68ad6d4b6f1f30f713b18d5184912ba8dd389f86aa7710db079abcb0",
		},
		"docker.io/jauderho/gobgp:v3.37.0@sha256:3bb7304d299c42383c738f5bde2464793e2def9c1ff7fa3f25707a5bb10aee37": {
			SourceRepository: "jauderho/dockerfiles", SourceCommit: "8712248e3458297472e4a4f5e4980a529c69bcf3",
			LicenseFile: "LICENSE", SPDX: "BSD-3-Clause", LicenseHash: "78daaa3193f2a3e0dd754da96ee2fc6e63af52b350a89e7667b7bf0f0900d9ae",
		},
		"docker.io/library/debian:trixie-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132": {
			SourceRepository: "debuerreotype/docker-debian-artifacts", SourceCommit: "7935fc7dd049cb343df42037c152f570069d274f",
			LicenseFile: "LICENSE", SPDX: "Apache-2.0", LicenseHash: "b40930bbcf80744c86c46a12bc9da056641d722716c378f5659b9e555ef833e1",
		},
		"docker.io/library/golang:1.27.0-trixie@sha256:ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a": {
			SourceRepository: "docker-library/golang", SourceCommit: "7aa947b3fda4de2c123815ab1873e50bc810569d",
			LicenseFile: "LICENSE", SPDX: "BSD-3-Clause", LicenseHash: "31668de690a9ecc50ba008a701b6bdb4d08ded13df7c319c49466b500eef6f69",
		},
		"docker.io/library/haproxy:3.4.3-trixie@sha256:4def76cf5d2610255d01fa51b37973d67ddee52f979981fc19117e8d0197bbf5": {
			SourceRepository: "docker-library/haproxy", SourceCommit: "40955df2217f5cb77f861e709b239cedd43ff613",
			LicenseFile: "LICENSE", SPDX: "GPL-2.0-only", LicenseHash: "ddb9db7630752f8fdc6898f7c99a99eaeeac5213627ecb093df9c82f56175dc7",
		},
		"docker.io/library/nginx:1.31.4-trixie@sha256:b34848eff6db786b6b1282d3a9c3fd0b5563dfb6d261df4923378b419e0d24f0": {
			SourceRepository: "nginx/docker-nginx", SourceCommit: "b8590bd36b4504b9b847fcf2e98a9111dcae85fa",
			LicenseFile: "LICENSE", SPDX: "BSD-2-Clause", LicenseHash: "5e01e80542c4ee1c1640501bea5bd55e1e2174034343b45e228465c300ce5158",
		},
		"docker.io/library/oraclelinux:10-slim@sha256:965eb786602f98a4bc9ca449295d44468e5c8f2ee9175ae04c29f21bf1a9809d": {
			SourceRepository: "oracle/container-images", SourceCommit: "5c8a1c296acd6e90487cd261d16cf85fd6bcb73f",
			LicenseFile: "LICENSE", SPDX: "LicenseRef-Oracle-Linux-Agreement", LicenseHash: "22802f1802feb6fbc19f77b89e740937d82f11806c7711a3600b23a8d47fe9d0",
		},
		"docker.io/library/python:3.14.7-slim-trixie@sha256:83ff1d245a3d57d04152252d3ef9cb361494d0b3395abd65a5ebe91c401c8e83": {
			SourceRepository: "docker-library/python", SourceCommit: "228f71e70a42ba9f9a092321b971031603bb88ff",
			LicenseFile: "LICENSE", SPDX: "MIT", LicenseHash: "0a3ffdfc4f368b76ae1140b5fbe2f2f69ee76ee00a3024e8a09ca9b2e265186f",
		},
		"docker.io/prom/prometheus:v3.14.0@sha256:5ce7540c3c00ef4ab0c9d2c995c6a5b9c421f44b4a115d97a2c7af3b1c21cbb0": {
			SourceRepository: "prometheus/prometheus", SourceCommit: "d7598b7141418fa35be2b5ec5d0fefb634199610",
			LicenseFile: "LICENSE", SPDX: "Apache-2.0", LicenseHash: "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4",
		},
		"ghcr.io/astral-sh/uv:0.12.6@sha256:88bc6eb1ccd4b82efd0e1b530caffabddf50dc2bf612e66c14ea25b8ee8a4d3d": {
			SourceRepository: "astral-sh/uv", SourceCommit: "210d1f6785e95a8c8c0d53e284408c9be1134700",
			LicenseFile: "LICENSE-APACHE", SPDX: "Apache-2.0 OR MIT", LicenseHash: "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4",
			SecondFile: "LICENSE-MIT", SecondHash: "860e3d7a86b84e6a7012c7a635fc64df475cebc6cce34dfeb73a5982ec58176c",
		},
		"ghcr.io/exa-networks/exabgp:5.0.13@sha256:80f64719841fe6192f5b5a3b46edc27270215521438fae8a704f28d221a4680b": {
			SourceRepository: "Exa-Networks/exabgp", SourceCommit: "3278b32d6f40669b7465aaee93b46e295b4b2a03",
			LicenseFile: "LICENCE.txt", SPDX: "BSD-3-Clause", LicenseHash: "19002288545f4fcb273332f0daef938a5c72c638af2de28ab440cc30ad987632",
		},
		"ghcr.io/holo-routing/holo-bundle@sha256:5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a": {
			SourceRepository: "holo-routing/holo", SourceCommit: "947daeb2811b0cb90f457458f35c3b1a3c989b6b",
			LicenseFile: "LICENSE", SPDX: "MIT", LicenseHash: "eba69e1d2e51315c7bb27f9cad585e6b03829aa2d1ac38d75d1747c939443e9a",
		},
		"quay.io/frrouting/frr:10.7.0@sha256:65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68": {
			SourceRepository: "FRRouting/frr", SourceCommit: "87fe21fda92ce9e2ba3eaf2b0a327bf71ee183ef",
			LicenseFile: "COPYING", SPDX: "GPL-2.0-or-later", LicenseHash: "7bf053957d6c38e39a06a112c60ff35b228d3bd03edbe8c9a03508b051128d16",
		},
	}
	license, ok := licenses[reference]
	return license, ok
}

func exactModuleLicense(path, version string) (moduleLicenseOverride, bool) {
	const gonumLicenseCommit = "59758bd3db1383fa1f02f88eceee1f8568a87039"
	const gonumLicenseHash = "4111f35b1d68ce44998af12c876acdc3cae7cecbbd1cc10866b4e0c324ccee26"
	type exactLicense struct {
		version, repository, commit, file, spdx, hash string
		gonum                                         bool
	}
	licenses := map[string]exactLicense{
		"github.com/hinshun/vt10x": {
			version: "v0.0.0-20220301184237-5011da428d02", repository: "hinshun/vt10x",
			commit: "5011da428d0284a8c86b954d5ca9e4c4f9886975", file: "LICENSE", spdx: "MIT",
			hash: "1607f32fc8a4d8c4e86cc570f2f23e7a5fd449a7c4e9c4bb2ad30a42b54e8de9",
		},
		"github.com/golang/freetype": {
			version: "v0.0.0-20170609003504-e2365dfdc4a0", repository: "golang/freetype",
			commit: "e2365dfdc4a05e4b8299a783240d4a7d5a65d4e4", file: "LICENSE",
			spdx: "FTL OR GPL-2.0-or-later",
			hash: "d3ba056adc2b7909e95681deaae397fb37c97ed491a920f491214f07b62c41d0",
		},
		"github.com/gonum/blas": {
			version: "v0.0.0-20181208220705-f22b278b28ac", repository: "gonum/blas",
			commit: "f22b278b28ac9805aadd613a754a60c35b24ae69", file: "README.md", spdx: "BSD-3-Clause",
			hash: "ebdba0c64f85fb0cd46094176cca01adc856e7e29f4945fe2c6aff17744c60fe", gonum: true,
		},
		"github.com/gonum/floats": {
			version: "v0.0.0-20181209220543-c233463c7e82", repository: "gonum/floats",
			commit: "c233463c7e827fd71a8cdb62dfda0e98f7c39ad5", file: "README.md", spdx: "BSD-3-Clause",
			hash: "715ae8f36ac8509bd952c0baa8b8f2a74e9d20caa25beb244ae3adb92ed85345", gonum: true,
		},
		"github.com/gonum/internal": {
			version: "v0.0.0-20181124074243-f884aa714029", repository: "gonum/internal",
			commit: "f884aa71402950fb2796dbea0d5aa9ef9cfad8ca", file: "README.md", spdx: "BSD-3-Clause",
			hash: "7dcd05f70854ce339ed9b4926e5b4984596c3fc44cea66bd96614ed085fc4e5d", gonum: true,
		},
		"github.com/gonum/lapack": {
			version: "v0.0.0-20181123203213-e4cdc5a0bff9", repository: "gonum/lapack",
			commit: "e4cdc5a0bff924bb10be88482e635bd40429f65e", file: "README.md", spdx: "BSD-3-Clause",
			hash: "5856abd79c20e164fecfec2dc2de107b5fec8dd9fc84a436d0672fa520e26153", gonum: true,
		},
		"github.com/gonum/matrix": {
			version: "v0.0.0-20181209220409-c518dec07be9", repository: "gonum/matrix",
			commit: "c518dec07be9a636c38a4650e217be059b5952ec", file: "README.md", spdx: "BSD-3-Clause",
			hash: "472467bf60ae3d75612dfb2282ce1c41b780ff9a7a2c8d89498ef5b2acb4bfdb", gonum: true,
		},
		"github.com/kr/logfmt": {
			version: "v0.0.0-20140226030751-b84e30acd515", repository: "kr/logfmt",
			commit: "b84e30acd515aadc4b783ad4ff83aff3299bdfe0", file: "Readme", spdx: "MIT",
			hash: "9ec1d73f6677490698fac2d49fff7d9037daaf0003682155c5781b3f0745eeae",
		},
		"github.com/mattn/go-localereader": {
			version: "v0.0.1", repository: "mattn/go-localereader",
			commit: "6338b4c69507fb1f9edba3db33882a8e9ab9bfa8", file: "README.md", spdx: "MIT",
			hash: "0c5c52517f13becd7a1e1234f1e8a5ba370f5a5078cee9ac058c2d534e267fcb",
		},
	}
	license, ok := licenses[path]
	if !ok || license.version != version {
		return moduleLicenseOverride{}, false
	}
	source := "https://api.github.com/repos/" + license.repository + "/contents/" + license.file + "?ref=" + license.commit
	command := "gh api 'repos/" + license.repository + "/contents/" + license.file + "?ref=" + license.commit + "' --jq .content | base64 -d | sha256sum"
	value := license.spdx + "; license-source-sha256:" + license.hash
	if license.gonum {
		command += " && gh api 'repos/gonum/license/contents/LICENSE?ref=" + gonumLicenseCommit + "' --jq .content | base64 -d | sha256sum"
		value += "; referenced-license-sha256:" + gonumLicenseHash
	}
	return moduleLicenseOverride{
		Value: value, Source: source, Command: command,
		SourceRepository: "https://github.com/" + license.repository, SourceCommit: license.commit,
	}, true
}

func declaredToolModule(id, version string) (moduleLicenseIdentity, bool) {
	paths := map[string]string{
		"tool:benchstat":             "golang.org/x/perf",
		"tool:buf":                   "github.com/bufbuild/buf",
		"tool:containerlab":          "github.com/srl-labs/containerlab",
		"tool:docker_compose":        "github.com/docker/compose/v5",
		"tool:gopls":                 "golang.org/x/tools/gopls",
		"tool:goreleaser":            "github.com/goreleaser/goreleaser/v2",
		"tool:gotestsum":             "gotest.tools/gotestsum",
		"tool:govulncheck":           "golang.org/x/vuln",
		"tool:osv_scanner":           "github.com/google/osv-scanner/v2",
		"tool:protoc_gen_connect_go": "connectrpc.com/connect",
		"tool:protoc_gen_go":         "google.golang.org/protobuf",
		"tool:syft":                  "github.com/anchore/syft",
		"tool:trivy":                 "github.com/aquasecurity/trivy",
	}
	path, ok := paths[id]
	if !ok {
		return moduleLicenseIdentity{}, false
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return moduleLicenseIdentity{Path: path, Version: version}, true
}

func declaredToolPyPIName(id string) (string, bool) {
	names := map[string]string{
		"tool:bandit":     "bandit",
		"tool:codespell":  "codespell",
		"tool:junit2html": "junit2html",
		"tool:pip_audit":  "pip-audit",
		"tool:ruff":       "ruff",
		"tool:ty":         "ty",
		"tool:yamllint":   "yamllint",
	}
	name, ok := names[id]
	return name, ok
}

func exactDeclaredToolLicense(id, version string) (moduleLicenseOverride, bool) {
	if id != "tool:uv" || version != "0.12.6" {
		return moduleLicenseOverride{}, false
	}
	return moduleLicenseOverride{
		Value: "Apache-2.0 OR MIT; license-source-sha256:c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4; " +
			"second-license-source-sha256:860e3d7a86b84e6a7012c7a635fc64df475cebc6cce34dfeb73a5982ec58176c",
		Source: "https://api.github.com/repos/astral-sh/uv/contents/LICENSE-APACHE?ref=7938ca5d53dbb9c614a4a030df406e41ff101ab9",
		Command: "gh api 'repos/astral-sh/uv/contents/LICENSE-APACHE?ref=7938ca5d53dbb9c614a4a030df406e41ff101ab9' --jq .content | base64 -d | sha256sum && " +
			"gh api 'repos/astral-sh/uv/contents/LICENSE-MIT?ref=7938ca5d53dbb9c614a4a030df406e41ff101ab9' --jq .content | base64 -d | sha256sum",
		SourceRepository: "https://github.com/astral-sh/uv", SourceCommit: "7938ca5d53dbb9c614a4a030df406e41ff101ab9",
	}, true
}

func exactPyPILegacyLicense(name, version string) (moduleLicenseOverride, bool) {
	overrides := map[string]moduleLicenseOverride{
		"colorama@0.4.6": {
			Value:            "BSD-3-Clause; license-source-sha256:cac35c02686e5d04a5a7140bfb3b36e73aed496656e891102e428886d7930318",
			Source:           "https://api.github.com/repos/tartley/colorama/contents/LICENSE.txt?ref=3de9f013df4b470069d03d250224062e8cf15c49",
			Command:          "gh api 'repos/tartley/colorama/contents/LICENSE.txt?ref=3de9f013df4b470069d03d250224062e8cf15c49' --jq .content | base64 -d | sha256sum",
			SourceRepository: "https://github.com/tartley/colorama", SourceCommit: "3de9f013df4b470069d03d250224062e8cf15c49",
		},
		"jinja2@3.1.6": {
			Value:            "BSD-3-Clause; license-source-sha256:3b49dcee4105eb37bac10faf1be260408fe85d252b8e9df2e0979fc1e094437b",
			Source:           "https://api.github.com/repos/pallets/jinja/contents/LICENSE.txt?ref=15206881c006c79667fe5154fe80c01c65410679",
			Command:          "gh api 'repos/pallets/jinja/contents/LICENSE.txt?ref=15206881c006c79667fe5154fe80c01c65410679' --jq .content | base64 -d | sha256sum",
			SourceRepository: "https://github.com/pallets/jinja", SourceCommit: "15206881c006c79667fe5154fe80c01c65410679",
		},
	}
	override, ok := overrides[name+"@"+version]
	return override, ok
}

func baseRecord(installed, channel string, sources []SourceLocation, pin ImmutablePin) Record {
	return Record{
		SourceLocations: sources,
		Installed:       installed,
		DeliveryChannel: channel,
		ImmutablePin:    pin,
	}
}

func runtimeBaseline(path string) string {
	switch path {
	case "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go":
		return "v1.36.11-20260415201107-50325440f8f2.1"
	case "connectrpc.com/connect":
		return "v1.19.2"
	case "connectrpc.com/grpchealth":
		return "v1.4.0"
	case "github.com/knadh/koanf/parsers/yaml":
		return "v1.1.0"
	case "github.com/knadh/koanf/v2":
		return "v2.3.4"
	case "github.com/prometheus/client_golang":
		return "v1.23.2"
	case "github.com/reeflective/console":
		return "v0.1.25"
	case "golang.org/x/net":
		return "v0.53.0"
	case "golang.org/x/sync":
		return "v0.20.0"
	case "golang.org/x/sys":
		return "v0.43.0"
	case "google.golang.org/grpc":
		return "v1.81.0"
	case "google.golang.org/protobuf":
		return "v1.36.11"
	case "go.yaml.in/yaml/v3":
		return "gopkg.in/yaml.v3@v3.0.1"
	default:
		return ""
	}
}

func assessmentTemplate(name string) Assessment {
	verified := func(value string) Review {
		return Review{Status: ReviewVerified, Value: value, EvidenceIDs: []string{}}
	}
	unverified := func(value string) Review {
		return Review{Status: ReviewUnverified, Value: value, EvidenceIDs: []string{}}
	}
	none := Review{Status: ReviewNotApplicable, EvidenceIDs: []string{}}
	license := unverified("per-record SPDX/license evidence is not yet reproducibly collected")
	assessment := Assessment{License: license}
	switch name {
	case "runtime-direct-audited":
		assessment.ChannelCurrent = unverified("selected identity is recorded; channel currency is not independently evidenced")
		assessment.UpstreamCurrent = unverified("the prior update audit is not reproduced by the offline inventory")
		assessment.ReleaseImpact = unverified("compatibility impact is covered by release gates, not module-list evidence")
		assessment.Security = verified("selected graph covered by the recorded vulnerability policy")
		assessment.Decision = adoptedDecision("adopt the audited direct runtime graph for v0.6.2")
	case "runtime-transitive-selected":
		assessment.ChannelCurrent = Review{Status: ReviewNotApplicable, Value: "latest channel is not the transitive selection contract", EvidenceIDs: []string{}}
		assessment.UpstreamCurrent = Review{Status: ReviewNotApplicable, Value: "individual latest is not the transitive selection contract", EvidenceIDs: []string{}}
		assessment.ReleaseImpact = unverified("transitive compatibility impact is covered by repository gates")
		assessment.Security = verified("selected graph covered by the recorded vulnerability policy")
		assessment.Decision = adoptedDecision("retain exact transitive versions selected by audited upstream requirements")
	case "tools-selected-graph":
		assessment.ChannelCurrent = Review{Status: ReviewNotApplicable, Value: "latest channel is not the selected tool-closure contract", EvidenceIDs: []string{}}
		assessment.UpstreamCurrent = Review{Status: ReviewNotApplicable, Value: "individual latest is not the transitive tool selection contract", EvidenceIDs: []string{}}
		assessment.ReleaseImpact = unverified("tool compatibility impact is covered by repository gates")
		assessment.Security = unverified("go list evidence does not prove vulnerability status")
		assessment.Decision = adoptedDecision("adopt the isolated exact tools graph")
	case "github-action-audited":
		assessment.ChannelCurrent = unverified("immutable commit identity does not prove channel currency")
		assessment.UpstreamCurrent = unverified("latest release is not independently evidenced per action record")
		assessment.ReleaseImpact = unverified("workflow impact is covered by CI rather than external metadata")
		assessment.Security = unverified("immutable pinning does not prove vulnerability status")
		assessment.Decision = adoptedDecision("adopt audited immutable workflow action pins")
	case "oci-digest-audited":
		assessment.ChannelCurrent = unverified("immutable digest identity does not prove channel currency")
		assessment.UpstreamCurrent = unverified("registry availability does not prove the latest upstream release")
		assessment.ReleaseImpact = unverified("image compatibility impact is covered by interop gates")
		assessment.Security = unverified("per-image vulnerability evidence remains a separate gate")
		assessment.Decision = adoptedDecision("adopt the audited immutable OCI digest")
	case "tool-version-audited":
		assessment.ChannelCurrent = unverified("declared identity does not prove channel currency")
		assessment.UpstreamCurrent = unverified("latest release is not independently evidenced per tool record")
		assessment.ReleaseImpact = unverified("tool compatibility impact is covered by repository gates")
		assessment.Security = unverified("tool-specific scanner evidence remains in the batch audit record")
		assessment.Decision = adoptedDecision("adopt the audited exact tool version")
	case "mutable-declared-deferred":
		assessment.ChannelCurrent = unverified("mutable tag or local image reference")
		assessment.UpstreamCurrent = unverified("mutable source requires a separate immutable-pin decision")
		assessment.ReleaseImpact = unverified("deferred until immutable source is available")
		assessment.Security = unverified("mutable artifact cannot support a reproducible security assessment")
		assessment.License = unverified("mutable artifact license evidence is not stable")
		assessment.Decision = Decision{Status: DecisionDeferred, Reason: "retain only as a tracked mutable interoperability input", Owner: "maintainers", Tracking: "gobfd-qj0.8.1.9", ReviewBy: "2026-09-30"}
	case "uv-locked-python-island":
		assessment.ChannelCurrent = unverified("exact current target selected in the single uv lock")
		assessment.UpstreamCurrent = unverified("release-channel evidence is retained in the owning Bead")
		assessment.ReleaseImpact = unverified("compatibility is covered by the Python and interop gates")
		assessment.Security = unverified("the frozen environment is checked by the pip-audit gate")
		assessment.License = unverified("complete transitive Python license provenance remains separately tracked")
		assessment.Decision = Decision{
			Status: DecisionAdopted, Reason: "adopt the single Python 3.14.7 uv lock", Owner: "maintainers",
			Tracking: "gobfd-qj0.8.1.5.2",
			LicenseException: &LicenseException{
				Owner: "maintainers", ReviewBy: "2026-09-30",
				Reason:   "complete declared tool and locked Python per-version license provenance review",
				Tracking: "gobfd-qj0.8.1.12",
			},
		}
	case "removed-direct-require":
		assessment = Assessment{ChannelCurrent: none, UpstreamCurrent: none, ReleaseImpact: none, Security: none, License: none, Decision: Decision{Status: DecisionRemoved, Reason: "remove redundant explicit indirect requirement; full selected graphs remain authoritative", Owner: "maintainers", Tracking: "gobfd-qj0.8.1.1"}}
	case "gobgp-v3-risk":
		assessment.ChannelCurrent = unverified("selected v3 identity does not independently prove channel currency")
		assessment.UpstreamCurrent = verified("v3.37.0 final v3 release")
		assessment.ReleaseImpact = unverified("v3 compatibility is proven by repository tests, not release metadata")
		assessment.Security = verified("GO-2026-4736; no fixed v3 release")
		assessment.Decision = adoptedDecision("retain v3.37.0 for v0.6.2 compatibility; v4 migration is separate")
		assessment.Decision.Status = DecisionRetained
		assessment.Decision.Tracking = "gobfd-qj0.8.2.4"
	case "gobgp-v4-risk":
		assessment.ChannelCurrent = unverified("candidate identity does not independently prove channel currency")
		assessment.UpstreamCurrent = verified("v4.8.0")
		assessment.ReleaseImpact = unverified("breaking migration impact remains a separate implementation review")
		assessment.Security = unverified("candidate vulnerability status is not covered by the selected runtime scan")
		assessment.Decision = Decision{Status: DecisionDeferred, Reason: "GoBGP v4 remains a separate v1 compatibility migration", Owner: "maintainers", Tracking: "gobfd-qj0.8.2.4", ReviewBy: "2026-09-30"}
	default:
		panic("unknown assessment template: " + name)
	}
	return assessment
}

func adoptedDecision(reason string) Decision {
	return Decision{
		Status: DecisionAdopted, Reason: reason, Owner: "maintainers", Tracking: "gobfd-qj0.8.1.1",
		LicenseException: &LicenseException{
			Owner: "maintainers", ReviewBy: "2026-09-30",
			Reason:   "complete declared tool and locked Python per-version license provenance review",
			Tracking: "gobfd-qj0.8.1.12",
		},
	}
}

func repositoryStateTemplate(name string, pin ImmutablePin) RepositoryState {
	verified := func(value string) Review {
		return Review{Status: ReviewVerified, Value: value, EvidenceIDs: []string{}}
	}
	unverified := func(value string) Review {
		return Review{Status: ReviewUnverified, Value: value, EvidenceIDs: []string{}}
	}
	none := Review{Status: ReviewNotApplicable, EvidenceIDs: []string{}}
	state := RepositoryState{
		RepositoryArchived: unverified("repository archival state is not independently proven for this record"),
		ArtifactAvailable:  unverified("artifact availability is not independently proven for this record"),
		ReleaseLineEOL:     unverified("release-line EOL is not independently proven for this record"),
	}
	switch name {
	case "runtime-direct-audited":
		state.ArtifactAvailable = verified("available")
	case "github-action-audited":
		state.RepositoryArchived = verified("active")
		state.ArtifactAvailable = verified("commit available")
	case "oci-digest-audited":
		state.ArtifactAvailable = verified("available at immutable digest")
	case "runtime-transitive-selected", "tools-selected-graph":
		if pin.Kind == "go-sum" {
			state.ArtifactAvailable = verified("available with committed go.sum checksum")
		}
	case "removed-direct-require":
		state = RepositoryState{RepositoryArchived: none, ArtifactAvailable: none, ReleaseLineEOL: none}
	case "gobgp-v3-risk":
		state.RepositoryArchived = verified("active repository")
		state.ArtifactAvailable = verified("v3.37.0 available")
		state.ReleaseLineEOL = verified("v3 release line ended at v3.37.0")
	case "gobgp-v4-risk":
		state.RepositoryArchived = verified("active repository")
		state.ArtifactAvailable = verified("v4.8.0 available")
		state.ReleaseLineEOL = verified("v4 release line active")
	}
	return state
}

func findGoSumPin(data []byte, path, version string) (string, bool) {
	for _, suffix := range []string{" h1:", "/go.mod h1:"} {
		prefix := path + " " + version + suffix
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.HasPrefix(line, prefix) {
				return line, true
			}
		}
	}
	return "", false
}

func deliveryChannel(kind Kind) string {
	switch kind {
	case KindGitHubAction:
		return "https://github.com"
	case KindOCIImage:
		return "OCI registry named by installed reference"
	case KindPythonPackage:
		return "https://pypi.org/simple"
	default:
		return "repository-declared delivery channel"
	}
}

func goModulePURL(path, version string) string {
	return "pkg:golang/" + path + "@" + version
}

func componentCoordinates(item declaredComponent) Coordinates {
	switch item.Kind {
	case KindGitHubAction:
		repository := actionRepository(item.Installed)
		_, commit, _ := strings.Cut(item.Installed, "@")
		return Coordinates{
			SourceRepository: "https://github.com/" + repository,
			SourceCommit:     commit,
			PURL:             "pkg:github/" + repository + "@" + commit,
		}
	case KindOCIImage:
		repository, version, digest := splitOCIReference(item.Installed)
		coordinates := Coordinates{PURL: "pkg:oci/" + repository + "@" + version}
		if digest != "" {
			coordinates.Digest = digest
		}
		return coordinates
	case KindPythonPackage:
		return Coordinates{
			PURL:   "pkg:pypi/" + strings.TrimPrefix(item.ID, "python-package:") + "@" + item.Installed,
			Digest: item.ArtifactHash,
		}
	default:
		return Coordinates{PURL: "pkg:generic/" + strings.TrimPrefix(item.ID, "tool:") + "@" + item.Installed}
	}
}

func actionRepository(installed string) string {
	ref, _, _ := strings.Cut(installed, "@")
	parts := strings.Split(ref, "/")
	if len(parts) < githubRepositoryParts {
		return ref
	}
	return strings.Join(parts[:2], "/")
}

func githubActionLicense(repository string) string {
	licenses := map[string]string{
		"SonarSource/sonarqube-scan-action": "LGPL-3.0",
		"actions/cache":                     "MIT",
		"actions/checkout":                  "MIT",
		"actions/download-artifact":         "MIT",
		"actions/github-script":             "MIT",
		"actions/setup-go":                  "MIT",
		"actions/setup-node":                "MIT",
		"actions/upload-artifact":           "MIT",
		"anchore/sbom-action":               "Apache-2.0",
		"aquasecurity/trivy-action":         "Apache-2.0",
		"bufbuild/buf-action":               "Apache-2.0",
		"codecov/codecov-action":            "MIT",
		"docker/login-action":               "Apache-2.0",
		"docker/setup-buildx-action":        "Apache-2.0",
		"docker/setup-qemu-action":          "Apache-2.0",
		"github/codeql-action":              "MIT",
		"goreleaser/goreleaser-action":      "MIT",
		"ossf/scorecard-action":             "Apache-2.0",
		"securego/gosec":                    "Apache-2.0",
	}
	return licenses[repository]
}

func splitOCIReference(reference string) (string, string, string) {
	if before, after, found := strings.Cut(reference, "@"); found {
		return before, after, after
	}
	lastSlash := strings.LastIndexByte(reference, '/')
	lastColon := strings.LastIndexByte(reference, ':')
	if lastColon > lastSlash {
		return reference[:lastColon], reference[lastColon+1:], ""
	}
	return reference, "unversioned", ""
}

type recordReview struct {
	name   string
	review *Review
}

func populateEvidence(inv *Inventory) {
	inv.Evidence = []Evidence{}
	for graphIndex := range inv.ModuleGraphs {
		graph := &inv.ModuleGraphs[graphIndex]
		for moduleIndex := range graph.Modules {
			module := &graph.Modules[moduleIndex]
			subject := graph.ID + ":" + module.Path
			command := "go list -m -json all"
			if graph.ID == "tools" {
				command = "go list -modfile=tools/go.mod -m -json all"
			}
			attachRecordEvidence(inv, subject, graph.Manifest, command, EvidenceTool{Name: "go", Version: runtime.Version()}, &module.Record)
		}
	}
	for componentIndex := range inv.Components {
		component := &inv.Components[componentIndex]
		source := "bead:gobfd-qj0.8.1.1"
		if len(component.SourceLocations) > 0 {
			source = component.SourceLocations[0].Path
		}
		attachRecordEvidence(inv, component.ID, source, "dependency inventory batch audit",
			EvidenceTool{Name: "dependencyinventory", Version: "schema-v2"}, &component.Record)
	}
	sort.Slice(inv.Evidence, func(i, j int) bool { return inv.Evidence[i].ID < inv.Evidence[j].ID })
}

func attachRecordEvidence(
	inv *Inventory,
	subject, source, command string,
	tool EvidenceTool,
	record *Record,
) {
	for _, binding := range recordReviews(record) {
		if binding.review.Status != ReviewVerified && binding.review.Status != ReviewStale {
			binding.review.EvidenceIDs = []string{}
			continue
		}
		evidenceSource, evidenceCommand, evidenceTool := evidenceContext(subject, binding.name, source, command, tool, record)
		evidence := Evidence{
			ID:             stableEvidenceID(subject, binding.name),
			Source:         evidenceSource,
			Subject:        subject,
			Review:         binding.name,
			CommandOrQuery: evidenceCommand,
			Tool:           evidenceTool,
			ObservedAt:     inv.AuditedAt,
			Result:         binding.review.Value,
			Hash:           evidenceResultHash(binding.review.Value),
		}
		binding.review.EvidenceIDs = []string{evidence.ID}
		inv.Evidence = append(inv.Evidence, evidence)
	}
}

func recordReviews(record *Record) []recordReview {
	return []recordReview{
		{name: "channel_current", review: &record.Assessment.ChannelCurrent},
		{name: "upstream_current", review: &record.Assessment.UpstreamCurrent},
		{name: "release_impact", review: &record.Assessment.ReleaseImpact},
		{name: "security", review: &record.Assessment.Security},
		{name: "license", review: &record.Assessment.License},
		{name: "repository_archived", review: &record.RepositoryState.RepositoryArchived},
		{name: "artifact_available", review: &record.RepositoryState.ArtifactAvailable},
		{name: "release_line_eol", review: &record.RepositoryState.ReleaseLineEOL},
	}
}

func evidenceContext(
	subject, review, source, command string,
	tool EvidenceTool,
	record *Record,
) (string, string, EvidenceTool) {
	if review == "license" && strings.HasPrefix(subject, "python-package:") {
		name := strings.TrimPrefix(subject, "python-package:")
		if override, ok := exactPyPILegacyLicense(name, record.Installed); ok {
			return override.Source, override.Command, EvidenceTool{Name: "gh", Version: "2.97.0"}
		}
		endpoint := pypiVersionURL(pypiAPIBase, name, record.Installed)
		return endpoint, "GET " + endpoint, EvidenceTool{Name: "PyPI", Version: "JSON API"}
	}
	if review == "license" && strings.HasPrefix(subject, "tool:") {
		if packageName, ok := declaredToolPyPIName(subject); ok {
			endpoint := pypiVersionURL(pypiAPIBase, packageName, record.Installed)
			return endpoint, "GET " + endpoint, EvidenceTool{Name: "PyPI", Version: "JSON API"}
		}
		if identity, ok := declaredToolModule(subject, record.Installed); ok {
			endpoint := depsDevVersionURL(identity.Path, identity.Version)
			return endpoint, "GET " + endpoint, EvidenceTool{Name: "deps.dev", Version: "v3"}
		}
		if override, ok := exactDeclaredToolLicense(subject, record.Installed); ok {
			return override.Source, override.Command, EvidenceTool{Name: "gh", Version: "2.97.0"}
		}
	}
	if (strings.HasPrefix(subject, "runtime:") || strings.HasPrefix(subject, "tools:")) && review == "license" {
		modulePath := strings.TrimPrefix(strings.TrimPrefix(subject, "runtime:"), "tools:")
		if override, ok := exactModuleLicense(modulePath, record.Installed); ok {
			return override.Source, override.Command, EvidenceTool{Name: "gh", Version: "2.97.0"}
		}
		endpoint := depsDevVersionURL(modulePath, record.Installed)
		return endpoint, "GET " + endpoint, EvidenceTool{Name: "deps.dev", Version: "v3"}
	}
	if strings.HasPrefix(subject, "github-action:") {
		repository := strings.TrimPrefix(record.Coordinates.SourceRepository, "https://github.com/")
		query := "gh api repos/" + repository
		switch review {
		case "license":
			query += " --jq .license.spdx_id"
		case "repository_archived":
			query += ` --jq 'if .archived then "archived" else "active" end'`
		case "artifact_available":
			query += "/commits/" + record.Coordinates.SourceCommit + " --jq .sha"
		}
		return "https://api.github.com/repos/" + repository, query, EvidenceTool{Name: "gh", Version: "2.97.0"}
	}
	if subject == "runtime:github.com/ovn-kubernetes/libovsdb" && review == "repository_archived" {
		return "https://api.github.com/repos/ovn-kubernetes/libovsdb",
			`gh api repos/ovn-kubernetes/libovsdb --jq 'if .archived then "archived" else "active" end'`,
			EvidenceTool{Name: "gh", Version: "2.97.0"}
	}
	if review == "license" && record.ImmutablePin.Kind == "oci-digest" {
		if license, ok := exactOCILicense(record.ImmutablePin.Value); ok && license.SPDX != "" {
			path := "repos/" + license.SourceRepository + "/contents/" + license.LicenseFile + "?ref=" + license.SourceCommit
			query := "gh api '" + path + "' --jq .content | base64 -d | sha256sum"
			if license.SecondFile != "" {
				query += " && gh api 'repos/" + license.SourceRepository + "/contents/" + license.SecondFile +
					"?ref=" + license.SourceCommit + "' --jq .content | base64 -d | sha256sum"
			}
			return "https://api.github.com/" + path, query, EvidenceTool{Name: "gh", Version: "2.97.0"}
		}
	}
	if strings.HasPrefix(subject, "oci-image:") && review == "artifact_available" {
		return record.ImmutablePin.Value, "podman manifest inspect " + record.ImmutablePin.Value,
			EvidenceTool{Name: "podman", Version: "5.8.2"}
	}
	if (subject == "runtime:github.com/osrg/gobgp/v3" || subject == "interop-daemon:gobgp-v4-candidate") &&
		(review == "upstream_current" || review == "repository_archived" || review == "artifact_available" || review == "release_line_eol") {
		query := "gh api repos/osrg/gobgp"
		switch review {
		case "upstream_current", "release_line_eol":
			query += "/releases?per_page=100"
		case "repository_archived":
			query += ` --jq 'if .archived then "archived" else "active repository" end'`
		case "artifact_available":
			query += "/git/ref/tags/" + record.Target
		}
		return "https://api.github.com/repos/osrg/gobgp", query, EvidenceTool{Name: "gh", Version: "2.97.0"}
	}
	if review == "security" && strings.HasPrefix(subject, "runtime:") {
		return "scripts/vuln-audit.go", "go run ./scripts/vuln-audit.go", EvidenceTool{Name: "go", Version: runtime.Version()}
	}
	return source, command, tool
}

func stableEvidenceID(subject, review string) string {
	digest := sha256.Sum256([]byte(subject + "|" + review))
	return fmt.Sprintf("ev-%x", digest[:12])
}

func evidenceResultHash(result string) string {
	digest := sha256.Sum256([]byte(result))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func specialComponents() []Component {
	removed := func(id, baseline string) Component {
		record := Record{
			SourceLocations: []SourceLocation{}, Baseline: baseline, DeliveryChannel: "https://proxy.golang.org",
			Coordinates:     Coordinates{PURL: goModulePURL(strings.TrimPrefix(id, "removed-go-mod-require:"), baseline)},
			Assessment:      assessmentTemplate("removed-direct-require"),
			RepositoryState: repositoryStateTemplate("removed-direct-require", ImmutablePin{Status: PinNotApplicable}),
			ImmutablePin:    ImmutablePin{Status: PinNotApplicable, Kind: "removed-direct-require", Value: baseline},
		}
		return Component{ID: id, Kind: KindRemoved, Record: record}
	}
	return []Component{
		{
			ID: "interop-daemon:gobgp-v4-candidate", Kind: KindInteropDaemon,
			Record: Record{
				SourceLocations: []SourceLocation{{Path: "docs/superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md", Match: "GoBGP remains on v3.37.0 and GoBGP v4 remains a separate v1 migration"}},
				Coordinates:     Coordinates{SourceRepository: "https://github.com/osrg/gobgp", PURL: "pkg:golang/github.com/osrg/gobgp/v4@v4.8.0"},
				Installed:       "not-installed", Target: "v4.8.0", DeliveryChannel: "https://github.com/osrg/gobgp/releases",
				Assessment:      assessmentTemplate("gobgp-v4-risk"),
				RepositoryState: repositoryStateTemplate("gobgp-v4-risk", ImmutablePin{Status: PinNotApplicable}),
				ImmutablePin:    ImmutablePin{Status: PinNotApplicable, Kind: "deferred-candidate", Value: "v4.8.0"},
			},
		},
		removed("removed-go-mod-require:github.com/davecgh/go-spew", "v1.1.1"),
		removed("removed-go-mod-require:github.com/dlclark/regexp2", "v1.12.0"),
		removed("removed-go-mod-require:github.com/pmezard/go-difflib", "v1.0.0"),
		removed("removed-go-mod-require:go.yaml.in/yaml/v2", "v2.4.4"),
	}
}

// Validate checks the in-memory inventory's structural and evidence contract.
func Validate(inv Inventory, root string) error {
	if inv.Schema != SchemaID {
		return fmt.Errorf("$schema = %q, want %q", inv.Schema, SchemaID)
	}
	if inv.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf("schema_version = %d, want %d", inv.SchemaVersion, currentSchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, inv.AuditedAt); err != nil {
		return fmt.Errorf("audited_at: %w", err)
	}
	if inv.GoPackageCount < 1 {
		return fmt.Errorf("go_package_count must be positive")
	}

	evidenceByID := make(map[string]Evidence, len(inv.Evidence))
	for _, evidence := range inv.Evidence {
		if evidence.ID == "" {
			return fmt.Errorf("evidence has missing id")
		}
		if _, exists := evidenceByID[evidence.ID]; exists {
			return fmt.Errorf("duplicate evidence id %q", evidence.ID)
		}
		if err := validateEvidence(evidence); err != nil {
			return err
		}
		evidenceByID[evidence.ID] = evidence
	}
	referencedEvidence := make(map[string]struct{}, len(inv.Evidence))

	graphIDs := make(map[string]struct{}, len(inv.ModuleGraphs))
	dependencyIDs := make(map[string]struct{})
	for _, graph := range inv.ModuleGraphs {
		if graph.ID == "" || graph.Manifest == "" || graph.Sum == "" {
			return fmt.Errorf("module graph has missing id, manifest, or sum")
		}
		expectedFiles, ok := map[string][2]string{
			"runtime": {"go.mod", "go.sum"},
			"tools":   {"tools/go.mod", "tools/go.sum"},
		}[graph.ID]
		if !ok || graph.Manifest != expectedFiles[0] || graph.Sum != expectedFiles[1] {
			return fmt.Errorf("module graph %q has unexpected manifest or sum", graph.ID)
		}
		if _, exists := graphIDs[graph.ID]; exists {
			return fmt.Errorf("duplicate module graph id %q", graph.ID)
		}
		graphIDs[graph.ID] = struct{}{}
		for _, module := range graph.Modules {
			id := graph.ID + ":" + module.Path
			if _, exists := dependencyIDs[id]; exists {
				return fmt.Errorf("duplicate dependency id %q", id)
			}
			dependencyIDs[id] = struct{}{}
			if module.Path == "" || module.Version == "" || module.Installed != module.Version {
				return fmt.Errorf("dependency %q has missing or inconsistent module identity", id)
			}
			if module.ImmutablePin.Kind == "selected-module-version" && module.ImmutablePin.Value != module.Path+"@"+module.Version {
				return fmt.Errorf("dependency %q selected module version pin is inconsistent", id)
			}
			if err := validateRecord(id, KindGoModule, module.Record, evidenceByID, referencedEvidence, root); err != nil {
				return err
			}
		}
	}
	for _, required := range []string{"runtime", "tools"} {
		if _, ok := graphIDs[required]; !ok {
			return fmt.Errorf("required module graph %q is missing", required)
		}
	}

	for _, component := range inv.Components {
		if component.ID == "" {
			return fmt.Errorf("component has missing id")
		}
		if _, exists := dependencyIDs[component.ID]; exists {
			return fmt.Errorf("duplicate dependency id %q", component.ID)
		}
		dependencyIDs[component.ID] = struct{}{}
	}
	for _, component := range inv.Components {
		if !slices.Contains([]Kind{
			KindGitHubAction, KindInteropDaemon, KindOCIImage, KindPythonPackage, KindRemoved, KindTool,
		}, component.Kind) {
			return fmt.Errorf("dependency %q has invalid kind %q", component.ID, component.Kind)
		}
		if err := validateRecord(component.ID, component.Kind, component.Record, evidenceByID, referencedEvidence, root); err != nil {
			return err
		}
	}
	for id := range evidenceByID {
		if _, ok := referencedEvidence[id]; !ok {
			return fmt.Errorf("evidence %q is not referenced by its subject review", id)
		}
	}
	return nil
}

func validateEvidence(evidence Evidence) error {
	if evidence.Source == "" || evidence.Subject == "" || evidence.Review == "" || evidence.CommandOrQuery == "" ||
		evidence.Tool.Name == "" || evidence.Tool.Version == "" || evidence.Result == "" || evidence.Hash == "" {
		return fmt.Errorf("evidence %q is incomplete", evidence.ID)
	}
	if !evidenceIDPattern.MatchString(evidence.ID) {
		return fmt.Errorf("evidence id %q is not stable sha256-derived form", evidence.ID)
	}
	if evidence.ID != stableEvidenceID(evidence.Subject, evidence.Review) {
		return fmt.Errorf("evidence id %q does not match its subject and review", evidence.ID)
	}
	if !slices.Contains([]string{
		"channel_current", "upstream_current", "release_impact", "security", "license",
		"repository_archived", "artifact_available", "release_line_eol",
	}, evidence.Review) {
		return fmt.Errorf("evidence %q has invalid review %q", evidence.ID, evidence.Review)
	}
	if _, err := time.Parse(time.RFC3339, evidence.ObservedAt); err != nil {
		return fmt.Errorf("evidence %q observed_at: %w", evidence.ID, err)
	}
	if evidence.Hash != evidenceResultHash(evidence.Result) {
		return fmt.Errorf("evidence result hash mismatch for %q", evidence.ID)
	}
	if evidence.Tool.Name == "dependencyinventory" || strings.Contains(evidence.CommandOrQuery, "batch audit") {
		return fmt.Errorf("evidence %q uses self-attested evidence for verified external review", evidence.ID)
	}
	if !evidenceCommandProvesReview(evidence) {
		return fmt.Errorf("evidence %q command %q cannot prove %s", evidence.ID, evidence.CommandOrQuery, evidence.Review)
	}
	return nil
}

func evidenceCommandProvesReview(evidence Evidence) bool {
	command := evidence.CommandOrQuery
	switch evidence.Review {
	case "channel_current":
		return evidence.Tool.Name == "bd" && strings.HasPrefix(command, "bd show ")
	case "license":
		return (evidence.Tool.Name == "gh" &&
			(strings.HasPrefix(command, "gh api repos/") || strings.HasPrefix(command, "gh api 'repos/"))) ||
			(evidence.Tool.Name == "deps.dev" && evidence.Tool.Version == "v3" &&
				strings.HasPrefix(evidence.Source, depsDevAPIBase+"/systems/go/packages/") && command == "GET "+evidence.Source) ||
			(evidence.Tool.Name == "PyPI" && evidence.Tool.Version == "JSON API" &&
				strings.HasPrefix(evidence.Source, pypiAPIBase+"/") && command == "GET "+evidence.Source)
	case "upstream_current", "repository_archived", "release_line_eol":
		return evidence.Tool.Name == "gh" && strings.HasPrefix(command, "gh api repos/")
	case "release_impact":
		return evidence.Tool.Name == "go" && strings.HasPrefix(command, "go test ")
	case "security":
		return evidence.Tool.Name == "go" && command == "go run ./scripts/vuln-audit.go"
	case "artifact_available":
		return (evidence.Tool.Name == "go" && strings.HasPrefix(command, "go list ")) ||
			(evidence.Tool.Name == "gh" && strings.HasPrefix(command, "gh api repos/")) ||
			(evidence.Tool.Name == "podman" && strings.HasPrefix(command, "podman manifest inspect "))
	default:
		return false
	}
}

func validateAssessment(
	id string,
	assessment Assessment,
	evidenceByID map[string]Evidence,
	referencedEvidence map[string]struct{},
) error {
	for _, binding := range []struct {
		name   string
		review Review
	}{
		{name: "channel_current", review: assessment.ChannelCurrent},
		{name: "upstream_current", review: assessment.UpstreamCurrent},
		{name: "release_impact", review: assessment.ReleaseImpact},
		{name: "security", review: assessment.Security},
		{name: "license", review: assessment.License},
	} {
		if err := validateReview(id, binding.name, binding.review, evidenceByID, referencedEvidence); err != nil {
			return err
		}
	}
	decision := assessment.Decision
	if !slices.Contains([]DecisionStatus{DecisionAdopted, DecisionDeferred, DecisionRemoved, DecisionRetained, DecisionStale}, decision.Status) ||
		decision.Reason == "" || decision.Owner == "" || decision.Tracking == "" {
		return fmt.Errorf("dependency %q decision is incomplete", id)
	}
	if decision.Status == DecisionDeferred || decision.Status == DecisionStale {
		if _, err := time.Parse(time.DateOnly, decision.ReviewBy); err != nil {
			return fmt.Errorf("dependency %q deferred or stale decision has missing or invalid review_by", id)
		}
	}
	if assessment.License.Status == ReviewUnverified &&
		(decision.Status == DecisionAdopted || decision.Status == DecisionRetained) {
		if err := validateLicenseException(id, decision.LicenseException); err != nil {
			return err
		}
	} else if decision.LicenseException != nil {
		return fmt.Errorf("dependency %q has license exception without an adopted or retained unverified license", id)
	}
	return nil
}

func validateLicenseException(id string, exception *LicenseException) error {
	if exception == nil || exception.Owner == "" || exception.Reason == "" || exception.Tracking == "" {
		return fmt.Errorf("dependency %q unverified license requires exception owner, reason, tracking, and review date", id)
	}
	if _, err := time.Parse(time.DateOnly, exception.ReviewBy); err != nil {
		return fmt.Errorf("dependency %q unverified license requires exception owner, reason, tracking, and review date", id)
	}
	return nil
}

func validateRepositoryState(
	id string,
	state RepositoryState,
	evidenceByID map[string]Evidence,
	referencedEvidence map[string]struct{},
) error {
	for _, binding := range []struct {
		name   string
		review Review
	}{
		{name: "repository_archived", review: state.RepositoryArchived},
		{name: "artifact_available", review: state.ArtifactAvailable},
		{name: "release_line_eol", review: state.ReleaseLineEOL},
	} {
		if err := validateReview(id, binding.name, binding.review, evidenceByID, referencedEvidence); err != nil {
			return err
		}
	}
	return nil
}

func validateReview(
	id, field string,
	review Review,
	evidenceByID map[string]Evidence,
	referencedEvidence map[string]struct{},
) error {
	if !slices.Contains([]ReviewStatus{ReviewNotApplicable, ReviewStale, ReviewUnverified, ReviewVerified}, review.Status) {
		return fmt.Errorf("dependency %q %s.status is missing or invalid", id, field)
	}
	if review.Status == ReviewVerified || review.Status == ReviewStale {
		if review.Value == "" || len(review.EvidenceIDs) == 0 {
			return fmt.Errorf("dependency %q %s verified or stale review lacks value or evidence_ids", id, field)
		}
	} else if len(review.EvidenceIDs) != 0 {
		return fmt.Errorf("dependency %q %s unverified or not-applicable review must not cite evidence", id, field)
	}
	seen := make(map[string]struct{}, len(review.EvidenceIDs))
	for _, evidenceID := range review.EvidenceIDs {
		if evidenceID == "" {
			return fmt.Errorf("dependency %q %s has empty evidence id", id, field)
		}
		if _, exists := seen[evidenceID]; exists {
			return fmt.Errorf("dependency %q %s repeats evidence %q", id, field, evidenceID)
		}
		seen[evidenceID] = struct{}{}
		evidence, ok := evidenceByID[evidenceID]
		if !ok {
			return fmt.Errorf("dependency %q %s references missing evidence %q", id, field, evidenceID)
		}
		if evidence.Subject != id || evidence.Review != field {
			return fmt.Errorf("evidence %q does not match subject and review for dependency %q %s", evidenceID, id, field)
		}
		referencedEvidence[evidenceID] = struct{}{}
	}
	return nil
}

func validateRecord(
	id string,
	kind Kind,
	record Record,
	evidenceByID map[string]Evidence,
	referencedEvidence map[string]struct{},
	root string,
) error {
	if kind != KindRemoved && record.Installed == "" {
		return fmt.Errorf("dependency %q installed is missing", id)
	}
	if record.DeliveryChannel == "" {
		return fmt.Errorf("dependency %q delivery_channel is missing", id)
	}
	if kind != KindRemoved && kind != KindGoModule && len(record.SourceLocations) == 0 {
		return fmt.Errorf("dependency %q source_locations is missing", id)
	}
	if !slices.Contains([]PinStatus{PinMutable, PinNotApplicable, PinVerified}, record.ImmutablePin.Status) ||
		record.ImmutablePin.Kind == "" || record.ImmutablePin.Value == "" {
		return fmt.Errorf("dependency %q immutable_pin is incomplete", id)
	}
	if kind == KindOCIImage && record.ImmutablePin.Status == PinVerified && !ociDigestReference(record.ImmutablePin.Value) {
		return fmt.Errorf("dependency %q has tag-only OCI reference claimed immutable", id)
	}
	if kind == KindGitHubAction && record.ImmutablePin.Status == PinVerified && !githubActionSHA(record.ImmutablePin.Value) {
		return fmt.Errorf("dependency %q has tag-only GitHub Action reference claimed immutable", id)
	}
	if err := validateCoordinates(id, kind, record); err != nil {
		return err
	}
	if err := validateAssessment(id, record.Assessment, evidenceByID, referencedEvidence); err != nil {
		return err
	}
	if err := validateRepositoryState(id, record.RepositoryState, evidenceByID, referencedEvidence); err != nil {
		return err
	}
	if record.ImmutablePin.Status == PinMutable && record.Assessment.Decision.Status != DecisionDeferred && record.Assessment.Decision.Status != DecisionStale {
		return fmt.Errorf("dependency %q mutable reference lacks deferred or stale decision", id)
	}
	if kind == KindRemoved {
		if record.Baseline == "" || record.Assessment.Decision.Status != DecisionRemoved || record.ImmutablePin.Status != PinNotApplicable {
			return fmt.Errorf("dependency %q removed direct requirement record is incomplete", id)
		}
	} else if record.Assessment.Decision.Status == DecisionRemoved {
		return fmt.Errorf("dependency %q non-removed record uses removed decision", id)
	}
	if record.Assessment.Decision.Status == DecisionStale && record.Target == "" {
		return fmt.Errorf("dependency %q stale decision is missing target", id)
	}
	for _, source := range record.SourceLocations {
		if err := validateSourceLocation(root, source); err != nil {
			return fmt.Errorf("dependency %q: %w", id, err)
		}
	}
	return nil
}

func validateCoordinates(id string, kind Kind, record Record) error {
	coordinates := record.Coordinates
	if coordinates.SourceRepository == "" && coordinates.SourceCommit == "" && coordinates.PURL == "" && coordinates.Digest == "" {
		return fmt.Errorf("dependency %q canonical coordinates are missing", id)
	}
	if coordinates.SourceCommit != "" {
		if coordinates.SourceRepository == "" || !sourceCommitPattern.MatchString(coordinates.SourceCommit) {
			return fmt.Errorf("dependency %q source commit coordinate is incomplete or invalid", id)
		}
	}
	if coordinates.PURL != "" && !strings.HasPrefix(coordinates.PURL, "pkg:") {
		return fmt.Errorf("dependency %q purl coordinate is invalid", id)
	}
	if coordinates.Digest != "" && !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(coordinates.Digest) {
		return fmt.Errorf("dependency %q digest coordinate is invalid", id)
	}
	if kind == KindOCIImage && record.ImmutablePin.Status == PinVerified &&
		(coordinates.Digest == "" || !strings.HasSuffix(record.ImmutablePin.Value, "@"+coordinates.Digest)) {
		return fmt.Errorf("dependency %q OCI digest coordinate does not match immutable pin", id)
	}
	if kind == KindGitHubAction && record.ImmutablePin.Status == PinVerified {
		_, commit, ok := strings.Cut(record.Installed, "@")
		if !ok || coordinates.SourceCommit != commit || record.ImmutablePin.Value != record.Installed {
			return fmt.Errorf("dependency %q GitHub Action coordinates do not match immutable pin", id)
		}
	}
	return nil
}

func validateSourceLocation(root string, source SourceLocation) error {
	if source.Path == "" || source.Match == "" || filepath.IsAbs(source.Path) {
		return fmt.Errorf("source location is incomplete or absolute")
	}
	clean := filepath.Clean(source.Path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("source location escapes repository root")
	}
	data, err := os.ReadFile(filepath.Join(root, clean))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && root != "" && len(source.Path) == 0 {
			return nil
		}
		return fmt.Errorf("read source location %s: %w", source.Path, err)
	}
	if !bytes.Contains(data, []byte(source.Match)) {
		return fmt.Errorf("stale source location %s does not contain %q", source.Path, source.Match)
	}
	return nil
}

func checkSchema(path string) error {
	var schema map[string]json.RawMessage
	if err := decodeOneJSONFile(path, &schema); err != nil {
		return fmt.Errorf("read dependency inventory schema: %w", err)
	}
	var id, schemaType string
	if err := json.Unmarshal(schema["$id"], &id); err != nil {
		return fmt.Errorf("decode dependency inventory schema id: %w", err)
	}
	if err := json.Unmarshal(schema["type"], &schemaType); err != nil {
		return fmt.Errorf("decode dependency inventory schema type: %w", err)
	}
	if id != SchemaID || schemaType != "object" {
		return fmt.Errorf("dependency inventory schema has unexpected id or type")
	}
	return nil
}

func readInventory(path string) (Inventory, error) {
	var inv Inventory
	if err := decodeOneJSONFile(path, &inv); err != nil {
		return Inventory{}, fmt.Errorf("read dependency inventory: %w", err)
	}
	return inv, nil
}

func decodeOneJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open JSON file: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxJSONFileBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if len(data) > maxJSONFileBytes {
		return fmt.Errorf("JSON file exceeds %d bytes", maxJSONFileBytes)
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON document: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON documents")
		}
		return fmt.Errorf("decode trailing JSON document: %w", err)
	}
	return nil
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON documents")
		}
		return fmt.Errorf("decode trailing JSON token: %w", err)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, tokenErr := decoder.Token()
	if tokenErr != nil {
		return fmt.Errorf("decode JSON token: %w", tokenErr)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return fmt.Errorf("decode JSON object key: %w", keyErr)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if valueErr := walkJSONValue(decoder); valueErr != nil {
				return valueErr
			}
		}
	case '[':
		for decoder.More() {
			if valueErr := walkJSONValue(decoder); valueErr != nil {
				return valueErr
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	closing, closingErr := decoder.Token()
	if closingErr != nil {
		return fmt.Errorf("decode JSON closing token: %w", closingErr)
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return fmt.Errorf("unexpected JSON closing delimiter %q", closing)
	}
	return nil
}

type moduleIdentity struct {
	Path     string
	Version  string
	Indirect bool
}

func compareModuleGraph(ctx context.Context, root string, graph ModuleGraph) error {
	want, err := selectedModuleGraph(ctx, root, graph)
	if err != nil {
		return err
	}
	got := make([]moduleIdentity, 0, len(graph.Modules))
	for _, module := range graph.Modules {
		got = append(got, moduleIdentity{Path: module.Path, Version: module.Version, Indirect: module.Indirect})
		if module.ImmutablePin.Status == PinVerified && module.ImmutablePin.Kind == "go-sum" {
			data, readErr := os.ReadFile(filepath.Join(root, graph.Sum))
			if readErr != nil {
				return fmt.Errorf("read %s: %w", graph.Sum, readErr)
			}
			if !bytes.Contains(data, []byte(module.ImmutablePin.Value+"\n")) {
				return fmt.Errorf("module %s:%s@%s has stale go.sum pin", graph.ID, module.Path, module.Version)
			}
		}
	}
	return compareModuleInventory(graph.ID, want, got)
}

func compareModuleInventory(graphID string, want, got []moduleIdentity) error {
	sortModuleIdentities(want)
	sortModuleIdentities(got)
	if !slices.Equal(want, got) {
		return fmt.Errorf("module graph mismatch for %s: %s", graphID, describeModuleDifference(want, got))
	}
	return nil
}

func selectedModuleGraph(ctx context.Context, root string, graph ModuleGraph) ([]moduleIdentity, error) {
	args := []string{"list"}
	if graph.ID == "tools" {
		args = append(args, "-modfile="+graph.Manifest)
	}
	args = append(args, "-m", "-json", "all")
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list %s selected module graph: %w: %s", graph.ID, err, strings.TrimSpace(string(output)))
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var modules []moduleIdentity
	for {
		//nolint:tagliatelle // The go command emits these exact exported JSON field names.
		var item struct {
			Path     string `json:"Path"`
			Version  string `json:"Version"`
			Main     bool   `json:"Main"`
			Indirect bool   `json:"Indirect"`
		}
		if err := decoder.Decode(&item); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode %s selected module graph: %w", graph.ID, err)
		}
		if item.Main {
			continue
		}
		if item.Path == "" || item.Version == "" {
			return nil, fmt.Errorf("decode %s selected module graph: module has missing path or version", graph.ID)
		}
		modules = append(modules, moduleIdentity{Path: item.Path, Version: item.Version, Indirect: item.Indirect})
	}
	return modules, nil
}

func sortModuleIdentities(modules []moduleIdentity) {
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Path != modules[j].Path {
			return modules[i].Path < modules[j].Path
		}
		if modules[i].Version != modules[j].Version {
			return modules[i].Version < modules[j].Version
		}
		return !modules[i].Indirect && modules[j].Indirect
	})
}

func describeModuleDifference(want, got []moduleIdentity) string {
	wantSet := make(map[string]struct{}, len(want))
	gotSet := make(map[string]struct{}, len(got))
	format := func(module moduleIdentity) string {
		return fmt.Sprintf("%s@%s (indirect=%t)", module.Path, module.Version, module.Indirect)
	}
	for _, module := range want {
		wantSet[format(module)] = struct{}{}
	}
	for _, module := range got {
		gotSet[format(module)] = struct{}{}
	}
	var missing, extra []string
	for item := range wantSet {
		if _, ok := gotSet[item]; !ok {
			missing = append(missing, item)
		}
	}
	for item := range gotSet {
		if _, ok := wantSet[item]; !ok {
			extra = append(extra, item)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return fmt.Sprintf("missing=%v extra=%v", missing, extra)
}

type declaredComponent struct {
	ID           string
	Kind         Kind
	Installed    string
	Sources      []SourceLocation
	Registry     string
	ArtifactHash string
}

var (
	actionPattern       = regexp.MustCompile(`\buses:\s*([^@\s]+)@([^\s#]+)`)
	containerPattern    = regexp.MustCompile(`^\s*FROM\s+([^\s]+)`)
	imagePattern        = regexp.MustCompile(`\bimage:\s*([^\s#]+)`)
	qualifiedImage      = regexp.MustCompile(`(?:docker\.io|quay\.io|ghcr\.io)/[A-Za-z0-9_./-]+(?::[A-Za-z0-9_.-]+)?(?:@sha256:[0-9a-f]{64})?`)
	declaredARGPattern  = regexp.MustCompile(`^\s*ARG\s+([A-Z][A-Z0-9_]*_VERSION)=([^\s]+)`)
	versionVarPattern   = regexp.MustCompile(`^\s*(?:readonly\s+)?([A-Z][A-Z0-9_]*_VERSION)="?\$\{[^:}]+:-([^}"]+)}`)
	fixedVersionVar     = regexp.MustCompile(`^\s*(?:readonly\s+)?([A-Z][A-Z0-9_]*_VERSION)=["']?([^"'\s]+)["']?\s*$`)
	workflowToolVersion = regexp.MustCompile(`^\s*(?:version|syft-version):\s*["']?([^"'#\s]+)`)
	pythonDependency    = regexp.MustCompile(`^\s*"([a-z0-9][a-z0-9_-]*)==([^"]+)",\s*$`)
	uvRequiredVersion   = regexp.MustCompile(`^\s*required-version\s*=\s*"==([^"]+)"\s*$`)
	ociDigestPattern    = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	actionSHAPattern    = regexp.MustCompile(`@[0-9a-f]{40}$`)
	sourceCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	evidenceIDPattern   = regexp.MustCompile(`^ev-[0-9a-f]{24}$`)
	uvPackageName       = regexp.MustCompile(`(?m)^name = "([a-z0-9][a-z0-9._-]*)"$`)
	uvPackageVersion    = regexp.MustCompile(`(?m)^version = "([^"]+)"$`)
	uvRegistrySource    = regexp.MustCompile(`(?m)^source = \{ registry = "([^"]+)" \}$`)
	uvVirtualSource     = regexp.MustCompile(`(?m)^source = \{ virtual = "[^"]+" \}$`)
	uvSDistHash         = regexp.MustCompile(`(?m)^sdist = \{[^\n]* hash = "(sha256:[0-9a-f]{64})"`)
	uvArtifactHash      = regexp.MustCompile(`hash = "(sha256:[0-9a-f]{64})"`)
	pythonNameSeparator = regexp.MustCompile(`[-_.]+`)
	spdxExpression      = regexp.MustCompile(`^[A-Za-z0-9.+() -]+$`)
)

func discoverDeclaredComponents(ctx context.Context, root string) ([]declaredComponent, error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open repository root: %w", err)
	}
	result, discoverErr := discoverDeclaredComponentsFromRoot(ctx, root, repository)
	if joinedErr := errors.Join(discoverErr, repository.Close()); joinedErr != nil {
		return nil, joinedErr
	}
	return result, nil
}

func discoverDeclaredComponentsFromRoot(
	ctx context.Context,
	root string,
	repository *os.Root,
) ([]declaredComponent, error) {
	components := make(map[string]declaredComponent)
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk dependency source %s: %w", path, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("discover declared dependencies: %w", err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("make dependency source relative: %w", err)
		}
		if entry.IsDir() {
			if relative == ".git" || relative == ".beads" || relative == "reports" || relative == "docs" ||
				relative == "test/internal/dependencyinventory" || relative == "test/cmd/dependencyinventory" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(relative, "_test.go") || strings.HasSuffix(relative, ".sum") {
			return nil
		}
		if !shouldScanDeclaredSource(relative) {
			return nil
		}
		data, err := readDeclaredSource(repository, relative)
		if err != nil {
			return fmt.Errorf("read dependency source %s: %w", relative, err)
		}
		currentAction := ""
		for line := range strings.SplitSeq(string(data), "\n") {
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
				continue
			}
			if strings.Contains(relative, ".github/workflows/") {
				currentAction = scanWorkflowLine(components, relative, line, trimmedLine, currentAction)
			}
			if strings.HasPrefix(filepath.Base(relative), "Containerfile") {
				if match := containerPattern.FindStringSubmatch(line); match != nil && match[1] != "scratch" {
					addImage(components, relative, match[1])
				}
				if match := declaredARGPattern.FindStringSubmatch(line); match != nil {
					addTool(components, relative, match[1], match[2], strings.TrimSpace(line))
				}
			}
			if strings.HasSuffix(relative, ".yml") || strings.HasSuffix(relative, ".yaml") {
				if match := imagePattern.FindStringSubmatch(line); match != nil {
					addImage(components, relative, strings.Trim(match[1], `"'`))
				}
			}
			if strings.HasSuffix(relative, ".sh") {
				if match := versionVarPattern.FindStringSubmatch(line); match != nil {
					addTool(components, relative, match[1], match[2], strings.TrimSpace(line))
				} else if match := fixedVersionVar.FindStringSubmatch(line); match != nil {
					addTool(components, relative, match[1], match[2], strings.TrimSpace(line))
				}
			}
			if relative == "pyproject.toml" {
				if match := pythonDependency.FindStringSubmatch(line); match != nil {
					addTool(components, relative, match[1], match[2], strings.TrimSpace(line))
				} else if match := uvRequiredVersion.FindStringSubmatch(line); match != nil {
					addTool(components, relative, "UV_VERSION", match[1], strings.TrimSpace(line))
				}
			}
			extension := filepath.Ext(relative)
			if extension == ".sh" || extension == ".json" || extension == ".py" {
				for _, bounds := range qualifiedImage.FindAllStringIndex(line, -1) {
					image := line[bounds[0]:bounds[1]]
					if strings.HasPrefix(line[bounds[1]:], ":{") || (extension == ".py" && !ociDigestReference(image)) {
						continue
					}
					addImage(components, relative, image)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("discover declared dependencies: %w", walkErr)
	}
	if _, err := repository.Lstat("uv.lock"); err == nil {
		data, readErr := readDeclaredSource(repository, "uv.lock")
		if readErr != nil {
			return nil, fmt.Errorf("read dependency source uv.lock: %w", readErr)
		}
		packages, parseErr := parseUVLockPackages(data)
		if parseErr != nil {
			return nil, fmt.Errorf("parse uv.lock: %w", parseErr)
		}
		for _, packageRecord := range packages {
			addDeclared(components, packageRecord)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("lstat dependency source uv.lock: %w", err)
	}
	result := make([]declaredComponent, 0, len(components))
	for _, component := range components {
		sort.Slice(component.Sources, func(i, j int) bool {
			if component.Sources[i].Path != component.Sources[j].Path {
				return component.Sources[i].Path < component.Sources[j].Path
			}
			return component.Sources[i].Match < component.Sources[j].Match
		})
		result = append(result, component)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func parseUVLockPackages(data []byte) ([]declaredComponent, error) {
	sections := strings.Split(string(data), "[[package]]")
	packages := make([]declaredComponent, 0, len(sections)-1)
	seen := make(map[string]struct{})
	for _, section := range sections[1:] {
		nameMatch := uvPackageName.FindStringSubmatch(section)
		versionMatch := uvPackageVersion.FindStringSubmatch(section)
		if nameMatch == nil || versionMatch == nil {
			return nil, fmt.Errorf("package entry has missing name or version")
		}
		if uvVirtualSource.MatchString(section) {
			continue
		}
		registryMatch := uvRegistrySource.FindStringSubmatch(section)
		if registryMatch == nil {
			return nil, fmt.Errorf("package %s@%s has unsupported or missing source", nameMatch[1], versionMatch[1])
		}
		if registryMatch[1] != "https://pypi.org/simple" {
			return nil, fmt.Errorf("package %s@%s uses unsupported registry %s", nameMatch[1], versionMatch[1], registryMatch[1])
		}
		hashMatch := uvSDistHash.FindStringSubmatch(section)
		if hashMatch == nil {
			hashMatch = uvArtifactHash.FindStringSubmatch(section)
		}
		if hashMatch == nil {
			return nil, fmt.Errorf("registry package %s@%s has no SHA-256 artifact", nameMatch[1], versionMatch[1])
		}
		normalizedName := pythonNameSeparator.ReplaceAllString(strings.ToLower(nameMatch[1]), "-")
		id := "python-package:" + normalizedName
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate locked Python package %s", id)
		}
		seen[id] = struct{}{}
		packages = append(packages, declaredComponent{
			ID: id, Kind: KindPythonPackage, Installed: versionMatch[1], Registry: registryMatch[1],
			ArtifactHash: hashMatch[1],
			Sources: []SourceLocation{
				{Path: "uv.lock", Match: "name = \"" + nameMatch[1] + "\"\nversion = \"" + versionMatch[1] + "\""},
				{Path: "uv.lock", Match: hashMatch[1]},
			},
		})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ID < packages[j].ID })
	return packages, nil
}

func scanWorkflowLine(
	components map[string]declaredComponent,
	path string,
	line string,
	trimmedLine string,
	currentAction string,
) string {
	if strings.HasPrefix(trimmedLine, "- name:") {
		currentAction = ""
	}
	if match := actionPattern.FindStringSubmatch(line); match != nil && !strings.HasPrefix(match[1], "./") {
		currentAction = match[1]
		addDeclared(components, declaredComponent{
			ID: "github-action:" + match[1], Kind: KindGitHubAction, Installed: match[1] + "@" + match[2],
			Sources: []SourceLocation{{Path: path, Match: match[1] + "@" + match[2]}},
		})
	}
	match := workflowToolVersion.FindStringSubmatch(line)
	if match == nil {
		return currentAction
	}
	var toolName string
	switch currentAction {
	case "anchore/sbom-action/download-syft":
		toolName = "SYFT_VERSION"
	case "aquasecurity/trivy-action":
		toolName = "TRIVY_VERSION"
	case "goreleaser/goreleaser-action":
		toolName = "GORELEASER_VERSION"
	}
	if toolName != "" {
		addTool(components, path, toolName, match[1], trimmedLine)
	}
	return currentAction
}

func shouldScanDeclaredSource(relative string) bool {
	if relative == "pyproject.toml" {
		return true
	}
	if strings.HasPrefix(filepath.Base(relative), "Containerfile") {
		return true
	}
	switch filepath.Ext(relative) {
	case ".json", ".py", ".sh", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func readDeclaredSource(repository *os.Root, relative string) ([]byte, error) {
	before, err := repository.Lstat(relative)
	if err != nil {
		return nil, fmt.Errorf("lstat source: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("source is not a regular non-symlink file")
	}
	file, err := repository.Open(relative)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		return nil, errors.Join(fmt.Errorf("stat opened source: %w", statErr), file.Close())
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.Join(fmt.Errorf("source identity changed before read"), file.Close())
	}
	const maxSourceBytes = 4 << 20
	data, readErr := io.ReadAll(io.LimitReader(file, maxSourceBytes+1))
	closeErr := file.Close()
	if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
		return nil, fmt.Errorf("read bounded source: %w", joinedErr)
	}
	if len(data) > maxSourceBytes {
		return nil, fmt.Errorf("source exceeds %d bytes", maxSourceBytes)
	}
	return data, nil
}

func addDeclared(components map[string]declaredComponent, candidate declaredComponent) {
	existing, ok := components[candidate.ID]
	if !ok {
		components[candidate.ID] = candidate
		return
	}
	if existing.Installed != candidate.Installed {
		candidate.ID += ":" + sanitizeID(candidate.Installed)
		components[candidate.ID] = candidate
		return
	}
	for _, source := range candidate.Sources {
		if !slices.Contains(existing.Sources, source) {
			existing.Sources = append(existing.Sources, source)
		}
	}
	components[candidate.ID] = existing
}

func addImage(components map[string]declaredComponent, path, image string) {
	if image == "" || image == "scratch" || strings.ContainsAny(image, "${}") {
		return
	}
	addDeclared(components, declaredComponent{
		ID: "oci-image:" + image, Kind: KindOCIImage, Installed: image,
		Sources: []SourceLocation{{Path: path, Match: image}},
	})
}

func addTool(components map[string]declaredComponent, path, name, version, match string) {
	if version == "" || strings.ContainsAny(version, "$(){}") {
		return
	}
	addDeclared(components, declaredComponent{
		ID:   "tool:" + strings.ReplaceAll(strings.ToLower(strings.TrimSuffix(name, "_VERSION")), "-", "_"),
		Kind: KindTool, Installed: version,
		Sources: []SourceLocation{{Path: path, Match: match}},
	})
}

func sanitizeID(value string) string {
	return strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-").Replace(value)
}

func compareDeclaredComponents(inventory []Component, declared []declaredComponent) error {
	byID := make(map[string]Component, len(inventory))
	for _, component := range inventory {
		byID[component.ID] = component
	}
	for _, expected := range declared {
		actual, ok := byID[expected.ID]
		if !ok {
			return fmt.Errorf("declared component missing from inventory: %s (%s)", expected.ID, expected.Installed)
		}
		if actual.Kind != expected.Kind || actual.Installed != expected.Installed {
			return fmt.Errorf("declared component %s is stale: inventory=%s source=%s", expected.ID, actual.Installed, expected.Installed)
		}
		for _, source := range expected.Sources {
			if !slices.Contains(actual.SourceLocations, source) {
				return fmt.Errorf("declared component %s is missing source location %s", expected.ID, source.Path)
			}
		}
	}
	return nil
}

func ociDigestReference(value string) bool {
	return ociDigestPattern.MatchString(value)
}

func githubActionSHA(value string) bool {
	return actionSHAPattern.MatchString(value)
}

func goPackageCount(ctx context.Context, root string) (int, error) {
	command := exec.CommandContext(ctx, "go", "list", "-buildvcs=false", "./...")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("list Go packages: %w", err)
	}
	count := len(bytes.Fields(output))
	if count == 0 {
		return 0, fmt.Errorf("list Go packages: no packages found")
	}
	return count, nil
}
