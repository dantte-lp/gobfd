//go:build interop_clab

package interop_clab_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

const (
	wireFixtureRecords = 20
	wireFixtureSnaplen = 256
	wireFixtureHeader  = 24
	wireFixtureRecord  = 16
)

func TestValidateFRRWireCaptureAcceptsClassicPCAP(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		order binary.ByteOrder
		nano  bool
	}{
		{name: "little_microseconds", order: binary.LittleEndian},
		{name: "big_microseconds", order: binary.BigEndian},
		{name: "little_nanoseconds", order: binary.LittleEndian, nano: true},
		{name: "big_nanoseconds", order: binary.BigEndian, nano: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := wireCaptureFixture(t, tc.order, tc.nano, wireFrameFixtures(t))
			if err := validateFRRWireCapture(data); err != nil {
				t.Fatalf("valid dual-stack capture rejected: %v", err)
			}
		})
	}
}

func TestValidateFRRWireCaptureAcceptsSourcePortBounds(t *testing.T) {
	t.Parallel()
	frames := wireFrameFixtures(t)
	for index, frame := range frames {
		if index%2 == 0 {
			copy(frame[34:36], []byte{0xff, 0xff})
		} else {
			copy(frame[54:56], []byte{0xc0, 0x00})
		}
	}
	if err := validateFRRWireCapture(wireCaptureFixture(t, binary.LittleEndian, false, frames)); err != nil {
		t.Fatalf("stable source ports at inclusive bounds rejected: %v", err)
	}
}

func TestValidateFRRWireCaptureAcceptsEthernetPadding(t *testing.T) {
	t.Parallel()
	frames := wireFrameFixtures(t)
	for index, frame := range frames {
		frames[index] = append(frame, make([]byte, wireFixtureSnaplen-len(frame))...)
	}
	if err := validateFRRWireCapture(wireCaptureFixture(t, binary.BigEndian, true, frames)); err != nil {
		t.Fatalf("complete records at snaplen and input bound rejected: %v", err)
	}
}

func TestValidateFRRWireCaptureRejectsFraming(t *testing.T) {
	t.Parallel()
	frames := wireFrameFixtures(t)
	valid := wireCaptureFixture(t, binary.LittleEndian, false, frames)
	nano := wireCaptureFixture(t, binary.LittleEndian, true, frames)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "empty"},
		{name: "short_global_header", data: valid[:23]},
		{name: "header_only", data: valid[:24]},
		{name: "pcapng_magic", data: patchedWireFixture(valid, 0, []byte{0x0a, 0x0d, 0x0d, 0x0a})},
		{name: "bad_major", data: patchedWireFixture(valid, 4, []byte{3, 0})},
		{name: "bad_minor", data: patchedWireFixture(valid, 6, []byte{3, 0})},
		{name: "bad_snaplen", data: patchedWireFixture(valid, 16, []byte{0xff, 0, 0, 0})},
		{name: "cooked_linktype", data: patchedWireFixture(valid, 20, []byte{113, 0, 0, 0})},
		{name: "linktype_flags", data: patchedWireFixture(valid, 20, []byte{1, 0, 0, 4})},
		{name: "micro_fraction_overflow", data: patchedWireFixture(valid, 28, []byte{0x40, 0x42, 0x0f, 0})},
		{name: "nano_fraction_overflow", data: patchedWireFixture(nano, 28, []byte{0, 0xca, 0x9a, 0x3b})},
		{name: "incl_over_snaplen", data: patchedWireFixture(valid, 32, []byte{1, 1, 0, 0})},
		{name: "incl_uint32_max", data: patchedWireFixture(valid, 32, []byte{0xff, 0xff, 0xff, 0xff})},
		{name: "orig_mismatch", data: patchedWireFixture(valid, 36, []byte{67, 0, 0, 0})},
		{name: "zero_record_length", data: patchedWireFixture(valid, 32, make([]byte, 8))},
		{name: "short_record_header", data: valid[:39]},
		{name: "short_record_body", data: valid[:105]},
		{name: "short_final_body", data: valid[:len(valid)-1]},
		{name: "nineteen_records", data: wireCaptureFixture(t, binary.LittleEndian, false, frames[:19])},
		{name: "twenty_one_records", data: wireCaptureFixture(t, binary.LittleEndian, false, append(frames, frames[0]))},
		{name: "trailing_byte", data: append(bytes.Clone(valid), 0)},
		{name: "oversized_input", data: make([]byte, 5465)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateFRRWireCapture(tc.data); err == nil {
				t.Fatal("invalid capture accepted")
			}
		})
	}
}

func TestValidateFRRWireCaptureRejectsBadFrame(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		frame  int
		offset int
		patch  []byte
	}{
		{name: "unsupported_ethernet", frame: 0, offset: 12, patch: []byte{0x81, 0}},
		{name: "ipv4_wrong_version", frame: 0, offset: 14, patch: []byte{0x65}},
		{name: "ipv4_small_ihl", frame: 0, offset: 14, patch: []byte{0x44}},
		{name: "ipv4_ihl_beyond_frame", frame: 0, offset: 14, patch: []byte{0x4f}},
		{name: "ipv4_total_too_small", frame: 0, offset: 16, patch: []byte{0, 19}},
		{name: "ipv4_total_beyond_frame", frame: 0, offset: 16, patch: []byte{0, 53}},
		{name: "ipv4_more_fragments", frame: 0, offset: 20, patch: []byte{0x20, 0}},
		{name: "ipv4_fragment_offset", frame: 0, offset: 20, patch: []byte{0, 1}},
		{name: "ipv4_not_udp", frame: 0, offset: 23, patch: []byte{6}},
		{name: "ipv4_ttl_254", frame: 0, offset: 22, patch: []byte{254}},
		{name: "ipv4_ttl_zero", frame: 0, offset: 22, patch: []byte{0}},
		{name: "ipv4_wrong_source", frame: 0, offset: 29, patch: []byte{2}},
		{name: "ipv4_wrong_destination", frame: 0, offset: 33, patch: []byte{1}},
		{name: "ipv4_source_49151", frame: 0, offset: 34, patch: []byte{0xbf, 0xff}},
		{name: "ipv4_source_zero", frame: 0, offset: 34, patch: []byte{0, 0}},
		{name: "ipv4_destination_3785", frame: 0, offset: 36, patch: []byte{0x0e, 0xc9}},
		{name: "ipv4_destination_zero", frame: 0, offset: 36, patch: []byte{0, 0}},
		{name: "ipv4_udp_short_header", frame: 0, offset: 38, patch: []byte{0, 7}},
		{name: "ipv4_udp_short_bfd", frame: 0, offset: 38, patch: []byte{0, 31}},
		{name: "ipv4_udp_beyond_ip", frame: 0, offset: 38, patch: []byte{0, 33}},
		{name: "ipv4_bfd_version_zero", frame: 0, offset: 42, patch: []byte{0}},
		{name: "ipv4_bfd_version_two", frame: 0, offset: 42, patch: []byte{0x40}},
		{name: "ipv4_bfd_length_short", frame: 0, offset: 45, patch: []byte{23}},
		{name: "ipv4_bfd_length_beyond_udp", frame: 0, offset: 45, patch: []byte{25}},
		{name: "ipv4_unstable_source_port", frame: 2, offset: 34, patch: []byte{0xc0, 1}},
		{name: "ipv6_wrong_version", frame: 1, offset: 14, patch: []byte{0x40}},
		{name: "ipv6_jumbo_payload", frame: 1, offset: 18, patch: []byte{0, 0}},
		{name: "ipv6_payload_beyond_frame", frame: 1, offset: 18, patch: []byte{0, 33}},
		{name: "ipv6_extension_header", frame: 1, offset: 20, patch: []byte{0}},
		{name: "ipv6_fragment_header", frame: 1, offset: 20, patch: []byte{44}},
		{name: "ipv6_hoplimit_254", frame: 1, offset: 21, patch: []byte{254}},
		{name: "ipv6_hoplimit_zero", frame: 1, offset: 21, patch: []byte{0}},
		{name: "ipv6_wrong_source", frame: 1, offset: 37, patch: []byte{1}},
		{name: "ipv6_wrong_destination", frame: 1, offset: 53, patch: []byte{0}},
		{name: "ipv6_source_49151", frame: 1, offset: 54, patch: []byte{0xbf, 0xff}},
		{name: "ipv6_source_zero", frame: 1, offset: 54, patch: []byte{0, 0}},
		{name: "ipv6_destination_3785", frame: 1, offset: 56, patch: []byte{0x0e, 0xc9}},
		{name: "ipv6_destination_zero", frame: 1, offset: 56, patch: []byte{0, 0}},
		{name: "ipv6_udp_short_header", frame: 1, offset: 58, patch: []byte{0, 7}},
		{name: "ipv6_udp_short_bfd", frame: 1, offset: 58, patch: []byte{0, 31}},
		{name: "ipv6_udp_beyond_ip", frame: 1, offset: 58, patch: []byte{0, 33}},
		{name: "ipv6_bfd_version_zero", frame: 1, offset: 62, patch: []byte{0}},
		{name: "ipv6_bfd_version_two", frame: 1, offset: 62, patch: []byte{0x40}},
		{name: "ipv6_bfd_length_short", frame: 1, offset: 65, patch: []byte{23}},
		{name: "ipv6_bfd_length_beyond_udp", frame: 1, offset: 65, patch: []byte{25}},
		{name: "ipv6_unstable_source_port", frame: 3, offset: 54, patch: []byte{0xff, 0xfe}},
		{name: "last_record_invalid", frame: 19, offset: 21, patch: []byte{254}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			frames := wireFrameFixtures(t)
			copy(frames[tc.frame][tc.offset:], tc.patch)
			if err := validateFRRWireCapture(wireCaptureFixture(t, binary.LittleEndian, false, frames)); err == nil {
				t.Fatal("capture containing an invalid frame accepted")
			}
		})
	}
}

func TestValidateFRRWireCaptureRejectsShortFrame(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		frame  int
		length int
	}{
		{name: "ethernet", frame: 0, length: 13},
		{name: "ipv4_header", frame: 0, length: 33},
		{name: "ipv4_udp_header", frame: 0, length: 41},
		{name: "ipv4_bfd_body", frame: 0, length: 65},
		{name: "ipv6_header", frame: 1, length: 53},
		{name: "ipv6_udp_header", frame: 1, length: 61},
		{name: "ipv6_bfd_body", frame: 1, length: 85},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			frames := wireFrameFixtures(t)
			frames[tc.frame] = frames[tc.frame][:tc.length]
			if err := validateFRRWireCapture(wireCaptureFixture(t, binary.LittleEndian, false, frames)); err == nil {
				t.Fatal("truncated wire frame accepted")
			}
		})
	}
}

func TestValidateFRRWireCaptureRequiresBothFamilies(t *testing.T) {
	t.Parallel()
	for _, family := range []int{0, 1} {
		name := "IPv4_only"
		if family == 1 {
			name = "IPv6_only"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			frames := wireFrameFixtures(t)
			for index := range frames {
				frames[index] = bytes.Clone(frames[family])
			}
			if err := validateFRRWireCapture(wireCaptureFixture(t, binary.LittleEndian, false, frames)); err == nil {
				t.Fatal("single-family capture accepted")
			}
		})
	}
}

func wireFrameFixtures(t *testing.T) [][]byte {
	t.Helper()
	// Hand-encoded Ethernet/IP/UDP/BFD frames, independent of daemon codecs.
	// IPv4: 10.0.6.1 -> 10.0.6.2, source 49152. IPv6: fd00:0:6:: -> fd00:0:6::1, source 65535.
	literals := []string{
		"0200000000020200000000010800" +
			"4500003400004000ff1100000a0006010a000602" +
			"c0000ec80020000020c003180000000100000002000493e0000493e000000000",
		"02000000000202000000000186dd" +
			"60000000002011fffd000000000600000000000000000000fd000000000600000000000000000001" +
			"ffff0ec80020000020c003180000000100000002000493e0000493e000000000",
	}
	frames := make([][]byte, wireFixtureRecords)
	for index := range frames {
		frame, err := hex.DecodeString(literals[index%2])
		if err != nil {
			t.Fatalf("decode literal wire fixture: %v", err)
		}
		frames[index] = frame
	}
	return frames
}

func wireCaptureFixture(t *testing.T, order binary.ByteOrder, nano bool, frames [][]byte) []byte {
	t.Helper()
	var globalHeader [wireFixtureHeader]byte
	data := globalHeader[:]
	magic, fraction := uint32(0xa1b2c3d4), uint32(999999)
	if nano {
		magic, fraction = 0xa1b23c4d, 999999999
	}
	order.PutUint32(data[:4], magic)
	order.PutUint16(data[4:6], 2)
	order.PutUint16(data[6:8], 4)
	order.PutUint32(data[16:20], wireFixtureSnaplen)
	order.PutUint32(data[20:24], 1)
	for _, frame := range frames {
		header := make([]byte, wireFixtureRecord)
		order.PutUint32(header[:4], 1)
		order.PutUint32(header[4:8], fraction)
		order.PutUint32(header[8:12], uint32(len(frame)))
		order.PutUint32(header[12:16], uint32(len(frame)))
		data = append(data, header...)
		data = append(data, frame...)
	}
	return data
}

func patchedWireFixture(data []byte, offset int, patch []byte) []byte {
	result := bytes.Clone(data)
	copy(result[offset:], patch)
	return result
}
