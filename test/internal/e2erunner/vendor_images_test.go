package e2erunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
	manifestDir := filepath.Join(root, "test", "e2e", "vendor")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatalf("create manifest directory: %v", err)
	}
	manifest := `{"profiles":[{"id":"frr","profile_class":"baseline","images":[],` +
		`"build":{"context":"test/interop/frr","dockerfile":"Containerfile"},"skip_policy":"missing-image"}]}`
	if err := os.WriteFile(filepath.Join(manifestDir, "profiles.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
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
	if len(evidence) != 1 {
		t.Fatalf("vendor image count = %d, want 1", len(evidence))
	}
	item := evidence[0]
	if item.ProfileID != "frr" || item.ProfileClass != "baseline" ||
		len(item.Images) != 0 || len(item.Available) != 0 ||
		item.Build["context"] != "test/interop/frr" || item.Build["dockerfile"] != "Containerfile" ||
		item.Status != "owned-receipt-required" || item.Reason != "run-owned-build" {
		t.Fatalf("build profile evidence loses recipe or implies image availability: %s", data)
	}
}
