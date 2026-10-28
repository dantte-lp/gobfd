//go:build !linux

package netio

import (
	"context"
	"fmt"
)

// rtnetlinkBondLinks has no rtnetlink outside Linux.
type rtnetlinkBondLinks struct{}

func (rtnetlinkBondLinks) Link(context.Context, string) (KernelBondLink, error) {
	return KernelBondLink{}, fmt.Errorf("kernel-bond rtnetlink: %w", ErrUnsupportedLAGActuatorBackend)
}

func (rtnetlinkBondLinks) SetMaster(context.Context, int, int) error {
	return fmt.Errorf("kernel-bond rtnetlink: %w", ErrUnsupportedLAGActuatorBackend)
}
