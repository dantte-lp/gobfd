package interopproject

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const composeImageKind = "interop-compose"

func (c *Controller) inspectOwnedImage(ctx context.Context, reference string) (podmanapi.ImageMetadata, error) {
	const format = `{"id":{{json .ID}},"labels":{{json .Labels}}}`
	output, err := c.podmanText(ctx, commandTimeout, "image", "inspect", "--format", format, reference)
	if err != nil {
		return podmanapi.ImageMetadata{}, err
	}
	var wire struct {
		ID     string            `json:"id"`
		Labels map[string]string `json:"labels"`
	}
	if decodeErr := decodeImageJSON([]byte(output), &wire); decodeErr != nil {
		return podmanapi.ImageMetadata{}, decodeErr
	}
	// Podman CLI may emit a bare config digest; accept only an exact SHA-256 value.
	id := "sha256:" + strings.TrimPrefix(wire.ID, "sha256:")
	if !containertest.IsImageDigest(id) {
		return podmanapi.ImageMetadata{}, fmt.Errorf("%w: image inspection returned an invalid ID", errControl)
	}
	return podmanapi.ImageMetadata{ID: id, Labels: wire.Labels}, nil
}

func (c *Controller) checkpointImage(ctx context.Context, name string) error {
	image := c.images.Owned.Images[name]
	metadata, err := c.inspectOwnedImage(ctx, image.Alias)
	if err != nil {
		return err
	}
	if ownerErr := containertest.ValidateImageOwner(
		metadata, image.Alias, c.images.Owned.Run, composeImageKind,
	); ownerErr != nil {
		return fmt.Errorf("validate built Compose image %s ownership: %w", name, ownerErr)
	}
	image.ID = metadata.ID
	c.images.Owned.Images[name] = image
	c.images.Services[name] = composeOwnedService{Image: metadata.ID, PullPolicy: composePullPolicyNever}
	return c.saveImageState(false)
}

func (c *Controller) preflightOwnedImages(ctx context.Context) error {
	if err := c.loadImageState(ctx); err != nil || c.images == nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(c.images.Owned.Images)) {
		if preErr := c.preflightSingleOwnedImage(ctx, name); preErr != nil {
			return preErr
		}
	}
	return nil
}

func (c *Controller) preflightSingleOwnedImage(ctx context.Context, name string) error {
	image := c.images.Owned.Images[name]
	reference := image.ID
	if reference == "" {
		reference = image.Alias
	}
	exists, existsErr := c.objectExists(ctx, "image", reference)
	if existsErr != nil {
		return existsErr
	}
	if !exists {
		return nil
	}
	metadata, metaErr := c.inspectOwnedImage(ctx, reference)
	if metaErr != nil {
		return metaErr
	}
	if ownerErr := containertest.ValidateImageOwner(
		metadata, image.Alias, c.images.Owned.Run, composeImageKind,
	); ownerErr != nil {
		return fmt.Errorf("preflight owned Compose image %s: %w", name, ownerErr)
	}
	if image.ID != "" && image.ID != metadata.ID {
		return fmt.Errorf("%w: owned image ID changed for %s", errControl, name)
	}
	if image.ID == "" {
		image.ID = metadata.ID
		c.images.Owned.Images[name] = image
		c.images.Services[name] = composeOwnedService{Image: metadata.ID, PullPolicy: composePullPolicyNever}
		return c.saveImageState(false)
	}
	return nil
}

func (c *Controller) cleanupOwnedImages(ctx context.Context) error {
	if c.images == nil {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(c.images.Owned.Images)) {
		if rmErr := c.removeSingleOwnedImage(ctx, name); rmErr != nil {
			return rmErr
		}
	}
	if stageErr := c.cleanupOwnedStageImages(ctx, c.images.Owned.Run); stageErr != nil {
		return stageErr
	}
	return c.removeImageState()
}

func (c *Controller) removeSingleOwnedImage(ctx context.Context, name string) error {
	image := c.images.Owned.Images[name]
	if image.ID == "" {
		return nil
	}
	exists, existsErr := c.objectExists(ctx, "image", image.ID)
	if existsErr != nil {
		return existsErr
	}
	if !exists {
		return nil
	}
	metadata, metaErr := c.inspectOwnedImage(ctx, image.ID)
	if metaErr != nil {
		return metaErr
	}
	if ownerErr := containertest.ValidateImageOwner(
		metadata, image.Alias, c.images.Owned.Run, composeImageKind,
	); ownerErr != nil {
		return fmt.Errorf("verify Compose image %s ownership before removal: %w", name, ownerErr)
	}
	if metadata.ID != image.ID {
		return fmt.Errorf("%w: refuse changed image identity %s", errControl, image.ID)
	}
	if _, rmErr := c.podmanText(ctx, commandTimeout, "image", "rm", "--no-prune", image.ID); rmErr != nil {
		return rmErr
	}
	stillExists, verifyErr := c.objectExists(ctx, "image", image.ID)
	if verifyErr != nil {
		return verifyErr
	}
	if stillExists {
		return fmt.Errorf("%w: owned image %s remains after removal", errControl, image.ID)
	}
	return nil
}

func (c *Controller) cleanupOwnedStageImages(ctx context.Context, run string) error {
	if run == "" {
		return nil
	}
	output, err := c.podmanText(
		ctx,
		commandTimeout,
		"image", "ls", "--all", "--no-trunc",
		"--filter", "label=io.gobfd.stage-run="+run,
		"--filter", "label=io.gobfd.stage-test="+composeImageKind,
		"--format", "{{.ID}}",
	)
	if err != nil {
		return fmt.Errorf("list owned Compose stage images: %w", err)
	}
	stageIDs := lines(output)
	for _, rawID := range slices.Backward(stageIDs) {
		stageID := "sha256:" + strings.TrimPrefix(rawID, "sha256:")
		if !containertest.IsImageDigest(stageID) {
			return fmt.Errorf("%w: invalid Compose stage image ID %q", errControl, rawID)
		}
		if rmErr := c.removeSingleOwnedStageImage(ctx, stageID, run); rmErr != nil {
			return rmErr
		}
	}
	return nil
}

func (c *Controller) removeSingleOwnedStageImage(ctx context.Context, stageID, run string) error {
	exists, existsErr := c.objectExists(ctx, "image", stageID)
	if existsErr != nil {
		return existsErr
	}
	if !exists {
		return nil
	}
	metadata, metaErr := c.inspectOwnedImage(ctx, stageID)
	if metaErr != nil {
		return metaErr
	}
	if ownerErr := containertest.ValidateStageImageOwner(metadata, run, composeImageKind); ownerErr != nil {
		return fmt.Errorf("verify Compose stage image %s ownership: %w", stageID, ownerErr)
	}
	if metadata.ID != stageID {
		return fmt.Errorf("%w: refuse changed stage image identity %s", errControl, stageID)
	}
	if _, rmErr := c.podmanText(ctx, commandTimeout, "image", "rm", "--no-prune", stageID); rmErr != nil {
		return rmErr
	}
	stillExists, verifyErr := c.objectExists(ctx, "image", stageID)
	if verifyErr != nil {
		return verifyErr
	}
	if stillExists {
		return fmt.Errorf("%w: owned stage image %s remains after removal", errControl, stageID)
	}
	return nil
}

func (c *Controller) removeImageState() (returnErr error) {
	path, err := c.imageStatePath()
	if err != nil {
		return err
	}
	data, info, readErr := ReadPrivateReceipt(path)
	if readErr != nil {
		return readErr
	}
	if !os.SameFile(c.imageStateInfo, info) || c.imageStateHash != sha256.Sum256(data) {
		return fmt.Errorf("%w: image receipt changed before removal", ErrUnsafeReceipt)
	}
	root, openRootErr := os.OpenRoot(filepath.Dir(path))
	if openRootErr != nil {
		return fmt.Errorf("open image state parent directory: %w", openRootErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close image state parent root: %w", closeErr))
		}
	}()
	directory, openDirErr := root.Open(".")
	if openDirErr != nil {
		return fmt.Errorf("open image state directory handle: %w", openDirErr)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close image state directory handle: %w", closeErr))
		}
	}()
	if valErr := ValidatePrivateReceiptDirectory(directory, filepath.Dir(path)); valErr != nil {
		return valErr
	}
	current, inspectErr := InspectPrivateReceipt(root, filepath.Base(path))
	if inspectErr != nil || !os.SameFile(info, current) {
		return errors.Join(inspectErr, ErrUnsafeReceipt)
	}
	if rmErr := root.Remove(filepath.Base(path)); rmErr != nil {
		return fmt.Errorf("remove image state receipt: %w", rmErr)
	}
	if syncErr := directory.Sync(); syncErr != nil {
		return fmt.Errorf("sync image state directory after removal: %w", syncErr)
	}
	c.images, c.imageStateInfo = nil, nil
	c.imageStateHash, c.imageStateUncertain = [32]byte{}, false
	return nil
}

func (c *Controller) readyImageOverride(ctx context.Context) (string, error) {
	if err := c.loadImageState(ctx); err != nil {
		return "", err
	}
	if c.images == nil {
		return "", fmt.Errorf("%w: Compose start has no owned image receipt", errControl)
	}
	for name, image := range c.images.Owned.Images {
		if image.ID == "" {
			return "", fmt.Errorf("%w: service %s has no verified image ID", errControl, name)
		}
	}
	if c.requiresCaptureDirectory() {
		if err := c.validateCaptureDirectory(); err != nil {
			return "", fmt.Errorf("validate capture directory before launch: %w", err)
		}
	}
	return c.imageStatePath()
}

func (c *Controller) checkpointAfterBuild(ctx context.Context, name string) error {
	checkpointCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return c.checkpointImage(checkpointCtx, name)
}
