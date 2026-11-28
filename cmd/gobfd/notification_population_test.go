package main

import (
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestConfiguredNotificationPopulationPreservesRealStartupBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const population = 160
		cfg := config.DefaultConfig()
		for index := range population {
			peer := netip.AddrFrom4([4]byte{127, 42, 0, byte(index + 1)})
			cfg.Sessions = append(cfg.Sessions, config.SessionConfig{
				Peer: peer.String(), Local: "127.0.0.1", Type: "single_hop",
				DesiredMinTx: time.Second, RequiredMinRx: time.Second, DetectMult: 3,
			})
		}
		manager, err := newManager(cfg, bfdmetrics.NewCollector(prometheus.NewRegistry()), nil, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		changes, err := manager.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for index, endpoint := range cfg.Sessions {
			session, err := manager.CreateSession(t.Context(), bfd.SessionConfig{
				LocalAddr: netip.MustParseAddr(endpoint.Local), PeerAddr: netip.MustParseAddr(endpoint.Peer),
				Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
				DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
			}, candidateTestSenderLeaseFactory())
			if err != nil {
				t.Fatal(err)
			}
			packet := bfd.ControlPacket{
				Version: bfd.Version, Length: bfd.MinPacketSizeNoAuth, DetectMult: 3,
				State: bfd.StateDown, MyDiscriminator: uint32(index + 1),
				DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 1000000,
			}
			if session.RecvPacket(&packet) != bfd.DeliveryQueued {
				t.Fatal("actual Down packet was refused")
			}
			synctest.Wait()
			packet.State, packet.YourDiscriminator = bfd.StateInit, session.LocalDiscriminator()
			if session.RecvPacket(&packet) != bfd.DeliveryQueued {
				t.Fatal("actual Init packet was refused")
			}
			synctest.Wait()
		}
		go manager.RunDispatch(t.Context())
		synctest.Wait()
		seen := make(map[uint32][]bfd.StateChange)
		for range 3 * population {
			select {
			case change := <-changes:
				if change.Gap || change.ObservationVersion == 0 || change.LocalDiscr == 0 {
					t.Fatalf("configured population lost a real lifecycle edge: %+v", change)
				}
				seen[change.LocalDiscr] = append(seen[change.LocalDiscr], change)
			default:
				t.Fatal("configured population did not retain every registration/Init/Up edge")
			}
		}
		if len(seen) != population {
			t.Fatalf("startup stream lost a physical owner: %d", len(seen))
		}
		for _, edges := range seen {
			if len(edges) != 3 || !edges[0].Added || edges[1].NewState != bfd.StateInit ||
				edges[2].NewState != bfd.StateUp || edges[1].ObservationVersion >= edges[2].ObservationVersion ||
				edges[0].Ref != edges[1].Ref || edges[1].Ref != edges[2].Ref {
				t.Fatalf("startup stream duplicated or omitted an incarnation edge: %+v", edges)
			}
		}
	})
}

func TestConfiguredNotificationPopulationCountsEnabledProducers(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Sessions = make([]config.SessionConfig, 2)
			cfg.Echo.Enabled, cfg.Echo.Peers = enabled, make([]config.EchoPeerConfig, 3)
			cfg.MicroBFD.Enabled = enabled
			cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{MemberLinks: []string{"eth0", "eth1"}}}
			cfg.VXLAN.Enabled, cfg.VXLAN.Peers = enabled, make([]config.VXLANPeerConfig, 1)
			cfg.Geneve.Enabled, cfg.Geneve.Peers = enabled, make([]config.GenevePeerConfig, 4)
			cfg.Unsolicited.Enabled, cfg.Unsolicited.MaxSessions = enabled, 7
			option, err := notificationPopulationOption(cfg)
			if err != nil {
				t.Fatal(err)
			}
			manager := bfd.NewManager(slog.New(slog.DiscardHandler), option)
			defer manager.Close()
			changes, err := manager.SubscribeStateChanges(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := 70
			if enabled {
				want = 121
			}
			if cap(changes) != want {
				t.Fatalf("configured notification reserve=%d, want %d", cap(changes), want)
			}
		})
	}
}
