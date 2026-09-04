package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
)

const (
	ghReleaseCommand = "release"
	ghRepositoryFlag = "--repo"
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

type releasePublicationInputs struct {
	root, artifactRoot, runnerTemp         string
	version, releaseBranch, expectedCommit string
	owner, repository                      string
}

type releasePublicationEvidence struct {
	receiptTagObject                string
	expectedAssets                  []string
	releaseNotes, identity, digests []byte
	versioned                       []releaseOCIImageDigest
}

// PublishVerifiedRelease revalidates the complete release state and performs one publication mutation.
func PublishVerifiedRelease(ctx context.Context, options PublishVerifiedReleaseOptions) (returnErr error) {
	inputs, err := validateReleasePublicationInputs(options)
	if err != nil {
		return err
	}
	verifierRoot, err := os.OpenRoot(inputs.root)
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
	artifactRoot, err = os.OpenRoot(inputs.artifactRoot)
	if err != nil {
		return fmt.Errorf("open release artifact root for publication: %w", err)
	}
	runnerTemp, err = os.OpenRoot(inputs.runnerTemp)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for release publication: %w", err)
	}
	if err := verifyReleasePublication(ctx, options, inputs, verifierRoot, artifactRoot, runnerTemp); err != nil {
		return err
	}
	closeErr := closeReleasePublicationRoots(verifierRoot, artifactRoot, runnerTemp)
	rootsClosed = true
	if closeErr != nil {
		return closeErr
	}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "gh", Args: []string{
			ghReleaseCommand, "edit", options.RefName, ghRepositoryFlag, inputs.owner + "/" + inputs.repository,
			"--draft=false", "--latest",
		}, Dir: inputs.root, Env: options.Environment,
	}); err != nil {
		return fmt.Errorf("publish exact verified release: %w", err)
	}
	return nil
}

func validateReleasePublicationInputs(options PublishVerifiedReleaseOptions) (releasePublicationInputs, error) {
	inputs := releasePublicationInputs{}
	var err error
	inputs.root, err = validateAbsoluteExistingDirectory(options.Root, "release verifier root")
	if err != nil {
		return releasePublicationInputs{}, err
	}
	inputs.artifactRoot, err = validateAbsoluteExistingDirectory(options.ArtifactRoot, "release artifact root")
	if err != nil {
		return releasePublicationInputs{}, err
	}
	inputs.runnerTemp, err = validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return releasePublicationInputs{}, err
	}
	inputs.version, inputs.releaseBranch, err = parseStableReleaseVersion(options.RefName)
	if err != nil {
		return releasePublicationInputs{}, err
	}
	inputs.expectedCommit, err = validateFullCommitSHA(options.SHA, "GITHUB_SHA")
	if err != nil {
		return releasePublicationInputs{}, err
	}
	inputs.owner, inputs.repository, err = parseGitHubRepository(options.Repository)
	if err != nil {
		return releasePublicationInputs{}, err
	}
	if options.Runner == nil {
		return releasePublicationInputs{}, fmt.Errorf(
			"verified release publication command runner is required: %w", errInvalidConfig,
		)
	}
	return inputs, nil
}

func verifyReleasePublication(
	ctx context.Context,
	options PublishVerifiedReleaseOptions,
	inputs releasePublicationInputs,
	verifierRoot, artifactRoot, runnerTemp *os.Root,
) error {
	if err := validatePromotionRoots(
		verifierRoot, inputs.root, artifactRoot, inputs.artifactRoot,
		runnerTemp, inputs.runnerTemp, "before publication checks",
	); err != nil {
		return err
	}
	evidence, err := readReleasePublicationEvidence(inputs, options.RefName, artifactRoot, runnerTemp)
	if err != nil {
		return err
	}
	const imageRepository = "ghcr.io/dantte-lp/gobfd"
	aliases := []releaseOCIImageDigest{
		{Image: imageRepository + ":latest", Digest: evidence.versioned[0].Digest},
		{Image: imageRepository + ":debian-trixie", Digest: evidence.versioned[0].Digest},
		{Image: imageRepository + ":oraclelinux10", Digest: evidence.versioned[2].Digest},
	}
	if _, aliasesErr := inspectReleaseOCIAliases(
		ctx, options.Runner, inputs.root, aliases,
		withoutEnvironmentKeys(options.Environment, "GH_TOKEN", "GITHUB_TOKEN"),
	); aliasesErr != nil {
		return aliasesErr
	}
	if err := verifyRemoteReleasePublication(ctx, options, inputs, evidence); err != nil {
		return err
	}
	return validatePromotionRoots(
		verifierRoot, inputs.root, artifactRoot, inputs.artifactRoot,
		runnerTemp, inputs.runnerTemp, "immediately before publication",
	)
}

func readReleasePublicationEvidence(
	inputs releasePublicationInputs,
	refName string,
	artifactRoot, runnerTemp *os.Root,
) (releasePublicationEvidence, error) {
	evidence := releasePublicationEvidence{}
	var err error
	evidence.receiptTagObject, err = readExpectedReleaseIdentityReceipts(
		runnerTemp, inputs.expectedCommit, inputs.releaseBranch,
	)
	if err != nil {
		return releasePublicationEvidence{}, err
	}
	evidence.expectedAssets = expectedReleaseAssetNames(inputs.version, refName)
	expectedAssetReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-assets.txt", "expected release asset manifest", releaseArtifactsManifestLimit,
	)
	if err != nil {
		return releasePublicationEvidence{}, err
	}
	if !bytes.Equal(expectedAssetReceipt, renderArtifactNames(evidence.expectedAssets)) {
		return releasePublicationEvidence{}, fmt.Errorf(
			"expected release asset manifest is not canonical: %w", errInvalidConfig,
		)
	}
	evidence.releaseNotes, err = readRootedRegularFile(
		artifactRoot, "release-notes.md", "release notes", releaseNotesLimit,
	)
	if err != nil {
		return releasePublicationEvidence{}, err
	}
	evidence.identity, err = readRootedRegularFile(
		runnerTemp, releaseAssetIdentityReceiptName,
		"release asset identity receipt", releaseAssetIdentityReceiptLimit,
	)
	if err != nil {
		return releasePublicationEvidence{}, err
	}
	evidence.digests, err = readRootedRegularFile(
		artifactRoot, "release-image-digests.txt", "release OCI digest receipt", releaseOCIDigestReceiptLimit,
	)
	if err != nil {
		return releasePublicationEvidence{}, err
	}
	evidence.versioned, err = parseReleaseOCIDigestReceipt(evidence.digests, inputs.version)
	if err != nil {
		return releasePublicationEvidence{}, err
	}
	return evidence, nil
}

func verifyRemoteReleasePublication(
	ctx context.Context,
	options PublishVerifiedReleaseOptions,
	inputs releasePublicationInputs,
	evidence releasePublicationEvidence,
) error {
	actualTagObject, err := verifyReleaseGitIdentity(
		ctx, options.Runner, inputs.root, inputs.owner, inputs.repository,
		options.RefName, inputs.releaseBranch, inputs.expectedCommit, options.Environment,
	)
	if err != nil {
		return err
	}
	if actualTagObject != evidence.receiptTagObject {
		return fmt.Errorf("release tag object receipt changed before publication: %w", errInvalidConfig)
	}
	draftData, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "gh", Args: []string{
			ghReleaseCommand, "view", options.RefName, ghRepositoryFlag, inputs.owner + "/" + inputs.repository,
			"--json", "isDraft,tagName,body,assets",
		}, Dir: inputs.root, Env: options.Environment,
	}, "read exact release draft before publication")
	if err != nil {
		return err
	}
	if validationErr := validateStrictJSONDocument(
		draftData, "release draft before publication",
	); validationErr != nil {
		return validationErr
	}
	remoteAssets, err := validateExactReleaseDraft(
		draftData, options.RefName, inputs.owner+"/"+inputs.repository,
		evidence.releaseNotes, evidence.expectedAssets,
	)
	if err != nil {
		return err
	}
	assetRecords, err := parseReleaseAssetIdentityReceipt(
		evidence.identity, options.RefName, evidence.expectedAssets, remoteAssets,
	)
	if err != nil {
		return err
	}
	return validateReleaseDigestSnapshot(evidence.digests, assetRecords)
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
