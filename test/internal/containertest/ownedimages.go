package containertest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const cleanupTimeout = 45 * time.Second

type ownedImageClient interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	ImageMetadata(ctx context.Context, image string) (podmanapi.ImageMetadata, error)
	StageImages(ctx context.Context, runID, testKind string) ([]podmanapi.ImageMetadata, error)
	RemoveImage(ctx context.Context, image string) error
}

var errImageOwnership = errors.New("invalid image ownership")

// StageBuildArgs returns a cloned build-arg map stamped with run-owned stage metadata.
func StageBuildArgs(base map[string]*string, runID, testKind string) map[string]*string {
	args := make(map[string]*string, len(base)+2)
	maps.Copy(args, base)
	runCopy := runID
	kindCopy := testKind
	args["GOBFD_IMAGE_RUN"] = &runCopy
	args["GOBFD_IMAGE_TEST"] = &kindCopy
	return args
}

// ValidateImageOwner verifies the immutable ID and all three ownership labels.
func ValidateImageOwner(metadata podmanapi.ImageMetadata, imageName, runID, testKind string) error {
	if !IsImageDigest(metadata.ID) || runID == "" || testKind == "" || imageName == "" ||
		metadata.Labels["io.gobfd.test"] != testKind || metadata.Labels["io.gobfd.run"] != runID ||
		metadata.Labels["io.gobfd.image"] != imageName {
		return fmt.Errorf("refuse unowned or invalid image %s: %w", imageName, errImageOwnership)
	}
	return nil
}

// ValidateStageImageOwner verifies an untagged multi-stage builder image's immutable ID and stage labels.
func ValidateStageImageOwner(metadata podmanapi.ImageMetadata, runID, testKind string) error {
	if !IsImageDigest(metadata.ID) || runID == "" || testKind == "" ||
		metadata.Labels["io.gobfd.stage-run"] != runID ||
		metadata.Labels["io.gobfd.stage-test"] != testKind ||
		metadata.Labels["io.gobfd.image"] != "" {
		return fmt.Errorf("refuse unowned or invalid stage image %s: %w", metadata.ID, errImageOwnership)
	}
	return nil
}

// RegisterImageCleanup binds cleanup to an immutable ID, removes owned stage images, and preserves replaced tags.
func RegisterImageCleanup(
	ctx context.Context,
	imageName, runID, testKind string,
	ownedID *string,
	client ownedImageClient,
	register func(func()),
	report func(error),
) error {
	if ownedID == nil || runID == "" || testKind == "" || imageName == "" {
		return fmt.Errorf("image cleanup requires a run and identity holder: %w", errImageOwnership)
	}
	exists, err := client.ImageExists(ctx, imageName)
	if err != nil {
		return fmt.Errorf("inspect unique test-owned image %s before build: %w", imageName, err)
	}
	if exists {
		return fmt.Errorf("refuse ownership of pre-existing image %s: %w", imageName, errImageOwnership)
	}
	register(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		removeOwnedPrimaryImage(cleanupCtx, imageName, runID, testKind, ownedID, client, report)
		removeOwnedStageImages(cleanupCtx, runID, testKind, client, report)
	})
	return nil
}

func removeOwnedPrimaryImage(
	ctx context.Context,
	imageName, runID, testKind string,
	ownedID *string,
	client ownedImageClient,
	report func(error),
) {
	reference := *ownedID
	if reference == "" {
		reference = imageName
	}
	exists, inspectErr := client.ImageExists(ctx, reference)
	if inspectErr != nil {
		report(fmt.Errorf("inspect exact test-owned image %s during cleanup: %w", imageName, inspectErr))
		return
	}
	if !exists {
		return
	}
	metadata, metaErr := client.ImageMetadata(ctx, reference)
	if metaErr != nil {
		report(fmt.Errorf("inspect image ownership before cleanup: %w", metaErr))
		return
	}
	if ownerErr := ValidateImageOwner(metadata, imageName, runID, testKind); ownerErr != nil {
		report(ownerErr)
		return
	}
	if *ownedID != "" && metadata.ID != *ownedID {
		report(fmt.Errorf("image identity changed for %s: %w", imageName, errImageOwnership))
		return
	}
	*ownedID = metadata.ID
	if removeErr := client.RemoveImage(ctx, metadata.ID); removeErr != nil {
		report(fmt.Errorf("remove exact test-owned image %s: %w", imageName, removeErr))
	}
}

func removeOwnedStageImages(
	ctx context.Context,
	runID, testKind string,
	client ownedImageClient,
	report func(error),
) {
	stages, err := client.StageImages(ctx, runID, testKind)
	if err != nil {
		report(fmt.Errorf("list test-owned stage images for %s: %w", runID, err))
		return
	}
	var deferred []podmanapi.ImageMetadata
	for _, stage := range slices.Backward(stages) {
		if retry, stageErr := tryRemoveOwnedStage(ctx, stage.ID, runID, testKind, client); stageErr != nil {
			if retry && len(stages) > 1 {
				deferred = append(deferred, stage)
				continue
			}
			report(stageErr)
		}
	}
	for _, stage := range deferred {
		if _, stageErr := tryRemoveOwnedStage(ctx, stage.ID, runID, testKind, client); stageErr != nil {
			report(stageErr)
		}
	}
}

func tryRemoveOwnedStage(
	ctx context.Context,
	stageID, runID, testKind string,
	client ownedImageClient,
) (bool, error) {
	exists, existsErr := client.ImageExists(ctx, stageID)
	if existsErr != nil {
		return false, fmt.Errorf("inspect test-owned stage image %s before cleanup: %w", stageID, existsErr)
	}
	if !exists {
		return false, nil
	}
	metadata, metaErr := client.ImageMetadata(ctx, stageID)
	if metaErr != nil {
		return false, fmt.Errorf("inspect stage image ownership %s: %w", stageID, metaErr)
	}
	if ownerErr := ValidateStageImageOwner(metadata, runID, testKind); ownerErr != nil {
		return false, ownerErr
	}
	if metadata.ID != stageID {
		return false, fmt.Errorf("stage image identity changed for %s: %w", stageID, errImageOwnership)
	}
	if removeErr := client.RemoveImage(ctx, metadata.ID); removeErr != nil {
		return true, fmt.Errorf("remove exact test-owned stage image %s: %w", stageID, removeErr)
	}
	return false, nil
}
