//go:build interop_testcontainers

package interop_test

import (
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func validateFourPeerImageMetadata(metadata podmanapi.ImageMetadata, revision string) (map[string]string, error) {
	if !fourPeerImageDigest(metadata.ID) {
		return nil, fmt.Errorf("image identity is not an immutable SHA-256 digest: %w", errTimerEvidence)
	}
	labels := make(map[string]string, 9)
	for _, name := range []string{
		"source", "revision", "created", "version", "licenses", "title", "description", "base.name", "base.digest",
	} {
		key := "org.opencontainers.image." + name
		value := metadata.Labels[key]
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("image annotation %s is empty: %w", key, errTimerEvidence)
		}
		labels[key] = value
	}
	if labels["org.opencontainers.image.source"] != "https://github.com/dantte-lp/gobfd" ||
		labels["org.opencontainers.image.revision"] != revision {
		return nil, fmt.Errorf("image source does not match the candidate: %w", errTimerEvidence)
	}
	if _, err := time.Parse(time.RFC3339, labels["org.opencontainers.image.created"]); err != nil {
		return nil, fmt.Errorf("parse image creation time: %w", err)
	}
	if labels["org.opencontainers.image.base.name"] != "docker.io/library/debian:trixie-slim" ||
		!fourPeerImageDigest(labels["org.opencontainers.image.base.digest"]) {
		return nil, fmt.Errorf("image base is not pinned Debian trixie: %w", errTimerEvidence)
	}
	return labels, nil
}

func fourPeerImageDigest(value string) bool {
	digest, found := strings.CutPrefix(value, "sha256:")
	if !found || len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func TestFourPeerImageProvenance(t *testing.T) {
	t.Parallel()
	revision := strings.Repeat("a", 40)
	labels := map[string]string{
		"org.opencontainers.image.source":      "https://github.com/dantte-lp/gobfd",
		"org.opencontainers.image.revision":    revision,
		"org.opencontainers.image.created":     "2026-10-24T00:00:00Z",
		"org.opencontainers.image.version":     "fixture",
		"org.opencontainers.image.licenses":    "Apache-2.0",
		"org.opencontainers.image.title":       "fixture",
		"org.opencontainers.image.description": "test fixture",
		"org.opencontainers.image.base.name":   "docker.io/library/debian:trixie-slim",
		"org.opencontainers.image.base.digest": "sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f",
		"private-value":                        "do-not-retain",
	}
	metadata := podmanapi.ImageMetadata{
		ID:     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Labels: labels,
	}
	retained, err := validateFourPeerImageMetadata(metadata, revision)
	if err != nil || len(retained) != 9 || retained["private-value"] != "" {
		t.Fatalf("image provenance retained wrong metadata: %v %v", retained, err)
	}
	for _, test := range []struct {
		name   string
		change func(*podmanapi.ImageMetadata)
	}{
		{"tag as identity", func(m *podmanapi.ImageMetadata) { m.ID = "localhost/fixture:tag" }},
		{"wrong source", func(m *podmanapi.ImageMetadata) { m.Labels["org.opencontainers.image.source"] = "other" }},
		{"wrong revision", func(m *podmanapi.ImageMetadata) { m.Labels["org.opencontainers.image.revision"] = "other" }},
		{"missing annotation", func(m *podmanapi.ImageMetadata) { delete(m.Labels, "org.opencontainers.image.version") }},
		{"invalid created", func(m *podmanapi.ImageMetadata) { m.Labels["org.opencontainers.image.created"] = "bad" }},
		{"invalid base", func(m *podmanapi.ImageMetadata) { m.Labels["org.opencontainers.image.base.digest"] = "latest" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := metadata
			changed.Labels = maps.Clone(labels)
			test.change(&changed)
			if _, err := validateFourPeerImageMetadata(changed, revision); err == nil {
				t.Fatal("invalid provenance accepted")
			}
		})
	}
}
