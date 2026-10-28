package containertest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

type ownedImageClient interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	ImageMetadata(ctx context.Context, image string) (podmanapi.ImageMetadata, error)
	RemoveImage(ctx context.Context, image string) error
}

var errImageOwnership = errors.New("invalid image ownership")

// ValidateImageOwner verifies the immutable ID and all three ownership labels.
func ValidateImageOwner(metadata podmanapi.ImageMetadata, imageName, runID, testKind string) error {
	if !IsImageDigest(metadata.ID) || runID == "" || testKind == "" || imageName == "" ||
		metadata.Labels["io.gobfd.test"] != testKind || metadata.Labels["io.gobfd.run"] != runID ||
		metadata.Labels["io.gobfd.image"] != imageName {
		return fmt.Errorf("refuse unowned or invalid image %s: %w", imageName, errImageOwnership)
	}
	return nil
}

// RegisterImageCleanup binds cleanup to an immutable ID and preserves replaced tags.
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
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		reference := *ownedID
		if reference == "" {
			reference = imageName
		}
		exists, inspectErr := client.ImageExists(cleanupCtx, reference)
		if inspectErr != nil {
			report(fmt.Errorf("inspect exact test-owned image %s during cleanup: %w", imageName, inspectErr))
			return
		}
		if !exists {
			return
		}
		metadata, err := client.ImageMetadata(cleanupCtx, reference)
		if err != nil {
			report(fmt.Errorf("inspect image ownership before cleanup: %w", err))
			return
		}
		if err := ValidateImageOwner(metadata, imageName, runID, testKind); err != nil {
			report(err)
			return
		}
		if *ownedID != "" && metadata.ID != *ownedID {
			report(fmt.Errorf("image identity changed for %s: %w", imageName, errImageOwnership))
			return
		}
		*ownedID = metadata.ID
		if removeErr := client.RemoveImage(cleanupCtx, metadata.ID); removeErr != nil {
			report(fmt.Errorf("remove exact test-owned image %s: %w", imageName, removeErr))
		}
	})
	return nil
}
