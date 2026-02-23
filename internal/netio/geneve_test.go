package netio_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Geneve Header Marshal/Unmarshal Tests
// -------------------------------------------------------------------------

func TestGeneveMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hdr       netio.GeneveHeader
		wantProto uint16
	}{
		{
			"ethernet_payload",
			netio.GeneveHeader{
				Version:      0,
				OptLen:       0,
				OBit:         true,
				CBit:         false,
				ProtocolType: netio.GeneveProtocolEthernet,
				VNI:          100,
			},
			netio.GeneveProtocolEthernet,
		},
		{
			"ipv4_payload",
			netio.GeneveHeader{
				Version:      0,
				OptLen:       0,
				OBit:         true,
				CBit:         false,
				ProtocolType: netio.GeneveProtocolIPv4,
				VNI:          42,
			},
			netio.GeneveProtocolIPv4,
		},
		{
			"ipv6_payload",
			netio.GeneveHeader{
				Version:      0,
				OptLen:       0,
				OBit:         true,
				CBit:         false,
				ProtocolType: netio.GeneveProtocolIPv6,
				VNI:          999,
			},
			netio.GeneveProtocolIPv6,
		},
		{
			"max_vni",
			netio.GeneveHeader{
				Version:      0,
				OBit:         true,
				ProtocolType: netio.GeneveProtocolEthernet,
				VNI:          0x00FFFFFF,
			},
			netio.GeneveProtocolEthernet,
		},
		{
			"zero_vni",
			netio.GeneveHeader{
				Version:      0,
				OBit:         true,
				ProtocolType: netio.GeneveProtocolIPv4,
				VNI:          0,
			},
			netio.GeneveProtocolIPv4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, netio.GeneveHeaderMinSize)
			n, err := netio.MarshalGeneveHeader(buf, tt.hdr)
			if err != nil {
				t.Fatalf("MarshalGeneveHeader: %v", err)
			}
			if n != netio.GeneveHeaderMinSize {
				t.Fatalf("wrote %d bytes, want %d", n, netio.GeneveHeaderMinSize)
			}

			got, err := netio.UnmarshalGeneveHeader(buf)
			if err != nil {
				t.Fatalf("UnmarshalGeneveHeader: %v", err)
			}

			if got.Version != tt.hdr.Version {
				t.Errorf("Version = %d, want %d", got.Version, tt.hdr.Version)
			}
			if got.OptLen != tt.hdr.OptLen {
				t.Errorf("OptLen = %d, want %d", got.OptLen, tt.hdr.OptLen)
			}
			if got.OBit != tt.hdr.OBit {
				t.Errorf("OBit = %v, want %v", got.OBit, tt.hdr.OBit)
			}
			if got.CBit != tt.hdr.CBit {
				t.Errorf("CBit = %v, want %v", got.CBit, tt.hdr.CBit)
			}
			if got.ProtocolType != tt.wantProto {
				t.Errorf("ProtocolType = 0x%04x, want 0x%04x", got.ProtocolType, tt.wantProto)
			}
			if got.VNI != tt.hdr.VNI {
				t.Errorf("VNI = %d, want %d", got.VNI, tt.hdr.VNI)
			}
		})
	}
}

func TestGeneveMarshalOBitSet(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.GeneveHeaderMinSize)
	hdr := netio.GeneveHeader{OBit: true, ProtocolType: netio.GeneveProtocolEthernet, VNI: 1}
	if _, err := netio.MarshalGeneveHeader(buf, hdr); err != nil {
		t.Fatalf("MarshalGeneveHeader: %v", err)
	}

	// O bit is bit 7 of byte 1.
	if buf[1]&0x80 == 0 {
		t.Error("O bit (control) not set")
	}
	// C bit should be clear.
	if buf[1]&0x40 != 0 {
		t.Error("C bit should not be set for BFD")
	}
}

func TestGeneveMarshalCBitSet(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.GeneveHeaderMinSize)
	hdr := netio.GeneveHeader{CBit: true, ProtocolType: netio.GeneveProtocolEthernet, VNI: 1}
	if _, err := netio.MarshalGeneveHeader(buf, hdr); err != nil {
		t.Fatalf("MarshalGeneveHeader: %v", err)
	}

	if buf[1]&0x40 == 0 {
		t.Error("C bit not set when requested")
	}
}

func TestGeneveUnmarshalInvalidVersion(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.GeneveHeaderMinSize)
	// Set version to 1 (bits 7-6 of byte 0).
	buf[0] = 0x40
	_, err := netio.UnmarshalGeneveHeader(buf)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestGeneveMarshalBufferTooShort(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 7)
	_, err := netio.MarshalGeneveHeader(buf, netio.GeneveHeader{})
	if err == nil {
		t.Fatal("expected error for short buffer")
	}
}

func TestGeneveUnmarshalBufferTooShort(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 7)
	_, err := netio.UnmarshalGeneveHeader(buf)
	if err == nil {
		t.Fatal("expected error for short buffer")
	}
}

func TestGeneveMarshalVNIOverflow(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.GeneveHeaderMinSize)
	hdr := netio.GeneveHeader{VNI: 0x01000000}
	_, err := netio.MarshalGeneveHeader(buf, hdr)
	if err == nil {
		t.Fatal("expected error for VNI overflow")
	}
}

func TestGenevePortConstant(t *testing.T) {
	t.Parallel()

	if netio.GenevePort != 6081 {
		t.Errorf("GenevePort = %d, want 6081", netio.GenevePort)
	}
}

func TestGeneveHeaderMinSizeConstant(t *testing.T) {
	t.Parallel()

	if netio.GeneveHeaderMinSize != 8 {
		t.Errorf("GeneveHeaderMinSize = %d, want 8", netio.GeneveHeaderMinSize)
	}
}

func TestGeneveProtocolTypeConstants(t *testing.T) {
	t.Parallel()

	if netio.GeneveProtocolEthernet != 0x6558 {
		t.Errorf("GeneveProtocolEthernet = 0x%04x, want 0x6558", netio.GeneveProtocolEthernet)
	}
	if netio.GeneveProtocolIPv4 != 0x0800 {
		t.Errorf("GeneveProtocolIPv4 = 0x%04x, want 0x0800", netio.GeneveProtocolIPv4)
	}
	if netio.GeneveProtocolIPv6 != 0x86DD {
		t.Errorf("GeneveProtocolIPv6 = 0x%04x, want 0x86DD", netio.GeneveProtocolIPv6)
	}
}

func TestGeneveTotalHeaderSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		optLen uint8
		want   int
	}{
		{"no_options", 0, 8},
		{"one_option_word", 1, 12},
		{"two_option_words", 2, 16},
		{"max_options", 63, 260},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hdr := netio.GeneveHeader{OptLen: tt.optLen}
			if got := hdr.TotalHeaderSize(); got != tt.want {
				t.Errorf("TotalHeaderSize() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGeneveMarshalOptLen(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.GeneveHeaderMinSize)
	hdr := netio.GeneveHeader{OptLen: 5, ProtocolType: netio.GeneveProtocolEthernet, VNI: 1}
	if _, err := netio.MarshalGeneveHeader(buf, hdr); err != nil {
		t.Fatalf("MarshalGeneveHeader: %v", err)
	}

	// Opt Len is in bits 5-0 of byte 0 (version is bits 7-6).
	gotOptLen := buf[0] & 0x3F
	if gotOptLen != 5 {
		t.Errorf("OptLen = %d, want 5", gotOptLen)
	}
}

func TestGeneveMarshalSpecificVNI(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.GeneveHeaderMinSize)
	hdr := netio.GeneveHeader{ProtocolType: netio.GeneveProtocolEthernet, VNI: 0xABCDEF}
	if _, err := netio.MarshalGeneveHeader(buf, hdr); err != nil {
		t.Fatalf("MarshalGeneveHeader: %v", err)
	}

	// VNI is stored as big-endian in bytes 4-6.
	if buf[4] != 0xAB || buf[5] != 0xCD || buf[6] != 0xEF {
		t.Errorf("VNI bytes = [%02x %02x %02x], want [AB CD EF]", buf[4], buf[5], buf[6])
	}
	// Byte 7 is reserved.
	if buf[7] != 0 {
		t.Errorf("reserved byte[7] = 0x%02x, want 0x00", buf[7])
	}
}
