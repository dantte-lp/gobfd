//go:build !linux

package stepca

import (
	"context"
	"errors"
	"testing"
)

func TestRenewFileRejectsUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	changed, err := RenewFile(t.Context(), Options{})
	if changed || !errors.Is(err, errors.ErrUnsupported) || errors.Is(err, ErrCommitted) {
		t.Fatalf("RenewFile = %t, %v; want unchanged and ErrUnsupported", changed, err)
	}
}

func TestRenewFilePreservesCancellationOnUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	changed, err := RenewFile(ctx, Options{})
	if changed || !errors.Is(err, context.Canceled) || errors.Is(err, ErrCommitted) {
		t.Fatalf("RenewFile = %t, %v; want unchanged and context.Canceled", changed, err)
	}
}
