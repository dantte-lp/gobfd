package netio_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// InterfaceEvent Tests
// -------------------------------------------------------------------------

func TestInterfaceEvent_Fields(t *testing.T) {
	t.Parallel()

	ev := netio.InterfaceEvent{
		IfName:  "eth0",
		IfIndex: 2,
		Up:      true,
	}

	if ev.IfName != "eth0" {
		t.Errorf("IfName = %s, want eth0", ev.IfName)
	}
	if ev.IfIndex != 2 {
		t.Errorf("IfIndex = %d, want 2", ev.IfIndex)
	}
	if !ev.Up {
		t.Error("Up should be true")
	}
}

func TestInterfaceEvent_Down(t *testing.T) {
	t.Parallel()

	ev := netio.InterfaceEvent{
		IfName:  "bond0",
		IfIndex: 5,
		Up:      false,
	}

	if ev.Up {
		t.Error("Up should be false for link-down event")
	}
}

// -------------------------------------------------------------------------
// StubInterfaceMonitor Tests
// -------------------------------------------------------------------------

func TestStubInterfaceMonitor_RunBlocksUntilCancel(t *testing.T) {
	t.Parallel()

	mon := netio.NewStubInterfaceMonitor(slog.Default())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- mon.Run(ctx)
	}()

	// Cancel should unblock Run.
	cancel()

	err := <-done
	if err != nil {
		t.Errorf("Run should return nil: %v", err)
	}
}

func TestStubInterfaceMonitor_EventsChannelClosed(t *testing.T) {
	t.Parallel()

	mon := netio.NewStubInterfaceMonitor(slog.Default())
	events := mon.Events()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		_ = mon.Run(ctx)
		close(done)
	}()

	cancel()
	<-done

	// Events channel should be closed after Run returns.
	_, open := <-events
	if open {
		t.Error("events channel should be closed after Run returns")
	}
}

func TestStubInterfaceMonitor_EventsChannelNoEvents(t *testing.T) {
	t.Parallel()

	mon := netio.NewStubInterfaceMonitor(slog.Default())
	events := mon.Events()

	// Non-blocking check: stub should never produce events.
	select {
	case ev, ok := <-events:
		if ok {
			t.Errorf("stub should not produce events, got: %+v", ev)
		}
	default:
		// Good: no events available.
	}
}

func TestStubInterfaceMonitor_CloseNoOp(t *testing.T) {
	t.Parallel()

	mon := netio.NewStubInterfaceMonitor(slog.Default())

	// Close should not panic or error.
	if err := mon.Close(); err != nil {
		t.Errorf("Close should return nil: %v", err)
	}

	// Double close should also be safe.
	if err := mon.Close(); err != nil {
		t.Errorf("double Close should return nil: %v", err)
	}
}

func TestStubInterfaceMonitor_EventsNotNil(t *testing.T) {
	t.Parallel()

	mon := netio.NewStubInterfaceMonitor(slog.Default())

	if mon.Events() == nil {
		t.Error("Events() should return a non-nil channel")
	}
}
