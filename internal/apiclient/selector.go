package apiclient

import (
	"errors"
	"fmt"
	"net/netip"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// ErrAmbiguousSession means the selector cannot identify one wire session.
var ErrAmbiguousSession = errors.New("session selector is ambiguous")

// SessionSelector selects a persistent session tuple from a Watch snapshot.
// An omitted field matches any value; Peer is required.
type SessionSelector struct {
	Peer      netip.Addr
	Local     netip.Addr
	Interface string
	Type      bfdv1.SessionType
}

// ParseSessionSelector validates companion configuration before starting Watch.
func ParseSessionSelector(peer, local, iface, kind string) (SessionSelector, error) {
	p, err := netip.ParseAddr(peer)
	if err != nil {
		return SessionSelector{}, fmt.Errorf("session peer: %w", err)
	}
	sel := SessionSelector{Peer: p.Unmap(), Interface: iface}
	if local != "" {
		addr, parseErr := netip.ParseAddr(local)
		if parseErr != nil {
			return SessionSelector{}, fmt.Errorf("session local address: %w", parseErr)
		}
		sel.Local = addr.Unmap()
	}
	switch kind {
	case "":
	case "single_hop":
		sel.Type = bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP
	case "multi_hop":
		sel.Type = bfdv1.SessionType_SESSION_TYPE_MULTI_HOP
	case "micro_bfd":
		sel.Type = bfdv1.SessionType_SESSION_TYPE_MICRO_BFD
	case "vxlan":
		sel.Type = bfdv1.SessionType_SESSION_TYPE_VXLAN
	case "geneve":
		sel.Type = bfdv1.SessionType_SESSION_TYPE_GENEVE
	default:
		return SessionSelector{}, fmt.Errorf("session type %q: %w", kind, errors.ErrUnsupported)
	}
	return sel, nil
}

// Select returns nil for no match and an error for multiple matches, including
// overlay sessions whose full transport identity is absent from bfd.v1.
func (s SessionSelector) Select(sessions []*bfdv1.BfdSession) (*bfdv1.BfdSession, error) {
	var match *bfdv1.BfdSession
	for _, session := range sessions {
		peer, err := netip.ParseAddr(session.GetPeerAddress())
		if err != nil || peer.Unmap() != s.Peer {
			continue
		}
		if s.Local.IsValid() {
			local, parseErr := netip.ParseAddr(session.GetLocalAddress())
			if parseErr != nil || local.Unmap() != s.Local {
				continue
			}
		}
		if s.Interface != "" && session.GetInterfaceName() != s.Interface ||
			s.Type != bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED && session.GetType() != s.Type {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("peer %s: %w", s.Peer, ErrAmbiguousSession)
		}
		match = session
	}
	return match, nil
}
