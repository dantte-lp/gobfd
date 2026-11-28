//go:build linux

package testutil

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// LockLoopbackBFD serializes fixed IPv6 BFD port probes across test processes.
func LockLoopbackBFD(tb testing.TB) {
	tb.Helper()
	namespace, err := os.Open("/proc/self/ns/net")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if closeErr := namespace.Close(); closeErr != nil {
			tb.Error(closeErr)
		}
	})
	// The namespace inode is shared across processes; closing the file releases the lock.
	if err := unix.Flock(int(namespace.Fd()), unix.LOCK_EX); err != nil {
		tb.Fatal(err)
	}
}
