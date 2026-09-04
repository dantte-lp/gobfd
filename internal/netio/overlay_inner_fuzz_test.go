package netio_test

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// FuzzInnerPacket tests BuildInnerPacket/StripInnerPacket round-trip
// with arbitrary BFD payloads. Verifies the inner packet codec never panics
// and preserves the payload through assembly/disassembly.
func FuzzInnerPacket(f *testing.F) {
	// Seed: typical 24-byte BFD Control packet.
	f.Add(make([]byte, 24))

	// Seed: minimum BFD packet.
	f.Add(make([]byte, 1))

	// Seed: padded BFD packet (RFC 9764).
	f.Add(make([]byte, 128))

	// Seed: empty payload.
	f.Add([]byte{})

	srcIP := netip.MustParseAddr("10.0.0.1")
	dstIP := netip.MustParseAddr("10.0.0.2")
	srcPort := uint16(49152)

	f.Fuzz(func(t *testing.T, bfdPayload []byte) {
		built, err := netio.BuildInnerPacket(bfdPayload, srcIP, dstIP, srcPort)
		if err != nil {
			return
		}

		stripped, gotSrc, gotDst, err := netio.StripInnerPacket(built)
		if err != nil {
			t.Fatalf("StripInnerPacket failed after successful build: %v", err)
		}

		if gotSrc != srcIP {
			t.Fatalf("round-trip srcIP mismatch: got %v, want %v", gotSrc, srcIP)
		}
		if gotDst != dstIP {
			t.Fatalf("round-trip dstIP mismatch: got %v, want %v", gotDst, dstIP)
		}

		if len(stripped) != len(bfdPayload) {
			t.Fatalf("round-trip payload length mismatch: got %d, want %d", len(stripped), len(bfdPayload))
		}

		for i := range bfdPayload {
			if stripped[i] != bfdPayload[i] {
				t.Fatalf("round-trip payload byte %d mismatch: got 0x%02x, want 0x%02x",
					i, stripped[i], bfdPayload[i])
			}
		}
	})
}

// FuzzStripInnerPacketRaw tests StripInnerPacket with arbitrary bytes.
func FuzzStripInnerPacketRaw(f *testing.F) {
	// Seed: valid inner packet.
	valid, _ := netio.BuildInnerPacket(make([]byte, 24),
		netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), 49152)
	f.Add(valid)

	// Seed: too short.
	f.Add([]byte{0x00, 0x52, 0x02})

	// Seed: wrong EtherType.
	wrong := make([]byte, netio.InnerOverheadIPv4+24)
	wrong[12] = 0x86
	wrong[13] = 0xDD
	f.Add(wrong)

	// Seeds: structurally complete packets with malformed length, TTL,
	// fragmentation, destination port, and checksum fields.
	mutations := []func([]byte) []byte{
		func(pkt []byte) []byte { return append(pkt, 0) },
		func(pkt []byte) []byte {
			pkt[netio.InnerEthSize+8] = 254
			updateIPv4Checksum(pkt)
			return pkt
		},
		func(pkt []byte) []byte {
			binary.BigEndian.PutUint16(pkt[netio.InnerEthSize+6:], 0x6000)
			updateIPv4Checksum(pkt)
			return pkt
		},
		func(pkt []byte) []byte {
			binary.BigEndian.PutUint16(pkt[netio.InnerEthSize+netio.InnerIPv4Size+2:], 4784)
			return pkt
		},
		func(pkt []byte) []byte { pkt[netio.InnerEthSize+10] ^= 1; return pkt },
	}
	for _, mutate := range mutations {
		seed := append([]byte(nil), valid...)
		f.Add(mutate(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _, err := netio.StripInnerPacket(data)
		if err != nil {
			return
		}

		if len(data) < netio.InnerOverheadIPv4 || !bytes.Equal(data[:6], []byte{0x00, 0x52, 0x02, 0, 0, 0}) {
			t.Fatal("accepted packet outside the supported Ethernet profile")
		}
		ip := data[netio.InnerEthSize:]
		ihl := int(ip[0]&0x0f) * 4
		if ip[0]>>4 != 4 || ihl < netio.InnerIPv4Size || ihl+netio.InnerUDPSize > len(ip) {
			t.Fatal("accepted invalid IPv4 version or IHL")
		}
		if int(binary.BigEndian.Uint16(ip[2:4])) != len(ip) || !verifyIPv4Checksum(ip[:ihl]) {
			t.Fatal("accepted invalid IPv4 length or checksum")
		}
		if binary.BigEndian.Uint16(ip[6:8])&^uint16(0x4000) != 0 || ip[8] != 255 || ip[9] != 17 {
			t.Fatal("accepted fragmented packet or invalid TTL/protocol")
		}
		udp := ip[ihl:]
		if binary.BigEndian.Uint16(udp[2:4]) != 3784 || int(binary.BigEndian.Uint16(udp[4:6])) != len(udp) {
			t.Fatal("accepted invalid UDP destination port or length")
		}
		if binary.BigEndian.Uint16(udp[6:8]) != 0 && !verifyUDPChecksum(ip, udp) {
			t.Fatal("accepted invalid UDP checksum")
		}
	})
}

func verifyUDPChecksum(ip, udp []byte) bool {
	sum := uint32(binary.BigEndian.Uint16(ip[12:14])) +
		uint32(binary.BigEndian.Uint16(ip[14:16])) +
		uint32(binary.BigEndian.Uint16(ip[16:18])) +
		uint32(binary.BigEndian.Uint16(ip[18:20])) +
		uint32(ip[9]) + uint32(len(udp))
	return checksum(udp, sum) == 0
}
