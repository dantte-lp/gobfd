package cirunner

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

const (
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
	return packageBuildWithInfo(ctx, options, buildinfo.Read)
}

func packageBuildWithInfo(ctx context.Context, options PackageOptions,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) error {
	if options.Runner == nil {
		return fmt.Errorf("package build command runner is required: %w", errInvalidConfig)
	}
	environment := append(
		withoutEnvironmentKeys(options.Environment, "GORELEASER_CURRENT_TAG", "GITHUB_REF_NAME"),
		"GORELEASER_CURRENT_TAG="+packageTestTag, "GITHUB_REF_NAME="+packageTestTag,
	)
	if err := verifyPackageUPX(ctx, options, environment); err != nil {
		return fmt.Errorf("verify package UPX prerequisite: %w", err)
	}
	options.Environment = environment
	return packageBuild(ctx, options, readBuildInfo)
}

func verifyPackageUPX(ctx context.Context, options PackageOptions, environment []string) error {
	path := ""
	for _, entry := range environment {
		if value, found := strings.CutPrefix(entry, "PATH="); found {
			path = value
		}
	}
	for _, directory := range filepath.SplitList(path) {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("UPX search directory must be absolute: %w", errInvalidConfig)
		}
		root, err := os.OpenRoot(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("open UPX search directory: %w", err)
		}
		_, err = root.Lstat("upx")
		if errors.Is(err, os.ErrNotExist) {
			if closeErr := root.Close(); closeErr != nil {
				return fmt.Errorf("close UPX search directory: %w", closeErr)
			}
			continue
		}
		if err != nil {
			return errors.Join(err, root.Close())
		}
		return verifyUPXBinVersion(ctx, options.Runner, root, options.Root,
			withoutEnvironmentKeys(environment, "GH_TOKEN", "GITHUB_TOKEN"), defaultUPXAssetContract())
	}
	return fmt.Errorf("pinned UPX is missing from PATH; run cictl release-upx first: %w", errInvalidConfig)
}

// PackageLifecycle installs the dist/ DEB into a systemd-enabled Debian 13
// container through the Podman-backed lifecycle test.
func PackageLifecycle(ctx context.Context, options PackageOptions) error {
	if options.Runner == nil {
		return fmt.Errorf("package lifecycle command runner is required: %w", errInvalidConfig)
	}
	environment := append(
		withoutEnvironmentKeys(options.Environment,
			"GOBFD_PACKAGE_DIST", "GOBFD_REQUIRE_PODMAN", "GOBFD_PACKAGE_ARTIFACT_DIR"),
		"GOBFD_PACKAGE_DIST="+filepath.Join(options.Root, "dist"), "GOBFD_REQUIRE_PODMAN=1",
		"GOBFD_PACKAGE_ARTIFACT_DIR="+filepath.Join(options.Root, "reports", "e2e", "package-lifecycle"),
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
