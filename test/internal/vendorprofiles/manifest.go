// Package vendorprofiles owns the shared Containerlab profile and execution policy.
package vendorprofiles

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// RetainedUnavailable preserves a NOS profile without permission to execute it.
	RetainedUnavailable = "retained-unavailable"
	// OwnedReceiptRequired permits only the owned FRR lifecycle.
	OwnedReceiptRequired = "owned-receipt-required"

	maxManifestSize    = 64 << 10
	qualificationIssue = "gobfd-qj0.8.2.8.5.2.7.4"
	frrIssue           = "gobfd-qj0.8.2.8.5.2.7.3"
)

// ErrUnavailable identifies an unqualified NOS execution request.
var ErrUnavailable = errors.New("vendor NOS execution is unavailable")

var errManifest = errors.New("invalid vendor profile manifest")

// Manifest is the tracked source of profile metadata and execution policy.
type Manifest struct {
	Runtime  Runtime   `json:"runtime"`
	Profiles []Profile `json:"profiles"`
}

// Runtime declares the supported test engine and public CI boundary.
type Runtime struct {
	ContainerlabRuntime string `json:"containerlab_runtime"`
	PodmanRequired      bool   `json:"podman_required"`
	PublicCIDefault     string `json:"public_ci_default"`
}

// Profile keeps historical role and image metadata separate from execution policy.
type Profile struct {
	ID                    string   `json:"id"`
	Vendor                string   `json:"vendor"`
	Platform              string   `json:"platform"`
	ProfileClass          string   `json:"profile_class"`
	TopologyNode          string   `json:"topology_node"`
	ConfigPaths           []string `json:"config_paths"`
	Images                []string `json:"images"`
	Build                 *Build   `json:"build,omitempty"`
	SkipPolicy            string   `json:"skip_policy"`
	Scenario              string   `json:"scenario"`
	Standards             []string `json:"standards"`
	Evidence              []string `json:"evidence"`
	ExecutionPolicy       string   `json:"execution_policy"`
	QualificationTracking string   `json:"qualification_tracking"`
}

// Build identifies the existing first-party FRR recipe.
type Build struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
}

// Load validates the exact checkout manifest without ancestor or cache fallback.
func Load(root string) (Manifest, error) {
	path := filepath.Join(root, "test", "e2e", "vendor", "profiles.json")
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open vendor profiles: %w", err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		return Manifest{}, fmt.Errorf("stat vendor profiles: %w", errors.Join(statErr, file.Close()))
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxManifestSize {
		return Manifest{}, errors.Join(errManifest, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxManifestSize+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return Manifest{}, fmt.Errorf("read vendor profiles: %w", err)
	}
	if len(data) > maxManifestSize || !jsontext.Value(data).IsValid() {
		return Manifest{}, fmt.Errorf("require one strict vendor profile JSON document: %w", errManifest)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode vendor profiles: %w", err)
	}
	if err := validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validate(manifest Manifest) error {
	known := map[string]struct{ node, class string }{
		"arista-ceos":   {node: "arista", class: "primary"},
		"nokia-srlinux": {node: "nokia", class: "primary"},
		"cisco-xrd":     {node: "cisco", class: "deferred"},
		"sonic-vs":      {node: "sonic", class: "primary"},
		"vyos":          {node: "vyos", class: "primary"},
		"frr":           {node: "frr", class: "baseline"},
	}
	if len(manifest.Profiles) != len(known) || manifest.Runtime.ContainerlabRuntime != "podman" ||
		!manifest.Runtime.PodmanRequired || manifest.Runtime.PublicCIDefault != "skip-topology" {
		return fmt.Errorf("require all vendor profiles and the Podman runtime contract: %w", errManifest)
	}
	for _, profile := range manifest.Profiles {
		want, ok := known[profile.ID]
		if !ok || profile.TopologyNode != want.node || profile.ProfileClass != want.class || !validPolicy(profile) {
			return fmt.Errorf("validate vendor profile %q execution disposition: %w", profile.ID, errManifest)
		}
		delete(known, profile.ID)
	}
	return nil
}

func validPolicy(profile Profile) bool {
	if profile.ID == "frr" {
		return profile.ExecutionPolicy == OwnedReceiptRequired && profile.QualificationTracking == frrIssue &&
			len(profile.Images) == 0 && profile.Build != nil &&
			profile.Build.Context == "test/interop/frr" && profile.Build.Dockerfile == "Containerfile"
	}
	return profile.ExecutionPolicy == RetainedUnavailable && profile.QualificationTracking == qualificationIssue &&
		len(profile.Images) != 0 && profile.Build == nil
}

// AllowsNode excludes cached NOS images and unknown nodes from execution.
func (manifest Manifest) AllowsNode(node string) bool {
	for _, profile := range manifest.Profiles {
		if profile.TopologyNode == node {
			return profile.ID == "frr" && profile.ExecutionPolicy == OwnedReceiptRequired
		}
	}
	return false
}
