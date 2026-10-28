//go:build linux

package bfdload

import (
	"errors"
	"math"
	"os"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestTerminalPacketAccountingDoesNotCountPipelineAdmission(t *testing.T) {
	t.Parallel()
	rows := []bfd.DeliveryCounter{
		{Source: bfd.DeliveryWireParse, Outcome: bfd.DeliveryAccepted, Count: 100},
		{Source: bfd.DeliveryBaseTTL, Outcome: bfd.DeliveryAccepted, Count: 100},
		{Source: bfd.DeliveryDemux, Outcome: bfd.DeliveryFound, Count: 100},
		{Source: bfd.DeliveryPacketAdmission, Outcome: bfd.DeliveryQueued, Count: 100},
		{Source: bfd.DeliveryWireParse, Outcome: bfd.DeliveryParserRejected, Count: 1},
		{Source: bfd.DeliveryBaseTTL, Outcome: bfd.DeliveryTTLRejected, Count: 2},
		{Source: bfd.DeliveryDemux, Outcome: bfd.DeliveryUnknownSession, Count: 3},
		{Source: bfd.DeliveryPacketAdmission, Outcome: bfd.DeliveryQueueFull, Count: 4},
		{Source: bfd.DeliveryPacketOwner, Outcome: bfd.DeliveryCommitted, Count: 5},
		{Source: bfd.DeliveryPacketOwner, Outcome: bfd.DeliveryAbandoned, Count: 6},
	}
	count, err := terminalPacketCount(OwnerObservation{Delivery: rows})
	if err != nil || count != 21 {
		t.Fatalf("terminal count=%d error=%v", count, err)
	}
	rows[0] = bfd.DeliveryCounter{
		Source: bfd.DeliveryPacketOwner, Outcome: bfd.DeliveryCommitted, Count: math.MaxUint64,
	}
	if _, countErr := terminalPacketCount(OwnerObservation{Delivery: rows}); !errors.Is(countErr, ErrControl) {
		t.Fatalf("overflow accepted: %v", countErr)
	}
}

func TestTerminalSocketDropCounterRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	name, labelName, labelValue := "gobfd_bfd_socket_drops_total", "kind", "control"
	value := float64(5)
	metric := &dto.Metric{Label: []*dto.LabelPair{{Name: &labelName, Value: &labelValue}}, Counter: &dto.Counter{Value: &value}}
	observed := OwnerObservation{Metrics: []*dto.MetricFamily{{Name: &name, Metric: []*dto.Metric{metric}}}}
	count, err := terminalPacketCount(observed)
	if err != nil || count != 5 {
		t.Fatalf("drop count=%d error=%v", count, err)
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), -1, 1.5, float64(uint64(1) << 53)} {
		metric.Counter.Value = &invalid
		if _, countErr := terminalPacketCount(observed); !errors.Is(countErr, ErrControl) {
			t.Fatalf("invalid drop value %f accepted: %v", invalid, countErr)
		}
	}
}

func TestControlRejectsSharedDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	control, err := OpenControl(directory)
	if control != nil {
		if closeErr := control.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if !errors.Is(err, ErrControl) {
		t.Fatalf("shared control directory accepted: %v", err)
	}
}
