//go:build linux

package netio

import (
	"encoding/binary"
	"net/netip"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAncillaryTTLNativeInt(t *testing.T) {
	t.Parallel()
	for _, family := range []struct {
		name        string
		level, kind int32
	}{
		{"IPv4", unix.IPPROTO_IP, unix.IP_TTL},
		{"IPv6", unix.IPPROTO_IPV6, unix.IPV6_HOPLIMIT},
	} {
		for _, tc := range []struct {
			name   string
			value  uint32
			length int
			want   uint8
		}{
			{"zero", 0, 4, 0},
			{"one", 1, 4, 1},
			{"echo", 254, 4, 254},
			{"gtsm", 255, 4, 255},
			{"overflow", 511, 4, 0},
			{"negative", 0xffffffff, 4, 0},
			{"empty", 255, 0, 0},
			{"byte", 255, 1, 0},
			{"short", 255, 3, 0},
			{"long", 255, 5, 0},
		} {
			t.Run(family.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var data [5]byte
				binary.NativeEndian.PutUint32(data[:4], tc.value)
				var meta PacketMeta
				parseControlMessages([]unix.SocketControlMessage{{
					Header: unix.Cmsghdr{Level: family.level, Type: family.kind}, Data: data[:tc.length],
				}}, &meta)
				if meta.TTL != tc.want {
					t.Fatalf("TTL/HopLimit = %d, want %d", meta.TTL, tc.want)
				}
			})
		}
	}
}

func TestAncillaryPacketInfoNativeIndex(t *testing.T) {
	t.Parallel()
	for _, index := range []uint32{1, 65537, 0x01020304} {
		t.Run(strconv.FormatUint(uint64(index), 10), func(t *testing.T) {
			t.Parallel()
			v4 := []byte{0, 0, 0, 0, 192, 0, 2, 1, 198, 51, 100, 7}
			binary.NativeEndian.PutUint32(v4[:4], index)
			v6 := netip.MustParseAddr("2001:db8::7").As16()
			v6Data := make([]byte, 20)
			copy(v6Data, v6[:])
			binary.NativeEndian.PutUint32(v6Data[16:], index)
			for _, tc := range []struct {
				level, kind int32
				data        []byte
				want        netip.Addr
			}{
				{unix.IPPROTO_IP, unix.IP_PKTINFO, v4, netip.MustParseAddr("198.51.100.7")},
				{unix.IPPROTO_IPV6, unix.IPV6_PKTINFO, v6Data, netip.MustParseAddr("2001:db8::7")},
			} {
				var meta PacketMeta
				parseControlMessages([]unix.SocketControlMessage{{
					Header: unix.Cmsghdr{Level: tc.level, Type: tc.kind}, Data: tc.data,
				}}, &meta)
				if meta.IfIndex != int(index) || meta.DstAddr != tc.want {
					t.Fatalf("packet info = %+v, want index %d destination %s", meta, index, tc.want)
				}
			}
		})
	}
}
