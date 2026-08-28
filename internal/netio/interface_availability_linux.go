//go:build linux

package netio

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type interfaceInventory func() ([]net.Interface, error)

// CheckInterfaceAvailable checks one optional Linux interface against the
// kernel inventory. A validated missing name is retryable; inventory failures
// and invalid names remain permanent errors.
func CheckInterfaceAvailable(ifName string) error {
	return checkInterfaceAvailable(ifName, net.Interfaces)
}

func checkInterfaceAvailable(ifName string, inventory interfaceInventory) error {
	if ifName == "" {
		return nil
	}
	if ifName == "." || ifName == ".." ||
		len(ifName) >= unix.IFNAMSIZ || strings.ContainsAny(ifName, "/:\x00 \t\n\v\f\r") {
		return fmt.Errorf("check interface availability: %w", ErrInvalidInterfaceName)
	}
	interfaces, err := inventory()
	if err != nil {
		return fmt.Errorf("list network interfaces: %w", err)
	}
	for _, iface := range interfaces {
		if iface.Name == ifName {
			return nil
		}
	}
	resourceErr := bfd.NewResourceUnavailableError(bfd.ResourceRef{
		Kind: bfd.ResourceKindInterface,
		ID:   ifName,
	})
	return fmt.Errorf("check interface %q availability: %w", ifName, resourceErr)
}
