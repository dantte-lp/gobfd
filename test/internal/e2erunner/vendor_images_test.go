package e2erunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVendorRetainedUnavailableDoesNotBecomeAvailableFromCache(t *testing.T) {
	root := t.TempDir()
	commandMarker := filepath.Join(root, "podman-called")
	commandDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(commandDir, "podman"),
		[]byte("#!/bin/sh\nprintf called > \"$GOBFD_VENDOR_COMMAND_MARKER\"\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write cached-image Podman sentinel: %v", err)
	}
	t.Setenv("PATH", commandDir)
	t.Setenv("GOBFD_VENDOR_COMMAND_MARKER", commandMarker)
	writeRetainedVendorManifest(t, root)
	r := &runner{root: root, reportDir: t.TempDir()}
	if err := r.writeVendorImages(t.Context()); err != nil {
		t.Fatalf("write vendor images: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(r.reportDir, "vendor-images.json"))
	if err != nil {
		t.Fatalf("read vendor images: %v", err)
	}
	var evidence []struct {
		ProfileID             string   `json:"profile_id"`
		ExecutionPolicy       string   `json:"execution_policy"`
		QualificationTracking string   `json:"qualification_tracking"`
		Available             []string `json:"available"`
		Status                string   `json:"status"`
		Reason                string   `json:"reason"`
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode vendor images: %v", err)
	}
	want := map[string]bool{
		"arista-ceos": false, "nokia-srlinux": false, "cisco-xrd": false,
		"sonic-vs": false, "vyos": false, "frr": false,
	}
	if len(evidence) != len(want) {
		t.Fatalf("vendor image count = %d, want 6", len(evidence))
	}
	for _, item := range evidence {
		seen, known := want[item.ProfileID]
		if !known || seen {
			t.Fatalf("unexpected or duplicate profile %q", item.ProfileID)
		}
		want[item.ProfileID] = true
		policy, tracking := "retained-unavailable", "gobfd-qj0.8.2.8.5.2.7.4"
		if item.ProfileID == "frr" {
			policy, tracking = "owned-receipt-required", "gobfd-qj0.8.2.8.5.2.7.3"
		}
		if item.Status != policy || item.ExecutionPolicy != policy ||
			item.QualificationTracking != tracking || len(item.Available) != 0 || item.Reason == "" {
			t.Errorf("profile %s loses execution boundary despite cached images: %+v", item.ProfileID, item)
		}
	}
	if _, err := os.Stat(commandMarker); !os.IsNotExist(err) {
		t.Errorf("retained profiles performed a Podman availability query: %v", err)
	}
}

func writeRetainedVendorManifest(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "e2e", "vendor", "profiles.json"))
	if err != nil {
		t.Fatalf("read repository vendor manifest: %v", err)
	}
	var manifest struct {
		Runtime  json.RawMessage  `json:"runtime"`
		Profiles []map[string]any `json:"profiles"`
	}
	if decodeErr := json.Unmarshal(data, &manifest); decodeErr != nil {
		t.Fatalf("decode repository vendor manifest: %v", decodeErr)
	}
	if len(manifest.Profiles) != 6 {
		t.Fatalf("repository vendor profile count = %d, want 6", len(manifest.Profiles))
	}
	for _, profile := range manifest.Profiles {
		profile["execution_policy"] = "retained-unavailable"
		profile["qualification_tracking"] = "gobfd-qj0.8.2.8.5.2.7.4"
		if profile["id"] == "frr" {
			profile["execution_policy"] = "owned-receipt-required"
			profile["qualification_tracking"] = "gobfd-qj0.8.2.8.5.2.7.3"
		}
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode retained vendor manifest: %v", err)
	}
	path := filepath.Join(root, "test", "e2e", "vendor", "profiles.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create manifest directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write retained vendor manifest: %v", err)
	}
}

func TestVendorBuildEvidenceRequiresOwnedReceipt(t *testing.T) {
	root := t.TempDir()
	commandMarker := filepath.Join(root, "podman-called")
	commandDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(commandDir, "podman"),
		[]byte("#!/bin/sh\nprintf called > \"$GOBFD_VENDOR_COMMAND_MARKER\"\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write Podman command sentinel: %v", err)
	}
	t.Setenv("PATH", commandDir)
	t.Setenv("GOBFD_VENDOR_COMMAND_MARKER", commandMarker)
	writeRetainedVendorManifest(t, root)
	r := &runner{root: root, reportDir: t.TempDir()}
	if err := r.writeVendorImages(t.Context()); err != nil {
		t.Fatalf("write vendor images: %v", err)
	}
	if _, err := os.Stat(commandMarker); !os.IsNotExist(err) {
		t.Fatalf("build profile executed Podman command: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(r.reportDir, "vendor-images.json"))
	if err != nil {
		t.Fatalf("read vendor images: %v", err)
	}
	var evidence []struct {
		ProfileID    string            `json:"profile_id"`
		ProfileClass string            `json:"profile_class"`
		Images       []string          `json:"images"`
		Build        map[string]string `json:"build"`
		Available    []string          `json:"available"`
		Status       string            `json:"status"`
		Reason       string            `json:"reason"`
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode vendor images: %v", err)
	}
	if len(evidence) != 6 {
		t.Fatalf("vendor image count = %d, want 6", len(evidence))
	}
	for _, item := range evidence {
		if item.ProfileID != "frr" {
			continue
		}
		if item.ProfileClass != "baseline" || len(item.Images) != 0 || len(item.Available) != 0 ||
			item.Build["context"] != "test/interop/frr" || item.Build["dockerfile"] != "Containerfile" ||
			item.Status != "owned-receipt-required" || item.Reason != "run-owned-build" {
			t.Fatalf("build profile evidence loses recipe or implies image availability: %s", data)
		}
		return
	}
	t.Fatal("FRR build profile evidence is missing")
}
