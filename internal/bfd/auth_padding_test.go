package bfd_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type paddedAuthVector struct {
	name string
	typ  bfd.AuthType
	wire string
}

// RFC 5880 Sections 6.7.2-6.7.4 vectors, independent of Sign. Keyed digests
// were calculated by OpenSSL dgst -md5/-sha1 over the literal PDU with
// "vector-key" zero-padded in the digest slot; extension deadbeef is protected.
func paddedAuthVectors() []paddedAuthVector {
	return []paddedAuthVector{
		{
			"password", bfd.AuthTypeSimplePassword,
			"20c4032500000064000000c8000f4240000f424000000000010d01766563746f722d6b6579",
		},
		{
			"md5", bfd.AuthTypeKeyedMD5,
			"20c4033000000064000000c8000f4240000f424000000000021801000000002a" +
				"7ccecd63d30b50ac1f52d6814ae1726e",
		},
		{
			"meticulous-md5", bfd.AuthTypeMeticulousKeyedMD5,
			"20c4033000000064000000c8000f4240000f424000000000031801000000002a" +
				"68875b366052515832f76313eca4620a",
		},
		{
			"sha1", bfd.AuthTypeKeyedSHA1,
			"20c4033400000064000000c8000f4240000f424000000000041c01000000002a" +
				"941b6dbd4bfce237a20e76ff50a9e1727165aee7",
		},
		{
			"meticulous-sha1", bfd.AuthTypeMeticulousKeyedSHA1,
			"20c4033400000064000000c8000f4240000f424000000000051c01000000002a" +
				"6de969255196c29ec8abcb887b3780c7e2a4e1c7",
		},
		{
			"md5-extension", bfd.AuthTypeKeyedMD5,
			"20c4033400000064000000c8000f4240000f424000000000021801000000002a" +
				"a27db9eb8bbf59b260adca214a1eaf67deadbeef",
		},
		{
			"meticulous-md5-extension", bfd.AuthTypeMeticulousKeyedMD5,
			"20c4033400000064000000c8000f4240000f424000000000031801000000002a" +
				"82bab0a2e808dc240aaf3d3c4ec0a167deadbeef",
		},
		{
			"sha1-extension", bfd.AuthTypeKeyedSHA1,
			"20c4033800000064000000c8000f4240000f424000000000041c01000000002a" +
				"ae0e146e436d9fb7ad73422f001e7098c8f03ba9deadbeef",
		},
		{
			"meticulous-sha1-extension", bfd.AuthTypeMeticulousKeyedSHA1,
			"20c4033800000064000000c8000f4240000f424000000000051c01000000002a" +
				"6af5864cbdd7c5cee46b1ed3eb6e9aa5faa49a2cdeadbeef",
		},
	}
}

func authVectorWire(t *testing.T, encoded string, size int) []byte {
	t.Helper()
	pdu, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode vector: %v", err)
	}
	wire := make([]byte, max(len(pdu), size))
	copy(wire, pdu)
	return wire
}

func TestRFC9764AuthKnownVectors(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors() {
		for _, size := range []int{0, 64, 128, 9000} {
			for _, nonzeroPadding := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/nonzero=%t", vector.name, size, nonzeroPadding), func(t *testing.T) {
					t.Parallel()
					wire := authVectorWire(t, vector.wire, size)
					if nonzeroPadding {
						for i := int(wire[3]); i < len(wire); i++ {
							wire[i] = 0xa5 // RFC 9764 Section 3: receivers SHOULD NOT validate padding.
						}
					}
					var pkt bfd.ControlPacket
					if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
						t.Fatalf("decode: %v", err)
					}
					auth, err := bfd.NewAuthenticator(vector.typ)
					if err != nil {
						t.Fatal(err)
					}
					state := &bfd.AuthState{AuthSeqKnown: true, RcvAuthSeq: 41}
					keys := newTestKeyStore(1, vector.typ, []byte("vector-key"))
					if err := auth.Verify(state, keys, &pkt, wire, len(wire)); err != nil {
						t.Fatalf("Verify: %v", err)
					}
					wantSeq := uint32(42)
					if vector.typ == bfd.AuthTypeSimplePassword {
						wantSeq = 41
					}
					if !state.AuthSeqKnown || state.RcvAuthSeq != wantSeq {
						t.Fatalf("receive state = %+v, want known seq %d", state, wantSeq)
					}
				})
			}
		}
	}
}

func TestRFC9764SignKnownVectors(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors()[:5] {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			auth, err := bfd.NewAuthenticator(vector.typ)
			if err != nil {
				t.Fatal(err)
			}
			pkt := newTestPacket()
			buf := make([]byte, 128)
			keys := newTestKeyStore(1, vector.typ, []byte("vector-key"))
			if err := auth.Sign(&bfd.AuthState{XmitAuthSeq: 41}, keys, pkt, buf, 0); err != nil {
				t.Fatalf("Sign n=0: %v", err)
			}
			n, err := bfd.MarshalControlPacket(pkt, buf)
			if err != nil {
				t.Fatal(err)
			}
			want := authVectorWire(t, vector.wire, 0)
			if !bytes.Equal(buf[:n], want) {
				t.Fatalf("signed PDU = %x, want %x", buf[:n], want)
			}
		})
	}
}

func TestRFC9764AuthTamperPreservesSequence(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors() {
		mutations := map[string]int{"type": 24, "auth-length": 25, "key-id": 26}
		if vector.typ == bfd.AuthTypeSimplePassword {
			mutations["password"] = 27
		} else {
			mutations["protected-header"] = 12
			mutations["header-length"] = 3
			mutations["sequence"] = 31
			mutations["digest"] = 32
			if strings.HasSuffix(vector.name, "-extension") {
				mutations["extension"] = len(vector.wire)/2 - 1
			}
		}
		for name, offset := range mutations {
			for _, known := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/known=%t", vector.name, name, known), func(t *testing.T) {
					t.Parallel()
					wire := authVectorWire(t, vector.wire, 128)
					wire[offset] ^= 1
					state := &bfd.AuthState{AuthSeqKnown: known, RcvAuthSeq: 41}
					before := *state
					var pkt bfd.ControlPacket
					err := bfd.UnmarshalControlPacket(wire, &pkt)
					if err == nil {
						auth, authErr := bfd.NewAuthenticator(vector.typ)
						if authErr != nil {
							t.Fatal(authErr)
						}
						err = auth.Verify(state, newTestKeyStore(1, vector.typ, []byte("vector-key")), &pkt, wire, len(wire))
					}
					if err == nil {
						t.Fatal("tampered packet accepted")
					}
					if *state != before {
						t.Fatalf("rejected packet changed state: %+v, want %+v", state, before)
					}
				})
			}
		}
	}
}

func TestRFC9764AuthWireBounds(t *testing.T) {
	t.Parallel()
	for _, vector := range paddedAuthVectors()[1:5] {
		pduSize := len(vector.wire) / 2
		for _, tc := range []struct {
			name                    string
			n, bufferSize, declared int
		}{
			{"negative n", -1, pduSize, pduSize},
			{"zero n", 0, pduSize, pduSize},
			{"truncated n", pduSize - 1, pduSize, pduSize},
			{"n beyond buffer", pduSize + 1, pduSize, pduSize},
			{"short buffer", pduSize, 3, pduSize},
			{"nil buffer", pduSize, 0, pduSize},
			{"zero Length", pduSize, pduSize, 0},
			{"Length below digest", pduSize, pduSize, pduSize - 1},
			{"Length exceeds n", pduSize, pduSize + 1, pduSize + 1},
		} {
			t.Run(vector.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				wire := authVectorWire(t, vector.wire, tc.bufferSize)
				var pkt bfd.ControlPacket
				if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
					t.Fatal(err)
				}
				wire[3] = byte(tc.declared) // Keep pkt.Length stale: the wire is authoritative.
				wire = wire[:tc.bufferSize]
				auth, err := bfd.NewAuthenticator(vector.typ)
				if err != nil {
					t.Fatal(err)
				}
				state := &bfd.AuthState{RcvAuthSeq: 41}
				before := *state
				err = auth.Verify(state, newTestKeyStore(1, vector.typ, []byte("vector-key")), &pkt, wire, tc.n)
				if !errors.Is(err, bfd.ErrAuthWireMissing) {
					t.Fatalf("Verify = %v, want ErrAuthWireMissing", err)
				}
				if *state != before {
					t.Fatalf("invalid wire changed state: %+v, want %+v", state, before)
				}
			})
		}
	}
}

func TestRFC9764UnauthenticatedBaseline(t *testing.T) {
	t.Parallel()
	const pdu = "20c0031800000064000000c8000f4240000f424000000000"
	for _, size := range []int{0, 64, 128, 9000} {
		wire := authVectorWire(t, pdu, size)
		for i := bfd.HeaderSize; i < len(wire); i++ {
			wire[i] = 0xa5
		}
		var pkt bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if pkt.AuthPresent || pkt.Length != bfd.HeaderSize {
			t.Fatalf("unexpected unauthenticated baseline: %+v", pkt)
		}
	}
}

func TestRFC9764SimplePasswordWireContract(t *testing.T) {
	t.Parallel()
	vector := paddedAuthVectors()[0]
	wire := authVectorWire(t, vector.wire, 0)
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
		t.Fatal(err)
	}
	state := &bfd.AuthState{AuthSeqKnown: true, RcvAuthSeq: 41}
	before := *state
	keys := newTestKeyStore(1, vector.typ, []byte("vector-key"))
	if err := (bfd.SimplePasswordAuth{}).Verify(state, keys, &pkt, nil, 0); err != nil {
		t.Fatalf("Verify without wire: %v", err)
	}
	if *state != before {
		t.Fatalf("password verification changed sequence state: %+v", state)
	}
	for _, size := range []int{0, 128} {
		for _, length := range []byte{0, 23, 24, 25, 26, 36, 129} {
			wire := authVectorWire(t, vector.wire, size)
			wire[3] = length
			if err := bfd.UnmarshalControlPacket(wire, &bfd.ControlPacket{}); err == nil {
				t.Errorf("size %d Length %d: malformed password PDU accepted", size, length)
			}
		}
	}
}
