package config_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

// A multihop path crosses several interfaces, so an interface binding can
// never match its demultiplexing key (RFC 5883 Section 4.1).
func TestValidateSessionsRejectsMultiHopInterface(t *testing.T) {
	t.Parallel()
	for _, peer := range []string{"192.0.2.1", "2001:db8::1"} {
		t.Run(peer, func(t *testing.T) {
			t.Parallel()
			contents := fmt.Sprintf("sessions:\n  - peer: %q\n    type: multi_hop\n    interface: eth0\n", peer)
			if _, err := config.Load(writeTemp(t, contents)); !errors.Is(err, config.ErrInvalidSessionMultiHopInterface) {
				t.Fatalf("Load(multi_hop + interface) error = %v, want ErrInvalidSessionMultiHopInterface", err)
			}
		})
	}
}

// multihop_max_hops applies only to multi_hop sessions; 1-254 keep the derived
// minimum TTL 255-N within 1-254 (reading A, RFC 5082 Appendix A).
func TestValidateSessionsHopPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		session string
		wantErr bool
	}{
		{name: "multi_hop default", session: "    type: multi_hop\n"},
		{name: "multi_hop 1", session: "    type: multi_hop\n    multihop_max_hops: 1\n"},
		{name: "multi_hop 2", session: "    type: multi_hop\n    multihop_max_hops: 2\n"},
		{name: "multi_hop 5", session: "    type: multi_hop\n    multihop_max_hops: 5\n"},
		{name: "multi_hop 254", session: "    type: multi_hop\n    multihop_max_hops: 254\n"},
		{name: "multi_hop 0", session: "    type: multi_hop\n    multihop_max_hops: 0\n", wantErr: true},
		{name: "multi_hop 255", session: "    type: multi_hop\n    multihop_max_hops: 255\n", wantErr: true},
		{name: "multi_hop -1", session: "    type: multi_hop\n    multihop_max_hops: -1\n", wantErr: true},
		{name: "multi_hop fraction", session: "    type: multi_hop\n    multihop_max_hops: 2.5\n", wantErr: true},
		{name: "multi_hop bool", session: "    type: multi_hop\n    multihop_max_hops: true\n", wantErr: true},
		{name: "multi_hop wraparound", session: "    type: multi_hop\n    multihop_max_hops: 4294967297\n", wantErr: true},
		{name: "single_hop", session: "    type: single_hop\n    multihop_max_hops: 1\n", wantErr: true},
		{name: "default type", session: "    multihop_max_hops: 1\n", wantErr: true},
	} {
		for _, peer := range []string{"192.0.2.1", "2001:db8::1"} {
			t.Run(tc.name+"/"+peer, func(t *testing.T) {
				t.Parallel()
				contents := fmt.Sprintf("sessions:\n  - peer: %q\n", peer) + tc.session
				cfg, err := config.Load(writeTemp(t, contents))
				if tc.wantErr {
					if !errors.Is(err, config.ErrInvalidSessionMultihopMaxHops) {
						t.Fatalf("Load error = %v, want ErrInvalidSessionMultihopMaxHops", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if got := cfg.Sessions[0].MultihopMaxHopsSet; got != (tc.name != "multi_hop default") {
					t.Fatalf("MultihopMaxHopsSet = %t", got)
				}
			})
		}
	}
}
