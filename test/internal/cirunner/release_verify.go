package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// VerifyReleaseDraftOptions supplies immutable release identity and evidence roots.
type VerifyReleaseDraftOptions struct {
	Root         string
	ArtifactRoot string
	RunnerTemp   string
	RefName      string
	SHA          string
	Repository   string
	Environment  []string
	Runner       SpecRunner
}

type releaseDraftVerification struct {
	options                                VerifyReleaseDraftOptions
	root, artifactRootPath, runnerTempPath string
	version, releaseBranch, expectedCommit string
	owner, repository                      string
	verifierRoot, artifactRoot, runnerTemp *os.Root
}

// VerifyReleaseDraft revalidates release identity, OCI receipts, and the exact draft manifest.
func VerifyReleaseDraft(ctx context.Context, options VerifyReleaseDraftOptions) (returnErr error) {
	verification, err := validateReleaseDraftVerification(options)
	if err != nil {
		return err
	}
	verification.verifierRoot, err = os.OpenRoot(verification.root)
	if err != nil {
		return fmt.Errorf("open release verifier root for draft verification: %w", err)
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			wrapOptional("close release verifier draft root", verification.verifierRoot.Close()),
		)
	}()
	verification.artifactRoot, err = os.OpenRoot(verification.artifactRootPath)
	if err != nil {
		return fmt.Errorf("open release artifact root for verification: %w", err)
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			wrapOptional("close release artifact verification root", verification.artifactRoot.Close()),
		)
	}()
	verification.runnerTemp, err = os.OpenRoot(verification.runnerTempPath)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for release verification: %w", err)
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			wrapOptional("close RUNNER_TEMP release verification root", verification.runnerTemp.Close()),
		)
	}()
	return verifyReleaseDraft(ctx, &verification)
}

func validateReleaseDraftVerification(options VerifyReleaseDraftOptions) (releaseDraftVerification, error) {
	verification := releaseDraftVerification{options: options}
	var err error
	verification.root, err = validateAbsoluteExistingDirectory(options.Root, "release verifier root")
	if err != nil {
		return releaseDraftVerification{}, err
	}
	verification.artifactRootPath, err = validateAbsoluteExistingDirectory(
		options.ArtifactRoot, "release artifact root",
	)
	if err != nil {
		return releaseDraftVerification{}, err
	}
	verification.runnerTempPath, err = validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return releaseDraftVerification{}, err
	}
	verification.version, verification.releaseBranch, err = parseStableReleaseVersion(options.RefName)
	if err != nil {
		return releaseDraftVerification{}, err
	}
	verification.expectedCommit, err = validateFullCommitSHA(options.SHA, "GITHUB_SHA")
	if err != nil {
		return releaseDraftVerification{}, err
	}
	verification.owner, verification.repository, err = parseGitHubRepository(options.Repository)
	if err != nil {
		return releaseDraftVerification{}, err
	}
	if options.Runner == nil {
		return releaseDraftVerification{}, fmt.Errorf(
			"release draft verification command runner is required: %w", errInvalidConfig,
		)
	}
	return verification, nil
}

func verifyReleaseDraft(ctx context.Context, verification *releaseDraftVerification) error {
	if err := verification.validateRoots("before verification", "before release verification"); err != nil {
		return err
	}
	receiptTagObject, err := readExpectedReleaseIdentityReceipts(
		verification.runnerTemp, verification.expectedCommit, verification.releaseBranch,
	)
	if err != nil {
		return err
	}
	actualTagObject, err := verifyReleaseGitIdentity(
		ctx, verification.options.Runner, verification.root,
		verification.owner, verification.repository, verification.options.RefName,
		verification.releaseBranch, verification.expectedCommit, verification.options.Environment,
	)
	if err != nil {
		return err
	}
	if actualTagObject != receiptTagObject {
		return fmt.Errorf("release tag object receipt changed after preflight: %w", errInvalidConfig)
	}
	remoteAssets, expectedAssets, err := verifyReleaseDraftEvidence(ctx, verification)
	if err != nil {
		return err
	}
	if err := verification.validateRoots("after verification", "after release verification"); err != nil {
		return err
	}
	return verifyDownloadedReleaseAssets(ctx, verification, remoteAssets, expectedAssets)
}

func (verification *releaseDraftVerification) validateRoots(phase, runnerPhase string) error {
	if err := validateRootPathIdentity(
		verification.verifierRoot, verification.root, "release verifier root "+phase,
	); err != nil {
		return err
	}
	if err := validateRootPathIdentity(
		verification.artifactRoot, verification.artifactRootPath, "release artifact root "+phase,
	); err != nil {
		return err
	}
	return validateRootPathIdentity(
		verification.runnerTemp, verification.runnerTempPath, "RUNNER_TEMP "+runnerPhase,
	)
}

func verifyReleaseDraftEvidence(
	ctx context.Context,
	verification *releaseDraftVerification,
) ([]releaseRemoteAssetIdentity, []string, error) {
	if err := verifyReleaseDraftOCI(ctx, verification); err != nil {
		return nil, nil, err
	}
	expectedAssets := expectedReleaseAssetNames(verification.version, verification.options.RefName)
	expectedAssetReceipt, err := readRootedRegularFile(
		verification.runnerTemp, "expected-release-assets.txt",
		"expected release asset manifest", releaseArtifactsManifestLimit,
	)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(expectedAssetReceipt, renderArtifactNames(expectedAssets)) {
		return nil, nil, fmt.Errorf("expected release asset manifest is not canonical: %w", errInvalidConfig)
	}
	releaseNotes, err := readRootedRegularFile(
		verification.artifactRoot, "release-notes.md", "release notes", releaseNotesLimit,
	)
	if err != nil {
		return nil, nil, err
	}
	draftData, err := runReleasePreflightCommand(ctx, verification.options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			ghReleaseCommand, "view", verification.options.RefName, ghRepositoryFlag,
			verification.owner + "/" + verification.repository,
			"--json", "isDraft,tagName,body,assets",
		},
		Dir: verification.root, Env: verification.options.Environment,
	}, "read exact release draft")
	if err != nil {
		return nil, nil, err
	}
	if validationErr := validateStrictJSONDocument(draftData, "release draft"); validationErr != nil {
		return nil, nil, validationErr
	}
	remoteAssets, err := validateExactReleaseDraft(
		draftData, verification.options.RefName,
		verification.owner+"/"+verification.repository, releaseNotes, expectedAssets,
	)
	if err != nil {
		return nil, nil, err
	}
	return remoteAssets, expectedAssets, nil
}

func verifyReleaseDraftOCI(ctx context.Context, verification *releaseDraftVerification) error {
	digestReceipt, err := readRootedRegularFile(
		verification.artifactRoot, "release-image-digests.txt",
		"release OCI digest receipt", releaseOCIDigestReceiptLimit,
	)
	if err != nil {
		return err
	}
	if validationErr := validateReleaseOCIDigestReceipt(digestReceipt, verification.version); validationErr != nil {
		return validationErr
	}
	ociEvidence, err := inspectReleaseOCIManifests(
		ctx, verification.options.Runner, verification.root,
		verification.options.RefName, verification.options.Environment,
	)
	if err != nil {
		return err
	}
	if actual := renderReleaseOCIDigestReceipt(ociEvidence); !bytes.Equal(actual, digestReceipt) {
		return fmt.Errorf(
			"release OCI digest receipt changed after evidence recording: %w", errInvalidConfig,
		)
	}
	return nil
}

func verifyDownloadedReleaseAssets(
	ctx context.Context,
	verification *releaseDraftVerification,
	remoteAssets []releaseRemoteAssetIdentity,
	expectedAssets []string,
) error {
	localAssets := make(map[string]releaseAssetSnapshot, len(expectedAssets))
	var identityReceipt []byte
	return downloadExactReleaseAssets(
		ctx, verification.options.Runner, verification.verifierRoot, verification.runnerTemp,
		verification.root, verification.runnerTempPath,
		verification.owner+"/"+verification.repository, verification.options.RefName,
		verification.options.Environment, expectedAssets,
		func(downloadRoot *os.Root) error {
			contentErr := validateReleaseAssetContentsWithEvidence(
				downloadRoot, verification.artifactRoot, verification.runnerTemp,
				verification.version, verification.options.RefName, localAssets,
			)
			var identityErr error
			if contentErr == nil {
				identityReceipt, identityErr = renderReleaseAssetIdentityReceipt(
					verification.options.RefName, remoteAssets, localAssets,
				)
			}
			return errors.Join(
				contentErr,
				identityErr,
				validateRootPathIdentity(
					verification.artifactRoot, verification.artifactRootPath,
					"release artifact root after asset content verification",
				),
			)
		},
		func() error {
			return writeAndVerifyReleaseAssetIdentityReceipt(
				verification.runnerTemp, verification.runnerTempPath, identityReceipt,
			)
		},
	)
}

func writeAndVerifyReleaseAssetIdentityReceipt(
	runnerTemp *os.Root,
	runnerTempPath string,
	identityReceipt []byte,
) (returnErr error) {
	if err := validateRootPathIdentity(
		runnerTemp, runnerTempPath, "RUNNER_TEMP before asset identity receipt",
	); err != nil {
		return err
	}
	publishedInfo, publishErr := publishReleaseAssetIdentityReceipt(runnerTemp, identityReceipt)
	keep := false
	defer func() {
		if !keep && publishedInfo != nil {
			returnErr = errors.Join(
				returnErr,
				removeOwnedRootedArtifact(
					runnerTemp, releaseAssetIdentityReceiptName, publishedInfo,
					"release asset identity receipt",
				),
			)
		}
	}()
	if publishErr != nil {
		return publishErr
	}
	if err := validateRootPathIdentity(
		runnerTemp, runnerTempPath, "RUNNER_TEMP after asset identity receipt",
	); err != nil {
		return err
	}
	writtenReceipt, err := readRootedRegularFile(
		runnerTemp, releaseAssetIdentityReceiptName,
		"release asset identity receipt", releaseAssetIdentityReceiptLimit,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(writtenReceipt, identityReceipt) {
		return fmt.Errorf("release asset identity receipt changed after write: %w", errInvalidConfig)
	}
	keep = true
	return nil
}

func removeOwnedRootedArtifact(root *os.Root, name string, expected os.FileInfo, purpose string) error {
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s before rollback: %w", purpose, err)
	}
	if expected == nil || !os.SameFile(current, expected) {
		return fmt.Errorf("%s ownership changed before rollback: %w", purpose, errInvalidConfig)
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("remove %s during rollback: %w", purpose, err)
	}
	return nil
}

func validateExactReleaseDraft(
	data []byte,
	refName string,
	repository string,
	releaseNotes []byte,
	expectedAssets []string,
) ([]releaseRemoteAssetIdentity, error) {
	fields, err := decodeRequiredJSONObject(data, "release draft fields", []string{"isDraft", "tagName", "body", "assets"})
	if err != nil {
		return nil, err
	}
	var isDraft *bool
	if decodeErr := decodeJSONDocument(
		fields["isDraft"], &isDraft, "release draft state",
	); decodeErr != nil || isDraft == nil || !*isDraft {
		return nil, fmt.Errorf("release is not an explicit draft: %w", errors.Join(decodeErr, errInvalidConfig))
	}
	tagName, err := decodeRequiredJSONString(fields["tagName"], "release draft tag")
	if err != nil {
		return nil, err
	}
	if tagName != refName {
		return nil, fmt.Errorf("release draft tag differs from canonical release tag: %w", errInvalidConfig)
	}
	body, err := decodeRequiredJSONString(fields["body"], "release draft body")
	if err != nil {
		return nil, err
	}
	if strings.TrimRight(body, "\n") != strings.TrimRight(string(releaseNotes), "\n") {
		return nil, fmt.Errorf("release draft body differs from release-notes.md: %w", errInvalidConfig)
	}
	assetFields := []map[string]json.RawMessage{}
	if err := decodeJSONDocument(fields["assets"], &assetFields, "release draft assets"); err != nil {
		return nil, err
	}
	return validateExactReleaseAssets(assetFields, repository, expectedAssets)
}

func validateExactReleaseAssets(
	assetFields []map[string]json.RawMessage,
	repository string,
	expectedAssets []string,
) ([]releaseRemoteAssetIdentity, error) {
	remoteAssets := make([]releaseRemoteAssetIdentity, 0, len(assetFields))
	nodeIDs := make(map[string]struct{}, len(assetFields))
	databaseIDs := make(map[uint64]struct{}, len(assetFields))
	for index, asset := range assetFields {
		if asset == nil {
			return nil, fmt.Errorf("release draft asset %d is not an object: %w", index, errInvalidConfig)
		}
		identity, err := validateRemoteReleaseAsset(asset, index, repository)
		if err != nil {
			return nil, err
		}
		if _, exists := nodeIDs[identity.NodeID]; exists {
			return nil, fmt.Errorf("release draft has duplicate node ID %s: %w", identity.NodeID, errInvalidConfig)
		}
		if _, exists := databaseIDs[identity.DatabaseID]; exists {
			return nil, fmt.Errorf("release draft has duplicate REST asset ID %d: %w", identity.DatabaseID, errInvalidConfig)
		}
		nodeIDs[identity.NodeID] = struct{}{}
		databaseIDs[identity.DatabaseID] = struct{}{}
		remoteAssets = append(remoteAssets, identity)
	}
	sort.Slice(remoteAssets, func(left, right int) bool { return remoteAssets[left].Name < remoteAssets[right].Name })
	actualAssets := make([]string, 0, len(remoteAssets))
	for _, asset := range remoteAssets {
		actualAssets = append(actualAssets, asset.Name)
	}
	if !slices.Equal(actualAssets, expectedAssets) {
		return nil, fmt.Errorf("release draft asset set differs from the exact manifest: %w", errInvalidConfig)
	}
	return remoteAssets, nil
}

func decodeRequiredJSONString(data []byte, purpose string) (string, error) {
	var value *string
	if err := decodeJSONDocument(data, &value, purpose); err != nil {
		return "", err
	}
	if value == nil || *value == "" {
		return "", fmt.Errorf("%s is not a nonempty string: %w", purpose, errInvalidConfig)
	}
	return *value, nil
}
