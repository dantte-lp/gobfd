//go:build linux

package bfdload

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAddressAckPreservesKernelErrorsAndRejectsForeignReplies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		code     uint32
		sequence uint32
		pid      uint32
		matched  bool
		err      error
	}{
		{"success", 0, 21, 0, true, nil},
		{"permission", 0xfffffff3, 21, 0, true, unix.EACCES},
		{"exists", 0xffffffef, 21, 0, true, unix.EEXIST},
		{"positive_errno", 1, 21, 0, true, ErrTopology},
		{"other_sequence", 0, 22, 0, false, nil},
		{"userspace_reply", 0, 21, 42, false, ErrTopology},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := make([]byte, 20)
			binary.NativeEndian.PutUint32(wire[:4], 20)
			binary.NativeEndian.PutUint16(wire[4:6], 2)
			binary.NativeEndian.PutUint32(wire[8:12], tc.sequence)
			binary.NativeEndian.PutUint32(wire[16:20], tc.code)
			matched, err := addressAck(wire, &unix.SockaddrNetlink{Pid: tc.pid}, 21)
			if matched != tc.matched || !errors.Is(err, tc.err) {
				t.Fatalf("ACK interpreted as success, wrong request, or lost errno: matched=%t error=%v", matched, err)
			}
		})
	}
	if _, err := addressAck([]byte{0}, &unix.SockaddrNetlink{}, 21); err == nil {
		t.Fatal("truncated ACK accepted")
	}
}

func TestAddressAckWaitHonorsParentDeadlineWithoutMutation(t *testing.T) {
	t.Parallel()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Close(fd); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := readAddressAck(ctx, fd, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("empty ACK socket lost parent deadline: %v", err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("parent deadline replaced by the longer per-request timeout")
	}
}
