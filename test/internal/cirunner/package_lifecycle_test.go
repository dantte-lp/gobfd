package cirunner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPackageBuildRejectsUnavailableUPX(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing", "wrong version"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bin := t.TempDir()
			if name == "wrong version" {
				if err := os.WriteFile(filepath.Join(bin, "upx"), []byte("fixture"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
				if spec.Name == "upx" {
					if _, err := io.WriteString(spec.Stdout, "upx 0.0.0\n"); err != nil {
						t.Fatal(err)
					}
				}
			}}
			err := PackageBuild(t.Context(), PackageOptions{
				Root: "/repo", Environment: []string{"PATH=" + bin}, Runner: runner,
			})
			if err == nil {
				t.Error("PackageBuild accepted an unavailable pinned UPX prerequisite")
			}
			for _, call := range runner.calls {
				if call.name != "upx" {
					t.Errorf("PackageBuild ran %s before accepting the UPX prerequisite", call.name)
				}
			}
		})
	}
}

func newPackageBuildFixture(t *testing.T) (*goreleaserFixture, *recordingSpecRunner, string) {
	t.Helper()
	fixture := newGoReleaserFixture(t)
	installation, err := installGoReleaser(t.Context(), GoReleaserOptions{
		Root: fixture.root, InstallDir: fixture.installDir, Runner: fixture,
	}, fixture.profile, fixture.buildInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := installation.Close(); err != nil {
			t.Errorf("close package publisher fixture: %v", err)
		}
	})
	upxBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(upxBin, "upx"), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		version := ""
		switch spec.Name {
		case "upx":
			if spec.Executable == nil {
				t.Fatal("UPX probe did not bind the opened executable")
			}
			version = "upx " + defaultUPXAssetContract().version + "\n"
		case goreleaserName:
			if spec.Executable == nil {
				t.Fatal("publisher invocation did not bind the opened executable")
			}
			if slices.Equal(spec.Args, []string{"--version"}) {
				version = fixture.version()
			}
		}
		if version != "" {
			if _, err := io.WriteString(spec.Stdout, version); err != nil {
				t.Fatal(err)
			}
		}
	}}
	path := upxBin + string(filepath.ListSeparator) + filepath.Join(fixture.installDir, "bin")
	return fixture, runner, path
}

func TestPackageBuildUsesReleasePath(t *testing.T) {
	t.Parallel()
	fixture, runner, path := newPackageBuildFixture(t)
	if err := packageBuildWithInfo(t.Context(), PackageOptions{
		Root: fixture.root, Environment: []string{"GITHUB_REF_NAME=v9.9.9", "PATH=" + path}, Runner: runner,
	}, fixture.buildInfo); err != nil {
		t.Fatalf("PackageBuild() error = %v", err)
	}
	if len(runner.calls) != 4 || runner.calls[0].name != "upx" ||
		!slices.Equal(runner.calls[1].args, []string{"--version"}) {
		t.Fatalf("PackageBuild commands = %v, want UPX, bound publisher probe/release and finalization", runner.calls)
	}
	release, finalize := runner.calls[2], runner.calls[3]
	if release.name != goreleaserName ||
		!slices.Equal(release.args, []string{
			"release", "--clean", "--parallelism=1",
			"--skip=publish,announce,validate,sign,sbom,docker",
		}) {
		t.Errorf("release command = %s %v", release.name, release.args)
	}
	if !slices.Equal(finalize.args, []string{"-C", "tools", "run", "./cmd/packagefinalize"}) {
		t.Errorf("finalize command = %v", finalize.args)
	}
	for _, spec := range []specInvocation{release, finalize} {
		if spec.dir != fixture.root {
			t.Errorf("%v runs in %q, want fixture checkout", spec.args, spec.dir)
		}
		for _, want := range []string{"GORELEASER_CURRENT_TAG=v0.0.0", "GITHUB_REF_NAME=v0.0.0", "PATH=" + path} {
			if !slices.Contains(spec.env, want) {
				t.Errorf("%v environment lacks %q: %v", spec.args, want, spec.env)
			}
		}
		if slices.Contains(spec.env, "GITHUB_REF_NAME=v9.9.9") {
			t.Errorf("%v keeps caller release ref", spec.args)
		}
	}
}

func TestPackageBuildRejectsMissingOrUnverifiedPublisher(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing", "missing profile", "wrong profile", "wrong executable"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture, runner, path := newPackageBuildFixture(t)
			switch fault {
			case "missing":
				path = strings.Split(path, string(filepath.ListSeparator))[0]
			case "missing profile":
				name := filepath.Join(fixture.root, "tools", "goreleaser", "source.json")
				if err := os.Rename(name, name+"-original"); err != nil {
					t.Fatal(err)
				}
			case "wrong profile":
				fixture.profile.Manifests.ModSHA256 = strings.Repeat("0", 64)
				fixture.writeProfile()
			case "wrong executable":
				fixture.fault = "forged main version"
			}
			if err := packageBuildWithInfo(t.Context(), PackageOptions{
				Root: fixture.root, Environment: []string{"PATH=" + path}, Runner: runner,
			}, fixture.buildInfo); err == nil {
				t.Fatal("unverified publisher accepted")
			}
			if len(runner.calls) != 1 || runner.calls[0].name != "upx" {
				t.Fatalf("publisher failure reached release/finalization: %v", runner.calls)
			}
		})
	}
}

func TestPackageLifecycleRunsTaggedTest(t *testing.T) {
	t.Parallel()

	runner := &recordingSpecRunner{}
	if err := PackageLifecycle(t.Context(), PackageOptions{Root: "/repo", Runner: runner}); err != nil {
		t.Fatalf("PackageLifecycle() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("PackageLifecycle ran %d commands, want one go test", len(runner.calls))
	}
	spec := runner.calls[0]
	if spec.name != "go" || spec.args[0] != "test" || !slices.Contains(spec.args, packageLifecycleTag) ||
		spec.args[len(spec.args)-1] != packageLifecyclePackage {
		t.Errorf("lifecycle command = %s %v", spec.name, spec.args)
	}
	for _, want := range []string{
		"GOBFD_PACKAGE_DIST=/repo/dist", "GOBFD_REQUIRE_PODMAN=1",
		"GOBFD_PACKAGE_ARTIFACT_DIR=/repo/reports/e2e/package-lifecycle",
	} {
		if !slices.Contains(spec.env, want) {
			t.Errorf("lifecycle environment lacks %q: %v", want, spec.env)
		}
	}
}

func TestPackageCommandsRejectMissingRunner(t *testing.T) {
	t.Parallel()

	for name, run := range map[string]func(context.Context, PackageOptions) error{
		"build": PackageBuild, "lifecycle": PackageLifecycle,
	} {
		if err := run(t.Context(), PackageOptions{Root: "/repo"}); !errors.Is(err, errInvalidConfig) {
			t.Errorf("%s without runner error = %v, want invalid config", name, err)
		}
	}
}
