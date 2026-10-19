package bfdmetrics_test

import (
	"log/slog"
	"net/netip"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestControlDeliveryUntrustedPeersKeepFixedMetricSeries(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(reg)
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithManagerMetrics(collector))
	defer mgr.Close()
	pkt := &bfd.ControlPacket{State: bfd.StateDown, MyDiscriminator: 1}
	for i := range 1000 {
		meta := bfd.PacketMeta{SrcAddr: netip.AddrFrom4([4]byte{192, 0, byte(i >> 8), byte(i % 256)})}
		if outcome, err := mgr.Demux(pkt, meta); outcome != bfd.DeliveryUnknownSession || err == nil {
			t.Fatalf("unknown peer outcome=%s err=%v", outcome, err)
		}
	}
	collector.RecordDelivery(bfd.DeliverySource(255), bfd.DeliveryOutcome(255))
	collector.RecordDelivery(bfd.DeliveryWireParse, bfd.DeliveryAbandoned)
	want := make(map[[2]string]uint64)
	for _, row := range mgr.DeliveryCounters() {
		want[[2]string{row.Source.String(), row.Outcome.String()}] = row.Count
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		switch family.GetName() {
		case "gobfd_bfd_packets_dropped_total", "gobfd_bfd_auth_failures_total":
			t.Error("untrusted peers created session metric labels")
		case "gobfd_bfd_delivery_outcomes_total":
			found = true
			if len(family.GetMetric()) != len(want) {
				t.Fatalf("delivery series=%d, want fixed %d", len(family.GetMetric()), len(want))
			}
			for _, metric := range family.GetMetric() {
				var key [2]string
				for _, label := range metric.GetLabel() {
					switch label.GetName() {
					case "source":
						key[0] = label.GetValue()
					case "outcome":
						key[1] = label.GetValue()
					default:
						t.Errorf("unbounded delivery label %q", label.GetName())
					}
				}
				count, ok := want[key]
				if !ok || len(metric.GetLabel()) != 2 || metric.GetCounter().GetValue() != float64(count) {
					t.Errorf("Prometheus/ledger mismatch for %v: %v, want %d", key, metric.GetCounter(), count)
				}
			}
		}
	}
	if !found || want[[2]string{"demux", "unknown_session"}] != 1000 {
		t.Fatalf("missing process ledger: %+v", want)
	}
}
