package cirunner

import (
	"context"
	"fmt"
	"path/filepath"
)

const (
	// goreleaserModule is the GoReleaser version pinned by the release workflow.
	goreleaserModule  = "github.com/goreleaser/goreleaser/v2@v2.18.2"
	goreleaserCommand = "release"
	// packageTestTag versions locally built packages; it never names a release.
	packageTestTag          = "v0.0.0"
	packageLifecycleTag     = "package_lifecycle_testcontainers"
	packageLifecyclePackage = "./test/package-lifecycle/"
)

// PackageOptions configures the package build and lifecycle commands.
type PackageOptions struct {
	Root        string
	Environment []string
	Runner      SpecRunner
}

// PackageBuild builds the DEB and RPM through the release path: GoReleaser
// without publishing, signing, SBOM or image stages, then the native RPM
// lifecycle finalization. The packages land in dist/ as version 0.0.0.
func PackageBuild(ctx context.Context, options PackageOptions) error {
	if options.Runner == nil {
		return fmt.Errorf("package build command runner is required: %w", errInvalidConfig)
	}
	environment := append(
		withoutEnvironmentKeys(options.Environment, "GORELEASER_CURRENT_TAG", "GITHUB_REF_NAME"),
		"GORELEASER_CURRENT_TAG="+packageTestTag, "GITHUB_REF_NAME="+packageTestTag,
	)
	steps := []struct {
		name string
		args []string
	}{
		{"GoReleaser packages", []string{
			"run", goreleaserModule, goreleaserCommand, "--clean", "--parallelism=1",
			"--skip=publish,announce,validate,sign,sbom,docker",
		}},
		{"RPM lifecycle finalization", []string{"-C", "tools", "run", "./cmd/packagefinalize"}},
	}
	for _, step := range steps {
		if err := options.Runner.RunCommand(ctx, CommandSpec{
			Name: "go", Args: step.args, Dir: options.Root, Env: environment,
		}); err != nil {
			return fmt.Errorf("build %s: %w", step.name, err)
		}
	}
	return nil
}

// PackageLifecycle installs the dist/ packages into systemd-enabled Debian 13
// and Oracle Linux 10 containers through the Podman-backed lifecycle test.
func PackageLifecycle(ctx context.Context, options PackageOptions) error {
	if options.Runner == nil {
		return fmt.Errorf("package lifecycle command runner is required: %w", errInvalidConfig)
	}
	environment := append(
		withoutEnvironmentKeys(options.Environment, "GOBFD_PACKAGE_DIST", "GOBFD_REQUIRE_PODMAN"),
		"GOBFD_PACKAGE_DIST="+filepath.Join(options.Root, "dist"), "GOBFD_REQUIRE_PODMAN=1",
	)
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go",
		Args: []string{
			"test", "-tags", packageLifecycleTag, "-count=1", "-v", "-timeout", "40m", packageLifecyclePackage,
		},
		Dir: options.Root,
		Env: environment,
	}); err != nil {
		return fmt.Errorf("run package lifecycle test: %w", err)
	}
	return nil
}
