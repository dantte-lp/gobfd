package dependencyinventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

const (
	upxToolID           = "tool:upx"
	licenseFileName     = "LICENSE"
	upxSourcePath       = "test/internal/cirunner/upx.go"
	upxRepositoryURL    = "https://github.com/upx/upx"
	upxLicenseValue     = "GPL-2.0-or-later; upstream special exception for compressed executables"
	upxLicenseFileLimit = 32 << 10
	upxLicenseTimeout   = 20 * time.Second
)

var errUPXLicense = errors.New("invalid exact-release UPX license evidence")

func upxDeclaration() declaredComponent {
	assets := cirunner.ReleaseUPXAssets()
	sources := make([]SourceLocation, 0, 1+len(assets))
	sources = append(sources, SourceLocation{Path: upxSourcePath, Match: `"` + assets[0].Version + `"`})
	pins := make([]string, 0, len(assets))
	for _, asset := range assets {
		sources = append(sources, SourceLocation{Path: upxSourcePath, Match: asset.SHA256})
		pins = append(pins, asset.ArchiveName+"@sha256:"+asset.SHA256)
	}
	return declaredComponent{
		ID: upxToolID, Kind: KindTool, Installed: assets[0].Version, Sources: sources,
		ContentPin: ImmutablePin{Status: PinVerified, Kind: "release-assets-sha256", Value: strings.Join(pins, "; ")},
	}
}

func upxCoordinates() Coordinates {
	asset := cirunner.ReleaseUPXAssets()[0]
	return Coordinates{
		SourceRepository: upxRepositoryURL, SourceCommit: asset.SourceCommit,
		PURL: "pkg:github/upx/upx@" + asset.Version,
	}
}

func discoverUPXDeclaration(components map[string]declaredComponent, data []byte) error {
	item := upxDeclaration()
	for _, source := range item.Sources {
		if !bytes.Contains(data, []byte(source.Match)) {
			return fmt.Errorf("UPX inventory metadata has no matching source declaration %q: %w", source.Match, errUPXLicense)
		}
	}
	addDeclared(components, item)
	return nil
}

func upxLicenseContext() (string, string, EvidenceTool) {
	asset := cirunner.ReleaseUPXAssets()[0]
	base := "https://raw.githubusercontent.com/upx/upx/" + asset.SourceCommit + "/"
	return base + licenseFileName,
		"GET " + base + licenseFileName + " sha256:" + asset.LicenseSHA256 +
			" && GET " + base + "COPYING sha256:" + asset.CopyingSHA256,
		EvidenceTool{Name: "upstream-license-files", Version: "sha256-v1"}
}

func collectUPXLicenseEvidence(ctx context.Context, inv *Inventory, supplied *http.Client) error {
	for index := range inv.Components {
		component := &inv.Components[index]
		if component.ID != upxToolID || component.Kind != KindTool {
			continue
		}
		component.Assessment.License = Review{Status: ReviewUnverified, Value: "exact-release UPX license request pending"}
		replaceLicenseEvidence(inv, component.ID, &component.Record, nil)
		if err := fetchUPXLicense(ctx, supplied, component.Record); err != nil {
			component.Assessment.License.Value = "exact-release UPX license request failed"
			return err
		}
		component.Assessment.License = Review{Status: ReviewVerified, Value: upxLicenseValue}
		component.Assessment.Decision.LicenseException = nil
		observeUPXLicense(inv, component.ID, &component.Record)
	}
	return nil
}

func observeUPXLicense(inv *Inventory, subject string, record *Record) {
	source, command, tool := upxLicenseContext()
	evidence := Evidence{
		ID: stableEvidenceID(subject, reviewLicense), Source: source, Subject: subject, Review: reviewLicense,
		CommandOrQuery: command, Tool: tool, ObservedAt: time.Now().UTC().Format(time.RFC3339),
		Result: upxLicenseValue, Hash: evidenceResultHash(upxLicenseValue),
	}
	replaceLicenseEvidence(inv, subject, record, &evidence)
}

func fetchUPXLicense(ctx context.Context, supplied *http.Client, record Record) error {
	asset := cirunner.ReleaseUPXAssets()[0]
	if record.Installed != asset.Version || record.ImmutablePin != upxDeclaration().ContentPin {
		return fmt.Errorf("UPX license identity does not match the selected archive contracts: %w", errUPXLicense)
	}
	client := http.Client{Timeout: upxLicenseTimeout}
	if supplied != nil {
		client.Transport = supplied.Transport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	files := []struct{ name, sha256 string }{{licenseFileName, asset.LicenseSHA256}, {"COPYING", asset.CopyingSHA256}}
	for _, file := range files {
		endpoint := "https://raw.githubusercontent.com/upx/upx/" + asset.SourceCommit + "/" + file.name
		if err := fetchUPXLicenseFile(ctx, &client, endpoint, file.sha256); err != nil {
			return fmt.Errorf("collect UPX %s: %w", file.name, err)
		}
	}
	return nil
}

func fetchUPXLicenseFile(ctx context.Context, client *http.Client, endpoint, sha256 string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create UPX license request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("query UPX license source: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, upxLicenseFileLimit+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return fmt.Errorf("read UPX license source: %w", err)
	}
	if response.StatusCode != http.StatusOK || len(data) > upxLicenseFileLimit ||
		contentDigest(data) != "sha256:"+sha256 {
		return fmt.Errorf("UPX license response status, size, or source hash mismatch: %w", errUPXLicense)
	}
	return nil
}

func upxLicenseCommandProvesReview(evidence Evidence) bool {
	source, command, tool := upxLicenseContext()
	return evidence.Subject == upxToolID && evidence.Review == reviewLicense && evidence.Source == source &&
		evidence.CommandOrQuery == command && evidence.Tool == tool && evidence.Result == upxLicenseValue
}

func validateUPXLicenseEvidence(subject string, record Record, evidenceByID map[string]Evidence) error {
	if subject != upxToolID || record.Assessment.License.Status != ReviewVerified {
		return nil
	}
	asset := cirunner.ReleaseUPXAssets()[0]
	license := record.Assessment.License
	if record.Installed != asset.Version || record.ImmutablePin != upxDeclaration().ContentPin ||
		record.Coordinates.SourceRepository != upxRepositoryURL || record.Coordinates.SourceCommit != asset.SourceCommit ||
		len(license.EvidenceIDs) != 1 || license.Value != upxLicenseValue {
		return fmt.Errorf("UPX license does not match selected release identity: %w", errUPXLicense)
	}
	evidence, exists := evidenceByID[license.EvidenceIDs[0]]
	if !exists || !upxLicenseCommandProvesReview(evidence) {
		return fmt.Errorf("UPX license requires exact-release source hash evidence: %w", errUPXLicense)
	}
	return nil
}
