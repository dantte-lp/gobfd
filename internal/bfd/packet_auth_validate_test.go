package bfd_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func validOutboundPacket(auth *bfd.AuthSection) *bfd.ControlPacket {
	return &bfd.ControlPacket{
		Version:           bfd.Version,
		State:             bfd.StateUp,
		DetectMult:        3,
		MyDiscriminator:   1,
		YourDiscriminator: 2,
		AuthPresent:       auth != nil,
		Auth:              auth,
	}
}

func digestAuth(authType bfd.AuthType, authLen uint8, digestLen int) *bfd.AuthSection {
	return &bfd.AuthSection{Type: authType, Len: authLen, KeyID: 1, Digest: make([]byte, digestLen)}
}

// H-03: MarshalControlPacket must reject an inconsistent authentication
// section before it writes any byte of buf.
func TestMarshalRejectsInvalidOutboundAuth(t *testing.T) {
	t.Parallel()

	md5 := func(n int) *bfd.AuthSection { return digestAuth(bfd.AuthTypeKeyedMD5, 24, n) }
	sha1 := func(n int) *bfd.AuthSection { return digestAuth(bfd.AuthTypeMeticulousKeyedSHA1, 28, n) }
	password := func(lenField uint8, n int) *bfd.AuthSection {
		return &bfd.AuthSection{
			Type: bfd.AuthTypeSimplePassword, Len: lenField, KeyID: 1, AuthData: bytes.Repeat([]byte("p"), n),
		}
	}

	tests := []struct {
		name    string
		pkt     *bfd.ControlPacket
		bufSize int
		wantErr error
	}{
		{"A set without auth", &bfd.ControlPacket{Version: bfd.Version, AuthPresent: true}, 64, bfd.ErrAuthMismatch},
		{"auth without A", &bfd.ControlPacket{Version: bfd.Version, Auth: md5(16)}, 64, bfd.ErrAuthMismatch},
		{"reserved type 0", validOutboundPacket(digestAuth(0, 24, 16)), 64, bfd.ErrInvalidAuthType},
		{"unknown type 6", validOutboundPacket(digestAuth(6, 24, 16)), 64, bfd.ErrInvalidAuthType},
		{"MD5 Len 28", validOutboundPacket(digestAuth(bfd.AuthTypeKeyedMD5, 28, 16)), 64, bfd.ErrAuthLenMismatch},
		{"MD5 short digest", validOutboundPacket(md5(15)), 64, bfd.ErrAuthLenMismatch},
		{"MD5 long digest", validOutboundPacket(md5(20)), 64, bfd.ErrAuthLenMismatch},
		{"MD5 nil digest", validOutboundPacket(md5(0)), 64, bfd.ErrAuthLenMismatch},
		{"SHA1 Len 24", validOutboundPacket(digestAuth(bfd.AuthTypeKeyedSHA1, 24, 20)), 64, bfd.ErrAuthLenMismatch},
		{"SHA1 short digest", validOutboundPacket(sha1(16)), 64, bfd.ErrAuthLenMismatch},
		{"password empty", validOutboundPacket(password(3, 0)), 64, bfd.ErrAuthLenMismatch},
		{"password 17 bytes", validOutboundPacket(password(20, 17)), 64, bfd.ErrAuthLenMismatch},
		{"password Len short", validOutboundPacket(password(4, 5)), 64, bfd.ErrAuthLenMismatch},
		{"password Len long", validOutboundPacket(password(10, 5)), 64, bfd.ErrAuthLenMismatch},
		{"buffer below header", validOutboundPacket(nil), bfd.HeaderSize - 1, bfd.ErrBufTooSmall},
		{"buffer below auth", validOutboundPacket(md5(16)), bfd.HeaderSize + 23, bfd.ErrBufTooSmall},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := bytes.Repeat([]byte{0xAA}, tt.bufSize)
			n, err := func() (n int, err error) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("MarshalControlPacket panicked: %v", r)
					}
				}()
				return bfd.MarshalControlPacket(tt.pkt, buf)
			}()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("MarshalControlPacket error = %v, want %v", err, tt.wantErr)
			}
			if n != 0 {
				t.Fatalf("MarshalControlPacket n = %d, want 0", n)
			}
			if !bytes.Equal(buf, bytes.Repeat([]byte{0xAA}, tt.bufSize)) {
				t.Fatalf("buffer mutated on validation failure: % x", buf)
			}
		})
	}
}

// Every valid authentication type marshals with the exact RFC 5880 length,
// key ID and payload, and round-trips through UnmarshalControlPacket.
func TestMarshalValidOutboundAuth(t *testing.T) {
	t.Parallel()

	hashed := func(authType bfd.AuthType, authLen, keyID uint8, seq uint32, digestLen int) bfd.AuthSection {
		return bfd.AuthSection{
			Type: authType, Len: authLen, KeyID: keyID, SequenceNumber: seq,
			Digest: bytes.Repeat([]byte{0x5C}, digestLen),
		}
	}
	tests := []struct {
		name string
		auth bfd.AuthSection
	}{
		{"password 1", bfd.AuthSection{Type: bfd.AuthTypeSimplePassword, Len: 4, KeyID: 7, AuthData: []byte("p")}},
		{"password 16", bfd.AuthSection{
			Type: bfd.AuthTypeSimplePassword, Len: 19, KeyID: 255, AuthData: bytes.Repeat([]byte("p"), 16),
		}},
		{"keyed MD5", hashed(bfd.AuthTypeKeyedMD5, 24, 2, 9, 16)},
		{"meticulous MD5", hashed(bfd.AuthTypeMeticulousKeyedMD5, 24, 3, 10, 16)},
		{"keyed SHA1", hashed(bfd.AuthTypeKeyedSHA1, 28, 4, 11, 20)},
		{"meticulous SHA1", hashed(bfd.AuthTypeMeticulousKeyedSHA1, 28, 0, 12, 20)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := tt.auth
			buf := make([]byte, bfd.HeaderSize+int(auth.Len))
			n, err := bfd.MarshalControlPacket(validOutboundPacket(&auth), buf)
			if err != nil {
				t.Fatalf("MarshalControlPacket: %v", err)
			}
			if n != len(buf) {
				t.Fatalf("n = %d, want %d", n, len(buf))
			}

			var got bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(buf, &got); err != nil {
				t.Fatalf("UnmarshalControlPacket: %v", err)
			}
			if got.Auth == nil || got.Auth.Type != auth.Type || got.Auth.Len != auth.Len ||
				got.Auth.KeyID != auth.KeyID || got.Auth.SequenceNumber != auth.SequenceNumber ||
				!bytes.Equal(got.Auth.AuthData, auth.AuthData) || !bytes.Equal(got.Auth.Digest, auth.Digest) {
				t.Fatalf("round trip auth = %+v, want %+v", got.Auth, auth)
			}
		})
	}
}

// FuzzMarshalOutboundAuth: any authentication section either marshals into
// a packet that decodes to the same section, or fails without touching buf.
func FuzzMarshalOutboundAuth(f *testing.F) {
	f.Add(true, true, uint8(bfd.AuthTypeKeyedMD5), uint8(24), uint8(1), []byte("pw"), bytes.Repeat([]byte{1}, 16))
	f.Add(true, true, uint8(bfd.AuthTypeSimplePassword), uint8(5), uint8(1), []byte("pw"), []byte(nil))
	f.Add(true, false, uint8(0), uint8(0), uint8(0), []byte(nil), []byte(nil))
	f.Add(true, true, uint8(bfd.AuthTypeKeyedSHA1), uint8(8), uint8(1), []byte(nil), []byte{1, 2})

	f.Fuzz(func(t *testing.T, aBit, hasAuth bool, authType, authLen, keyID uint8, data, digest []byte) {
		pkt := validOutboundPacket(nil)
		pkt.AuthPresent = aBit
		if hasAuth {
			pkt.Auth = &bfd.AuthSection{
				Type: bfd.AuthType(authType), Len: authLen, KeyID: keyID, AuthData: data, Digest: digest,
			}
		}

		buf := bytes.Repeat([]byte{0xAA}, bfd.MaxPacketSize)
		n, err := bfd.MarshalControlPacket(pkt, buf)
		if err != nil {
			if !bytes.Equal(buf, bytes.Repeat([]byte{0xAA}, bfd.MaxPacketSize)) {
				t.Fatalf("buffer mutated on error %v", err)
			}
			return
		}

		var got bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(buf[:n], &got); err != nil {
			t.Fatalf("marshaled packet does not decode: %v", err)
		}
		if (got.Auth == nil) != (pkt.Auth == nil) {
			t.Fatalf("decoded auth = %+v, want %+v", got.Auth, pkt.Auth)
		}
		if pkt.Auth != nil && (got.Auth.Type != pkt.Auth.Type || got.Auth.Len != pkt.Auth.Len ||
			got.Auth.KeyID != keyID) {
			t.Fatalf("decoded auth = %+v, want %+v", got.Auth, pkt.Auth)
		}
	})
}
