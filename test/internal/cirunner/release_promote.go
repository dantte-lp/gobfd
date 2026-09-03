package cirunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const releaseOCIAliasRollbackTimeout = 10 * time.Minute

// PromoteReleaseOCIAliasesOptions supplies immutable release identity and receipt roots.
type PromoteReleaseOCIAliasesOptions struct {
	Root         string
	ArtifactRoot string
	RunnerTemp   string
	RefName      string
	SHA          string
	Repository   string
	Environment  []string
	Runner       SpecRunner
}

// PromoteReleaseOCIAliases moves stable OCI aliases to verified versioned index digests.
func PromoteReleaseOCIAliases(ctx context.Context, options PromoteReleaseOCIAliasesOptions) (returnErr error) {
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
		return fmt.Errorf("release OCI alias promotion command runner is required: %w", errInvalidConfig)
	}

	verifierRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open release verifier root for OCI alias promotion: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release verifier OCI alias root", verifierRoot.Close()))
	}()
	artifactRoot, err := os.OpenRoot(artifactRootPath)
	if err != nil {
		return fmt.Errorf("open release artifact root for OCI alias promotion: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release artifact OCI alias root", artifactRoot.Close()))
	}()
	runnerTemp, err := os.OpenRoot(runnerTempPath)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for OCI alias promotion: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close RUNNER_TEMP OCI alias root", runnerTemp.Close()))
	}()
	if rootsErr := validatePromotionRoots(
		verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "before identity verification",
	); rootsErr != nil {
		return rootsErr
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
		return fmt.Errorf("release tag object receipt changed before OCI alias promotion: %w", errInvalidConfig)
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
	if rootsErr := validatePromotionRoots(
		verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "before alias mutation",
	); rootsErr != nil {
		return rootsErr
	}

	const imageRepository = "ghcr.io/dantte-lp/gobfd"
	dockerEnvironment := withoutEnvironmentKeys(options.Environment, "GH_TOKEN", "GITHUB_TOKEN")
	aliases := []releaseOCIImageDigest{
		{Image: imageRepository + ":latest", Digest: versioned[0].Digest},
		{Image: imageRepository + ":debian-trixie", Digest: versioned[0].Digest},
		{Image: imageRepository + ":oraclelinux10", Digest: versioned[2].Digest},
	}
	snapshotTargets := make([]releaseOCIImageDigest, len(aliases))
	for index, alias := range aliases {
		snapshotTargets[index].Image = alias.Image
	}
	previousAliases, err := inspectReleaseOCIAliases(
		ctx, options.Runner, root, snapshotTargets, dockerEnvironment,
	)
	if err != nil {
		return fmt.Errorf("snapshot existing OCI aliases before promotion: %w", err)
	}
	if err := validatePromotionRoots(
		verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "after alias snapshot",
	); err != nil {
		return err
	}
	commands := []CommandSpec{
		{
			Name: "docker", Args: []string{
				"buildx", "imagetools", "create",
				"--tag", imageRepository + ":latest",
				"--tag", imageRepository + ":debian-trixie",
				imageRepository + "@" + versioned[0].Digest,
			}, Dir: root, Env: dockerEnvironment,
		},
		{
			Name: "docker", Args: []string{
				"buildx", "imagetools", "create",
				"--tag", imageRepository + ":oraclelinux10",
				imageRepository + "@" + versioned[2].Digest,
			}, Dir: root, Env: dockerEnvironment,
		},
	}
	promotionErr := func() error {
		for _, command := range commands {
			if err := options.Runner.RunCommand(ctx, command); err != nil {
				return fmt.Errorf("promote verified OCI alias: %w", err)
			}
		}
		_, err := inspectReleaseOCIAliases(ctx, options.Runner, root, aliases, dockerEnvironment)
		if err != nil {
			return err
		}
		return validatePromotionRoots(
			verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "after alias verification",
		)
	}()
	if promotionErr != nil {
		rollbackContext, cancelRollback := context.WithTimeout(
			context.WithoutCancel(ctx), releaseOCIAliasRollbackTimeout,
		)
		rollbackErr := errors.Join(
			restoreReleaseOCIAliases(
				rollbackContext, options.Runner, root, imageRepository, previousAliases, dockerEnvironment,
			),
			validatePromotionRoots(
				verifierRoot, root, artifactRoot, artifactRootPath, runnerTemp, runnerTempPath, "after alias rollback",
			),
		)
		cancelRollback()
		return errors.Join(promotionErr, wrapOptional("rollback OCI aliases after failed promotion", rollbackErr))
	}
	return nil
}

func inspectReleaseOCIAliases(
	ctx context.Context,
	runner SpecRunner,
	root string,
	expected []releaseOCIImageDigest,
	environment []string,
) ([]releaseOCIImageDigest, error) {
	actual := make([]releaseOCIImageDigest, 0, len(expected))
	for _, alias := range expected {
		digest, err := inspectReleaseOCIAliasDigest(ctx, runner, root, alias.Image, environment)
		if err != nil {
			return nil, err
		}
		if alias.Digest != "" && digest != alias.Digest {
			return nil, fmt.Errorf("%s does not reference the verified OCI index: %w", alias.Image, errInvalidConfig)
		}
		actual = append(actual, releaseOCIImageDigest{Image: alias.Image, Digest: digest})
	}
	return actual, nil
}

func restoreReleaseOCIAliases(
	ctx context.Context,
	runner SpecRunner,
	root string,
	imageRepository string,
	previous []releaseOCIImageDigest,
	environment []string,
) error {
	var result error
	for _, alias := range previous {
		if err := runner.RunCommand(ctx, CommandSpec{
			Name: "docker", Args: []string{
				"buildx", "imagetools", "create", "--tag", alias.Image,
				imageRepository + "@" + alias.Digest,
			}, Dir: root, Env: environment,
		}); err != nil {
			result = errors.Join(result, fmt.Errorf("restore OCI alias %s: %w", alias.Image, err))
		}
	}
	for _, alias := range previous {
		actual, err := inspectReleaseOCIAliasDigest(ctx, runner, root, alias.Image, environment)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("verify restored OCI alias %s: %w", alias.Image, err))
			continue
		}
		if actual != alias.Digest {
			result = errors.Join(
				result,
				fmt.Errorf("restored OCI alias %s has unexpected digest: %w", alias.Image, errInvalidConfig),
			)
		}
	}
	return result
}

func readExpectedReleaseIdentityReceipts(
	runnerTemp *os.Root,
	expectedCommit string,
	releaseBranch string,
) (string, error) {
	commitReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-commit.txt", "expected release commit receipt", releaseReceiptLimit,
	)
	if err != nil {
		return "", err
	}
	receiptCommit, err := parseCommandSHA(commitReceipt, "expected release commit receipt")
	if err != nil {
		return "", err
	}
	if receiptCommit != expectedCommit {
		return "", fmt.Errorf("release commit receipt differs from GITHUB_SHA: %w", errInvalidConfig)
	}
	branchReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-branch.txt", "expected release branch receipt", releaseReceiptLimit,
	)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(branchReceipt, []byte(releaseBranch+"\n")) {
		return "", fmt.Errorf("release branch receipt differs from canonical tag branch: %w", errInvalidConfig)
	}
	tagReceipt, err := readRootedRegularFile(
		runnerTemp, "expected-release-tag-object.txt", "expected release tag object receipt", releaseReceiptLimit,
	)
	if err != nil {
		return "", err
	}
	return parseCommandSHA(tagReceipt, "expected release tag object receipt")
}

func inspectReleaseOCIAliasDigest(
	ctx context.Context,
	runner SpecRunner,
	root string,
	image string,
	environment []string,
) (string, error) {
	data, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "docker", Args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}", image,
		}, Dir: root, Env: environment,
	}, "inspect promoted OCI alias "+image)
	if err != nil {
		return "", err
	}
	if validationErr := validateStrictJSONDocument(data, "promoted OCI alias "+image); validationErr != nil {
		return "", validationErr
	}
	if fieldsErr := validateOCIManifestJSONFields(data, image); fieldsErr != nil {
		return "", fieldsErr
	}
	index := ociManifestIndex{}
	if decodeErr := decodeJSONDocument(data, &index, "promoted OCI alias "+image); decodeErr != nil {
		return "", decodeErr
	}
	evidence, err := validateOCIManifestIndex(image, index)
	if err != nil {
		return "", err
	}
	return evidence.Digest, nil
}

func validatePromotionRoots(
	verifierRoot *os.Root,
	verifierPath string,
	artifactRoot *os.Root,
	artifactPath string,
	runnerTemp *os.Root,
	runnerTempPath string,
	phase string,
) error {
	return errors.Join(
		validateRootPathIdentity(verifierRoot, verifierPath, "release verifier root "+phase),
		validateRootPathIdentity(artifactRoot, artifactPath, "release artifact root "+phase),
		validateRootPathIdentity(runnerTemp, runnerTempPath, "RUNNER_TEMP "+phase),
	)
}
