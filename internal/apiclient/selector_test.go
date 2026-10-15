package apiclient_test

import (
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestSessionSelectorRejectsAmbiguousIdentity(t *testing.T) {
	t.Parallel()
	sessions := []*bfdv1.BfdSession{
		{
			PeerAddress: "192.0.2.1", LocalAddress: "192.0.2.2", InterfaceName: "eth0",
			Type: bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, LocalDiscriminator: 1,
		},
		{
			PeerAddress: "::ffff:192.0.2.1", LocalAddress: "192.0.2.3", InterfaceName: "eth1",
			Type: bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, LocalDiscriminator: 2,
		},
	}
	for _, tc := range []struct {
		name, local, iface, kind string
		want                     uint32
		ambiguous                bool
	}{
		{name: "peer only", ambiguous: true},
		{name: "local", local: "::ffff:192.0.2.2", want: 1},
		{name: "interface", iface: "eth1", want: 2},
		{name: "type", kind: "single_hop", want: 1},
		{name: "absent", local: "192.0.2.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sel, err := apiclient.ParseSessionSelector("192.0.2.1", tc.local, tc.iface, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			got, err := sel.Select(sessions)
			if errors.Is(err, apiclient.ErrAmbiguousSession) != tc.ambiguous || got.GetLocalDiscriminator() != tc.want {
				t.Fatalf("selection = %v, %v; want discriminator %d, ambiguous %v", got, err, tc.want, tc.ambiguous)
			}
		})
	}
	// Even an explicit public tuple cannot identify hidden overlay scope.
	sel, err := apiclient.ParseSessionSelector("192.0.2.1", "192.0.2.2", "eth0", "single_hop")
	if err != nil {
		t.Fatal(err)
	}
	_, err = sel.Select([]*bfdv1.BfdSession{sessions[0], sessions[0]})
	if !errors.Is(err, apiclient.ErrAmbiguousSession) {
		t.Fatalf("duplicate projection: %v", err)
	}
}

func TestSessionSelectorValidatesConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range [][4]string{
		{"invalid", "", "", ""},
		{"192.0.2.1", "invalid", "", ""},
		{"192.0.2.1", "", "", "unknown"},
	} {
		if _, err := apiclient.ParseSessionSelector(tc[0], tc[1], tc[2], tc[3]); err == nil {
			t.Fatalf("accepted %v", tc)
		}
	}
}
