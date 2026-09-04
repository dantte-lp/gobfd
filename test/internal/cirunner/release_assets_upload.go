package cirunner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// UploadReleaseAssetsOptions supplies the exact draft-release asset roots.
type UploadReleaseAssetsOptions struct {
	ArtifactRoot string
	RunnerTemp   string
	RefName      string
	Environment  []string
	Runner       SpecRunner
}

// UploadReleaseAssets validates and uploads the canonical GoReleaser asset set.
func UploadReleaseAssets(ctx context.Context, options UploadReleaseAssetsOptions) error {
	artifactRoot, err := validateAbsoluteExistingDirectory(options.ArtifactRoot, "release artifact root")
	if err != nil {
		return err
	}
	runnerTemp, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	version, _, err := parseStableReleaseVersion(options.RefName)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("release asset command runner is required: %w", errInvalidConfig)
	}
	distPath, err := validateAbsoluteExistingDirectory(filepath.Join(artifactRoot, "dist"), "release dist root")
	if err != nil {
		return err
	}
	dist, err := os.OpenRoot(distPath)
	if err != nil {
		return fmt.Errorf("open release dist root: %w", err)
	}
	defer dist.Close()
	receipts, err := os.OpenRoot(runnerTemp)
	if err != nil {
		return fmt.Errorf("open release receipt root: %w", err)
	}
	defer receipts.Close()
	expected := expectedChecksummedArtifactNames(version)
	receipt, err := readRootedRegularFile(
		receipts, "expected-checksummed-assets.txt", "expected checksummed asset manifest", releaseArtifactsManifestLimit,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(receipt, renderArtifactNames(expected)) {
		return fmt.Errorf("expected checksummed asset manifest is not canonical: %w", errInvalidConfig)
	}
	if err := validateReleaseUploadAssets(dist, expected); err != nil {
		return fmt.Errorf("validate release assets before upload: %w", err)
	}
	arguments := []string{ghReleaseCommand, "upload", options.RefName, filepath.Join(distPath, "checksums.txt")}
	for _, name := range expected {
		arguments = append(arguments, filepath.Join(distPath, name))
	}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "gh", Args: arguments, Dir: artifactRoot, Env: options.Environment,
	}); err != nil {
		return fmt.Errorf("upload primary release assets: %w", err)
	}
	if err := validateReleaseUploadAssets(dist, expected); err != nil {
		return fmt.Errorf("validate release assets after upload: %w", err)
	}
	return nil
}

func validateReleaseUploadAssets(dist *os.Root, expected []string) error {
	checksums, err := readRootedRegularFile(
		dist, "checksums.txt", "release checksum manifest", releaseChecksumFileLimit,
	)
	if err != nil {
		return err
	}
	records, err := parseReleaseChecksumRecords(checksums, "release checksum manifest", expected)
	if err != nil {
		return err
	}
	_, _, err = validateReleaseChecksums(dist, records, expected, nil)
	return err
}
