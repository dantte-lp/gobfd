//go:build linux

package netio

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type interfaceInventory func() ([]net.Interface, error)

// CheckInterfaceAvailable checks one optional Linux interface against one
// kernel inventory snapshot.
func CheckInterfaceAvailable(ifName string) error {
	return checkInterfaceAvailable(ifName, net.Interfaces)
}

func checkInterfaceAvailable(ifName string, inventory interfaceInventory) error {
	_, err := checkInterfacesAvailable([]string{ifName}, inventory)
	return err
}

// CheckInterfacesAvailable checks a complete set of optional Linux interface
// claims against one kernel inventory snapshot. The returned count includes
// each unavailable claim, including duplicate names.
func CheckInterfacesAvailable(ifNames []string) (int, error) {
	return checkInterfacesAvailable(ifNames, net.Interfaces)
}

func checkInterfacesAvailable(ifNames []string, inventory interfaceInventory) (int, error) {
	validationErrors := make([]error, 0, len(ifNames))
	namedClaims := 0
	for _, ifName := range ifNames {
		if ifName == "" {
			continue
		}
		namedClaims++
		if !validAvailabilityInterfaceName(ifName) {
			validationErrors = append(validationErrors,
				fmt.Errorf("validate interface %q availability: %w", ifName, ErrInvalidInterfaceName))
		}
	}
	if err := errors.Join(validationErrors...); err != nil {
		return 0, err
	}
	if namedClaims == 0 {
		return 0, nil
	}

	interfaces, err := inventory()
	if err != nil {
		return 0, fmt.Errorf("list network interfaces: %w", err)
	}
	flagsByName := make(map[string]net.Flags, len(interfaces))
	for _, iface := range interfaces {
		flagsByName[iface.Name] = iface.Flags
	}

	unavailableErrors := make([]error, 0, namedClaims)
	for _, ifName := range ifNames {
		if ifName == "" {
			continue
		}
		flags, exists := flagsByName[ifName]
		if exists && flags&net.FlagUp != 0 && flags&net.FlagRunning != 0 {
			continue
		}
		resourceErr := bfd.NewResourceUnavailableError(bfd.ResourceRef{
			Kind: bfd.ResourceKindInterface,
			ID:   ifName,
		})
		unavailableErrors = append(unavailableErrors,
			fmt.Errorf("check interface %q availability: %w", ifName, resourceErr))
	}
	return len(unavailableErrors), errors.Join(unavailableErrors...)
}

func validAvailabilityInterfaceName(ifName string) bool {
	return ifName != "." && ifName != ".." &&
		len(ifName) < unix.IFNAMSIZ && !strings.ContainsAny(ifName, "/:\x00 \t\n\v\f\r")
}
