package dependencyinventory

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

func TestUPXInventoryUsesAllBootstrapAssetPins(t *testing.T) {
	t.Parallel()

	item := upxDeclaration()
	components := make(map[string]declaredComponent)
	var source strings.Builder
	for _, location := range item.Sources {
		source.WriteString(location.Match)
		source.WriteByte('\n')
	}
	if err := discoverUPXDeclaration(components, []byte(source.String())); err != nil {
		t.Fatalf("discover UPX: %v", err)
	}
	for _, asset := range cirunner.ReleaseUPXAssets() {
		if item.Installed != asset.Version ||
			!strings.Contains(item.ContentPin.Value, asset.ArchiveName+"@sha256:"+asset.SHA256) {
			t.Fatalf("UPX inventory omitted bootstrap asset %s", asset.Architecture)
		}
	}
	if err := discoverUPXDeclaration(components, []byte(`"outdated"`)); err == nil {
		t.Fatal("UPX compiled metadata accepted a stale source declaration")
	}
}

func TestUPXLicenseFileRequiresBoundedSuccessfulHashMatch(t *testing.T) {
	t.Parallel()

	const fixture = "license fixture"
	for _, test := range []struct {
		name string
		body string
		code int
		want bool
	}{
		{name: "verified", body: fixture, code: http.StatusOK, want: true},
		{name: "hash mismatch", body: "different", code: http.StatusOK},
		{name: "not found", body: fixture, code: http.StatusNotFound},
		{name: "oversized", body: strings.Repeat("x", upxLicenseFileLimit+1), code: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.Header.Get("Authorization") != "" ||
					request.Header.Get("Cookie") != "" {
					t.Fatal("UPX license request is not a public GET")
				}
				return &http.Response{StatusCode: test.code, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			source, _, _ := upxLicenseContext()
			sha := strings.TrimPrefix(contentDigest([]byte(fixture)), "sha256:")
			err := fetchUPXLicenseFile(t.Context(), client, source, sha)
			if (err == nil) != test.want {
				t.Fatalf("license hash verification = %v, want success %t", err, test.want)
			}
		})
	}
}

func TestUPXLicenseFailureInvalidatesPriorObservation(t *testing.T) {
	t.Parallel()

	inv := upxLicenseFixture()
	calls := 0
	client := &http.Client{Transport: licenseRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		deadline, bounded := request.Context().Deadline()
		if !bounded || time.Until(deadline) > 20*time.Second {
			t.Fatal("UPX source request has no bounded deadline")
		}
		return nil, errUPXLicense
	})}
	err := collectUPXLicenseEvidence(t.Context(), &inv, client)
	if !errors.Is(err, errUPXLicense) || calls != 1 || len(inv.Evidence) != 0 ||
		inv.Components[0].Assessment.License.Status != ReviewUnverified {
		t.Fatalf("failed refresh retained verified UPX license: error=%v, calls=%d, inventory=%#v", err, calls, inv)
	}
}

func TestUPXLicenseObservationRequiresCurrentReleaseAndSourceHashes(t *testing.T) {
	t.Parallel()

	before := time.Now().UTC().Truncate(time.Second)
	inv := upxLicenseFixture()
	observed, err := time.Parse(time.RFC3339, inv.Evidence[0].ObservedAt)
	if err != nil || observed.Before(before) || observed.After(time.Now().UTC()) {
		t.Fatalf("UPX observation is not current UTC: %q, %v", inv.Evidence[0].ObservedAt, err)
	}
	for _, mutation := range []string{"none", "version", "pin", "commit", "hash", "query"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()

			component := inv.Components[0]
			evidence := inv.Evidence[0]
			switch mutation {
			case "version":
				component.Installed = "0.0.0"
			case "pin":
				component.ImmutablePin.Value += "0"
			case "commit":
				component.Coordinates.SourceCommit = strings.Repeat("0", 40)
			case "hash":
				evidence.CommandOrQuery += "0"
			case "query":
				evidence.Source = "https://untrusted.invalid/LICENSE"
			case "none":
			default:
			}
			err := validateUPXLicenseEvidence(component.ID, component.Record, map[string]Evidence{evidence.ID: evidence})
			if (err == nil) != (mutation == "none") {
				t.Fatalf("UPX license identity validation = %v for %s", err, mutation)
			}
		})
	}
}

func TestUPXLicenseRejectsChangedReleaseBeforeNetwork(t *testing.T) {
	t.Parallel()

	inv := upxLicenseFixture()
	inv.Components[0].Installed = "0.0.0"
	client := &http.Client{Transport: licenseRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("changed UPX release made a source request")
		return nil, errUPXLicense
	})}
	if err := collectUPXLicenseEvidence(context.Background(), &inv, client); !errors.Is(err, errUPXLicense) {
		t.Fatalf("changed UPX release = %v", err)
	}
}

func upxLicenseFixture() Inventory {
	item := upxDeclaration()
	record := baseRecord(item.Installed, upxRepositoryURL+"/releases", item.Sources, item.ContentPin)
	record.Coordinates = upxCoordinates()
	record.Assessment = assessmentTemplate("tool-version-audited")
	record.RepositoryState = repositoryStateTemplate("tool-version-audited", item.ContentPin)
	record.Assessment.License = Review{Status: ReviewVerified, Value: upxLicenseValue}
	inv := Inventory{
		AuditedAt: snapshotObservedAt, Components: []Component{{ID: upxToolID, Kind: KindTool, Record: record}},
	}
	observeUPXLicense(&inv, item.ID, &inv.Components[0].Record)
	return inv
}
