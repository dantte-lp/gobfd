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
	if err := validateRootPathIdentity(verifierRoot, root, "release verifier root before verification"); err != nil {
		return err
	}
	if err := validateRootPathIdentity(artifactRoot, artifactRootPath, "release artifact root before verification"); err != nil {
		return err
	}
	if err := validateRootPathIdentity(runnerTemp, runnerTempPath, "RUNNER_TEMP before release verification"); err != nil {
		return err
	}

	commitReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-commit.txt", "expected release commit receipt", releaseReceiptLimit,
	)
	if err != nil {
		return err
	}
	receiptCommit, err := parseCommandSHA(commitReceipt, "expected release commit receipt")
	if err != nil {
		return err
	}
	if receiptCommit != expectedCommit {
		return fmt.Errorf("release commit receipt differs from GITHUB_SHA: %w", errInvalidConfig)
	}
	branchReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-branch.txt", "expected release branch receipt", releaseReceiptLimit,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(branchReceipt, []byte(releaseBranch+"\n")) {
		return fmt.Errorf("release branch receipt differs from canonical tag branch: %w", errInvalidConfig)
	}
	tagReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-tag-object.txt", "expected release tag object receipt", releaseReceiptLimit,
	)
	if err != nil {
		return err
	}
	receiptTagObject, err := parseCommandSHA(tagReceipt, "expected release tag object receipt")
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
	if err := validateReleaseOCIDigestReceipt(digestReceipt, version); err != nil {
		return err
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
	if err := validateStrictJSONDocument(draftData, "release draft"); err != nil {
		return err
	}
	if err := validateExactReleaseDraft(draftData, options.RefName, releaseNotes, expectedAssets); err != nil {
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
	if err := downloadExactReleaseAssets(
		ctx, options.Runner, verifierRoot, runnerTemp, root, runnerTempPath,
		owner+"/"+repository, options.RefName, options.Environment, expectedAssets,
	); err != nil {
		return err
	}
	if err := validateRootPathIdentity(verifierRoot, root, "release verifier root after asset download"); err != nil {
		return err
	}
	if err := validateRootPathIdentity(artifactRoot, artifactRootPath, "release artifact root after asset download"); err != nil {
		return err
	}
	if err := validateRootPathIdentity(runnerTemp, runnerTempPath, "RUNNER_TEMP after asset download"); err != nil {
		return err
	}
	return nil
}

func validateExactReleaseDraft(data []byte, refName string, releaseNotes []byte, expectedAssets []string) error {
	fields, err := decodeRequiredJSONObject(data, "release draft fields", []string{"isDraft", "tagName", "body", "assets"})
	if err != nil {
		return err
	}
	var isDraft *bool
	if err := decodeJSONDocument(fields["isDraft"], &isDraft, "release draft state"); err != nil || isDraft == nil || !*isDraft {
		return fmt.Errorf("release is not an explicit draft: %w", errors.Join(err, errInvalidConfig))
	}
	tagName, err := decodeRequiredJSONString(fields["tagName"], "release draft tag")
	if err != nil {
		return err
	}
	if tagName != refName {
		return fmt.Errorf("release draft tag differs from canonical release tag: %w", errInvalidConfig)
	}
	body, err := decodeRequiredJSONString(fields["body"], "release draft body")
	if err != nil {
		return err
	}
	if strings.TrimRight(body, "\n") != strings.TrimRight(string(releaseNotes), "\n") {
		return fmt.Errorf("release draft body differs from release-notes.md: %w", errInvalidConfig)
	}
	assetFields := []map[string]json.RawMessage{}
	if err := decodeJSONDocument(fields["assets"], &assetFields, "release draft assets"); err != nil {
		return err
	}
	actualAssets := make([]string, 0, len(assetFields))
	for index, asset := range assetFields {
		if asset == nil {
			return fmt.Errorf("release draft asset %d is not an object: %w", index, errInvalidConfig)
		}
		if err := rejectJSONFieldAliases(asset, []string{"name"}); err != nil {
			return err
		}
		nameJSON, exists := asset["name"]
		if !exists {
			return fmt.Errorf("release draft asset %d lacks name: %w", index, errInvalidConfig)
		}
		name, err := decodeRequiredJSONString(nameJSON, "release draft asset name")
		if err != nil {
			return err
		}
		actualAssets = append(actualAssets, name)
	}
	sort.Strings(actualAssets)
	if !slices.Equal(actualAssets, expectedAssets) {
		return fmt.Errorf("release draft asset set differs from the exact manifest: %w", errInvalidConfig)
	}
	return nil
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
