//go:build !linux

package toolbootstrap

import "os"

func composeDirectoryOwnership(os.FileInfo) (bool, bool) {
	return false, false
}
