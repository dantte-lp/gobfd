//go:build linux

package netio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func timestampTestConn(
	t *testing.T,
	family loopbackFamily,
	overflowAncillary bool,
	requiredOptions ...int,
) *net.UDPConn {
	t.Helper()
	conn, err := listenUDP(t.Context(), netip.AddrPortFrom(family.addr, 0), "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if deadlineErr := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Control(func(fd uintptr) {
		for _, opt := range requiredOptions {
			value, sockErr := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, opt)
			if errors.Is(sockErr, unix.ENOPROTOOPT) {
				t.Skipf("kernel does not support receive option %d: %v", opt, sockErr)
			}
			if sockErr != nil || value != 1 {
				t.Errorf("receive option %d = %d, error=%v, want enabled", opt, value, sockErr)
			}
		}
		if overflowAncillary {
			// Deliberately request a second timestamp format to overflow the receive control buffer.
			flags := unix.SOF_TIMESTAMPING_RX_SOFTWARE | unix.SOF_TIMESTAMPING_SOFTWARE
			if sockErr := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TIMESTAMPING_NEW, flags); sockErr != nil {
				if errors.Is(sockErr, unix.ENOPROTOOPT) {
					t.Skipf("kernel does not support the extra timestamp format: %v", sockErr)
				}
				t.Error(sockErr)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestRXTimestampPreservesTransportMetadata(t *testing.T) {
	t.Parallel()
	for _, family := range loopbackFamilies() {
		t.Run(family.name, func(t *testing.T) {
			t.Parallel()
			conn := timestampTestConn(t, family, false, unix.SO_TIMESTAMPNS_NEW, unix.SO_RXQ_OVFL)
			payload := []byte{0x20, 0xc0, 0x03, 0x18}
			before := time.Now().UnixNano()
			sendLoopback(t, family, conn.LocalAddr().(*net.UDPAddr).AddrPort().Port(), 255, payload)
			var buf [64]byte
			n, meta, err := (&LinuxPacketConn{conn: conn}).ReadPacket(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf[:n], payload) || meta.TTL != 255 ||
				meta.DstAddr != family.addr || meta.IfIndex != loopbackIndex(t) {
				t.Fatalf("timestamp displaced transport metadata: n=%d meta=%+v", n, meta)
			}
			if meta.KernelRXUnixNano < before || meta.KernelRXUnixNano > time.Now().UnixNano() {
				t.Fatalf("kernel receive timestamp outside CLOCK_REALTIME receive window: %+v", meta)
			}
		})
	}
}

func TestRXTimestampRejectsTruncatedAncillary(t *testing.T) {
	t.Parallel()
	family := loopbackFamilies()[0]
	conn := timestampTestConn(t, family, true, unix.SO_TIMESTAMPNS_NEW)
	sendLoopback(t, family, conn.LocalAddr().(*net.UDPAddr).AddrPort().Port(), 255, []byte{1})
	var buf [64]byte
	if _, _, err := (&LinuxPacketConn{conn: conn}).ReadPacket(buf[:]); !errors.Is(err, ErrAncillaryTruncated) {
		t.Fatalf("truncated ancillary error = %v, want ErrAncillaryTruncated", err)
	}
}

func TestRXTimestampTime64Validation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		sec, nsec uint64
		length    int
		want      int64
	}{
		{"valid", 1_700_000_000, 42, 16, 1_700_000_000_000_000_042},
		{"after2038", 2_200_000_000, 999_999_999, 16, 2_200_000_000_999_999_999},
		{"int64Boundary", 9_223_372_036, 854_775_807, 16, math.MaxInt64},
		{"overflowNanosecond", 9_223_372_036, 854_775_808, 16, 0},
		{"overflowSecond", 9_223_372_037, 0, 16, 0},
		{"negativeSecond", math.MaxUint64, 0, 16, 0},
		{"negativeNanosecond", 1, math.MaxUint64, 16, 0},
		{"invalidNanosecond", 1, 1_000_000_000, 16, 0},
		{"zero", 0, 0, 16, 0},
		{"short", 1, 0, 15, 0},
		{"long", 1, 0, 17, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := make([]byte, 17)
			binary.NativeEndian.PutUint64(data[:8], tc.sec)
			binary.NativeEndian.PutUint64(data[8:16], tc.nsec)
			var meta PacketMeta
			parseControlMessages([]unix.SocketControlMessage{{
				Header: unix.Cmsghdr{Level: unix.SOL_SOCKET, Type: unix.SO_TIMESTAMPNS_NEW},
				Data:   data[:tc.length],
			}}, &meta)
			if meta.KernelRXUnixNano != tc.want {
				t.Fatalf("kernel receive timestamp = %d, want %d", meta.KernelRXUnixNano, tc.want)
			}
		})
	}
}

func TestRXTimestampSocketDropSamples(t *testing.T) {
	t.Parallel()
	nativeSample := make([]byte, 4)
	binary.NativeEndian.PutUint32(nativeSample, 0x01020304)
	for _, tc := range []struct {
		name  string
		data  []byte
		count uint32
		known bool
	}{
		{"missing", nil, 0, false},
		{"zero", []byte{0, 0, 0, 0}, 0, true},
		{"maximum", []byte{255, 255, 255, 255}, math.MaxUint32, true},
		{"nativeOrder", nativeSample, 0x01020304, true},
		{"short", []byte{0, 0, 0}, 0, false},
		{"long", []byte{0, 0, 0, 0, 0}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var meta PacketMeta
			if tc.data != nil {
				parseControlMessages([]unix.SocketControlMessage{{
					Header: unix.Cmsghdr{Level: unix.SOL_SOCKET, Type: unix.SO_RXQ_OVFL}, Data: tc.data,
				}}, &meta)
			}
			if meta.SocketDropsKnown != tc.known || meta.SocketDrops != tc.count {
				t.Fatalf("drop sample=(%d,%v), want (%d,%v)", meta.SocketDrops, meta.SocketDropsKnown, tc.count, tc.known)
			}
		})
	}
}

func TestRXTimestampAncillaryBudget(t *testing.T) {
	t.Parallel()
	for _, pktInfoSize := range []int{12, 20} {
		needed := unix.CmsgSpace(pktInfoSize) + unix.CmsgSpace(4) + unix.CmsgSpace(16) + unix.CmsgSpace(4)
		if oobSize < needed {
			t.Errorf("ancillary capacity %d cannot hold %d-byte receive metadata", oobSize, needed)
		}
	}
}

func TestRXTimestampReportsCumulativeSocketDrops(t *testing.T) {
	t.Parallel()
	family := loopbackFamilies()[0]
	conn := timestampTestConn(t, family, false, unix.SO_RXQ_OVFL)
	if err := conn.SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	for range 128 {
		sendLoopback(t, family, port, 255, make([]byte, 512))
	}
	packetConn := &LinuxPacketConn{conn: conn}
	var buf [1024]byte
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := packetConn.ReadPacket(buf[:])
		if err == nil {
			continue
		}
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("drain overflowed socket: %v", err)
		}
		break
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var previous uint32
	for range 2 {
		// A fresh accepted datagram carries drops that happened after the old queue filled.
		sendLoopback(t, family, port, 255, []byte{1})
		_, meta, err := packetConn.ReadPacket(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		if !meta.SocketDropsKnown || meta.SocketDrops == 0 || (previous != 0 && meta.SocketDrops != previous) {
			t.Fatalf("missing, reset or duplicated socket-drop sample: previous=%d meta=%+v", previous, meta)
		}
		previous = meta.SocketDrops
	}
	t.Logf("kernel cumulative socket drops on both post-load markers: %d", previous)
}

func TestRXTimestampIgnoresOtherClockFormats(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		level, kind int32
	}{
		{"hardwareTuple", unix.SOL_SOCKET, unix.SO_TIMESTAMPING_NEW},
		{"oldABI", unix.SOL_SOCKET, unix.SCM_TIMESTAMPNS},
		{"wrongLevel", unix.IPPROTO_IP, unix.SO_TIMESTAMPNS_NEW},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var data [16]byte
			binary.NativeEndian.PutUint64(data[:8], 1_700_000_000)
			binary.NativeEndian.PutUint64(data[8:], 42)
			var meta PacketMeta
			parseControlMessages([]unix.SocketControlMessage{{
				Header: unix.Cmsghdr{Level: tc.level, Type: tc.kind}, Data: data[:],
			}}, &meta)
			if meta.KernelRXUnixNano != 0 {
				t.Fatalf("unsupported clock format accepted as software time64: %+v", meta)
			}
		})
	}
}

func BenchmarkRXTimestampDecode(b *testing.B) {
	var data [16]byte
	binary.NativeEndian.PutUint64(data[:8], 1_700_000_000)
	binary.NativeEndian.PutUint64(data[8:], 42)
	b.ReportAllocs()
	for b.Loop() {
		if got := parseKernelRXTimestamp(data[:]); got != 1_700_000_000_000_000_042 {
			b.Fatalf("receive timestamp = %d", got)
		}
	}
}

func TestRXTelemetryOptionErrors(t *testing.T) {
	t.Parallel()
	conn, err := listenUDP(t.Context(), netip.AddrPortFrom(loopbackFamilies()[0].addr, 0), "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Control(func(fd uintptr) {
		for _, tc := range []struct {
			name       string
			fd, option int
			want       error
		}{
			{"enabled", int(fd), unix.SO_RXQ_OVFL, nil},
			{"unsupported", int(fd), -1, nil},
			{"invalidValueSize", int(fd), unix.SO_RCVTIMEO_OLD, unix.EINVAL},
			{"invalidFD", -1, unix.SO_TIMESTAMPNS_NEW, unix.EBADF},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := enableReceiveTelemetry(tc.fd, tc.option); !errors.Is(got, tc.want) {
					t.Fatalf("receive telemetry option error = %v, want %v", got, tc.want)
				}
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
}
