package cirunner

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestPackageBuildUsesReleasePath(t *testing.T) {
	t.Parallel()

	runner := &recordingSpecRunner{}
	if err := PackageBuild(t.Context(), PackageOptions{
		Root: "/repo", Environment: []string{"GITHUB_REF_NAME=v9.9.9", "PATH=/bin"}, Runner: runner,
	}); err != nil {
		t.Fatalf("PackageBuild() error = %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("PackageBuild ran %d commands, want GoReleaser and RPM finalization", len(runner.calls))
	}
	release, finalize := runner.calls[0], runner.calls[1]
	if release.name != "go" || !slices.Contains(release.args, goreleaserModule) ||
		!slices.Contains(release.args, "--skip=publish,announce,validate,sign,sbom,docker") {
		t.Errorf("release command = %s %v", release.name, release.args)
	}
	if !slices.Equal(finalize.args, []string{"-C", "tools", "run", "./cmd/packagefinalize"}) {
		t.Errorf("finalize command = %v", finalize.args)
	}
	for _, spec := range runner.calls {
		if spec.dir != "/repo" {
			t.Errorf("%v runs in %q, want /repo", spec.args, spec.dir)
		}
		for _, want := range []string{"GORELEASER_CURRENT_TAG=v0.0.0", "GITHUB_REF_NAME=v0.0.0", "PATH=/bin"} {
			if !slices.Contains(spec.env, want) {
				t.Errorf("%v environment lacks %q: %v", spec.args, want, spec.env)
			}
		}
		if slices.Contains(spec.env, "GITHUB_REF_NAME=v9.9.9") {
			t.Errorf("%v keeps the caller release ref", spec.args)
		}
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
