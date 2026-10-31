package dependencyinventory

import (
	"slices"
	"strings"
	"time"
)

const (
	reviewSecurity           = "security"
	reviewReleaseImpact      = "release_impact"
	reviewArtifactAvailable  = "artifact_available"
	reviewLicense            = "license"
	reviewRepositoryArchived = "repository_archived"
	reviewUpstreamCurrent    = "upstream_current"
	reviewChannelCurrent     = "channel_current"
	reviewReleaseLineEOL     = "release_line_eol"
	depsDevToolName          = "deps.dev"
)

func inventoryRecords(inv *Inventory) map[string]*Record {
	records := make(map[string]*Record)
	for graphIndex := range inv.ModuleGraphs {
		graph := &inv.ModuleGraphs[graphIndex]
		for index := range graph.Modules {
			module := &graph.Modules[index]
			records[graph.ID+":"+module.Path] = &module.Record
		}
	}
	for index := range inv.Components {
		component := &inv.Components[index]
		records[component.ID] = &component.Record
	}
	return records
}

func normalizePublicTracking(inv *Inventory) {
	for _, record := range inventoryRecords(inv) {
		decision := &record.Assessment.Decision
		decision.Tracking = publicTrackingReference(decision.Tracking)
		if decision.LicenseException != nil {
			decision.LicenseException.Tracking = publicTrackingReference(decision.LicenseException.Tracking)
		}
		if decision.ReviewException != nil {
			decision.ReviewException.Tracking = publicTrackingReference(decision.ReviewException.Tracking)
		}
	}
}

func publicTrackingReference(reference string) string {
	if strings.HasPrefix(reference, "gobfd-") || strings.HasPrefix(reference, "bead:gobfd-") ||
		strings.HasPrefix(reference, "bd show gobfd-") {
		return devTracking
	}
	return reference
}

func reuseHistoricalEvidence(inv *Inventory, prior Inventory) {
	// Only this generator's explicit observation epoch is a trusted reuse input.
	// Older snapshots remain available in Git without becoming fresh observations.
	if prior.AuditedAt != snapshotObservedAt {
		return
	}
	previous := inventoryRecords(&prior)
	evidenceByID := make(map[string]Evidence, len(prior.Evidence))
	for _, evidence := range prior.Evidence {
		evidenceByID[evidence.ID] = evidence
	}
	for subject, record := range inventoryRecords(inv) {
		old, ok := previous[subject]
		if !ok || record.ImmutablePin.Status != PinVerified ||
			record.Installed != old.Installed || record.ImmutablePin != old.ImmutablePin {
			continue
		}
		reuseRecordEvidence(inv, subject, record, old, evidenceByID)
	}
	slices.SortFunc(inv.Evidence, func(left, right Evidence) int { return strings.Compare(left.ID, right.ID) })
}

func reuseRecordEvidence(inv *Inventory, subject string, record, old *Record, evidenceByID map[string]Evidence) {
	oldReviews := recordReviews(old)
	for index, binding := range recordReviews(record) {
		if binding.name == reviewSecurity || binding.name == reviewReleaseImpact ||
			(binding.name == reviewArtifactAvailable && binding.review.Status == ReviewVerified) {
			continue
		}
		historicalReview := oldReviews[index].review
		evidence, ok := historicalReviewEvidence(subject, binding.name, historicalReview, evidenceByID)
		if !ok || !evidenceMatchesRecord(subject, binding.name, evidence, record) {
			continue
		}
		*binding.review = *historicalReview
		if slices.Contains([]string{
			reviewRepositoryArchived, reviewUpstreamCurrent, reviewChannelCurrent, reviewReleaseLineEOL,
		}, binding.name) {
			binding.review.Status = ReviewStale
		}
		inv.Evidence = append(inv.Evidence, evidence)
	}
}

func historicalReviewEvidence(
	subject, review string, historical *Review, evidenceByID map[string]Evidence,
) (Evidence, bool) {
	if (historical.Status != ReviewVerified && historical.Status != ReviewStale) || len(historical.EvidenceIDs) != 1 {
		return Evidence{}, false
	}
	evidence, found := evidenceByID[historical.EvidenceIDs[0]]
	if !found || evidence.Subject != subject || evidence.Review != review ||
		evidence.Result != historical.Value || validateEvidence(evidence) != nil {
		return Evidence{}, false
	}
	observed, parseErr := time.Parse(time.RFC3339, evidence.ObservedAt)
	snapshot, snapshotErr := time.Parse(time.RFC3339, snapshotObservedAt)
	return evidence, parseErr == nil && snapshotErr == nil && !observed.After(snapshot)
}

func evidenceMatchesRecord(subject, review string, evidence Evidence, record *Record) bool {
	if review == reviewLicense {
		if identity, ok := recordModuleIdentity(subject, record); ok {
			endpoint := depsDevVersionURL(identity.Path, identity.Version)
			if evidence.Source == endpoint && evidence.CommandOrQuery == "GET "+endpoint &&
				evidence.Tool == (EvidenceTool{Name: depsDevToolName, Version: "v3"}) {
				return true
			}
		}
	}
	source, query, tool := evidenceContext(subject, review, evidence.Source,
		evidence.CommandOrQuery, evidence.Tool, record)
	return source == evidence.Source && query == evidence.CommandOrQuery && tool == evidence.Tool
}

func recordModuleIdentity(subject string, record *Record) (moduleLicenseIdentity, bool) {
	for _, graph := range moduleGraphDefinitions() {
		if path, ok := strings.CutPrefix(subject, graph.ID+":"); ok {
			return moduleLicenseIdentity{Path: path, Version: record.Installed}, true
		}
	}
	return declaredToolModule(subject, record.Installed)
}

func invalidateUnobservedLicenses(inv *Inventory, results map[moduleLicenseIdentity]moduleLicenseResult) {
	for subject, record := range inventoryRecords(inv) {
		identity, ok := recordModuleIdentity(subject, record)
		if !ok || record.Assessment.License.Status == ReviewNotApplicable {
			continue
		}
		if _, succeeded := results[identity]; !succeeded {
			record.Assessment.License = Review{Status: ReviewUnverified, Value: "exact-version license request failed"}
			replaceLicenseEvidence(inv, subject, record, nil)
		}
	}
}

func replaceLicenseEvidence(inv *Inventory, subject string, record *Record, evidence *Evidence) {
	id := stableEvidenceID(subject, reviewLicense)
	inv.Evidence = slices.DeleteFunc(inv.Evidence, func(item Evidence) bool { return item.ID == id })
	record.Assessment.License.EvidenceIDs = []string{}
	if evidence != nil {
		record.Assessment.License.EvidenceIDs = []string{id}
		inv.Evidence = append(inv.Evidence, *evidence)
	}
}

func observeDepsDevLicense(inv *Inventory, subject string, record *Record, identity moduleLicenseIdentity) {
	endpoint := depsDevVersionURL(identity.Path, identity.Version)
	evidence := Evidence{
		ID: stableEvidenceID(subject, reviewLicense), Source: endpoint, Subject: subject, Review: reviewLicense,
		CommandOrQuery: "GET " + endpoint, Tool: EvidenceTool{Name: depsDevToolName, Version: "v3"},
		ObservedAt: inv.AuditedAt, Result: record.Assessment.License.Value,
		Hash: evidenceResultHash(record.Assessment.License.Value),
	}
	replaceLicenseEvidence(inv, subject, record, &evidence)
}
