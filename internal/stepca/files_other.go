//go:build !linux

package stepca

import (
	"context"
	"errors"
	"fmt"
)

// RenewFile rejects unsupported platforms without opening credentials or
// contacting the CA. Atomic credential replacement is qualified only on Linux.
func RenewFile(ctx context.Context, _ Options) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("renew certificate: %w", err)
	}
	return false, fmt.Errorf("certificate file renewal requires Linux: %w", errors.ErrUnsupported)
}
