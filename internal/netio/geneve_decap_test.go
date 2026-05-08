package netio

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"
)

const testVNI uint32 = 100

// newTestGeneveConn constructs a GeneveConn without opening a UDP socket.
// Used to drive the decapsulation paths under unit-test conditions.
func newTestGeneveConn() *GeneveConn {
	return &GeneveConn{
		vni:       testVNI,
		localAddr: netip.MustParseAddr("10.0.0.1"),
		srcPort:   12345,
		readBuf:   make([]byte, 2048),
		logger:    slog.New(slog.DiscardHandler),
	}
}

func TestDecapGenevePacket_HappyPath(t *testing.T) {
	t.Parallel()

	src := netip.MustParseAddr("10.0.0.2")
	dst := netio4Addr("10.0.0.1")
	pkt, err := BuildGenevePacket([]byte{
		0x20, 0x40, 0x06, 0x18,
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}, testVNI, src, dst, 0xC000)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	c := newTestGeneveConn()
	payload, hdr, err := c.decapGenevePacket(pkt)
	if err != nil {
		t.Fatalf("decapGenevePacket: %v", err)
	}
	if len(payload) == 0 {
		t.Fatalf("expected non-empty BFD payload")
	}
	if hdr.VNI != testVNI {
		t.Fatalf("VNI: got %d, want %d", hdr.VNI, testVNI)
	}
	if !hdr.OBit {
		t.Fatalf("expected O bit set")
	}
	if hdr.CBit {
		t.Fatalf("expected C bit clear")
	}
}

func TestDecapGenevePacket_TooShort(t *testing.T) {
	t.Parallel()

	c := newTestGeneveConn()
	_, _, err := c.decapGenevePacket(make([]byte, GeneveHeaderMinSize-1))
	if !errors.Is(err, ErrGeneveHeaderTooShort) {
		t.Fatalf("expected ErrGeneveHeaderTooShort, got %v", err)
	}
}

func TestValidateGeneveHeader_VNIMismatch(t *testing.T) {
	t.Parallel()

	c := newTestGeneveConn()
	hdr := GeneveHeader{
		OBit:         true,
		CBit:         false,
		ProtocolType: GeneveProtocolEthernet,
		VNI:          200,
	}
	err := c.validateGeneveHeader(hdr, GeneveHeaderMinSize+InnerOverheadIPv4, GeneveHeaderMinSize)
	if !errors.Is(err, ErrOverlayVNIMismatch) {
		t.Fatalf("expected ErrOverlayVNIMismatch, got %v", err)
	}
}

func TestValidateGeneveHeader_OBitClear(t *testing.T) {
	t.Parallel()

	c := newTestGeneveConn()
	hdr := GeneveHeader{
		OBit:         false,
		CBit:         false,
		ProtocolType: GeneveProtocolEthernet,
		VNI:          100,
	}
	err := c.validateGeneveHeader(hdr, GeneveHeaderMinSize+InnerOverheadIPv4, GeneveHeaderMinSize)
	if !errors.Is(err, ErrGeneveOBitNotSet) {
		t.Fatalf("expected ErrGeneveOBitNotSet, got %v", err)
	}
}

func TestValidateGeneveHeader_CBitSet(t *testing.T) {
	t.Parallel()

	c := newTestGeneveConn()
	hdr := GeneveHeader{
		OBit:         true,
		CBit:         true,
		ProtocolType: GeneveProtocolEthernet,
		VNI:          100,
	}
	err := c.validateGeneveHeader(hdr, GeneveHeaderMinSize+InnerOverheadIPv4, GeneveHeaderMinSize)
	if !errors.Is(err, ErrGeneveCBitSet) {
		t.Fatalf("expected ErrGeneveCBitSet, got %v", err)
	}
}

func TestValidateGeneveHeader_UnexpectedProtocol(t *testing.T) {
	t.Parallel()

	c := newTestGeneveConn()
	hdr := GeneveHeader{
		OBit:         true,
		CBit:         false,
		ProtocolType: 0x0800, // IPv4 instead of Ethernet
		VNI:          100,
	}
	err := c.validateGeneveHeader(hdr, GeneveHeaderMinSize+InnerOverheadIPv4, GeneveHeaderMinSize)
	if !errors.Is(err, ErrGeneveUnexpectedProto) {
		t.Fatalf("expected ErrGeneveUnexpectedProto, got %v", err)
	}
}

func TestValidateGeneveHeader_PacketTooShort(t *testing.T) {
	t.Parallel()

	c := newTestGeneveConn()
	hdr := GeneveHeader{
		OBit:         true,
		CBit:         false,
		ProtocolType: GeneveProtocolEthernet,
		VNI:          100,
	}
	err := c.validateGeneveHeader(hdr, GeneveHeaderMinSize+1, GeneveHeaderMinSize)
	if !errors.Is(err, ErrInnerPacketTooShort) {
		t.Fatalf("expected ErrInnerPacketTooShort, got %v", err)
	}
}

// netio4Addr is a tiny helper to dodge a netip.MustParseAddr name shadowing
// rule inside this test file.
func netio4Addr(s string) netip.Addr { return netip.MustParseAddr(s) }
