package bfd

import (
	"fmt"
	"testing"
)

func TestRFC9764SessionAuthTransmit(t *testing.T) {
	t.Parallel()
	for _, typ := range []AuthType{
		AuthTypeSimplePassword, AuthTypeKeyedMD5, AuthTypeMeticulousKeyedMD5,
		AuthTypeKeyedSHA1, AuthTypeMeticulousKeyedSHA1,
	} {
		for _, size := range []uint16{0, 26, 64, 128, 9000} {
			t.Run(fmt.Sprintf("%s/%d", typ, size), func(t *testing.T) {
				t.Parallel()
				s := newCachedPacketTestSession(max(MaxPacketSize, int(size)))
				s.paddedPduSize = size
				auth, err := NewAuthenticator(typ)
				if err != nil {
					t.Fatal(err)
				}
				keys, err := NewStaticAuthKeyStore(AuthKey{ID: 1, Type: typ, Secret: []byte("vector-key")})
				if err != nil {
					t.Fatal(err)
				}
				s.auth, s.authKeys, s.authState = auth, keys, &AuthState{XmitAuthSeq: 41}
				sender := &retryCachedPacketSender{}
				s.sender = sender
				s.sendControl(t.Context())
				if len(sender.packets) != 1 {
					t.Fatalf("sent packets = %d, want 1", len(sender.packets))
				}
				wire := sender.packets[0]
				pduLen := 52
				if typ == AuthTypeSimplePassword {
					pduLen = 37
				} else if typ == AuthTypeKeyedMD5 || typ == AuthTypeMeticulousKeyedMD5 {
					pduLen = 48
				}
				if int(wire[3]) != pduLen || len(wire) != max(pduLen, int(size)) {
					t.Fatalf("Length=%d payload=%d, want Length=%d payload=%d", wire[3], len(wire), pduLen, max(pduLen, int(size)))
				}
				for i := pduLen; i < len(wire); i++ {
					if wire[i] != 0 {
						t.Fatalf("nonzero TX padding at %d", i)
					}
				}
				var pkt ControlPacket
				if err := UnmarshalControlPacket(wire, &pkt); err != nil {
					t.Fatal(err)
				}
				if err := auth.Verify(&AuthState{}, keys, &pkt, wire, len(wire)); err != nil {
					t.Fatalf("verify transmitted PDU: %v", err)
				}
			})
		}
	}
}
