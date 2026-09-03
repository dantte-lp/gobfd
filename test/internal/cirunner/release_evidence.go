package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	releaseEvidenceChecksumLimit = 1 << 10
	releaseEvidenceStageName     = ".release-evidence-upload"
)

// ReleaseEvidenceOptions supplies trusted command and artifact roots for supplemental release assets.
type ReleaseEvidenceOptions struct {
	Root         string
	ArtifactRoot string
	RefName      string
	Environment  []string
	Runner       SpecRunner
}

// ReleaseEvidence validates, hashes, and uploads the fixed supplemental release asset set.
func ReleaseEvidence(ctx context.Context, options ReleaseEvidenceOptions) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "release verifier root")
	if err != nil {
		return err
	}
	artifactRootPath, err := validateAbsoluteExistingDirectory(options.ArtifactRoot, "release evidence root")
	if err != nil {
		return err
	}
	version, _, err := parseStableReleaseVersion(options.RefName)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("release evidence command runner is required: %w", errInvalidConfig)
	}
	verifierRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open release verifier root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release verifier root", verifierRoot.Close()))
	}()
	if identityErr := validateRootPathIdentity(
		verifierRoot, root, "release verifier root before staging",
	); identityErr != nil {
		return identityErr
	}
	artifactRoot, err := os.OpenRoot(artifactRootPath)
	if err != nil {
		return fmt.Errorf("open release evidence root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release evidence root", artifactRoot.Close()))
	}()
	if identityErr := validateRootPathIdentity(
		artifactRoot, artifactRootPath, "release evidence root before validation",
	); identityErr != nil {
		return identityErr
	}

	reportName := "gobfd-" + options.RefName + "-reports.tar.gz"
	digestName := "release-image-digests.txt"
	checksumName := "release-evidence-checksums.txt"
	if targetErr := validateRootedRegularTarget(
		artifactRoot, checksumName, "release evidence checksum",
	); targetErr != nil {
		return targetErr
	}
	report, err := readRootedRegularFile(artifactRoot, reportName, "release reports archive", releaseArtifactLimit)
	if err != nil {
		return err
	}
	digests, err := readRootedRegularFile(
		artifactRoot, digestName, "release image digest receipt", releaseOCIDigestReceiptLimit,
	)
	if err != nil {
		return err
	}
	if digestErr := validateReleaseOCIDigestReceipt(digests, version); digestErr != nil {
		return digestErr
	}
	checksums := append(
		formatReleaseSHA256Line(report, reportName),
		formatReleaseSHA256Line(digests, digestName)...,
	)
	if identityErr := validateRootPathIdentity(
		artifactRoot, artifactRootPath, "release evidence root before checksums",
	); identityErr != nil {
		return identityErr
	}
	if writeErr := writeRootedArtifact(
		artifactRoot, checksumName, checksums, "release evidence checksum", releaseEvidenceChecksumLimit,
	); writeErr != nil {
		return writeErr
	}
	expected := []releaseEvidenceFile{
		{name: reportName, data: report, limit: releaseArtifactLimit},
		{name: digestName, data: digests, limit: releaseOCIDigestReceiptLimit},
		{name: checksumName, data: checksums, limit: releaseEvidenceChecksumLimit},
	}
	if snapshotErr := validateReleaseEvidenceSnapshot(
		artifactRoot, artifactRootPath, expected, "before upload",
	); snapshotErr != nil {
		return snapshotErr
	}
	stageRoot, stageInfo, err := prepareReleaseEvidenceStage(verifierRoot, root, expected)
	if err != nil {
		return err
	}
	stagePath := filepath.Join(root, releaseEvidenceStageName)
	defer func() {
		returnErr = errors.Join(
			returnErr,
			cleanupReleaseEvidenceStage(verifierRoot, stageRoot, stageInfo, expected),
		)
	}()
	arguments := []string{"release", "upload", options.RefName}
	for _, asset := range expected {
		arguments = append(arguments, filepath.Join(stagePath, asset.name))
	}
	uploadErr := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "gh", Args: arguments, Dir: root, Env: options.Environment,
	})
	if uploadErr != nil {
		uploadErr = fmt.Errorf("upload fixed supplemental release assets: %w", uploadErr)
	}
	postValidationErr := errors.Join(
		validateReleaseEvidenceSnapshot(artifactRoot, artifactRootPath, expected, "after upload"),
		validateReleaseEvidenceSnapshot(stageRoot, stagePath, expected, "after upload"),
	)
	if uploadErr != nil || postValidationErr != nil {
		return errors.Join(uploadErr, postValidationErr)
	}
	return nil
}

type releaseEvidenceFile struct {
	name  string
	data  []byte
	limit int64
}

func prepareReleaseEvidenceStage(
	verifierRoot *os.Root,
	verifierRootPath string,
	files []releaseEvidenceFile,
) (stageRoot *os.Root, stageInfo os.FileInfo, returnErr error) {
	if err := validateRootPathIdentity(
		verifierRoot, verifierRootPath, "release verifier root before staging snapshot",
	); err != nil {
		return nil, nil, err
	}
	if _, err := verifierRoot.Lstat(releaseEvidenceStageName); err == nil {
		return nil, nil, fmt.Errorf("release evidence staging directory already exists: %w", errInvalidConfig)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("inspect release evidence staging directory: %w", err)
	}
	if err := verifierRoot.Mkdir(releaseEvidenceStageName, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create release evidence staging directory: %w", err)
	}
	defer func() {
		if returnErr == nil {
			return
		}
		returnErr = errors.Join(
			returnErr,
			cleanupReleaseEvidenceStage(verifierRoot, stageRoot, stageInfo, files),
		)
	}()
	stageInfo, err := verifierRoot.Lstat(releaseEvidenceStageName)
	if err != nil || !stageInfo.IsDir() {
		return nil, stageInfo, fmt.Errorf(
			"inspect release evidence staging directory: %w", errors.Join(err, errInvalidConfig),
		)
	}
	stageRoot, err = verifierRoot.OpenRoot(releaseEvidenceStageName)
	if err != nil {
		return nil, stageInfo, fmt.Errorf("open release evidence staging root: %w", err)
	}
	openedInfo, err := stageRoot.Stat(".")
	if err != nil || !os.SameFile(openedInfo, stageInfo) {
		return stageRoot, stageInfo, fmt.Errorf(
			"release evidence staging root identity changed: %w", errors.Join(err, errInvalidConfig),
		)
	}
	for _, file := range files {
		if err := writeRootedArtifact(
			stageRoot, file.name, file.data, "release evidence staging snapshot", int(file.limit),
		); err != nil {
			return stageRoot, stageInfo, err
		}
	}
	stagePath := filepath.Join(verifierRootPath, releaseEvidenceStageName)
	if err := validateReleaseEvidenceSnapshot(stageRoot, stagePath, files, "before upload"); err != nil {
		return stageRoot, stageInfo, err
	}
	return stageRoot, stageInfo, nil
}

func cleanupReleaseEvidenceStage(
	verifierRoot *os.Root,
	stageRoot *os.Root,
	expected os.FileInfo,
	files []releaseEvidenceFile,
) error {
	var result error
	if stageRoot != nil {
		opened, err := stageRoot.Stat(".")
		if err != nil || expected == nil || !os.SameFile(opened, expected) {
			return errors.Join(
				fmt.Errorf("release evidence opened staging ownership changed: %w", errors.Join(err, errInvalidConfig)),
				wrapOptional("close unowned release evidence staging root", stageRoot.Close()),
			)
		}
		for _, file := range files {
			if err := stageRoot.Remove(file.name); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, fmt.Errorf("remove staged release evidence %s: %w", file.name, err))
			}
		}
		result = errors.Join(result, wrapOptional("close release evidence staging root", stageRoot.Close()))
	}
	current, err := verifierRoot.Lstat(releaseEvidenceStageName)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		return errors.Join(result, fmt.Errorf("inspect release evidence staging directory before cleanup: %w", err))
	}
	if expected == nil || !current.IsDir() || !os.SameFile(current, expected) {
		return errors.Join(result, fmt.Errorf("release evidence staging directory ownership changed: %w", errInvalidConfig))
	}
	if err := verifierRoot.Remove(releaseEvidenceStageName); err != nil {
		return errors.Join(result, fmt.Errorf("remove release evidence staging directory: %w", err))
	}
	return result
}

func validateReleaseEvidenceSnapshot(
	root *os.Root,
	rootPath string,
	files []releaseEvidenceFile,
	phase string,
) error {
	if err := validateRootPathIdentity(root, rootPath, "release evidence root "+phase); err != nil {
		return err
	}
	for _, expected := range files {
		actual, err := readRootedRegularFile(root, expected.name, "release evidence "+phase, expected.limit)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected.data) {
			return fmt.Errorf("release evidence %s changed %s: %w", expected.name, phase, errInvalidConfig)
		}
	}
	return nil
}

type releaseOCIImageDigest struct {
	Image  string
	Digest string
}

func validateReleaseOCIDigestReceipt(data []byte, version string) error {
	_, err := parseReleaseOCIDigestReceipt(data, version)
	return err
}

func parseReleaseOCIDigestReceipt(data []byte, version string) ([]releaseOCIImageDigest, error) {
	expectedImages := []string{
		"ghcr.io/dantte-lp/gobfd:" + version,
		"ghcr.io/dantte-lp/gobfd:" + version + "-debian-trixie",
		"ghcr.io/dantte-lp/gobfd:" + version + "-oraclelinux10",
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != len(expectedImages)+1 || lines[len(lines)-1] != "" {
		return nil, fmt.Errorf("OCI digest receipt must contain exactly three newline-terminated records: %w", errInvalidConfig)
	}
	records := make([]releaseOCIImageDigest, len(expectedImages))
	for index, expectedImage := range expectedImages {
		image, digest, found := strings.Cut(lines[index], " ")
		if !found || image != expectedImage || strings.ContainsAny(digest, " \t\r") || !canonicalOCIDigest(digest) {
			return nil, fmt.Errorf("OCI digest receipt record %d is not canonical: %w", index, errInvalidConfig)
		}
		records[index] = releaseOCIImageDigest{Image: image, Digest: digest}
	}
	if records[0].Digest != records[1].Digest {
		return nil, fmt.Errorf("primary and Debian OCI digest receipt records differ: %w", errInvalidConfig)
	}
	return records, nil
}

func formatReleaseSHA256Line(data []byte, name string) []byte {
	digest := sha256.Sum256(data)
	return fmt.Appendf(nil, "%x  %s\n", digest, name)
}
