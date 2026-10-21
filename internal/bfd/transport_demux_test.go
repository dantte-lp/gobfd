package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

type transportFamily struct {
	name        string
	peer, local netip.Addr
}

// transportFamilies covers RFC 5881 Section 2 / RFC 5883 Section 5 IPv4 and IPv6.
func transportFamilies() []transportFamily {
	return []transportFamily{
		{name: "ipv4", peer: netip.MustParseAddr("192.0.2.10"), local: netip.MustParseAddr("192.0.2.20")},
		{name: "ipv6", peer: netip.MustParseAddr("2001:db8::10"), local: netip.MustParseAddr("2001:db8::20")},
	}
}

type demuxPath uint8

const (
	demuxPathDiscr demuxPath = iota
	demuxPathPeer
	demuxPathLegacyPeer
)

func (p demuxPath) String() string {
	return [...]string{"discr", "peer", "legacy_peer"}[p]
}

func demuxPaths() []demuxPath { return []demuxPath{demuxPathDiscr, demuxPathPeer, demuxPathLegacyPeer} }

func newTransportManager(t *testing.T, opts ...ManagerOption) *Manager {
	t.Helper()
	mgr := NewManager(slog.New(slog.DiscardHandler), opts...)
	t.Cleanup(mgr.Close)
	return mgr
}

func transportSessionConfig(peer, local netip.Addr, sessType SessionType) SessionConfig {
	return SessionConfig{
		PeerAddr: peer, LocalAddr: local, Type: sessType, Role: RoleActive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
	}
}

func createTransportSession(t *testing.T, mgr *Manager, cfg SessionConfig) *Session {
	t.Helper()
	sess, err := mgr.CreateSession(context.Background(), cfg, NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

func demuxTransport(mgr *Manager, sess *Session, path demuxPath, meta PacketMeta) (DeliveryOutcome, error) {
	pkt := &ControlPacket{
		Version: Version, State: StateDown, DetectMult: 3, MyDiscriminator: 100,
		DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	switch path {
	case demuxPathDiscr:
		pkt.YourDiscriminator = sess.LocalDiscriminator()
		return mgr.DemuxWithWire(pkt, meta, nil)
	case demuxPathPeer:
		return mgr.DemuxWithWire(pkt, meta, nil)
	default:
		return mgr.Demux(pkt, meta)
	}
}

// RFC 5881 Section 5 discards single-hop packets whose TTL is not 255; a
// packet received on the RFC 5883 multihop port must therefore never reach a
// single-hop session, and a single-hop packet never reaches a multihop one.
func TestDemuxRejectsTransportClassMismatch(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		for _, tc := range []struct {
			name     string
			sessType SessionType
			multiHop bool
			ttl      uint8
		}{
			{name: "multihop_packet_to_single_hop", sessType: SessionTypeSingleHop, multiHop: true, ttl: 254},
			{name: "single_hop_packet_to_multihop", sessType: SessionTypeMultiHop, multiHop: false, ttl: 255},
		} {
			for _, path := range demuxPaths() {
				t.Run(fmt.Sprintf("%s/%s/%s", family.name, tc.name, path), func(t *testing.T) {
					t.Parallel()
					mgr := newTransportManager(t)
					sess := createTransportSession(t, mgr, transportSessionConfig(family.peer, family.local, tc.sessType))
					meta := PacketMeta{SrcAddr: family.peer, DstAddr: family.local, TTL: tc.ttl, MultiHop: tc.multiHop}

					outcome, err := demuxTransport(mgr, sess, path, meta)
					if outcome != DeliveryScopeMismatch || !errors.Is(err, ErrDemuxNoMatch) {
						t.Fatalf("demux = %s, %v; want ScopeMismatch, ErrDemuxNoMatch", outcome, err)
					}
					if got := deliveryCount(t, mgr, DeliveryDemux, DeliveryScopeMismatch); got != 1 {
						t.Fatalf("demux scope mismatch count = %d, want 1", got)
					}
					if got := deliveryCount(t, mgr, DeliveryDemux, DeliveryFound); got != 0 {
						t.Fatalf("demux found count = %d, want 0", got)
					}
				})
			}
		}
	}
}

// RFC 5881 Section 3: the session is bound to the interface; a packet from
// another interface must not reach it, including by Your Discriminator.
func TestDemuxByDiscrRejectsSingleHopInterfaceMismatch(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		for _, tc := range []struct {
			ifName string
			want   DeliveryOutcome
		}{
			{ifName: "eth1", want: DeliveryScopeMismatch},
			{ifName: "eth0", want: DeliveryQueued},
		} {
			t.Run(family.name+"/"+tc.ifName, func(t *testing.T) {
				t.Parallel()
				mgr := newTransportManager(t)
				cfg := transportSessionConfig(family.peer, family.local, SessionTypeSingleHop)
				cfg.Interface = "eth0"
				sess := createTransportSession(t, mgr, cfg)
				meta := PacketMeta{SrcAddr: family.peer, DstAddr: family.local, TTL: 255, IfName: tc.ifName}

				outcome, err := demuxTransport(mgr, sess, demuxPathDiscr, meta)
				if outcome != tc.want {
					t.Fatalf("demux = %s, %v; want %s", outcome, err, tc.want)
				}
				if tc.want == DeliveryScopeMismatch && !errors.Is(err, ErrDemuxNoMatch) {
					t.Fatalf("demux error = %v, want ErrDemuxNoMatch", err)
				}
			})
		}
	}
}

var errFakeResolve = errors.New("fake resolver failure")

// Option (b): a single-hop session without an explicit interface accepts only
// packets whose PKTINFO interface index matches the interface that owns its
// local address, resolved once at session creation.
func TestDemuxVerifiesSingleHopReceiveIfIndex(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		for _, path := range demuxPaths() {
			for _, tc := range []struct {
				name      string
				ifName    string
				ifIndex   int
				wantIndex int
				want      DeliveryOutcome
			}{
				{name: "owner_index", ifIndex: 7, wantIndex: 7, want: DeliveryQueued},
				{name: "other_index", ifIndex: 9, wantIndex: 7, want: DeliveryScopeMismatch},
				{name: "missing_index", ifIndex: 0, wantIndex: 7, want: DeliveryScopeMismatch},
				{name: "configured_interface_other_index", ifName: "eth0", ifIndex: 4, wantIndex: 3, want: DeliveryScopeMismatch},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", family.name, path, tc.name), func(t *testing.T) {
					t.Parallel()
					var gotLocal netip.Addr
					var gotIfName string
					mgr := newTransportManager(t, WithSessionInterfaceResolver(func(local netip.Addr, ifName string) (int, error) {
						gotLocal, gotIfName = local, ifName
						return tc.wantIndex, nil
					}))
					cfg := transportSessionConfig(family.peer, family.local, SessionTypeSingleHop)
					cfg.Interface = tc.ifName
					sess := createTransportSession(t, mgr, cfg)
					if gotLocal != family.local || gotIfName != tc.ifName {
						t.Fatalf("resolver(%s, %q), want (%s, %q)", gotLocal, gotIfName, family.local, tc.ifName)
					}
					// meta.IfName is the listener device binding: empty for an address listener.
					meta := PacketMeta{SrcAddr: family.peer, DstAddr: family.local, TTL: 255, IfIndex: tc.ifIndex}
					if path != demuxPathDiscr && tc.ifName != "" {
						meta.IfName = tc.ifName
					}

					outcome, err := demuxTransport(mgr, sess, path, meta)
					if outcome != tc.want {
						t.Fatalf("demux = %s, %v; want %s", outcome, err, tc.want)
					}
				})
			}
		}
	}
}

func TestSessionInterfaceResolverScope(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		t.Run(family.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			failing := newTransportManager(t, WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) {
				calls++
				return 0, errFakeResolve
			}))
			_, err := failing.CreateSession(context.Background(),
				transportSessionConfig(family.peer, family.local, SessionTypeSingleHop),
				NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}))
			if !errors.Is(err, errFakeResolve) {
				t.Fatalf("CreateSession(unresolvable local) error = %v, want resolver error", err)
			}
			if !errors.Is(err, ErrInterfaceUnresolved) {
				t.Fatalf("CreateSession(unresolvable local) error = %v, want ErrInterfaceUnresolved", err)
			}
			if calls != 1 {
				t.Fatalf("resolver calls = %d, want 1", calls)
			}
			// Multihop sessions are not bound to a receive interface (RFC 5883).
			createTransportSession(t, failing, transportSessionConfig(family.peer, family.local, SessionTypeMultiHop))
			if calls != 1 {
				t.Fatalf("resolver calls after multihop create = %d, want 1", calls)
			}
		})
	}
}

// Reading A of the hop policy: minimum TTL = 255 - N, so 1/2/5 hops accept
// 254/253/250 (RFC 5082 Appendix A, non-normative; RFC 5883 has no TTL rule).
// The check runs per demultiplexed session (RFC 5082 Section 3).
func TestMultiHopSessionHopPolicy(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		for _, policy := range []struct {
			hops   uint8
			minTTL uint8
		}{{0, 254}, {1, 254}, {2, 253}, {5, 250}} {
			for _, path := range demuxPaths() {
				for _, ttl := range []uint8{255, policy.minTTL, policy.minTTL - 1} {
					t.Run(fmt.Sprintf("%s/hops=%d/%s/ttl=%d", family.name, policy.hops, path, ttl), func(t *testing.T) {
						t.Parallel()
						mgr := newTransportManager(t)
						cfg := transportSessionConfig(family.peer, family.local, SessionTypeMultiHop)
						cfg.MultiHopMaxHops = policy.hops
						sess := createTransportSession(t, mgr, cfg)
						meta := PacketMeta{SrcAddr: family.peer, DstAddr: family.local, TTL: ttl, MultiHop: true}

						outcome, err := demuxTransport(mgr, sess, path, meta)
						if ttl >= policy.minTTL {
							if outcome != DeliveryQueued || err != nil {
								t.Fatalf("demux = %s, %v; want Queued", outcome, err)
							}
							return
						}
						if outcome != DeliveryTTLRejected || !errors.Is(err, ErrDemuxTTLRejected) {
							t.Fatalf("demux = %s, %v; want TTLRejected, ErrDemuxTTLRejected", outcome, err)
						}
						if got := deliveryCount(t, mgr, DeliveryDemux, DeliveryTTLRejected); got != 1 {
							t.Fatalf("demux TTL rejected count = %d, want 1", got)
						}
					})
				}
			}
		}
	}
}

// IPV6_PKTINFO yields an unzoned destination while a link-local session may
// be configured with a zone; ifName already carries the link (RFC 5881 Section 3).
func TestDemuxIPv6LinkLocalSingleHop(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name               string
		cfgPeer, cfgLocal  string
		metaPeer, metaDest string
	}{
		{name: "unzoned_config", cfgPeer: "fe80::1", cfgLocal: "fe80::2", metaPeer: "fe80::1", metaDest: "fe80::2"},
		{name: "zoned_local", cfgPeer: "fe80::1", cfgLocal: "fe80::2%eth0", metaPeer: "fe80::1", metaDest: "fe80::2"},
		{name: "zoned_source", cfgPeer: "fe80::1", cfgLocal: "fe80::2", metaPeer: "fe80::1%eth0", metaDest: "fe80::2"},
		{
			name: "zoned_both", cfgPeer: "fe80::1%eth0", cfgLocal: "fe80::2%eth0",
			metaPeer: "fe80::1%eth0", metaDest: "fe80::2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := newTransportManager(t)
			peer, local := netip.MustParseAddr(tc.cfgPeer), netip.MustParseAddr(tc.cfgLocal)
			cfg := transportSessionConfig(peer, local, SessionTypeSingleHop)
			cfg.Interface = "eth0"
			sess := createTransportSession(t, mgr, cfg)
			meta := PacketMeta{
				SrcAddr: netip.MustParseAddr(tc.metaPeer), DstAddr: netip.MustParseAddr(tc.metaDest),
				TTL: 255, IfName: "eth0",
			}
			if outcome, err := demuxTransport(mgr, sess, demuxPathPeer, meta); outcome != DeliveryQueued {
				t.Fatalf("demux = %s, %v; want Queued", outcome, err)
			}
		})
	}
}

// RFC 5883 Section 3 forbids the Echo function over multiple hops (MUST NOT).
func TestMultiHopControlAdvertisesNoEcho(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		t.Run(family.name, func(t *testing.T) {
			t.Parallel()
			sess, err := NewSession(transportSessionConfig(family.peer, family.local, SessionTypeMultiHop), 1,
				overlayIdentityNoopSender{}, nil, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if got := sess.buildControlPacket().RequiredMinEchoRxInterval; got != 0 {
				t.Fatalf("RequiredMinEchoRxInterval = %d, want 0", got)
			}
		})
	}
}

func TestValidateSessionConfigTransportPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		sessType SessionType
		ifName   string
		hops     uint8
		want     error
	}{
		{name: "multihop_interface", sessType: SessionTypeMultiHop, ifName: "eth0", want: ErrInvalidMultiHopInterface},
		{name: "single_hop_hops", sessType: SessionTypeSingleHop, hops: 1, want: ErrInvalidMultiHopMaxHops},
		{name: "multihop_255", sessType: SessionTypeMultiHop, hops: 255, want: ErrInvalidMultiHopMaxHops},
		{name: "multihop_254", sessType: SessionTypeMultiHop, hops: 254},
		{name: "multihop_5", sessType: SessionTypeMultiHop, hops: 5},
		{name: "single_hop_interface", sessType: SessionTypeSingleHop, ifName: "eth0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			family := transportFamilies()[0]
			cfg := transportSessionConfig(family.peer, family.local, tc.sessType)
			cfg.Interface, cfg.MultiHopMaxHops = tc.ifName, tc.hops
			if err := ValidateSessionConfig(cfg); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("ValidateSessionConfig = %v, want %v", err, tc.want)
			}
		})
	}
}

func configOwner(id string) SessionOwner {
	return SessionOwner{Source: SessionOwnerSourceConfig, ID: id}
}

// The hop policy is an effective session parameter: zero and one are the same
// policy, and a different policy is a conflict rather than a silent no-op.
func TestMultiHopMaxHopsIsEffectiveParameter(t *testing.T) {
	t.Parallel()
	family := transportFamilies()[1]
	mgr := newTransportManager(t)
	cfg := transportSessionConfig(family.peer, family.local, SessionTypeMultiHop)
	factory := NonOwningSenderLeaseFactory(overlayIdentityNoopSender{})
	if _, err := mgr.CreateSessionAs(context.Background(), cfg, factory, configOwner("a")); err != nil {
		t.Fatalf("CreateSessionAs(default): %v", err)
	}
	cfg.MultiHopMaxHops = 1
	if _, err := mgr.CreateSessionAs(context.Background(), cfg, factory, configOwner("b")); err != nil {
		t.Fatalf("CreateSessionAs(hops=1): %v", err)
	}
	cfg.MultiHopMaxHops = 2
	_, err := mgr.CreateSessionAs(context.Background(), cfg, factory, configOwner("c"))
	if !errors.Is(err, ErrSessionParameterConflict) {
		t.Fatalf("CreateSessionAs(hops=2) error = %v, want ErrSessionParameterConflict", err)
	}
}

// Without an explicit interface, the zone of a link-local address selects the
// session interface (RFC 5881 Section 3), so one link-local pair on two links
// forms two sessions that demultiplex by receive interface.
func TestLinkLocalZoneSelectsSessionInterface(t *testing.T) {
	t.Parallel()
	mgr := newTransportManager(t)
	sessions := make(map[string]*Session)
	for _, ifName := range []string{"eth0", "eth1"} {
		peer, local := netip.MustParseAddr("fe80::1%"+ifName), netip.MustParseAddr("fe80::2%"+ifName)
		sess := createTransportSession(t, mgr, transportSessionConfig(peer, local, SessionTypeSingleHop))
		if sess.Interface() != ifName {
			t.Fatalf("session interface = %q, want zone %q", sess.Interface(), ifName)
		}
		sessions[ifName] = sess
	}
	for ifName, sess := range sessions {
		meta := PacketMeta{
			SrcAddr: netip.MustParseAddr("fe80::1"), DstAddr: netip.MustParseAddr("fe80::2"),
			TTL: 255, IfName: ifName,
		}
		if got, ok := mgr.LookupByPeer(packetDemuxKeyFromMeta(meta)); !ok || got != sess {
			t.Fatalf("peer lookup on %s found %v, want session %d", ifName, got, sess.LocalDiscriminator())
		}
		if outcome, err := demuxTransport(mgr, sess, demuxPathPeer, meta); outcome != DeliveryQueued {
			t.Fatalf("demux on %s = %s, %v; want Queued", ifName, outcome, err)
		}
	}
}

func TestValidateSessionConfigZoneInterface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, ifName, peer, local string
		want                      error
	}{
		{
			name: "interface differs from local zone", ifName: "eth0", peer: "fe80::1", local: "fe80::2%eth1",
			want: ErrInvalidSessionZone,
		},
		{
			name: "interface differs from peer zone", ifName: "eth0", peer: "fe80::1%eth1", local: "fe80::2",
			want: ErrInvalidSessionZone,
		},
		{
			name: "peer zone differs from local zone", peer: "fe80::1%eth1", local: "fe80::2%eth0",
			want: ErrInvalidSessionZone,
		},
		{name: "matching zones", ifName: "eth0", peer: "fe80::1%eth0", local: "fe80::2%eth0"},
		{name: "zone only", peer: "fe80::1", local: "fe80::2%eth0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := transportSessionConfig(netip.MustParseAddr(tc.peer), netip.MustParseAddr(tc.local), SessionTypeSingleHop)
			cfg.Interface = tc.ifName
			if err := ValidateSessionConfig(cfg); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("ValidateSessionConfig = %v, want %v", err, tc.want)
			}
		})
	}
}

// RFC 5881 Section 5: single-hop packets demultiplexed to the session are
// discarded unless the received TTL or Hop Limit is 255.
func TestDemuxRejectsSingleHopTTLBelow255(t *testing.T) {
	t.Parallel()
	for _, family := range transportFamilies() {
		for _, path := range demuxPaths() {
			t.Run(family.name+"/"+path.String(), func(t *testing.T) {
				t.Parallel()
				mgr := newTransportManager(t)
				sess := createTransportSession(t, mgr, transportSessionConfig(family.peer, family.local, SessionTypeSingleHop))
				meta := PacketMeta{SrcAddr: family.peer, DstAddr: family.local, TTL: 254}
				outcome, err := demuxTransport(mgr, sess, path, meta)
				if outcome != DeliveryTTLRejected || !errors.Is(err, ErrDemuxTTLRejected) {
					t.Fatalf("demux = %s, %v; want TTLRejected, ErrDemuxTTLRejected", outcome, err)
				}
			})
		}
	}
}

// An unresolvable receive interface makes one desired session pending without
// failing or rolling back its neighbours; a later pass creates it.
func TestReconcileTreatsUnresolvedInterfaceAsPending(t *testing.T) {
	t.Parallel()
	unresolved := netip.MustParseAddr("192.0.2.250")
	var ready atomic.Bool
	mgr := newTransportManager(t, WithSessionInterfaceResolver(func(local netip.Addr, _ string) (int, error) {
		if local == unresolved && !ready.Load() {
			return 0, errFakeResolve
		}
		return 7, nil
	}))
	desired := []ReconcileConfig{
		{
			Key: "good", SessionConfig: transportSessionConfig(
				netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2"), SessionTypeSingleHop),
			SenderLeaseFactory: NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}),
		},
		{
			Key: "pending", SessionConfig: transportSessionConfig(
				netip.MustParseAddr("192.0.2.3"), unresolved, SessionTypeSingleHop),
			SenderLeaseFactory: NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}),
		},
	}
	result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(), ConfigReconciliationOwner(), desired)
	if result.Created != 1 || result.ResourcePending != 1 || result.Failed != 0 ||
		!errors.Is(result.Err(), ErrInterfaceUnresolved) || len(mgr.Sessions()) != 1 {
		t.Fatalf("first pass = %+v, err %v, sessions %d; want 1 created, 1 pending",
			result, result.Err(), len(mgr.Sessions()))
	}
	ready.Store(true)
	result = mgr.ReconcileSessionsForOwnerDetailed(context.Background(), ConfigReconciliationOwner(), desired)
	if result.Created != 1 || result.ResourcePending != 0 || result.Err() != nil || len(mgr.Sessions()) != 2 {
		t.Fatalf("retry = %+v, err %v, sessions %d; want 1 created, none pending", result, result.Err(), len(mgr.Sessions()))
	}
}
