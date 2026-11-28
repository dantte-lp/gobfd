package netio

import (
	"errors"
	"fmt"
)

const (
	// linuxIFNAMSIZ matches Linux kernel IFNAMSIZ (include/uapi/linux/if.h).
	linuxIFNAMSIZ = 16
	// latin1NBSPByte matches Linux lib/ctype.c _ctype[0xa0] = _S|_SP used by
	// net/core/dev.c:dev_valid_name isspace(*name).
	latin1NBSPByte = 0xa0
)

// ErrInvalidInterfaceName indicates an interface identity that cannot be
// represented by the platform transport boundary.
var ErrInvalidInterfaceName = errors.New("invalid interface name")

// ValidateInterfaceName validates name against Linux kernel dev_valid_name
// rules: non-empty, shorter than IFNAMSIZ (16 bytes), not "." or "..", and
// containing no '/', ':', NUL, ASCII whitespace, or 0xa0 (NBSP) bytes.
func ValidateInterfaceName(name string) error {
	if name == "" || len(name) >= linuxIFNAMSIZ || name == "." || name == ".." {
		return fmt.Errorf("interface %q: %w", name, ErrInvalidInterfaceName)
	}
	for i := range len(name) {
		if isInvalidInterfaceNameByte(name[i]) {
			return fmt.Errorf("interface %q: %w", name, ErrInvalidInterfaceName)
		}
	}
	return nil
}

func isInvalidInterfaceNameByte(b byte) bool {
	switch b {
	case '/', ':', 0x00, ' ', '\t', '\n', '\v', '\f', '\r', latin1NBSPByte:
		return true
	default:
		return false
	}
}
