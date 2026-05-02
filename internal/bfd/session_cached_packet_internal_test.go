package bfd

import (
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

	sess.rebuildCachedPacket()
}

func TestSignCachedPacketLogsAuthSignError(t *testing.T) {
	sess := newCachedPacketTestSession(MaxPacketSize)
	sess.auth = failingCachedPacketAuth{}

	pkt := sess.buildControlPacket()
	sess.signCachedPacket(&pkt)
}

func TestSignCachedPacketLogsAuthenticatedMarshalError(t *testing.T) {
	sess := newCachedPacketTestSession(MaxPacketSize)
	sess.auth = oversizedCachedPacketAuth{}

	pkt := sess.buildControlPacket()
	sess.signCachedPacket(&pkt)
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
