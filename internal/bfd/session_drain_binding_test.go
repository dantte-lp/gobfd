package bfd

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestManagerDrainPendingBindingIsLocal(t *testing.T) {
	for _, tc := range []struct {
		name               string
		pending, restore   bool
		passive            bool
		change             string
		peerOnly, complete bool
	}{
		{name: "confirmed", peerOnly: true},
		{name: "pending", pending: true},
		{name: "unknown passive pending", pending: true, passive: true},
		{name: "initial pending recovers", pending: true, change: "initial", restore: true},
		{name: "prepare during window", change: "prepare"},
		{name: "suspend during window", change: "suspend"},
		{name: "prepare recovers", change: "prepare", restore: true},
		{name: "suspend recovers", change: "suspend", restore: true},
		{name: "completion wins", pending: true, change: "initial", restore: true, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &adminDrainSender{
					attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64), packets: make(chan []byte, 64),
				}
				s := newAdminDownTestSession(t, sender, 69)
				s.role, s.remoteDiscr = RoleActive, 91
				if tc.passive {
					s.role, s.remoteDiscr = RolePassive, 0
				}
				s.remoteMinRxInterval = 0
				s.remoteDesiredMinTxInterval, s.remoteDetectMult = 20*time.Second, 3
				s.pendingFinal = true
				s.receiveBinding.Store(&receiveBinding{ifIndex: 7, pending: tc.pending})
				mgr := NewManager(s.logger)
				mgr.sessions[69] = &sessionEntry{session: s}
				limit := 10 * time.Second
				if tc.complete {
					limit = 90 * time.Second
				}
				budget, stop := context.WithTimeout(t.Context(), limit)
				drainDone := make(chan struct{})
				var drainErr error
				go func() { drainErr = mgr.DrainAllSessions(budget); close(drainDone) }()
				defer func() { stop(); <-drainDone }()
				synctest.Wait()
				if len(s.ctrlCh) != 1 {
					t.Fatal("AdminDown was not queued before owner start")
				}
				ctx, cancel := context.WithCancel(t.Context())
				ownerDone := make(chan struct{})
				go func() { s.Run(ctx); close(ownerDone) }()
				defer func() { cancel(); <-ownerDone }()
				synctest.Wait()
				if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown {
					t.Fatal("owner did not commit AdminDown/7")
				}
				writes := 1
				stage := "notification window"
				if tc.pending {
					writes, stage = 0, "transmission"
				}
				if len(sender.attempts) != writes || len(sender.successful) != writes {
					t.Fatalf("AdminDown attempts=%d successes=%d, want %d", len(sender.attempts), len(sender.successful), writes)
				}
				if tc.change != "" {
					synctest.Sleep(time.Second)
					binding := s.receiveBinding.Load()
					switch tc.change {
					case "prepare":
						var err error
						binding, err = s.prepareReceiveBinding(8)
						if err != nil {
							t.Fatal(err)
						}
					case "suspend":
						if err := s.suspendReceiveBinding(); err != nil {
							t.Fatal(err)
						}
						binding = s.receiveBinding.Load()
					}
					if binding == nil || !binding.pending {
						t.Fatal("local receive binding did not become pending")
					}
					synctest.Sleep(time.Second)
					if tc.restore {
						if err := s.commitReceiveBinding(t.Context(), binding, &receiveBinding{ifIndex: 8}); err != nil {
							t.Fatal(err)
						}
						if tc.pending {
							<-sender.successful
							synctest.Wait()
							writes = 1
							stage = "notification window"
						}
					}
				}
				if len(sender.packets) != writes {
					t.Fatalf("captured writes=%d, want %d", len(sender.packets), writes)
				}
				if writes != 0 {
					var packet ControlPacket
					if err := UnmarshalControlPacket(<-sender.packets, &packet); err != nil {
						t.Fatal(err)
					}
					if !packet.Final || packet.State != StateAdminDown || packet.Diag != DiagAdminDown {
						t.Fatalf("first write=%+v, want AdminDown/7 Final", packet)
					}
				}
				<-drainDone
				if tc.complete {
					if drainErr != nil {
						t.Fatalf("completed notification window lost precedence over historical binding: %v", drainErr)
					}
					canceled, cancelCaller := context.WithCancel(t.Context())
					cancelCaller()
					if err := mgr.DrainAllSessions(canceled); err != nil {
						t.Fatalf("completed drain lost precedence for a repeated canceled caller: %v", err)
					}
					return
				}
				incomplete, ok := errors.AsType[*DrainIncompleteError](drainErr)
				if !ok || !errors.Is(drainErr, context.DeadlineExceeded) {
					t.Fatalf("drain=%v, want typed deadline", drainErr)
				}
				if incomplete.Stage != stage || incomplete.SendErr != nil {
					t.Fatalf("drain=%v, want %s without failed write", drainErr, stage)
				}
				if incomplete.PeerAttributed() != tc.peerOnly {
					t.Errorf("peer attributed=%t, want %t: %v", incomplete.PeerAttributed(), tc.peerOnly, drainErr)
				}
				if tc.restore {
					retryCtx, cancelRetry := context.WithTimeout(t.Context(), time.Second)
					defer cancelRetry()
					retryErr := mgr.DrainAllSessions(retryCtx)
					retry, ok := errors.AsType[*DrainIncompleteError](retryErr)
					if !ok || !errors.Is(retryErr, context.DeadlineExceeded) || retry.PeerAttributed() {
						t.Errorf("repeated drain lost local binding history: %v", retryErr)
					}
				}
			})
		})
	}
}
