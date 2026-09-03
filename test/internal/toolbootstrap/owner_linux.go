//go:build linux

package toolbootstrap

import (
	"os"
	"strconv"
	"syscall"
)

func composeDirectoryOwnership(info os.FileInfo) (bool, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, false
	}
	uid, err := strconv.ParseUint(strconv.Itoa(os.Geteuid()), 10, 32)
	if err != nil {
		return false, false
	}
	owner := uint64(stat.Uid)
	return owner == 0 || owner == uid, owner == uid
}
