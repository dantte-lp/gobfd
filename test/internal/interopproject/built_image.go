package interopproject

import (
	"context"
	"fmt"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
)

// BuiltImage identifies one image produced under the current project lock.
type BuiltImage struct {
	Alias string `json:"alias"`
	ID    string `json:"id"`
}

// Image validates a built image by immutable ID before a lifecycle consumer uses it.
func (c *Controller) Image(ctx context.Context, service string) (BuiltImage, error) {
	if _, err := c.readyImageOverride(ctx); err != nil {
		return BuiltImage{}, err
	}
	image, exists := c.images.Owned.Images[service]
	if !exists {
		return BuiltImage{}, fmt.Errorf("%w: no built image for service %s", errControl, service)
	}
	metadata, err := c.inspectOwnedImage(ctx, image.ID)
	if err != nil {
		return BuiltImage{}, err
	}
	if ownerErr := containertest.ValidateImageOwner(
		metadata, image.Alias, c.images.Owned.Run, composeImageKind,
	); ownerErr != nil {
		return BuiltImage{}, fmt.Errorf("validate built service %s image ownership: %w", service, ownerErr)
	}
	if metadata.ID != image.ID {
		return BuiltImage{}, fmt.Errorf("%w: built image ID changed for service %s", errControl, service)
	}
	return BuiltImage(image), nil
}
