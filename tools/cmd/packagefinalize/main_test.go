package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	assertRPMArchiveSize(t, data)
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

func assertRPMArchiveSize(t *testing.T, data []byte) {
	t.Helper()
	const leadSize, headerSize, indexSize, alignment, payloadSizeTag = 96, 16, 16, 8, 1007
	signature := data[leadSize:]
	entries := int(binary.BigEndian.Uint32(signature[8:12]))
	storeSize := int(binary.BigEndian.Uint32(signature[12:headerSize]))
	store := signature[headerSize+entries*indexSize:]
	var declared uint32
	found := false
	for index := range entries {
		entry := signature[headerSize+index*indexSize:][:indexSize]
		if binary.BigEndian.Uint32(entry) == payloadSizeTag {
			offset := binary.BigEndian.Uint32(entry[8:12])
			declared = binary.BigEndian.Uint32(store[offset:])
			found = true
		}
	}
	if !found {
		t.Fatal("RPM signature has no uncompressed payload size")
	}
	length := headerSize + entries*indexSize + storeSize
	headerOffset := leadSize + (length+alignment-1)/alignment*alignment
	header := data[headerOffset:]
	payloadOffset := headerOffset + headerSize + int(binary.BigEndian.Uint32(header[8:12]))*indexSize +
		int(binary.BigEndian.Uint32(header[12:headerSize]))
	payload, err := gzip.NewReader(bytes.NewReader(data[payloadOffset:]))
	if err != nil {
		t.Fatal(err)
	}
	actual, readErr := io.Copy(io.Discard, payload)
	if err := errors.Join(readErr, payload.Close()); err != nil {
		t.Fatal(err)
	}
	if actual != int64(declared) {
		t.Fatalf("RPM uncompressed archive size = %d, declared %d", actual, declared)
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
