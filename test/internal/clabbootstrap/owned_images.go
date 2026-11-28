package clabbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const (
	ownedCleanupTimeout     = time.Minute
	managedApplicationCount = 2
)

func validOwnedReceipt(receipt lifecycleReceipt) bool {
	if !networkRunRE.MatchString(receipt.RunID) || len(receipt.RunID) > 48 || !validReceiptImages(receipt) {
		return false
	}
	if receipt.Topology == "" {
		return len(receipt.Containers) == 0 && len(receipt.ExpectedContainers) == 0 && receipt.Network == (lifecycleNetwork{})
	}
	if !filepath.IsAbs(receipt.Topology) || receipt.Network.Name != networkName(receipt.RunID) ||
		(receipt.Network.ID != "" && !networkIDRE.MatchString(receipt.Network.ID)) {
		return false
	}
	return validExpectedContainers(receipt)
}

func validReceiptImages(receipt lifecycleReceipt) bool {
	return receipt.Image.Reference == gobfdImageRepo+":"+receipt.RunID &&
		receipt.FRR.Reference == frrImageRepo+":"+receipt.RunID &&
		(receipt.Image.ID == "" || validImageID(receipt.Image.ID)) &&
		(receipt.FRR.ID == "" || validImageID(receipt.FRR.ID))
}

func validExpectedContainers(receipt lifecycleReceipt) bool {
	if !slices.Contains(receipt.ExpectedContainers, gobfdContainer) ||
		!slices.Contains(receipt.ExpectedContainers, frrContainer) {
		return false
	}
	known := []string{
		gobfdContainer, frrContainer, "clab-" + labName + "-arista", "clab-" + labName + "-nokia",
		"clab-" + labName + "-cisco", "clab-" + labName + "-sonic", "clab-" + labName + "-vyos",
	}
	seen := make(map[string]bool, len(receipt.ExpectedContainers))
	for _, name := range receipt.ExpectedContainers {
		if !slices.Contains(known, name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	for name, id := range receipt.Containers {
		if !seen[name] || id == "" {
			return false
		}
	}
	return true
}

func applicationImages(receipt lifecycleReceipt) ([]lifecycleImage, error) {
	if !validLifecycleReceipt(receipt) {
		return nil, fmt.Errorf("invalid application image receipt: %w", errLifecycleState)
	}
	switch receipt.SchemaVersion {
	case legacyReceiptV1:
		return nil, nil
	case ownedReceiptV2:
		return []lifecycleImage{receipt.Image}, nil
	case ownedReceiptV3:
		return []lifecycleImage{receipt.Image, receipt.FRR}, nil
	default:
		return nil, fmt.Errorf("unsupported image receipt schema: %w", errLifecycleState)
	}
}

func validateCleanupImages(
	ctx context.Context, runner Runner, images []lifecycleImage, runID string,
) ([]string, error) {
	ids := make([]string, len(images))
	for index, image := range images {
		id, present, err := validateOwnedImageForCleanup(ctx, runner, image, runID)
		if err != nil {
			return nil, err
		}
		if present {
			ids[index] = id
		}
	}
	return ids, nil
}

func stageOwnedImages(ctx context.Context, options Options, runner Runner) (lifecycleReceipt, error) {
	if options.lifecycleLockHeld {
		return stageOwnedImagesLocked(ctx, options, runner)
	}
	lock, err := acquireLifecycleLock(options.ProjectRoot)
	if err != nil {
		return lifecycleReceipt{}, err
	}
	receipt, stageErr := stageOwnedImagesLocked(ctx, options, runner)
	return receipt, errors.Join(stageErr, releaseLifecycleLock(lock))
}

func stageOwnedImagesLocked(ctx context.Context, options Options, runner Runner) (lifecycleReceipt, error) {
	if options.SkipBuild {
		receipt, err := loadLifecycleReceipt(options.ProjectRoot)
		if err != nil {
			return lifecycleReceipt{}, err
		}
		if receipt.SchemaVersion != ownedReceiptV3 || receipt.Topology != "" || len(receipt.Containers) != 0 {
			return lifecycleReceipt{}, fmt.Errorf("application images are not staged: %w", errLifecycleState)
		}
		return receipt, validateApplicationImages(ctx, runner, receipt)
	}
	return buildStagedImages(ctx, options, runner)
}

func newImageReceipt(runID string) lifecycleReceipt {
	return lifecycleReceipt{
		SchemaVersion: ownedReceiptV3, RunID: runID, Containers: make(map[string]string),
		Image: lifecycleImage{Reference: gobfdImageRepo + ":" + runID},
		FRR:   lifecycleImage{Reference: frrImageRepo + ":" + runID},
	}
}

//nolint:nonamedreturns // Rollback preserves ownership and joins cleanup failures with the build error.
func buildStagedImages(
	ctx context.Context, options Options, runner Runner,
) (receipt lifecycleReceipt, returnErr error) {
	if _, err := os.Lstat(receiptPath(options.ProjectRoot)); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return receipt, fmt.Errorf("vendor receipt exists; run interop-clab-down: %w", errLifecycleState)
		}
		return receipt, fmt.Errorf("inspect vendor receipt: %w", err)
	}
	receipt = newImageReceipt(fmt.Sprintf("%d-%d", time.Now().UTC().UnixNano(), os.Getpid()))
	for _, image := range []lifecycleImage{receipt.Image, receipt.FRR} {
		exists, err := imageExists(ctx, options, runner, image.Reference)
		if err != nil {
			return receipt, err
		}
		if exists {
			return receipt, fmt.Errorf("application image %s already exists: %w", image.Reference, errLifecycleState)
		}
	}
	revision, date, err := interopproject.BuildMetadata(ctx, options.ProjectRoot, os.Getenv("GOBFD_BUILD_REVISION"))
	if err != nil {
		return receipt, fmt.Errorf("resolve application image build metadata: %w", err)
	}
	if err := createLifecycleReceipt(options.ProjectRoot, receipt); err != nil {
		return receipt, err
	}
	defer func() {
		if returnErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ownedCleanupTimeout)
			defer cancel()
			returnErr = errors.Join(returnErr, cleanupTopology(cleanupCtx, options, runner, receipt))
		}
	}()
	images := []*lifecycleImage{&receipt.Image, &receipt.FRR}
	for index, command := range applicationBuildCommands(options, receipt, revision, date) {
		if err := runCommand(ctx, runner, command); err != nil {
			return receipt, fmt.Errorf("build application image %s: %w", images[index].Reference, err)
		}
		id, err := inspectOwnedImage(ctx, runner, images[index].Reference, receipt.RunID)
		if err != nil {
			return receipt, err
		}
		images[index].ID = id
		if err := writeLifecycleReceipt(options.ProjectRoot, receipt); err != nil {
			return receipt, err
		}
	}
	return receipt, validateApplicationImages(ctx, runner, receipt)
}

func applicationBuildCommands(options Options, receipt lifecycleReceipt, revision, date string) []Command {
	frrContext := filepath.Join(options.ProjectRoot, "test", "interop", "frr")
	builds := []struct {
		image, context, file string
	}{
		{
			receipt.Image.Reference, options.ProjectRoot,
			filepath.Join(options.ProjectRoot, "test", "interop-clab", "Containerfile.gobfd"),
		},
		{receipt.FRR.Reference, frrContext, filepath.Join(frrContext, "Containerfile")},
	}
	commands := make([]Command, 0, len(builds))
	for _, build := range builds {
		commands = append(commands, Command{
			Executable: executablePodman, DryRun: options.DryRun,
			Arguments: []string{
				"build", "--cpu-period", "100000", "--cpu-quota", "200000",
				"--memory", "2g", "--memory-swap", "2g", "--jobs", "1", "--force-rm",
				labelOption, ownerLabel + "=" + labName, labelOption, runLabel + "=" + receipt.RunID,
				"--build-arg", "VCS_REF=" + revision, "--build-arg", "BUILD_DATE=" + date,
				"-t", build.image, "-f", build.file, build.context,
			},
		})
	}
	return commands
}

func validateApplicationImages(ctx context.Context, runner Runner, receipt lifecycleReceipt) error {
	if !validLifecycleReceipt(receipt) || receipt.SchemaVersion != ownedReceiptV3 || !validImageID(receipt.Image.ID) ||
		!validImageID(receipt.FRR.ID) ||
		strings.TrimPrefix(receipt.Image.ID, "sha256:") == strings.TrimPrefix(receipt.FRR.ID, "sha256:") {
		return fmt.Errorf("two distinct application images are required: %w", errLifecycleState)
	}
	for _, image := range []lifecycleImage{receipt.Image, receipt.FRR} {
		if _, err := validateOwnedImage(ctx, runner, image, receipt.RunID); err != nil {
			return err
		}
	}
	return nil
}

func validImageID(id string) bool {
	value := strings.TrimPrefix(id, "sha256:")
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateOwnedImage(ctx context.Context, runner Runner, image lifecycleImage, runID string) (string, error) {
	exists, err := imageReferenceExists(ctx, runner, image.Reference)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("owned image %s is missing: %w", image.Reference, errLifecycleState)
	}
	id, err := inspectOwnedImage(ctx, runner, image.Reference, runID)
	if err != nil {
		return "", err
	}
	if image.ID != "" && id != image.ID {
		return "", fmt.Errorf("owned image %s changed identity: %w", image.Reference, errLifecycleState)
	}
	return id, nil
}

func validateOwnedImageForCleanup(
	ctx context.Context, runner Runner, image lifecycleImage, runID string,
) (string, bool, error) {
	exists, err := imageReferenceExists(ctx, runner, image.Reference)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, verifyOwnedImageAbsent(ctx, runner, image, image.ID)
	}
	id, err := validateOwnedImage(ctx, runner, image, runID)
	return id, err == nil, err
}

func inspectOwnedImage(ctx context.Context, runner Runner, reference, runID string) (string, error) {
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"image", podmanInspect, networkFormatOption, inspectImageOwnershipFormat, reference},
	})
	if err != nil {
		return "", fmt.Errorf("inspect owned image %s: %w", reference, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("inspect owned image %s: exit %d: %w", reference, result.ExitCode, ErrBootstrapFailed)
	}
	fields := strings.Split(strings.TrimSpace(result.Stdout), "|")
	if len(fields) != 3 || !validImageID(fields[0]) || fields[1] != labName || fields[2] != runID {
		return "", fmt.Errorf("image %s is not owned by run %s: %w", reference, runID, errLifecycleState)
	}
	return fields[0], nil
}

func imageReferenceExists(ctx context.Context, runner Runner, reference string) (bool, error) {
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman, Arguments: []string{"image", "exists", reference},
	})
	if err != nil {
		return false, fmt.Errorf("inspect owned image %s: %w", reference, err)
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("inspect owned image %s: exit %d: %w", reference, result.ExitCode, ErrBootstrapFailed)
	}
}

func verifyOwnedImageAbsent(ctx context.Context, runner Runner, image lifecycleImage, imageID string) error {
	for _, reference := range []string{imageID, image.Reference} {
		if reference == "" {
			continue
		}
		exists, err := imageReferenceExists(ctx, runner, reference)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("owned image %s remains present: %w", reference, errLifecycleState)
		}
	}
	return nil
}
