//go:build interop_testcontainers

package interop_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func validateFourPeerImageMetadata(metadata podmanapi.ImageMetadata, revision string) (map[string]string, error) {
	labels, err := containertest.ValidateImageProvenance(metadata, revision)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errTimerEvidence, err)
	}
	return labels, nil
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
		"org.opencontainers.image.base.digest": "sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad",
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
