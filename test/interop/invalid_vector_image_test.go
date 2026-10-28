//go:build interop && !interop_testcontainers

package interop_test

import (
	"context"
	"os"
	"os/exec" //nolint:depguard // Compose-mode tests retain the bounded direct Podman build backend.
	"path/filepath"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func buildInvalidVectorImage(ctx context.Context, t *testing.T) string {
	t.Helper()
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
		"-t", bfdFuzzImage, "-f", "scapy/Containerfile", root,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("podman build BFD invalid-vector generator: %v\n%s", err, output)
	}
	return bfdFuzzImage
}
