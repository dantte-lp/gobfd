//go:build interop && !interop_testcontainers

package interop_test

import (
	"context"
	"os"
	"os/exec" //nolint:depguard // Compose-mode tests retain the bounded direct Podman build backend.
	"path/filepath"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func buildInvalidVectorImage(ctx context.Context, t *testing.T) string {
	t.Helper()
	image := invalidVectorImageName()
	imageClient, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		t.Fatalf("create Podman client for invalid-vector image: %v", err)
	}
	var ownedID string
	if regErr := containertest.RegisterImageCleanup(ctx, image, image, "invalid-vector", &ownedID,
		imageClient, t.Cleanup, func(cleanupErr error) { t.Errorf("%v", cleanupErr) }); regErr != nil {
		t.Fatalf("register invalid-vector image ownership: %v", regErr)
	}
	t.Logf("invalid-vector image=%s", image)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve invalid-vector build root: %v", err)
	}
	revision, buildDate, err := interopproject.BuildMetadata(ctx, root, os.Getenv("GOBFD_BUILD_REVISION"))
	if err != nil {
		t.Fatalf("resolve invalid-vector build metadata: %v", err)
	}
	output, err := exec.CommandContext(ctx,
		"podman", "build",
		"--cpu-period", "100000", "--cpu-quota", "200000",
		"--memory", "2g", "--memory-swap", "2g", "--jobs", "1",
		"--build-arg", "VCS_REF="+revision, "--build-arg", "BUILD_DATE="+buildDate,
		"--build-arg", "GOBFD_IMAGE_RUN="+image, "--build-arg", "GOBFD_IMAGE_TEST=invalid-vector",
		"--label", "io.gobfd.test=invalid-vector", "--label", "io.gobfd.run="+image,
		"--label", "io.gobfd.image="+image,
		"-t", image, "-f", "scapy/Containerfile", root,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("podman build BFD invalid-vector generator: %v\n%s", err, output)
	}
	metadata, err := imageClient.ImageMetadata(ctx, image)
	if err != nil {
		t.Fatalf("inspect invalid-vector image: %v", err)
	}
	if err := containertest.ValidateImageOwner(metadata, image, image, "invalid-vector"); err != nil {
		t.Fatalf("validate invalid-vector image ownership: %v", err)
	}
	ownedID = metadata.ID
	t.Logf("invalid-vector image=%s id=%s", image, ownedID)
	return ownedID
}
