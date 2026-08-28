//go:build !linux

package netio

import (
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestCheckInterfaceAvailableIsExplicitOnUnsupportedPlatforms(t *testing.T) {
	t.Parallel()

	if err := CheckInterfaceAvailable(""); err != nil {
		t.Fatalf("empty optional interface: %v", err)
	}
	err := CheckInterfaceAvailable("en0")
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("non-empty interface error = %v, want ErrUnsupportedPlatform", err)
	}
	if errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("unsupported platform promoted to unavailable: %v", err)
	}
}
