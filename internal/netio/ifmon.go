package netio

import (
	"context"
	"log/slog"
)

// -------------------------------------------------------------------------
// Interface Monitor — network interface state change detection
// -------------------------------------------------------------------------

// InterfaceEvent represents a network interface state change.
// These events are used by the BFD session manager to detect link failures
// and trigger immediate session teardown (faster than detection timer expiry).
type InterfaceEvent struct {
	// IfName is the network interface name (e.g., "eth0", "bond0").
	IfName string

	// IfIndex is the kernel interface index.
	IfIndex int

	// Up indicates whether the interface transitioned to Up (true) or
	// Down (false). This maps to IFF_UP | IFF_RUNNING in the kernel.
	Up bool
}

// InterfaceMonitor watches for network interface state changes and emits
// events when interfaces go up or down.
//
// Implementations may use NETLINK_ROUTE (Linux), kqueue (BSD), or polling
// as the underlying mechanism. The interface is kept minimal so that the
// BFD session manager can react to link events without depending on a
// specific OS mechanism.
//
// Usage:
//
//	mon := netio.NewStubInterfaceMonitor(logger)
//	events := mon.Events()
//	go func() {
//	    for ev := range events {
//	        handleLinkChange(ev)
//	    }
//	}()
//	mon.Run(ctx) // blocks until ctx is cancelled
type InterfaceMonitor interface {
	// Run starts monitoring interface state changes. It blocks until ctx
	// is cancelled. Detected events are sent to the channel returned by
	// Events(). Run must be called at most once.
	Run(ctx context.Context) error

	// Events returns a read-only channel that receives interface state
	// change events. The channel is created at construction time and is
	// closed when Run returns. Callers should drain the channel after
	// Run completes.
	Events() <-chan InterfaceEvent

	// Close releases any resources held by the monitor. If Run is still
	// active, the caller should cancel the context first.
	Close() error
}

// -------------------------------------------------------------------------
// StubInterfaceMonitor — no-op implementation
// -------------------------------------------------------------------------

// StubInterfaceMonitor is a no-op implementation of InterfaceMonitor that
// never emits events. It is used when no platform-specific monitor is
// available or when interface monitoring is disabled.
//
// A future implementation will use mdlayher/netlink with NETLINK_ROUTE
// to subscribe to RTM_NEWLINK / RTM_DELLINK messages for real-time
// interface state tracking on Linux.
type StubInterfaceMonitor struct {
	events chan InterfaceEvent
	logger *slog.Logger
}

// NewStubInterfaceMonitor creates a no-op interface monitor.
func NewStubInterfaceMonitor(logger *slog.Logger) *StubInterfaceMonitor {
	return &StubInterfaceMonitor{
		events: make(chan InterfaceEvent, 16),
		logger: logger.With(slog.String("component", "ifmon.stub")),
	}
}

// Run blocks until ctx is cancelled. The stub implementation does not
// emit any events; it simply waits for cancellation and closes the
// events channel.
func (m *StubInterfaceMonitor) Run(ctx context.Context) error {
	m.logger.Info("stub interface monitor started (no-op)")
	<-ctx.Done()
	close(m.events)
	m.logger.Info("stub interface monitor stopped")
	return nil
}

// Events returns the (always empty) event channel.
func (m *StubInterfaceMonitor) Events() <-chan InterfaceEvent {
	return m.events
}

// Close is a no-op for the stub monitor.
func (m *StubInterfaceMonitor) Close() error {
	return nil
}
