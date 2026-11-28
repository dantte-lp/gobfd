//go:build !linux

package netio

import "errors"

// CheckInterfaceAvailable accepts an omitted interface on every platform.
// Named BFD transport interfaces require Linux and are permanent failures on
// platforms without the Linux socket implementation.
func CheckInterfaceAvailable(ifName string) error {
	_, err := CheckInterfacesAvailable([]string{ifName})
	return err
}

// CheckInterfacesAvailable accepts omitted interfaces on every platform and
// reports each named dependency as a permanent unsupported-platform error.
func CheckInterfacesAvailable(ifNames []string) (int, error) {
	unsupportedErrors := make([]error, 0, len(ifNames))
	for _, ifName := range ifNames {
		if ifName != "" {
			unsupportedErrors = append(unsupportedErrors,
				unsupportedPlatform("check interface %q availability", ifName))
		}
	}
	return 0, errors.Join(unsupportedErrors...)
}
