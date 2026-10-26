package bfd

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestAuthExpiryIgnoresWallTelemetry(t *testing.T) {
	t.Parallel()
	for _, offset := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		t.Run(offset.String(), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _, _ := newAuthExpirySession(t)
				s.lastPacketRecv.Store(time.Now().Add(offset).UnixNano())
				detect := s.calcDetectionTimeHot()
				synctest.Sleep(2*detect - time.Nanosecond)
				s.maybeResetAuthSeqKnown()
				if !s.authState.AuthSeqKnown {
					t.Error("AuthSeqKnown reset before 2x boundary")
				}
				synctest.Sleep(time.Nanosecond)
				s.maybeResetAuthSeqKnown()
				if s.authState.AuthSeqKnown {
					t.Error("AuthSeqKnown retained at 2x boundary")
				}
				if s.authState.RcvAuthSeq != 201 {
					t.Errorf("RcvAuthSeq=%d, want 201", s.authState.RcvAuthSeq)
				}
			})
		})
	}
}

func TestAuthExpiryDetectionTimerAllStates(t *testing.T) {
	t.Parallel()
	for _, state := range []State{StateAdminDown, StateDown, StateInit, StateUp} {
		t.Run(state.String(), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, tx, timer := newAuthExpirySession(t)
				s.cachedState = state
				s.mu.Lock()
				s.localPair.Store(packStateDiag(state, DiagNone))
				s.observation.State = state
				s.mu.Unlock()
				// An exported wall timestamp must not keep authentication alive.
				s.lastPacketRecv.Store(time.Now().Add(24 * time.Hour).UnixNano())
				detection := s.calcDetectionTimeHot()
				timer.Reset(detection)
				synctest.Sleep(detection)
				<-timer.C
				s.handleDetectTimer(t.Context(), tx, timer)
				if !s.authState.AuthSeqKnown {
					t.Error("AuthSeqKnown reset at first detection timeout")
				}
				synctest.Sleep(detection - time.Nanosecond)
				if !s.authState.AuthSeqKnown {
					t.Error("AuthSeqKnown reset before 2x boundary")
				}
				synctest.Sleep(time.Nanosecond)
				<-timer.C
				s.handleDetectTimer(t.Context(), tx, timer)
				if s.authState.AuthSeqKnown || s.authState.RcvAuthSeq != 201 {
					t.Errorf("auth state after 2x timeout=%+v", s.authState)
				}
			})
		})
	}
}

func TestAuthExpiryLatePacketBeforeTimeoutDispatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, tx, detect := newAuthExpirySession(t)
		detection := s.calcDetectionTimeHot()
		detect.Reset(detection)
		restarted := signedAuthExpiryPacket(t, s, 100)
		synctest.Sleep(2*detection - time.Nanosecond)
		s.handleRecvPacket(t.Context(), restarted, tx, detect)
		if s.PacketsReceived() != 1 || s.authState.RcvAuthSeq != 201 {
			t.Fatal("low sequence accepted before 2x")
		}
		synctest.Sleep(time.Nanosecond)
		// Process RX before the already-ready detection timer, as select may do.
		s.handleRecvPacket(t.Context(), restarted, tx, detect)
		if s.PacketsReceived() != 2 || !s.authState.AuthSeqKnown || s.authState.RcvAuthSeq != 100 {
			t.Fatalf("restarted sequence rejected at 2x: received=%d auth=%+v", s.PacketsReceived(), s.authState)
		}
		// A successful new reception starts the next independent expiry period.
		synctest.Sleep(2*detection - time.Nanosecond)
		s.maybeResetAuthSeqKnown()
		if !s.authState.AuthSeqKnown {
			t.Error("new receive expired early")
		}
		synctest.Sleep(time.Nanosecond)
		s.maybeResetAuthSeqKnown()
		if s.authState.AuthSeqKnown || s.authState.RcvAuthSeq != 100 {
			t.Errorf("repeated expiry=%+v", s.authState)
		}
	})
}

func TestAuthExpiryInvalidDigestDoesNotRefreshReceiveTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, tx, detect := newAuthExpirySession(t)
		received := s.lastValidRecv
		detection := s.calcDetectionTimeHot()
		s.lastPacketRecv.Store(time.Now().Add(24 * time.Hour).UnixNano())
		bad := signedAuthExpiryPacket(t, s, 202)
		bad.pkt.Auth.Data[0] ^= 0xff
		synctest.Sleep(2*detection - time.Nanosecond)
		s.handleRecvPacket(t.Context(), bad, tx, detect)
		if s.PacketsReceived() != 1 || s.lastValidRecv != received ||
			!s.authState.AuthSeqKnown {
			t.Fatal("invalid digest refreshed reception or changed sequence before expiry")
		}
		synctest.Sleep(time.Nanosecond)
		s.handleDetectTimer(t.Context(), tx, detect)
		if s.authState.AuthSeqKnown || s.authState.RcvAuthSeq != 201 {
			t.Errorf("invalid digest postponed expiry: %+v", s.authState)
		}
	})
}

func newAuthExpirySession(t *testing.T) (*Session, *time.Timer, *time.Timer) {
	t.Helper()
	s, _, tx, detect := newRemoteTxTestSession(t)
	keys, err := NewStaticAuthKeyStore(AuthKey{
		ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("auth-expiry-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.auth, s.authKeys = MeticulousKeyedSHA1Auth{}, keys
	s.authState = &AuthState{Type: AuthTypeMeticulousKeyedSHA1, AuthSeqKnown: true, RcvAuthSeq: 201}
	s.remoteDetectMult = 3
	s.remoteDesiredMinTxInterval = 100 * time.Millisecond
	s.recordValidReceivedPacket()
	return s, tx, detect
}

func signedAuthExpiryPacket(t *testing.T, s *Session, seq uint32) recvItem {
	t.Helper()
	pkt := ControlPacket{
		Version: Version, State: StateUp, DetectMult: 3, MyDiscriminator: 99, YourDiscriminator: s.localDiscr,
		DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000,
	}
	wire := make([]byte, MaxPacketSize)
	state := &AuthState{Type: AuthTypeMeticulousKeyedSHA1, XmitAuthSeq: seq - 1}
	if err := s.auth.Sign(state, s.authKeys, &pkt, wire, 0); err != nil {
		t.Fatal(err)
	}
	wire = wire[:int(wire[3])]
	var parsed ControlPacket
	if err := UnmarshalControlPacket(wire, &parsed); err != nil {
		t.Fatal(err)
	}
	return recvItem{pkt: parsed, wire: wire}
}
