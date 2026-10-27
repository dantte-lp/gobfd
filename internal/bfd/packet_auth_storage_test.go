package bfd_test

import (
	"bytes"
	"errors"
	"strconv"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// Literal RFC 5880 headers and auth fields keep the decoder out of fixture setup.
func storageTestWire(authType byte) []byte {
	auth := []byte{authType, 5, 7, 'p', 'w'}
	if authType != 1 {
		length := byte(24)
		if authType >= 4 {
			length = 28
		}
		auth = append([]byte{authType, length, 7, 0, 0, 0, 0, 41}, bytes.Repeat([]byte{0x5a}, int(length)-8)...)
	}
	return append([]byte{
		0x20, 0xc4, 3, byte(24 + len(auth)),
		0, 0, 0, 1, 0, 0, 0, 2,
		0, 1, 0x86, 0xa0, 0, 1, 0x86, 0xa0, 0, 0, 0, 0,
	}, auth...)
}

func TestAuthenticatedDecodeAllocations(t *testing.T) {
	for authType := byte(1); authType <= 5; authType++ {
		t.Run(strconv.Itoa(int(authType)), func(t *testing.T) {
			wire := storageTestWire(authType)
			allocs := testing.AllocsPerRun(200000, func() {
				var pkt bfd.ControlPacket
				if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
					panic(err)
				}
				if !pkt.AuthPresent || pkt.Auth.Type != bfd.AuthType(authType) || pkt.Auth.KeyID != 7 {
					panic("decoded authentication was not retained")
				}
			})
			if allocs != 0 {
				t.Fatalf("authenticated decode allocations = %g, want 0", allocs)
			}
		})
	}
}

func TestAuthenticationVerifyAllocations(t *testing.T) {
	for _, vector := range paddedAuthVectors() {
		t.Run(vector.name, func(t *testing.T) {
			wire := authVectorWire(t, vector.wire, 128)
			before := bytes.Clone(wire)
			var pkt bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
				t.Fatal(err)
			}
			auth, err := bfd.NewAuthenticator(vector.typ)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{
				ID: 1, Type: vector.typ, Secret: []byte("vector-key"),
			})
			if err != nil {
				t.Fatal(err)
			}
			state := bfd.AuthState{AuthSeqKnown: true}
			allocs := testing.AllocsPerRun(200000, func() {
				state.RcvAuthSeq = pkt.Auth.SequenceNumber - 1
				if verifyErr := auth.Verify(&state, keys, pkt, wire, len(wire)); verifyErr != nil {
					panic(verifyErr)
				}
			})
			if allocs != 0 {
				t.Fatalf("successful authentication verify allocations = %g, want 0", allocs)
			}
			if !bytes.Equal(wire, before) {
				t.Fatal("Verify changed immutable decoded wire")
			}
		})
	}
}

func TestAuthenticationSignAllocations(t *testing.T) {
	for typ := bfd.AuthTypeSimplePassword; typ <= bfd.AuthTypeMeticulousKeyedSHA1; typ++ {
		t.Run(typ.String(), func(t *testing.T) {
			auth, err := bfd.NewAuthenticator(typ)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{
				ID: 1, Type: typ, Secret: []byte("benchmark-secret"),
			})
			if err != nil {
				t.Fatal(err)
			}
			pkt := newTestPacket()
			var state bfd.AuthState
			buf := make([]byte, bfd.MaxPacketSize)
			allocs := testing.AllocsPerRun(200000, func() {
				if signErr := auth.Sign(&state, keys, pkt, buf, len(buf)); signErr != nil {
					panic(signErr)
				}
			})
			if allocs != 0 {
				t.Fatalf("successful authentication sign allocations = %g, want 0", allocs)
			}
		})
	}
}

func TestAuthenticatedDecodeOwnsPayload(t *testing.T) {
	t.Parallel()
	for typ := byte(1); typ <= 5; typ++ {
		t.Run(strconv.Itoa(int(typ)), func(t *testing.T) {
			t.Parallel()
			wire := storageTestWire(typ)
			before := bytes.Clone(wire)
			var pkt bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
				t.Fatal(err)
			}
			retained := pkt
			clear(wire)
			buf := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&retained, buf)
			if err != nil || !bytes.Equal(buf[:n], before) {
				t.Fatalf("input reuse changed decoded copy: n=%d error=%v", n, err)
			}
		})
	}
}

func TestAuthenticationSignCopyOwnsPayload(t *testing.T) {
	t.Parallel()
	for typ := bfd.AuthTypeSimplePassword; typ <= bfd.AuthTypeMeticulousKeyedSHA1; typ++ {
		t.Run(typ.String(), func(t *testing.T) {
			t.Parallel()
			auth, err := bfd.NewAuthenticator(typ)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{
				ID: 1, Type: typ, Secret: []byte("benchmark-secret"),
			})
			if err != nil {
				t.Fatal(err)
			}
			pkt := newTestPacket()
			var state bfd.AuthState
			buf := make([]byte, bfd.MaxPacketSize)
			if signErr := auth.Sign(&state, keys, pkt, buf, len(buf)); signErr != nil {
				t.Fatal(signErr)
			}
			n, err := bfd.MarshalControlPacket(pkt, buf)
			if err != nil {
				t.Fatal(err)
			}
			before, retained := bytes.Clone(buf[:n]), *pkt
			pkt.Auth.Data[0] ^= 0xff
			if signErr := auth.Sign(&state, keys, pkt, buf, len(buf)); signErr != nil {
				t.Fatal(signErr)
			}
			clear(buf)
			n, err = bfd.MarshalControlPacket(&retained, buf)
			if err != nil || !bytes.Equal(buf[:n], before) {
				t.Fatalf("mutable copy or re-sign changed retained packet: n=%d error=%v", n, err)
			}
			if verifyErr := auth.Verify(&bfd.AuthState{}, keys, retained, before, n); verifyErr != nil {
				t.Fatal(verifyErr)
			}
			if !bytes.Equal(keys.CurrentKey().Secret, []byte("benchmark-secret")) {
				t.Fatal("packet mutation changed private key storage")
			}
		})
	}
}

func TestAuthLogicalPayloadLength(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors() {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			wire := authVectorWire(t, vector.wire, 128)
			before := bytes.Clone(wire)
			var original bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(wire, &original); err != nil {
				t.Fatal(err)
			}
			auth, err := bfd.NewAuthenticator(vector.typ)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{ID: 1, Type: vector.typ, Secret: []byte("vector-key")})
			if err != nil {
				t.Fatal(err)
			}
			for _, length := range []uint8{0, 15, 16, 17, 19, 20, 21, 255, original.Auth.DataLen} {
				t.Run(strconv.Itoa(int(length)), func(t *testing.T) {
					pkt := original
					pkt.Auth.DataLen = length
					invalid := length != original.Auth.DataLen
					buf := bytes.Repeat([]byte{0xaa}, bfd.MaxPacketSize)
					n, marshalErr := bfd.MarshalControlPacket(&pkt, buf)
					if invalid {
						if !errors.Is(marshalErr, bfd.ErrAuthLenMismatch) || n != 0 ||
							!bytes.Equal(buf, bytes.Repeat([]byte{0xaa}, len(buf))) {
							t.Fatalf("invalid payload length wrote wire: n=%d error=%v", n, marshalErr)
						}
					} else if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					var state bfd.AuthState
					var wantErr error
					if invalid {
						wantErr = bfd.ErrAuthDigestMismatch
						if vector.typ == bfd.AuthTypeSimplePassword {
							wantErr = bfd.ErrAuthPasswordMismatch
						}
					}
					if verifyErr := auth.Verify(&state, keys, pkt, wire, len(wire)); !errors.Is(verifyErr, wantErr) {
						t.Fatalf("Verify error=%v, want %v", verifyErr, wantErr)
					}
					if invalid && state != (bfd.AuthState{}) || !bytes.Equal(wire, before) {
						t.Fatal("failed verification changed state or immutable wire")
					}
				})
			}
		})
	}
}

func TestAuthenticationSignClearsUnusedTail(t *testing.T) {
	t.Parallel()
	pkt := newTestPacket()
	var state bfd.AuthState
	buf := make([]byte, bfd.MaxPacketSize)
	for _, key := range []bfd.AuthKey{
		{ID: 1, Type: bfd.AuthTypeKeyedSHA1, Secret: []byte("01234567890123456789")},
		{ID: 1, Type: bfd.AuthTypeSimplePassword, Secret: []byte("0123456789012345")},
		{ID: 1, Type: bfd.AuthTypeSimplePassword, Secret: []byte("k")},
		{ID: 1, Type: bfd.AuthTypeKeyedMD5, Secret: []byte("k")},
	} {
		auth, err := bfd.NewAuthenticator(key.Type)
		if err != nil {
			t.Fatal(err)
		}
		keys, err := bfd.NewStaticAuthKeyStore(key)
		if err != nil {
			t.Fatal(err)
		}
		if signErr := auth.Sign(&state, keys, pkt, buf, len(buf)); signErr != nil {
			t.Fatal(signErr)
		}
		for _, unused := range pkt.Auth.Data[pkt.Auth.DataLen:] {
			if unused != 0 {
				t.Fatal("re-sign retained previous payload in unused storage")
			}
		}
		n, err := bfd.MarshalControlPacket(pkt, buf)
		if err != nil {
			t.Fatal(err)
		}
		if verifyErr := auth.Verify(&bfd.AuthState{}, keys, *pkt, buf, n); verifyErr != nil {
			t.Fatal(verifyErr)
		}
	}
}

func TestAuthenticatedDecodeCopySurvivesRedecode(t *testing.T) {
	t.Parallel()
	for authType := byte(1); authType <= 5; authType++ {
		t.Run(strconv.Itoa(int(authType)), func(t *testing.T) {
			t.Parallel()
			wire := storageTestWire(authType)
			var pkt bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
				t.Fatal(err)
			}
			saved := pkt
			next := bytes.Clone(wire)
			next[26] = 9
			next[len(next)-1] = 0x77
			if err := bfd.UnmarshalControlPacket(next, &pkt); err != nil {
				t.Fatal(err)
			}
			plain := bytes.Clone(wire[:24])
			plain[1], plain[3] = 0xc0, 24
			if err := bfd.UnmarshalControlPacket(plain, &pkt); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				packet *bfd.ControlPacket
				wire   []byte
			}{
				{&saved, wire}, {&pkt, plain},
			} {
				var buf [64]byte
				n, err := bfd.MarshalControlPacket(tc.packet, buf[:])
				if err != nil || !bytes.Equal(buf[:n], tc.wire) {
					t.Fatalf("copied/redecoded packet changed: n=%d error=%v wire=%x, want %x", n, err, buf[:n], tc.wire)
				}
			}
		})
	}
}

func TestAuthenticatedDecodeFailureDoesNotPublishSection(t *testing.T) {
	t.Parallel()
	var pkt bfd.ControlPacket
	wire := storageTestWire(4)
	if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
		t.Fatal(err)
	}
	bad := bytes.Clone(wire)
	bad[24] = 0
	if err := bfd.UnmarshalControlPacket(bad, &pkt); !errors.Is(err, bfd.ErrInvalidAuthType) {
		t.Fatalf("reserved auth type error = %v", err)
	}
	var buf [64]byte
	if _, err := bfd.MarshalControlPacket(&pkt, buf[:]); !errors.Is(err, bfd.ErrAuthMismatch) {
		t.Fatalf("failed decode published auth: marshal error = %v", err)
	}
}

func TestHashVerifyPreservesDecodedWire(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors() {
		for _, tamper := range []bool{false, true} {
			t.Run(vector.name+"/tamper="+strconv.FormatBool(tamper), func(t *testing.T) {
				t.Parallel()
				wire := authVectorWire(t, vector.wire, 128)
				var wantErr error
				if tamper {
					if vector.typ == bfd.AuthTypeSimplePassword {
						wire[27] ^= 1
						wantErr = bfd.ErrAuthPasswordMismatch
					} else {
						wire[32] ^= 1
						wantErr = bfd.ErrAuthDigestMismatch
					}
				}
				before := bytes.Clone(wire)
				var pkt bfd.ControlPacket
				if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
					t.Fatal(err)
				}
				auth, err := bfd.NewAuthenticator(vector.typ)
				if err != nil {
					t.Fatal(err)
				}
				keys := newTestKeyStore(1, vector.typ, []byte("vector-key"))
				if err := auth.Verify(&bfd.AuthState{}, keys, pkt, wire, len(wire)); !errors.Is(err, wantErr) {
					t.Fatalf("Verify error = %v, want %v", err, wantErr)
				}
				if !bytes.Equal(wire, before) {
					t.Fatal("Verify changed immutable wire backing the decoded packet")
				}
				if !tamper {
					if err := auth.Verify(&bfd.AuthState{}, keys, pkt, wire, len(wire)); err != nil {
						t.Fatalf("independent second verification: %v", err)
					}
				}
			})
		}
	}
}

func TestHashVerifyMaximumPDULength(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors()[1:5] {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			wire := authVectorWire(t, vector.wire, 255)
			wire[3] = 255 // Changed authenticated Length invalidates the fixed digest.
			before := bytes.Clone(wire)
			var pkt bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
				t.Fatal(err)
			}
			auth, err := bfd.NewAuthenticator(vector.typ)
			if err != nil {
				t.Fatal(err)
			}
			state := &bfd.AuthState{}
			if err := auth.Verify(state, newTestKeyStore(1, vector.typ, []byte("vector-key")), pkt,
				wire, len(wire)); !errors.Is(err, bfd.ErrAuthDigestMismatch) {
				t.Fatalf("maximum-length PDU error = %v", err)
			}
			if state.AuthSeqKnown || !bytes.Equal(wire, before) {
				t.Fatal("rejected maximum-length PDU changed receive state or wire")
			}
		})
	}
}

func TestHashVerifyConcurrentDecodedWire(t *testing.T) {
	t.Parallel()
	vector := paddedAuthVectors()[4]
	wire := authVectorWire(t, vector.wire, 128)
	before := bytes.Clone(wire)
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
		t.Fatal(err)
	}
	auth, err := bfd.NewAuthenticator(vector.typ)
	if err != nil {
		t.Fatal(err)
	}
	keys := newTestKeyStore(1, vector.typ, []byte("vector-key"))
	results := make(chan error, 4)
	for range cap(results) {
		go func() { results <- auth.Verify(&bfd.AuthState{}, keys, pkt, wire, len(wire)) }()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if !bytes.Equal(wire, before) {
		t.Fatal("concurrent verification changed decoded wire")
	}
}
