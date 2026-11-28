//go:build linux

package netio

import (
	"context"
	"encoding/binary"
	"log/slog"
	"syscall"
	"testing"
	"testing/synctest"

	"golang.org/x/sys/unix"
)

func TestInterfaceMonitorOverflowRequestsResync(t *testing.T) {
	t.Parallel()
	mon := &LinuxInterfaceMonitor{events: make(chan InterfaceEvent, 1), logger: slog.Default()}
	keepReading, err := mon.handleReceiveError(context.Background(), unix.ENOBUFS)
	if err != nil || !keepReading {
		t.Fatalf("overflow result = %v, %v", keepReading, err)
	}
	select {
	case ev := <-mon.Events():
		if !ev.Resync || ev.IfName != "" {
			t.Fatalf("overflow requested only one interface: %+v", ev)
		}
	default:
		t.Fatal("netlink overflow did not request interface resynchronization")
	}
}

func TestInterfaceMonitorResyncPrecedesContinuedTraffic(t *testing.T) {
	t.Parallel()
	mon := &LinuxInterfaceMonitor{events: make(chan InterfaceEvent, 1), logger: slog.New(slog.DiscardHandler)}
	mon.events <- InterfaceEvent{IfName: "first0"}
	if _, err := mon.handleReceiveError(context.Background(), unix.ENOBUFS); err != nil {
		t.Fatalf("overflow: %v", err)
	}
	<-mon.events
	msg := makeLinkMessage(t, syscall.RTM_NEWLINK, "unrelated0", uint32(syscall.IFF_UP|syscall.IFF_RUNNING))
	buf := make([]byte, syscall.NLMSG_HDRLEN+len(msg.Data))
	binary.NativeEndian.PutUint32(buf[:4], uint32(len(buf)))
	binary.NativeEndian.PutUint16(buf[4:6], syscall.RTM_NEWLINK)
	copy(buf[syscall.NLMSG_HDRLEN:], msg.Data)
	if err := mon.handleNetlinkBuffer(buf); err != nil {
		t.Fatalf("continued traffic: %v", err)
	}
	select {
	case ev := <-mon.events:
		if !ev.Resync {
			t.Fatalf("ordinary traffic starved pending resync: %+v", ev)
		}
	default:
		t.Fatal("pending resync not delivered during continued traffic")
	}
}

func TestLinkEventFromNetlinkMessageNewLink(t *testing.T) {
	t.Parallel()

	msg := makeLinkMessage(t, syscall.RTM_NEWLINK, "eth0",
		uint32(syscall.IFF_UP|syscall.IFF_RUNNING))

	ev, ok, err := linkEventFromNetlinkMessage(msg)
	if err != nil {
		t.Fatalf("linkEventFromNetlinkMessage: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if ev.IfName != "eth0" {
		t.Fatalf("IfName = %q, want eth0", ev.IfName)
	}
	if ev.IfIndex != 7 {
		t.Fatalf("IfIndex = %d, want 7", ev.IfIndex)
	}
	if !ev.Up {
		t.Fatal("Up = false, want true")
	}
}

func TestLinkEventFromNetlinkMessageDownWhenNotRunning(t *testing.T) {
	t.Parallel()

	msg := makeLinkMessage(t, syscall.RTM_NEWLINK, "eth1", uint32(syscall.IFF_UP))

	ev, ok, err := linkEventFromNetlinkMessage(msg)
	if err != nil {
		t.Fatalf("linkEventFromNetlinkMessage: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if ev.Up {
		t.Fatal("Up = true, want false")
	}
}

func TestLinkEventFromNetlinkMessageDelLink(t *testing.T) {
	t.Parallel()

	msg := makeLinkMessage(t, syscall.RTM_DELLINK, "eth2",
		uint32(syscall.IFF_UP|syscall.IFF_RUNNING))

	ev, ok, err := linkEventFromNetlinkMessage(msg)
	if err != nil {
		t.Fatalf("linkEventFromNetlinkMessage: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if ev.Up {
		t.Fatal("Up = true, want false for RTM_DELLINK")
	}
}

func makeLinkMessage(t *testing.T, msgType uint16, ifName string, flags uint32) syscall.NetlinkMessage {
	t.Helper()

	data := make([]byte, 0, syscall.SizeofIfInfomsg+rtAttrAlignedLen(len(ifName)+1))
	data = append(data, make([]byte, syscall.SizeofIfInfomsg)...)
	binary.NativeEndian.PutUint16(data[2:4], 1)
	binary.NativeEndian.PutUint32(data[4:8], 7)
	binary.NativeEndian.PutUint32(data[8:12], flags)
	binary.NativeEndian.PutUint32(data[12:16], ^uint32(0))
	data = append(data, makeRtAttr(syscall.IFLA_IFNAME, append([]byte(ifName), 0))...)

	return syscall.NetlinkMessage{
		Header: syscall.NlMsghdr{
			Type: msgType,
		},
		Data: data,
	}
}

func makeRtAttr(attrType uint16, value []byte) []byte {
	attrLen := syscall.SizeofRtAttr + len(value)
	alignedLen := rtAttrAlignedLen(len(value))
	buf := make([]byte, alignedLen)
	binary.NativeEndian.PutUint16(buf[0:2], uint16(attrLen))
	binary.NativeEndian.PutUint16(buf[2:4], attrType)
	copy(buf[syscall.SizeofRtAttr:], value)
	return buf
}

func rtAttrAlignedLen(valueLen int) int {
	return (syscall.SizeofRtAttr + valueLen + 3) &^ 3
}

func TestInterfaceMonitorFullQueueRetainsResync(t *testing.T) {
	t.Parallel()
	mon := &LinuxInterfaceMonitor{events: make(chan InterfaceEvent, 1), logger: slog.New(slog.DiscardHandler)}
	mon.events <- InterfaceEvent{IfName: "first0", Up: true}
	msg := makeLinkMessage(t, syscall.RTM_DELLINK, "lost0", 0)
	buf := make([]byte, syscall.NLMSG_HDRLEN+len(msg.Data))
	binary.NativeEndian.PutUint32(buf[:4], uint32(len(buf)))
	binary.NativeEndian.PutUint16(buf[4:6], syscall.RTM_DELLINK)
	copy(buf[syscall.NLMSG_HDRLEN:], msg.Data)
	if err := mon.handleNetlinkBuffer(buf); err != nil {
		t.Fatalf("handleNetlinkBuffer: %v", err)
	}
	<-mon.events
	if keepReading, err := mon.handleReceiveError(context.Background(), unix.EAGAIN); err != nil || !keepReading {
		t.Fatalf("timeout result = %v, %v", keepReading, err)
	}
	select {
	case ev := <-mon.events:
		if !ev.Resync {
			t.Fatalf("lost event did not trigger complete resync: %+v", ev)
		}
	default:
		t.Fatal("resync was lost while the channel was full")
	}
	if _, err := mon.handleReceiveError(context.Background(), unix.EAGAIN); err != nil {
		t.Fatalf("repeat timeout: %v", err)
	}
	select {
	case ev := <-mon.events:
		t.Fatalf("resync replayed after successful delivery: %+v", ev)
	default:
	}
}

func TestInterfaceMonitorRunJoinsCancellationWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mon := &LinuxInterfaceMonitor{fd: -1, events: make(chan InterfaceEvent, 1), logger: slog.New(slog.DiscardHandler)}
		if err := mon.Run(context.Background()); err != nil {
			t.Fatalf("Run after socket close: %v", err)
		}
		if _, ok := <-mon.events; ok {
			t.Fatal("monitor returned without closing events")
		}
	})
}

// An address added to or removed from an interface is a distinct event
// kind: it carries no link state and must not flap sessions.
func TestAddressEventFromNetlinkMessage(t *testing.T) {
	t.Parallel()

	for _, msgType := range []uint16{syscall.RTM_NEWADDR, syscall.RTM_DELADDR} {
		data := make([]byte, syscall.SizeofIfAddrmsg)
		data[0] = syscall.AF_INET
		binary.NativeEndian.PutUint32(data[4:8], 9)
		ev, ok, err := linkEventFromNetlinkMessage(syscall.NetlinkMessage{
			Header: syscall.NlMsghdr{Type: msgType}, Data: data,
		})
		if err != nil || !ok {
			t.Fatalf("type %d: ok=%v err=%v, want address event", msgType, ok, err)
		}
		if !ev.Address || ev.IfIndex != 9 || ev.Up || ev.Resync {
			t.Fatalf("type %d: event = %+v, want address event for ifindex 9", msgType, ev)
		}
	}
	if _, _, err := linkEventFromNetlinkMessage(syscall.NetlinkMessage{
		Header: syscall.NlMsghdr{Type: syscall.RTM_NEWADDR}, Data: make([]byte, 3),
	}); err == nil {
		t.Fatal("short ifaddrmsg accepted")
	}
}
