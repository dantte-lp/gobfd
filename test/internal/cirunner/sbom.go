package cirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const reportFileMode = 0o644

// SBOMOptions configures reports for each first-party Go module manifest.
type SBOMOptions struct {
	Root        string
	ReportDir   string
	Environment []string
	Runner      SpecRunner
}

// SBOM emits CycloneDX and SPDX manifest reports, not full selected-MVS attestations.
func SBOM(ctx context.Context, options SBOMOptions) (returnErr error) {
	if options.Runner == nil {
		return fmt.Errorf("SBOM command runner is required: %w", errInvalidConfig)
	}
	reportDir, err := validateSafeDirectory(options.ReportDir, "SBOM report", true)
	if err != nil {
		return err
	}
	if directoryErr := ensureDirectory(reportDir, "SBOM report"); directoryErr != nil {
		return directoryErr
	}
	root, err := validateAbsoluteExistingDirectory(options.Root, "SBOM repository")
	if err != nil {
		return err
	}
	installation, err := InstallSyft(ctx, SyftOptions{
		Root: root, InstallDir: temporarySyftDirectory(reportDir),
		Environment: options.Environment, Runner: options.Runner,
	})
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, installation.Close()) }()

	reports := []struct {
		name  string
		input string
	}{
		{name: "runtime", input: goModFilename},
		{name: "tools", input: "tools/go.mod"},
		{name: "gopls", input: "tools/gopls/go.mod"},
		{name: syftName, input: toolprofile.SyftManifest},
	}
	options.Root, options.ReportDir = root, reportDir
	for _, report := range reports {
		if scanErr := emitManifestSBOMs(ctx, options, installation, report.name, report.input); scanErr != nil {
			return scanErr
		}
	}
	return nil
}

func emitManifestSBOMs(
	ctx context.Context, options SBOMOptions, installation *SyftInstallation, name, input string,
) error {
	for _, format := range []struct{ extension, cataloger string }{
		{"cdx", "cyclonedx-json"}, {"spdx", "spdx-json"},
	} {
		output := filepath.Join(options.ReportDir, name+"-sbom."+format.extension+".json")
		if err := prepareArtifact(output, name); err != nil {
			return err
		}
		spec := CommandSpec{
			Name: syftName, Dir: options.Root, Env: syftEnvironment(options.Environment), Executable: installation.executable,
			Args: []string{
				"scan", "file:" + input,
				"--override-default-catalogers", "go-module-file-cataloger",
				"--quiet", "--output", format.cataloger + "=" + output,
			},
		}
		if err := installation.verify(); err != nil {
			return err
		}
		if err := options.Runner.RunCommand(ctx, spec); err != nil {
			return fmt.Errorf("generate %s SBOM: %w", name, err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("finish %s SBOM: %w", name, err)
		}
		if err := installation.verify(); err != nil {
			return err
		}
		if err := validateArtifact(output, name); err != nil {
			return err
		}
	}
	return nil
}

func prepareArtifact(path, name string) error {
	if err := rejectNonregularArtifact(path, name); err != nil {
		return err
	}
	// Public SBOMs contain dependency identities and preserve shared-reader mode 0644.
	// #nosec G302 G304 -- Fixed output basenames use validateSafeDirectory; nonregular targets are rejected.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, reportFileMode)
	if err != nil {
		return fmt.Errorf("prepare %s SBOM artifact: %w", name, err)
	}
	if err := errors.Join(file.Chmod(reportFileMode), file.Close()); err != nil {
		return fmt.Errorf("set fresh %s SBOM artifact mode: %w", name, err)
	}
	return nil
}

func rejectNonregularArtifact(path, name string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s SBOM artifact: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s SBOM artifact %s has mode %s: %w", name, path, info.Mode(), errInvalidConfig)
	}
	return nil
}

func validateArtifact(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s SBOM artifact: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s SBOM artifact %s is not a non-empty regular file: %w",
			name, path, errInvalidConfig)
	}
	// #nosec G302 -- The validated dependency SBOM intentionally permits public readers with mode 0644.
	if err := os.Chmod(path, reportFileMode); err != nil {
		return fmt.Errorf("set %s SBOM artifact mode: %w", name, err)
	}
	return nil
}
