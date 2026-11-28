package clabbootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/vendorprofiles"
)

func TestBootstrapRejectsMissingVendorPolicyBeforeCommands(t *testing.T) {
	t.Parallel()

	options := DefaultOptions(bootstrapTestRoot(t))
	if err := os.Remove(filepath.Join(options.ProjectRoot, "test", "e2e", "vendor", "profiles.json")); err != nil {
		t.Fatal(err)
	}
	options.DryRun = true
	options.Deploy = true
	options.Jobs = 1
	runner := &recordingRunner{}
	err := Run(t.Context(), options, runner)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing vendor execution policy error = %v, want os.ErrNotExist", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("missing vendor policy allowed %d external commands", len(runner.commands))
	}
}

func TestBootstrapRejectsAmbiguousVendorPolicyBeforeCommands(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{
		"duplicate-member", "invalid-utf8", "trailing-document", "unknown-field",
		"duplicate-profile", "unknown-policy", "oversized",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := bootstrapTestRoot(t)
			path := filepath.Join(root, "test", "e2e", "vendor", "profiles.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate-member":
				data = append([]byte(`{"runtime":{},`), data[1:]...)
			case "invalid-utf8":
				data = []byte(strings.Replace(string(data), "Arista", string([]byte{0xff}), 1))
			case "trailing-document":
				data = append(data, []byte("\n{}")...)
			case "unknown-field":
				data = append([]byte(`{"allow_unqualified":true,`), data[1:]...)
			case "duplicate-profile", "unknown-policy":
				var manifest vendorprofiles.Manifest
				if decodeErr := json.Unmarshal(data, &manifest); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if kind == "duplicate-profile" {
					manifest.Profiles[0] = manifest.Profiles[1]
				} else {
					manifest.Profiles[0].ExecutionPolicy = "approved"
				}
				data, err = json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
			case "oversized":
				data = append(data, []byte(strings.Repeat(" ", 1<<20))...)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			options := DefaultOptions(root)
			options.DryRun, options.Jobs = true, 1
			runner := &recordingRunner{}
			if err := Run(t.Context(), options, runner); err == nil || len(runner.commands) != 0 {
				t.Fatalf("invalid policy %s: error=%v, commands=%d", kind, err, len(runner.commands))
			}
		})
	}
}

func TestUnavailableVendorPreparationRejectsBeforeCommands(t *testing.T) {
	t.Parallel()
	for _, vendor := range []string{"arista", "cisco", "vyos"} {
		t.Run(vendor, func(t *testing.T) {
			t.Parallel()
			options := DefaultOptions(bootstrapTestRoot(t))
			options.DryRun, options.Jobs = true, 1
			switch vendor {
			case "arista":
				options.Archives.Arista = "operator-ceos.tar"
			case "cisco":
				options.Archives.Cisco = "operator-xrd.tar"
			case "vyos":
				options.VyOSISO = "operator-vyos.iso"
			}
			runner := &recordingRunner{}
			err := Run(t.Context(), options, runner)
			if !errors.Is(err, vendorprofiles.ErrUnavailable) || len(runner.commands) != 0 {
				t.Fatalf("unqualified %s preparation: error=%v, commands=%d", vendor, err, len(runner.commands))
			}
		})
	}
}

func TestCachedVendorImagesCannotEnterExecutionProfiles(t *testing.T) {
	t.Parallel()
	options := DefaultOptions(bootstrapTestRoot(t))
	runner := &recordingRunner{images: make(map[string]bool)}
	for _, candidate := range vendorCandidates(options, testFRRImageID) {
		for _, image := range candidate.images {
			runner.images[image] = true
		}
	}
	profiles, err := availableProfiles(t.Context(), options, runner, testFRRImageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].name != "frr" || profiles[0].image != testFRRImageID {
		t.Fatalf("cached NOS entered executable profiles: %+v", profiles)
	}
	for _, command := range runner.commands {
		if !slices.Contains(command.Arguments, testFRRImageID) {
			t.Fatalf("retained NOS triggered image selection: %v", command.Arguments)
		}
	}
}
