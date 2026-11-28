//go:build linux

package netio

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

type receiveTimingSender struct{}

func (receiveTimingSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func TestReceiveTimingRealUDPCommit(t *testing.T) {
	t.Parallel()
	for _, family := range loopbackFamilies() {
		for _, byDiscriminator := range []bool{false, true} {
			t.Run(family.name+"/"+map[bool]string{false: "peer", true: "discriminator"}[byDiscriminator], func(t *testing.T) {
				t.Parallel()
				conn := timestampTestConn(t, family, false, unix.SO_TIMESTAMPNS_NEW)
				ln := NewListenerFromConn(&LinuxPacketConn{conn: conn}, false)
				reg := prometheus.NewRegistry()
				metrics := bfdmetrics.NewCollector(reg)
				logger := slog.New(slog.DiscardHandler)
				mgr := bfd.NewManager(logger, bfd.WithManagerMetrics(metrics))
				t.Cleanup(mgr.Close)
				session, err := mgr.CreateSession(t.Context(), bfd.SessionConfig{
					PeerAddr: family.addr, LocalAddr: family.addr, IfIndex: loopbackIndex(t),
					Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
					DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
				}, func() (*bfd.SenderLease, error) { return bfd.NewSenderLease(receiveTimingSender{}, nil), nil })
				if err != nil {
					t.Fatal(err)
				}
				pkt := bfd.ControlPacket{
					Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, MyDiscriminator: 17,
					DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
				}
				if byDiscriminator {
					pkt.YourDiscriminator = session.LocalDiscriminator()
				}
				var payload [bfd.HeaderSize]byte
				n, err := bfd.MarshalControlPacket(&pkt, payload[:])
				if err != nil {
					t.Fatal(err)
				}
				sendLoopback(t, family, conn.LocalAddr().(*net.UDPAddr).AddrPort().Port(), 255, payload[:n])
				if err := NewReceiver(mgr, logger).recvOne(t.Context(), ln); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					var metric dto.Metric
					if err := metrics.ReceiveCommitLatency.Write(&metric); err != nil {
						t.Fatal(err)
					}
					if metric.GetHistogram().GetSampleCount() == 1 {
						if metric.GetHistogram().GetSampleSum() <= 0 || session.State() != bfd.StateInit {
							t.Fatalf("latency=%v state=%s", metric.GetHistogram(), session.State())
						}
						var counter dto.Metric
						if err := metrics.ReceiveTimingOutcomes.WithLabelValues("valid").Write(&counter); err != nil {
							t.Fatal(err)
						}
						if counter.GetCounter().GetValue() != 1 {
							t.Fatal("missing committed timing denominator")
						}
						t.Logf("UDP input=1, owner commits=1, valid samples=1, latency_seconds=%g",
							metric.GetHistogram().GetSampleSum())
						return
					}
					select {
					case <-ctx.Done():
						t.Fatalf("no actual kernel RX->owner commit sample: %v", metric.GetHistogram())
					case <-ticker.C:
					}
				}
			})
		}
	}
}
