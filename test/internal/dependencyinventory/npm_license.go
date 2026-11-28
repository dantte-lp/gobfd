package dependencyinventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

const (
	npmLicenseRegistry      = "https://registry.npmjs.org"
	npmLicenseResponseLimit = 1 << 20
	npmLicenseVersionLimit  = 64
	npmLicenseHTTPTimeout   = 20 * time.Second
)

var errNPMLicense = errors.New("invalid exact-version npm license evidence")

type npmLicenseIdentity struct {
	Name    string
	Version string
	SPDX    string
}

func declaredNPMLicense(subject, version string) (npmLicenseIdentity, bool) {
	switch subject {
	case npmToolID:
		return npmLicenseIdentity{Name: "npm", Version: version, SPDX: "Artistic-2.0"}, true
	case releasePleaseToolID:
		return npmLicenseIdentity{Name: "release-please", Version: version, SPDX: licenseApache2}, true
	default:
		return npmLicenseIdentity{}, false
	}
}

func npmLicenseURL(identity npmLicenseIdentity) string {
	return npmLicenseRegistry + "/" + identity.Name + "/" + identity.Version
}

func npmLicenseTool() EvidenceTool {
	// The exact-version registry metadata endpoint has no published API version.
	return EvidenceTool{Name: "npm-registry", Version: "unversioned"}
}

func collectNPMLicenseEvidence(ctx context.Context, inv *Inventory, client *http.Client) error {
	// Invalidate every selected npm record before any request can fail or be cancelled.
	for index := range inv.Components {
		component := &inv.Components[index]
		if _, selected := declaredNPMLicense(component.ID, component.Installed); !selected || component.Kind != KindTool {
			continue
		}
		component.Assessment.License = Review{Status: ReviewUnverified, Value: "exact-version npm license request pending"}
		replaceLicenseEvidence(inv, component.ID, &component.Record, nil)
	}
	var failures []error
	for index := range inv.Components {
		component := &inv.Components[index]
		identity, selected := declaredNPMLicense(component.ID, component.Installed)
		if !selected || component.Kind != KindTool {
			continue
		}
		if err := fetchNPMLicense(ctx, client, identity); err != nil {
			component.Assessment.License.Value = "exact-version npm license request failed"
			failures = append(failures, err)
			continue
		}
		component.Assessment.License = Review{Status: ReviewVerified, Value: identity.SPDX}
		component.Assessment.Decision.LicenseException = nil
		observeNPMLicense(inv, component.ID, &component.Record, identity)
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("collect npm package licenses: %w", err)
	}
	return nil
}

func fetchNPMLicense(ctx context.Context, supplied *http.Client, identity npmLicenseIdentity) error {
	if !validNPMLicenseIdentity(identity) {
		return fmt.Errorf("npm license request requires an approved package and canonical version: %w", errNPMLicense)
	}
	client := http.Client{Timeout: npmLicenseHTTPTimeout}
	if supplied != nil {
		client.Transport = supplied.Transport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, npmLicenseURL(identity), nil)
	if err != nil {
		return fmt.Errorf("create npm license request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("query npm license for %s@%s: %w", identity.Name, identity.Version, err)
	}
	body, readErr := readNPMLicenseResponse(response)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return fmt.Errorf("read npm license for %s@%s: %w", identity.Name, identity.Version, err)
	}
	return validateNPMLicenseMetadata(body, identity)
}

func readNPMLicenseResponse(response *http.Response) ([]byte, error) {
	if response.StatusCode != http.StatusOK || response.ContentLength > npmLicenseResponseLimit {
		return nil, fmt.Errorf("npm metadata response status or size: %w", errNPMLicense)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, npmLicenseResponseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded npm metadata: %w", err)
	}
	if len(body) > npmLicenseResponseLimit {
		return nil, fmt.Errorf("npm metadata exceeds response bound: %w", errNPMLicense)
	}
	return body, nil
}

func validateNPMLicenseMetadata(data []byte, identity npmLicenseIdentity) error {
	if err := strictjson.Validate(data); err != nil {
		return fmt.Errorf("validate npm license JSON: %w", err)
	}
	var name, version, license string
	fields := map[string]any{"name": &name, "version": &version, reviewLicense: &license}
	if err := decodeReleasePleaseFields(data, fields); err != nil {
		return fmt.Errorf("decode canonical npm license fields: %w", err)
	}
	if name != identity.Name || version != identity.Version || license != identity.SPDX {
		return fmt.Errorf("npm metadata identity or SPDX mismatch for %s@%s: %w",
			identity.Name, identity.Version, errNPMLicense)
	}
	return nil
}

func observeNPMLicense(inv *Inventory, subject string, record *Record, identity npmLicenseIdentity) {
	endpoint := npmLicenseURL(identity)
	evidence := Evidence{
		ID: stableEvidenceID(subject, reviewLicense), Source: endpoint, Subject: subject, Review: reviewLicense,
		CommandOrQuery: "GET " + endpoint, Tool: npmLicenseTool(), ObservedAt: time.Now().UTC().Format(time.RFC3339),
		Result: identity.SPDX, Hash: evidenceResultHash(identity.SPDX),
	}
	replaceLicenseEvidence(inv, subject, record, &evidence)
}

func npmLicenseCommandProvesReview(evidence Evidence) bool {
	identity, selected := declaredNPMLicense(evidence.Subject, "")
	if !selected || evidence.Tool != npmLicenseTool() || evidence.Result != identity.SPDX ||
		evidence.Review != reviewLicense || evidence.CommandOrQuery != "GET "+evidence.Source {
		return false
	}
	version, found := strings.CutPrefix(evidence.Source, npmLicenseURL(identity))
	return found && validNPMLicenseVersion(version)
}

func validateNPMLicenseEvidence(subject string, record Record, evidenceByID map[string]Evidence) error {
	identity, selected := declaredNPMLicense(subject, record.Installed)
	if !selected || record.Assessment.License.Status != ReviewVerified {
		return nil
	}
	license := record.Assessment.License
	if len(license.EvidenceIDs) != 1 {
		return fmt.Errorf("npm license requires one exact-version observation: %w", errNPMLicense)
	}
	evidence, found := evidenceByID[license.EvidenceIDs[0]]
	if !found || evidence.Subject != subject || !npmLicenseCommandProvesReview(evidence) ||
		evidence.Source != npmLicenseURL(identity) || license.Value != identity.SPDX {
		return fmt.Errorf("npm license evidence does not match installed package version: %w", errNPMLicense)
	}
	return nil
}

func validNPMLicenseIdentity(identity npmLicenseIdentity) bool {
	if !validNPMLicenseVersion(identity.Version) {
		return false
	}
	for _, subject := range []string{npmToolID, releasePleaseToolID} {
		expected, _ := declaredNPMLicense(subject, identity.Version)
		if expected == identity {
			return true
		}
	}
	return false
}

func validNPMLicenseVersion(version string) bool {
	return len(version) <= npmLicenseVersionLimit && npmExactVersionPattern.MatchString(version)
}
