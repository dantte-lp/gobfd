//go:build !linux

package netio

// CheckInterfaceAvailable accepts an omitted interface on every platform.
// Named BFD transport interfaces require Linux and are permanent failures on
// platforms without the Linux socket implementation.
func CheckInterfaceAvailable(ifName string) error {
	if ifName == "" {
		return nil
	}
	return unsupportedPlatform("check interface %q availability", ifName)
}
