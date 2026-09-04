package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRPMUsesNativeLifecyclePrograms(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := map[string]string{
		"dist/gobfd_linux_amd64_v1/gobfd":                                     "gobfd",
		"dist/gobfdctl_linux_amd64_v1/gobfdctl":                               "gobfdctl",
		"dist/gobfd-haproxy-agent_linux_amd64_v1/gobfd-haproxy-agent":         "haproxy",
		"dist/gobfd-exabgp-bridge_linux_amd64_v1/gobfd-exabgp-bridge":         "exabgp",
		"dist/gobfd-package-lifecycle_linux_amd64_v1/gobfd-package-lifecycle": "lifecycle",
		"configs/gobfd.example.yml":                                           "config",
		"deployments/systemd/gobfd.service":                                   "service",
		"deployments/systemd/gobfd.sysusers":                                  "sysusers",
		"deployments/systemd/gobfd.tmpfiles":                                  "tmpfiles",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var output bytes.Buffer
	if err := writeRPM(&output, root, "0.6.5", "amd64"); err != nil {
		t.Fatalf("writeRPM() error = %v", err)
	}
	data := output.Bytes()
	for _, want := range [][]byte{
		[]byte("/usr/libexec/gobfd-postinstall"),
		[]byte("/usr/libexec/gobfd-preremove"),
	} {
		if !bytes.Contains(data, want) {
			t.Errorf("RPM does not contain %q", want)
		}
	}
	if bytes.Contains(data, []byte("/bin/sh")) {
		t.Error("RPM retains /bin/sh scriptlet program")
	}
}

func TestRefreshArtifactManifestUpdatesFinalizedRPMChecksums(t *testing.T) {
	t.Parallel()

	dist := t.TempDir()
	artifacts := []map[string]any{
		{"name": "amd64", "path": "dist/gobfd_0.6.5_linux_amd64.rpm", "extra": map[string]string{"Checksum": "stale"}},
		{"name": "arm64", "path": "dist/gobfd_0.6.5_linux_arm64.rpm", "extra": map[string]string{"Checksum": "stale"}},
	}
	manifest, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(filepath.Join(dist, "artifacts.json"), manifest, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	for _, arch := range []string{amd64, arm64} {
		path := filepath.Join(dist, "gobfd_0.6.5_linux_"+arch+".rpm")
		if writeErr := os.WriteFile(path, []byte(arch), 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if refreshErr := refreshArtifactManifest(dist, "0.6.5"); refreshErr != nil {
		t.Fatalf("refreshArtifactManifest() error = %v", refreshErr)
	}
	updated, err := os.ReadFile(filepath.Join(dist, "artifacts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{amd64, arm64} {
		want := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(arch)))
		if !bytes.Contains(updated, []byte(want)) {
			t.Errorf("artifact manifest lacks %s checksum %q", arch, want)
		}
	}
	if !bytes.Contains(updated, []byte(`"name": "amd64"`)) {
		t.Error("artifact manifest lost existing metadata")
	}
}
