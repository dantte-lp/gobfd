package bfd

import (
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestDetectionReceiveResetReplacesExpiredUnreadTimer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		consumer func(*testing.T) (*time.Timer, time.Duration, func())
	}{
		{
			name: "Control",
			consumer: func(t *testing.T) (*time.Timer, time.Duration, func()) {
				t.Helper()
				s, _, tx, detect := newRemoteTxTestSession(t)
				packet := s.buildControlPacket()
				packet.State, packet.MyDiscriminator = StateUp, 7
				packet.YourDiscriminator = s.localDiscr
				return detect, s.calcDetectionTimeHot(), func() {
					s.handleRecvPacket(t.Context(), recvItem{pkt: packet}, tx, detect)
					if s.PacketsReceived() != 1 || s.State() != StateUp {
						t.Fatal("Control receive did not commit a valid Up packet")
					}
				}
			},
		},
		{
			name: "Echo",
			consumer: func(t *testing.T) (*time.Timer, time.Duration, func()) {
				t.Helper()
				cfg := echoLeaseConfig("192.0.2.61")
				cfg.TxInterval = 100 * time.Millisecond
				es, err := NewEchoSession(cfg, 42, echoLeaseSender{}, nil, slog.New(slog.DiscardHandler))
				if err != nil {
					t.Fatal(err)
				}
				detect := time.NewTimer(time.Hour)
				t.Cleanup(func() { detect.Stop() })
				return detect, es.DetectionTime(), func() {
					es.RecvEcho()
					<-es.recvCh
					es.handleEchoRecv(t.Context(), detect)
					if es.EchosReceived() != 1 || es.State() != StateUp {
						t.Fatal("Echo receive did not commit an Up return")
					}
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				detect, detectionTime, receive := tc.consumer(t)
				detect.Reset(time.Millisecond)
				// Leave the expired tick unread until the actual receive consumer resets it.
				synctest.Sleep(2 * time.Millisecond)
				wantDue := time.Now().Add(detectionTime)
				receive()
				select {
				case <-detect.C:
					t.Fatal("receive reset left the old detection tick readable")
				default:
				}
				synctest.Sleep(detectionTime - time.Nanosecond)
				select {
				case <-detect.C:
					t.Fatal("receive reset fired before the new detection deadline")
				default:
				}
				synctest.Sleep(time.Nanosecond)
				select {
				case due := <-detect.C:
					if due != wantDue {
						t.Fatalf("detection due = %s, want %s", due, wantDue)
					}
				default:
					t.Fatal("receive reset missed the new detection deadline")
				}
			})
		})
	}
}
