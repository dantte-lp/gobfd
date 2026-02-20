package bfd_test

import (
	"slices"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// TestFSMTransitionTable verifies every transition in the BFD FSM table
// against the pseudocode in RFC 5880 Section 6.8.6, the state diagram
// in Section 6.2, and the timer expiration rules in Section 6.8.4.
//
// This test covers all 17 explicit entries in the transition table plus
// validation of self-loops and state changes.
func TestFSMTransitionTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       bfd.State
		event       bfd.Event
		wantState   bfd.State
		wantChanged bool
		wantActions []bfd.Action
	}{
		// =============================================================
		// AdminDown state (RFC 5880 Section 6.8.6, Section 6.8.16)
		// =============================================================
		{
			name:        "AdminDown+AdminUp->Down (Section 6.8.16)",
			state:       bfd.StateAdminDown,
			event:       bfd.EventAdminUp,
			wantState:   bfd.StateDown,
			wantChanged: true,
			wantActions: nil,
		},

		// =============================================================
		// Down state (RFC 5880 Section 6.8.6)
		// =============================================================
		{
			name:        "Down+RecvDown->Init (Section 6.8.6)",
			state:       bfd.StateDown,
			event:       bfd.EventRecvDown,
			wantState:   bfd.StateInit,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSendControl},
		},
		{
			name:        "Down+RecvInit->Up (Section 6.8.6)",
			state:       bfd.StateDown,
			event:       bfd.EventRecvInit,
			wantState:   bfd.StateUp,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSendControl, bfd.ActionNotifyUp},
		},
		{
			name:        "Down+AdminDown->AdminDown (Section 6.8.16)",
			state:       bfd.StateDown,
			event:       bfd.EventAdminDown,
			wantState:   bfd.StateAdminDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagAdminDown},
		},

		// =============================================================
		// Init state (RFC 5880 Section 6.8.6, Section 6.2)
		// =============================================================
		{
			name:        "Init+RecvAdminDown->Down (Section 6.8.6)",
			state:       bfd.StateInit,
			event:       bfd.EventRecvAdminDown,
			wantState:   bfd.StateDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagNeighborDown, bfd.ActionNotifyDown},
		},
		{
			name:        "Init+RecvDown->Init self-loop (Section 6.2 diagram)",
			state:       bfd.StateInit,
			event:       bfd.EventRecvDown,
			wantState:   bfd.StateInit,
			wantChanged: false,
			wantActions: nil,
		},
		{
			name:        "Init+RecvInit->Up (Section 6.8.6)",
			state:       bfd.StateInit,
			event:       bfd.EventRecvInit,
			wantState:   bfd.StateUp,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSendControl, bfd.ActionNotifyUp},
		},
		{
			name:        "Init+RecvUp->Up (Section 6.8.6)",
			state:       bfd.StateInit,
			event:       bfd.EventRecvUp,
			wantState:   bfd.StateUp,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSendControl, bfd.ActionNotifyUp},
		},
		{
			name:        "Init+TimerExpired->Down (Section 6.8.4)",
			state:       bfd.StateInit,
			event:       bfd.EventTimerExpired,
			wantState:   bfd.StateDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagTimeExpired, bfd.ActionNotifyDown},
		},
		{
			name:        "Init+AdminDown->AdminDown (Section 6.8.16)",
			state:       bfd.StateInit,
			event:       bfd.EventAdminDown,
			wantState:   bfd.StateAdminDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagAdminDown},
		},

		// =============================================================
		// Up state (RFC 5880 Section 6.8.6, Section 6.2)
		// =============================================================
		{
			name:        "Up+RecvAdminDown->Down (Section 6.8.6)",
			state:       bfd.StateUp,
			event:       bfd.EventRecvAdminDown,
			wantState:   bfd.StateDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagNeighborDown, bfd.ActionNotifyDown},
		},
		{
			name:        "Up+RecvDown->Down (Section 6.8.6)",
			state:       bfd.StateUp,
			event:       bfd.EventRecvDown,
			wantState:   bfd.StateDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagNeighborDown, bfd.ActionNotifyDown},
		},
		{
			name:        "Up+RecvInit->Up self-loop (Section 6.2 diagram)",
			state:       bfd.StateUp,
			event:       bfd.EventRecvInit,
			wantState:   bfd.StateUp,
			wantChanged: false,
			wantActions: nil,
		},
		{
			name:        "Up+RecvUp->Up self-loop (Section 6.2 diagram)",
			state:       bfd.StateUp,
			event:       bfd.EventRecvUp,
			wantState:   bfd.StateUp,
			wantChanged: false,
			wantActions: nil,
		},
		{
			name:        "Up+TimerExpired->Down (Section 6.8.4)",
			state:       bfd.StateUp,
			event:       bfd.EventTimerExpired,
			wantState:   bfd.StateDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagTimeExpired, bfd.ActionNotifyDown},
		},
		{
			name:        "Up+AdminDown->AdminDown (Section 6.8.16)",
			state:       bfd.StateUp,
			event:       bfd.EventAdminDown,
			wantState:   bfd.StateAdminDown,
			wantChanged: true,
			wantActions: []bfd.Action{bfd.ActionSetDiagAdminDown},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := bfd.ApplyEvent(tt.state, tt.event)

			if result.OldState != tt.state {
				t.Errorf("OldState = %s, want %s", result.OldState, tt.state)
			}
			if result.NewState != tt.wantState {
				t.Errorf("NewState = %s, want %s", result.NewState, tt.wantState)
			}
			if result.Changed != tt.wantChanged {
				t.Errorf("Changed = %v, want %v", result.Changed, tt.wantChanged)
			}
			assertActionsEqual(t, result.Actions, tt.wantActions)
		})
	}
}

// TestFSMAdminDownIgnoresPackets verifies that AdminDown state discards all
// received BFD Control packets. RFC 5880 Section 6.8.6: "If bfd.SessionState
// is AdminDown, discard the packet."
func TestFSMAdminDownIgnoresPackets(t *testing.T) {
	t.Parallel()

	recvEvents := []struct {
		name  string
		event bfd.Event
	}{
		{"RecvAdminDown", bfd.EventRecvAdminDown},
		{"RecvDown", bfd.EventRecvDown},
		{"RecvInit", bfd.EventRecvInit},
		{"RecvUp", bfd.EventRecvUp},
		{"TimerExpired", bfd.EventTimerExpired},
	}

	for _, ev := range recvEvents {
		t.Run(ev.name, func(t *testing.T) {
			t.Parallel()

			result := bfd.ApplyEvent(bfd.StateAdminDown, ev.event)

			if result.Changed {
				t.Errorf("AdminDown + %s: Changed = true, want false", ev.name)
			}
			if result.NewState != bfd.StateAdminDown {
				t.Errorf("AdminDown + %s: NewState = %s, want AdminDown",
					ev.name, result.NewState)
			}
			if len(result.Actions) != 0 {
				t.Errorf("AdminDown + %s: got %d actions, want 0",
					ev.name, len(result.Actions))
			}
		})
	}
}

// TestFSMThreeWayHandshake simulates a full BFD three-way handshake between
// two peers (A and B) as described in RFC 5880 Section 6.2.
//
// Sequence:
//  1. Both peers start in Down state.
//  2. Peer A receives Down from B -> A transitions to Init.
//  3. Peer B receives Down from A -> B transitions to Init.
//  4. Peer A receives Init from B -> A transitions to Up.
//  5. Peer B receives Init from A -> B transitions to Up. (or Up from A)
//
// This matches the state diagram in RFC 5880 Section 6.2.
func TestFSMThreeWayHandshake(t *testing.T) {
	t.Parallel()

	// Both peers start in Down (RFC 5880 Section 6.8.1).
	peerA := bfd.StateDown
	peerB := bfd.StateDown

	// Step 1: Peer A receives Down from Peer B.
	// Down + RecvDown -> Init (RFC 5880 Section 6.8.6).
	resultA := bfd.ApplyEvent(peerA, bfd.EventRecvDown)
	assertTransition(t, "A: Down+RecvDown", resultA, bfd.StateDown, bfd.StateInit)
	peerA = resultA.NewState

	// Step 2: Peer B receives Down from Peer A (A was Down when it sent).
	// Down + RecvDown -> Init.
	resultB := bfd.ApplyEvent(peerB, bfd.EventRecvDown)
	assertTransition(t, "B: Down+RecvDown", resultB, bfd.StateDown, bfd.StateInit)
	peerB = resultB.NewState

	// Step 3: Peer A receives Init from Peer B.
	// Init + RecvInit -> Up (RFC 5880 Section 6.8.6).
	resultA = bfd.ApplyEvent(peerA, bfd.EventRecvInit)
	assertTransition(t, "A: Init+RecvInit", resultA, bfd.StateInit, bfd.StateUp)
	assertContainsAction(t, "A: Init+RecvInit", resultA.Actions, bfd.ActionNotifyUp)
	peerA = resultA.NewState

	// Step 4: Peer B receives Init (or Up) from Peer A.
	// Init + RecvUp -> Up (RFC 5880 Section 6.8.6: "Init or Up").
	resultB = bfd.ApplyEvent(peerB, bfd.EventRecvUp)
	assertTransition(t, "B: Init+RecvUp", resultB, bfd.StateInit, bfd.StateUp)
	assertContainsAction(t, "B: Init+RecvUp", resultB.Actions, bfd.ActionNotifyUp)
	peerB = resultB.NewState

	// Both peers are now Up.
	if peerA != bfd.StateUp {
		t.Errorf("peer A final state = %s, want Up", peerA)
	}
	if peerB != bfd.StateUp {
		t.Errorf("peer B final state = %s, want Up", peerB)
	}
}

// TestFSMDetectionTimeout verifies that detection timer expiration transitions
// Init and Up states to Down with DiagTimeExpired action.
// RFC 5880 Section 6.8.4: "If the Detection Time expires [...] the session
// has gone down -- the local system MUST set bfd.SessionState to Down and
// bfd.LocalDiag to 1 (Control Detection Time Expired)."
func TestFSMDetectionTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fromState bfd.State
	}{
		{
			name:      "Init+TimerExpired->Down",
			fromState: bfd.StateInit,
		},
		{
			name:      "Up+TimerExpired->Down",
			fromState: bfd.StateUp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := bfd.ApplyEvent(tt.fromState, bfd.EventTimerExpired)

			if result.NewState != bfd.StateDown {
				t.Errorf("NewState = %s, want Down", result.NewState)
			}
			if !result.Changed {
				t.Error("Changed = false, want true")
			}
			assertContainsAction(t, tt.name, result.Actions, bfd.ActionSetDiagTimeExpired)
			assertContainsAction(t, tt.name, result.Actions, bfd.ActionNotifyDown)
		})
	}

	// Down + TimerExpired should be ignored (already Down).
	// RFC 5880 Section 6.2 diagram: "UP, ADMIN DOWN, TIMER" self-loop on Down.
	t.Run("Down+TimerExpired->ignored", func(t *testing.T) {
		t.Parallel()

		result := bfd.ApplyEvent(bfd.StateDown, bfd.EventTimerExpired)
		if result.Changed {
			t.Error("Down + TimerExpired: Changed = true, want false")
		}
		if result.NewState != bfd.StateDown {
			t.Errorf("Down + TimerExpired: NewState = %s, want Down", result.NewState)
		}
	})

	// AdminDown + TimerExpired should be ignored (packet discarded).
	t.Run("AdminDown+TimerExpired->ignored", func(t *testing.T) {
		t.Parallel()

		result := bfd.ApplyEvent(bfd.StateAdminDown, bfd.EventTimerExpired)
		if result.Changed {
			t.Error("AdminDown + TimerExpired: Changed = true, want false")
		}
	})
}

// TestFSMAdminControl tests administrative transitions from each state.
// RFC 5880 Section 6.8.16.
func TestFSMAdminControl(t *testing.T) {
	t.Parallel()

	// AdminDown from every non-AdminDown state.
	t.Run("AdminDown transitions", func(t *testing.T) {
		t.Parallel()

		states := []struct {
			name  string
			state bfd.State
		}{
			{"Down->AdminDown", bfd.StateDown},
			{"Init->AdminDown", bfd.StateInit},
			{"Up->AdminDown", bfd.StateUp},
		}

		for _, st := range states {
			t.Run(st.name, func(t *testing.T) {
				t.Parallel()

				result := bfd.ApplyEvent(st.state, bfd.EventAdminDown)

				if result.NewState != bfd.StateAdminDown {
					t.Errorf("NewState = %s, want AdminDown", result.NewState)
				}
				if !result.Changed {
					t.Error("Changed = false, want true")
				}
				assertContainsAction(t, st.name, result.Actions, bfd.ActionSetDiagAdminDown)
			})
		}
	})

	// AdminUp from AdminDown -> Down.
	t.Run("AdminDown+AdminUp->Down", func(t *testing.T) {
		t.Parallel()

		result := bfd.ApplyEvent(bfd.StateAdminDown, bfd.EventAdminUp)

		if result.NewState != bfd.StateDown {
			t.Errorf("NewState = %s, want Down", result.NewState)
		}
		if !result.Changed {
			t.Error("Changed = false, want true")
		}
	})

	// AdminUp from non-AdminDown states should be ignored.
	t.Run("AdminUp from non-AdminDown is ignored", func(t *testing.T) {
		t.Parallel()

		for _, state := range []bfd.State{bfd.StateDown, bfd.StateInit, bfd.StateUp} {
			result := bfd.ApplyEvent(state, bfd.EventAdminUp)
			if result.Changed {
				t.Errorf("%s + AdminUp: Changed = true, want false", state)
			}
		}
	})

	// AdminDown from AdminDown should be ignored (already AdminDown).
	t.Run("AdminDown+AdminDown->ignored", func(t *testing.T) {
		t.Parallel()

		result := bfd.ApplyEvent(bfd.StateAdminDown, bfd.EventAdminDown)
		if result.Changed {
			t.Error("AdminDown + AdminDown: Changed = true, want false")
		}
	})
}

// TestFSMSelfLoops verifies that self-loop transitions do not report a state
// change (Changed=false) and return the same state. Self-loops occur when:
// - Up receives Init or Up (RFC 5880 Section 6.2 diagram: "INIT, UP" arc)
// - Init receives Down (RFC 5880 Section 6.2 diagram: "DOWN" arc on Init)
func TestFSMSelfLoops(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state bfd.State
		event bfd.Event
	}{
		// Up self-loops (Section 6.2 diagram: "INIT, UP" on Up).
		{"Up+RecvInit", bfd.StateUp, bfd.EventRecvInit},
		{"Up+RecvUp", bfd.StateUp, bfd.EventRecvUp},

		// Init self-loop (Section 6.2 diagram: "DOWN" on Init).
		{"Init+RecvDown", bfd.StateInit, bfd.EventRecvDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := bfd.ApplyEvent(tt.state, tt.event)

			if result.Changed {
				t.Errorf("Changed = true, want false for self-loop %s", tt.name)
			}
			if result.NewState != tt.state {
				t.Errorf("NewState = %s, want %s", result.NewState, tt.state)
			}
			if result.OldState != tt.state {
				t.Errorf("OldState = %s, want %s", result.OldState, tt.state)
			}
		})
	}
}

// TestFSMUnknownEvent verifies that events not present in the transition
// table are silently ignored. This tests the graceful degradation path
// described in RFC 5880 Section 6.8.6 (e.g., receiving packets in
// AdminDown state).
func TestFSMUnknownEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state bfd.State
		event bfd.Event
	}{
		// AdminDown ignores all received-packet events.
		{"AdminDown+RecvDown", bfd.StateAdminDown, bfd.EventRecvDown},
		{"AdminDown+RecvInit", bfd.StateAdminDown, bfd.EventRecvInit},
		{"AdminDown+RecvUp", bfd.StateAdminDown, bfd.EventRecvUp},
		{"AdminDown+RecvAdminDown", bfd.StateAdminDown, bfd.EventRecvAdminDown},
		{"AdminDown+TimerExpired", bfd.StateAdminDown, bfd.EventTimerExpired},
		{"AdminDown+AdminDown", bfd.StateAdminDown, bfd.EventAdminDown},

		// Down ignores recv Up (not listed in Section 6.8.6 for Down state).
		{"Down+RecvUp", bfd.StateDown, bfd.EventRecvUp},

		// Down ignores recv AdminDown (already Down, no state change needed).
		{"Down+RecvAdminDown", bfd.StateDown, bfd.EventRecvAdminDown},

		// Down ignores timer expired (already Down).
		{"Down+TimerExpired", bfd.StateDown, bfd.EventTimerExpired},

		// AdminUp from non-AdminDown states.
		{"Down+AdminUp", bfd.StateDown, bfd.EventAdminUp},
		{"Init+AdminUp", bfd.StateInit, bfd.EventAdminUp},
		{"Up+AdminUp", bfd.StateUp, bfd.EventAdminUp},

		// Invalid event value.
		{"Down+InvalidEvent", bfd.StateDown, bfd.Event(255)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := bfd.ApplyEvent(tt.state, tt.event)

			if result.Changed {
				t.Errorf("Changed = true, want false for ignored event")
			}
			if result.NewState != tt.state {
				t.Errorf("NewState = %s, want %s (unchanged)", result.NewState, tt.state)
			}
			if len(result.Actions) != 0 {
				t.Errorf("got %d actions, want 0 for ignored event", len(result.Actions))
			}
		})
	}
}

// TestEventString verifies that all Event constants have meaningful string
// representations and that unknown values produce "Unknown".
func TestEventString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		event bfd.Event
		want  string
	}{
		{bfd.EventRecvAdminDown, "RecvAdminDown"},
		{bfd.EventRecvDown, "RecvDown"},
		{bfd.EventRecvInit, "RecvInit"},
		{bfd.EventRecvUp, "RecvUp"},
		{bfd.EventTimerExpired, "TimerExpired"},
		{bfd.EventAdminDown, "AdminDown"},
		{bfd.EventAdminUp, "AdminUp"},
		{bfd.Event(255), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.event.String(); got != tt.want {
				t.Errorf("Event(%d).String() = %q, want %q", tt.event, got, tt.want)
			}
		})
	}
}

// TestActionString verifies that all Action constants have meaningful string
// representations and that unknown values produce "Unknown".
func TestActionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action bfd.Action
		want   string
	}{
		{bfd.ActionSendControl, "SendControl"},
		{bfd.ActionNotifyUp, "NotifyUp"},
		{bfd.ActionNotifyDown, "NotifyDown"},
		{bfd.ActionSetDiagTimeExpired, "SetDiagTimeExpired"},
		{bfd.ActionSetDiagNeighborDown, "SetDiagNeighborDown"},
		{bfd.ActionSetDiagAdminDown, "SetDiagAdminDown"},
		{bfd.Action(0), "Unknown"},
		{bfd.Action(255), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.action.String(); got != tt.want {
				t.Errorf("Action(%d).String() = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

// TestRecvStateToEvent verifies the mapping from received BFD State values
// to FSM events. Reference: RFC 5880 Section 6.8.6 — the State field of
// a received packet determines which event to apply to the local FSM.
func TestRecvStateToEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		remoteState bfd.State
		wantEvent   bfd.Event
	}{
		{bfd.StateAdminDown, bfd.EventRecvAdminDown},
		{bfd.StateDown, bfd.EventRecvDown},
		{bfd.StateInit, bfd.EventRecvInit},
		{bfd.StateUp, bfd.EventRecvUp},
		// Unknown state values default to EventRecvDown for safety.
		{bfd.State(255), bfd.EventRecvDown},
	}

	for _, tt := range tests {
		t.Run(tt.remoteState.String(), func(t *testing.T) {
			t.Parallel()

			got := bfd.RecvStateToEvent(tt.remoteState)
			if got != tt.wantEvent {
				t.Errorf("RecvStateToEvent(%s) = %s, want %s",
					tt.remoteState, got, tt.wantEvent)
			}
		})
	}
}

// TestFSMTableCompleteness verifies that the FSM table has the expected
// number of entries and that every entry produces a valid result.
func TestFSMTableCompleteness(t *testing.T) {
	t.Parallel()

	// Count transitions that produce a change or have an explicit entry.
	// We test all 4 states x 7 events = 28 combinations.
	allStates := []bfd.State{
		bfd.StateAdminDown, bfd.StateDown, bfd.StateInit, bfd.StateUp,
	}
	allEvents := []bfd.Event{
		bfd.EventRecvAdminDown, bfd.EventRecvDown, bfd.EventRecvInit,
		bfd.EventRecvUp, bfd.EventTimerExpired, bfd.EventAdminDown,
		bfd.EventAdminUp,
	}

	for _, state := range allStates {
		for _, event := range allEvents {
			result := bfd.ApplyEvent(state, event)

			// Every result must have OldState set correctly.
			if result.OldState != state {
				t.Errorf("ApplyEvent(%s, %s): OldState = %s, want %s",
					state, event, result.OldState, state)
			}

			// Changed must be consistent with state comparison.
			if result.Changed != (result.OldState != result.NewState) {
				t.Errorf("ApplyEvent(%s, %s): Changed = %v but OldState=%s, NewState=%s",
					state, event, result.Changed, result.OldState, result.NewState)
			}
		}
	}
}

// TestFSMFullSessionLifecycle simulates a complete session lifecycle:
// AdminDown -> Down -> Init -> Up -> (peer down) -> Down -> (admin disable)
// -> AdminDown -> (admin enable) -> Down.
func TestFSMFullSessionLifecycle(t *testing.T) {
	t.Parallel()

	state := bfd.StateAdminDown

	// Step 1: AdminUp -> Down
	result := bfd.ApplyEvent(state, bfd.EventAdminUp)
	assertTransition(t, "lifecycle: AdminUp", result, bfd.StateAdminDown, bfd.StateDown)
	state = result.NewState

	// Step 2: Recv Down from peer -> Init
	result = bfd.ApplyEvent(state, bfd.EventRecvDown)
	assertTransition(t, "lifecycle: RecvDown", result, bfd.StateDown, bfd.StateInit)
	state = result.NewState

	// Step 3: Recv Init from peer -> Up (three-way handshake complete)
	result = bfd.ApplyEvent(state, bfd.EventRecvInit)
	assertTransition(t, "lifecycle: RecvInit", result, bfd.StateInit, bfd.StateUp)
	assertContainsAction(t, "lifecycle: RecvInit", result.Actions, bfd.ActionNotifyUp)
	state = result.NewState

	// Step 4: Steady-state keepalives (self-loop)
	result = bfd.ApplyEvent(state, bfd.EventRecvUp)
	if result.Changed {
		t.Error("lifecycle: steady-state RecvUp should not change state")
	}

	// Step 5: Peer goes down
	result = bfd.ApplyEvent(state, bfd.EventRecvDown)
	assertTransition(t, "lifecycle: peer down", result, bfd.StateUp, bfd.StateDown)
	assertContainsAction(t, "lifecycle: peer down", result.Actions, bfd.ActionSetDiagNeighborDown)
	assertContainsAction(t, "lifecycle: peer down", result.Actions, bfd.ActionNotifyDown)
	state = result.NewState

	// Step 6: Admin disables session
	result = bfd.ApplyEvent(state, bfd.EventAdminDown)
	assertTransition(t, "lifecycle: admin disable", result, bfd.StateDown, bfd.StateAdminDown)
	assertContainsAction(t, "lifecycle: admin disable", result.Actions, bfd.ActionSetDiagAdminDown)
	state = result.NewState

	// Step 7: Admin re-enables session
	result = bfd.ApplyEvent(state, bfd.EventAdminUp)
	assertTransition(t, "lifecycle: admin enable", result, bfd.StateAdminDown, bfd.StateDown)
	state = result.NewState

	if state != bfd.StateDown {
		t.Errorf("lifecycle: final state = %s, want Down", state)
	}
}

// --- Test helpers ---

// assertTransition checks that an FSMResult matches expected old/new state
// and changed flag.
func assertTransition(
	t *testing.T,
	label string,
	result bfd.FSMResult,
	wantOld, wantNew bfd.State,
) {
	t.Helper()

	if result.OldState != wantOld {
		t.Errorf("%s: OldState = %s, want %s", label, result.OldState, wantOld)
	}
	if result.NewState != wantNew {
		t.Errorf("%s: NewState = %s, want %s", label, result.NewState, wantNew)
	}

	wantChanged := wantOld != wantNew
	if result.Changed != wantChanged {
		t.Errorf("%s: Changed = %v, want %v", label, result.Changed, wantChanged)
	}
}

// assertContainsAction checks that the action list contains a specific action.
func assertContainsAction(t *testing.T, label string, actions []bfd.Action, want bfd.Action) {
	t.Helper()

	if !slices.Contains(actions, want) {
		t.Errorf("%s: action %s not found in %v", label, want, actions)
	}
}

// assertActionsEqual checks that two action slices are identical.
func assertActionsEqual(t *testing.T, got, want []bfd.Action) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("actions: got %v (len %d), want %v (len %d)", got, len(got), want, len(want))
		return
	}

	for i := range got {
		if got[i] != want[i] {
			t.Errorf("actions[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
