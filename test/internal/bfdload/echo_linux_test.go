//go:build linux

package bfdload

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func echoFixture(t *testing.T) PeerConfig {
	t.Helper()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Echo = true
	profile.Interval, profile.Duration = 10*time.Millisecond, time.Second
	return PeerConfig{Profile: profile, SourcePortBase: 62000, Sessions: []OwnerEndpoint{
		{Local: netip.MustParseAddrPort("127.0.0.1:3785"), Peer: netip.MustParseAddrPort("127.0.0.2:3785")},
		{Local: netip.MustParseAddrPort("[::1]:3785"), Peer: netip.MustParseAddrPort("[::1]:38385")},
	}}
}

func TestEchoOwnerActualRegistrationsAndCancellation(t *testing.T) {
	cfg := echoFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner, err := StartOwner(ctx, OwnerConfig{
		Echo: true, Sessions: cfg.Sessions,
		Interval: cfg.Profile.Interval, DetectMultiplier: cfg.Profile.DetectMultiplier,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := owner.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if len(owner.manager.EchoSessions()) != 2 || len(owner.manager.Sessions()) != 0 {
		t.Fatal("Echo mode did not create actual Echo owners exclusively")
	}
	initial, err := owner.Snapshot(t.Context())
	if err != nil || initial.Registrations.KnownPeers != 2 || initial.Registrations.ActiveMetricRegistrations != 2 {
		t.Fatalf("Echo registry/collector disagree: %+v: %v", initial.Registrations, err)
	}
	first := initial.Sessions[0]
	claimConfig := bfd.EchoSessionConfig{
		LocalAddr: first.Endpoint.Local.Addr(), PeerAddr: first.Endpoint.Peer.Addr(),
		Interface: first.Endpoint.Interface, TxInterval: cfg.Profile.Interval, DetectMultiplier: cfg.Profile.DetectMultiplier,
	}
	observer := bfd.APISessionOwner("metric-probe")
	for range 2 {
		shared, claimErr := owner.manager.CreateEchoSessionAs(t.Context(), claimConfig, nil, observer)
		if claimErr != nil || shared.LocalDiscriminator() != first.LocalDiscriminator {
			t.Fatalf("idempotent Echo claim allocated a second wire: %v", claimErr)
		}
	}
	shared, err := owner.Snapshot(t.Context())
	if err != nil || shared.Registrations.ActiveMetricRegistrations != 2 {
		t.Fatalf("Echo metrics counted claims instead of physical sessions: %+v: %v", shared.Registrations, err)
	}
	releaseErr := owner.manager.DestroyEchoSessionAs(t.Context(), first.LocalDiscriminator, observer)
	if releaseErr != nil {
		t.Fatal(releaseErr)
	}
	shared, err = owner.Snapshot(t.Context())
	if err != nil || shared.Registrations.KnownPeers != 2 || shared.Registrations.ActiveMetricRegistrations != 2 {
		t.Fatalf("releasing one Echo claim decremented physical metrics: %+v: %v", shared.Registrations, err)
	}
	loadOwner := bfd.APISessionOwner("bfdload")
	releaseErr = owner.manager.DestroyEchoSessionAs(t.Context(), first.LocalDiscriminator, loadOwner)
	if releaseErr != nil {
		t.Fatal(releaseErr)
	}
	released, err := owner.Snapshot(t.Context())
	if err != nil || released.Registrations.KnownPeers != 1 || released.Registrations.ActiveMetricRegistrations != 1 {
		t.Fatalf("final Echo claim did not unregister its physical metrics: %+v: %v", released.Registrations, err)
	}
	cancel()
	select {
	case <-owner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Echo cancellation did not join its owned workers")
	}
	final, err := owner.Snapshot(t.Context())
	if err != nil || !final.Stopped || final.Registrations.KnownPeers != 0 ||
		final.Registrations.ActiveMetricRegistrations != 0 {
		t.Fatalf("Echo close retained active registrations: %+v: %v", final.Registrations, err)
	}
	for _, session := range initial.Sessions {
		for _, binding := range []netip.AddrPort{
			session.Endpoint.Local,
			netip.AddrPortFrom(session.Endpoint.Local.Addr(), session.SourcePort),
		} {
			conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(binding))
			if err != nil {
				t.Fatalf("Echo close leaked owned socket %s: %v", binding, err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestEchoPeersActualReturnsBlackholeAndRecovery(t *testing.T) {
	for _, txOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "both_directions", true: "return_only"}[txOnly], func(t *testing.T) {
			cfg := echoFixture(t)
			cfg.Profile.TXOnlyBlackhole = txOnly
			cfg.Profile.BlackholeEvery, cfg.Profile.BlackholeFor = 300*time.Millisecond, 100*time.Millisecond
			owner, err := StartOwner(t.Context(), OwnerConfig{
				Echo: true, Sessions: cfg.Sessions,
				Interval: cfg.Profile.Interval, DetectMultiplier: cfg.Profile.DetectMultiplier,
			}, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := owner.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			if len(owner.manager.EchoSessions()) != 2 {
				t.Fatal("actual UDP fixture has no Echo owners")
			}
			changes, err := owner.manager.SubscribeStateChanges(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			peers, err := RunPeers(t.Context(), cfg, slog.New(slog.DiscardHandler))
			if err != nil || !peers.Completed || !peers.Stopped || peers.ReferenceQualified || len(peers.Peers) != 2 {
				t.Fatalf("actual Echo returns failed: %+v: %v", peers, err)
			}
			var written uint64
			for _, peer := range peers.Peers {
				written += peer.Written
			}
			drainCtx, cancelDrain := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancelDrain()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				observed, snapshotErr := owner.Snapshot(drainCtx)
				if snapshotErr != nil || observed.TerminalPackets > written {
					t.Fatalf("Echo return drain is inconsistent: terminal=%d written=%d: %v",
						observed.TerminalPackets, written, snapshotErr)
				}
				if observed.TerminalPackets == written {
					break
				}
				select {
				case <-drainCtx.Done():
					t.Fatal("actual Echo returns did not settle before owner shutdown")
				case <-tick.C:
				}
			}
			if closeErr := owner.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			var up, down uint64
			for change := range changes {
				if change.Type != bfd.SessionTypeEcho || change.RemoteKnown || change.Status.RemoteDiscr != 0 {
					t.Fatalf("Echo fixture manufactured a remote Control peer: %+v", change)
				}
				if change.Added || change.Deleted {
					continue
				}
				if change.NewState == bfd.StateUp {
					up++
				}
				if change.OldState == bfd.StateUp && change.NewState == bfd.StateDown {
					down++
					if change.Diag != bfd.DiagEchoFailed || change.TransitionTiming.Version != change.ObservationVersion {
						t.Fatalf("Echo blackhole did not retain its actual expiry: %+v", change)
					}
				}
			}
			if up < 4 || down < 2 {
				t.Fatalf("wire returns did not prove Down/Up recovery for both families: up=%d down=%d", up, down)
			}
			final, err := owner.Snapshot(t.Context())
			if err != nil || final.TerminalPackets == 0 {
				t.Fatalf("Echo return delivery was not accounted: %+v: %v", final.Delivery, err)
			}
			for _, peer := range peers.Peers {
				assertEchoOmissionInputs(t, peer)
				disposed := peer.Accepted + peer.Rejected + peer.ReceiveRandomOmitted +
					peer.ReceiveBlackholeOmitted + peer.ReceiveNotProcessed
				if peer.Read == 0 || peer.Accepted == 0 || peer.Written == 0 || peer.RemoteDiscriminator != 0 ||
					peer.UpTransitions != 0 || peer.DownTransitions != 0 || peer.WriteErrors != 0 || peer.ReadErrors != 0 ||
					peer.Scheduled != peer.Written+peer.RandomOmitted+peer.BlackholeOmitted+peer.WriteCanceled ||
					peer.Read != disposed {
					t.Fatalf("Echo accounting or absence of remote FSM is wrong: %+v", peer)
				}
				if (!txOnly && peer.ReceiveBlackholeOmitted == 0) || (txOnly && peer.BlackholeOmitted == 0) {
					t.Fatalf("requested Echo omission direction did not run: %+v", peer)
				}
			}
			if final.TerminalPackets != written {
				t.Fatalf("Echo lifetime terminal packets=%d, actual independent returns=%d", final.TerminalPackets, written)
			}
		})
	}
}

func assertEchoOmissionInputs(t *testing.T, peer PeerSessionObservation) {
	t.Helper()
	seen := make(map[uint64]bool)
	previous := make(map[string]uint64)
	for _, window := range peer.BlackholeWindows {
		for _, segment := range window.Segments {
			if !segment.Valid || uint64(len(segment.ReceivedSequences)) != segment.Count ||
				len(segment.ReceivedSequences) != len(segment.OffsetsNS) ||
				len(segment.ReceivedSequences) != len(segment.Sequences) {
				t.Fatalf("Echo omission lost its actual accepted inputs: %+v", segment)
			}
			for _, sequence := range segment.ReceivedSequences {
				if sequence == 0 || sequence > peer.Read || sequence <= previous[segment.Direction] || seen[sequence] {
					t.Fatalf("Echo omission input is absent, repeated or reversed: %+v", segment)
				}
				previous[segment.Direction], seen[sequence] = sequence, true
			}
		}
	}
}

func TestEchoThousandMixedIdentityPlan(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("reference")
	if err != nil {
		t.Fatal(err)
	}
	profile.Echo = true
	cfg, err := LoopbackConfig(profile)
	if err != nil || len(cfg.Sessions) != 1000 {
		t.Fatalf("full Echo reference identities were not retained: count=%d: %v", len(cfg.Sessions), err)
	}
	locals := make(map[netip.Addr]bool)
	for _, endpoint := range cfg.Sessions {
		locals[endpoint.Local.Addr()] = true
		if endpoint.Local.Port() != 3785 || endpoint.Peer.Port() != 3785 {
			t.Fatalf("Echo identity retained Control ports: %+v", endpoint)
		}
	}
	if len(locals) != 2 {
		t.Fatalf("Echo plan allocated unnecessary local identities: %d", len(locals))
	}
}

func TestEchoControlledWorkloadRetainsActualPhaseLedgerAndIncarnations(t *testing.T) {
	for _, churn := range []bool{false, true} {
		t.Run(map[bool]string{false: "blackhole", true: "churn"}[churn], func(t *testing.T) {
			profile, err := DefaultProfile("fixture")
			if err != nil {
				t.Fatal(err)
			}
			profile.Echo, profile.Sessions = true, 1
			profile.Duration = 1500 * time.Millisecond
			profile.BlackholeEvery, profile.BlackholeFor = 500*time.Millisecond, 400*time.Millisecond
			if churn {
				profile.SessionChurn, profile.Duration = true, 6*time.Second
				profile.BlackholeEvery, profile.BlackholeFor = 0, 0
			}
			owner, peers := runControlledWorkload(t, profile, 0)
			if !owner.Initial.Echo || !owner.MeasurementFinal.Echo || !owner.Final.Echo ||
				!owner.DownJournal.Complete || !owner.DownJournal.IncarnationsComplete ||
				peers.Peers[0].RemoteDiscriminator != 0 || peers.Peers[0].UpTransitions != 0 ||
				owner.ReferenceQualified || owner.Final.TimingUncertaintyNS != nil {
				t.Fatalf("Echo phase/physical identity or uncertainty was fabricated: %+v", owner.DownJournal)
			}
			if churn {
				if owner.Final.Sessions[0].Generation != 2 || owner.SessionChurn.Recovered == 0 {
					t.Fatal("Echo churn did not prove actual generation replacement and return recovery")
				}
				return
			}
			if len(owner.DownWitnesses) == 0 {
				t.Fatal("Echo blackhole was not observed through the production Down journal")
			}
			for _, witness := range owner.DownWitnesses {
				if witness.Type != bfd.SessionTypeEcho || !witness.Incarnation.Known ||
					!witness.Timing.ValidatedKnown || !witness.Timing.DetectionKnown || witness.RemoteKnown {
					t.Fatalf("actual Echo owner witness lost its typed identity/deadline: %+v", witness)
				}
			}
		})
	}
}
