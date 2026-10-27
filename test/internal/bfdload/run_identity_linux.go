//go:build linux

package bfdload

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	bootIDLength = 36
	bootIDGroups = 5
)

// RunIdentity binds the private shared control directory to one kernel boot.
// Binary/source and resource evidence remains in the existing controller receipts.
type RunIdentity struct {
	BootID string `json:"boot_id"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func controlRunIdentity(stat unix.Stat_t) (RunIdentity, error) {
	file, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return RunIdentity{}, fmt.Errorf("open workload boot identity: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxControlBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return RunIdentity{}, fmt.Errorf("read workload boot identity: %w", errors.Join(readErr, closeErr))
	}
	identity := RunIdentity{BootID: strings.TrimSpace(string(data)), Device: stat.Dev, Inode: stat.Ino}
	if !identity.valid() {
		return RunIdentity{}, fmt.Errorf("invalid workload boot/control identity: %w", ErrControl)
	}
	return identity, nil
}

func (r RunIdentity) valid() bool {
	if len(r.BootID) != bootIDLength || r.Inode == 0 {
		return false
	}
	groups := strings.Split(r.BootID, "-")
	if len(groups) != bootIDGroups {
		return false
	}
	lengths := [...]int{8, 4, 4, 4, 12}
	for index, group := range groups {
		if len(group) != lengths[index] {
			return false
		}
	}
	_, err := hex.DecodeString(strings.Join(groups, ""))
	return err == nil
}
