//go:build linux

package bfdload

import (
	"context"
	"fmt"
	"math"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func (c *Control) ownerStart(ctx context.Context, owner *Owner) (*phaseStart, error) {
	readyCtx, cancel := context.WithTimeout(ctx, controlReadyTimeout)
	defer cancel()
	if err := c.wait(readyCtx, func() (bool, error) {
		owner.inventory.RLock()
		defer owner.inventory.RUnlock()
		for _, entry := range owner.sessions {
			if entry.session == nil || entry.session.State() != bfd.StateUp {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		return nil, err
	}
	if err := c.publish("owner-ready", "ready\n"); err != nil {
		return nil, err
	}
	phase, err := c.awaitStart(readyCtx)
	if err != nil {
		return nil, err
	}
	return phase, nil
}

func (c *Control) drainOwner(ctx context.Context, owner *Owner) error {
	drainCtx, cancel := context.WithTimeout(ctx, controlDrainTimeout)
	defer cancel()
	expected, err := c.expectedWrites(drainCtx)
	if err != nil {
		return err
	}
	var last uint64
	waitErr := c.wait(drainCtx, func() (bool, error) {
		observed, snapshotErr := owner.Snapshot(drainCtx)
		if snapshotErr != nil {
			return false, snapshotErr
		}
		count := observed.TerminalPackets
		last = count
		if count > expected {
			return false, fmt.Errorf("owner terminal packets %d exceed actual peer writes %d: %w", count, expected, ErrControl)
		}
		return count == expected, nil
	})
	if waitErr != nil {
		return fmt.Errorf("drain owner terminal packets %d of peer writes %d: %w", last, expected, waitErr)
	}
	return nil
}

func terminalPacketCount(observed OwnerObservation) (uint64, error) {
	var count uint64
	for _, row := range observed.Delivery {
		terminal := terminalDelivery(row)
		if observed.Echo {
			terminal = terminalEchoDelivery(row)
		}
		if terminal {
			if row.Count > math.MaxUint64-count {
				return 0, fmt.Errorf("terminal packet counter overflow: %w", ErrControl)
			}
			count += row.Count
		}
	}
	drops, err := ownerSocketDrops(observed)
	if err != nil {
		return 0, err
	}
	if drops > math.MaxUint64-count {
		return 0, fmt.Errorf("kernel drop counter overflow: %w", ErrControl)
	}
	return count + drops, nil
}

func terminalDelivery(row bfd.DeliveryCounter) bool {
	switch row.Source {
	case bfd.DeliveryWireParse:
		return row.Outcome == bfd.DeliveryParserRejected
	case bfd.DeliveryBaseTTL:
		return row.Outcome == bfd.DeliveryTTLRejected
	case bfd.DeliveryDemux:
		return row.Outcome == bfd.DeliveryUnknownSession || row.Outcome == bfd.DeliveryScopeMismatch ||
			row.Outcome == bfd.DeliveryTTLRejected
	case bfd.DeliveryPacketAdmission:
		return row.Outcome == bfd.DeliveryQueueFull || row.Outcome == bfd.DeliveryClosed
	case bfd.DeliveryPacketOwner:
		return true
	default:
		return false
	}
}

func terminalEchoDelivery(row bfd.DeliveryCounter) bool {
	switch row.Source {
	case bfd.DeliveryEchoTTL:
		return row.Outcome == bfd.DeliveryTTLRejected
	case bfd.DeliveryEchoParse:
		return row.Outcome == bfd.DeliveryParserRejected
	case bfd.DeliveryEchoDemux:
		return row.Outcome == bfd.DeliveryUnknownSession
	case bfd.DeliveryEchoAdmission:
		return row.Outcome == bfd.DeliveryQueueFull || row.Outcome == bfd.DeliveryClosed
	case bfd.DeliveryEchoOwner:
		return row.Outcome == bfd.DeliveryCommitted || row.Outcome == bfd.DeliveryAbandoned
	default:
		return false
	}
}

func ownerSocketDrops(observed OwnerObservation) (uint64, error) {
	const largestExactCounter = uint64(1) << 53
	kind := "control"
	if observed.Echo {
		kind = "echo"
	}
	for _, family := range observed.Metrics {
		if family.GetName() != "gobfd_bfd_socket_drops_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := metric.GetLabel()
			if len(labels) != 1 || labels[0].GetName() != "kind" || labels[0].GetValue() != kind {
				continue
			}
			drops := metric.GetCounter().GetValue()
			if math.IsNaN(drops) || drops < 0 || drops >= float64(largestExactCounter) || drops != math.Trunc(drops) {
				return 0, fmt.Errorf("invalid kernel drop counter: %w", ErrControl)
			}
			return uint64(drops), nil
		}
	}
	return 0, nil
}
