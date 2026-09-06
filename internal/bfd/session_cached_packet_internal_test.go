package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

type failingCachedPacketAuth struct{}

func (failingCachedPacketAuth) Sign(*AuthState, AuthKeyStore, *ControlPacket, []byte, int) error {
	return errors.New("test auth sign failure")
}

func (failingCachedPacketAuth) Verify(*AuthState, AuthKeyStore, *ControlPacket, []byte, int) error {
	return nil
}

type oversizedCachedPacketAuth struct{}

func (oversizedCachedPacketAuth) Sign(_ *AuthState, _ AuthKeyStore, pkt *ControlPacket, _ []byte, _ int) error {
	pkt.AuthPresent = true
	pkt.Auth = &AuthSection{
		Type: AuthTypeSimplePassword,
		Len:  MaxPacketSize,
	}
	return nil
}

func (oversizedCachedPacketAuth) Verify(*AuthState, AuthKeyStore, *ControlPacket, []byte, int) error {
	return nil
}

func TestRebuildCachedPacketLogsMarshalError(t *testing.T) {
	sess := newCachedPacketTestSession(HeaderSize - 1)

	if sess.rebuildCachedPacket() {
		t.Fatal("rebuild succeeded with undersized buffer")
	}
}

func TestSignCachedPacketLogsAuthSignError(t *testing.T) {
	sess := newCachedPacketTestSession(MaxPacketSize)
	sess.auth = failingCachedPacketAuth{}

	pkt := sess.buildControlPacket()
	if sess.signCachedPacket(&pkt) {
		t.Fatal("sign succeeded with failing authenticator")
	}
}

func TestSignCachedPacketLogsAuthenticatedMarshalError(t *testing.T) {
	sess := newCachedPacketTestSession(MaxPacketSize)
	sess.auth = oversizedCachedPacketAuth{}

	pkt := sess.buildControlPacket()
	if sess.signCachedPacket(&pkt) {
		t.Fatal("sign succeeded with oversized authenticated packet")
	}
}

type retryCachedPacketSender struct {
	failures int
	attempts int
	packets  [][]byte
}

func (s *retryCachedPacketSender) SendPacket(_ context.Context, buf []byte, _ netip.Addr) error {
	s.attempts++
	if s.failures > 0 {
		s.failures--
		return errors.New("test socket failure")
	}
	s.packets = append(s.packets, append([]byte(nil), buf...))
	return nil
}

func TestSendControlRetainsPendingFinalUntilSuccessfulSend(t *testing.T) {
	tests := []struct {
		name         string
		prepare      func(*Session, *retryCachedPacketSender)
		repair       func(*Session)
		wantAttempts int
	}{
		{
			name: "signing failure",
			prepare: func(sess *Session, _ *retryCachedPacketSender) {
				sess.auth = failingCachedPacketAuth{}
			},
			repair: func(sess *Session) { sess.auth = nil },
		},
		{
			name: "marshal failure",
			prepare: func(sess *Session, _ *retryCachedPacketSender) {
				sess.cachedPacket = make([]byte, HeaderSize-1)
			},
			repair: func(sess *Session) { sess.cachedPacket = make([]byte, MaxPacketSize) },
		},
		{
			name: "authenticated marshal failure",
			prepare: func(sess *Session, _ *retryCachedPacketSender) {
				sess.auth = oversizedCachedPacketAuth{}
			},
			repair: func(sess *Session) { sess.auth = nil },
		},
		{
			name: "socket failure",
			prepare: func(_ *Session, sender *retryCachedPacketSender) {
				sender.failures = 1
			},
			wantAttempts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &retryCachedPacketSender{}
			sess := newCachedPacketTestSession(MaxPacketSize)
			sess.sender = sender
			sess.pollActive = true
			sess.pendingFinal = true
			tt.prepare(sess, sender)

			sess.sendControl(context.Background())
			if !sess.pendingFinal {
				t.Fatal("pending Final cleared after failed send")
			}
			if !sess.pollActive {
				t.Fatal("local Poll sequence cancelled by Final response")
			}
			if sender.attempts != tt.wantAttempts {
				t.Fatalf("send attempts after failure = %d, want %d", sender.attempts, tt.wantAttempts)
			}
			if got := sess.packetsSent.Load(); got != 0 {
				t.Fatalf("packets sent after failure = %d, want 0", got)
			}

			if tt.repair != nil {
				tt.repair(sess)
			}
			sess.sendControl(context.Background())
			if sess.pendingFinal {
				t.Fatal("pending Final retained after successful send")
			}
			if !sess.pollActive {
				t.Fatal("local Poll sequence cancelled after successful Final response")
			}
			assertCachedPacketFlags(t, sender.packets[0], false, true)

			sess.sendControl(context.Background())
			assertCachedPacketFlags(t, sender.packets[1], true, false)
			if got := sess.packetsSent.Load(); got != 2 {
				t.Fatalf("packets sent after successes = %d, want 2", got)
			}
		})
	}
}

func TestMaybeSendControlRetriesPendingFinalBeforePeriodicGates(t *testing.T) {
	sender := &retryCachedPacketSender{}
	sess := newCachedPacketTestSession(MaxPacketSize)
	sess.sender = sender
	sess.role = RolePassive
	sess.remoteDiscr = 0
	sess.remoteMinRxInterval = 0
	sess.pendingFinal = true

	sess.maybeSendControl(context.Background())

	if len(sender.packets) != 1 {
		t.Fatalf("sent packets = %d, want 1", len(sender.packets))
	}
	assertCachedPacketFlags(t, sender.packets[0], false, true)
}

func assertCachedPacketFlags(t *testing.T, wire []byte, wantPoll, wantFinal bool) {
	t.Helper()
	var pkt ControlPacket
	if err := UnmarshalControlPacket(wire, &pkt); err != nil {
		t.Fatalf("unmarshal sent packet: %v", err)
	}
	if pkt.Poll != wantPoll || pkt.Final != wantFinal {
		t.Fatalf("sent flags: Poll=%t Final=%t, want Poll=%t Final=%t", pkt.Poll, pkt.Final, wantPoll, wantFinal)
	}
}

func newCachedPacketTestSession(packetSize int) *Session {
	return &Session{
		peerAddr:              netip.MustParseAddr("192.0.2.1"),
		localAddr:             netip.MustParseAddr("192.0.2.2"),
		sessionType:           SessionTypeSingleHop,
		role:                  RoleActive,
		localDiscr:            42,
		cachedState:           StateDown,
		detectMult:            3,
		desiredMinTxInterval:  100 * time.Millisecond,
		requiredMinRxInterval: 100 * time.Millisecond,
		cachedPacket:          make([]byte, packetSize),
		logger:                slog.New(slog.DiscardHandler),
		metrics:               noopMetrics{},
	}
}
