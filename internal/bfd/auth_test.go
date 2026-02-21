package bfd_test

import (
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// testKeyStore — in-memory AuthKeyStore for testing
// -------------------------------------------------------------------------

// testKeyStore implements bfd.AuthKeyStore with a fixed set of keys.
type testKeyStore struct {
	keys       map[uint8]bfd.AuthKey
	currentKey bfd.AuthKey
}

// LookupKey returns the key with the given ID, or an error if not found.
func (s *testKeyStore) LookupKey(id uint8) (bfd.AuthKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return bfd.AuthKey{}, errors.New("key not found")
	}

	return key, nil
}

// CurrentKey returns the currently selected key for transmission.
func (s *testKeyStore) CurrentKey() bfd.AuthKey {
	return s.currentKey
}

// newTestKeyStore creates a testKeyStore with one key.
func newTestKeyStore(id uint8, authType bfd.AuthType, secret []byte) *testKeyStore {
	key := bfd.AuthKey{ID: id, Type: authType, Secret: secret}

	return &testKeyStore{
		keys:       map[uint8]bfd.AuthKey{id: key},
		currentKey: key,
	}
}

// -------------------------------------------------------------------------
// newTestPacket — helper to build a minimal valid ControlPacket
// -------------------------------------------------------------------------

func newTestPacket() *bfd.ControlPacket {
	return &bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateUp,
		DetectMult:            3,
		MyDiscriminator:       100,
		YourDiscriminator:     200,
		DesiredMinTxInterval:  1000000,
		RequiredMinRxInterval: 1000000,
	}
}

// -------------------------------------------------------------------------
// TestSimplePasswordSignVerify — RFC 5880 Section 6.7.2
// -------------------------------------------------------------------------

func TestSimplePasswordSignVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
	}{
		{name: "short password", password: "abc"},
		{name: "max password", password: "1234567890123456"},
		{name: "single char", password: "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys := newTestKeyStore(1, bfd.AuthTypeSimplePassword, []byte(tt.password))
			auth := bfd.SimplePasswordAuth{}
			state := &bfd.AuthState{Type: bfd.AuthTypeSimplePassword}
			pkt := newTestPacket()
			buf := make([]byte, bfd.MaxPacketSize)

			if err := auth.Sign(state, keys, pkt, buf, 0); err != nil {
				t.Fatalf("Sign: %v", err)
			}

			verifySimplePasswordFields(t, pkt, tt.password)

			// Re-marshal for Verify round-trip.
			n := marshalForVerify(t, pkt, buf)

			// Unmarshal to get a "received" packet.
			var rxPkt bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf[:n], &rxPkt); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			if err := auth.Verify(state, keys, &rxPkt, buf[:n], n); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// verifySimplePasswordFields checks the auth section after Sign.
func verifySimplePasswordFields(t *testing.T, pkt *bfd.ControlPacket, password string) {
	t.Helper()

	if !pkt.AuthPresent {
		t.Fatal("AuthPresent not set after Sign")
	}

	if pkt.Auth == nil {
		t.Fatal("Auth section nil after Sign")
	}

	if pkt.Auth.Type != bfd.AuthTypeSimplePassword {
		t.Errorf("Auth.Type: got %d, want %d", pkt.Auth.Type, bfd.AuthTypeSimplePassword)
	}

	wantLen := uint8(3 + len(password))
	if pkt.Auth.Len != wantLen {
		t.Errorf("Auth.Len: got %d, want %d", pkt.Auth.Len, wantLen)
	}

	if string(pkt.Auth.AuthData) != password {
		t.Errorf("Auth.AuthData: got %q, want %q", pkt.Auth.AuthData, password)
	}
}

// -------------------------------------------------------------------------
// TestKeyedMD5SignVerify — RFC 5880 Section 6.7.3
// -------------------------------------------------------------------------

func TestKeyedMD5SignVerify(t *testing.T) {
	t.Parallel()

	keys := newTestKeyStore(5, bfd.AuthTypeKeyedMD5, []byte("md5-secret-key!!"))
	auth := bfd.KeyedMD5Auth{}
	state := &bfd.AuthState{Type: bfd.AuthTypeKeyedMD5, XmitAuthSeq: 100}
	pkt := newTestPacket()
	buf := make([]byte, bfd.MaxPacketSize)

	if err := auth.Sign(state, keys, pkt, buf, 0); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	verifyHashAuthFields(t, pkt, bfd.AuthTypeKeyedMD5, 24, 16)

	// Verify the signed packet.
	rxBuf, rxPkt, n := unmarshalSigned(t, pkt, buf)

	rxState := &bfd.AuthState{Type: bfd.AuthTypeKeyedMD5}
	if err := auth.Verify(rxState, keys, rxPkt, rxBuf[:n], n); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !rxState.AuthSeqKnown {
		t.Error("AuthSeqKnown not set after successful Verify")
	}
}

// -------------------------------------------------------------------------
// TestMeticulousKeyedMD5SignVerify — RFC 5880 Section 6.7.3
// -------------------------------------------------------------------------

func TestMeticulousKeyedMD5SignVerify(t *testing.T) {
	t.Parallel()

	keys := newTestKeyStore(7, bfd.AuthTypeMeticulousKeyedMD5, []byte("met-md5-key12345"))
	auth := bfd.MeticulousKeyedMD5Auth{}
	state := &bfd.AuthState{
		Type:        bfd.AuthTypeMeticulousKeyedMD5,
		XmitAuthSeq: 500,
	}

	// Sign multiple packets: sequence MUST increment each time.
	seqs := signMultipleAndCollectSeqs(t, auth, state, keys, 3)
	verifyStrictlyIncrementing(t, seqs)
}

// -------------------------------------------------------------------------
// TestKeyedSHA1SignVerify — RFC 5880 Section 6.7.4
// -------------------------------------------------------------------------

func TestKeyedSHA1SignVerify(t *testing.T) {
	t.Parallel()

	keys := newTestKeyStore(3, bfd.AuthTypeKeyedSHA1, []byte("sha1-key-20bytes"))
	auth := bfd.KeyedSHA1Auth{}
	state := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1, XmitAuthSeq: 42}
	pkt := newTestPacket()
	buf := make([]byte, bfd.MaxPacketSize)

	if err := auth.Sign(state, keys, pkt, buf, 0); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	verifyHashAuthFields(t, pkt, bfd.AuthTypeKeyedSHA1, 28, 20)

	// Verify the signed packet.
	rxBuf, rxPkt, n := unmarshalSigned(t, pkt, buf)

	rxState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1}
	if err := auth.Verify(rxState, keys, rxPkt, rxBuf[:n], n); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !rxState.AuthSeqKnown {
		t.Error("AuthSeqKnown not set after successful Verify")
	}

	if rxState.RcvAuthSeq != rxPkt.Auth.SequenceNumber {
		t.Errorf("RcvAuthSeq: got %d, want %d",
			rxState.RcvAuthSeq, rxPkt.Auth.SequenceNumber)
	}
}

// -------------------------------------------------------------------------
// TestMeticulousKeyedSHA1SignVerify — RFC 5880 Section 6.7.4
// -------------------------------------------------------------------------

func TestMeticulousKeyedSHA1SignVerify(t *testing.T) {
	t.Parallel()

	keys := newTestKeyStore(1, bfd.AuthTypeMeticulousKeyedSHA1, []byte("met-sha1-key-here!!"))
	auth := bfd.MeticulousKeyedSHA1Auth{}
	state := &bfd.AuthState{
		Type:        bfd.AuthTypeMeticulousKeyedSHA1,
		XmitAuthSeq: 1000,
	}

	// Sign multiple packets: sequence MUST increment each time.
	seqs := signMultipleAndCollectSeqs(t, auth, state, keys, 3)
	verifyStrictlyIncrementing(t, seqs)
}

// -------------------------------------------------------------------------
// TestSeqInWindow — circular uint32 window checks
// -------------------------------------------------------------------------

func TestSeqInWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seq  uint32
		lo   uint32
		hi   uint32
		want bool
	}{
		{name: "in range", seq: 5, lo: 3, hi: 10, want: true},
		{name: "at lo", seq: 3, lo: 3, hi: 10, want: true},
		{name: "at hi", seq: 10, lo: 3, hi: 10, want: true},
		{name: "below lo", seq: 2, lo: 3, hi: 10, want: false},
		{name: "above hi", seq: 11, lo: 3, hi: 10, want: false},
		{name: "wrap-around in range", seq: 2, lo: 0xFFFFFFFE, hi: 5, want: true},
		{name: "wrap-around at lo", seq: 0xFFFFFFFE, lo: 0xFFFFFFFE, hi: 5, want: true},
		{name: "wrap-around at hi", seq: 5, lo: 0xFFFFFFFE, hi: 5, want: true},
		{name: "wrap-around below lo", seq: 0xFFFFFFFD, lo: 0xFFFFFFFE, hi: 5, want: false},
		{name: "wrap-around above hi", seq: 6, lo: 0xFFFFFFFE, hi: 5, want: false},
		{name: "max uint32", seq: 0xFFFFFFFF, lo: 0xFFFFFFFF, hi: 0xFFFFFFFF, want: true},
		{name: "zero window", seq: 0, lo: 0, hi: 0, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := bfd.SeqInWindow(tt.seq, tt.lo, tt.hi)
			if got != tt.want {
				t.Errorf("SeqInWindow(%d, %d, %d) = %t, want %t",
					tt.seq, tt.lo, tt.hi, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestAuthSequenceReplay — reject replayed sequence numbers
// -------------------------------------------------------------------------

func TestAuthSequenceReplay(t *testing.T) {
	t.Parallel()

	keys := newTestKeyStore(1, bfd.AuthTypeKeyedSHA1, []byte("sha1-replay-test"))
	auth := bfd.KeyedSHA1Auth{}

	// Create a signed packet.
	txState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1, XmitAuthSeq: 100}
	pkt := newTestPacket()
	buf := make([]byte, bfd.MaxPacketSize)

	if err := auth.Sign(txState, keys, pkt, buf, 0); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// First verify: should succeed and set RcvAuthSeq.
	rxBuf1, rxPkt1, n1 := unmarshalSigned(t, pkt, buf)
	rxState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1}

	if err := auth.Verify(rxState, keys, rxPkt1, rxBuf1[:n1], n1); err != nil {
		t.Fatalf("first Verify: %v", err)
	}

	// Second verify with same sequence: should fail for non-meticulous
	// because lo=RcvAuthSeq and seq==RcvAuthSeq is accepted.
	// But for meticulous, lo=RcvAuthSeq+1, so same seq would fail.
	// Test with MeticulousKeyedSHA1 for replay detection.
	metAuth := bfd.MeticulousKeyedSHA1Auth{}
	metKeys := newTestKeyStore(1, bfd.AuthTypeMeticulousKeyedSHA1, []byte("sha1-replay-test"))
	metTxState := &bfd.AuthState{
		Type:        bfd.AuthTypeMeticulousKeyedSHA1,
		XmitAuthSeq: 200,
	}

	metPkt := newTestPacket()
	metBuf := make([]byte, bfd.MaxPacketSize)

	if err := metAuth.Sign(metTxState, metKeys, metPkt, metBuf, 0); err != nil {
		t.Fatalf("meticulous Sign: %v", err)
	}

	// First verify succeeds.
	rxBuf2, rxPkt2, n2 := unmarshalSigned(t, metPkt, metBuf)
	metRxState := &bfd.AuthState{Type: bfd.AuthTypeMeticulousKeyedSHA1}

	if err := metAuth.Verify(metRxState, metKeys, rxPkt2, rxBuf2[:n2], n2); err != nil {
		t.Fatalf("meticulous first Verify: %v", err)
	}

	// Replay the same packet: should be rejected.
	rxBuf3, rxPkt3, n3 := unmarshalSigned(t, metPkt, metBuf)
	err := metAuth.Verify(metRxState, metKeys, rxPkt3, rxBuf3[:n3], n3)

	if err == nil {
		t.Fatal("expected error for replayed sequence, got nil")
	}

	if !errors.Is(err, bfd.ErrAuthSeqOutOfWindow) {
		t.Errorf("expected ErrAuthSeqOutOfWindow, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// TestAuthKeyMismatch — reject wrong key ID
// -------------------------------------------------------------------------

func TestAuthKeyMismatch(t *testing.T) {
	t.Parallel()

	// Sign with key ID 5.
	txKeys := newTestKeyStore(5, bfd.AuthTypeKeyedSHA1, []byte("sha1-key-mismatch"))
	auth := bfd.KeyedSHA1Auth{}
	txState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1, XmitAuthSeq: 10}
	pkt := newTestPacket()
	buf := make([]byte, bfd.MaxPacketSize)

	if err := auth.Sign(txState, txKeys, pkt, buf, 0); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Verify with a key store that only has key ID 9.
	rxKeys := newTestKeyStore(9, bfd.AuthTypeKeyedSHA1, []byte("different-key!!!"))
	rxBuf, rxPkt, n := unmarshalSigned(t, pkt, buf)
	rxState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1}

	err := auth.Verify(rxState, rxKeys, rxPkt, rxBuf[:n], n)
	if err == nil {
		t.Fatal("expected error for wrong key ID, got nil")
	}

	if !errors.Is(err, bfd.ErrAuthKeyNotFound) {
		t.Errorf("expected ErrAuthKeyNotFound, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// TestAuthDigestMismatch — reject tampered packets
// -------------------------------------------------------------------------

func TestAuthDigestMismatch(t *testing.T) {
	t.Parallel()

	keys := newTestKeyStore(1, bfd.AuthTypeKeyedSHA1, []byte("sha1-tamper-test!"))
	auth := bfd.KeyedSHA1Auth{}
	txState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1, XmitAuthSeq: 50}
	pkt := newTestPacket()
	buf := make([]byte, bfd.MaxPacketSize)

	if err := auth.Sign(txState, keys, pkt, buf, 0); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	rxBuf, rxPkt, n := unmarshalSigned(t, pkt, buf)

	// Tamper with a packet field (flip a bit in MyDiscriminator).
	rxBuf[4] ^= 0xFF

	rxState := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1}
	err := auth.Verify(rxState, keys, rxPkt, rxBuf[:n], n)

	if err == nil {
		t.Fatal("expected error for tampered packet, got nil")
	}

	if !errors.Is(err, bfd.ErrAuthDigestMismatch) {
		t.Errorf("expected ErrAuthDigestMismatch, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// TestAuthStateInitialization — XmitAuthSeq random, AuthSeqKnown=false
// -------------------------------------------------------------------------

func TestAuthStateInitialization(t *testing.T) {
	t.Parallel()

	// Create multiple AuthState instances and verify they have different
	// XmitAuthSeq values (statistical test: probability of collision for
	// two random uint32 is ~1/2^32, negligible).
	state1, err := bfd.NewAuthState(bfd.AuthTypeKeyedSHA1)
	if err != nil {
		t.Fatalf("NewAuthState 1: %v", err)
	}

	state2, err := bfd.NewAuthState(bfd.AuthTypeKeyedSHA1)
	if err != nil {
		t.Fatalf("NewAuthState 2: %v", err)
	}

	// RFC 5880 Section 6.8.1: AuthSeqKnown MUST be initialized to false.
	if state1.AuthSeqKnown {
		t.Error("state1.AuthSeqKnown should be false")
	}

	if state2.AuthSeqKnown {
		t.Error("state2.AuthSeqKnown should be false")
	}

	// RFC 5880 Section 6.8.1: XmitAuthSeq MUST be initialized to random.
	// Verify the two are different (extremely unlikely to collide).
	if state1.XmitAuthSeq == state2.XmitAuthSeq {
		t.Errorf("XmitAuthSeq collision: both are %d", state1.XmitAuthSeq)
	}

	// Verify the auth type is set correctly.
	if state1.Type != bfd.AuthTypeKeyedSHA1 {
		t.Errorf("state1.Type: got %d, want %d",
			state1.Type, bfd.AuthTypeKeyedSHA1)
	}
}

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// marshalForVerify marshals a signed packet into buf and returns the
// number of bytes written.
func marshalForVerify(t *testing.T, pkt *bfd.ControlPacket, buf []byte) int {
	t.Helper()

	n, err := bfd.MarshalControlPacket(pkt, buf)
	if err != nil {
		t.Fatalf("MarshalControlPacket: %v", err)
	}

	return n
}

// unmarshalSigned marshals the packet, then unmarshals it into a fresh
// buffer and packet, simulating reception. Returns the buffer, packet,
// and valid byte count.
func unmarshalSigned(
	t *testing.T,
	pkt *bfd.ControlPacket,
	_ []byte,
) ([]byte, *bfd.ControlPacket, int) {
	t.Helper()

	rxBuf := make([]byte, bfd.MaxPacketSize)

	n, err := bfd.MarshalControlPacket(pkt, rxBuf)
	if err != nil {
		t.Fatalf("MarshalControlPacket for verify: %v", err)
	}

	var rxPkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(rxBuf[:n], &rxPkt); err != nil {
		t.Fatalf("UnmarshalControlPacket: %v", err)
	}

	return rxBuf, &rxPkt, n
}

// verifyHashAuthFields checks common auth section fields after Sign.
func verifyHashAuthFields(
	t *testing.T,
	pkt *bfd.ControlPacket,
	authType bfd.AuthType,
	expectedLen uint8,
	digestSize int,
) {
	t.Helper()

	if !pkt.AuthPresent {
		t.Fatal("AuthPresent not set after Sign")
	}

	if pkt.Auth == nil {
		t.Fatal("Auth section nil after Sign")
	}

	if pkt.Auth.Type != authType {
		t.Errorf("Auth.Type: got %d, want %d", pkt.Auth.Type, authType)
	}

	if pkt.Auth.Len != expectedLen {
		t.Errorf("Auth.Len: got %d, want %d", pkt.Auth.Len, expectedLen)
	}

	if len(pkt.Auth.Digest) != digestSize {
		t.Errorf("Auth.Digest length: got %d, want %d",
			len(pkt.Auth.Digest), digestSize)
	}

	// Verify digest is not all zeros (hash should produce non-zero).
	allZero := true
	for _, b := range pkt.Auth.Digest {
		if b != 0 {
			allZero = false

			break
		}
	}

	if allZero {
		t.Error("Auth.Digest is all zeros after Sign")
	}
}

// signMultipleAndCollectSeqs signs multiple packets and returns the
// sequence numbers used for each.
func signMultipleAndCollectSeqs(
	t *testing.T,
	auth bfd.Authenticator,
	state *bfd.AuthState,
	keys bfd.AuthKeyStore,
	count int,
) []uint32 {
	t.Helper()

	seqs := make([]uint32, 0, count)

	for i := range count {
		pkt := newTestPacket()
		buf := make([]byte, bfd.MaxPacketSize)

		if err := auth.Sign(state, keys, pkt, buf, 0); err != nil {
			t.Fatalf("Sign %d: %v", i, err)
		}

		seqs = append(seqs, pkt.Auth.SequenceNumber)
	}

	return seqs
}

// verifyStrictlyIncrementing checks that each element is exactly one
// more than the previous (meticulous behavior).
func verifyStrictlyIncrementing(t *testing.T, seqs []uint32) {
	t.Helper()

	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Errorf("sequence[%d]=%d not exactly +1 from sequence[%d]=%d",
				i, seqs[i], i-1, seqs[i-1])
		}
	}
}

// -------------------------------------------------------------------------
// FuzzAuthSignVerify — fuzz Sign→Marshal→Unmarshal→Verify round-trip
// -------------------------------------------------------------------------

// FuzzAuthSignVerify verifies that for any valid key material and sequence
// number, the Sign→Marshal→Unmarshal→Verify round-trip always succeeds.
// This catches consistency bugs in the crypto path across all 5 auth types.
func FuzzAuthSignVerify(f *testing.F) {
	// Seeds: one per auth type with representative secrets.
	f.Add([]byte("pass"), uint8(0), uint32(0))                     // Simple Password
	f.Add([]byte("md5-secret-key!!"), uint8(1), uint32(100))       // Keyed MD5
	f.Add([]byte("met-md5-secret!!"), uint8(2), uint32(500))       // Meticulous MD5
	f.Add([]byte("sha1-secret-20bytes!"), uint8(3), uint32(42))    // Keyed SHA1
	f.Add([]byte("met-sha1-secret!!!!"), uint8(4), uint32(999999)) // Meticulous SHA1
	f.Add([]byte("x"), uint8(0), uint32(0xFFFFFFFE))               // single-char near wrap

	f.Fuzz(func(t *testing.T, secret []byte, authIdx uint8, seq uint32) {
		authTypes := []bfd.AuthType{
			bfd.AuthTypeSimplePassword,
			bfd.AuthTypeKeyedMD5,
			bfd.AuthTypeMeticulousKeyedMD5,
			bfd.AuthTypeKeyedSHA1,
			bfd.AuthTypeMeticulousKeyedSHA1,
		}
		authType := authTypes[authIdx%uint8(len(authTypes))]

		// Constrain secret to valid lengths per RFC 5880.
		if len(secret) == 0 {
			return
		}
		if authType == bfd.AuthTypeSimplePassword && len(secret) > 16 {
			return
		}
		if authType != bfd.AuthTypeSimplePassword && len(secret) > 20 {
			return
		}

		keys := newTestKeyStore(1, authType, secret)

		var auth bfd.Authenticator
		switch authType {
		case bfd.AuthTypeNone:
			return // No authenticator for AuthTypeNone.
		case bfd.AuthTypeSimplePassword:
			auth = bfd.SimplePasswordAuth{}
		case bfd.AuthTypeKeyedMD5:
			auth = bfd.KeyedMD5Auth{}
		case bfd.AuthTypeMeticulousKeyedMD5:
			auth = bfd.MeticulousKeyedMD5Auth{}
		case bfd.AuthTypeKeyedSHA1:
			auth = bfd.KeyedSHA1Auth{}
		case bfd.AuthTypeMeticulousKeyedSHA1:
			auth = bfd.MeticulousKeyedSHA1Auth{}
		}

		txState := &bfd.AuthState{Type: authType, XmitAuthSeq: seq}
		pkt := newTestPacket()
		buf := make([]byte, bfd.MaxPacketSize)

		// Sign must succeed for valid inputs.
		if err := auth.Sign(txState, keys, pkt, buf, 0); err != nil {
			t.Fatalf("Sign(%s, secret=%d bytes, seq=%d): %v",
				authType, len(secret), seq, err)
		}

		// Marshal→Unmarshal round-trip.
		rxBuf := make([]byte, bfd.MaxPacketSize)

		n, err := bfd.MarshalControlPacket(pkt, rxBuf)
		if err != nil {
			t.Fatalf("Marshal after Sign: %v", err)
		}

		var rxPkt bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(rxBuf[:n], &rxPkt); err != nil {
			t.Fatalf("Unmarshal after Sign: %v", err)
		}

		// Verify must succeed for a freshly signed packet.
		rxState := &bfd.AuthState{Type: authType}
		if err := auth.Verify(rxState, keys, &rxPkt, rxBuf[:n], n); err != nil {
			t.Fatalf("Verify(%s, secret=%d bytes, seq=%d): %v",
				authType, len(secret), seq, err)
		}
	})
}

// -------------------------------------------------------------------------
// FuzzSeqInWindow — fuzz circular uint32 window arithmetic
// -------------------------------------------------------------------------

// FuzzSeqInWindow verifies mathematical invariants of the circular uint32
// window check used by MD5/SHA1 sequence number validation
// (RFC 5880 Sections 6.7.3, 6.7.4).
func FuzzSeqInWindow(f *testing.F) {
	f.Add(uint32(5), uint32(3), uint32(10))                           // normal range
	f.Add(uint32(2), uint32(0xFFFFFFFE), uint32(5))                   // wrap-around
	f.Add(uint32(0), uint32(0), uint32(0))                            // zero window
	f.Add(uint32(0xFFFFFFFF), uint32(0xFFFFFFFF), uint32(0))          // max→0 wrap
	f.Add(uint32(0x80000000), uint32(0x7FFFFFFF), uint32(0x80000001)) // half-space boundary

	f.Fuzz(func(t *testing.T, seq, lo, hi uint32) {
		// Must not panic.
		_ = bfd.SeqInWindow(seq, lo, hi)

		// Invariant 1: lo is always in [lo, hi].
		if !bfd.SeqInWindow(lo, lo, hi) {
			t.Errorf("lo=%d must be in [lo=%d, hi=%d]", lo, lo, hi)
		}

		// Invariant 2: hi is always in [lo, hi].
		if !bfd.SeqInWindow(hi, lo, hi) {
			t.Errorf("hi=%d must be in [lo=%d, hi=%d]", hi, lo, hi)
		}

		// Invariant 3: if lo == hi, only lo (==hi) is in window.
		if lo == hi {
			if bfd.SeqInWindow(lo+1, lo, hi) {
				t.Errorf("lo+1=%d should NOT be in single-element window [%d, %d]",
					lo+1, lo, hi)
			}
		}

		// Invariant 4: if seq is in [lo, hi], then seq-lo <= hi-lo
		// (direct formula verification).
		result := bfd.SeqInWindow(seq, lo, hi)
		expected := (seq - lo) <= (hi - lo)
		if result != expected {
			t.Errorf("SeqInWindow(%d, %d, %d) = %t, formula says %t",
				seq, lo, hi, result, expected)
		}
	})
}
