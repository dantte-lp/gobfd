# GoBFD Implementation Plan -- RFC 5880 / RFC 5881

Detailed breakdown of every data structure, type, interface, constant, and function
required to implement the BFD base protocol (RFC 5880) and BFD for IPv4/IPv6
single-hop (RFC 5881). Each item references its normative RFC section.

This document is organized by Go package in the `gobfd/` repository.

---

## Table of Contents

1. [Package `internal/bfd` -- Protocol Core](#1-package-internalbfd----protocol-core)
   - 1.1 packet.go -- BFD Control Packet Codec
   - 1.2 auth.go -- Authentication Mechanisms
   - 1.3 fsm.go -- Finite State Machine
   - 1.4 session.go -- Session State and Lifecycle
   - 1.5 discriminator.go -- Discriminator Allocator
   - 1.6 manager.go -- Session Manager
2. [Package `internal/netio` -- Network I/O](#2-package-internalnetio----network-io)
   - 2.1 rawsock.go -- Interface Definitions
   - 2.2 rawsock_linux.go -- Linux Implementation
   - 2.3 listener.go -- UDP Listeners
3. [Mandatory vs Optional Features](#3-mandatory-vs-optional-features)
4. [Implementation Phases](#4-implementation-phases)

---

## 1. Package `internal/bfd` -- Protocol Core

### 1.1 packet.go -- BFD Control Packet Codec

**Source**: RFC 5880 Section 4.1 (Generic BFD Control Packet Format)

#### Wire Format (24 bytes mandatory header)

```
Byte offset  Field                    Bits   Go type
-----------  -----                    ----   -------
 0           Version(3)|Diag(5)        8     uint8 (packed)
 1           Sta(2)|P|F|C|A|D|M        8     uint8 (packed)
 2           Detect Mult                8     uint8
 3           Length                     8     uint8
 4-7         My Discriminator          32     uint32
 8-11        Your Discriminator        32     uint32
12-15        Desired Min TX Interval   32     uint32 (microseconds)
16-19        Required Min RX Interval  32     uint32 (microseconds)
20-23        Req Min Echo RX Interval  32     uint32 (microseconds)
```

All multi-byte fields are big-endian (network byte order).

#### Constants

```go
// RFC 5880 Section 4.1 -- protocol version.
const Version uint8 = 1

// Mandatory header size in bytes (RFC 5880 Section 4.1).
const HeaderSize = 24

// Maximum BFD Control packet size: 24 header + 28 SHA1 auth = 52 bytes.
// Allow extra room for future auth types.
const MaxPacketSize = 64

// Minimum packet sizes for validation (RFC 5880 Section 6.8.6, step 2).
const (
    MinPacketSizeNoAuth   = 24  // A bit clear
    MinPacketSizeWithAuth = 26  // A bit set -- 24 header + 2 (type + len minimum)
)
```

#### Diagnostic Codes (RFC 5880 Section 4.1)

```go
// Diag represents the BFD Diagnostic code (RFC 5880 Section 4.1).
// 5-bit field, values 0-8 defined, 9-31 reserved.
type Diag uint8

const (
    DiagNone                  Diag = 0 // No Diagnostic
    DiagControlTimeExpired    Diag = 1 // Control Detection Time Expired
    DiagEchoFailed            Diag = 2 // Echo Function Failed
    DiagNeighborDown          Diag = 3 // Neighbor Signaled Session Down
    DiagForwardingPlaneReset  Diag = 4 // Forwarding Plane Reset
    DiagPathDown              Diag = 5 // Path Down
    DiagConcatPathDown        Diag = 6 // Concatenated Path Down
    DiagAdminDown             Diag = 7 // Administratively Down
    DiagReverseConcatPathDown Diag = 8 // Reverse Concatenated Path Down
)
```

#### Session State (RFC 5880 Section 4.1)

```go
// State represents the BFD session state (RFC 5880 Section 4.1, Section 6.2).
// 2-bit field in the wire format.
type State uint8

const (
    StateAdminDown State = 0
    StateDown      State = 1
    StateInit      State = 2
    StateUp        State = 3
)
```

#### Authentication Type Codes (RFC 5880 Section 4.1)

```go
// AuthType identifies the authentication mechanism (RFC 5880 Section 4.1).
type AuthType uint8

const (
    AuthTypeNone             AuthType = 0 // No authentication
    AuthTypeSimplePassword   AuthType = 1 // RFC 5880 Section 4.2
    AuthTypeKeyedMD5         AuthType = 2 // RFC 5880 Section 4.3
    AuthTypeMeticulousKeyedMD5  AuthType = 3 // RFC 5880 Section 4.3
    AuthTypeKeyedSHA1        AuthType = 4 // RFC 5880 Section 4.4
    AuthTypeMeticulousKeyedSHA1 AuthType = 5 // RFC 5880 Section 4.4
)
```

#### Packet Structure

```go
// ControlPacket represents a decoded BFD Control packet (RFC 5880 Section 4.1).
//
// Field names match the RFC terminology exactly. All interval fields are in
// MICROSECONDS as specified in the RFC wire format -- callers convert to
// time.Duration at the boundary.
type ControlPacket struct {
    Version    uint8  // 3 bits. MUST be 1.
    Diag       Diag   // 5 bits. Diagnostic code.
    State      State  // 2 bits. Session state.
    Poll       bool   // 1 bit. (P) Requesting connectivity verification.
    Final      bool   // 1 bit. (F) Responding to Poll.
    ControlPlaneIndependent bool // 1 bit. (C) BFD independent of control plane.
    AuthPresent bool  // 1 bit. (A) Authentication Section present.
    Demand     bool   // 1 bit. (D) Demand mode active.
    Multipoint bool   // 1 bit. (M) Reserved. MUST be zero (RFC 5880 Section 4.1).
    DetectMult uint8  // Detection time multiplier.
    Length     uint8  // Packet length in bytes.

    MyDiscriminator        uint32 // Local discriminator (nonzero, unique).
    YourDiscriminator      uint32 // Remote discriminator (0 if unknown).
    DesiredMinTxInterval   uint32 // Microseconds. MUST NOT be zero.
    RequiredMinRxInterval  uint32 // Microseconds. Zero = don't send me packets.
    RequiredMinEchoRxInterval uint32 // Microseconds. Zero = no echo support.

    // Auth holds the decoded authentication section, nil if A bit is clear.
    Auth *AuthSection
}
```

#### Authentication Section Structures (RFC 5880 Sections 4.2, 4.3, 4.4)

```go
// AuthSection represents the optional authentication section.
// The wire format varies by AuthType.
type AuthSection struct {
    Type   AuthType // Auth Type (1 byte).
    Len    uint8    // Auth Len (1 byte) -- total length of auth section.
    KeyID  uint8    // Auth Key ID (1 byte).

    // For Simple Password (Type=1): the password bytes (1-16 bytes).
    // RFC 5880 Section 4.2: Auth Len = password length + 3.
    Password []byte

    // For Keyed MD5 / Meticulous Keyed MD5 (Type=2,3):
    // RFC 5880 Section 4.3: Auth Len = 24, digest = 16 bytes.
    // For Keyed SHA1 / Meticulous Keyed SHA1 (Type=4,5):
    // RFC 5880 Section 4.4: Auth Len = 28, hash = 20 bytes.
    SequenceNumber uint32 // 4 bytes. Replay protection.
    Digest         []byte // 16 bytes (MD5) or 20 bytes (SHA1).
}
```

#### Codec Functions

```go
// MarshalControlPacket serializes a ControlPacket into buf.
// buf MUST be at least MaxPacketSize bytes (caller provides from sync.Pool).
// Returns the number of bytes written.
//
// Zero-allocation: uses encoding/binary.BigEndian directly on the buffer.
// Does NOT allocate -- the sync.Pool pattern from gVisor netstack applies.
//
// Reference: RFC 5880 Section 4.1 (wire format), Section 6.8.7 (field rules).
func MarshalControlPacket(pkt *ControlPacket, buf []byte) (int, error)

// UnmarshalControlPacket decodes a BFD Control packet from buf.
// buf must contain at least MinPacketSizeNoAuth bytes.
// Returns ControlPacket or an error describing the first validation failure.
//
// Zero-allocation: the returned ControlPacket is filled in-place.
// Auth.Digest / Auth.Password reference slices of buf (no copy).
//
// Validation performed (RFC 5880 Section 6.8.6, steps 1-5):
//   - Version == 1
//   - Length >= 24 (no auth) or >= 26 (auth present)
//   - Length <= len(buf) (not exceeding encapsulation payload)
//   - DetectMult != 0
//   - Multipoint == 0
//   - MyDiscriminator != 0
//   - YourDiscriminator != 0 unless State is Down or AdminDown
func UnmarshalControlPacket(buf []byte, pkt *ControlPacket) error
```

#### Buffer Pool

```go
// PacketPool provides reusable buffers for BFD packet I/O.
// Pattern: gVisor netstack sync.Pool.
var PacketPool = sync.Pool{
    New: func() any {
        buf := make([]byte, MaxPacketSize)
        return &buf
    },
}
```

---

### 1.2 auth.go -- Authentication Mechanisms

**Source**: RFC 5880 Sections 6.7, 6.7.2, 6.7.3, 6.7.4

#### Key Store

```go
// AuthKey represents a single authentication key configured for a session.
type AuthKey struct {
    ID       uint8    // Key ID (matches Auth Key ID field).
    Type     AuthType // Authentication type this key is used for.
    Secret   []byte   // Key material: 1-16 bytes (password/MD5), 1-20 bytes (SHA1).
}

// AuthKeyStore manages authentication keys for a session.
// Supports multiple active keys for hitless key rotation.
type AuthKeyStore interface {
    // LookupKey returns the key with the given ID, or an error if not found.
    LookupKey(id uint8) (AuthKey, error)

    // CurrentKey returns the currently selected key for transmission.
    CurrentKey() AuthKey
}
```

#### Authentication State Variables (RFC 5880 Section 6.8.1)

```go
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

    // AuthSeqKnown is bfd.AuthSeqKnown: 1 if expected receive sequence is known.
    // MUST be initialized to 0 (RFC 5880 Section 6.8.1).
    // MUST be set to 0 after no packets received for 2x Detection Time.
    AuthSeqKnown bool
}
```

#### Authenticator Interface

```go
// Authenticator handles authentication for BFD Control packets.
//
// Implementations:
//   - SimplePasswordAuth  (RFC 5880 Section 6.7.2)
//   - KeyedMD5Auth        (RFC 5880 Section 6.7.3, Type=2)
//   - MeticulousKeyedMD5Auth (RFC 5880 Section 6.7.3, Type=3)
//   - KeyedSHA1Auth       (RFC 5880 Section 6.7.4, Type=4)
//   - MeticulousKeyedSHA1Auth (RFC 5880 Section 6.7.4, Type=5)
type Authenticator interface {
    // Sign populates the AuthSection of pkt and computes the digest/hash.
    // For Simple Password: stores password and key ID.
    // For MD5/SHA1: places key in digest field, computes hash over entire
    //   serialized packet, replaces key with hash.
    //
    // For Meticulous variants: caller MUST increment XmitAuthSeq before calling.
    // For non-Meticulous variants: caller increments XmitAuthSeq on state change.
    //
    // buf is the full serialized packet buffer (needed for hash computation).
    // n is the number of valid bytes in buf.
    Sign(state *AuthState, keys AuthKeyStore, pkt *ControlPacket, buf []byte, n int) error

    // Verify checks the authentication of a received packet.
    // Returns nil if the packet is accepted, or an error describing the rejection.
    //
    // Sequence number validation (RFC 5880 Section 6.7.3/6.7.4):
    //   - If AuthSeqKnown: check window [RcvAuthSeq, RcvAuthSeq + 3*DetectMult]
    //     for non-meticulous; [RcvAuthSeq+1, RcvAuthSeq + 3*DetectMult] for meticulous.
    //   - If !AuthSeqKnown: set AuthSeqKnown=true, RcvAuthSeq=received seq.
    //
    // Digest validation:
    //   - Replace digest field with key, compute hash over entire packet.
    //   - Compare computed hash with received digest.
    //
    // buf is the full received packet buffer.
    // n is the number of valid bytes in buf.
    Verify(state *AuthState, keys AuthKeyStore, pkt *ControlPacket, buf []byte, n int) error
}
```

#### Concrete Implementations

```go
// --- Simple Password (RFC 5880 Section 6.7.2) ---
//
// TX: Store password + key ID in auth section. Auth Len = len(password) + 3.
// RX: Discard if Auth Type != 1, Key ID not configured, Auth Len mismatch,
//     or password mismatch.
type SimplePasswordAuth struct{}

// --- Keyed MD5 (RFC 5880 Section 6.7.3, Type=2) ---
//
// Auth Len = 24 (fixed). Digest = 16 bytes (MD5).
// Sequence: incremented occasionally (SHOULD on state change or content change).
// RX window: [RcvAuthSeq, RcvAuthSeq + 3*DetectMult] inclusive.
type KeyedMD5Auth struct{}

// --- Meticulous Keyed MD5 (RFC 5880 Section 6.7.3, Type=3) ---
//
// Auth Len = 24. Sequence: MUST increment on every packet.
// RX window: [RcvAuthSeq+1, RcvAuthSeq + 3*DetectMult] inclusive (strictly greater).
type MeticulousKeyedMD5Auth struct{}

// --- Keyed SHA1 (RFC 5880 Section 6.7.4, Type=4) ---
//
// Auth Len = 28 (fixed). Hash = 20 bytes (SHA1).
// Sequence: incremented occasionally.
// RX window: [RcvAuthSeq, RcvAuthSeq + 3*DetectMult] inclusive.
// MUST be supported (RFC 5880 Section 6.7).
type KeyedSHA1Auth struct{}

// --- Meticulous Keyed SHA1 (RFC 5880 Section 6.7.4, Type=5) ---
//
// Auth Len = 28. Sequence: MUST increment on every packet.
// RX window: [RcvAuthSeq+1, RcvAuthSeq + 3*DetectMult] inclusive.
// MUST be supported (RFC 5880 Section 6.7).
type MeticulousKeyedSHA1Auth struct{}
```

#### Helper for Sequence Number Window Check

```go
// seqInWindow checks if seq falls within [lo, hi] in circular uint32 space.
// Used by MD5 and SHA1 authentication (RFC 5880 Sections 6.7.3, 6.7.4).
//
// For non-meticulous: lo = RcvAuthSeq, hi = RcvAuthSeq + 3*DetectMult.
// For meticulous: lo = RcvAuthSeq + 1, hi = RcvAuthSeq + 3*DetectMult.
func seqInWindow(seq, lo, hi uint32) bool
```

---

### 1.3 fsm.go -- Finite State Machine

**Source**: RFC 5880 Section 6.2, Section 6.8.6 (packet reception FSM transitions)

#### Events

```go
// Event represents a BFD FSM event (RFC 5880 Section 6.2, Section 6.8.6).
type Event uint8

const (
    // EventRecvAdminDown: received BFD Control with State = AdminDown.
    // RFC 5880 Section 6.8.6: "If received state is AdminDown".
    EventRecvAdminDown Event = iota

    // EventRecvDown: received BFD Control with State = Down.
    // RFC 5880 Section 6.8.6: "If bfd.SessionState is Down / received State is Down".
    EventRecvDown

    // EventRecvInit: received BFD Control with State = Init.
    // RFC 5880 Section 6.8.6: "received State is Init".
    EventRecvInit

    // EventRecvUp: received BFD Control with State = Up.
    // RFC 5880 Section 6.8.6: "received State is Init or Up".
    EventRecvUp

    // EventTimerExpired: Detection Time expired without receiving valid packet.
    // RFC 5880 Section 6.8.4: "set bfd.SessionState to Down and
    //   bfd.LocalDiag to 1 (Control Detection Time Expired)".
    EventTimerExpired

    // EventAdminDown: local administrative action to disable session.
    // RFC 5880 Section 6.8.16.
    EventAdminDown

    // EventAdminUp: local administrative action to re-enable session.
    // RFC 5880 Section 6.8.16: "Set bfd.SessionState to Down".
    EventAdminUp
)
```

#### Actions

```go
// Action represents a side-effect to execute on an FSM transition.
type Action func(s *Session)

// Predefined actions invoked from the FSM transition table.
var (
    // actionSendControl triggers immediate transmission of a BFD Control packet.
    actionSendControl Action

    // actionNotifyUp signals session consumers that the session has reached Up state.
    actionNotifyUp Action

    // actionNotifyDown signals session consumers that the session has gone Down.
    actionNotifyDown Action

    // actionSetDiagTimeExpired sets bfd.LocalDiag = 1 (Control Detection Time Expired).
    // RFC 5880 Section 6.8.4.
    actionSetDiagTimeExpired Action

    // actionSetDiagNeighborDown sets bfd.LocalDiag = 3 (Neighbor Signaled Session Down).
    // RFC 5880 Section 6.8.6.
    actionSetDiagNeighborDown Action

    // actionSetDiagAdminDown sets bfd.LocalDiag = 7 (Administratively Down).
    // RFC 5880 Section 6.8.16.
    actionSetDiagAdminDown Action

    // actionEnterAdminDown sets state to AdminDown, ceases Echo packets.
    // RFC 5880 Section 6.8.16.
    actionEnterAdminDown Action
)
```

#### Transition Table (RFC 5880 Section 6.8.6, Section 6.2)

This is the complete FSM table derived from the pseudocode in Section 6.8.6
and the state diagram in Section 6.2. Every transition MUST be implemented;
unlisted (state, event) pairs are silently ignored with a warning log.

```go
type stateEvent struct {
    state State
    event Event
}

type transition struct {
    newState State
    actions  []Action
}

// fsmTable is the complete BFD FSM transition table.
// Derived from RFC 5880 Section 6.8.6 pseudocode.
//
// The pseudocode logic maps to events as follows:
//
// "If bfd.SessionState is AdminDown, discard the packet"
//   -> No transitions from AdminDown via received packets.
//      Only EventAdminUp can leave AdminDown.
//
// "If received state is AdminDown":
//   "If bfd.SessionState is not Down":
//     Set bfd.LocalDiag to 3, Set bfd.SessionState to Down
//
// "Else" (received state is NOT AdminDown):
//   "If bfd.SessionState is Down":
//     "If received State is Down": -> Init
//     "Else if received State is Init": -> Up
//   "Else if bfd.SessionState is Init":
//     "If received State is Init or Up": -> Up
//   "Else (bfd.SessionState is Up)":
//     "If received State is Down":
//       Set bfd.LocalDiag to 3, Set bfd.SessionState to Down
//
// Timer expiration (Section 6.8.4):
//   If bfd.SessionState is Init or Up: -> Down, Diag=1
var fsmTable = map[stateEvent]transition{
    // === AdminDown state ===
    // Only administrative re-enable can leave AdminDown.
    {StateAdminDown, EventAdminUp}: {
        newState: StateDown,
        actions:  nil, // RFC 5880 Section 6.8.16: "Set bfd.SessionState to Down"
    },

    // === Down state ===
    // Down + recv AdminDown: remain Down (no-op, already Down).
    // Implicit: no entry needed, state does not change.

    // Down + recv Down -> Init (RFC 5880 Section 6.8.6)
    {StateDown, EventRecvDown}: {
        newState: StateInit,
        actions:  []Action{actionSendControl},
    },
    // Down + recv Init -> Up (RFC 5880 Section 6.8.6)
    {StateDown, EventRecvInit}: {
        newState: StateUp,
        actions:  []Action{actionSendControl, actionNotifyUp},
    },
    // Down + timer expired -> Down (remain, RFC 5880 Section 6.2 diagram: "TIMER" self-loop)
    // No state change, no actions (session was already Down).

    // Down + AdminDown -> AdminDown (RFC 5880 Section 6.8.16)
    {StateDown, EventAdminDown}: {
        newState: StateAdminDown,
        actions:  []Action{actionSetDiagAdminDown, actionEnterAdminDown},
    },

    // === Init state ===
    // Init + recv AdminDown -> Down (RFC 5880 Section 6.8.6)
    {StateInit, EventRecvAdminDown}: {
        newState: StateDown,
        actions:  []Action{actionSetDiagNeighborDown, actionNotifyDown},
    },
    // Init + recv Down -> remain Init (no transition listed in Section 6.8.6 for Init+Down)
    // The FSM diagram shows "DOWN" as self-loop on Init.
    {StateInit, EventRecvDown}: {
        newState: StateInit,
        actions:  nil,
    },
    // Init + recv Init -> Up (RFC 5880 Section 6.8.6)
    {StateInit, EventRecvInit}: {
        newState: StateUp,
        actions:  []Action{actionSendControl, actionNotifyUp},
    },
    // Init + recv Up -> Up (RFC 5880 Section 6.8.6)
    {StateInit, EventRecvUp}: {
        newState: StateUp,
        actions:  []Action{actionSendControl, actionNotifyUp},
    },
    // Init + timer expired -> Down (RFC 5880 Section 6.8.4:
    //   "if bfd.SessionState is Init or Up")
    {StateInit, EventTimerExpired}: {
        newState: StateDown,
        actions:  []Action{actionSetDiagTimeExpired, actionNotifyDown},
    },
    // Init + AdminDown -> AdminDown
    {StateInit, EventAdminDown}: {
        newState: StateAdminDown,
        actions:  []Action{actionSetDiagAdminDown, actionEnterAdminDown},
    },

    // === Up state ===
    // Up + recv AdminDown -> Down (RFC 5880 Section 6.8.6)
    {StateUp, EventRecvAdminDown}: {
        newState: StateDown,
        actions:  []Action{actionSetDiagNeighborDown, actionNotifyDown},
    },
    // Up + recv Down -> Down (RFC 5880 Section 6.8.6)
    {StateUp, EventRecvDown}: {
        newState: StateDown,
        actions:  []Action{actionSetDiagNeighborDown, actionNotifyDown},
    },
    // Up + recv Init -> Up (remain, RFC 5880 Section 6.2 diagram: "INIT, UP" self-loop)
    {StateUp, EventRecvInit}: {
        newState: StateUp,
        actions:  nil,
    },
    // Up + recv Up -> Up (remain, self-loop)
    {StateUp, EventRecvUp}: {
        newState: StateUp,
        actions:  nil,
    },
    // Up + timer expired -> Down (RFC 5880 Section 6.8.4)
    {StateUp, EventTimerExpired}: {
        newState: StateDown,
        actions:  []Action{actionSetDiagTimeExpired, actionNotifyDown},
    },
    // Up + AdminDown -> AdminDown
    {StateUp, EventAdminDown}: {
        newState: StateAdminDown,
        actions:  []Action{actionSetDiagAdminDown, actionEnterAdminDown},
    },
}
```

#### FSM Function

```go
// applyEvent applies an event to the session FSM and executes resulting actions.
// This function MUST be called from the session goroutine only (no lock needed).
//
// If the (state, event) pair has no entry in fsmTable, the event is logged at
// Warn level and ignored -- this handles cases like receiving a packet in
// AdminDown state (RFC 5880 Section 6.8.6: "discard the packet").
func (s *Session) applyEvent(event Event)
```

---

### 1.4 session.go -- Session State and Lifecycle

**Source**: RFC 5880 Section 6.8.1 (State Variables), Section 6.8.2-6.8.4 (Timers),
Section 6.8.7 (Transmitting), Section 6.5 (Poll Sequence)

#### Session Type

```go
// SessionType distinguishes single-hop from multi-hop sessions.
// Affects TTL validation and port selection.
type SessionType uint8

const (
    // SessionTypeSingleHop: RFC 5881 single-hop. Port 3784. TTL=255 both ways.
    SessionTypeSingleHop SessionType = iota

    // SessionTypeMultiHop: RFC 5883 multi-hop. Port 4784. TX TTL=255, RX TTL>=254.
    SessionTypeMultiHop
)
```

#### Session Role

```go
// SessionRole determines initial packet transmission behavior (RFC 5880 Section 6.1).
type SessionRole uint8

const (
    // RoleActive: MUST send BFD Control packets regardless of received packets.
    // RFC 5881 Section 3: both sides MUST take Active role for single-hop.
    RoleActive SessionRole = iota

    // RolePassive: MUST NOT send until a packet from remote is received.
    RolePassive
)
```

#### Session Configuration

```go
// SessionConfig contains the parameters needed to create a new BFD session.
type SessionConfig struct {
    // PeerAddr is the remote system's IP address.
    PeerAddr netip.Addr

    // LocalAddr is the local IP address to use for this session.
    LocalAddr netip.Addr

    // Interface is the network interface name (required for single-hop,
    // RFC 5881 Section 3: session MUST be bound to interface).
    Interface string

    // Type selects single-hop (RFC 5881) or multi-hop (RFC 5883).
    Type SessionType

    // Role selects Active or Passive (RFC 5880 Section 6.1).
    // For single-hop, MUST be Active (RFC 5881 Section 3).
    Role SessionRole

    // DesiredMinTxInterval is bfd.DesiredMinTxInterval.
    // Minimum: 50ms (implementation limit). In non-Up state, MUST be >= 1s
    // (RFC 5880 Section 6.8.3). Stored in microseconds internally.
    DesiredMinTxInterval time.Duration

    // RequiredMinRxInterval is bfd.RequiredMinRxInterval.
    // Zero means: do not send me periodic packets (RFC 5880 Section 6.8.1).
    RequiredMinRxInterval time.Duration

    // DetectMultiplier is bfd.DetectMult. MUST be >= 1 (RFC 5880 Section 6.8.1).
    DetectMultiplier uint8

    // AuthType and AuthKeys configure authentication (RFC 5880 Section 6.7).
    // AuthType=0 means no authentication.
    AuthType AuthType
    AuthKeys []AuthKey
}
```

#### Session State Variables (RFC 5880 Section 6.8.1 -- complete set)

```go
// Session represents a single BFD session with all state variables
// from RFC 5880 Section 6.8.1 and runtime data.
//
// A Session is owned by a single goroutine (s.run) and MUST NOT be accessed
// concurrently without proper synchronization. State queries from external
// goroutines go through atomic loads or channel-based requests.
type Session struct {
    // --- RFC 5880 Section 6.8.1 State Variables ---

    // bfd.SessionState -- perceived local session state.
    // MUST be initialized to Down (RFC 5880 Section 6.8.1).
    state State

    // bfd.RemoteSessionState -- last reported state from remote.
    // MUST be initialized to Down.
    remoteState State

    // bfd.LocalDiscr -- unique local discriminator (nonzero).
    // SHOULD be random for security (RFC 5880 Section 6.8.1).
    localDiscr uint32

    // bfd.RemoteDiscr -- remote discriminator. MUST be initialized to 0.
    // Reset to 0 if Detection Time passes without valid packet.
    remoteDiscr uint32

    // bfd.LocalDiag -- reason for most recent state change.
    // MUST be initialized to 0 (No Diagnostic).
    localDiag Diag

    // bfd.DesiredMinTxInterval -- minimum desired TX interval in microseconds.
    // MUST be initialized to >= 1,000,000 (1 second) per Section 6.8.3.
    desiredMinTxInterval time.Duration

    // bfd.RequiredMinRxInterval -- minimum acceptable RX interval in microseconds.
    requiredMinRxInterval time.Duration

    // bfd.RemoteMinRxInterval -- last received Required Min RX Interval.
    // MUST be initialized to 1 (RFC 5880 Section 6.8.1).
    remoteMinRxInterval time.Duration

    // bfd.DemandMode -- local demand mode flag. NOT IMPLEMENTED in MVP.
    // Always false.
    demandMode bool

    // bfd.RemoteDemandMode -- remote demand mode flag (from D bit).
    // MUST be initialized to 0. NOT IMPLEMENTED in MVP.
    remoteDemandMode bool

    // bfd.DetectMult -- detection time multiplier. Nonzero integer.
    detectMult uint8

    // Authentication state (RFC 5880 Section 6.8.1).
    authState AuthState

    // --- Derived / Runtime Fields ---

    // sessionType: single-hop (RFC 5881) or multi-hop (RFC 5883).
    sessionType SessionType

    // role: Active or Passive (RFC 5880 Section 6.1).
    role SessionRole

    // peerAddr: remote system's address.
    peerAddr netip.Addr

    // localAddr: local system's address.
    localAddr netip.Addr

    // ifName: bound interface name (RFC 5881).
    ifName string

    // sourcePort: UDP source port for this session (RFC 5881 Section 4:
    //   MUST be 49152-65535, same port for all packets in session, SHOULD be unique).
    sourcePort uint16

    // --- Poll Sequence (RFC 5880 Section 6.5) ---

    // pollActive: true if a Poll Sequence is in progress.
    // Set when P bit is sent, cleared when F bit is received.
    pollActive bool

    // pendingParamChange: true if timer parameters changed, waiting for Poll completion
    // before applying new TX interval (RFC 5880 Section 6.8.3).
    pendingParamChange bool

    // --- Cached Packet (FRR bfdd pattern) ---

    // cachedPacket: pre-serialized BFD Control packet. Rebuilt only when
    // parameters or state changes. On each TX interval, only Auth Sequence
    // and jitter-applied timing need updating.
    cachedPacket []byte

    // --- Timers ---

    // txTimer: periodic transmission timer.
    // Interval = max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval) - jitter.
    txTimer *time.Timer

    // detectTimer: detection timeout timer.
    // Interval = RemoteDetectMult * max(bfd.RequiredMinRxInterval, RemoteDesiredMinTxInterval).
    // RFC 5880 Section 6.8.4.
    detectTimer *time.Timer

    // --- Lifecycle ---
    cancel context.CancelFunc
    logger *slog.Logger

    // --- Channels ---

    // recvCh receives decoded packets from the network listener.
    recvCh chan receivedPacket

    // notifyCh sends state change notifications to the manager.
    notifyCh chan<- StateChange

    // --- Metrics ---
    metrics *SessionMetrics

    // --- Auth ---
    authenticator Authenticator
    keyStore      AuthKeyStore
}
```

#### Key Methods

```go
// run is the main session goroutine. It owns all mutable session state.
// It processes received packets, timer events, and administrative commands.
// Exits when ctx is cancelled.
//
// Goroutine lifetime = context lifetime (CLAUDE.md rule).
func (s *Session) run(ctx context.Context)

// processPacket handles a single received BFD Control packet.
// Implements the full RFC 5880 Section 6.8.6 reception procedure.
func (s *Session) processPacket(pkt *ControlPacket, meta PacketMeta)

// validateControlPacket performs the 13-step validation from RFC 5880 Section 6.8.6.
// Steps 1-12 (up to but not including FSM transitions).
func (s *Session) validateControlPacket(pkt *ControlPacket, meta PacketMeta) error

// updateTimers recalculates TX interval and Detection Time.
//
// TX interval (RFC 5880 Section 6.8.7):
//   actual_tx = max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval)
//
// Detection Time (RFC 5880 Section 6.8.4, Asynchronous mode):
//   detect_time = remote_detect_mult * max(bfd.RequiredMinRxInterval, remote_desired_min_tx)
//
// All intervals in MICROSECONDS per RFC, converted to time.Duration at boundaries.
func (s *Session) updateTimers()

// applyJitter reduces interval by 0-25% random factor (RFC 5880 Section 6.8.7).
// For DetectMult=1: interval is between 75% and 90% (not 75%-100%).
// Uses math/rand/v2 with crypto/rand seed.
func applyJitter(interval time.Duration, detectMult uint8) time.Duration

// rebuildCachedPacket reconstructs the pre-serialized packet.
// Called on: state change, parameter change, Poll/Final bit change.
// RFC 5880 Section 6.8.7 defines all field assignments.
func (s *Session) rebuildCachedPacket()

// sendControl transmits a BFD Control packet with the cached contents.
// Updates Auth Sequence Number for meticulous auth types.
func (s *Session) sendControl(ctx context.Context) error

// sendFinal transmits a BFD Control packet with F=1 in response to P=1.
// RFC 5880 Section 6.8.7: "as soon as practicable, without respect to
// the transmission timer or any other transmission limitations".
func (s *Session) sendFinal(ctx context.Context) error

// startPollSequence initiates a Poll Sequence (RFC 5880 Section 6.5).
// Sets P=1 on subsequent periodic transmissions. Does NOT send extra packets
// if periodic transmission is active (RFC 5880 Section 6.5).
func (s *Session) startPollSequence()

// terminatePollSequence ends the Poll Sequence when F bit is received.
// Applies any pending parameter changes (RFC 5880 Section 6.8.3).
func (s *Session) terminatePollSequence()

// slowTxInterval returns 1 second (1,000,000 microseconds).
// Used when bfd.SessionState is not Up (RFC 5880 Section 6.8.3:
//   "MUST set bfd.DesiredMinTxInterval to not less than one second").
func slowTxInterval() time.Duration
```

#### Timer Negotiation Formulas (RFC 5880 Sections 6.8.2, 6.8.3, 6.8.4)

```
Actual TX Interval (what we transmit at):
  actual_tx_interval = max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval)

  If bfd.SessionState != Up:
    actual_tx_interval = max(1_000_000us, bfd.RemoteMinRxInterval)
    (RFC 5880 Section 6.8.3: "not less than one second")

Detection Time (when we declare session down):
  Asynchronous mode:
    detection_time = remote.DetectMult * max(bfd.RequiredMinRxInterval, remote.DesiredMinTxInterval)
    (RFC 5880 Section 6.8.4)

  Demand mode (NOT IMPLEMENTED in MVP):
    detection_time = bfd.DetectMult * max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval)
```

#### Supporting Types

```go
// receivedPacket bundles a decoded packet with transport metadata.
type receivedPacket struct {
    Packet *ControlPacket
    Meta   PacketMeta
}

// PacketMeta carries transport-layer information about a received packet.
type PacketMeta struct {
    SrcAddr   netip.Addr
    DstAddr   netip.Addr
    TTL       uint8
    Interface string
    Timestamp time.Time
}

// StateChange is emitted when the session FSM transitions.
type StateChange struct {
    LocalDiscr uint32
    PeerAddr   netip.Addr
    OldState   State
    NewState   State
    Diag       Diag
    Timestamp  time.Time
}

// SessionMetrics holds Prometheus metric references for a single session.
type SessionMetrics struct {
    packetsTx        prometheus.Counter
    packetsRx        prometheus.Counter
    packetsDropped   prometheus.Counter
    stateTransitions *prometheus.CounterVec // labels: old_state, new_state
    currentState     prometheus.Gauge
    txJitter         prometheus.Histogram
}
```

---

### 1.5 discriminator.go -- Discriminator Allocator

**Source**: RFC 5880 Section 6.3, Section 6.8.1

```go
// DiscriminatorAllocator generates unique, nonzero, random local discriminators.
//
// RFC 5880 Section 6.8.1: bfd.LocalDiscr "MUST be unique across all BFD
// sessions on this system, and nonzero. It SHOULD be set to a random
// (but still unique) value to improve security."
//
// Implementation: generates random uint32 values using crypto/rand,
// checking against a set of allocated values. The zero value is never returned.
type DiscriminatorAllocator struct {
    mu        sync.Mutex
    allocated map[uint32]struct{}
}

// Allocate returns a new unique random discriminator.
// Returns an error if the allocator is exhausted (extremely unlikely with uint32).
func (d *DiscriminatorAllocator) Allocate() (uint32, error)

// Release returns a discriminator to the pool.
// Called when a session is destroyed.
func (d *DiscriminatorAllocator) Release(discr uint32)

// IsAllocated checks if a discriminator is currently in use.
func (d *DiscriminatorAllocator) IsAllocated(discr uint32) bool
```

---

### 1.6 manager.go -- Session Manager

**Source**: RFC 5880 Section 6.3 (demultiplexing), Section 6.8.6 (session lookup)

```go
// Manager owns all BFD sessions, handles demultiplexing of incoming packets,
// and provides the CRUD API for session lifecycle.
//
// Demultiplexing strategy (RFC 5880 Section 6.8.6, Section 6.3):
//
// 1. If Your Discriminator != 0:
//    Look up session by Your Discriminator (O(1) map lookup).
//    If no session found, discard.
//
// 2. If Your Discriminator == 0 AND State is Down or AdminDown:
//    Match by (source IP, interface) for single-hop (RFC 5881 Section 3).
//    Match by (source IP, dest IP) for multi-hop (RFC 5883).
//    If no match found, MAY create new session (implementation choice).
//
// This two-tier lookup is the standard BFD demux pattern (FRR, GoBGP, Junos).
type Manager struct {
    // sessions indexed by local discriminator (primary lookup).
    sessions map[uint32]*Session

    // sessionsByPeer indexed by (peerAddr, interface) for initial demux
    // when Your Discriminator is zero.
    sessionsByPeer map[sessionKey]*Session

    mu sync.RWMutex

    discriminators *DiscriminatorAllocator

    // socket is the network I/O abstraction for sending/receiving packets.
    socket PacketConn

    // notifyCh aggregates state changes from all sessions.
    notifyCh chan StateChange

    logger *slog.Logger
}

// sessionKey is the lookup key for initial demultiplexing.
type sessionKey struct {
    peerAddr  netip.Addr
    localAddr netip.Addr
    ifName    string
}

// CreateSession creates and starts a new BFD session.
//
// Validates:
//   - DetectMultiplier >= 1 (RFC 5880 Section 6.8.1)
//   - DesiredMinTxInterval >= 50ms (implementation minimum)
//   - For single-hop: Role must be Active (RFC 5881 Section 3)
//   - For single-hop: Interface must be non-empty (RFC 5881 Section 3)
//   - No duplicate session for the same (peerAddr, localAddr, interface)
func (m *Manager) CreateSession(ctx context.Context, cfg SessionConfig) (*Session, error)

// DestroySession tears down a session by local discriminator.
// Cancels the session goroutine and removes it from all maps.
// The session continues transmitting for one Detection Time with AdminDown
// diagnostic to notify the remote system (RFC 5880 Section 6.8.16).
func (m *Manager) DestroySession(ctx context.Context, localDiscr uint32) error

// LookupByDiscriminator returns the session with the given local discriminator.
// Used for primary demux (RFC 5880 Section 6.8.6, Your Discriminator != 0).
func (m *Manager) LookupByDiscriminator(discr uint32) (*Session, bool)

// LookupByPeer returns the session matching the given peer criteria.
// Used for initial demux (RFC 5880 Section 6.8.6, Your Discriminator == 0).
func (m *Manager) LookupByPeer(key sessionKey) (*Session, bool)

// Demux routes a received packet to the correct session.
// Implements the two-tier lookup described above.
// Returns ErrSessionNotFound if no matching session exists.
func (m *Manager) Demux(pkt *ControlPacket, meta PacketMeta) error

// recvLoop reads packets from the socket and dispatches via Demux.
// Runs as a goroutine for the lifetime of the Manager.
func (m *Manager) recvLoop(ctx context.Context)

// StateChanges returns a read-only channel of session state transitions.
// Used by the gRPC server for streaming notifications.
func (m *Manager) StateChanges() <-chan StateChange

// Sessions returns a snapshot of all active sessions (for gRPC ListSessions).
func (m *Manager) Sessions() []SessionSnapshot
```

---

## 2. Package `internal/netio` -- Network I/O

### 2.1 rawsock.go -- Interface Definitions

**Source**: RFC 5881 Sections 4, 5; RFC 5883 Section 2

```go
// PacketConn abstracts BFD packet send/receive operations.
// Implementations are platform-specific (Linux in rawsock_linux.go).
//
// This interface enables testing with mock sockets.
type PacketConn interface {
    // ReadPacket reads a single BFD Control packet.
    // Returns the number of bytes read, transport metadata, and any error.
    // The buffer MUST come from bfd.PacketPool.
    //
    // Metadata includes: source IP, destination IP, TTL/Hop Limit, interface.
    // TTL is critical for GTSM validation (RFC 5881 Section 5, RFC 5883 Section 2).
    ReadPacket(buf []byte) (n int, meta PacketMeta, err error)

    // WritePacket sends a BFD Control packet.
    // dst specifies the target address. srcPort is the session's source port.
    // ifName is required for single-hop (SO_BINDTODEVICE).
    //
    // TTL MUST be set to 255 (RFC 5881 Section 5, RFC 5883 Section 2).
    WritePacket(buf []byte, n int, dst netip.AddrPort, srcPort uint16, ifName string) error

    // Close releases all socket resources.
    Close() error
}

// PacketMeta carries transport-layer metadata for a received packet.
// (Defined here for use by both netio and bfd packages.)
type PacketMeta struct {
    SrcAddr   netip.Addr
    DstAddr   netip.Addr
    SrcPort   uint16
    TTL       uint8       // Hop Limit for IPv6
    Interface string      // Interface name the packet arrived on
    Timestamp time.Time   // Kernel timestamp if available
}

// ReadBatcher extends PacketConn with batch I/O (recvmmsg/sendmmsg).
// Optional optimization -- not required for MVP.
type ReadBatcher interface {
    PacketConn
    // ReadBatch reads up to len(msgs) packets in a single syscall.
    // Uses ipv4.PacketConn.ReadBatch / ipv6.PacketConn.ReadBatch.
    ReadBatch(msgs []Message) (int, error)
}

// Message represents a single packet in a batch operation.
type Message struct {
    Buf  []byte
    N    int
    Meta PacketMeta
}
```

### 2.2 rawsock_linux.go -- Linux Implementation

**Source**: RFC 5881 Sections 4, 5, 6

```go
// LinuxPacketConn implements PacketConn using golang.org/x/net/ipv4,
// golang.org/x/net/ipv6, and golang.org/x/sys/unix.
//
// Socket configuration (all MUST requirements from RFC 5881):
//
//   1. UDP socket bound to port 3784 (single-hop, RFC 5881 Section 4)
//      or port 4784 (multi-hop, RFC 5883 Section 2).
//
//   2. Source port MUST be 49152-65535 for single-hop (RFC 5881 Section 4).
//
//   3. TTL/Hop Limit MUST be set to 255 on transmit (RFC 5881 Section 5).
//      Uses: ipv4.PacketConn.SetTTL(255) or ipv6.PacketConn.SetHopLimit(255).
//
//   4. TTL/Hop Limit MUST be received (for GTSM check).
//      IPv4: setsockopt IP_RECVTTL via net.ListenConfig.Control.
//      IPv6: setsockopt IPV6_RECVHOPLIMIT.
//
//   5. Control messages enabled for TTL, interface, destination address:
//      ipv4.PacketConn.SetControlMessage(ipv4.FlagTTL|ipv4.FlagInterface|ipv4.FlagDst, true)
//      ipv6.PacketConn.SetControlMessage(ipv6.FlagHopLimit|ipv6.FlagInterface|ipv6.FlagDst, true)
//
//   6. SO_BINDTODEVICE for interface binding (single-hop, RFC 5881 Section 3).
//      Set via net.ListenConfig.Control using unix.SO_BINDTODEVICE.
//
//   7. SO_REUSEADDR for multiple listeners.
//
// Socket options set via net.ListenConfig.Control (x/sys/unix, NOT syscall):
//
//   func(network, address string, c syscall.RawConn) error {
//       return c.Control(func(fd uintptr) {
//           unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE_INDEX, ifIndex)
//           unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL, 255)
//           unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_RECVTTL, 1)
//           // ... etc
//       })
//   }
type LinuxPacketConn struct {
    conn4 *ipv4.PacketConn // IPv4 listener
    conn6 *ipv6.PacketConn // IPv6 listener
    port  uint16           // listening port (3784 or 4784)
}

// NewSingleHopListener creates a listener for RFC 5881 single-hop BFD.
// Binds to UDP port 3784, sets TTL=255, enables IP_RECVTTL, SO_BINDTODEVICE.
//
// RFC 5881 Section 4: "destination port 3784"
// RFC 5881 Section 5: "TTL or Hop Limit value of 255"
func NewSingleHopListener(ctx context.Context, addr netip.Addr, ifName string) (*LinuxPacketConn, error)

// NewMultiHopListener creates a listener for RFC 5883 multi-hop BFD.
// Binds to UDP port 4784. TTL=255 on TX, check TTL>=254 on RX.
func NewMultiHopListener(ctx context.Context, addr netip.Addr) (*LinuxPacketConn, error)

// --- Source Port Allocation (RFC 5881 Section 4) ---

// AllocateSourcePort returns a unique source port in the range 49152-65535.
// RFC 5881 Section 4: "The source port MUST be in the range 49152 through 65535."
// "The same UDP source port number MUST be used for all BFD Control packets
// associated with a particular session."
// "The source port number SHOULD be unique among all BFD sessions on the system."
type SourcePortAllocator struct {
    mu        sync.Mutex
    allocated map[uint16]struct{}
}

func (a *SourcePortAllocator) Allocate() (uint16, error)
func (a *SourcePortAllocator) Release(port uint16)
```

#### Socket Options Summary

| Socket Option | IPv4 | IPv6 | Purpose | RFC Reference |
|---|---|---|---|---|
| `IP_TTL` / `IPV6_UNICAST_HOPS` | `unix.IP_TTL` | `unix.IPV6_UNICAST_HOPS` | Set outgoing TTL=255 | RFC 5881 Section 5 |
| `IP_RECVTTL` / `IPV6_RECVHOPLIMIT` | `unix.IP_RECVTTL` | `unix.IPV6_RECVHOPLIMIT` | Receive TTL in ancillary data | RFC 5881 Section 5 |
| `IP_PKTINFO` / `IPV6_RECVPKTINFO` | `unix.IP_PKTINFO` | `unix.IPV6_RECVPKTINFO` | Receive dest addr + interface | RFC 5881 Section 6 |
| `SO_BINDTODEVICE` | `unix.SO_BINDTODEVICE` | `unix.SO_BINDTODEVICE` | Bind to specific interface | RFC 5881 Section 3 |
| `SO_REUSEADDR` | `unix.SO_REUSEADDR` | `unix.SO_REUSEADDR` | Allow port reuse | Implementation |

### 2.3 listener.go -- UDP Listeners

```go
// ListenerConfig configures a BFD packet listener.
type ListenerConfig struct {
    // ListenAddr is the local address to bind.
    ListenAddr netip.Addr

    // Port is the UDP port (3784 for single-hop, 4784 for multi-hop).
    Port uint16

    // Interface is the interface name for SO_BINDTODEVICE (single-hop only).
    Interface string

    // BPFFilter, if true, attaches a BPF socket filter to pre-filter
    // non-BFD packets in the kernel. Optional performance optimization.
    BPFFilter bool
}

// Listener manages the UDP socket(s) for receiving BFD packets.
// Supports both IPv4 and IPv6 simultaneously.
type Listener struct {
    cfg     ListenerConfig
    conn4   *LinuxPacketConn // nil if IPv6-only
    conn6   *LinuxPacketConn // nil if IPv4-only
    logger  *slog.Logger
}

// NewListener creates and configures the listening socket(s).
func NewListener(ctx context.Context, cfg ListenerConfig) (*Listener, error)

// Recv blocks until a packet is received, returning the raw buffer and metadata.
// The caller is responsible for returning buf to bfd.PacketPool.
func (l *Listener) Recv(buf []byte) (n int, meta PacketMeta, err error)

// Close shuts down the listener and releases sockets.
func (l *Listener) Close() error
```

---

## 3. Mandatory vs Optional Features

### 3.1 MUST Implement (MVP)

| Feature | RFC Reference | Package | Status |
|---|---|---|---|
| Asynchronous mode | RFC 5880 Section 3.2 | `internal/bfd/` | MVP |
| BFD Control Packet encode/decode (24-byte header) | RFC 5880 Section 4.1 | `internal/bfd/packet.go` | MVP |
| All 4 FSM states (AdminDown, Down, Init, Up) | RFC 5880 Section 6.2 | `internal/bfd/fsm.go` | MVP |
| Complete FSM transition table | RFC 5880 Section 6.8.6 | `internal/bfd/fsm.go` | MVP |
| 13-step packet reception validation | RFC 5880 Section 6.8.6 | `internal/bfd/session.go` | MVP |
| Packet transmission field rules | RFC 5880 Section 6.8.7 | `internal/bfd/session.go` | MVP |
| Timer negotiation | RFC 5880 Sections 6.8.2, 6.8.3, 6.8.4 | `internal/bfd/session.go` | MVP |
| Detection Time calculation (Async mode) | RFC 5880 Section 6.8.4 | `internal/bfd/session.go` | MVP |
| TX interval jitter (25%, 90% for DetectMult=1) | RFC 5880 Section 6.8.7 | `internal/bfd/session.go` | MVP |
| Poll Sequence (P/F bit exchange) | RFC 5880 Section 6.5 | `internal/bfd/session.go` | MVP |
| Discriminator-based demultiplexing | RFC 5880 Section 6.3 | `internal/bfd/manager.go` | MVP |
| Administrative control (AdminDown) | RFC 5880 Section 6.8.16 | `internal/bfd/session.go` | MVP |
| Slow TX rate when not Up (>= 1 second) | RFC 5880 Section 6.8.3 | `internal/bfd/session.go` | MVP |
| Keyed SHA1 authentication | RFC 5880 Section 6.7.4 | `internal/bfd/auth.go` | MVP |
| Meticulous Keyed SHA1 authentication | RFC 5880 Section 6.7.4 | `internal/bfd/auth.go` | MVP |
| Single-hop IPv4 (port 3784, TTL=255 both ways) | RFC 5881 Sections 4, 5 | `internal/netio/` | MVP |
| Single-hop IPv6 (port 3784, Hop Limit=255) | RFC 5881 Sections 4, 5 | `internal/netio/` | MVP |
| Source port 49152-65535 | RFC 5881 Section 4 | `internal/netio/` | MVP |
| Interface binding (SO_BINDTODEVICE) | RFC 5881 Section 3 | `internal/netio/` | MVP |
| Active role for single-hop sessions | RFC 5881 Section 3 | `internal/bfd/` | MVP |
| Version field = 1 | RFC 5880 Section 4.1 | `internal/bfd/packet.go` | MVP |
| Multipoint bit always zero | RFC 5880 Section 4.1 | `internal/bfd/packet.go` | MVP |
| bfd.RemoteMinRxInterval initialized to 1 | RFC 5880 Section 6.8.1 | `internal/bfd/session.go` | MVP |
| bfd.XmitAuthSeq initialized to random value | RFC 5880 Section 6.8.1 | `internal/bfd/auth.go` | MVP |
| bfd.AuthSeqKnown reset after 2x Detection Time | RFC 5880 Section 6.8.1 | `internal/bfd/auth.go` | MVP |
| Session state preserved for Detection Time after going down | RFC 5880 Section 6.8.1 | `internal/bfd/manager.go` | MVP |

### 3.2 SHOULD/MAY Implement (Post-MVP)

| Feature | RFC Keyword | RFC Reference | Package | Priority |
|---|---|---|---|---|
| Simple Password authentication | "optional" (other forms) | RFC 5880 Section 6.7 | `internal/bfd/auth.go` | P1 |
| Keyed MD5 authentication | "optional" | RFC 5880 Section 6.7 | `internal/bfd/auth.go` | P2 |
| Meticulous Keyed MD5 authentication | "optional" | RFC 5880 Section 6.7 | `internal/bfd/auth.go` | P2 |
| Multi-hop (port 4784, TTL>=254 on RX) | MUST (in RFC 5883) | RFC 5883 Section 2 | `internal/netio/` | P1 |
| Demand mode | MAY | RFC 5880 Section 6.6 | `internal/bfd/` | P3 |
| Echo function | MAY | RFC 5880 Section 6.4 | `internal/bfd/` | P3 |
| BPF socket filter | Performance opt | N/A | `internal/netio/` | P2 |
| Batch I/O (recvmmsg/sendmmsg) | Performance opt | N/A | `internal/netio/` | P2 |
| Congestion control for multi-hop | MUST | RFC 5880 Section 7 | `internal/bfd/` | P1 (with multihop) |
| MPLS LSP BFD (stub) | MUST (in RFC 5884) | RFC 5884 | Stub interfaces | P3 |
| Micro-BFD for LAG | MUST (in RFC 7130) | RFC 7130 | `internal/bfd/` | P2 |

### 3.3 NOT Implemented (Explicit Exclusions)

| Feature | RFC Reference | Reason |
|---|---|---|
| Echo Mode | RFC 5880 Section 6.4 | Requires forwarding plane changes, out of scope for userspace daemon |
| Demand Mode | RFC 5880 Section 6.6 | Requires external connectivity verification mechanism |
| Multipoint BFD | RFC 5880 Section 4.1 (M bit) | Reserved for future RFC, M bit MUST be zero |
| BFD Echo packets (port 3785) | RFC 5881 Section 4 | Depends on Echo Mode |

---

## 4. Implementation Phases

### Phase 1: Packet Codec and FSM (Week 1-2)

Files to create:
- `internal/bfd/packet.go` + `internal/bfd/packet_test.go`
  - ControlPacket struct, MarshalControlPacket, UnmarshalControlPacket
  - All constants (State, Diag, AuthType)
  - Fuzz test: `func FuzzControlPacket(f *testing.F)` -- round-trip parse/marshal/parse
  - Table-driven tests for every validation step in Section 6.8.6

- `internal/bfd/fsm.go` + `internal/bfd/fsm_test.go`
  - Event, Action, transition types
  - Complete fsmTable (20+ entries from Section 6.8.6)
  - `applyEvent` function
  - Tests using `testing/synctest` for every transition
  - Test for every entry in the state diagram (Section 6.2)

### Phase 2: Session and Timers (Week 2-3)

Files to create:
- `internal/bfd/session.go` + `internal/bfd/session_test.go`
  - Session struct with all RFC 5880 Section 6.8.1 state variables
  - `run()` goroutine with select loop (recvCh, txTimer, detectTimer, ctx.Done)
  - `processPacket()` implementing full Section 6.8.6 procedure
  - `validateControlPacket()` implementing 13-step validation
  - `updateTimers()` with formulas from Section 6.8.4
  - `applyJitter()` per Section 6.8.7
  - `rebuildCachedPacket()` with all field assignments from Section 6.8.7
  - Poll Sequence management per Section 6.5
  - synctest-based timer tests (detection timeout, tx interval, slow rate)

- `internal/bfd/discriminator.go` + `internal/bfd/discriminator_test.go`
  - DiscriminatorAllocator with crypto/rand generation

### Phase 3: Authentication (Week 3-4)

Files to create:
- `internal/bfd/auth.go` + `internal/bfd/auth_test.go`
  - Authenticator interface
  - KeyedSHA1Auth + MeticulousKeyedSHA1Auth (MUST, RFC 5880 Section 6.7)
  - SimplePasswordAuth (convenience)
  - KeyedMD5Auth + MeticulousKeyedMD5Auth (compatibility)
  - seqInWindow helper for circular uint32 range check
  - AuthState management (AuthSeqKnown reset after 2x Detection Time)
  - Test vectors for each auth type

### Phase 4: Network I/O (Week 4-5)

Files to create:
- `internal/netio/rawsock.go`
  - PacketConn interface, PacketMeta, Message types

- `internal/netio/rawsock_linux.go`
  - LinuxPacketConn using x/net/ipv4, x/net/ipv6, x/sys/unix
  - NewSingleHopListener (port 3784, TTL=255, SO_BINDTODEVICE)
  - NewMultiHopListener (port 4784, TTL=255 TX, >=254 RX)
  - SourcePortAllocator (range 49152-65535)
  - All socket options from the table above

- `internal/netio/listener.go`
  - Listener struct, Recv(), Close()

### Phase 5: Session Manager (Week 5-6)

Files to create:
- `internal/bfd/manager.go` + `internal/bfd/manager_test.go`
  - Manager struct with dual-index lookup
  - CreateSession / DestroySession
  - Demux with two-tier lookup (Your Discriminator, then peer key)
  - recvLoop goroutine
  - StateChanges channel for gRPC integration

### Phase 6: Integration (Week 6-8)

Files to create/modify:
- `cmd/gobfd/main.go` -- daemon entry point
  - signal.NotifyContext + errgroup lifecycle
  - sd_notify(READY) via coreos/go-systemd
  - SIGHUP for hot reload
- `internal/server/` -- ConnectRPC gRPC server
- `internal/config/` -- koanf/v2 configuration
- `internal/metrics/` -- Prometheus collectors
- Integration tests with testcontainers-go

---

## Appendix A: Complete Packet Reception Procedure (RFC 5880 Section 6.8.6)

Step-by-step, each implemented as a check in `validateControlPacket` or
`processPacket`:

```
Step  Check                                            Action on Fail  Location
----  -----                                            --------------  --------
 1    Version == 1                                     Discard         validateControlPacket
 2    Length >= 24 (A=0) or >= 26 (A=1)                Discard         validateControlPacket
 3    Length <= encapsulation payload size              Discard         validateControlPacket
 4    Detect Mult != 0                                 Discard         validateControlPacket
 5    Multipoint (M) == 0                              Discard         validateControlPacket
 6    My Discriminator != 0                            Discard         validateControlPacket
 7a   Your Discriminator != 0 -> lookup by YD          Discard if none Demux
 7b   Your Discriminator == 0 AND State != Down/Admin  Discard         validateControlPacket
 7c   Your Discriminator == 0 -> lookup by peer info   Discard if none Demux
 8    A=1 but bfd.AuthType==0                          Discard         validateControlPacket
 9    A=0 but bfd.AuthType!=0                          Discard         validateControlPacket
10    A=1 -> authenticate per Section 6.7              Discard if fail processPacket
11    Set bfd.RemoteDiscr, bfd.RemoteState,                            processPacket
      bfd.RemoteDemandMode, bfd.RemoteMinRxInterval
12    Handle RequiredMinEchoRxInterval==0                               processPacket
13    Handle Final bit (terminate Poll Sequence)                        processPacket
14    Update transmit interval (Section 6.8.2)                         processPacket
15    Update Detection Time (Section 6.8.4)                            processPacket
16    FSM transitions based on (local state, remote state)             applyEvent
17    Check Demand mode activation/deactivation                        processPacket
18    Handle Poll bit -> send Final                                    processPacket
```

## Appendix B: Complete Packet Transmission Field Map (RFC 5880 Section 6.8.7)

```
Field                      Source                              Offset  Size
-----                      ------                              ------  ----
Version                    Constant 1                          0:3     3 bits
Diag                       bfd.LocalDiag                       0:5     5 bits
State                      bfd.SessionState                    1:2     2 bits
Poll (P)                   1 if Poll Sequence active, else 0   1:1     1 bit
Final (F)                  1 if responding to Poll, else 0     1:1     1 bit
C                          0 (control plane dependent)         1:1     1 bit
A                          1 if bfd.AuthType != 0              1:1     1 bit
D                          bfd.DemandMode if both Up, else 0   1:1     1 bit
M                          Constant 0                          1:1     1 bit
Detect Mult                bfd.DetectMult                      2       1 byte
Length                     24 + auth section length             3       1 byte
My Discriminator           bfd.LocalDiscr                      4-7     4 bytes
Your Discriminator         bfd.RemoteDiscr                     8-11    4 bytes
Desired Min TX Interval    bfd.DesiredMinTxInterval (usec)     12-15   4 bytes
Required Min RX Interval   bfd.RequiredMinRxInterval (usec)    16-19   4 bytes
Req Min Echo RX Interval   0 (no echo support in MVP)          20-23   4 bytes
Auth Section               Per Section 6.7 if A=1              24+     variable
```

## Appendix C: Single-Hop vs Multi-Hop Comparison

| Aspect | Single-Hop (RFC 5881) | Multi-Hop (RFC 5883) |
|---|---|---|
| Destination UDP port | 3784 | 4784 |
| Source UDP port | 49152-65535 | 49152-65535 |
| Outgoing TTL | MUST be 255 | MUST be 255 |
| Incoming TTL check (no auth) | MUST equal 255 | MUST be >= 254 |
| Incoming TTL check (with auth) | MAY check = 255 | MUST be >= 254 |
| Interface binding | MUST (SO_BINDTODEVICE) | NOT required |
| Session role | Both sides MUST be Active | Either may be Passive |
| Initial demux (YD=0) | (src IP, interface) | (src IP, dst IP) |
| BPF Echo port | 3785 | N/A |
