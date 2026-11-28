package bfd

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

func TestSessionRejectedAuthLogIsUnique(t *testing.T) {
	t.Parallel()
	keys, err := NewStaticAuthKeyStore(AuthKey{ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("fixture")})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, err := NewSession(SessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.2"), LocalAddr: netip.MustParseAddr("192.0.2.1"),
		Type: SessionTypeSingleHop, Role: RoleActive, DesiredMinTxInterval: 100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond, DetectMultiplier: 3, Auth: MeticulousKeyedSHA1Auth{}, AuthKeys: keys,
	}, 42, &retryCachedPacketSender{}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	s.authState.AuthSeqKnown, s.authState.RcvAuthSeq = true, 201
	s.recordValidReceivedPacket()
	before := s.counters()
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	defer tx.Stop()
	defer detect.Stop()
	s.handleRecvPacket(t.Context(), signedAuthExpiryPacket(t, s, 201), tx, detect)
	after := s.counters()
	if after.PacketsReceived != before.PacketsReceived || after.AuthFailures != before.AuthFailures+1 ||
		s.State() != StateDown || s.authState.RcvAuthSeq != 201 || !s.authState.AuthSeqKnown {
		t.Fatal("rejected authentication changed accepted receive/FSM state")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		if tokenErr != nil {
			t.Fatal(tokenErr)
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			t.Fatalf("duplicate or invalid session log member: %v", token)
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	var event struct {
		Message string `json:"msg"`
		Peer    string `json:"peer"`
		Local   uint32 `json:"local_discr"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Message != "auth verification failed" || event.Peer != "192.0.2.2" || event.Local != 42 || event.Error == "" {
		t.Fatalf("authentication log lost diagnostic context: %+v", event)
	}
}
