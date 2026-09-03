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

// VerifyReleaseDraft revalidates release identity, OCI receipts, and the exact draft manifest.
func VerifyReleaseDraft(ctx context.Context, options VerifyReleaseDraftOptions) (returnErr error) {
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
		return fmt.Errorf("release draft verification command runner is required: %w", errInvalidConfig)
	}

	verifierRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open release verifier root for draft verification: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release verifier draft root", verifierRoot.Close()))
	}()
	artifactRoot, err := os.OpenRoot(artifactRootPath)
	if err != nil {
		return fmt.Errorf("open release artifact root for verification: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release artifact verification root", artifactRoot.Close()))
	}()
	runnerTemp, err := os.OpenRoot(runnerTempPath)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for release verification: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close RUNNER_TEMP release verification root", runnerTemp.Close()))
	}()
	if identityErr := validateRootPathIdentity(
		verifierRoot, root, "release verifier root before verification",
	); identityErr != nil {
		return identityErr
	}
	if identityErr := validateRootPathIdentity(
		artifactRoot, artifactRootPath, "release artifact root before verification",
	); identityErr != nil {
		return identityErr
	}
	if identityErr := validateRootPathIdentity(
		runnerTemp, runnerTempPath, "RUNNER_TEMP before release verification",
	); identityErr != nil {
		return identityErr
	}

	receiptTagObject, err := readExpectedReleaseIdentityReceipts(runnerTemp, expectedCommit, releaseBranch)
	if err != nil {
		return err
	}

	actualTagObject, err := verifyReleaseGitIdentity(
		ctx, options.Runner, root, owner, repository, options.RefName, releaseBranch, expectedCommit, options.Environment,
	)
	if err != nil {
		return err
	}
	if actualTagObject != receiptTagObject {
		return fmt.Errorf("release tag object receipt changed after preflight: %w", errInvalidConfig)
	}

	digestReceipt, err := readRootedRegularFile(
		artifactRoot, "release-image-digests.txt", "release OCI digest receipt", releaseOCIDigestReceiptLimit,
	)
	if err != nil {
		return err
	}
	if digestErr := validateReleaseOCIDigestReceipt(digestReceipt, version); digestErr != nil {
		return digestErr
	}
	ociEvidence, err := inspectReleaseOCIManifests(
		ctx, options.Runner, root, options.RefName, options.Environment,
	)
	if err != nil {
		return err
	}
	if actual := renderReleaseOCIDigestReceipt(ociEvidence); !bytes.Equal(actual, digestReceipt) {
		return fmt.Errorf("release OCI digest receipt changed after evidence recording: %w", errInvalidConfig)
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
	draftData, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			"release", "view", options.RefName, "--repo", owner + "/" + repository,
			"--json", "isDraft,tagName,body,assets",
		},
		Dir: root, Env: options.Environment,
	}, "read exact release draft")
	if err != nil {
		return err
	}
	if validationErr := validateStrictJSONDocument(draftData, "release draft"); validationErr != nil {
		return validationErr
	}
	remoteAssets, err := validateExactReleaseDraft(
		draftData, options.RefName, owner+"/"+repository, releaseNotes, expectedAssets,
	)
	if err != nil {
		return err
	}
	if err := validateRootPathIdentity(verifierRoot, root, "release verifier root after verification"); err != nil {
		return err
	}
	if err := validateRootPathIdentity(artifactRoot, artifactRootPath, "release artifact root after verification"); err != nil {
		return err
	}
	if err := validateRootPathIdentity(runnerTemp, runnerTempPath, "RUNNER_TEMP after release verification"); err != nil {
		return err
	}
	localAssets := make(map[string]releaseAssetSnapshot, len(expectedAssets))
	var identityReceipt []byte
	if err := downloadExactReleaseAssets(
		ctx, options.Runner, verifierRoot, runnerTemp, root, runnerTempPath,
		owner+"/"+repository, options.RefName, options.Environment, expectedAssets,
		func(downloadRoot *os.Root) error {
			contentErr := validateReleaseAssetContentsWithEvidence(
				downloadRoot, artifactRoot, runnerTemp, version, options.RefName, localAssets,
			)
			var identityErr error
			if contentErr == nil {
				identityReceipt, identityErr = renderReleaseAssetIdentityReceipt(
					options.RefName, remoteAssets, localAssets,
				)
			}
			return errors.Join(
				contentErr,
				identityErr,
				validateRootPathIdentity(
					artifactRoot, artifactRootPath, "release artifact root after asset content verification",
				),
			)
		},
		func() error {
			return writeAndVerifyReleaseAssetIdentityReceipt(
				runnerTemp, runnerTempPath, identityReceipt,
			)
		},
	); err != nil {
		return err
	}
	return nil
}

func writeAndVerifyReleaseAssetIdentityReceipt(
	runnerTemp *os.Root,
	runnerTempPath string,
	identityReceipt []byte,
) (returnErr error) {
	if err := validateRootPathIdentity(runnerTemp, runnerTempPath, "RUNNER_TEMP before asset identity receipt"); err != nil {
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
	if err := validateRootPathIdentity(runnerTemp, runnerTempPath, "RUNNER_TEMP after asset identity receipt"); err != nil {
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
