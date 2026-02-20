package bfd

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
