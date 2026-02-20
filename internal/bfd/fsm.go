package bfd

// This file implements the BFD Finite State Machine (RFC 5880 Section 6.2,
// Section 6.8.6). The FSM is implemented as a pure function over a transition
// table -- no side effects, no Session dependency. This makes it trivially
// testable and auditable against the RFC pseudocode.
//
// State diagram (RFC 5880 Section 6.2):
//
//                          +--+
//                          |  | UP, ADMIN DOWN, TIMER
//                          |  V
//                  DOWN  +------+  INIT
//           +------------|      |------------+
//           |            | DOWN |            |
//           |  +-------->|      |<--------+  |
//           |  |         +------+         |  |
//           |  |                          |  |
//           |  |               ADMIN DOWN,|  |
//           |  |ADMIN DOWN,          DOWN,|  |
//           |  |TIMER                TIMER|  |
//           V  |                          |  V
//         +------+                      +------+
//    +----|      |                      |      |----+
// DOWN    | INIT |--------------------->|  UP  |    INIT, UP
//    +--->|      | INIT, UP             |      |<---+
//         +------+                      +------+

// Event represents a BFD FSM event (RFC 5880 Section 6.2, Section 6.8.6).
type Event uint8

const (
	// EventRecvAdminDown is the event for receiving a BFD Control packet
	// with State = AdminDown (RFC 5880 Section 6.8.6).
	EventRecvAdminDown Event = iota

	// EventRecvDown is the event for receiving a BFD Control packet
	// with State = Down (RFC 5880 Section 6.8.6).
	EventRecvDown

	// EventRecvInit is the event for receiving a BFD Control packet
	// with State = Init (RFC 5880 Section 6.8.6).
	EventRecvInit

	// EventRecvUp is the event for receiving a BFD Control packet
	// with State = Up (RFC 5880 Section 6.8.6).
	EventRecvUp

	// EventTimerExpired is the event when the Detection Time expires without
	// receiving a valid packet (RFC 5880 Section 6.8.4).
	EventTimerExpired

	// EventAdminDown is the event for a local administrative action to
	// disable the session (RFC 5880 Section 6.8.16).
	EventAdminDown

	// EventAdminUp is the event for a local administrative action to
	// re-enable the session (RFC 5880 Section 6.8.16).
	EventAdminUp
)

// String returns the human-readable name of the event.
func (e Event) String() string {
	switch e {
	case EventRecvAdminDown:
		return "RecvAdminDown"
	case EventRecvDown:
		return "RecvDown"
	case EventRecvInit:
		return "RecvInit"
	case EventRecvUp:
		return "RecvUp"
	case EventTimerExpired:
		return "TimerExpired"
	case EventAdminDown:
		return "AdminDown"
	case EventAdminUp:
		return "AdminUp"
	default:
		return "Unknown"
	}
}

// Action represents a side-effect to execute after an FSM transition.
// Actions are returned as part of FSMResult and executed by the caller
// (typically Session.applyEvent). The FSM itself is a pure function.
type Action uint8

const (
	// ActionSendControl triggers immediate transmission of a BFD Control packet.
	// RFC 5880 Section 6.8.7.
	ActionSendControl Action = iota + 1

	// ActionNotifyUp signals session consumers that the session reached Up state.
	ActionNotifyUp

	// ActionNotifyDown signals session consumers that the session went Down.
	ActionNotifyDown

	// ActionSetDiagTimeExpired sets bfd.LocalDiag to 1 (Control Detection Time Expired).
	// RFC 5880 Section 6.8.4.
	ActionSetDiagTimeExpired

	// ActionSetDiagNeighborDown sets bfd.LocalDiag to 3 (Neighbor Signaled Session Down).
	// RFC 5880 Section 6.8.6.
	ActionSetDiagNeighborDown

	// ActionSetDiagAdminDown sets bfd.LocalDiag to 7 (Administratively Down).
	// RFC 5880 Section 6.8.16.
	ActionSetDiagAdminDown
)

// String returns the human-readable name of the action.
func (a Action) String() string {
	switch a {
	case ActionSendControl:
		return "SendControl"
	case ActionNotifyUp:
		return "NotifyUp"
	case ActionNotifyDown:
		return "NotifyDown"
	case ActionSetDiagTimeExpired:
		return "SetDiagTimeExpired"
	case ActionSetDiagNeighborDown:
		return "SetDiagNeighborDown"
	case ActionSetDiagAdminDown:
		return "SetDiagAdminDown"
	default:
		return "Unknown"
	}
}

// stateEvent is the FSM transition table key: current state + incoming event.
type stateEvent struct {
	state State
	event Event
}

// transition describes the target state and side-effects for a single
// FSM transition.
type transition struct {
	newState State
	actions  []Action
}

// FSMResult holds the outcome of applying an event to the FSM.
// The caller inspects Changed to decide whether state-change processing
// (logging, metrics, notifications) is needed.
type FSMResult struct {
	// OldState is the state before the event was applied.
	OldState State

	// NewState is the state after the event was applied.
	// Equal to OldState when the event is ignored or a self-loop.
	NewState State

	// Actions lists the side-effects that the caller must execute.
	// Empty when the event is ignored.
	Actions []Action

	// Changed is true when NewState differs from OldState.
	// Self-loops (e.g., Up + RecvUp -> Up) have Changed=false.
	Changed bool
}

// fsmTable is the complete BFD FSM transition table.
//
// Derived from RFC 5880 Section 6.8.6 pseudocode and the state diagram
// in Section 6.2. Every (state, event) pair listed here is a valid
// transition. Unlisted pairs are silently ignored (event dropped).
//
// The pseudocode logic maps to events as follows:
//
//	AdminDown:    discard all received packets
//	RecvAdminDown + !Down: Diag=3, State=Down
//	Down + RecvDown:       State=Init
//	Down + RecvInit:       State=Up
//	Init + RecvInit|Up:    State=Up
//	Up + RecvDown:         Diag=3, State=Down
//	TimerExpired + Init|Up: Diag=1, State=Down
//
//nolint:gochecknoglobals // FSM transition table is intentionally package-level.
var fsmTable = map[stateEvent]transition{
	// ===================================================================
	// AdminDown state
	// ===================================================================
	//
	// RFC 5880 Section 6.8.6: "If bfd.SessionState is AdminDown, discard
	// the packet." -- No received-packet events produce transitions.
	// Only administrative re-enable can leave AdminDown.

	// AdminDown + AdminUp -> Down
	// RFC 5880 Section 6.8.16: "Set bfd.SessionState to Down".
	{StateAdminDown, EventAdminUp}: {
		newState: StateDown,
		actions:  nil,
	},

	// ===================================================================
	// Down state
	// ===================================================================
	//
	// RFC 5880 Section 6.8.6: "If bfd.SessionState is Down":
	//   "If received State is Down" -> set bfd.SessionState to Init
	//   "Else if received State is Init" -> set bfd.SessionState to Up
	//
	// Down + recv AdminDown: remain Down (already Down, no-op).
	// Not listed because state does not change and no actions are needed.
	//
	// Down + recv Up: not listed in the pseudocode for state Down.
	// The RFC only handles Down and Init when local state is Down.
	// Receiving Up while in Down is implicitly ignored.
	//
	// Down + timer expired: Down is the initial state; detection timer
	// self-loop on the state diagram (Section 6.2: "UP, ADMIN DOWN, TIMER"
	// arc on Down). No state change, no actions.

	// Down + recv Down -> Init (RFC 5880 Section 6.8.6).
	{StateDown, EventRecvDown}: {
		newState: StateInit,
		actions:  []Action{ActionSendControl},
	},

	// Down + recv Init -> Up (RFC 5880 Section 6.8.6).
	{StateDown, EventRecvInit}: {
		newState: StateUp,
		actions:  []Action{ActionSendControl, ActionNotifyUp},
	},

	// Down + AdminDown -> AdminDown (RFC 5880 Section 6.8.16).
	{StateDown, EventAdminDown}: {
		newState: StateAdminDown,
		actions:  []Action{ActionSetDiagAdminDown},
	},

	// ===================================================================
	// Init state
	// ===================================================================
	//
	// RFC 5880 Section 6.8.6 for Init:
	//   "If received state is AdminDown" -> if not Down, set Diag=3, state=Down
	//   "If received State is Init or Up" -> set bfd.SessionState to Up
	//
	// RFC 5880 Section 6.2 diagram: Init has self-loops for DOWN and
	// transitions to Up for INIT/UP. ADMIN DOWN and TIMER go to Down.

	// Init + recv AdminDown -> Down (RFC 5880 Section 6.8.6).
	// "If received state is AdminDown" and "bfd.SessionState is not Down":
	// set bfd.LocalDiag to 3, set bfd.SessionState to Down.
	{StateInit, EventRecvAdminDown}: {
		newState: StateDown,
		actions:  []Action{ActionSetDiagNeighborDown, ActionNotifyDown},
	},

	// Init + recv Down -> remain Init (RFC 5880 Section 6.2 diagram: "DOWN"
	// self-loop on Init). The pseudocode in Section 6.8.6 does not list
	// any transition for Init + Down (the "If bfd.SessionState is Down"
	// branch does not apply when local state is Init).
	{StateInit, EventRecvDown}: {
		newState: StateInit,
		actions:  nil,
	},

	// Init + recv Init -> Up (RFC 5880 Section 6.8.6:
	// "Else if bfd.SessionState is Init, if received State is Init or Up").
	{StateInit, EventRecvInit}: {
		newState: StateUp,
		actions:  []Action{ActionSendControl, ActionNotifyUp},
	},

	// Init + recv Up -> Up (RFC 5880 Section 6.8.6:
	// "Else if bfd.SessionState is Init, if received State is Init or Up").
	{StateInit, EventRecvUp}: {
		newState: StateUp,
		actions:  []Action{ActionSendControl, ActionNotifyUp},
	},

	// Init + timer expired -> Down (RFC 5880 Section 6.8.4:
	// "if bfd.SessionState is Init or Up" -> set state to Down, Diag=1).
	{StateInit, EventTimerExpired}: {
		newState: StateDown,
		actions:  []Action{ActionSetDiagTimeExpired, ActionNotifyDown},
	},

	// Init + AdminDown -> AdminDown (RFC 5880 Section 6.8.16).
	{StateInit, EventAdminDown}: {
		newState: StateAdminDown,
		actions:  []Action{ActionSetDiagAdminDown},
	},

	// ===================================================================
	// Up state
	// ===================================================================
	//
	// RFC 5880 Section 6.8.6 for Up:
	//   "If received state is AdminDown" -> Diag=3, state=Down
	//   "If received State is Down" -> Diag=3, state=Down
	//   Init and Up are self-loops (state diagram Section 6.2: "INIT, UP").
	//
	// RFC 5880 Section 6.8.4: timer expired -> Down, Diag=1.

	// Up + recv AdminDown -> Down (RFC 5880 Section 6.8.6:
	// "If received state is AdminDown" and "bfd.SessionState is not Down").
	{StateUp, EventRecvAdminDown}: {
		newState: StateDown,
		actions:  []Action{ActionSetDiagNeighborDown, ActionNotifyDown},
	},

	// Up + recv Down -> Down (RFC 5880 Section 6.8.6:
	// "Else (bfd.SessionState is Up), if received State is Down":
	// set bfd.LocalDiag to 3, set bfd.SessionState to Down).
	{StateUp, EventRecvDown}: {
		newState: StateDown,
		actions:  []Action{ActionSetDiagNeighborDown, ActionNotifyDown},
	},

	// Up + recv Init -> Up (self-loop, RFC 5880 Section 6.2 diagram:
	// "INIT, UP" arc on Up state). No transition listed in pseudocode.
	{StateUp, EventRecvInit}: {
		newState: StateUp,
		actions:  nil,
	},

	// Up + recv Up -> Up (self-loop, RFC 5880 Section 6.2 diagram:
	// "INIT, UP" arc on Up state). Normal keepalive path.
	{StateUp, EventRecvUp}: {
		newState: StateUp,
		actions:  nil,
	},

	// Up + timer expired -> Down (RFC 5880 Section 6.8.4:
	// "if bfd.SessionState is Init or Up" -> Diag=1, state=Down).
	{StateUp, EventTimerExpired}: {
		newState: StateDown,
		actions:  []Action{ActionSetDiagTimeExpired, ActionNotifyDown},
	},

	// Up + AdminDown -> AdminDown (RFC 5880 Section 6.8.16).
	{StateUp, EventAdminDown}: {
		newState: StateAdminDown,
		actions:  []Action{ActionSetDiagAdminDown},
	},
}

// ApplyEvent applies an FSM event to the given state and returns the result.
//
// This is a pure function with no side effects. The caller is responsible
// for executing the returned actions (setting diagnostics, sending packets,
// emitting notifications). If the (state, event) pair has no entry in the
// transition table, the event is silently ignored and FSMResult.Changed is
// false with an empty action list.
//
// Reference: RFC 5880 Section 6.8.6 (reception FSM transitions),
// Section 6.8.4 (timer expiration), Section 6.8.16 (administrative control).
func ApplyEvent(currentState State, event Event) FSMResult {
	key := stateEvent{state: currentState, event: event}

	tr, ok := fsmTable[key]
	if !ok {
		// Event is not applicable in this state. Per RFC 5880 Section 6.8.6,
		// AdminDown discards all received packets; Down ignores recv Up and
		// timer expiration. Return unchanged.
		return FSMResult{
			OldState: currentState,
			NewState: currentState,
			Actions:  nil,
			Changed:  false,
		}
	}

	return FSMResult{
		OldState: currentState,
		NewState: tr.newState,
		Actions:  tr.actions,
		Changed:  currentState != tr.newState,
	}
}

// RecvStateToEvent maps a received BFD session state (from the State field
// of a BFD Control packet) to the corresponding FSM event. This simplifies
// the packet reception path in Session.processPacket.
//
// Reference: RFC 5880 Section 6.8.6 — the received State field drives
// the FSM transitions.
func RecvStateToEvent(remoteState State) Event {
	switch remoteState {
	case StateAdminDown:
		return EventRecvAdminDown
	case StateDown:
		return EventRecvDown
	case StateInit:
		return EventRecvInit
	case StateUp:
		return EventRecvUp
	default:
		// Unknown state value: treat as Down for safety.
		// RFC 5880 Section 4.1 defines only 4 state values (0-3).
		return EventRecvDown
	}
}
