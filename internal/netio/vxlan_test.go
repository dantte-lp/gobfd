package netio_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// VXLAN Header Marshal/Unmarshal Tests
// -------------------------------------------------------------------------

func TestVXLANMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vni  uint32
	}{
		{"zero_vni", 0},
		{"vni_1", 1},
		{"vni_100", 100},
		{"vni_4096", 4096},
		{"vni_max_24bit", 0x00FFFFFF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, netio.VXLANHeaderSize)
			n, err := netio.MarshalVXLANHeader(buf, tt.vni)
			if err != nil {
				t.Fatalf("MarshalVXLANHeader(%d): %v", tt.vni, err)
			}
			if n != netio.VXLANHeaderSize {
				t.Fatalf("MarshalVXLANHeader wrote %d bytes, want %d", n, netio.VXLANHeaderSize)
			}

			hdr, err := netio.UnmarshalVXLANHeader(buf)
			if err != nil {
				t.Fatalf("UnmarshalVXLANHeader: %v", err)
			}
			if hdr.VNI != tt.vni {
				t.Errorf("VNI = %d, want %d", hdr.VNI, tt.vni)
			}
		})
	}
}

func TestVXLANMarshalIFlagSet(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.VXLANHeaderSize)
	if _, err := netio.MarshalVXLANHeader(buf, 42); err != nil {
		t.Fatalf("MarshalVXLANHeader: %v", err)
	}

	// I flag is bit 4 of byte 0 (0x08).
	if buf[0]&0x08 == 0 {
		t.Error("I flag (VNI valid) not set in marshaled header")
	}
}

func TestVXLANMarshalReservedBytesZero(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.VXLANHeaderSize)
	if _, err := netio.MarshalVXLANHeader(buf, 100); err != nil {
		t.Fatalf("MarshalVXLANHeader: %v", err)
	}

	// Bytes 1-3 are reserved (must be zero).
	for i := 1; i <= 3; i++ {
		if buf[i] != 0 {
			t.Errorf("reserved byte[%d] = 0x%02x, want 0x00", i, buf[i])
		}
	}

	// Byte 7 is reserved (low 8 bits of the VNI word).
	if buf[7] != 0 {
		t.Errorf("reserved byte[7] = 0x%02x, want 0x00", buf[7])
	}
}

func TestVXLANUnmarshalNoIFlag(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.VXLANHeaderSize)
	// I flag not set — all zeros.
	_, err := netio.UnmarshalVXLANHeader(buf)
	if err == nil {
		t.Fatal("expected error for missing I flag")
	}
}

func TestVXLANMarshalBufferTooShort(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 7) // too short
	_, err := netio.MarshalVXLANHeader(buf, 1)
	if err == nil {
		t.Fatal("expected error for short buffer")
	}
}

func TestVXLANUnmarshalBufferTooShort(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 7)
	_, err := netio.UnmarshalVXLANHeader(buf)
	if err == nil {
		t.Fatal("expected error for short buffer")
	}
}

func TestVXLANMarshalVNIOverflow(t *testing.T) {
	t.Parallel()

	buf := make([]byte, netio.VXLANHeaderSize)
	_, err := netio.MarshalVXLANHeader(buf, 0x01000000) // 25-bit value
	if err == nil {
		t.Fatal("expected error for VNI overflow")
	}
}

func TestVXLANPortConstant(t *testing.T) {
	t.Parallel()

	if netio.VXLANPort != 4789 {
		t.Errorf("VXLANPort = %d, want 4789", netio.VXLANPort)
	}
}

func TestVXLANHeaderSizeConstant(t *testing.T) {
	t.Parallel()

	if netio.VXLANHeaderSize != 8 {
		t.Errorf("VXLANHeaderSize = %d, want 8", netio.VXLANHeaderSize)
	}
}

func TestVXLANBFDInnerMAC(t *testing.T) {
	t.Parallel()

	// RFC 8971 Section 3.1: inner destination MAC for BFD-over-VXLAN.
	if netio.VXLANBFDInnerMAC != "00:52:02:00:00:00" {
		t.Errorf("VXLANBFDInnerMAC = %q, want %q", netio.VXLANBFDInnerMAC, "00:52:02:00:00:00")
	}
}

func TestVXLANMarshalSpecificVNI(t *testing.T) {
	t.Parallel()

	// VNI 0xABCDEF should be encoded in bytes 4-6.
	buf := make([]byte, netio.VXLANHeaderSize)
	if _, err := netio.MarshalVXLANHeader(buf, 0xABCDEF); err != nil {
		t.Fatalf("MarshalVXLANHeader: %v", err)
	}

	// VNI is stored as big-endian in bytes 4-6.
	if buf[4] != 0xAB || buf[5] != 0xCD || buf[6] != 0xEF {
		t.Errorf("VNI bytes = [%02x %02x %02x], want [AB CD EF]", buf[4], buf[5], buf[6])
	}
}
