package bfd

import (
	"bytes"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

func TestFullWatchOwnerStatusIsFrozen(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	ch := make(chan StateChange, 4)
	s.notifyCh = ch
	pkt := s.buildControlPacket()
	pkt.State, pkt.Diag, pkt.ControlPlaneIndependent = StateAdminDown, DiagAdminDown, true
	s.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
	first := <-ch
	want := first.Status
	if want.State != StateDown || !want.RemoteKnown || want.RemoteState != StateAdminDown ||
		want.RemoteDiag != DiagAdminDown || !want.RemoteC || want.Cause != CauseRemoteAdminDown ||
		want.Counters.PacketsReceived != 1 || want.Counters.StateTransitions != 1 ||
		want.LastPacketReceived.IsZero() || want.LastStateChange.IsZero() ||
		want.DesiredMinTx != s.DesiredMinTxInterval() {
		t.Fatalf("owner status not captured at publication: %+v", want)
	}
	pkt.State, pkt.Diag, pkt.ControlPlaneIndependent = StateDown, DiagNone, false
	pkt.RequiredMinRxInterval = 900000
	s.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
	s.mu.Lock()
	s.desiredMinTxInterval = 2 * time.Second
	s.mu.Unlock()
	s.packetsSent.Add(7)
	if latest := snapshotOf(s); latest.ObservationVersion <= want.ObservationVersion || latest == want {
		t.Fatalf("later receive/timer/counter mutation did not change status: %+v", latest)
	}
	if first.Status != want {
		t.Fatalf("delayed event changed: got %+v, want %+v", first.Status, want)
	}
	// Even a delayed publication uses its explicit commit, never the latest tuple.
	committed := Observation{
		State: want.State, LocalDiag: want.LocalDiag, RemoteKnown: want.RemoteKnown,
		RemoteState: want.RemoteState, RemoteDiag: want.RemoteDiag, LocalC: want.LocalC,
		RemoteC: want.RemoteC, Cause: want.Cause, ObservationVersion: want.ObservationVersion,
	}
	s.emitNotification(FSMResult{OldState: StateUp, NewState: committed.State}, committed)
	<-ch // Later receive event.
	late := <-ch
	if late.Status.State != committed.State || late.Status.RemoteState != committed.RemoteState ||
		late.Status.LocalDiag != committed.LocalDiag || late.Status.RemoteKnown != committed.RemoteKnown ||
		late.Status.RemoteDiag != committed.RemoteDiag || late.Status.LocalC != committed.LocalC ||
		late.Status.RemoteC != committed.RemoteC || late.Status.Cause != committed.Cause ||
		late.Status.ObservationVersion != committed.ObservationVersion ||
		late.Status.DesiredMinTx != 2*time.Second {
		t.Fatalf("explicit commit replaced by latest tuple: %+v", late.Status)
	}
}

func TestFullWatchNotificationCaptureAllocations(t *testing.T) {
	s := newCachedPacketTestSession(MaxPacketSize)
	ch := make(chan StateChange, 1)
	s.notifyCh = ch
	observation := s.Observation()
	allocs := testing.AllocsPerRun(100, func() {
		s.emitNotification(FSMResult{OldState: StateDown, NewState: StateDown}, observation)
		<-ch
	})
	if allocs != 0 {
		t.Fatalf("active notification capture = %v allocs/op, want 0", allocs)
	}
}

func TestFullWatchRegistrationKeepsLegacyChannelEmpty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		mgr := NewManager(slog.New(slog.NewTextHandler(&logs, nil)))
		defer mgr.Close()
		for i := range notifyChSize + 1 {
			cfg := senderLeaseTestConfig("192.0.2.1")
			cfg.PeerAddr = netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)})
			cfg.Role = RolePassive
			if _, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory()); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if len(mgr.StateChanges()) != 0 || bytes.Contains(logs.Bytes(), []byte("level=WARN")) {
			t.Fatalf("registration filled legacy channel or warned: queued=%d logs=%s", len(mgr.StateChanges()), &logs)
		}
	})
}
