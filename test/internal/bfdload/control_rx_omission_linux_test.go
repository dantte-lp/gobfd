//go:build linux

package bfdload

import (
	"encoding/json"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestControlRXOmissionQualificationDoesNotAcceptOrMutate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*peerInput)
		want bool
	}{
		{"valid", func(*peerInput) {}, true},
		{"codec", func(p *peerInput) { p.invalid = true }, false},
		{"ttl", func(p *peerInput) { p.meta.TTL = 254 }, false},
		{"tuple", func(p *peerInput) { p.meta.SrcAddr = netip.MustParseAddr("192.0.2.3") }, false},
		{"demand", func(p *peerInput) { p.packet.Demand = true }, false},
		{"auth", func(p *peerInput) { p.packet.AuthPresent = true }, false},
		{"discriminator", func(p *peerInput) { p.packet.YourDiscriminator = 12 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				peer := blackholePeer(t)
				peer.state = fixturePeerState(t)
				started := time.Now()
				if _, err := peer.state.receive(fixturePeerPacket(bfd.StateInit), started); err != nil {
					t.Fatal(err)
				}
				peer.observed.Endpoint = OwnerEndpoint{
					Local: netip.MustParseAddrPort("192.0.2.1:3784"), Peer: netip.MustParseAddrPort("192.0.2.2:3784"),
				}
				input := peerInput{packet: fixturePeerPacket(bfd.StateUp), meta: netio.PacketMeta{
					SrcAddr: peer.observed.Endpoint.Local.Addr(), DstAddr: peer.observed.Endpoint.Peer.Addr(), TTL: 255,
				}}
				tc.edit(&input)
				before := peer.state
				synctest.Sleep(10 * time.Millisecond)
				if err := peer.onInput(t.Context(), input, started, nil); err != nil {
					t.Fatal(err)
				}
				if peer.state != before || peer.observed.Accepted != 0 || peer.observed.Rejected != 0 ||
					peer.observed.ReceiveBlackholeOmitted != 1 {
					t.Fatal("RX omission changed the actual FSM, deadline or receive disposition")
				}
				data, err := json.Marshal(peer.observed.BlackholeWindows[0].Segments[0])
				if err != nil {
					t.Fatal(err)
				}
				var row struct {
					Valid     bool `json:"valid"`
					Qualified bool `json:"receive_qualified"`
				}
				if err := json.Unmarshal(data, &row); err != nil {
					t.Fatal(err)
				}
				if row.Valid || row.Qualified != tc.want {
					t.Fatalf("actual Control RX qualification: got %+v, want receive_qualified=%v", row, tc.want)
				}
			})
		})
	}
}

func TestControlRXOmissionDoesNotCoalesceDifferentAcceptanceEvidence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		peer := blackholePeer(t)
		peer.state = fixturePeerState(t)
		started := time.Now()
		if _, err := peer.state.receive(fixturePeerPacket(bfd.StateInit), started); err != nil {
			t.Fatal(err)
		}
		synctest.Sleep(10 * time.Millisecond)
		input := peerInput{packet: fixturePeerPacket(bfd.StateUp), meta: netio.PacketMeta{TTL: 255}}
		for _, ttl := range []uint8{255, 254, 255} {
			input.meta.TTL = ttl
			if err := peer.onInput(t.Context(), input, started, nil); err != nil {
				t.Fatal(err)
			}
		}
		rows := peer.observed.BlackholeWindows[0].Segments
		if len(rows) != 3 || !rows[0].ReceiveQualified || rows[1].ReceiveQualified || !rows[2].ReceiveQualified {
			t.Fatal("identical packet bodies merged different actual receive acceptance checks")
		}
		if validWorkloadOmission(PeerOmissionSegment{Direction: "tx", ReceiveQualified: true}, false) ||
			validWorkloadOmission(PeerOmissionSegment{Direction: "rx", ReceiveQualified: true}, true) {
			t.Fatal("Control RX qualification was accepted for another direction or mode")
		}
	})
}
