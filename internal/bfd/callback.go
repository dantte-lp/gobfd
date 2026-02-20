package bfd

import "net/netip"

// StateCallback is a function invoked when a BFD session changes state.
//
// External systems (e.g., GoBGP integration, OSPF notification) register
// callbacks to react to BFD session events such as Up->Down transitions
// that should trigger route withdrawal.
//
// Callbacks are invoked synchronously by the consumer goroutine. Long-running
// operations should be dispatched asynchronously to avoid blocking the
// notification pipeline.
//
// Usage with Manager.StateChanges():
//
//	go func() {
//	    for change := range mgr.StateChanges() {
//	        for _, cb := range callbacks {
//	            cb(change)
//	        }
//	    }
//	}()
//
// The Manager exposes state change notifications via the StateChanges() channel.
// External consumers read from this channel and invoke registered callbacks.
// This decoupled design avoids import cycles between the bfd package and
// protocol-specific integration packages (e.g., internal/gobgp).
//
// For BFD flap dampening (RFC 5882 Section 3.2), the callback consumer
// should implement exponential backoff before propagating rapid Down->Up->Down
// oscillations to routing protocols.
type StateCallback func(change StateChange)

// MetricsReporter is a consumer interface for reporting BFD protocol metrics.
//
// This interface lives in the bfd package (near consumers) to break the
// import cycle between bfd and the metrics package. The bfdmetrics.Collector
// implements this interface via an adapter. Methods are designed for the
// hot path: all parameters are primitive or value types to avoid allocation.
//
// Implementations MUST be safe for concurrent use -- Session goroutines and
// the receiver goroutine may call methods concurrently from multiple sessions.
//
// A nil MetricsReporter is never passed to a session; the Manager wraps nil
// collectors with a no-op implementation.
type MetricsReporter interface {
	// IncPacketsSent increments the transmitted packets counter.
	// Called after each successful BFD Control packet transmission.
	IncPacketsSent(peer, local netip.Addr)

	// IncPacketsReceived increments the received packets counter.
	// Called after each successfully demultiplexed BFD Control packet.
	IncPacketsReceived(peer, local netip.Addr)

	// IncPacketsDropped increments the dropped packets counter.
	// Called when a packet fails validation or cannot be delivered.
	IncPacketsDropped(peer, local netip.Addr)

	// RecordStateTransition increments the state transition counter.
	// Called on each FSM state change with the old and new state names.
	RecordStateTransition(peer, local netip.Addr, from, to string)

	// RegisterSession increments the active sessions gauge.
	// Called when a new BFD session is created by the Manager.
	RegisterSession(peer, local netip.Addr, sessionType string)

	// UnregisterSession decrements the active sessions gauge.
	// Called when a BFD session is destroyed by the Manager.
	UnregisterSession(peer, local netip.Addr, sessionType string)
}

// noopMetrics is a no-op implementation of MetricsReporter used when no
// metrics collector is configured. All methods are empty -- zero overhead
// on the hot path beyond the interface dispatch.
type noopMetrics struct{}

func (noopMetrics) IncPacketsSent(_, _ netip.Addr)                     {}
func (noopMetrics) IncPacketsReceived(_, _ netip.Addr)                 {}
func (noopMetrics) IncPacketsDropped(_, _ netip.Addr)                  {}
func (noopMetrics) RecordStateTransition(_, _ netip.Addr, _, _ string) {}
func (noopMetrics) RegisterSession(_, _ netip.Addr, _ string)          {}
func (noopMetrics) UnregisterSession(_, _ netip.Addr, _ string)        {}
