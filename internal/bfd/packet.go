// Package bfd implements the core BFD protocol (RFC 5880).
//
// This includes the FSM (Section 6.8), session management, packet codec,
// authentication mechanisms, and discriminator allocation.
package bfd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// -------------------------------------------------------------------------
// Protocol Constants — RFC 5880 Section 4.1
// -------------------------------------------------------------------------

// Version is the BFD protocol version (RFC 5880 Section 4.1).
// This document defines protocol version 1.
const Version uint8 = 1

// HeaderSize is the mandatory BFD Control packet header size in bytes
// (RFC 5880 Section 4.1: 6 x 32-bit words = 24 bytes).
const HeaderSize = 24

// MaxPacketSize is the maximum BFD Control packet size in bytes.
// 24-byte header + up to 28 bytes for SHA1 auth section = 52 bytes.
// Padded to 64 for alignment and future auth types.
const MaxPacketSize = 64

// unknownStr is the string representation for unrecognized enum values.
const unknownStr = "Unknown"

// unknownFmt is the format string for unrecognized enum values with numeric code.
const unknownFmt = "Unknown(%d)"

// MinPacketSizeNoAuth is the minimum valid packet size when the A bit is
// clear (RFC 5880 Section 6.8.6: "24 if the A bit is clear").
const MinPacketSizeNoAuth = 24

// MinPacketSizeWithAuth is the minimum valid packet size when the A bit is
// set (RFC 5880 Section 6.8.6: "26 if the A bit is set").
// 24-byte header + 2 bytes minimum auth section (Auth Type + Auth Len).
const MinPacketSizeWithAuth = 26

// Auth section fixed sizes per RFC 5880 Sections 4.3 and 4.4.
const (
	// authLenMD5 is the fixed Auth Len for Keyed MD5 / Meticulous Keyed MD5
	// (RFC 5880 Section 4.3: "the length is 24").
	authLenMD5 = 24

	// authLenSHA1 is the fixed Auth Len for Keyed SHA1 / Meticulous Keyed SHA1
	// (RFC 5880 Section 4.4: "the length is 28").
	authLenSHA1 = 28

	// md5DigestSize is the MD5 digest length (16 bytes).
	md5DigestSize = 16

	// sha1DigestSize is the SHA1 hash length (20 bytes).
	sha1DigestSize = 20

	// simplePasswordMinLen is the minimum Simple Password length (1 byte)
	// (RFC 5880 Section 4.2: "MUST be from 1 to 16 bytes in length").
	simplePasswordMinLen = 1

	// simplePasswordMaxLen is the maximum Simple Password length (16 bytes).
	simplePasswordMaxLen = 16

	// authSimpleHeaderSize is the overhead before password data for Simple
	// Password auth: Auth Type (1) + Auth Len (1) + Auth Key ID (1) = 3 bytes
	// (RFC 5880 Section 4.2).
	authSimpleHeaderSize = 3
)

// -------------------------------------------------------------------------
// Diagnostic Codes — RFC 5880 Section 4.1
// -------------------------------------------------------------------------

// Diag represents the BFD Diagnostic code (RFC 5880 Section 4.1).
// This is a 5-bit field (values 0-8 defined, 9-31 reserved).
type Diag uint8

const (
	// DiagNone indicates no diagnostic (RFC 5880 Section 4.1: value 0).
	DiagNone Diag = 0

	// DiagControlTimeExpired indicates the control detection time expired
	// (RFC 5880 Section 4.1: value 1).
	DiagControlTimeExpired Diag = 1

	// DiagEchoFailed indicates the echo function failed
	// (RFC 5880 Section 4.1: value 2).
	DiagEchoFailed Diag = 2

	// DiagNeighborDown indicates the neighbor signaled session down
	// (RFC 5880 Section 4.1: value 3).
	DiagNeighborDown Diag = 3

	// DiagForwardingPlaneReset indicates the forwarding plane was reset
	// (RFC 5880 Section 4.1: value 4).
	DiagForwardingPlaneReset Diag = 4

	// DiagPathDown indicates the path is down
	// (RFC 5880 Section 4.1: value 5).
	DiagPathDown Diag = 5

	// DiagConcatPathDown indicates a concatenated path is down
	// (RFC 5880 Section 4.1: value 6).
	DiagConcatPathDown Diag = 6

	// DiagAdminDown indicates the session is administratively down
	// (RFC 5880 Section 4.1: value 7).
	DiagAdminDown Diag = 7

	// DiagReverseConcatPathDown indicates a reverse concatenated path is down
	// (RFC 5880 Section 4.1: value 8).
	DiagReverseConcatPathDown Diag = 8
)

// diagNames maps diagnostic codes to human-readable strings.
var diagNames = [9]string{
	"None",
	"Control Detection Time Expired",
	"Echo Function Failed",
	"Neighbor Signaled Session Down",
	"Forwarding Plane Reset",
	"Path Down",
	"Concatenated Path Down",
	"Administratively Down",
	"Reverse Concatenated Path Down",
}

// String returns the human-readable name for the diagnostic code.
func (d Diag) String() string {
	if int(d) < len(diagNames) {
		return diagNames[d]
	}
	return fmt.Sprintf(unknownFmt, d)
}

// -------------------------------------------------------------------------
// Session State — RFC 5880 Section 4.1
// -------------------------------------------------------------------------

// State represents the BFD session state (RFC 5880 Section 4.1, Section 6.2).
// This is a 2-bit field in the wire format.
type State uint8

const (
	// StateAdminDown indicates the session is administratively down
	// (RFC 5880 Section 4.1: value 0).
	StateAdminDown State = 0

	// StateDown indicates the session is down or has just been created
	// (RFC 5880 Section 4.1: value 1).
	StateDown State = 1

	// StateInit indicates the remote session is down but local session is up
	// (RFC 5880 Section 4.1: value 2).
	StateInit State = 2

	// StateUp indicates the session is fully established
	// (RFC 5880 Section 4.1: value 3).
	StateUp State = 3
)

// stateNames maps state values to human-readable strings.
var stateNames = [4]string{
	"AdminDown",
	"Down",
	"Init",
	"Up",
}

// String returns the human-readable name for the session state.
func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf(unknownFmt, s)
}

// -------------------------------------------------------------------------
// Authentication Type Codes — RFC 5880 Section 4.1
// -------------------------------------------------------------------------

// AuthType identifies the authentication mechanism (RFC 5880 Section 4.1).
type AuthType uint8

const (
	// AuthTypeNone indicates no authentication is in use.
	AuthTypeNone AuthType = 0

	// AuthTypeSimplePassword indicates Simple Password authentication
	// (RFC 5880 Section 4.2).
	AuthTypeSimplePassword AuthType = 1

	// AuthTypeKeyedMD5 indicates Keyed MD5 authentication
	// (RFC 5880 Section 4.3).
	AuthTypeKeyedMD5 AuthType = 2

	// AuthTypeMeticulousKeyedMD5 indicates Meticulous Keyed MD5 authentication
	// (RFC 5880 Section 4.3).
	AuthTypeMeticulousKeyedMD5 AuthType = 3

	// AuthTypeKeyedSHA1 indicates Keyed SHA1 authentication
	// (RFC 5880 Section 4.4).
	AuthTypeKeyedSHA1 AuthType = 4

	// AuthTypeMeticulousKeyedSHA1 indicates Meticulous Keyed SHA1 authentication
	// (RFC 5880 Section 4.4).
	AuthTypeMeticulousKeyedSHA1 AuthType = 5
)

// authTypeNames maps auth type values to human-readable strings.
var authTypeNames = [6]string{
	"None",
	"Simple Password",
	"Keyed MD5",
	"Meticulous Keyed MD5",
	"Keyed SHA1",
	"Meticulous Keyed SHA1",
}

// String returns the human-readable name for the authentication type.
func (a AuthType) String() string {
	if int(a) < len(authTypeNames) {
		return authTypeNames[a]
	}
	return fmt.Sprintf(unknownFmt, a)
}

// -------------------------------------------------------------------------
// ControlPacket — RFC 5880 Section 4.1
// -------------------------------------------------------------------------

// ControlPacket represents a decoded BFD Control packet (RFC 5880 Section 4.1).
//
// Field names match the RFC terminology exactly. All interval fields are in
// MICROSECONDS as specified in the RFC wire format. Callers convert to
// time.Duration at the boundary:
//
//	interval := time.Duration(pkt.DesiredMinTxInterval) * time.Microsecond
type ControlPacket struct {
	// Version is the protocol version (3 bits). MUST be 1
	// (RFC 5880 Section 4.1).
	Version uint8

	// Diag is the diagnostic code (5 bits) indicating the reason for
	// the last session state change (RFC 5880 Section 4.1).
	Diag Diag

	// State is the current BFD session state (2 bits)
	// (RFC 5880 Section 4.1).
	State State

	// Poll indicates the transmitting system is requesting verification
	// of connectivity or a parameter change (P bit, RFC 5880 Section 4.1).
	Poll bool

	// Final indicates the transmitting system is responding to a received
	// Poll (F bit, RFC 5880 Section 4.1).
	Final bool

	// ControlPlaneIndependent indicates BFD does not share fate with the
	// control plane (C bit, RFC 5880 Section 4.1).
	ControlPlaneIndependent bool

	// AuthPresent indicates the Authentication Section is present
	// (A bit, RFC 5880 Section 4.1).
	AuthPresent bool

	// Demand indicates Demand mode is active in the transmitting system
	// (D bit, RFC 5880 Section 4.1).
	Demand bool

	// Multipoint is reserved for future point-to-multipoint extensions.
	// MUST be zero on both transmit and receipt (M bit, RFC 5880 Section 4.1).
	Multipoint bool

	// DetectMult is the detection time multiplier (RFC 5880 Section 4.1).
	// The negotiated transmit interval multiplied by this value provides
	// the Detection Time for the receiving system.
	DetectMult uint8

	// Length is the total packet length in bytes (RFC 5880 Section 4.1).
	Length uint8

	// MyDiscriminator is a unique, nonzero discriminator value generated
	// by the transmitting system (RFC 5880 Section 4.1). Offset: bytes 4-7.
	MyDiscriminator uint32

	// YourDiscriminator reflects back the received My Discriminator from
	// the remote system, or zero if unknown (RFC 5880 Section 4.1).
	// Offset: bytes 8-11.
	YourDiscriminator uint32

	// DesiredMinTxInterval is the minimum TX interval in MICROSECONDS
	// (RFC 5880 Section 4.1). The value zero is reserved.
	// Offset: bytes 12-15.
	DesiredMinTxInterval uint32

	// RequiredMinRxInterval is the minimum acceptable RX interval in
	// MICROSECONDS (RFC 5880 Section 4.1). Zero means "don't send me
	// periodic packets." Offset: bytes 16-19.
	RequiredMinRxInterval uint32

	// RequiredMinEchoRxInterval is the minimum acceptable Echo RX interval
	// in MICROSECONDS (RFC 5880 Section 4.1). Zero means the transmitting
	// system does not support Echo. Offset: bytes 20-23.
	RequiredMinEchoRxInterval uint32

	// Auth holds the decoded authentication section, nil if the A bit is
	// clear (RFC 5880 Section 4.1).
	Auth *AuthSection
}

// -------------------------------------------------------------------------
// AuthSection — RFC 5880 Sections 4.2, 4.3, 4.4
// -------------------------------------------------------------------------

// AuthSection represents the optional BFD authentication section.
// The wire format varies by AuthType:
//
//   - Simple Password (Type=1, RFC 5880 Section 4.2):
//     Auth Type(1) + Auth Len(1) + Key ID(1) + Password(1-16)
//     Auth Len = password length + 3.
//
//   - Keyed MD5 / Meticulous Keyed MD5 (Type=2,3, RFC 5880 Section 4.3):
//     Auth Type(1) + Auth Len(1) + Key ID(1) + Reserved(1) + SeqNum(4) + Digest(16)
//     Auth Len = 24 (fixed).
//
//   - Keyed SHA1 / Meticulous Keyed SHA1 (Type=4,5, RFC 5880 Section 4.4):
//     Auth Type(1) + Auth Len(1) + Key ID(1) + Reserved(1) + SeqNum(4) + Hash(20)
//     Auth Len = 28 (fixed).
type AuthSection struct {
	// Type is the authentication type code (1 byte, RFC 5880 Section 4.1).
	Type AuthType

	// Len is the total length of the authentication section in bytes
	// (RFC 5880 Section 4.1).
	Len uint8

	// KeyID is the authentication key ID, allowing multiple keys to be
	// active simultaneously (RFC 5880 Sections 4.2, 4.3, 4.4).
	KeyID uint8

	// AuthData holds the Simple Password bytes (1-16 bytes) when Type=1
	// (RFC 5880 Section 4.2: Auth Len = password length + 3).
	AuthData []byte

	// SequenceNumber provides replay protection for MD5 and SHA1 auth
	// types (RFC 5880 Sections 4.3, 4.4). Not used for Simple Password.
	SequenceNumber uint32

	// Digest holds the 16-byte MD5 digest (Type=2,3) or the 20-byte
	// SHA1 hash (Type=4,5). Not used for Simple Password.
	// NOTE: after UnmarshalControlPacket, this slice references the
	// original buffer (zero-copy). Callers must copy if the buffer
	// will be returned to PacketPool.
	Digest []byte
}

// -------------------------------------------------------------------------
// Codec Errors
// -------------------------------------------------------------------------

// Sentinel errors for packet validation failures. These correspond to the
// validation steps in RFC 5880 Section 6.8.6.
var (
	// ErrInvalidVersion indicates the Version field is not 1
	// (RFC 5880 Section 6.8.6 step 1).
	ErrInvalidVersion = errors.New("invalid BFD version")

	// ErrPacketTooShort indicates the received data is shorter than the
	// minimum BFD Control packet (24 bytes).
	ErrPacketTooShort = errors.New("packet too short")

	// ErrInvalidLength indicates the Length field is invalid
	// (RFC 5880 Section 6.8.6 step 2).
	ErrInvalidLength = errors.New("invalid length field")

	// ErrLengthExceedsPayload indicates the Length field exceeds the
	// encapsulation payload (RFC 5880 Section 6.8.6 step 3).
	ErrLengthExceedsPayload = errors.New("length exceeds payload")

	// ErrZeroDetectMult indicates the Detect Mult field is zero
	// (RFC 5880 Section 6.8.6 step 4).
	ErrZeroDetectMult = errors.New("detect multiplier is zero")

	// ErrMultipointSet indicates the Multipoint bit is nonzero
	// (RFC 5880 Section 6.8.6 step 5).
	ErrMultipointSet = errors.New("multipoint bit is set")

	// ErrZeroMyDiscriminator indicates My Discriminator is zero
	// (RFC 5880 Section 6.8.6 step 6).
	ErrZeroMyDiscriminator = errors.New("my discriminator is zero")

	// ErrZeroYourDiscriminator indicates Your Discriminator is zero
	// in a state other than Down or AdminDown
	// (RFC 5880 Section 6.8.6 step 7b).
	ErrZeroYourDiscriminator = errors.New("your discriminator is zero in non-Down state")

	// ErrAuthMismatch indicates a mismatch between the A bit and the
	// auth section (RFC 5880 Section 6.8.6 steps 8-9).
	ErrAuthMismatch = errors.New("auth present bit and auth section mismatch")

	// ErrBufTooSmall indicates the caller-provided buffer is too small
	// for MarshalControlPacket.
	ErrBufTooSmall = errors.New("buffer too small for BFD control packet")

	// ErrInvalidAuthType indicates an unknown authentication type.
	ErrInvalidAuthType = errors.New("invalid auth type")

	// ErrAuthSectionTruncated indicates the auth section data is truncated.
	ErrAuthSectionTruncated = errors.New("auth section truncated")
)

// unmarshalErrPrefix is the common error prefix for packet decoding failures.
const unmarshalErrPrefix = "unmarshal control packet"

// -------------------------------------------------------------------------
// MarshalControlPacket — RFC 5880 Section 4.1
// -------------------------------------------------------------------------

// MarshalControlPacket serializes a ControlPacket into buf.
// The buffer MUST be at least HeaderSize bytes (24). For packets with
// authentication, buf MUST be at least HeaderSize + auth section length.
// Callers typically provide a MaxPacketSize buffer from PacketPool.
//
// Returns the number of bytes written, or an error if the buffer is
// too small or the packet contains invalid data.
//
// Zero-allocation: uses encoding/binary.BigEndian directly on the buffer.
// The sync.Pool pattern from gVisor netstack applies — caller owns the buffer.
//
// Wire format (RFC 5880 Section 4.1):
//
//	Byte 0:    Version(3 bits) | Diag(5 bits)
//	Byte 1:    State(2 bits) | P | F | C | A | D | M
//	Byte 2:    Detect Mult
//	Byte 3:    Length
//	Bytes 4-7: My Discriminator (big-endian uint32)
//	Bytes 8-11: Your Discriminator (big-endian uint32)
//	Bytes 12-15: Desired Min TX Interval (big-endian uint32, microseconds)
//	Bytes 16-19: Required Min RX Interval (big-endian uint32, microseconds)
//	Bytes 20-23: Required Min Echo RX Interval (big-endian uint32, microseconds)
//	Bytes 24+: Authentication Section (optional)
func MarshalControlPacket(pkt *ControlPacket, buf []byte) (int, error) {
	// Calculate total packet length.
	totalLen := HeaderSize
	if pkt.AuthPresent && pkt.Auth != nil {
		totalLen += int(pkt.Auth.Len)
	}

	if len(buf) < totalLen {
		return 0, fmt.Errorf("marshal control packet: need %d bytes, got %d: %w",
			totalLen, len(buf), ErrBufTooSmall)
	}

	// Byte 0: Version(3 bits high) | Diag(5 bits low)
	// RFC 5880 Section 4.1: Version occupies bits 0-2, Diag occupies bits 3-7.
	buf[0] = (pkt.Version << 5) | (uint8(pkt.Diag) & 0x1F)

	// Byte 1: State(2 bits) | P | F | C | A | D | M
	// RFC 5880 Section 4.1: State occupies bits 0-1, flags occupy bits 2-7.
	var flags uint8
	flags = uint8(pkt.State) << 6
	if pkt.Poll {
		flags |= 1 << 5 // bit 2 (P)
	}
	if pkt.Final {
		flags |= 1 << 4 // bit 3 (F)
	}
	if pkt.ControlPlaneIndependent {
		flags |= 1 << 3 // bit 4 (C)
	}
	if pkt.AuthPresent {
		flags |= 1 << 2 // bit 5 (A)
	}
	if pkt.Demand {
		flags |= 1 << 1 // bit 6 (D)
	}
	if pkt.Multipoint {
		flags |= 1 << 0 // bit 7 (M)
	}
	buf[1] = flags

	// Byte 2: Detect Mult
	buf[2] = pkt.DetectMult

	// Byte 3: Length (total packet length)
	buf[3] = uint8(totalLen)

	// Bytes 4-7: My Discriminator (big-endian)
	binary.BigEndian.PutUint32(buf[4:8], pkt.MyDiscriminator)

	// Bytes 8-11: Your Discriminator (big-endian)
	binary.BigEndian.PutUint32(buf[8:12], pkt.YourDiscriminator)

	// Bytes 12-15: Desired Min TX Interval (big-endian, microseconds)
	binary.BigEndian.PutUint32(buf[12:16], pkt.DesiredMinTxInterval)

	// Bytes 16-19: Required Min RX Interval (big-endian, microseconds)
	binary.BigEndian.PutUint32(buf[16:20], pkt.RequiredMinRxInterval)

	// Bytes 20-23: Required Min Echo RX Interval (big-endian, microseconds)
	binary.BigEndian.PutUint32(buf[20:24], pkt.RequiredMinEchoRxInterval)

	// Optional Authentication Section (RFC 5880 Sections 4.2, 4.3, 4.4).
	if pkt.AuthPresent && pkt.Auth != nil {
		if err := marshalAuthSection(pkt.Auth, buf[HeaderSize:]); err != nil {
			return 0, fmt.Errorf("marshal auth section: %w", err)
		}
	}

	return totalLen, nil
}

// marshalAuthSection writes the authentication section to buf.
// buf must be at least Auth.Len bytes. Caller has already verified this.
func marshalAuthSection(auth *AuthSection, buf []byte) error {
	if int(auth.Len) > len(buf) {
		return fmt.Errorf("auth section needs %d bytes, buffer has %d: %w",
			auth.Len, len(buf), ErrBufTooSmall)
	}

	// Common header: Auth Type (byte 0), Auth Len (byte 1).
	buf[0] = uint8(auth.Type)
	buf[1] = auth.Len

	switch auth.Type {
	case AuthTypeSimplePassword:
		// RFC 5880 Section 4.2: Type(1) + Len(1) + KeyID(1) + Password(1-16).
		buf[2] = auth.KeyID
		copy(buf[3:], auth.AuthData)

	case AuthTypeKeyedMD5, AuthTypeMeticulousKeyedMD5,
		AuthTypeKeyedSHA1, AuthTypeMeticulousKeyedSHA1:
		// RFC 5880 Section 4.3 (MD5): Type(1) + Len(1) + KeyID(1) + Reserved(1) +
		// SeqNum(4) + Digest(16). Auth Len = 24.
		// RFC 5880 Section 4.4 (SHA1): same layout, Digest(20). Auth Len = 28.
		// Wire format is identical; digest length is encoded in auth.Length.
		buf[2] = auth.KeyID
		buf[3] = 0 // Reserved: MUST be set to zero on transmit.
		binary.BigEndian.PutUint32(buf[4:8], auth.SequenceNumber)
		copy(buf[8:], auth.Digest)

	default:
		return fmt.Errorf("auth type %d: %w", auth.Type, ErrInvalidAuthType)
	}

	return nil
}

// -------------------------------------------------------------------------
// UnmarshalControlPacket — RFC 5880 Section 4.1, Section 6.8.6
// -------------------------------------------------------------------------

// UnmarshalControlPacket decodes a BFD Control packet from buf into pkt.
// The buffer must contain at least MinPacketSizeNoAuth bytes (24).
//
// Zero-allocation: pkt is filled in-place. Auth.Digest and Auth.AuthData
// reference slices of buf (no copy). Callers must copy these fields if the
// buffer will be returned to PacketPool before the packet is fully processed.
//
// Validation performed per RFC 5880 Section 6.8.6 (steps 1-7):
//
//  1. Version == 1
//  2. Length >= 24 (A=0) or >= 26 (A=1)
//  3. Length <= len(buf)
//  4. DetectMult != 0
//  5. Multipoint == 0
//  6. MyDiscriminator != 0
//  7. YourDiscriminator != 0 unless State is Down or AdminDown
//
// Steps 8-18 (auth verification, FSM transitions, timer updates) are
// performed by the session layer, not the codec.
func UnmarshalControlPacket(buf []byte, pkt *ControlPacket) error {
	// Pre-check: need at least 24 bytes to decode the mandatory header.
	if len(buf) < MinPacketSizeNoAuth {
		return fmt.Errorf("%s: received %d bytes, minimum %d: %w",
			unmarshalErrPrefix, len(buf), MinPacketSizeNoAuth, ErrPacketTooShort)
	}

	// Decode fixed header fields (bytes 0-3).
	decodeHeader(buf, pkt)

	// RFC 5880 Section 6.8.6 steps 1-7: validate decoded fields.
	if err := validateHeader(buf, pkt); err != nil {
		return err
	}

	// Bytes 4-23: discriminators and interval fields.
	decodeBody(buf, pkt)

	// RFC 5880 Section 6.8.6 steps 6-7: validate discriminators.
	if err := validateDiscriminators(pkt); err != nil {
		return err
	}

	// Decode optional Authentication Section if the A bit is set.
	pkt.Auth = nil
	if pkt.AuthPresent {
		auth := &AuthSection{}
		if err := unmarshalAuthSection(buf[HeaderSize:pkt.Length], auth); err != nil {
			return fmt.Errorf("%s: %w", unmarshalErrPrefix, err)
		}
		pkt.Auth = auth
	}

	return nil
}

// decodeHeader extracts the fixed 4-byte header fields from buf into pkt.
func decodeHeader(buf []byte, pkt *ControlPacket) {
	// Byte 0: Version(3 bits high) | Diag(5 bits low).
	pkt.Version = buf[0] >> 5
	pkt.Diag = Diag(buf[0] & 0x1F)

	// Byte 1: State(2 bits) | P | F | C | A | D | M.
	flags := buf[1]
	pkt.State = State(flags >> 6)
	pkt.Poll = flags&(1<<5) != 0
	pkt.Final = flags&(1<<4) != 0
	pkt.ControlPlaneIndependent = flags&(1<<3) != 0
	pkt.AuthPresent = flags&(1<<2) != 0
	pkt.Demand = flags&(1<<1) != 0
	pkt.Multipoint = flags&(1<<0) != 0

	// Bytes 2-3: Detect Mult and Length.
	pkt.DetectMult = buf[2]
	pkt.Length = buf[3]
}

// validateHeader checks RFC 5880 Section 6.8.6 steps 1-5.
func validateHeader(buf []byte, pkt *ControlPacket) error {
	// Step 1: version must be 1.
	if pkt.Version != Version {
		return fmt.Errorf("%s: version %d: %w",
			unmarshalErrPrefix, pkt.Version, ErrInvalidVersion)
	}

	// Step 2: length >= minimum (24 or 26 with auth).
	minLen := uint8(MinPacketSizeNoAuth)
	if pkt.AuthPresent {
		minLen = MinPacketSizeWithAuth
	}
	if pkt.Length < minLen {
		return fmt.Errorf("%s: length field %d below minimum %d (auth=%t): %w",
			unmarshalErrPrefix, pkt.Length, minLen, pkt.AuthPresent, ErrInvalidLength)
	}

	// Step 3: length <= payload size.
	if int(pkt.Length) > len(buf) {
		return fmt.Errorf("%s: length field %d exceeds payload %d: %w",
			unmarshalErrPrefix, pkt.Length, len(buf), ErrLengthExceedsPayload)
	}

	// Step 4: detect multiplier != 0.
	if pkt.DetectMult == 0 {
		return fmt.Errorf("%s: %w", unmarshalErrPrefix, ErrZeroDetectMult)
	}

	// Step 5: multipoint bit must be zero.
	if pkt.Multipoint {
		return fmt.Errorf("%s: %w", unmarshalErrPrefix, ErrMultipointSet)
	}

	return nil
}

// decodeBody extracts the 20-byte body (discriminators + intervals) from buf.
func decodeBody(buf []byte, pkt *ControlPacket) {
	pkt.MyDiscriminator = binary.BigEndian.Uint32(buf[4:8])
	pkt.YourDiscriminator = binary.BigEndian.Uint32(buf[8:12])
	pkt.DesiredMinTxInterval = binary.BigEndian.Uint32(buf[12:16])
	pkt.RequiredMinRxInterval = binary.BigEndian.Uint32(buf[16:20])
	pkt.RequiredMinEchoRxInterval = binary.BigEndian.Uint32(buf[20:24])
}

// validateDiscriminators checks RFC 5880 Section 6.8.6 steps 6-7.
func validateDiscriminators(pkt *ControlPacket) error {
	// Step 6: my discriminator must be nonzero.
	if pkt.MyDiscriminator == 0 {
		return fmt.Errorf("%s: %w", unmarshalErrPrefix, ErrZeroMyDiscriminator)
	}

	// Step 7b: your discriminator zero only valid in Down or AdminDown.
	if pkt.YourDiscriminator == 0 && pkt.State != StateDown && pkt.State != StateAdminDown {
		return fmt.Errorf("%s: state %s with zero your discriminator: %w",
			unmarshalErrPrefix, pkt.State, ErrZeroYourDiscriminator)
	}

	return nil
}

// unmarshalAuthSection decodes the authentication section from buf.
// buf contains only the auth section bytes (header already stripped).
func unmarshalAuthSection(buf []byte, auth *AuthSection) error {
	// Minimum auth section: Type(1) + Len(1) = 2 bytes.
	if len(buf) < 2 {
		return fmt.Errorf("auth section: need at least 2 bytes, got %d: %w",
			len(buf), ErrAuthSectionTruncated)
	}

	auth.Type = AuthType(buf[0])
	auth.Len = buf[1]

	// Validate that the auth section length fits in the available data.
	if int(auth.Len) > len(buf)+HeaderSize {
		return fmt.Errorf("auth section: len field %d exceeds available data %d: %w",
			auth.Len, len(buf), ErrAuthSectionTruncated)
	}

	switch auth.Type {
	case AuthTypeSimplePassword:
		return unmarshalSimplePassword(buf, auth)
	case AuthTypeKeyedMD5, AuthTypeMeticulousKeyedMD5:
		return unmarshalHashAuth(buf, auth, authLenMD5, md5DigestSize, "MD5")
	case AuthTypeKeyedSHA1, AuthTypeMeticulousKeyedSHA1:
		return unmarshalHashAuth(buf, auth, authLenSHA1, sha1DigestSize, "SHA1")
	default:
		return fmt.Errorf("auth section: type %d: %w", auth.Type, ErrInvalidAuthType)
	}
}

// unmarshalSimplePassword decodes Simple Password auth (RFC 5880 Section 4.2).
func unmarshalSimplePassword(buf []byte, auth *AuthSection) error {
	if auth.Len < uint8(authSimpleHeaderSize+simplePasswordMinLen) {
		return fmt.Errorf("auth section: simple password len %d too short: %w",
			auth.Len, ErrAuthSectionTruncated)
	}
	if len(buf) < int(auth.Len) {
		return fmt.Errorf("auth section: simple password needs %d bytes, got %d: %w",
			auth.Len, len(buf), ErrAuthSectionTruncated)
	}
	auth.KeyID = buf[2]
	pwLen := int(auth.Len) - authSimpleHeaderSize
	if pwLen < simplePasswordMinLen || pwLen > simplePasswordMaxLen {
		return fmt.Errorf("auth section: simple password length %d out of range [%d, %d]: %w",
			pwLen, simplePasswordMinLen, simplePasswordMaxLen, ErrAuthSectionTruncated)
	}
	auth.AuthData = buf[3 : 3+pwLen]

	return nil
}

// unmarshalHashAuth decodes MD5 or SHA1 auth (RFC 5880 Sections 4.3, 4.4).
func unmarshalHashAuth(buf []byte, auth *AuthSection, expectedLen uint8, digestSize int, name string) error {
	if auth.Len != expectedLen {
		return fmt.Errorf("auth section: %s auth len %d, expected %d: %w",
			name, auth.Len, expectedLen, ErrInvalidLength)
	}
	if len(buf) < int(expectedLen) {
		return fmt.Errorf("auth section: %s needs %d bytes, got %d: %w",
			name, expectedLen, len(buf), ErrAuthSectionTruncated)
	}
	auth.KeyID = buf[2]
	// buf[3] = Reserved (ignored on receipt per RFC).
	auth.SequenceNumber = binary.BigEndian.Uint32(buf[4:8])
	auth.Digest = buf[8 : 8+digestSize]

	return nil
}

// -------------------------------------------------------------------------
// PacketPool — sync.Pool for zero-allocation I/O
// -------------------------------------------------------------------------

// PacketPool provides reusable buffers for BFD packet I/O.
// Callers Get() a *[]byte before receiving, and Put() it after processing.
//
// Pattern: gVisor netstack sync.Pool. The pool stores *[]byte (pointer to
// slice) to avoid interface allocation on Get()/Put().
//
// Usage:
//
//	bufp := PacketPool.Get().(*[]byte)
//	defer PacketPool.Put(bufp)
//	n, meta, err := conn.ReadPacket(*bufp)
var PacketPool = sync.Pool{
	New: func() any {
		buf := make([]byte, MaxPacketSize)
		return &buf
	},
}
