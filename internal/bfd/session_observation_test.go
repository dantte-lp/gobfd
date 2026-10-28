package bfd

import (
	"net/netip"
	"testing"
	"time"
)

type causalMetrics struct {
	noopMetrics

	observe func()
}

func (m causalMetrics) RecordStateTransition(_, _ netip.Addr, _, _ string) { m.observe() }

func TestCausalObserversSeeCommittedPair(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	observed := false
	s.metrics = causalMetrics{observe: func() {
		observed = true
		packet, snapshot := s.buildControlPacket(), snapshotOf(s)
		if packet.State != StateDown || packet.Diag != DiagNeighborDown ||
			snapshot.State != packet.State || snapshot.LocalDiag != packet.Diag || !snapshot.RemoteKnown ||
			snapshot.RemoteState != StateAdminDown || snapshot.Cause != CauseRemoteAdminDown {
			t.Fatalf("observer ran before causal commit: packet=%+v snapshot=%+v", packet, snapshot)
		}
	}}
	pkt := s.buildControlPacket()
	pkt.State = StateAdminDown
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
	if !observed {
		t.Fatal("state transition observer did not run")
	}
}

func TestCausalRemoteAndSameStateVersions(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	ch := make(chan StateChange, 10)
	s.notifyCh = ch
	pkt := s.buildControlPacket()
	pkt.State, pkt.Diag, pkt.ControlPlaneIndependent = StateAdminDown, DiagAdminDown, true
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
	first := s.Observation()
	sc := <-ch
	snap := snapshotOf(s)
	if first.State != StateDown || first.LocalDiag != DiagNeighborDown || !first.RemoteKnown ||
		first.RemoteState != StateAdminDown || first.RemoteDiag != DiagAdminDown || !first.RemoteC || first.LocalC ||
		first.Cause != CauseRemoteAdminDown || first.ObservationVersion <= 1 ||
		sc.ObservationVersion != first.ObservationVersion || sc.RemoteState != first.RemoteState ||
		sc.Diag != first.LocalDiag ||
		snap.ObservationVersion != first.ObservationVersion || snap.RemoteDiag != first.RemoteDiag {
		t.Fatalf("incoherent causal facts: observation=%+v event=%+v snapshot=%+v", first, sc, snap)
	}
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
	if got := s.Observation(); got != first || len(ch) != 0 {
		t.Fatalf("unchanged packet changed observation: %+v, queued=%d", got, len(ch))
	}
	edges, changedAt := s.StateTransitions(), s.LastStateChange()
	s.handleDetectTimer(t.Context(), tx, detect)
	timed := s.Observation()
	if timed.LocalDiag != DiagControlTimeExpired || timed.RemoteState != StateAdminDown || !timed.RemoteKnown ||
		timed.Cause != CauseDetectionTimeout || timed.ObservationVersion <= first.ObservationVersion ||
		s.StateTransitions() != edges || s.LastStateChange() != changedAt {
		t.Fatalf("timeout lost administrative fact or counted an edge: %+v", timed)
	}
	<-ch
	s.handleDetectTimer(t.Context(), tx, detect)
	if s.Observation() != timed || len(ch) != 0 {
		t.Fatal("repeated timeout republished unchanged facts")
	}
	s.handlePathDown(t.Context(), tx, detect)
	path := <-ch
	if path.Cause != CausePathDown || path.Diag != DiagPathDown || path.OldState != StateDown ||
		path.NewState != StateDown || s.StateTransitions() != edges || s.LastStateChange() != changedAt {
		t.Fatalf("same-state explicit PathDown lost: %+v", path)
	}
	s.handleDetectTimer(t.Context(), tx, detect)
	timeoutPath := <-ch
	if timeoutPath.Cause != CausePathDown || timeoutPath.Diag != DiagControlTimeExpired ||
		timeoutPath.ObservationVersion <= path.ObservationVersion || s.StateTransitions() != edges {
		t.Fatalf("timeout erased explicit local path failure: %+v", timeoutPath)
	}
	pkt.State = StateUp // Ignored by the Down FSM; this is not a new PathDown command.
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
	if got := s.Observation(); got.LocalDiag != DiagControlTimeExpired || got.Cause != CausePathDown {
		t.Fatalf("ignored received state changed the retained diagnostic: %+v", got)
	}
}

func TestCausalRecoveryPreservesDiagnostic(t *testing.T) {
	t.Parallel()
	s, sender, tx, detect := newRemoteTxTestSession(t)
	s.handleDetectTimer(t.Context(), tx, detect)
	pkt := s.buildControlPacket()
	if pkt.State != StateDown || pkt.Diag != DiagControlTimeExpired {
		t.Fatalf("timeout packet: %+v", pkt)
	}
	pkt.State = StateInit
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
	if s.State() != StateUp || s.LocalDiag() != DiagControlTimeExpired {
		t.Fatalf("recovery = %+v", s.Observation())
	}
	var sent ControlPacket
	if err := UnmarshalControlPacket(sender.packets[len(sender.packets)-1], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.State != StateUp || sent.Diag != DiagControlTimeExpired {
		t.Fatalf("recovery wire packet: %+v", sent)
	}
}

func TestCausalDiagnosticPublication(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		state State
		event Event
		diag  Diag
	}{
		{"timeout-up", StateUp, EventTimerExpired, DiagControlTimeExpired},
		{"timeout-down", StateDown, EventTimerExpired, DiagControlTimeExpired},
		{"neighbor-down", StateUp, EventRecvDown, DiagNeighborDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, _, tx, detect := newRemoteTxTestSession(t)
			s.mu.Lock()
			s.localPair.Store(packStateDiag(tc.state, DiagNone))
			s.observation.State = tc.state
			s.mu.Unlock()
			s.cachedState = tc.state
			ch := make(chan StateChange, 2)
			s.notifyCh = ch
			s.applyFSMEvent(t.Context(), tc.event, tx, detect)
			select {
			case sc := <-ch:
				if sc.Diag != tc.diag || sc.NewState != StateDown {
					t.Fatalf("published state/diag = %s/%s, want Down/%s", sc.NewState, sc.Diag, tc.diag)
				}
			default:
				t.Fatal("semantic diagnostic update was not published")
			}
		})
	}
}

func TestCausalAdminPathDownGuard(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	s.handleAdminDown(t.Context(), make(chan struct{}), tx, detect)
	s.handlePathDown(t.Context(), tx, detect)
	if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown {
		t.Fatalf("selected graceful AdminDown policy changed: %s/%s", s.State(), s.LocalDiag())
	}
}

func TestCausalRemoteAdminAlreadyDown(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	s.handleDetectTimer(t.Context(), tx, detect)
	before, edges := s.Observation(), s.StateTransitions()
	ch := make(chan StateChange, 2)
	s.notifyCh = ch
	pkt := s.buildControlPacket()
	pkt.State, pkt.Diag = StateAdminDown, DiagAdminDown
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
	got := s.Observation()
	if got.State != StateDown || got.LocalDiag != DiagControlTimeExpired || !got.RemoteKnown ||
		got.RemoteState != StateAdminDown || got.Cause != CauseRemoteAdminDown ||
		got.ObservationVersion <= before.ObservationVersion || s.StateTransitions() != edges || len(ch) != 1 {
		t.Fatalf("same-state remote administrative observation: %+v, events=%d", got, len(ch))
	}
}

// TestDownTimeoutWithoutReceivedPacketStaysSilent covers RFC 5880 Section
// 6.8.4: Detection Time expiry takes a session down only from Init or Up. A
// Down session that never received a packet reports no state change and keeps
// diagnostic 0, while the Section 6.8.1 and 6.8.18 remote resets still apply.
func TestDownTimeoutWithoutReceivedPacketStaysSilent(t *testing.T) {
	t.Parallel()
	s := newCachedPacketTestSession(MaxPacketSize)
	s.sender = &retryCachedPacketSender{}
	s.remoteDiscr, s.remoteMinRxInterval, s.remoteDemandMode = 7, 10*time.Second, true
	ch := make(chan StateChange, 2)
	s.notifyCh = ch
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	t.Cleanup(func() { tx.Stop(); detect.Stop() })
	before, edges := s.Observation(), s.StateTransitions()

	s.handleDetectTimer(t.Context(), tx, detect)

	if len(ch) != 0 || s.LocalDiag() != DiagNone || s.Observation() != before || s.StateTransitions() != edges {
		t.Fatalf("Down timeout without a received packet: diag=%s events=%d observation=%+v",
			s.LocalDiag(), len(ch), s.Observation())
	}
	if pkt := s.buildControlPacket(); pkt.State != StateDown || pkt.Diag != DiagNone {
		t.Fatalf("wire packet after silent timeout: %s/%s", pkt.State, pkt.Diag)
	}
	if s.remoteDiscr != 0 || s.remoteMinRxInterval != initialRemoteMinRx || s.remoteDemandMode {
		t.Fatalf("remote reset lost: discr=%d rx=%s demand=%t",
			s.remoteDiscr, s.remoteMinRxInterval, s.remoteDemandMode)
	}
}
