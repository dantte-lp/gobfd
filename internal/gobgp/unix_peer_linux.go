//go:build linux

package gobgp

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type unixTransportIdentity struct {
	peerPID       int32
	peerUID       uint32
	socketDev     uint64
	socketIno     uint64
	bootID        string
	procStartTime uint64
}

func captureUnixTransportIdentity(conn net.Conn, socketPath string) (unixTransportIdentity, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return unixTransportIdentity{}, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return unixTransportIdentity{}, false
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil || cred.Pid <= 0 {
		return unixTransportIdentity{}, false
	}

	id := unixTransportIdentity{
		peerPID:       cred.Pid,
		peerUID:       cred.Uid,
		bootID:        readKernelBootID(),
		procStartTime: readProcStartTime(cred.Pid),
	}
	if info, statErr := os.Stat(socketPath); statErr == nil {
		if st, stOK := info.Sys().(*syscall.Stat_t); stOK {
			id.socketDev = st.Dev
			id.socketIno = st.Ino
		}
	}
	return id, true
}

func readKernelBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readProcStartTime reads field 22 (starttime in clock ticks after boot) from
// /proc/<pid>/stat. Returns 0 when /proc hides non-owned processes (for example
// under systemd ProtectProc=invisible).
func readProcStartTime(pid int32) uint64 {
	if pid <= 0 {
		return 0
	}
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath) // #nosec G304 -- pid is kernel-reported SO_PEERCRED int32.
	if err != nil {
		return 0
	}
	line := string(data)
	closeParen := strings.LastIndexByte(line, ')')
	if closeParen < 0 || closeParen+2 >= len(line) {
		return 0
	}
	// Field 3 (state) starts right after ") "; starttime (field 22) is index 19 in rest.
	fields := strings.Fields(line[closeParen+2:])
	const startTimeFieldOffset = 19
	if len(fields) <= startTimeFieldOffset {
		return 0
	}
	ticks, err := strconv.ParseUint(fields[startTimeFieldOffset], 10, 64)
	if err != nil {
		return 0
	}
	return ticks
}
