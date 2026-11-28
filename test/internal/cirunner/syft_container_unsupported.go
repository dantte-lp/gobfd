//go:build !linux

package cirunner

import (
	"context"
	"fmt"
)

// ReleaseSyftCompiler requires the Linux hosted runner used by the release job.
func ReleaseSyftCompiler(context.Context, ReleaseSyftOptions) error {
	return fmt.Errorf("syft compiler container requires a Linux host: %w", errInvalidConfig)
}

// ReleaseSourceToolsCompiler requires the Linux hosted runner used by the release job.
func ReleaseSourceToolsCompiler(context.Context, ReleaseSyftOptions) error {
	return fmt.Errorf("source compiler containers require a Linux host: %w", errInvalidConfig)
}
