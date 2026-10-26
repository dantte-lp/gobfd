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
