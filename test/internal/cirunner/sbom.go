package cirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	reportDirectoryMode = 0o755
	reportFileMode      = 0o644
	syftModule          = "github.com/anchore/syft/cmd/syft@v1.51.0"
)

// SBOMOptions configures generation of the separate runtime and tools SBOMs.
type SBOMOptions struct {
	ReportDir string
	Runner    SpecRunner
}

// SBOM generates and validates the runtime and tools CycloneDX reports.
func SBOM(ctx context.Context, options SBOMOptions) error {
	if options.Runner == nil {
		return fmt.Errorf("SBOM command runner is required: %w", errInvalidConfig)
	}
	reportDir, err := validateSafeDirectory(options.ReportDir, "SBOM report", true)
	if err != nil {
		return err
	}
	if err := ensureDirectory(reportDir, "SBOM report", reportDirectoryMode); err != nil {
		return err
	}

	reports := []struct {
		name   string
		input  string
		output string
	}{
		{name: "runtime", input: "go.mod", output: "runtime-sbom.cdx.json"},
		{name: "tools", input: "tools/go.mod", output: "tools-sbom.cdx.json"},
	}
	for _, report := range reports {
		output := filepath.Join(reportDir, report.output)
		if err := prepareArtifact(output, report.name); err != nil {
			return err
		}
		spec := CommandSpec{
			Name: "go",
			Args: []string{
				"run", syftModule, "scan", "file:" + report.input,
				"--override-default-catalogers", "go-module-file-cataloger",
				"--quiet", "--output", "cyclonedx-json=" + output,
			},
		}
		if err := options.Runner.RunCommand(ctx, spec); err != nil {
			return fmt.Errorf("generate %s SBOM: %w", report.name, err)
		}
		if err := validateArtifact(output, report.name); err != nil {
			return err
		}
	}
	return nil
}

func prepareArtifact(path, name string) error {
	if err := rejectNonregularArtifact(path, name); err != nil {
		return err
	}
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
	if err := os.Chmod(path, reportFileMode); err != nil {
		return fmt.Errorf("set %s SBOM artifact mode: %w", name, err)
	}
	return nil
}
