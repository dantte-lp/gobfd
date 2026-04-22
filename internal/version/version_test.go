package appversion_test

import (
	"strings"
	"testing"

	appversion "github.com/dantte-lp/gobfd/internal/version"
)

func TestFull_ContainsBinaryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		binary string
	}{
		{name: "gobfd daemon", binary: "gobfd"},
		{name: "haproxy agent", binary: "gobfd-haproxy-agent"},
		{name: "exabgp bridge", binary: "gobfd-exabgp-bridge"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := appversion.Full(tt.binary)
			if !strings.Contains(got, tt.binary) {
				t.Errorf("appversion.Full(%q) = %q, want string containing %q", tt.binary, got, tt.binary)
			}
		})
	}
}

func TestFull_DefaultValues(t *testing.T) {
	t.Parallel()

	// Package-level vars have default values: Version="dev", GitCommit="unknown", BuildDate="unknown".
	// These are only overridden via ldflags at build time, so in tests they remain at defaults.
	got := appversion.Full("gobfd")

	if !strings.Contains(got, "dev") {
		t.Errorf("appversion.Full() = %q, want string containing default version %q", got, "dev")
	}
	if !strings.Contains(got, "unknown") {
		t.Errorf("appversion.Full() = %q, want string containing default commit/date %q", got, "unknown")
	}
}

func TestFull_Format(t *testing.T) {
	t.Parallel()

	got := appversion.Full("gobfd")

	// Expected format:
	//   gobfd dev
	//     commit:  unknown
	//     built:   unknown
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("appversion.Full() produced %d lines, want 3; got:\n%s", len(lines), got)
	}

	// Line 0: "<binary> <version>"
	if !strings.HasPrefix(lines[0], "gobfd ") {
		t.Errorf("line 0 = %q, want prefix %q", lines[0], "gobfd ")
	}

	// Line 1: "  commit:  <commit>"
	if !strings.Contains(lines[1], "commit:") {
		t.Errorf("line 1 = %q, want it to contain %q", lines[1], "commit:")
	}

	// Line 2: "  built:   <date>"
	if !strings.Contains(lines[2], "built:") {
		t.Errorf("line 2 = %q, want it to contain %q", lines[2], "built:")
	}
}

func TestFull_OverriddenValues(t *testing.T) {
	// Save and restore package-level vars.
	origVersion := appversion.Version
	origCommit := appversion.GitCommit
	origDate := appversion.BuildDate
	t.Cleanup(func() {
		appversion.Version = origVersion
		appversion.GitCommit = origCommit
		appversion.BuildDate = origDate
	})

	appversion.Version = "v1.2.3"
	appversion.GitCommit = "abc1234"
	appversion.BuildDate = "2026-02-22T12:00:00Z"

	got := appversion.Full("gobfd")

	for _, want := range []string{"v1.2.3", "abc1234", "2026-02-22T12:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("appversion.Full() = %q, want string containing %q", got, want)
		}
	}
}
