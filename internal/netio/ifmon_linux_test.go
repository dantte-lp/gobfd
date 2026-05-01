//go:build linux

package netio

import (
	"encoding/binary"
	"syscall"
	"testing"
)

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
