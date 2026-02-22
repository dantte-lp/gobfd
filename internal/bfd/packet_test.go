package bfd_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// TestMarshalUnmarshalRoundTrip — basic codec round-trip verification
// -------------------------------------------------------------------------

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pkt  bfd.ControlPacket
	}{
		{
			name: "minimal packet no auth",
			pkt: bfd.ControlPacket{
				Version:                   bfd.Version,
				Diag:                      bfd.DiagNone,
				State:                     bfd.StateDown,
				DetectMult:                3,
				MyDiscriminator:           0x00000001,
				YourDiscriminator:         0x00000000,
				DesiredMinTxInterval:      1000000, // 1 second in microseconds
				RequiredMinRxInterval:     1000000,
				RequiredMinEchoRxInterval: 0,
			},
		},
		{
			name: "full flags set state up",
			pkt: bfd.ControlPacket{
				Version:                   bfd.Version,
				Diag:                      bfd.DiagControlTimeExpired,
				State:                     bfd.StateUp,
				Poll:                      true,
				Final:                     true,
				ControlPlaneIndependent:   true,
				AuthPresent:               false,
				Demand:                    true,
				DetectMult:                5,
				MyDiscriminator:           0xDEADBEEF,
				YourDiscriminator:         0xCAFEBABE,
				DesiredMinTxInterval:      50000,  // 50ms in microseconds
				RequiredMinRxInterval:     100000, // 100ms in microseconds
				RequiredMinEchoRxInterval: 200000,
			},
		},
		{
			// RFC 5880 Section 6.8.6 step 7b: YourDiscriminator MUST be nonzero
			// when State is Init (only Down/AdminDown allow zero).
			name: "state init with diag neighbor down",
			pkt: bfd.ControlPacket{
				Version:                   bfd.Version,
				Diag:                      bfd.DiagNeighborDown,
				State:                     bfd.StateInit,
				DetectMult:                1,
				MyDiscriminator:           42,
				YourDiscriminator:         99,
				DesiredMinTxInterval:      300000,
				RequiredMinRxInterval:     300000,
				RequiredMinEchoRxInterval: 0,
			},
		},
		{
			name: "admin down state",
			pkt: bfd.ControlPacket{
				Version:                   bfd.Version,
				Diag:                      bfd.DiagAdminDown,
				State:                     bfd.StateAdminDown,
				DetectMult:                3,
				MyDiscriminator:           0xFFFFFFFF,
				YourDiscriminator:         0,
				DesiredMinTxInterval:      1000000,
				RequiredMinRxInterval:     1000000,
				RequiredMinEchoRxInterval: 0,
			},
		},
		{
			name: "max interval values",
			pkt: bfd.ControlPacket{
				Version:                   bfd.Version,
				Diag:                      bfd.DiagReverseConcatPathDown,
				State:                     bfd.StateUp,
				DetectMult:                255,
				MyDiscriminator:           0xFFFFFFFF,
				YourDiscriminator:         0xFFFFFFFF,
				DesiredMinTxInterval:      0xFFFFFFFF,
				RequiredMinRxInterval:     0xFFFFFFFF,
				RequiredMinEchoRxInterval: 0xFFFFFFFF,
			},
		},
		{
			name: "with simple password auth",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				Diag:                  bfd.DiagNone,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       100,
				YourDiscriminator:     200,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:     bfd.AuthTypeSimplePassword,
					Len:      7,
					KeyID:    1,
					AuthData: []byte("test"),
				},
			},
		},
		{
			name: "with keyed MD5 auth",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				Diag:                  bfd.DiagNone,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       100,
				YourDiscriminator:     200,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeKeyedMD5,
					Len:            24,
					KeyID:          5,
					SequenceNumber: 42,
					Digest:         make([]byte, 16),
				},
			},
		},
		{
			name: "with meticulous keyed SHA1 auth",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				Diag:                  bfd.DiagNone,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       100,
				YourDiscriminator:     200,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeMeticulousKeyedSHA1,
					Len:            28,
					KeyID:          3,
					SequenceNumber: 0xDEAD,
					Digest:         make([]byte, 20),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, bfd.MaxPacketSize)

			n, err := bfd.MarshalControlPacket(&tt.pkt, buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}

			var got bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf[:n], &got); err != nil {
				t.Fatalf("UnmarshalControlPacket: %v", err)
			}

			// Compare all mandatory header fields.
			if got.Version != tt.pkt.Version {
				t.Errorf("Version: got %d, want %d", got.Version, tt.pkt.Version)
			}
			if got.Diag != tt.pkt.Diag {
				t.Errorf("Diag: got %d (%s), want %d (%s)", got.Diag, got.Diag, tt.pkt.Diag, tt.pkt.Diag)
			}
			if got.State != tt.pkt.State {
				t.Errorf("State: got %d (%s), want %d (%s)", got.State, got.State, tt.pkt.State, tt.pkt.State)
			}
			if got.Poll != tt.pkt.Poll {
				t.Errorf("Poll: got %t, want %t", got.Poll, tt.pkt.Poll)
			}
			if got.Final != tt.pkt.Final {
				t.Errorf("Final: got %t, want %t", got.Final, tt.pkt.Final)
			}
			if got.ControlPlaneIndependent != tt.pkt.ControlPlaneIndependent {
				t.Errorf("ControlPlaneIndependent: got %t, want %t",
					got.ControlPlaneIndependent, tt.pkt.ControlPlaneIndependent)
			}
			if got.AuthPresent != tt.pkt.AuthPresent {
				t.Errorf("AuthPresent: got %t, want %t", got.AuthPresent, tt.pkt.AuthPresent)
			}
			if got.Demand != tt.pkt.Demand {
				t.Errorf("Demand: got %t, want %t", got.Demand, tt.pkt.Demand)
			}
			if got.Multipoint != tt.pkt.Multipoint {
				t.Errorf("Multipoint: got %t, want %t", got.Multipoint, tt.pkt.Multipoint)
			}
			if got.DetectMult != tt.pkt.DetectMult {
				t.Errorf("DetectMult: got %d, want %d", got.DetectMult, tt.pkt.DetectMult)
			}
			if got.MyDiscriminator != tt.pkt.MyDiscriminator {
				t.Errorf("MyDiscriminator: got 0x%08X, want 0x%08X",
					got.MyDiscriminator, tt.pkt.MyDiscriminator)
			}
			if got.YourDiscriminator != tt.pkt.YourDiscriminator {
				t.Errorf("YourDiscriminator: got 0x%08X, want 0x%08X",
					got.YourDiscriminator, tt.pkt.YourDiscriminator)
			}
			if got.DesiredMinTxInterval != tt.pkt.DesiredMinTxInterval {
				t.Errorf("DesiredMinTxInterval: got %d us, want %d us",
					got.DesiredMinTxInterval, tt.pkt.DesiredMinTxInterval)
			}
			if got.RequiredMinRxInterval != tt.pkt.RequiredMinRxInterval {
				t.Errorf("RequiredMinRxInterval: got %d us, want %d us",
					got.RequiredMinRxInterval, tt.pkt.RequiredMinRxInterval)
			}
			if got.RequiredMinEchoRxInterval != tt.pkt.RequiredMinEchoRxInterval {
				t.Errorf("RequiredMinEchoRxInterval: got %d us, want %d us",
					got.RequiredMinEchoRxInterval, tt.pkt.RequiredMinEchoRxInterval)
			}

			// Compare auth section if present.
			if tt.pkt.AuthPresent && tt.pkt.Auth != nil {
				if got.Auth == nil {
					t.Fatal("Auth: got nil, want non-nil")
				}
				if got.Auth.Type != tt.pkt.Auth.Type {
					t.Errorf("Auth.Type: got %d, want %d", got.Auth.Type, tt.pkt.Auth.Type)
				}
				if got.Auth.Len != tt.pkt.Auth.Len {
					t.Errorf("Auth.Len: got %d, want %d", got.Auth.Len, tt.pkt.Auth.Len)
				}
				if got.Auth.KeyID != tt.pkt.Auth.KeyID {
					t.Errorf("Auth.KeyID: got %d, want %d", got.Auth.KeyID, tt.pkt.Auth.KeyID)
				}
				if tt.pkt.Auth.Type == bfd.AuthTypeSimplePassword {
					if string(got.Auth.AuthData) != string(tt.pkt.Auth.AuthData) {
						t.Errorf("Auth.AuthData: got %q, want %q",
							got.Auth.AuthData, tt.pkt.Auth.AuthData)
					}
				} else {
					if got.Auth.SequenceNumber != tt.pkt.Auth.SequenceNumber {
						t.Errorf("Auth.SequenceNumber: got %d, want %d",
							got.Auth.SequenceNumber, tt.pkt.Auth.SequenceNumber)
					}
				}
			} else if got.Auth != nil {
				t.Errorf("Auth: got non-nil, want nil")
			}

			// Verify Length field was set correctly by marshal.
			expectedLen := uint8(bfd.HeaderSize)
			if tt.pkt.AuthPresent && tt.pkt.Auth != nil {
				expectedLen += tt.pkt.Auth.Len
			}
			if got.Length != expectedLen {
				t.Errorf("Length: got %d, want %d", got.Length, expectedLen)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestUnmarshalValidation — RFC 5880 Section 6.8.6 validation steps
// -------------------------------------------------------------------------

func TestUnmarshalValidation(t *testing.T) {
	t.Parallel()

	// validPacket builds a minimal valid BFD Control packet in wire format.
	// State=Down, DetectMult=3, MyDiscr=1, YourDiscr=0.
	validPacket := func() []byte {
		buf := make([]byte, bfd.HeaderSize)
		// Byte 0: Version=1(3bits) | Diag=0(5bits) = 0b001_00000 = 0x20
		buf[0] = 0x20
		// Byte 1: State=Down(1)(2bits) | P=0|F=0|C=0|A=0|D=0|M=0 = 0b01_000000 = 0x40
		buf[1] = 0x40
		// Byte 2: DetectMult=3
		buf[2] = 3
		// Byte 3: Length=24
		buf[3] = bfd.HeaderSize
		// Bytes 4-7: MyDiscriminator=1
		binary.BigEndian.PutUint32(buf[4:8], 1)
		// Bytes 8-11: YourDiscriminator=0 (valid for state Down)
		binary.BigEndian.PutUint32(buf[8:12], 0)
		// Bytes 12-15: DesiredMinTxInterval=1000000 (1s)
		binary.BigEndian.PutUint32(buf[12:16], 1000000)
		// Bytes 16-19: RequiredMinRxInterval=1000000 (1s)
		binary.BigEndian.PutUint32(buf[16:20], 1000000)
		// Bytes 20-23: RequiredMinEchoRxInterval=0
		binary.BigEndian.PutUint32(buf[20:24], 0)
		return buf
	}

	// validUpPacket builds a valid packet in state Up with both discriminators set.
	validUpPacket := func() []byte {
		buf := validPacket()
		// State=Up(3): 0b11_000000 = 0xC0
		buf[1] = 0xC0
		// YourDiscriminator must be nonzero for Up state.
		binary.BigEndian.PutUint32(buf[8:12], 42)
		return buf
	}

	tests := []struct {
		name    string
		buf     []byte
		wantErr error
	}{
		// --- RFC 5880 Section 6.8.6 Step 1: Version check ---
		{
			name: "step1: wrong version 0",
			buf: func() []byte {
				b := validPacket()
				// Version=0: clear high 3 bits of byte 0.
				b[0] &= 0x1F // version=0, keep diag
				return b
			}(),
			wantErr: bfd.ErrInvalidVersion,
		},
		{
			name: "step1: wrong version 2",
			buf: func() []byte {
				b := validPacket()
				// Version=2: 0b010_00000 = 0x40, Diag stays 0.
				b[0] = 0x40
				return b
			}(),
			wantErr: bfd.ErrInvalidVersion,
		},
		{
			name: "step1: wrong version 7",
			buf: func() []byte {
				b := validPacket()
				// Version=7: 0b111_00000 = 0xE0.
				b[0] = 0xE0
				return b
			}(),
			wantErr: bfd.ErrInvalidVersion,
		},

		// --- Packet too short to even decode ---
		{
			name:    "too short: 0 bytes",
			buf:     []byte{},
			wantErr: bfd.ErrPacketTooShort,
		},
		{
			name:    "too short: 23 bytes",
			buf:     make([]byte, 23),
			wantErr: bfd.ErrPacketTooShort,
		},

		// --- RFC 5880 Section 6.8.6 Step 2: Length field minimum ---
		{
			name: "step2: length field 23 no auth",
			buf: func() []byte {
				b := validPacket()
				b[3] = 23 // Length < MinPacketSizeNoAuth
				return b
			}(),
			wantErr: bfd.ErrInvalidLength,
		},
		{
			name: "step2: length field 24 with auth bit set",
			buf: func() []byte {
				b := make([]byte, 30)
				copy(b, validPacket())
				// Set A bit: byte 1 bit 2.
				b[1] |= 1 << 2
				b[3] = 24 // Length < MinPacketSizeWithAuth (26)
				return b
			}(),
			wantErr: bfd.ErrInvalidLength,
		},
		{
			name: "step2: length field 25 with auth bit set",
			buf: func() []byte {
				b := make([]byte, 30)
				copy(b, validPacket())
				b[1] |= 1 << 2
				b[3] = 25 // Length < MinPacketSizeWithAuth (26)
				return b
			}(),
			wantErr: bfd.ErrInvalidLength,
		},

		// --- RFC 5880 Section 6.8.6 Step 3: Length exceeds payload ---
		{
			name: "step3: length field exceeds buffer",
			buf: func() []byte {
				b := validPacket()
				b[3] = 48 // Length > len(buf)=24
				return b
			}(),
			wantErr: bfd.ErrLengthExceedsPayload,
		},

		// --- RFC 5880 Section 6.8.6 Step 4: DetectMult zero ---
		{
			name: "step4: zero detect multiplier",
			buf: func() []byte {
				b := validPacket()
				b[2] = 0
				return b
			}(),
			wantErr: bfd.ErrZeroDetectMult,
		},

		// --- RFC 5880 Section 6.8.6 Step 5: Multipoint bit ---
		{
			name: "step5: multipoint bit set",
			buf: func() []byte {
				b := validPacket()
				b[1] |= 0x01 // Set M bit (bit 0 of byte 1).
				return b
			}(),
			wantErr: bfd.ErrMultipointSet,
		},

		// --- RFC 5880 Section 6.8.6 Step 6: MyDiscriminator zero ---
		{
			name: "step6: zero my discriminator",
			buf: func() []byte {
				b := validPacket()
				binary.BigEndian.PutUint32(b[4:8], 0)
				return b
			}(),
			wantErr: bfd.ErrZeroMyDiscriminator,
		},

		// --- RFC 5880 Section 6.8.6 Step 7b: YourDiscriminator zero in non-Down state ---
		{
			name: "step7b: your discriminator zero in state Up",
			buf: func() []byte {
				b := validUpPacket()
				binary.BigEndian.PutUint32(b[8:12], 0) // zero YourDiscr
				return b
			}(),
			wantErr: bfd.ErrZeroYourDiscriminator,
		},
		{
			name: "step7b: your discriminator zero in state Init",
			buf: func() []byte {
				b := validPacket()
				// State=Init(2): 0b10_000000 = 0x80
				b[1] = 0x80
				binary.BigEndian.PutUint32(b[8:12], 0)
				return b
			}(),
			wantErr: bfd.ErrZeroYourDiscriminator,
		},

		// --- Valid: YourDiscriminator=0 in Down/AdminDown states (steps 7b should NOT fail) ---
		{
			name: "step7b ok: your discriminator zero in state Down",
			buf: func() []byte {
				b := validPacket() // State=Down, YourDiscr=0
				return b
			}(),
			wantErr: nil,
		},
		{
			name: "step7b ok: your discriminator zero in state AdminDown",
			buf: func() []byte {
				b := validPacket()
				// State=AdminDown(0): 0b00_000000 = 0x00
				b[1] = 0x00
				return b
			}(),
			wantErr: nil,
		},

		// --- Auth section: A bit set but auth section too short ---
		{
			name: "auth: A bit set with invalid auth type",
			buf: func() []byte {
				// Build a packet with A bit, auth section of unknown type.
				b := make([]byte, 30)
				copy(b, validUpPacket())
				b[1] |= 1 << 2 // Set A bit.
				b[3] = 26      // Length = 26 (minimum with auth).
				b[24] = 255    // Auth Type = 255 (unknown).
				b[25] = 2      // Auth Len = 2 (just type + len, no data).
				return b
			}(),
			wantErr: bfd.ErrInvalidAuthType,
		},

		// --- Auth section: MD5 with wrong auth len ---
		{
			name: "auth: MD5 wrong auth len",
			buf: func() []byte {
				b := make([]byte, 52)
				copy(b, validUpPacket())
				b[1] |= 1 << 2 // A bit
				b[3] = 48      // Length
				b[24] = 2      // Auth Type = Keyed MD5
				b[25] = 20     // Auth Len = 20 (should be 24)
				return b
			}(),
			wantErr: bfd.ErrInvalidLength,
		},

		// --- Auth section: SHA1 with wrong auth len ---
		{
			name: "auth: SHA1 wrong auth len",
			buf: func() []byte {
				b := make([]byte, 56)
				copy(b, validUpPacket())
				b[1] |= 1 << 2 // A bit
				b[3] = 52      // Length
				b[24] = 4      // Auth Type = Keyed SHA1
				b[25] = 24     // Auth Len = 24 (should be 28)
				return b
			}(),
			wantErr: bfd.ErrInvalidLength,
		},

		// --- Auth section: Simple password too short ---
		{
			name: "auth: simple password len too short",
			buf: func() []byte {
				b := make([]byte, 28)
				copy(b, validUpPacket())
				b[1] |= 1 << 2 // A bit
				b[3] = 26      // Length
				b[24] = 1      // Auth Type = Simple Password
				b[25] = 2      // Auth Len = 2 (no key ID, no password)
				return b
			}(),
			wantErr: bfd.ErrAuthSectionTruncated,
		},

		// --- Auth section: truncated auth section data ---
		{
			name: "auth: A bit set but auth section data truncated",
			buf: func() []byte {
				b := make([]byte, 26)
				copy(b, validUpPacket())
				b[1] |= 1 << 2 // A bit
				b[3] = 26      // Length = 26
				b[24] = 4      // Auth Type = Keyed SHA1
				b[25] = 28     // Auth Len = 28 (needs 28 bytes, only 2 available)
				return b
			}(),
			wantErr: bfd.ErrAuthSectionTruncated,
		},

		// --- Valid complete packets ---
		{
			name:    "valid: minimal down packet",
			buf:     validPacket(),
			wantErr: nil,
		},
		{
			name:    "valid: up packet with both discriminators",
			buf:     validUpPacket(),
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var pkt bfd.ControlPacket
			err := bfd.UnmarshalControlPacket(tt.buf, &pkt)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error wrapping %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error wrapping %v, got: %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestMarshalFieldPositions — verify byte offsets match RFC 5880 Section 4.1
// -------------------------------------------------------------------------

func TestMarshalFieldPositions(t *testing.T) {
	t.Parallel()

	pkt := &bfd.ControlPacket{
		Version:                   bfd.Version,
		Diag:                      bfd.DiagPathDown, // 5
		State:                     bfd.StateUp,      // 3
		Poll:                      true,
		Final:                     false,
		ControlPlaneIndependent:   true,
		AuthPresent:               false,
		Demand:                    true,
		Multipoint:                false,
		DetectMult:                7,
		MyDiscriminator:           0x01020304,
		YourDiscriminator:         0x05060708,
		DesiredMinTxInterval:      0x090A0B0C,
		RequiredMinRxInterval:     0x0D0E0F10,
		RequiredMinEchoRxInterval: 0x11121314,
	}

	buf := make([]byte, bfd.MaxPacketSize)
	n, err := bfd.MarshalControlPacket(pkt, buf)
	if err != nil {
		t.Fatalf("MarshalControlPacket: %v", err)
	}

	if n != bfd.HeaderSize {
		t.Fatalf("expected %d bytes written, got %d", bfd.HeaderSize, n)
	}

	// Byte 0: Version(3bits) | Diag(5bits)
	// Version=1 -> 001, Diag=5 -> 00101 -> 0b001_00101 = 0x25
	if buf[0] != 0x25 {
		t.Errorf("byte 0: got 0x%02X, want 0x25 (version=1|diag=5)", buf[0])
	}

	// Byte 1: State(2bits) | P | F | C | A | D | M
	// State=3(Up) -> 11, P=1, F=0, C=1, A=0, D=1, M=0
	// 0b11_1_0_1_0_1_0 = 0xEA
	if buf[1] != 0xEA {
		t.Errorf("byte 1: got 0x%02X, want 0xEA (state=Up|P=1|F=0|C=1|A=0|D=1|M=0)", buf[1])
	}

	// Byte 2: DetectMult = 7
	if buf[2] != 7 {
		t.Errorf("byte 2 (DetectMult): got %d, want 7", buf[2])
	}

	// Byte 3: Length = 24 (no auth)
	if buf[3] != 24 {
		t.Errorf("byte 3 (Length): got %d, want 24", buf[3])
	}

	// Bytes 4-7: MyDiscriminator = 0x01020304
	if got := binary.BigEndian.Uint32(buf[4:8]); got != 0x01020304 {
		t.Errorf("bytes 4-7 (MyDiscriminator): got 0x%08X, want 0x01020304", got)
	}

	// Bytes 8-11: YourDiscriminator = 0x05060708
	if got := binary.BigEndian.Uint32(buf[8:12]); got != 0x05060708 {
		t.Errorf("bytes 8-11 (YourDiscriminator): got 0x%08X, want 0x05060708", got)
	}

	// Bytes 12-15: DesiredMinTxInterval = 0x090A0B0C (microseconds)
	if got := binary.BigEndian.Uint32(buf[12:16]); got != 0x090A0B0C {
		t.Errorf("bytes 12-15 (DesiredMinTxInterval): got 0x%08X, want 0x090A0B0C", got)
	}

	// Bytes 16-19: RequiredMinRxInterval = 0x0D0E0F10 (microseconds)
	if got := binary.BigEndian.Uint32(buf[16:20]); got != 0x0D0E0F10 {
		t.Errorf("bytes 16-19 (RequiredMinRxInterval): got 0x%08X, want 0x0D0E0F10", got)
	}

	// Bytes 20-23: RequiredMinEchoRxInterval = 0x11121314 (microseconds)
	if got := binary.BigEndian.Uint32(buf[20:24]); got != 0x11121314 {
		t.Errorf("bytes 20-23 (RequiredMinEchoRxInterval): got 0x%08X, want 0x11121314", got)
	}
}

// -------------------------------------------------------------------------
// TestControlPacketFlags — verify all flag bit packing/unpacking combinations
// -------------------------------------------------------------------------

func TestControlPacketFlags(t *testing.T) {
	t.Parallel()

	// Test every individual flag and all combinations via bitmask iteration.
	// Byte 1 layout: State(2) | P | F | C | A | D | M
	// We only test flags (bits 5-0), keeping State=Down(1) to allow YourDiscr=0.

	type flagSet struct {
		Poll                    bool
		Final                   bool
		ControlPlaneIndependent bool
		AuthPresent             bool
		Demand                  bool
		Multipoint              bool
	}

	// Iterate all 64 combinations of 6 boolean flags.
	// Skip combinations with Multipoint=true because unmarshal will reject them
	// (RFC 5880 Section 6.8.6 step 5).
	// Skip combinations with AuthPresent=true because they require an auth section.
	for mask := range uint8(64) {
		flags := flagSet{
			Poll:                    mask&(1<<5) != 0,
			Final:                   mask&(1<<4) != 0,
			ControlPlaneIndependent: mask&(1<<3) != 0,
			AuthPresent:             mask&(1<<2) != 0,
			Demand:                  mask&(1<<1) != 0,
			Multipoint:              mask&(1<<0) != 0,
		}

		// Skip invalid combinations for round-trip test.
		if flags.Multipoint || flags.AuthPresent {
			continue
		}

		t.Run(fmt.Sprintf("flags_0x%02X", mask), func(t *testing.T) {
			t.Parallel()

			pkt := bfd.ControlPacket{
				Version:                 bfd.Version,
				State:                   bfd.StateDown,
				DetectMult:              1,
				MyDiscriminator:         1,
				DesiredMinTxInterval:    1000000,
				RequiredMinRxInterval:   1000000,
				Poll:                    flags.Poll,
				Final:                   flags.Final,
				ControlPlaneIndependent: flags.ControlPlaneIndependent,
				Demand:                  flags.Demand,
			}

			buf := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&pkt, buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}

			var got bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf[:n], &got); err != nil {
				t.Fatalf("UnmarshalControlPacket: %v", err)
			}

			if got.Poll != flags.Poll {
				t.Errorf("Poll: got %t, want %t", got.Poll, flags.Poll)
			}
			if got.Final != flags.Final {
				t.Errorf("Final: got %t, want %t", got.Final, flags.Final)
			}
			if got.ControlPlaneIndependent != flags.ControlPlaneIndependent {
				t.Errorf("ControlPlaneIndependent: got %t, want %t",
					got.ControlPlaneIndependent, flags.ControlPlaneIndependent)
			}
			if got.Demand != flags.Demand {
				t.Errorf("Demand: got %t, want %t", got.Demand, flags.Demand)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestAuthSectionMarshal — test auth section encoding for each type
// -------------------------------------------------------------------------

func TestAuthSectionMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pkt      bfd.ControlPacket
		checkBuf func(t *testing.T, buf []byte, n int)
	}{
		{
			name: "simple password",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       1,
				YourDiscriminator:     2,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:     bfd.AuthTypeSimplePassword,
					Len:      7, // 3 + len("test")
					KeyID:    1,
					AuthData: []byte("test"),
				},
			},
			checkBuf: func(t *testing.T, buf []byte, n int) {
				t.Helper()
				// Total: 24 header + 7 auth = 31 bytes.
				if n != 31 {
					t.Fatalf("n: got %d, want 31", n)
				}
				// Auth section starts at offset 24.
				if buf[24] != 1 { // Auth Type = Simple Password
					t.Errorf("auth type: got %d, want 1", buf[24])
				}
				if buf[25] != 7 { // Auth Len
					t.Errorf("auth len: got %d, want 7", buf[25])
				}
				if buf[26] != 1 { // Key ID
					t.Errorf("key id: got %d, want 1", buf[26])
				}
				if string(buf[27:31]) != "test" {
					t.Errorf("password: got %q, want %q", buf[27:31], "test")
				}
			},
		},
		{
			name: "keyed MD5",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       1,
				YourDiscriminator:     2,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeKeyedMD5,
					Len:            24,
					KeyID:          5,
					SequenceNumber: 0x12345678,
					Digest: []byte{
						0x01,
						0x02,
						0x03,
						0x04,
						0x05,
						0x06,
						0x07,
						0x08,
						0x09,
						0x0A,
						0x0B,
						0x0C,
						0x0D,
						0x0E,
						0x0F,
						0x10,
					},
				},
			},
			checkBuf: func(t *testing.T, buf []byte, n int) {
				t.Helper()
				// Total: 24 + 24 = 48 bytes.
				if n != 48 {
					t.Fatalf("n: got %d, want 48", n)
				}
				if buf[24] != 2 { // Auth Type = Keyed MD5
					t.Errorf("auth type: got %d, want 2", buf[24])
				}
				if buf[25] != 24 { // Auth Len
					t.Errorf("auth len: got %d, want 24", buf[25])
				}
				if buf[26] != 5 { // Key ID
					t.Errorf("key id: got %d, want 5", buf[26])
				}
				if buf[27] != 0 { // Reserved
					t.Errorf("reserved: got %d, want 0", buf[27])
				}
				// Sequence Number at offset 28-31.
				seq := binary.BigEndian.Uint32(buf[28:32])
				if seq != 0x12345678 {
					t.Errorf("sequence: got 0x%08X, want 0x12345678", seq)
				}
				// Digest at offset 32-47.
				for i := range 16 {
					if buf[32+i] != byte(i+1) {
						t.Errorf("digest[%d]: got 0x%02X, want 0x%02X", i, buf[32+i], byte(i+1))
					}
				}
			},
		},
		{
			name: "meticulous keyed SHA1",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       1,
				YourDiscriminator:     2,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeMeticulousKeyedSHA1,
					Len:            28,
					KeyID:          3,
					SequenceNumber: 0xDEADBEEF,
					Digest:         make([]byte, 20),
				},
			},
			checkBuf: func(t *testing.T, buf []byte, n int) {
				t.Helper()
				// Total: 24 + 28 = 52 bytes.
				if n != 52 {
					t.Fatalf("n: got %d, want 52", n)
				}
				if buf[24] != 5 { // Auth Type = Meticulous Keyed SHA1
					t.Errorf("auth type: got %d, want 5", buf[24])
				}
				if buf[25] != 28 { // Auth Len
					t.Errorf("auth len: got %d, want 28", buf[25])
				}
				if buf[26] != 3 { // Key ID
					t.Errorf("key id: got %d, want 3", buf[26])
				}
				if buf[27] != 0 { // Reserved
					t.Errorf("reserved: got %d, want 0", buf[27])
				}
				seq := binary.BigEndian.Uint32(buf[28:32])
				if seq != 0xDEADBEEF {
					t.Errorf("sequence: got 0x%08X, want 0xDEADBEEF", seq)
				}
				// Digest at offset 32-51 (20 bytes, all zero).
				for i := range 20 {
					if buf[32+i] != 0 {
						t.Errorf("digest[%d]: got 0x%02X, want 0x00", i, buf[32+i])
					}
				}
			},
		},
		{
			name: "keyed SHA1",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       1,
				YourDiscriminator:     2,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeKeyedSHA1,
					Len:            28,
					KeyID:          7,
					SequenceNumber: 1,
					Digest:         make([]byte, 20),
				},
			},
			checkBuf: func(t *testing.T, buf []byte, n int) {
				t.Helper()
				if n != 52 {
					t.Fatalf("n: got %d, want 52", n)
				}
				if buf[24] != 4 { // Auth Type = Keyed SHA1
					t.Errorf("auth type: got %d, want 4", buf[24])
				}
				if buf[25] != 28 {
					t.Errorf("auth len: got %d, want 28", buf[25])
				}
				if buf[26] != 7 {
					t.Errorf("key id: got %d, want 7", buf[26])
				}
			},
		},
		{
			name: "meticulous keyed MD5",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateUp,
				AuthPresent:           true,
				DetectMult:            3,
				MyDiscriminator:       1,
				YourDiscriminator:     2,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeMeticulousKeyedMD5,
					Len:            24,
					KeyID:          9,
					SequenceNumber: 100,
					Digest:         make([]byte, 16),
				},
			},
			checkBuf: func(t *testing.T, buf []byte, n int) {
				t.Helper()
				if n != 48 {
					t.Fatalf("n: got %d, want 48", n)
				}
				if buf[24] != 3 { // Auth Type = Meticulous Keyed MD5
					t.Errorf("auth type: got %d, want 3", buf[24])
				}
				if buf[25] != 24 {
					t.Errorf("auth len: got %d, want 24", buf[25])
				}
				if buf[26] != 9 {
					t.Errorf("key id: got %d, want 9", buf[26])
				}
				seq := binary.BigEndian.Uint32(buf[28:32])
				if seq != 100 {
					t.Errorf("sequence: got %d, want 100", seq)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&tt.pkt, buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}

			tt.checkBuf(t, buf, n)

			// Also verify round-trip.
			var got bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf[:n], &got); err != nil {
				t.Fatalf("UnmarshalControlPacket round-trip: %v", err)
			}
			if got.Auth == nil {
				t.Fatal("round-trip: Auth is nil after unmarshal")
			}
			if got.Auth.Type != tt.pkt.Auth.Type {
				t.Errorf("round-trip Auth.Type: got %d, want %d", got.Auth.Type, tt.pkt.Auth.Type)
			}
			if got.Auth.Len != tt.pkt.Auth.Len {
				t.Errorf("round-trip Auth.Len: got %d, want %d", got.Auth.Len, tt.pkt.Auth.Len)
			}
			if got.Auth.KeyID != tt.pkt.Auth.KeyID {
				t.Errorf("round-trip Auth.KeyID: got %d, want %d", got.Auth.KeyID, tt.pkt.Auth.KeyID)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestMarshalBufferTooSmall — verify error when buffer is too small
// -------------------------------------------------------------------------

func TestMarshalBufferTooSmall(t *testing.T) {
	t.Parallel()

	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateDown,
		DetectMult:            3,
		MyDiscriminator:       1,
		DesiredMinTxInterval:  1000000,
		RequiredMinRxInterval: 1000000,
	}

	buf := make([]byte, 20) // too small for 24 byte header
	_, err := bfd.MarshalControlPacket(pkt, buf)
	if err == nil {
		t.Fatal("expected error for buffer too small, got nil")
	}
	if !errors.Is(err, bfd.ErrBufTooSmall) {
		t.Fatalf("expected ErrBufTooSmall, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// TestMarshalAuthBufferTooSmall — verify error when buffer cannot fit auth
// -------------------------------------------------------------------------

func TestMarshalAuthBufferTooSmall(t *testing.T) {
	t.Parallel()

	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateUp,
		AuthPresent:           true,
		DetectMult:            3,
		MyDiscriminator:       1,
		YourDiscriminator:     2,
		DesiredMinTxInterval:  1000000,
		RequiredMinRxInterval: 1000000,
		Auth: &bfd.AuthSection{
			Type:           bfd.AuthTypeKeyedSHA1,
			Len:            28,
			KeyID:          1,
			SequenceNumber: 1,
			Digest:         make([]byte, 20),
		},
	}

	// Need 24 + 28 = 52 bytes, provide only 40.
	buf := make([]byte, 40)
	_, err := bfd.MarshalControlPacket(pkt, buf)
	if err == nil {
		t.Fatal("expected error for buffer too small with auth, got nil")
	}
	if !errors.Is(err, bfd.ErrBufTooSmall) {
		t.Fatalf("expected ErrBufTooSmall, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// FuzzControlPacket — fuzz test: unmarshal arbitrary bytes, round-trip valid
// -------------------------------------------------------------------------

// FuzzControlPacket tests that UnmarshalControlPacket never panics on
// arbitrary input, and that valid packets survive a marshal-unmarshal
// round-trip without data loss.
//
// Seeded with known-good packets per RFC 5880 Section 4.1.
func FuzzControlPacket(f *testing.F) {
	// Seed corpus: valid minimal packet (State=Down, DetectMult=3, MyDiscr=1).
	seed1 := make([]byte, bfd.HeaderSize)
	seed1[0] = 0x20                                   // Version=1, Diag=0
	seed1[1] = 0x40                                   // State=Down
	seed1[2] = 3                                      // DetectMult
	seed1[3] = 24                                     // Length
	binary.BigEndian.PutUint32(seed1[4:8], 1)         // MyDiscriminator
	binary.BigEndian.PutUint32(seed1[12:16], 1000000) // DesiredMinTxInterval
	binary.BigEndian.PutUint32(seed1[16:20], 1000000) // RequiredMinRxInterval
	f.Add(seed1)

	// Seed: valid Up packet with both discriminators.
	seed2 := make([]byte, bfd.HeaderSize)
	seed2[0] = 0x20 // Version=1, Diag=0
	seed2[1] = 0xC0 // State=Up
	seed2[2] = 5    // DetectMult
	seed2[3] = 24   // Length
	binary.BigEndian.PutUint32(seed2[4:8], 0xDEADBEEF)
	binary.BigEndian.PutUint32(seed2[8:12], 0xCAFEBABE)
	binary.BigEndian.PutUint32(seed2[12:16], 100000)
	binary.BigEndian.PutUint32(seed2[16:20], 100000)
	f.Add(seed2)

	// Seed: packet with Simple Password auth.
	seed3 := make([]byte, 31)
	seed3[0] = 0x20        // Version=1
	seed3[1] = 0xC0 | 0x04 // State=Up, A=1
	seed3[2] = 3           // DetectMult
	seed3[3] = 31          // Length
	binary.BigEndian.PutUint32(seed3[4:8], 1)
	binary.BigEndian.PutUint32(seed3[8:12], 2)
	binary.BigEndian.PutUint32(seed3[12:16], 1000000)
	binary.BigEndian.PutUint32(seed3[16:20], 1000000)
	seed3[24] = 1 // Auth Type = Simple Password
	seed3[25] = 7 // Auth Len = 3 + 4 bytes password
	seed3[26] = 1 // Key ID
	copy(seed3[27:], []byte("test"))
	f.Add(seed3)

	// Seed: packet with Keyed SHA1 auth.
	seed4 := make([]byte, 52)
	seed4[0] = 0x20        // Version=1
	seed4[1] = 0xC0 | 0x04 // State=Up, A=1
	seed4[2] = 3           // DetectMult
	seed4[3] = 52          // Length
	binary.BigEndian.PutUint32(seed4[4:8], 1)
	binary.BigEndian.PutUint32(seed4[8:12], 2)
	binary.BigEndian.PutUint32(seed4[12:16], 1000000)
	binary.BigEndian.PutUint32(seed4[16:20], 1000000)
	seed4[24] = 4                                // Auth Type = Keyed SHA1
	seed4[25] = 28                               // Auth Len
	seed4[26] = 1                                // Key ID
	seed4[27] = 0                                // Reserved
	binary.BigEndian.PutUint32(seed4[28:32], 42) // Sequence Number
	f.Add(seed4)

	// Seed: packet with Keyed MD5 auth.
	seed5 := make([]byte, 48)
	seed5[0] = 0x20        // Version=1
	seed5[1] = 0xC0 | 0x04 // State=Up, A=1
	seed5[2] = 3           // DetectMult
	seed5[3] = 48          // Length
	binary.BigEndian.PutUint32(seed5[4:8], 1)
	binary.BigEndian.PutUint32(seed5[8:12], 2)
	binary.BigEndian.PutUint32(seed5[12:16], 1000000)
	binary.BigEndian.PutUint32(seed5[16:20], 1000000)
	seed5[24] = 2                                 // Auth Type = Keyed MD5
	seed5[25] = 24                                // Auth Len
	seed5[26] = 1                                 // Key ID
	seed5[27] = 0                                 // Reserved
	binary.BigEndian.PutUint32(seed5[28:32], 100) // Sequence Number
	f.Add(seed5)

	// Seed: packet with Meticulous Keyed SHA1 auth.
	seed6 := make([]byte, 52)
	seed6[0] = 0x20        // Version=1
	seed6[1] = 0xC0 | 0x04 // State=Up, A=1
	seed6[2] = 3           // DetectMult
	seed6[3] = 52          // Length
	binary.BigEndian.PutUint32(seed6[4:8], 0x12345678)
	binary.BigEndian.PutUint32(seed6[8:12], 0x9ABCDEF0)
	binary.BigEndian.PutUint32(seed6[12:16], 300000)
	binary.BigEndian.PutUint32(seed6[16:20], 300000)
	seed6[24] = 5                                  // Auth Type = Meticulous Keyed SHA1
	seed6[25] = 28                                 // Auth Len
	seed6[26] = 2                                  // Key ID
	seed6[27] = 0                                  // Reserved
	binary.BigEndian.PutUint32(seed6[28:32], 9999) // Sequence Number
	f.Add(seed6)

	// Seed: packet with Meticulous Keyed MD5 auth.
	seed7 := make([]byte, 48)
	seed7[0] = 0x20        // Version=1
	seed7[1] = 0x40 | 0x04 // State=Down, A=1
	seed7[2] = 1           // DetectMult
	seed7[3] = 48          // Length
	binary.BigEndian.PutUint32(seed7[4:8], 0xAABBCCDD)
	binary.BigEndian.PutUint32(seed7[12:16], 1000000)
	binary.BigEndian.PutUint32(seed7[16:20], 1000000)
	seed7[24] = 3                                      // Auth Type = Meticulous Keyed MD5
	seed7[25] = 24                                     // Auth Len
	seed7[26] = 3                                      // Key ID
	seed7[27] = 0                                      // Reserved
	binary.BigEndian.PutUint32(seed7[28:32], 0xFFFFFF) // Sequence Number near wrap
	f.Add(seed7)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Step 1: UnmarshalControlPacket must not panic on arbitrary input.
		var pkt bfd.ControlPacket
		err := bfd.UnmarshalControlPacket(data, &pkt)
		if err != nil {
			// Invalid packet — that is fine, just must not panic.
			return
		}

		// Step 2: For valid packets, marshal and re-unmarshal must produce
		// identical results (round-trip property).
		buf := make([]byte, bfd.MaxPacketSize)
		n, err := bfd.MarshalControlPacket(&pkt, buf)
		if err != nil {
			// Some valid-to-unmarshal packets may not be valid to marshal
			// (e.g., if the auth section digest was not fully copied).
			// This is acceptable for the fuzz test.
			return
		}

		var pkt2 bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(buf[:n], &pkt2); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\noriginal data: %x\nmarshaled: %x",
				err, data, buf[:n])
		}

		// Compare all mandatory header fields.
		if pkt2.Version != pkt.Version {
			t.Errorf("round-trip Version mismatch: %d vs %d", pkt2.Version, pkt.Version)
		}
		if pkt2.Diag != pkt.Diag {
			t.Errorf("round-trip Diag mismatch: %d vs %d", pkt2.Diag, pkt.Diag)
		}
		if pkt2.State != pkt.State {
			t.Errorf("round-trip State mismatch: %d vs %d", pkt2.State, pkt.State)
		}
		if pkt2.Poll != pkt.Poll {
			t.Errorf("round-trip Poll mismatch: %t vs %t", pkt2.Poll, pkt.Poll)
		}
		if pkt2.Final != pkt.Final {
			t.Errorf("round-trip Final mismatch: %t vs %t", pkt2.Final, pkt.Final)
		}
		if pkt2.ControlPlaneIndependent != pkt.ControlPlaneIndependent {
			t.Errorf("round-trip CPI mismatch: %t vs %t",
				pkt2.ControlPlaneIndependent, pkt.ControlPlaneIndependent)
		}
		if pkt2.AuthPresent != pkt.AuthPresent {
			t.Errorf("round-trip AuthPresent mismatch: %t vs %t",
				pkt2.AuthPresent, pkt.AuthPresent)
		}
		if pkt2.Demand != pkt.Demand {
			t.Errorf("round-trip Demand mismatch: %t vs %t", pkt2.Demand, pkt.Demand)
		}
		if pkt2.DetectMult != pkt.DetectMult {
			t.Errorf("round-trip DetectMult mismatch: %d vs %d",
				pkt2.DetectMult, pkt.DetectMult)
		}
		if pkt2.MyDiscriminator != pkt.MyDiscriminator {
			t.Errorf("round-trip MyDiscriminator mismatch: 0x%08X vs 0x%08X",
				pkt2.MyDiscriminator, pkt.MyDiscriminator)
		}
		if pkt2.YourDiscriminator != pkt.YourDiscriminator {
			t.Errorf("round-trip YourDiscriminator mismatch: 0x%08X vs 0x%08X",
				pkt2.YourDiscriminator, pkt.YourDiscriminator)
		}
		if pkt2.DesiredMinTxInterval != pkt.DesiredMinTxInterval {
			t.Errorf("round-trip DesiredMinTxInterval mismatch: %d vs %d",
				pkt2.DesiredMinTxInterval, pkt.DesiredMinTxInterval)
		}
		if pkt2.RequiredMinRxInterval != pkt.RequiredMinRxInterval {
			t.Errorf("round-trip RequiredMinRxInterval mismatch: %d vs %d",
				pkt2.RequiredMinRxInterval, pkt.RequiredMinRxInterval)
		}
		if pkt2.RequiredMinEchoRxInterval != pkt.RequiredMinEchoRxInterval {
			t.Errorf("round-trip RequiredMinEchoRxInterval mismatch: %d vs %d",
				pkt2.RequiredMinEchoRxInterval, pkt.RequiredMinEchoRxInterval)
		}

		// Compare auth section fields if present.
		if pkt.AuthPresent && pkt2.AuthPresent && pkt.Auth != nil && pkt2.Auth != nil {
			if pkt2.Auth.Type != pkt.Auth.Type {
				t.Errorf("round-trip Auth.Type mismatch: %d vs %d",
					pkt2.Auth.Type, pkt.Auth.Type)
			}
			if pkt2.Auth.Len != pkt.Auth.Len {
				t.Errorf("round-trip Auth.Len mismatch: %d vs %d",
					pkt2.Auth.Len, pkt.Auth.Len)
			}
			if pkt2.Auth.KeyID != pkt.Auth.KeyID {
				t.Errorf("round-trip Auth.KeyID mismatch: %d vs %d",
					pkt2.Auth.KeyID, pkt.Auth.KeyID)
			}
			if pkt2.Auth.SequenceNumber != pkt.Auth.SequenceNumber {
				t.Errorf("round-trip Auth.SequenceNumber mismatch: %d vs %d",
					pkt2.Auth.SequenceNumber, pkt.Auth.SequenceNumber)
			}
		}
	})
}

// -------------------------------------------------------------------------
// TestStateString — verify State.String() output
// -------------------------------------------------------------------------

func TestStateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state bfd.State
		want  string
	}{
		{bfd.StateAdminDown, "AdminDown"},
		{bfd.StateDown, "Down"},
		{bfd.StateInit, "Init"},
		{bfd.StateUp, "Up"},
		{bfd.State(4), "Unknown(4)"},
		{bfd.State(255), "Unknown(255)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.state.String(); got != tt.want {
				t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestDiagString — verify Diag.String() output
// -------------------------------------------------------------------------

func TestDiagString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		diag bfd.Diag
		want string
	}{
		{bfd.DiagNone, "None"},
		{bfd.DiagControlTimeExpired, "Control Detection Time Expired"},
		{bfd.DiagEchoFailed, "Echo Function Failed"},
		{bfd.DiagNeighborDown, "Neighbor Signaled Session Down"},
		{bfd.DiagForwardingPlaneReset, "Forwarding Plane Reset"},
		{bfd.DiagPathDown, "Path Down"},
		{bfd.DiagConcatPathDown, "Concatenated Path Down"},
		{bfd.DiagAdminDown, "Administratively Down"},
		{bfd.DiagReverseConcatPathDown, "Reverse Concatenated Path Down"},
		{bfd.Diag(9), "Unknown(9)"},
		{bfd.Diag(31), "Unknown(31)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.diag.String(); got != tt.want {
				t.Errorf("Diag(%d).String() = %q, want %q", tt.diag, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestAuthTypeString — verify AuthType.String() output
// -------------------------------------------------------------------------

func TestAuthTypeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		authType bfd.AuthType
		want     string
	}{
		{bfd.AuthTypeNone, "None"},
		{bfd.AuthTypeSimplePassword, "Simple Password"},
		{bfd.AuthTypeKeyedMD5, "Keyed MD5"},
		{bfd.AuthTypeMeticulousKeyedMD5, "Meticulous Keyed MD5"},
		{bfd.AuthTypeKeyedSHA1, "Keyed SHA1"},
		{bfd.AuthTypeMeticulousKeyedSHA1, "Meticulous Keyed SHA1"},
		{bfd.AuthType(6), "Unknown(6)"},
		{bfd.AuthType(255), "Unknown(255)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.authType.String(); got != tt.want {
				t.Errorf("AuthType(%d).String() = %q, want %q", tt.authType, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestPacketPool — verify sync.Pool returns correctly sized buffers
// -------------------------------------------------------------------------

func TestPacketPool(t *testing.T) {
	t.Parallel()

	bufp := bfd.PacketPool.Get().(*[]byte)
	defer bfd.PacketPool.Put(bufp)

	if len(*bufp) != bfd.MaxPacketSize {
		t.Errorf("PacketPool buffer size: got %d, want %d", len(*bufp), bfd.MaxPacketSize)
	}
}

// -------------------------------------------------------------------------
// TestAllStatesRoundTrip — verify all 4 State values survive round-trip
// -------------------------------------------------------------------------

func TestAllStatesRoundTrip(t *testing.T) {
	t.Parallel()

	states := []bfd.State{
		bfd.StateAdminDown,
		bfd.StateDown,
		bfd.StateInit,
		bfd.StateUp,
	}

	for _, state := range states {
		t.Run(state.String(), func(t *testing.T) {
			t.Parallel()

			pkt := bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 state,
				DetectMult:            3,
				MyDiscriminator:       1,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
			}

			// States other than Down/AdminDown require nonzero YourDiscriminator.
			if state == bfd.StateInit || state == bfd.StateUp {
				pkt.YourDiscriminator = 42
			}

			buf := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&pkt, buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}

			var got bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf[:n], &got); err != nil {
				t.Fatalf("UnmarshalControlPacket: %v", err)
			}

			if got.State != state {
				t.Errorf("State: got %s, want %s", got.State, state)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestAllDiagsRoundTrip — verify all 9 Diag values survive round-trip
// -------------------------------------------------------------------------

func TestAllDiagsRoundTrip(t *testing.T) {
	t.Parallel()

	diags := []bfd.Diag{
		bfd.DiagNone,
		bfd.DiagControlTimeExpired,
		bfd.DiagEchoFailed,
		bfd.DiagNeighborDown,
		bfd.DiagForwardingPlaneReset,
		bfd.DiagPathDown,
		bfd.DiagConcatPathDown,
		bfd.DiagAdminDown,
		bfd.DiagReverseConcatPathDown,
	}

	for _, diag := range diags {
		t.Run(diag.String(), func(t *testing.T) {
			t.Parallel()

			pkt := bfd.ControlPacket{
				Version:               bfd.Version,
				Diag:                  diag,
				State:                 bfd.StateDown,
				DetectMult:            3,
				MyDiscriminator:       1,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
			}

			buf := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&pkt, buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}

			var got bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf[:n], &got); err != nil {
				t.Fatalf("UnmarshalControlPacket: %v", err)
			}

			if got.Diag != diag {
				t.Errorf("Diag: got %s, want %s", got.Diag, diag)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestLengthFieldAutoSet — verify marshal auto-sets the Length field
// -------------------------------------------------------------------------

func TestLengthFieldAutoSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pkt     bfd.ControlPacket
		wantLen uint8
	}{
		{
			name: "no auth",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateDown,
				DetectMult:            1,
				MyDiscriminator:       1,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
			},
			wantLen: 24,
		},
		{
			name: "simple password 4 bytes",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateDown,
				AuthPresent:           true,
				DetectMult:            1,
				MyDiscriminator:       1,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:     bfd.AuthTypeSimplePassword,
					Len:      7, // 3 + 4
					KeyID:    1,
					AuthData: []byte("abcd"),
				},
			},
			wantLen: 31, // 24 + 7
		},
		{
			name: "keyed MD5",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateDown,
				AuthPresent:           true,
				DetectMult:            1,
				MyDiscriminator:       1,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeKeyedMD5,
					Len:            24,
					KeyID:          1,
					SequenceNumber: 1,
					Digest:         make([]byte, 16),
				},
			},
			wantLen: 48, // 24 + 24
		},
		{
			name: "keyed SHA1",
			pkt: bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateDown,
				AuthPresent:           true,
				DetectMult:            1,
				MyDiscriminator:       1,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
				Auth: &bfd.AuthSection{
					Type:           bfd.AuthTypeKeyedSHA1,
					Len:            28,
					KeyID:          1,
					SequenceNumber: 1,
					Digest:         make([]byte, 20),
				},
			},
			wantLen: 52, // 24 + 28
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&tt.pkt, buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}

			// Check the Length byte in the wire format (byte 3).
			if buf[3] != tt.wantLen {
				t.Errorf("Length field: got %d, want %d", buf[3], tt.wantLen)
			}

			// Also check that n matches.
			if n != int(tt.wantLen) {
				t.Errorf("bytes written: got %d, want %d", n, tt.wantLen)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestUnmarshalExtraData — verify packet with extra trailing data is valid
// -------------------------------------------------------------------------

func TestUnmarshalExtraData(t *testing.T) {
	t.Parallel()

	// RFC 5880 Section 6.8.6: Length field defines the valid portion.
	// Extra bytes beyond Length are ignored (common with UDP padding).
	buf := make([]byte, 48) // 24 extra bytes beyond a 24-byte packet
	buf[0] = 0x20           // Version=1, Diag=0
	buf[1] = 0x40           // State=Down
	buf[2] = 3              // DetectMult
	buf[3] = 24             // Length = 24 (only header, ignore extra)
	binary.BigEndian.PutUint32(buf[4:8], 1)
	binary.BigEndian.PutUint32(buf[12:16], 1000000)
	binary.BigEndian.PutUint32(buf[16:20], 1000000)

	// Fill trailing bytes with garbage to ensure they are ignored.
	for i := 24; i < 48; i++ {
		buf[i] = 0xFF
	}

	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(buf, &pkt); err != nil {
		t.Fatalf("UnmarshalControlPacket with extra data: %v", err)
	}

	if pkt.Length != 24 {
		t.Errorf("Length: got %d, want 24", pkt.Length)
	}
}
