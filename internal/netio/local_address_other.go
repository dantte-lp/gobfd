//go:build !linux

package netio

// CheckLocalAddressesAvailable requires the Linux bind and inventory boundary.
func CheckLocalAddressesAvailable(claims []LocalAddress) (int, error) {
	if len(claims) == 0 {
		return 0, nil
	}
	return 0, unsupportedPlatform("inspect local address availability")
}
