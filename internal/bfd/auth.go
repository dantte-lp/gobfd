package bfd

import (
	"crypto/md5" //nolint:gosec // G501: MD5 required by RFC 5880 Section 6.7.3
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: SHA1 required by RFC 5880 Section 6.7.4
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

// -------------------------------------------------------------------------
// Auth Errors
// -------------------------------------------------------------------------

// Sentinel errors for authentication failures. These correspond to the
// validation steps in RFC 5880 Sections 6.7.2, 6.7.3, 6.7.4.
var (
	// ErrAuthKeyNotFound indicates the received Auth Key ID is not
	// configured (RFC 5880 Section 6.7.2/6.7.3/6.7.4).
	ErrAuthKeyNotFound = errors.New("auth key not found")

	// ErrAuthTypeMismatch indicates the received Auth Type does not
	// match the configured authentication type.
	ErrAuthTypeMismatch = errors.New("auth type mismatch")

	// ErrAuthDigestMismatch indicates the computed digest does not
	// match the received digest (RFC 5880 Section 6.7.3/6.7.4).
	ErrAuthDigestMismatch = errors.New("auth digest mismatch")

	// ErrAuthPasswordMismatch indicates the received password does
	// not match the configured password (RFC 5880 Section 6.7.2).
	ErrAuthPasswordMismatch = errors.New("auth password mismatch")

	// ErrAuthSeqOutOfWindow indicates the received sequence number
	// is outside the acceptance window (RFC 5880 Section 6.7.3/6.7.4).
	ErrAuthSeqOutOfWindow = errors.New("auth sequence number out of window")

	// ErrAuthMissingSection indicates the packet has no auth section
	// when one was expected.
	ErrAuthMissingSection = errors.New("auth section missing")

	// ErrAuthLenMismatch indicates the Auth Len field does not match
	// the expected value for the auth type.
	ErrAuthLenMismatch = errors.New("auth len mismatch")
)

// -------------------------------------------------------------------------
// AuthKey — RFC 5880 Sections 6.7.2, 6.7.3, 6.7.4
// -------------------------------------------------------------------------

// AuthKey represents a single authentication key configured for a session.
type AuthKey struct {
	// ID is the Auth Key ID, allowing multiple keys to be active
	// simultaneously for hitless key rotation.
	ID uint8

	// Type is the authentication type this key is used for.
	Type AuthType

	// Secret is the key material: 1-16 bytes for Simple Password and
	// MD5, 1-20 bytes for SHA1 (RFC 5880 Sections 4.2, 4.3, 4.4).
	Secret []byte //nolint:gosec // G117: field name is intentional RFC terminology for auth key material
}

// -------------------------------------------------------------------------
// AuthKeyStore — Key Management Interface
// -------------------------------------------------------------------------

// AuthKeyStore manages authentication keys for a session.
// Supports multiple active keys for hitless key rotation.
type AuthKeyStore interface {
	// LookupKey returns the key with the given ID, or an error if not found.
	LookupKey(id uint8) (AuthKey, error)

	// CurrentKey returns the currently selected key for transmission.
	CurrentKey() AuthKey
}

// -------------------------------------------------------------------------
// AuthState — RFC 5880 Section 6.8.1
// -------------------------------------------------------------------------

// AuthState tracks per-session authentication state (RFC 5880 Section 6.8.1).
type AuthState struct {
	// Type is bfd.AuthType: the auth type in use, or zero if none.
	Type AuthType

	// RcvAuthSeq is bfd.RcvAuthSeq: last received sequence number.
	// Initial value is unimportant (RFC 5880 Section 6.8.1).
	RcvAuthSeq uint32

	// XmitAuthSeq is bfd.XmitAuthSeq: next sequence number to transmit.
	// MUST be initialized to a random 32-bit value (RFC 5880 Section 6.8.1).
	XmitAuthSeq uint32

	// AuthSeqKnown is bfd.AuthSeqKnown: true if expected receive sequence
	// is known. MUST be initialized to false (RFC 5880 Section 6.8.1).
	// MUST be reset to false after no packets received for 2x Detection Time.
	AuthSeqKnown bool
}

// NewAuthState creates a new AuthState with XmitAuthSeq initialized to a
// cryptographically random value per RFC 5880 Section 6.8.1.
func NewAuthState(authType AuthType) (*AuthState, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("initialize auth state: %w", err)
	}

	return &AuthState{
		Type:         authType,
		XmitAuthSeq:  binary.BigEndian.Uint32(buf[:]),
		AuthSeqKnown: false, // RFC 5880 Section 6.8.1: MUST be initialized to 0.
	}, nil
}

// -------------------------------------------------------------------------
// Authenticator — Interface
// -------------------------------------------------------------------------

// Authenticator handles authentication for BFD Control packets.
// Implementations correspond to RFC 5880 Sections 6.7.2, 6.7.3, 6.7.4.
type Authenticator interface {
	// Sign populates the auth section and computes the digest.
	// buf is the full serialized packet buffer, n is valid byte count.
	Sign(state *AuthState, keys AuthKeyStore, pkt *ControlPacket, buf []byte, n int) error

	// Verify checks the authentication of a received packet.
	// buf is the full received packet buffer, n is valid byte count.
	Verify(state *AuthState, keys AuthKeyStore, pkt *ControlPacket, buf []byte, n int) error
}

// -------------------------------------------------------------------------
// seqInWindow — Circular uint32 window check
// -------------------------------------------------------------------------

// SeqInWindow checks if seq falls within [lo, hi] in circular uint32 space.
// Used by MD5 and SHA1 authentication (RFC 5880 Sections 6.7.3, 6.7.4).
//
// For non-meticulous: lo = RcvAuthSeq, hi = RcvAuthSeq + 3*DetectMult.
// For meticulous: lo = RcvAuthSeq + 1, hi = RcvAuthSeq + 3*DetectMult.
//
// Handles wrap-around: if hi < lo (numerically, due to overflow), the
// window wraps around the uint32 space.
func SeqInWindow(seq, lo, hi uint32) bool {
	// Circular arithmetic: seq is in [lo, hi] if (seq - lo) <= (hi - lo).
	// All operations wrap naturally with uint32 overflow.
	return seq-lo <= hi-lo
}

// -------------------------------------------------------------------------
// SimplePasswordAuth — RFC 5880 Section 6.7.2
// -------------------------------------------------------------------------

// SimplePasswordAuth implements Simple Password authentication
// (RFC 5880 Section 6.7.2). Auth Len = len(password) + 3.
type SimplePasswordAuth struct{}

// Sign sets the auth section for Simple Password (RFC 5880 Section 6.7.2).
func (a SimplePasswordAuth) Sign(
	_ *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	_ []byte,
	_ int,
) error {
	key := keys.CurrentKey()
	pkt.Auth = &AuthSection{
		Type: AuthTypeSimplePassword,
		Len: uint8( //nolint:gosec // G115: password max 16 bytes per RFC 5880 Section 4.2, sum fits uint8.
			authSimpleHeaderSize + len(key.Secret),
		),
		KeyID:    key.ID,
		AuthData: key.Secret,
	}
	pkt.AuthPresent = true

	return nil
}

// Verify checks Simple Password authentication (RFC 5880 Section 6.7.2).
func (a SimplePasswordAuth) Verify(
	_ *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	_ []byte,
	_ int,
) error {
	if err := requireAuthSection(pkt); err != nil {
		return err
	}

	if pkt.Auth.Type != AuthTypeSimplePassword {
		return fmt.Errorf("simple password: got type %d: %w",
			pkt.Auth.Type, ErrAuthTypeMismatch)
	}

	key, err := keys.LookupKey(pkt.Auth.KeyID)
	if err != nil {
		return fmt.Errorf("simple password key %d: %w",
			pkt.Auth.KeyID, ErrAuthKeyNotFound)
	}

	return verifyPassword(key, pkt.Auth)
}

// verifyPassword checks password length and value match.
func verifyPassword(key AuthKey, auth *AuthSection) error {
	expectedLen := uint8( //nolint:gosec // G115: password max 16 bytes per RFC 5880 Section 4.2, sum fits uint8.
		authSimpleHeaderSize + len(key.Secret),
	)
	if auth.Len != expectedLen {
		return fmt.Errorf("simple password: auth len %d, expected %d: %w",
			auth.Len, expectedLen, ErrAuthLenMismatch)
	}

	if subtle.ConstantTimeCompare(auth.AuthData, key.Secret) != 1 {
		return fmt.Errorf("simple password: %w", ErrAuthPasswordMismatch)
	}

	return nil
}

// -------------------------------------------------------------------------
// KeyedMD5Auth — RFC 5880 Section 6.7.3, Type=2
// -------------------------------------------------------------------------

// KeyedMD5Auth implements Keyed MD5 authentication
// (RFC 5880 Section 6.7.3, Type=2). Non-meticulous: sequence number
// is incremented on state change, not every packet.
type KeyedMD5Auth struct{}

// Sign computes the Keyed MD5 digest (RFC 5880 Section 6.7.3).
func (a KeyedMD5Auth) Sign(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	return signHash(state, keys, pkt, buf, n, hashParamsMD5())
}

// Verify checks the Keyed MD5 digest (RFC 5880 Section 6.7.3).
func (a KeyedMD5Auth) Verify(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	return verifyHash(state, keys, pkt, buf, n, hashParamsMD5())
}

// -------------------------------------------------------------------------
// MeticulousKeyedMD5Auth — RFC 5880 Section 6.7.3, Type=3
// -------------------------------------------------------------------------

// MeticulousKeyedMD5Auth implements Meticulous Keyed MD5 authentication
// (RFC 5880 Section 6.7.3, Type=3). The sequence number MUST be
// incremented on every transmitted packet.
type MeticulousKeyedMD5Auth struct{}

// Sign computes the Meticulous Keyed MD5 digest (RFC 5880 Section 6.7.3).
func (a MeticulousKeyedMD5Auth) Sign(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	p := hashParamsMD5()
	p.meticulous = true
	p.authType = AuthTypeMeticulousKeyedMD5

	return signHash(state, keys, pkt, buf, n, p)
}

// Verify checks the Meticulous Keyed MD5 digest (RFC 5880 Section 6.7.3).
func (a MeticulousKeyedMD5Auth) Verify(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	p := hashParamsMD5()
	p.meticulous = true
	p.authType = AuthTypeMeticulousKeyedMD5

	return verifyHash(state, keys, pkt, buf, n, p)
}

// -------------------------------------------------------------------------
// KeyedSHA1Auth — RFC 5880 Section 6.7.4, Type=4
// -------------------------------------------------------------------------

// KeyedSHA1Auth implements Keyed SHA1 authentication
// (RFC 5880 Section 6.7.4, Type=4). MUST be supported per RFC 5880
// Section 6.7.
type KeyedSHA1Auth struct{}

// Sign computes the Keyed SHA1 hash (RFC 5880 Section 6.7.4).
func (a KeyedSHA1Auth) Sign(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	return signHash(state, keys, pkt, buf, n, hashParamsSHA1())
}

// Verify checks the Keyed SHA1 hash (RFC 5880 Section 6.7.4).
func (a KeyedSHA1Auth) Verify(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	return verifyHash(state, keys, pkt, buf, n, hashParamsSHA1())
}

// -------------------------------------------------------------------------
// MeticulousKeyedSHA1Auth — RFC 5880 Section 6.7.4, Type=5
// -------------------------------------------------------------------------

// MeticulousKeyedSHA1Auth implements Meticulous Keyed SHA1 authentication
// (RFC 5880 Section 6.7.4, Type=5). The sequence number MUST be
// incremented on every transmitted packet.
type MeticulousKeyedSHA1Auth struct{}

// Sign computes the Meticulous Keyed SHA1 hash (RFC 5880 Section 6.7.4).
func (a MeticulousKeyedSHA1Auth) Sign(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	p := hashParamsSHA1()
	p.meticulous = true
	p.authType = AuthTypeMeticulousKeyedSHA1

	return signHash(state, keys, pkt, buf, n, p)
}

// Verify checks the Meticulous Keyed SHA1 hash (RFC 5880 Section 6.7.4).
func (a MeticulousKeyedSHA1Auth) Verify(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
) error {
	p := hashParamsSHA1()
	p.meticulous = true
	p.authType = AuthTypeMeticulousKeyedSHA1

	return verifyHash(state, keys, pkt, buf, n, p)
}

// -------------------------------------------------------------------------
// hashParams — shared configuration for MD5/SHA1 authenticators
// -------------------------------------------------------------------------

// hashParams holds the parameters that differentiate MD5 from SHA1
// and meticulous from non-meticulous authenticators.
type hashParams struct {
	authType   AuthType
	authLen    uint8
	digestSize int
	meticulous bool
}

// hashParamsMD5 returns hashParams for Keyed MD5 (RFC 5880 Section 6.7.3).
func hashParamsMD5() hashParams {
	return hashParams{
		authType:   AuthTypeKeyedMD5,
		authLen:    authLenMD5,
		digestSize: md5DigestSize,
	}
}

// hashParamsSHA1 returns hashParams for Keyed SHA1 (RFC 5880 Section 6.7.4).
func hashParamsSHA1() hashParams {
	return hashParams{
		authType:   AuthTypeKeyedSHA1,
		authLen:    authLenSHA1,
		digestSize: sha1DigestSize,
	}
}

// -------------------------------------------------------------------------
// signHash — shared sign logic for MD5/SHA1 (RFC 5880 Sections 6.7.3/6.7.4)
// -------------------------------------------------------------------------

// signHash implements the common signing procedure for Keyed MD5 and
// Keyed SHA1 auth types (RFC 5880 Sections 6.7.3, 6.7.4):
//  1. Set auth section fields (type, len, key ID, sequence number).
//  2. Place the key (zero-padded to digest size) in the digest slot.
//  3. Serialize the full packet into buf.
//  4. Compute hash over buf[0:n].
//  5. Replace the digest slot with the computed hash.
func signHash(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	_ int,
	p hashParams,
) error {
	key := keys.CurrentKey()

	// RFC 5880 Section 6.7.3/6.7.4: increment sequence number.
	// For meticulous variants, caller increments on every packet.
	// For non-meticulous, the caller is responsible for increment timing,
	// but we increment here for simplicity (SHOULD per RFC).
	state.XmitAuthSeq++

	pkt.Auth = buildAuthSection(key, state, p)
	pkt.AuthPresent = true

	// Re-serialize the packet with the key in the digest slot.
	n, err := MarshalControlPacket(pkt, buf)
	if err != nil {
		return fmt.Errorf("sign hash: marshal: %w", err)
	}

	// Compute hash and write digest into buf and pkt.Auth.Digest.
	computeAndPlaceDigest(buf, n, p)
	pkt.Auth.Digest = copyDigest(buf, p)

	return nil
}

// buildAuthSection creates an AuthSection with the key material placed
// in the digest slot for hash computation (RFC 5880 Sections 6.7.3/6.7.4).
func buildAuthSection(key AuthKey, state *AuthState, p hashParams) *AuthSection {
	// RFC 5880 Section 6.7.3/6.7.4: "the Auth Key/Hash field is set to
	// the value of the Authentication Key."
	digest := make([]byte, p.digestSize)
	copy(digest, key.Secret)

	return &AuthSection{
		Type:           p.authType,
		Len:            p.authLen,
		KeyID:          key.ID,
		SequenceNumber: state.XmitAuthSeq,
		Digest:         digest,
	}
}

// computeAndPlaceDigest computes the hash over buf[0:n] and writes
// the result into the digest slot at the correct offset.
func computeAndPlaceDigest(buf []byte, n int, p hashParams) {
	// Auth section digest starts at: HeaderSize + 8 (type+len+keyid+reserved+seq).
	digestOffset := HeaderSize + 8
	digest := computeDigest(buf[:n], p)
	copy(buf[digestOffset:], digest)
}

// copyDigest extracts a copy of the digest from buf after it has been
// written by computeAndPlaceDigest.
func copyDigest(buf []byte, p hashParams) []byte {
	digestOffset := HeaderSize + 8
	result := make([]byte, p.digestSize)
	copy(result, buf[digestOffset:digestOffset+p.digestSize])

	return result
}

// computeDigest computes MD5 or SHA1 hash over the given data.
func computeDigest(data []byte, p hashParams) []byte {
	if p.digestSize == md5DigestSize {
		sum := md5.Sum(data) //nolint:gosec // G401: MD5 required by RFC 5880 Section 6.7.3
		return sum[:]
	}

	sum := sha1.Sum(data) //nolint:gosec // G401: SHA1 required by RFC 5880 Section 6.7.4
	return sum[:]
}

// -------------------------------------------------------------------------
// verifyHash — shared verify logic (RFC 5880 Sections 6.7.3/6.7.4)
// -------------------------------------------------------------------------

// verifyHash implements the common verification procedure for Keyed MD5
// and Keyed SHA1 auth types (RFC 5880 Sections 6.7.3, 6.7.4):
//  1. Check auth section present and type matches.
//  2. Look up key by Auth Key ID.
//  3. Check sequence number window.
//  4. Save received digest, replace with key material.
//  5. Compute hash over modified packet.
//  6. Compare computed hash with saved digest.
//  7. Update RcvAuthSeq on success.
func verifyHash(
	state *AuthState,
	keys AuthKeyStore,
	pkt *ControlPacket,
	buf []byte,
	n int,
	p hashParams,
) error {
	if err := validateHashAuth(pkt, p); err != nil {
		return err
	}

	key, err := keys.LookupKey(pkt.Auth.KeyID)
	if err != nil {
		return fmt.Errorf("hash auth key %d: %w",
			pkt.Auth.KeyID, ErrAuthKeyNotFound)
	}

	if err := checkSeqWindow(state, pkt, p); err != nil {
		return err
	}

	return verifyAndUpdateSeq(state, pkt, key, buf, n, p)
}

// validateHashAuth checks the auth section is present and has the
// correct type and length.
func validateHashAuth(pkt *ControlPacket, p hashParams) error {
	if err := requireAuthSection(pkt); err != nil {
		return err
	}

	if pkt.Auth.Type != p.authType {
		return fmt.Errorf("hash auth: got type %d, expected %d: %w",
			pkt.Auth.Type, p.authType, ErrAuthTypeMismatch)
	}

	if pkt.Auth.Len != p.authLen {
		return fmt.Errorf("hash auth: auth len %d, expected %d: %w",
			pkt.Auth.Len, p.authLen, ErrAuthLenMismatch)
	}

	return nil
}

// checkSeqWindow validates the sequence number against the acceptance
// window (RFC 5880 Sections 6.7.3, 6.7.4).
func checkSeqWindow(state *AuthState, pkt *ControlPacket, p hashParams) error {
	if !state.AuthSeqKnown {
		return nil // First packet: accept any sequence.
	}

	detectMult := uint32(pkt.DetectMult)
	lo := state.RcvAuthSeq
	hi := state.RcvAuthSeq + 3*detectMult

	if p.meticulous {
		// RFC 5880 Section 6.7.3/6.7.4 (meticulous):
		// "The sequence number is greater than RcvAuthSeq."
		lo = state.RcvAuthSeq + 1
	}

	if !SeqInWindow(pkt.Auth.SequenceNumber, lo, hi) {
		return fmt.Errorf(
			"hash auth: seq %d outside window [%d, %d]: %w",
			pkt.Auth.SequenceNumber, lo, hi, ErrAuthSeqOutOfWindow)
	}

	return nil
}

// verifyAndUpdateSeq saves the digest, computes the expected hash,
// compares, and updates RcvAuthSeq on success.
func verifyAndUpdateSeq(
	state *AuthState,
	pkt *ControlPacket,
	key AuthKey,
	buf []byte,
	n int,
	p hashParams,
) error {
	// Save received digest before overwriting.
	savedDigest := make([]byte, p.digestSize)
	copy(savedDigest, pkt.Auth.Digest)

	// Replace digest slot with key material (zero-padded).
	digestOffset := HeaderSize + 8
	clearDigestSlot(buf, digestOffset, p.digestSize)
	copy(buf[digestOffset:], key.Secret)

	// Compute hash over the modified packet.
	computed := computeDigest(buf[:n], p)

	if subtle.ConstantTimeCompare(savedDigest, computed) != 1 {
		return fmt.Errorf("hash auth: %w", ErrAuthDigestMismatch)
	}

	// RFC 5880 Section 6.7.3/6.7.4: update receive sequence.
	state.RcvAuthSeq = pkt.Auth.SequenceNumber
	state.AuthSeqKnown = true

	return nil
}

// clearDigestSlot zeroes out the digest area in the buffer.
func clearDigestSlot(buf []byte, offset, size int) {
	for i := range size {
		buf[offset+i] = 0
	}
}

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

// requireAuthSection returns an error if the packet has no auth section.
func requireAuthSection(pkt *ControlPacket) error {
	if pkt.Auth == nil {
		return fmt.Errorf("verify auth: %w", ErrAuthMissingSection)
	}

	return nil
}
