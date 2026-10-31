package cirunner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

func fixtureManifestHashes(mod, sum []byte) *goreleaserManifests {
	return &goreleaserManifests{ModSHA256: goreleaserSHA256(mod), SumSHA256: goreleaserSHA256(sum)}
}

func (fixture *goreleaserFixture) configureManifestFault(fault string) {
	switch fault {
	case "unqualified manifests":
		fixture.profile.Manifests = nil
	case "wrong manifest":
		fixture.profile.Manifests.ModSHA256 = strings.Repeat("0", 64)
	case "wrong source manifest":
		fixture.profile.SourceManifests.ModSHA256 = strings.Repeat("0", 64)
	case "wrong refmt manifest":
		fixture.profile.Refmt.Manifests.ModSHA256 = strings.Repeat("0", 64)
	case "wrong refmt source":
		fixture.profile.Refmt.Sum = fixture.profile.Sum
	case "unqualified source manifests":
		fixture.profile.SourceManifests = nil
	case "unqualified refmt manifests":
		fixture.profile.Refmt.Manifests = nil
	case "wrong patch":
		fixture.profile.Patch.BeforeSHA256 = strings.Repeat("0", 64)
	}
}

func TestPreparedGoReleaserWorkspaceBindsBothComponentsAndConsumer(t *testing.T) {
	t.Parallel()
	fixture := newGoReleaserFixture(t)
	workspace, err := PrepareGoReleaserWorkspace(t.Context(), GoReleaserOptions{
		Root: fixture.root, InstallDir: fixture.installDir, Runner: fixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	directory, err := workspace.Directory()
	if err != nil || directory != filepath.Join(fixture.installDir, "stage") {
		t.Fatalf("prepared publisher cwd = %q: %v", directory, err)
	}
	for name, expected := range map[string]int{
		goreleaserSourceDirectoryName: fixture.profile.Files, refmtDirectoryName: fixture.profile.Refmt.Files,
	} {
		source := workspace.source
		if name == refmtDirectoryName {
			source = workspace.refmt
		}
		if len(source.contents) != expected {
			t.Fatalf("prepared source files %s = %d", name, len(source.contents))
		}
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatalf("second workspace close: %v", err)
	}
	if _, err := workspace.Directory(); err == nil {
		t.Fatal("closed workspace returned a working directory")
	}
	if _, err := os.Lstat(fixture.installDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared source/consumer roots retained after close: %v", err)
	}
}

func TestPreparedPublisherWorkspaceRejectsChangedConsumerAndPreservesForeignRefmt(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"consumer", "foreign Refmt"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture := newGoReleaserFixture(t)
			workspace, err := PrepareGoReleaserWorkspace(t.Context(), GoReleaserOptions{
				Root: fixture.root, InstallDir: fixture.installDir, Runner: fixture,
			})
			if err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(fixture.installDir, "stage", goModFilename)
			if fault == "foreign Refmt" {
				filename = filepath.Join(fixture.installDir, refmtDirectoryName)
				if err := os.Rename(filename, filename+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filename, 0o700); err != nil {
					t.Fatal(err)
				}
				filename = filepath.Join(filename, "foreign")
			}
			if err := os.WriteFile(filename, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := workspace.Directory(); err == nil {
				t.Fatal("changed prepared workspace accepted")
			}
			closeErr := workspace.Close()
			if fault == "consumer" {
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			} else {
				if closeErr == nil {
					t.Fatal("foreign Refmt cleanup reported success")
				}
				data, readErr := os.ReadFile(filename)
				if readErr != nil || string(data) != "foreign" {
					t.Fatalf("foreign Refmt changed: %q: %v", data, readErr)
				}
			}
		})
	}
}

func (fixture *goreleaserFixture) prepareRefmtAndConsumer() {
	fixture.t.Helper()
	before, after := `"github.com/go-yaml/yaml"`, `"go.yaml.in/yaml/v2"`
	source := []byte("package main\nimport " + before + "\n")
	fixture.refmtContents = map[string][]byte{
		goModFilename: []byte("module " + toolprofile.RefmtModule + "\n\ngo 1.27.3\n"),
		"go.sum":      []byte("original refmt fixture\n"), "cmd/refmt/helper_yaml.go": source,
	}
	profile := &refmtSourceProfile{
		Module: toolprofile.RefmtModule, Version: "v0.90.0",
		SourceCommit: "35c3f9f2719f09f226ddd74e6e4595c309b56165", Files: len(fixture.refmtContents),
		Patch: goreleaserSourcePatch{
			Path: "cmd/refmt/helper_yaml.go", Before: before, After: after,
			BeforeSHA256: goreleaserSHA256(source),
			AfterSHA256:  goreleaserSHA256([]byte(strings.Replace(string(source), before, after, 1))),
		},
	}
	for _, data := range fixture.refmtContents {
		profile.Bytes += int64(len(data))
	}
	profile.Sum = goReleaserContentSum(fixture.refmtContents, profile.Module+"@"+profile.Version+"/")
	profile.GoModSum = goReleaserContentSum(map[string][]byte{goModFilename: fixture.refmtContents[goModFilename]}, "")
	mod := append([]byte{}, fixture.refmtContents[goModFilename]...)
	mod = append(mod, []byte("// native migrated fixture\n")...)
	sum := []byte("native refmt fixture\n")
	profile.Manifests = fixtureManifestHashes(mod, sum)
	fixture.profile.Refmt = profile
	fixture.refmtZIP = writePublisherFixtureZIP(fixture.t, profile.sourceProfile(), fixture.refmtContents, nil)
	consumer := []byte("module " + toolprofile.GoReleaserConsumer + "\n\ngo 1.27.3\n")
	consumerSum := []byte("consumer fixture\n")
	fixture.profile.Manifests = fixtureManifestHashes(consumer, consumerSum)
	for name, data := range map[string][]byte{
		"refmt.mod": mod, "refmt.sum": sum, "qualified.mod": consumer, "qualified.sum": consumerSum,
	} {
		if err := os.WriteFile(filepath.Join(fixture.root, "tools", "goreleaser", name), data, 0o600); err != nil {
			fixture.t.Fatal(err)
		}
	}
}

func (fixture *goreleaserFixture) manifest() publisherConsumerManifest {
	manifest := publisherConsumerManifest{
		Module: publisherModuleVersion{Path: toolprofile.GoReleaserConsumer},
		Tool:   []publisherModuleVersion{{Path: fixture.profile.Module}},
		Require: []publisherModuleVersion{
			{Path: fixture.profile.Module, Version: fixture.profile.Version},
			{Path: toolprofile.RefmtModule, Version: fixture.profile.Refmt.Version},
		},
		Replace: []publisherConsumerReplacement{
			{
				Old: publisherModuleVersion{Path: fixture.profile.Module, Version: fixture.profile.Version},
				New: publisherModuleVersion{Path: "../source"},
			},
			{
				Old: publisherModuleVersion{Path: toolprofile.RefmtModule, Version: fixture.profile.Refmt.Version},
				New: publisherModuleVersion{Path: "../refmt-local"},
			},
		},
	}
	for _, module := range []string{
		"github.com/cilium/ebpf", "github.com/google/cel-go",
		"github.com/labstack/echo/v4", "github.com/prometheus/prometheus", "github.com/yuin/goldmark",
		"go.etcd.io/etcd/client/pkg/v3", "go.etcd.io/etcd/server/v3", "go.etcd.io/etcd/v3",
	} {
		manifest.Replace = append(manifest.Replace, publisherConsumerReplacement{
			Old: publisherModuleVersion{Path: module}, New: publisherModuleVersion{Path: module, Version: "v1.0.0"},
		})
	}
	return manifest
}

func (fixture *goreleaserFixture) writeManifestJSON(spec CommandSpec) error {
	fixture.t.Helper()
	manifest := fixture.manifest()
	switch filepath.Base(spec.Dir) {
	case goreleaserSourceDirectoryName:
		manifest.Module.Path = fixture.profile.Module
		if fixture.fault == "source overlay role" {
			manifest.Module.Path = toolprofile.GoReleaserConsumer
		}
	case refmtDirectoryName:
		manifest.Module.Path = toolprofile.RefmtModule
		if fixture.fault == "refmt overlay role" {
			manifest.Module.Path = toolprofile.GoReleaserConsumer
		}
	case "stage":
		switch fixture.fault {
		case "source manifest role":
			manifest.Module.Path = fixture.profile.Module
		case "wrong consumer replacement":
			manifest.Replace[0].New.Path = "../foreign"
		case "consumer manifest drift":
			if err := os.WriteFile(filepath.Join(spec.Dir, goModFilename), []byte("changed"), 0o600); err != nil {
				return err
			}
		}
	default:
		fixture.t.Fatalf("manifest parser cwd escaped prepared source layout: %s", spec.Dir)
	}
	return json.NewEncoder(spec.Stdout).Encode(manifest)
}

func TestPublisherConsumerManifestRejectsWrongRolesAndBindings(t *testing.T) {
	t.Parallel()
	fixture := newGoReleaserFixture(t)
	for _, fault := range []string{"module", "tool", "require", "local version", "foreign local", "foreign security"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			manifest := fixture.manifest()
			switch fault {
			case "module":
				manifest.Module.Path = fixture.profile.Module
			case "tool":
				manifest.Tool = nil
			case "require":
				manifest.Require[0].Version = "v2.18.0"
			case "local version":
				manifest.Replace[0].Old.Version = ""
			case "foreign local":
				manifest.Replace[1].New.Path = "/foreign/refmt"
			case "foreign security":
				manifest.Replace[2].New.Path = "example.invalid/alias"
			}
			if err := manifest.validate(fixture.profile); err == nil {
				t.Fatal("foreign consumer role/binding accepted")
			}
		})
	}
}
