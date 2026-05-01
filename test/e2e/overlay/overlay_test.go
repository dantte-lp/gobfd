//go:build e2e_overlay

// Package overlay_test validates S10.4 overlay backend boundaries.
package overlay_test

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"net/netip"
	"os"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

var reservedBackends = []string{
	config.OverlayBackendKernel,
	config.OverlayBackendOVS,
	config.OverlayBackendOVN,
	config.OverlayBackendCilium,
	config.OverlayBackendCalico,
	config.OverlayBackendNSX,
}

const bfdControlPort uint16 = 3784

func TestOverlayReservedBackendsFailClosed(t *testing.T) {
	for _, backend := range reservedBackends {
		t.Run("vxlan "+backend, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.VXLAN.Enabled = true
			cfg.VXLAN.Backend = backend
			cfg.VXLAN.ManagementVNI = 100
			cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "10.0.0.1", Local: "10.0.0.2"}}
			if err := config.Validate(cfg); !errors.Is(err, config.ErrUnsupportedOverlayBackend) {
				t.Fatalf("Validate() error = %v, want ErrUnsupportedOverlayBackend", err)
			}
		})

		t.Run("geneve "+backend, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Geneve.Enabled = true
			cfg.Geneve.Backend = backend
			cfg.Geneve.DefaultVNI = 100
			cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "10.0.0.1", Local: "10.0.0.2"}}
			if err := config.Validate(cfg); !errors.Is(err, config.ErrUnsupportedOverlayBackend) {
				t.Fatalf("Validate() error = %v, want ErrUnsupportedOverlayBackend", err)
			}
		})
	}
}

func TestOverlayUserspacePacketShape(t *testing.T) {
	src := netip.MustParseAddr("10.0.0.2")
	dst := netip.MustParseAddr("10.0.0.1")
	payload := []byte{
		0x20, 0x01, 0x03, 0x18,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x02,
		0x00, 0x0f, 0x42, 0x40,
		0x00, 0x0f, 0x42, 0x40,
		0x00, 0x00, 0x00, 0x00,
	}

	vxlan, err := netio.BuildVXLANPacket(payload, 100, src, dst, 49152)
	if err != nil {
		t.Fatalf("BuildVXLANPacket: %v", err)
	}
	assertOverlayInnerShape(t, "VXLAN", vxlan, netio.VXLANHeaderSize, netio.VXLANPort)
	gotVXLANPayload, gotVNI, gotSrc, gotDst, err := netio.ParseVXLANPacket(vxlan)
	if err != nil {
		t.Fatalf("ParseVXLANPacket: %v", err)
	}
	if gotVNI != 100 || gotSrc != src || gotDst != dst || !bytes.Equal(gotVXLANPayload, payload) {
		t.Fatalf("VXLAN parse = payload:%x vni:%d src:%s dst:%s", gotVXLANPayload, gotVNI, gotSrc, gotDst)
	}

	geneve, err := netio.BuildGenevePacket(payload, 200, src, dst, 49153)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}
	assertOverlayInnerShape(t, "Geneve", geneve, netio.GeneveHeaderMinSize, netio.GenevePort)
	gotGenevePayload, gotGeneveHeader, gotGeneveSrc, gotGeneveDst, err := netio.ParseGenevePacket(geneve)
	if err != nil {
		t.Fatalf("ParseGenevePacket: %v", err)
	}
	if gotGeneveHeader.VNI != 200 || !gotGeneveHeader.OBit || gotGeneveHeader.CBit ||
		gotGeneveHeader.ProtocolType != netio.GeneveProtocolEthernet ||
		gotGeneveSrc != src || gotGeneveDst != dst || !bytes.Equal(gotGenevePayload, payload) {
		t.Fatalf("Geneve parse = payload:%x header:%+v src:%s dst:%s",
			gotGenevePayload, gotGeneveHeader, gotGeneveSrc, gotGeneveDst)
	}

	writePacketCSV(t, [][]string{
		{"overlay", "outer_udp_dst", "inner_udp_dst", "vni", "ttl", "o_bit", "c_bit", "backend"},
		{"vxlan", "4789", "3784", "100", "255", "", "", config.OverlayBackendUserspaceUDP},
		{"geneve", "6081", "3784", "200", "255", "true", "false", config.OverlayBackendUserspaceUDP},
	})
}

func assertOverlayInnerShape(t *testing.T, name string, pkt []byte, overlayHeaderSize int, outerUDPPort uint16) {
	t.Helper()

	ipOff := overlayHeaderSize + netio.InnerEthSize
	udpOff := ipOff + netio.InnerIPv4Size
	if len(pkt) < udpOff+4 {
		t.Fatalf("%s packet too short for inner UDP destination: len=%d", name, len(pkt))
	}
	if outerUDPPort != netio.VXLANPort && outerUDPPort != netio.GenevePort {
		t.Fatalf("%s outer UDP destination = %d, want VXLAN or Geneve standard port", name, outerUDPPort)
	}
	if ttl := pkt[ipOff+8]; ttl != 255 {
		t.Fatalf("%s inner IPv4 TTL = %d, want 255", name, ttl)
	}
	if dstPort := binary.BigEndian.Uint16(pkt[udpOff+2 : udpOff+4]); dstPort != bfdControlPort {
		t.Fatalf("%s inner UDP destination = %d, want %d", name, dstPort, bfdControlPort)
	}
}

func writePacketCSV(t *testing.T, rows [][]string) {
	t.Helper()
	path := os.Getenv("E2E_OVERLAY_PACKET_CSV")
	if path == "" {
		return
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create packet CSV: %v", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		t.Fatalf("write packet CSV: %v", err)
	}
	if err := w.Error(); err != nil {
		t.Fatalf("flush packet CSV: %v", err)
	}
}
