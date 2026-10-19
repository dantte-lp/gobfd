package bfd

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"
)

const unsolicitedAuthKeyID = 7

func unsolicitedAuthKeys(t *testing.T, id uint8) AuthKeyStore {
	t.Helper()

	keys, err := NewStaticAuthKeyStore(AuthKey{
		ID: id, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("unsolicited-sha1"),
	})
	if err != nil {
		t.Fatalf("NewStaticAuthKeyStore: %v", err)
	}
	return keys
}

// signedTrigger returns a Down packet from an unknown peer signed with
// Meticulous Keyed SHA1 under keys, and its wire bytes.
func signedTrigger(t *testing.T, keys AuthKeyStore) (*ControlPacket, []byte) {
	t.Helper()

	pkt := ownershipDownPacket(51)
	state := &AuthState{Type: AuthTypeMeticulousKeyedSHA1, XmitAuthSeq: 100}
	buf := make([]byte, MaxPacketSize)
	if err := (MeticulousKeyedSHA1Auth{}).Sign(state, keys, pkt, buf, 0); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	n := HeaderSize + int(pkt.Auth.Len)
	return pkt, buf[:n]
}

func authManager(t *testing.T, withAuth bool) *Manager {
	t.Helper()

	policy := admissionPolicy(10)
	if withAuth {
		policy.Auth = MeticulousKeyedSHA1Auth{}
		policy.AuthKeys = unsolicitedAuthKeys(t, unsolicitedAuthKeyID)
	}
	mgr := NewManager(
		slog.New(slog.DiscardHandler),
		WithUnsolicitedPolicy(policy),
		WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
		WithUnsolicitedSessionSender(nonOwningSessionSender(senderLeaseTestSender{})),
	)
	t.Cleanup(mgr.Close)
	return mgr
}

// RFC 9468 Section 6.2: with authentication configured, the triggering
// packet is verified before a session exists, and the session signs its
// replies with the configured key.
func TestUnsolicitedAuthAdmitsSignedTrigger(t *testing.T) {
	t.Parallel()

	mgr := authManager(t, true)
	pkt, wire := signedTrigger(t, unsolicitedAuthKeys(t, unsolicitedAuthKeyID))
	if _, err := mgr.tryCreateUnsolicited(pkt, admissionMeta("192.0.2.10", "192.0.2.1"), wire); err != nil {
		t.Fatalf("tryCreateUnsolicited with a valid signature: %v", err)
	}

	sessions := mgr.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	sess, ok := mgr.LookupByDiscriminator(sessions[0].LocalDiscr)
	if !ok || sess.auth == nil || sess.authKeys == nil {
		t.Fatal("unsolicited session has no authenticator")
	}
	var reply ControlPacket
	if err := UnmarshalControlPacket(sess.cachedPacket, &reply); err != nil {
		t.Fatalf("decode cached reply: %v", err)
	}
	if !reply.AuthPresent || reply.Auth.Type != AuthTypeMeticulousKeyedSHA1 || reply.Auth.KeyID != unsolicitedAuthKeyID {
		t.Fatalf("cached reply auth = %+v, want Meticulous Keyed SHA1 key %d", reply.Auth, unsolicitedAuthKeyID)
	}
}

func TestUnsolicitedAuthRejectsBadTriggers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		withAuth bool
		trigger  func(t *testing.T) (*ControlPacket, []byte)
	}{
		{"unsigned packet, auth required", true, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			pkt := ownershipDownPacket(52)
			wire := make([]byte, MaxPacketSize)
			n, err := MarshalControlPacket(pkt, wire)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			return pkt, wire[:n]
		}},
		{"wrong digest", true, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			_, wire := signedTrigger(t, unsolicitedAuthKeys(t, unsolicitedAuthKeyID))
			wire[len(wire)-1] ^= 0xFF
			pkt := new(ControlPacket)
			if err := UnmarshalControlPacket(wire, pkt); err != nil {
				t.Fatalf("decode tampered packet: %v", err)
			}
			return pkt, wire
		}},
		{"unknown key ID", true, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			return signedTrigger(t, unsolicitedAuthKeys(t, unsolicitedAuthKeyID+1))
		}},
		{"Keyed SHA1 signature", true, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			return signedWith(t, KeyedSHA1Auth{}, AuthTypeKeyedSHA1)
		}},
		{"Simple Password", true, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			return signedWith(t, SimplePasswordAuth{}, AuthTypeSimplePassword)
		}},
		{"signed packet without wire bytes", true, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			pkt, _ := signedTrigger(t, unsolicitedAuthKeys(t, unsolicitedAuthKeyID))
			return pkt, nil
		}},
		// RFC 5880 Section 6.8.6: with no authentication in use, a packet
		// with the A bit set is discarded.
		{"signed packet, auth not configured", false, func(t *testing.T) (*ControlPacket, []byte) {
			t.Helper()
			return signedTrigger(t, unsolicitedAuthKeys(t, unsolicitedAuthKeyID))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mgr := authManager(t, tt.withAuth)
			pkt, wire := tt.trigger(t)
			_, err := mgr.tryCreateUnsolicited(pkt, admissionMeta("192.0.2.10", "192.0.2.1"), wire)
			if !errors.Is(err, ErrUnsolicitedAuthFailed) {
				t.Fatalf("tryCreateUnsolicited error = %v, want ErrUnsolicitedAuthFailed", err)
			}
			if got := len(mgr.Sessions()); got != 0 {
				t.Fatalf("sessions = %d, want 0", got)
			}
			if got := mgr.unsolicited.sessionCount.Load(); got != 0 {
				t.Fatalf("reserved quota = %d, want 0 after rejection", got)
			}
		})
	}
}

// signedWith signs a trigger with another mechanism under the configured key
// ID and secret, to prove admission requires Meticulous Keyed SHA1 itself.
func signedWith(t *testing.T, auth Authenticator, authType AuthType) (*ControlPacket, []byte) {
	t.Helper()

	keys, err := NewStaticAuthKeyStore(AuthKey{
		ID: unsolicitedAuthKeyID, Type: authType, Secret: []byte("unsolicited-sha1"),
	})
	if err != nil {
		t.Fatalf("NewStaticAuthKeyStore: %v", err)
	}
	pkt := ownershipDownPacket(53)
	buf := make([]byte, MaxPacketSize)
	if signErr := auth.Sign(&AuthState{Type: authType, XmitAuthSeq: 7}, keys, pkt, buf, 0); signErr != nil {
		t.Fatalf("Sign: %v", signErr)
	}
	n, err := MarshalControlPacket(pkt, buf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return pkt, buf[:n]
}
