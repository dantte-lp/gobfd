package bfdmetrics_test

import (
	"context"
	"log/slog"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestReceiveTimingMetricsBounded(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg)
	for outcome := range bfd.ReceiveTimingOutcomeCount {
		c.ObserveReceiveTiming(outcome, 5*time.Millisecond)
	}
	c.ObserveReceiveTiming(bfd.ReceiveTimingOutcomeCount, time.Second)
	c.ObserveReceiveTiming(bfd.ReceiveTimingValid, -time.Nanosecond)
	for outcome := range bfd.ReceiveTimingOutcomeCount {
		want := float64(1)
		if outcome == bfd.ReceiveTimingInvalid {
			want = 2
		}
		if got := counterValue(t, c.ReceiveTimingOutcomes, outcome.String()); got != want {
			t.Fatalf("outcome=%s count=%v want=%v", outcome, got, want)
		}
	}
	var metric dto.Metric
	if err := c.ReceiveCommitLatency.Write(&metric); err != nil {
		t.Fatal(err)
	}
	if metric.GetHistogram().GetSampleCount() != 1 || metric.GetHistogram().GetSampleSum() != 0.005 {
		t.Fatalf("invalid timing polluted latency histogram: %v", metric.GetHistogram())
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() != "gobfd_bfd_control_rx_timing_total" {
			continue
		}
		found = true
		if len(family.GetMetric()) != int(bfd.ReceiveTimingOutcomeCount) {
			t.Fatalf("unbounded outcomes: %v", family)
		}
		for _, sample := range family.GetMetric() {
			if len(sample.GetLabel()) != 1 || sample.GetLabel()[0].GetName() != "outcome" {
				t.Fatalf("unexpected labels: %v", sample.GetLabel())
			}
		}
	}
	if !found {
		t.Fatal("timing denominator not exported")
	}
}

func BenchmarkReceiveTimingMetrics(b *testing.B) {
	c := bfdmetrics.NewCollector(prometheus.NewRegistry())
	allocs := testing.AllocsPerRun(100, func() { c.ObserveReceiveTiming(bfd.ReceiveTimingValid, time.Millisecond) })
	if allocs != 0 {
		b.Fatalf("timing metric allocations=%g, want0", allocs)
	}
	b.ReportAllocs()
	for b.Loop() {
		c.ObserveReceiveTiming(bfd.ReceiveTimingValid, time.Millisecond)
	}
}

type receiveOwnerMetrics struct {
	*bfdmetrics.Collector

	commits atomic.Uint64
	valid   atomic.Uint64
}

func (m *receiveOwnerMetrics) ObserveReceiveTiming(outcome bfd.ReceiveTimingOutcome, latency time.Duration) {
	m.Collector.ObserveReceiveTiming(outcome, latency)
	if outcome == bfd.ReceiveTimingValid {
		m.valid.Add(1)
	}
	m.commits.Add(1)
}

type receiveOwnerSender struct{}

func (receiveOwnerSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

// BenchmarkReceiveOwnerCollector waits for every owner commit with real metrics;
// its synthetic clock sample is not kernel RX or a latency-distribution claim.
func BenchmarkReceiveOwnerCollector(b *testing.B) {
	benchmarkReceiveOwnerCollector(b, bfd.AuthTypeNone)
}

func BenchmarkReceiveOwnerCollectorAuthentication(b *testing.B) {
	for typ := bfd.AuthTypeSimplePassword; typ <= bfd.AuthTypeMeticulousKeyedSHA1; typ++ {
		b.Run(typ.String(), func(b *testing.B) { benchmarkReceiveOwnerCollector(b, typ) })
	}
}

func benchmarkReceiveOwnerCollector(b *testing.B, typ bfd.AuthType) {
	b.Helper()
	var auth bfd.Authenticator
	var keys bfd.AuthKeyStore
	if typ != bfd.AuthTypeNone {
		var authErr error
		auth, authErr = bfd.NewAuthenticator(typ)
		if authErr != nil {
			b.Fatal(authErr)
		}
		keys, authErr = bfd.NewStaticAuthKeyStore(bfd.AuthKey{
			ID: 1, Type: typ, Secret: []byte("benchmark-secret"),
		})
		if authErr != nil {
			b.Fatal(authErr)
		}
	}
	metrics := &receiveOwnerMetrics{Collector: bfdmetrics.NewCollector(prometheus.NewRegistry())}
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithManagerMetrics(metrics))
	b.Cleanup(mgr.Close)
	peer, local := testPeers()
	session, err := mgr.CreateSession(b.Context(), bfd.SessionConfig{
		PeerAddr: peer, LocalAddr: local, IfIndex: 1, Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
		Auth: auth, AuthKeys: keys,
	}, func() (*bfd.SenderLease, error) { return bfd.NewSenderLease(receiveOwnerSender{}, nil), nil })
	if err != nil {
		b.Fatal(err)
	}
	pkt := bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateUp, DetectMult: 3, MyDiscriminator: 7,
		YourDiscriminator: session.LocalDiscriminator(), DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	meta := bfd.PacketMeta{SrcAddr: peer, DstAddr: local, TTL: 255, IfIndex: 1}
	clock := bfd.NewReceiveClock()
	var count uint64
	var signer bfd.AuthState
	buf := make([]byte, bfd.MaxPacketSize)
	deliver := func() {
		meta.ReceiveTiming = clock.Capture(time.Now().UnixNano())
		var wire []byte
		if auth != nil {
			if signErr := auth.Sign(&signer, keys, &pkt, buf, len(buf)); signErr != nil {
				b.Fatal(signErr)
			}
			n, marshalErr := bfd.MarshalControlPacket(&pkt, buf)
			if marshalErr != nil {
				b.Fatal(marshalErr)
			}
			var decoded bfd.ControlPacket
			if decodeErr := bfd.UnmarshalControlPacket(buf[:n], &decoded); decodeErr != nil {
				b.Fatal(decodeErr)
			}
			pkt, wire = decoded, buf[:n]
		}
		outcome, demuxErr := mgr.DemuxWithWire(pkt, meta, wire)
		if demuxErr != nil || outcome != bfd.DeliveryQueued {
			b.Fatalf("admission=%s error=%v", outcome, demuxErr)
		}
		count++
		deadline := time.Now().Add(5 * time.Second)
		for metrics.commits.Load() < count {
			if time.Now().After(deadline) {
				b.Fatal("queued packet did not reach owner commit")
			}
			runtime.Gosched()
		}
	}
	if auth != nil {
		// Reach steady Up before timing; remote Sign advances meticulous sequences.
		pkt.State = bfd.StateDown
		deliver()
		pkt.State = bfd.StateInit
		deliver()
		pkt.State = bfd.StateUp
		if session.State() != bfd.StateUp {
			b.Fatal("authenticated fixture did not reach Up")
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		deliver()
	}
	if metrics.commits.Load() != count {
		b.Fatal("noncommitted input counted")
	}
	if count == 0 || metrics.valid.Load() != count {
		b.Fatalf("valid timing samples=%d, committed inputs=%d", metrics.valid.Load(), count)
	}
	if session.PacketsReceived() != count || auth != nil && session.State() != bfd.StateUp {
		b.Fatal("owner receive count or steady state changed")
	}
}
