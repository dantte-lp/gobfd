package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

func openPackagePublisher(ctx context.Context, options PackageOptions,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) (_ *sourceInstallation, returnErr error) {
	profile, err := loadGoReleaserProfile(options.Root)
	if err != nil {
		return nil, err
	}
	if profileErr := profile.requireManifests(); profileErr != nil {
		return nil, profileErr
	}
	directory, err := findPackagePublisher(options.Environment)
	if err != nil {
		return nil, err
	}
	installation, err := openPublishedPublisher(directory)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, installation.Close())
		}
	}()
	if err := verifyGoReleaserReceipt(installation, profile); err != nil {
		return nil, err
	}
	if err := verifyGoReleaserBinary(ctx, options.Runner, installation, profile, readBuildInfo); err != nil {
		return nil, err
	}
	return installation, nil
}

func findPackagePublisher(environment []string) (string, error) {
	for _, directory := range filepath.SplitList(environmentValue(environment, "PATH")) {
		if !filepath.IsAbs(directory) {
			return "", fmt.Errorf("publisher search directory must be absolute: %w", errInvalidConfig)
		}
		info, err := os.Lstat(filepath.Join(directory, goreleaserName))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || filepath.Base(directory) != "bin" {
			return "", fmt.Errorf("publisher prerequisite is not a source installation: %w", errors.Join(err, errInvalidConfig))
		}
		return filepath.Dir(directory), nil
	}
	return "", fmt.Errorf("source-built publisher is missing from PATH; install release-source-tools first: %w",
		errInvalidConfig)
}

func environmentValue(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if candidate, found := strings.CutPrefix(entry, name+"="); found {
			value = candidate
		}
	}
	return value
}

func openPublishedPublisher(directory string) (_ *sourceInstallation, returnErr error) {
	if err := validateSourceInstallationPath(directory, goreleaserName); err != nil {
		return nil, err
	}
	installation := &sourceInstallation{
		directory: directory, name: filepath.Base(directory), command: goreleaserName,
		owned: make(map[string]os.FileInfo), retained: true,
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, installation.Close())
		}
	}()
	var err error
	installation.parent, err = os.OpenRoot(filepath.Dir(directory))
	if err != nil {
		return nil, fmt.Errorf("open publisher prerequisite parent: %w", err)
	}
	installation.parentInfo, err = installation.parent.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("inspect publisher prerequisite parent: %w", err)
	}
	installation.root, err = installation.parent.OpenRoot(installation.name)
	if err != nil {
		return nil, fmt.Errorf("open publisher prerequisite: %w", err)
	}
	for _, name := range []string{".", sourceStageDirectory, "bin"} {
		installation.owned[name], err = installation.root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("inspect publisher prerequisite %s: %w", name, err)
		}
	}
	if openErr := installation.openExecutableAt(filepath.Join("bin", goreleaserName)); openErr != nil {
		return nil, openErr
	}
	info, err := installation.root.Lstat(goreleaserProvenance)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("publisher prerequisite provenance is not private and regular: %w",
			errors.Join(err, errInvalidConfig))
	}
	installation.artifacts = map[string]os.FileInfo{goreleaserProvenance: info}
	return installation, nil
}

func packageBuild(ctx context.Context, options PackageOptions,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) (returnErr error) {
	installation, err := openPackagePublisher(ctx, options, readBuildInfo)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, installation.Close()) }()
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: goreleaserName, Executable: installation.executable, Dir: options.Root, Env: options.Environment,
		Args: []string{ghReleaseCommand, "--clean", "--parallelism=1", "--skip=publish,announce,validate,sign,sbom,docker"},
	}); err != nil {
		return fmt.Errorf("build GoReleaser packages: %w", err)
	}
	if err := installation.verify(); err != nil {
		return err
	}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go", Args: []string{"-C", "tools", "run", "./cmd/packagefinalize"},
		Dir: options.Root, Env: options.Environment,
	}); err != nil {
		return fmt.Errorf("finalize RPM lifecycle: %w", err)
	}
	return nil
}
