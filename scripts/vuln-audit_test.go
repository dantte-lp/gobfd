package main

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyFindingsHonorsAllowlistExpiry(t *testing.T) {
	t.Parallel()

	entries := map[string]allowEntry{
		"GO-2099-0001": {
			Package:    "example.com/module",
			Owner:      "netops",
			Expires:    "2026-04-30",
			Reason:     "waiting for upstream fix",
			Mitigation: "localhost only",
		},
	}
	findings := []finding{{ID: "GO-2099-0001", Package: "example.com/module"}}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(allowed) != 0 {
		t.Fatalf("allowed = %v, want none", allowed)
	}
	if len(unallowed) != 0 {
		t.Fatalf("unallowed = %v, want none", unallowed)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "expired on 2026-04-30") {
		t.Fatalf("failures = %v, want expiry failure", failures)
	}
}

func TestClassifyFindingsSeparatesAllowedAndUnallowed(t *testing.T) {
	t.Parallel()

	entries := map[string]allowEntry{
		"GO-2099-0001": {
			Package:    "example.com/module",
			Owner:      "netops",
			Expires:    "2026-12-31",
			Reason:     "waiting for upstream fix",
			Mitigation: "localhost only",
		},
	}
	findings := []finding{
		{ID: "GO-2099-0001", Package: "example.com/module"},
		{ID: "GO-2099-0002", Package: "example.com/other"},
	}

	allowed, unallowed, failures := classifyFindings(findings, entries, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	if got := len(allowed["GO-2099-0001"]); got != 1 {
		t.Fatalf("allowed GO-2099-0001 count = %d, want 1", got)
	}
	if got := len(unallowed["GO-2099-0002"]); got != 1 {
		t.Fatalf("unallowed GO-2099-0002 count = %d, want 1", got)
	}
}
