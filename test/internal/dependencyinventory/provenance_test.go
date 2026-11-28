package dependencyinventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestDevSnapshotDoesNotClaimHistoricalAcceptance(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"runtime-direct-audited", "runtime-transitive-selected", "tools-selected-graph",
		"github-action-audited", "oci-digest-audited", "tool-version-audited", "gobgp-v4-risk",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assessment := assessmentTemplate(name)
			if assessment.Decision.Status != DecisionDeferred || assessment.Decision.ReviewBy == "" {
				t.Errorf("decision = %#v, want bounded deferred dev selection", assessment.Decision)
			}
			if strings.Contains(assessment.Decision.Reason, "v0.6.2") ||
				assessment.Decision.ReviewException != nil || assessment.Decision.LicenseException != nil {
				t.Errorf("decision inherits historical release acceptance: %#v", assessment.Decision)
			}
			if assessment.Security.Status != ReviewUnverified {
				t.Errorf("security = %#v, want unverified without a scan receipt", assessment.Security)
			}
		})
	}
}

func TestModuleSelectionDoesNotProveArtifactAvailability(t *testing.T) {
	t.Parallel()

	pin := ImmutablePin{Status: PinVerified, Kind: "go-sum", Value: "selected checksum"}
	for _, policy := range []string{
		"runtime-direct-audited", "runtime-transitive-selected", "tools-selected-graph", "gobgp-v4-risk",
	} {
		t.Run(policy, func(t *testing.T) {
			t.Parallel()

			state := repositoryStateTemplate(policy, pin)
			if state.ArtifactAvailable.Status != ReviewUnverified || len(state.ArtifactAvailable.EvidenceIDs) != 0 {
				t.Fatalf("selected identity/checksum became artifact availability proof: %#v", state.ArtifactAvailable)
			}
		})
	}
}

func TestHistoricalDepsDevLicenseSurvivesStaticOverride(t *testing.T) {
	t.Parallel()

	const modulePath = "github.com/hinshun/vt10x"
	const version = "v0.0.0-20220301184237-5011da428d02"
	prior := validInventory()
	prior.AuditedAt = snapshotObservedAt
	prior.Components = nil
	module := Module{
		Path: modulePath, Version: version,
		Installed: version, ImmutablePin: ImmutablePin{Status: PinVerified, Kind: "go-sum", Value: "selected checksum"},
	}
	module.Assessment.License = Review{Status: ReviewVerified, Value: "MIT"}
	prior.ModuleGraphs[0].Modules = []Module{module}
	observeDepsDevLicense(&prior, "runtime:"+modulePath, &prior.ModuleGraphs[0].Modules[0].Record,
		moduleLicenseIdentity{Path: modulePath, Version: version})
	prior.Evidence[0].ObservedAt = "2026-10-14T12:34:56Z"
	current := validInventory()
	current.AuditedAt = snapshotObservedAt
	current.Components = nil
	module.Assessment = assessmentTemplate("runtime-direct-audited")
	current.ModuleGraphs[0].Modules = []Module{module}

	reuseHistoricalEvidence(&current, prior)
	if !reflect.DeepEqual(current.Evidence, prior.Evidence) {
		t.Fatalf("actual deps.dev observation changed: got %#v, want %#v", current.Evidence, prior.Evidence)
	}
}

func TestPopulateEvidencePreservesHistoricalObservation(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.AuditedAt = "2026-10-15T21:30:03Z"
	module := Module{Path: "example.invalid/module", Version: "v1.0.0", Installed: "v1.0.0"}
	subject := "runtime:" + module.Path
	endpoint := depsDevVersionURL(module.Path, module.Version)
	historical := Evidence{
		ID: stableEvidenceID(subject, "license"), Source: endpoint,
		Subject: subject, Review: "license", CommandOrQuery: "GET " + endpoint,
		Tool:       EvidenceTool{Name: "deps.dev", Version: "v3"},
		ObservedAt: "2026-09-04T00:00:00Z", Result: "MIT", Hash: evidenceResultHash("MIT"),
	}
	module.Assessment.License = Review{Status: ReviewVerified, Value: "MIT", EvidenceIDs: []string{historical.ID}}
	inv.ModuleGraphs[0].Modules = []Module{module}
	inv.Evidence = []Evidence{historical}

	populateEvidence(&inv)
	found := false
	for _, evidence := range inv.Evidence {
		if evidence.ID == historical.ID {
			found = true
			if evidence != historical {
				t.Fatalf("historical observation changed: got %#v, want %#v", evidence, historical)
			}
		}
	}
	if !found {
		t.Fatal("historical evidence disappeared")
	}
}

func TestHistoricalEvidenceRequiresExactIdentity(t *testing.T) {
	t.Parallel()

	prior := validInventory()
	prior.Components = nil
	prior.AuditedAt = snapshotObservedAt
	prior.ModuleGraphs[0].Modules = []Module{{
		Path: "github.com/ovn-kubernetes/libovsdb", Version: "v1.0.0",
		Installed: "v1.0.0", ImmutablePin: ImmutablePin{Status: PinVerified, Kind: "go-sum", Value: "one"},
	}}
	prior.ModuleGraphs[0].Modules[0].Assessment.License = Review{Status: ReviewVerified, Value: "MIT"}
	prior.ModuleGraphs[0].Modules[0].RepositoryState.RepositoryArchived = Review{Status: ReviewVerified, Value: "active"}
	populateEvidence(&prior)
	for index := range prior.Evidence {
		prior.Evidence[index].ObservedAt = "2026-08-21T00:00:00Z"
	}
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same identity", true: "changed identity"}[changed], func(t *testing.T) {
			t.Parallel()

			current := validInventory()
			current.Components = nil
			current.AuditedAt = snapshotObservedAt
			module := prior.ModuleGraphs[0].Modules[0]
			module.Assessment = assessmentTemplate("runtime-direct-audited")
			module.RepositoryState = repositoryStateTemplate("runtime-direct-audited", module.ImmutablePin)
			if changed {
				module.Version, module.Installed, module.ImmutablePin.Value = "v1.1.0", "v1.1.0", "two"
			}
			current.ModuleGraphs[0].Modules = []Module{module}
			reuseHistoricalEvidence(&current, prior)
			licenseStatus := current.ModuleGraphs[0].Modules[0].Assessment.License.Status
			if changed && (len(current.Evidence) != 0 || licenseStatus != ReviewUnverified) {
				t.Fatal("new identity inherited historical evidence")
			}
			if !changed && !reflect.DeepEqual(current.Evidence, prior.Evidence) {
				t.Fatalf("historical evidence changed: got %#v, want %#v", current.Evidence, prior.Evidence)
			}
			if !changed && current.ModuleGraphs[0].Modules[0].RepositoryState.RepositoryArchived.Status != ReviewStale {
				t.Fatal("historical mutable repository observation did not become stale")
			}
			first := current.Evidence
			reuseHistoricalEvidence(&current, Inventory{})
			if !reflect.DeepEqual(first, current.Evidence) {
				t.Fatal("empty history changed deterministic evidence")
			}
		})
	}
}

func TestLegacySnapshotDoesNotCreateExternalObservations(t *testing.T) {
	t.Parallel()

	prior := validInventory()
	prior.AuditedAt = "2026-09-04T00:00:00Z"
	prior.Components = nil
	prior.ModuleGraphs[0].Modules = []Module{{
		Path: "example.invalid/unrelated", Version: "v1.0.0",
		Installed: "v1.0.0", ImmutablePin: ImmutablePin{Status: PinVerified, Kind: "go-sum", Value: "one"},
	}}
	prior.ModuleGraphs[0].Modules[0].Assessment.License = Review{Status: ReviewVerified, Value: "MIT"}
	populateEvidence(&prior)
	current := validInventory()
	current.Components = nil
	current.AuditedAt = snapshotObservedAt
	module := prior.ModuleGraphs[0].Modules[0]
	module.Assessment = assessmentTemplate("runtime-direct-audited")
	current.ModuleGraphs[0].Modules = []Module{module}

	for _, epoch := range []string{"2026-09-04T00:00:00Z", "2026-10-14T12:34:56Z"} {
		prior.AuditedAt = epoch
		reuseHistoricalEvidence(&current, prior)
		if len(current.Evidence) != 0 || current.ModuleGraphs[0].Modules[0].Assessment.License.Status != ReviewUnverified {
			t.Fatalf("untrusted snapshot epoch %s became trusted external evidence", epoch)
		}
	}
}

type licenseRoundTripper func(*http.Request) (*http.Response, error)

func (transport licenseRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestCancelledLicenseCollectionInvalidatesUnobservedLicenses(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components = nil
	inv.ModuleGraphs[0].Modules = nil
	for index := range 32 {
		inv.ModuleGraphs[0].Modules = append(inv.ModuleGraphs[0].Modules, Module{
			Path: fmt.Sprintf("example.invalid/module%d", index), Version: "v1.0.0",
			Installed: "v1.0.0", Assessment: Assessment{
				License: Review{Status: ReviewVerified, Value: "MIT"},
			},
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})}
	if err := collectLicenseEvidence(ctx, &inv, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("collection error = %v, want context cancellation", err)
	}
	for _, module := range inv.ModuleGraphs[0].Modules {
		if module.Assessment.License.Status != ReviewUnverified {
			t.Fatalf("cancelled collection retained verified license for %s", module.Path)
		}
	}
}

func TestLicenseCollectionObservesOnlyItsOwnResponses(t *testing.T) {
	t.Parallel()

	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fails], func(t *testing.T) {
			t.Parallel()

			inv := validInventory()
			inv.Components = nil
			inv.AuditedAt = snapshotObservedAt
			inv.ModuleGraphs[0].Modules = []Module{{
				Path: "example.invalid/module", Version: "v1.0.0", Installed: "v1.0.0",
			}}
			unrelated := Evidence{ID: "historical-vendor", ObservedAt: "2026-09-04T00:00:00Z", Result: "unchanged"}
			inv.Evidence = []Evidence{unrelated}
			client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
				if fails {
					return nil, errors.New("license request unavailable")
				}
				if request.URL.String() != depsDevVersionURL("example.invalid/module", "v1.0.0") {
					t.Errorf("unexpected license request: %s", request.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"versionKey":{"system":"GO","name":"example.invalid/module","version":"v1.0.0"},
						"licenses":["MIT"]
					}`)),
				}, nil
			})}
			err := collectLicenseEvidence(context.Background(), &inv, client)
			if (err != nil) != fails {
				t.Fatalf("collect license error = %v, want failure %t", err, fails)
			}
			found := false
			for _, evidence := range inv.Evidence {
				if evidence.ID == unrelated.ID {
					found = true
					if evidence != unrelated {
						t.Fatal("license collector changed unrelated historical evidence")
					}
				} else if evidence.ObservedAt != snapshotObservedAt || evidence.Tool.Name != "deps.dev" {
					t.Fatalf("new license has wrong observation context: %#v", evidence)
				}
			}
			if !found {
				t.Fatal("license collector dropped unrelated historical evidence")
			}
			want := ReviewVerified
			if fails {
				want = ReviewUnverified
			}
			if got := inv.ModuleGraphs[0].Modules[0].Assessment.License.Status; got != want {
				t.Fatalf("license status = %s, want %s", got, want)
			}
		})
	}
}
