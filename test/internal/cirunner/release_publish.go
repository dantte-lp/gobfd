package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
)

// PublishVerifiedReleaseOptions supplies immutable release identity and evidence roots.
type PublishVerifiedReleaseOptions struct {
	Root         string
	ArtifactRoot string
	RunnerTemp   string
	RefName      string
	SHA          string
	Repository   string
	Environment  []string
	Runner       SpecRunner
}

// PublishVerifiedRelease revalidates the complete release state and performs one publication mutation.
func PublishVerifiedRelease(ctx context.Context, options PublishVerifiedReleaseOptions) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "release verifier root")
	if err != nil {
		return err
	}
	artifactRootPath, err := validateAbsoluteExistingDirectory(options.ArtifactRoot, "release artifact root")
	if err != nil {
		return err
	}
	runnerTempPath, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	version, releaseBranch, err := parseStableReleaseVersion(options.RefName)
	if err != nil {
		return err
	}
	expectedCommit, err := validateFullCommitSHA(options.SHA, "GITHUB_SHA")
	if err != nil {
		return err
	}
	owner, repository, err := parseGitHubRepository(options.Repository)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("verified release publication command runner is required: %w", errInvalidConfig)
	}

	verifierRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open release verifier root for publication: %w", err)
	}
	var artifactRoot, runnerTemp *os.Root
	rootsClosed := false
	defer func() {
		if !rootsClosed {
			returnErr = errors.Join(
				returnErr,
				closeReleasePublicationRoots(verifierRoot, artifactRoot, runnerTemp),
			)
		}
	}()
	artifactRoot, err = os.OpenRoot(artifactRootPath)
	if err != nil {
		return fmt.Errorf("open release artifact root for publication: %w", err)
	}
	runnerTemp, err = os.OpenRoot(runnerTempPath)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for release publication: %w", err)
	}
	if err := validatePromotionRoots(
		verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "before publication checks",
	); err != nil {
		return err
	}
	receiptTagObject, err := readExpectedReleaseIdentityReceipts(runnerTemp, expectedCommit, releaseBranch)
	if err != nil {
		return err
	}
	expectedAssets := expectedReleaseAssetNames(version, options.RefName)
	expectedAssetReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-assets.txt", "expected release asset manifest", releaseArtifactsManifestLimit,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(expectedAssetReceipt, renderArtifactNames(expectedAssets)) {
		return fmt.Errorf("expected release asset manifest is not canonical: %w", errInvalidConfig)
	}
	releaseNotes, err := readRootedRegularFile(artifactRoot, "release-notes.md", "release notes", releaseNotesLimit)
	if err != nil {
		return err
	}
	identityReceipt, err := readRootedRegularFile(
		runnerTemp, releaseAssetIdentityReceiptName,
		"release asset identity receipt", releaseAssetIdentityReceiptLimit,
	)
	if err != nil {
		return err
	}
	digestReceipt, err := readRootedRegularFile(
		artifactRoot, "release-image-digests.txt", "release OCI digest receipt", releaseOCIDigestReceiptLimit,
	)
	if err != nil {
		return err
	}
	versioned, err := parseReleaseOCIDigestReceipt(digestReceipt, version)
	if err != nil {
		return err
	}
	const imageRepository = "ghcr.io/dantte-lp/gobfd"
	aliases := []releaseOCIImageDigest{
		{Image: imageRepository + ":latest", Digest: versioned[0].Digest},
		{Image: imageRepository + ":debian-trixie", Digest: versioned[0].Digest},
		{Image: imageRepository + ":oraclelinux10", Digest: versioned[2].Digest},
	}
	if _, err := inspectReleaseOCIAliases(
		ctx, options.Runner, root, aliases,
		withoutEnvironmentKeys(options.Environment, "GH_TOKEN", "GITHUB_TOKEN"),
	); err != nil {
		return err
	}
	actualTagObject, err := verifyReleaseGitIdentity(
		ctx, options.Runner, root, owner, repository, options.RefName, releaseBranch, expectedCommit, options.Environment,
	)
	if err != nil {
		return err
	}
	if actualTagObject != receiptTagObject {
		return fmt.Errorf("release tag object receipt changed before publication: %w", errInvalidConfig)
	}
	draftData, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "gh", Args: []string{
			"release", "view", options.RefName, "--repo", owner + "/" + repository,
			"--json", "isDraft,tagName,body,assets",
		}, Dir: root, Env: options.Environment,
	}, "read exact release draft before publication")
	if err != nil {
		return err
	}
	if err := validateStrictJSONDocument(draftData, "release draft before publication"); err != nil {
		return err
	}
	remoteAssets, err := validateExactReleaseDraft(
		draftData, options.RefName, owner+"/"+repository, releaseNotes, expectedAssets,
	)
	if err != nil {
		return err
	}
	assetRecords, err := parseReleaseAssetIdentityReceipt(
		identityReceipt, options.RefName, expectedAssets, remoteAssets,
	)
	if err != nil {
		return err
	}
	if err := validateReleaseDigestSnapshot(digestReceipt, assetRecords); err != nil {
		return err
	}
	if err := validatePromotionRoots(
		verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "immediately before publication",
	); err != nil {
		return err
	}
	closeErr := closeReleasePublicationRoots(verifierRoot, artifactRoot, runnerTemp)
	rootsClosed = true
	if closeErr != nil {
		return closeErr
	}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "gh", Args: []string{
			"release", "edit", options.RefName, "--repo", owner + "/" + repository,
			"--draft=false", "--latest",
		}, Dir: root, Env: options.Environment,
	}); err != nil {
		return fmt.Errorf("publish exact verified release: %w", err)
	}
	return nil
}

func validateReleaseDigestSnapshot(data []byte, records []releaseAssetIdentityRecord) error {
	for _, record := range records {
		if record.Name != "release-image-digests.txt" {
			continue
		}
		digest := sha256.Sum256(data)
		if record.Size != int64(len(data)) || record.Digest != fmt.Sprintf("sha256:%x", digest) {
			return fmt.Errorf("local OCI digest receipt differs from verified release asset bytes: %w", errInvalidConfig)
		}
		return nil
	}
	return fmt.Errorf("release asset identity receipt lacks OCI digest receipt: %w", errInvalidConfig)
}

func closeReleasePublicationRoots(verifierRoot, artifactRoot, runnerTemp *os.Root) error {
	var result error
	if runnerTemp != nil {
		result = errors.Join(result, wrapOptional("close RUNNER_TEMP release publication root", runnerTemp.Close()))
	}
	if artifactRoot != nil {
		result = errors.Join(result, wrapOptional("close release artifact publication root", artifactRoot.Close()))
	}
	if verifierRoot != nil {
		result = errors.Join(result, wrapOptional("close release verifier publication root", verifierRoot.Close()))
	}
	return result
}
