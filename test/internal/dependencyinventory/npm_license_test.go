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
	"time"
)

func TestNPMLicenseCollectionUsesExactPublicMetadataAndActualObservationTime(t *testing.T) {
	t.Parallel()
	inv := npmLicenseFixture()
	unrelated := Evidence{ID: "unrelated", Result: "preserved"}
	inv.Evidence = append(inv.Evidence, unrelated)
	calls := 0
	before := time.Now().UTC().Truncate(time.Second)
	client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "registry.npmjs.org" ||
			request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("license request escaped unauthenticated public registry")
		}
		deadline, bounded := request.Context().Deadline()
		if !bounded || time.Until(deadline) > npmLicenseHTTPTimeout {
			t.Fatal("license request has no bounded deadline")
		}
		var identity npmLicenseIdentity
		switch request.URL.Path {
		case "/npm/12.2.1":
			identity, _ = declaredNPMLicense("tool:npm", "12.2.1")
		case "/release-please/17.11.2":
			identity, _ = declaredNPMLicense("tool:release_please", "17.11.2")
		default:
			t.Fatal("license request did not use an exact version")
		}
		body := fmt.Sprintf(`{"name":%q,"version":%q,"license":%q,"extra":true}`,
			identity.Name, identity.Version, identity.SPDX)
		return npmLicenseResponse(http.StatusOK, body), nil
	})}
	if err := collectLicenseEvidence(t.Context(), &inv, client); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(inv.Evidence) != 3 {
		t.Fatalf("requests=%d evidence=%d", calls, len(inv.Evidence))
	}
	byID := make(map[string]Evidence)
	for _, evidence := range inv.Evidence {
		byID[evidence.ID] = evidence
	}
	if !reflect.DeepEqual(byID[unrelated.ID], unrelated) {
		t.Fatal("unrelated evidence changed")
	}
	for _, component := range inv.Components {
		if component.Assessment.License.Status != ReviewVerified {
			t.Fatal("fresh registry license remained unverified")
		}
		if err := validateNPMLicenseEvidence(component.ID, component.Record, byID); err != nil {
			t.Fatal(err)
		}
		evidence := byID[component.Assessment.License.EvidenceIDs[0]]
		if err := validateEvidence(evidence); err != nil {
			t.Fatal(err)
		}
		observed, err := time.Parse(time.RFC3339, evidence.ObservedAt)
		if err != nil || observed.Before(before) || observed.After(time.Now().UTC()) || evidence.ObservedAt == inv.AuditedAt {
			t.Fatalf("observation time=%s, error=%v", evidence.ObservedAt, err)
		}
	}
	if err := collectLicenseEvidence(t.Context(), &inv, client); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(inv.Evidence) != 3 {
		t.Fatal("repeated collection duplicated license evidence")
	}
}

func TestNPMLicenseFailedRefreshInvalidatesPriorVerifiedEvidence(t *testing.T) {
	t.Parallel()
	cases := []string{
		"request", "status", "redirect", "oversized", "version", "SPDX", "alias", "alias override",
		"duplicate", "null", "trailing",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			inv := npmLicenseFixture()
			calls := 0
			client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				for _, component := range inv.Components {
					if component.Assessment.License.Status != ReviewUnverified {
						t.Fatal("registry request retained prior verified authority")
					}
				}
				if name == "request" {
					return nil, errors.New("injected registry failure")
				}
				identity, _ := declaredNPMLicense("tool:npm", "12.2.1")
				if strings.HasPrefix(request.URL.Path, "/release-please/") {
					identity, _ = declaredNPMLicense("tool:release_please", "17.11.2")
				}
				body := fmt.Sprintf(`{"name":%q,"version":%q,"license":%q}`, identity.Name, identity.Version, identity.SPDX)
				status := http.StatusOK
				switch name {
				case "status":
					status = http.StatusNotFound
				case "redirect":
					status = http.StatusFound
				case "oversized":
					body = strings.Repeat(" ", npmLicenseResponseLimit+1)
				case "version":
					body = strings.Replace(body, identity.Version, "99.0.0", 1)
				case "SPDX":
					body = strings.Replace(body, identity.SPDX, "SEE LICENSE IN LICENSE", 1)
				case "alias":
					body = strings.Replace(body, `"license"`, `"License"`, 1)
				case "alias override":
					body = strings.Replace(body, fmt.Sprintf(`"license":%q`, identity.SPDX),
						fmt.Sprintf(`"license":"UNLICENSED","License":%q`, identity.SPDX), 1)
				case "duplicate":
					body = strings.Replace(body, `"name":`, `"name":"ignored","name":`, 1)
				case "null":
					body = strings.Replace(body, fmt.Sprintf("%q", identity.SPDX), "null", 1)
				case "trailing":
					body += "{}"
				default:
				}
				response := npmLicenseResponse(status, body)
				response.Header.Set("Location", "https://foreign.invalid/license")
				return response, nil
			})}
			if err := collectNPMLicenseEvidence(t.Context(), &inv, client); err == nil {
				t.Fatal("invalid exact-version license response accepted")
			}
			if calls != 2 || len(inv.Evidence) != 0 {
				t.Fatalf("requests=%d retained evidence=%d", calls, len(inv.Evidence))
			}
			for _, component := range inv.Components {
				if component.Assessment.License.Status != ReviewUnverified || len(component.Assessment.License.EvidenceIDs) != 0 {
					t.Fatal("failed request retained verified license")
				}
			}
		})
	}
}

func TestNPMLicenseCancellationInvalidatesBothTools(t *testing.T) {
	t.Parallel()
	inv := npmLicenseFixture()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})}
	if err := collectNPMLicenseEvidence(ctx, &inv, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if len(inv.Evidence) != 0 {
		t.Fatal("cancelled collection retained prior evidence")
	}
}

func TestNPMLicenseRejectsUnapprovedRequestIdentityBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, identity := range []npmLicenseIdentity{
		{Name: "npm", Version: "12.02.1", SPDX: "Artistic-2.0"},
		{Name: "../npm", Version: "12.2.1", SPDX: "Artistic-2.0"},
		{Name: "npm", Version: "12.2.1/latest", SPDX: "Artistic-2.0"},
	} {
		client := &http.Client{Transport: licenseRoundTripper(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid package identity made an HTTP request")
			return nil, errNPMLicense
		})}
		if err := fetchNPMLicense(t.Context(), client, identity); !errors.Is(err, errNPMLicense) {
			t.Fatalf("invalid request identity error = %v", err)
		}
	}
}

func TestNPMLicenseEvidenceRequiresCanonicalInstalledVersionAndInstrument(t *testing.T) {
	t.Parallel()
	inv := npmLicenseFixture()
	component := inv.Components[0]
	evidence := inv.Evidence[0]
	for _, name := range []string{"version", "host", "subject", "instrument", "query"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed := evidence
			switch name {
			case "version":
				changed.Source = strings.Replace(changed.Source, "12.2.1", "12.2.0", 1)
				changed.CommandOrQuery = "GET " + changed.Source
			case "host":
				changed.Source = "https://registry.npmjs.org.foreign.invalid/npm/12.2.1"
				changed.CommandOrQuery = "GET " + changed.Source
			case "subject":
				changed.Subject = "tool:unrelated"
			case "instrument":
				changed.Tool = EvidenceTool{Name: "dependencyinventory", Version: "self-attested"}
			case "query":
				changed.CommandOrQuery = "GET https://registry.npmjs.org/npm/latest"
			default:
			}
			byID := map[string]Evidence{changed.ID: changed}
			if err := validateNPMLicenseEvidence(component.ID, component.Record, byID); err == nil {
				t.Fatal("mismatched npm license evidence accepted")
			}
		})
	}
	identity, _ := declaredNPMLicense(component.ID, component.Installed)
	source, query, tool := evidenceContext(
		component.ID, reviewLicense, "old-source", "old-query", EvidenceTool{}, &component.Record,
	)
	if source != npmLicenseURL(identity) || query != "GET "+source || tool != npmLicenseTool() {
		t.Fatal("npm provenance did not resolve the canonical exact-version registry endpoint")
	}
}

func npmLicenseFixture() Inventory {
	inv := Inventory{AuditedAt: "2020-01-01T00:00:00Z"}
	for _, item := range []struct{ subject, version string }{{"tool:npm", "12.2.1"}, {"tool:release_please", "17.11.2"}} {
		identity, _ := declaredNPMLicense(item.subject, item.version)
		id := stableEvidenceID(item.subject, reviewLicense)
		record := Record{
			Installed: item.version,
			Assessment: Assessment{License: Review{
				Status: ReviewVerified, Value: identity.SPDX, EvidenceIDs: []string{id},
			}},
		}
		inv.Components = append(inv.Components, Component{
			ID: item.subject, Kind: KindTool, Record: record,
		})
		endpoint := npmLicenseURL(identity)
		inv.Evidence = append(inv.Evidence, Evidence{
			ID: id, Source: endpoint, Subject: item.subject, Review: reviewLicense,
			CommandOrQuery: "GET " + endpoint, Tool: npmLicenseTool(), ObservedAt: inv.AuditedAt,
			Result: identity.SPDX, Hash: evidenceResultHash(identity.SPDX),
		})
	}
	return inv
}

func npmLicenseResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Header: make(http.Header),
	}
}
