package bfd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestManagerLastClaimDrainWaitsForRetryAndWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64), fail: true}
		released := make(chan time.Time, 2)
		cfg := ownershipTestConfig("192.0.2.201")
		alice, bob := APISessionOwner("alice"), APISessionOwner("bob")
		s, err := mgr.CreateSessionAs(t.Context(), cfg, func() (*SenderLease, error) {
			return NewSenderLease(sender, func() error { released <- time.Now(); return nil }), nil
		}, alice)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.CreateSessionAs(t.Context(), cfg, ownershipSenderLeaseFactory(), bob); err != nil {
			t.Fatal(err)
		}
		if err := mgr.DestroySessionAs(t.Context(), s.LocalDiscriminator(), alice); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if s.State() != StateDown || len(sender.attempts) != 0 || len(released) != 0 {
			t.Fatal("shared claim release changed wire state or released its sender")
		}
		result := make(chan error, 1)
		go func() { result <- mgr.DestroySessionAs(t.Context(), s.LocalDiscriminator(), bob) }()
		synctest.Wait()
		if len(sender.attempts) != 1 || len(sender.successful) != 0 || len(result) != 0 || len(released) != 0 {
			t.Fatal("last claim did not retain sender across failed first AdminDown write")
		}
		first := <-sender.successful
		if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown {
			t.Fatal("successful drain write preceded owner AdminDown/7 commit")
		}
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(result) != 0 || len(released) != 0 {
			t.Fatal("last claim released sender before negotiated notification window")
		}
		synctest.Sleep(time.Nanosecond)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if mgr.discriminators.IsAllocated(s.LocalDiscriminator()) {
			t.Error("last claim leaked discriminator after drain")
		}
		if at := <-released; at.Sub(first) != 3*time.Second {
			t.Errorf("release delay=%v, want 3s", at.Sub(first))
		}
		if err := mgr.DestroySessionAs(t.Context(), s.LocalDiscriminator(), bob); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("repeated destroy=%v", err)
		}
		mgr.Close()
		if len(released) != 0 {
			t.Error("sender released more than once")
		}
	})
}

func TestManagerLastClaimDrainIncompleteReleasesResources(t *testing.T) {
	for _, kind := range []string{"deadline", "cancel", "passive"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
				cfg := ownershipTestConfig("192.0.2.202")
				if kind == "passive" {
					cfg.Role = RolePassive
				}
				releases := 0
				releaseErr := errors.New("injected lease release failure")
				s, err := mgr.CreateSession(t.Context(), cfg, func() (*SenderLease, error) {
					return NewSenderLease(sender, func() error { releases++; return releaseErr }), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				ctx := context.Background()
				var cancel context.CancelFunc
				wantErr, wantDuration := context.DeadlineExceeded, 10*time.Second
				switch kind {
				case "deadline":
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					wantDuration = time.Second
				case "cancel":
					ctx, cancel = context.WithCancel(t.Context())
					wantErr, wantDuration = context.Canceled, 0
					cancel()
				}
				if cancel != nil {
					defer cancel()
				}
				start := time.Now()
				err = mgr.DestroySession(ctx, s.LocalDiscriminator())
				if !errors.Is(err, wantErr) || !errors.Is(err, releaseErr) {
					t.Errorf("destroy=%v, want context and release errors", err)
				}
				if _, ok := errors.AsType[*DrainIncompleteError](err); !ok {
					t.Errorf("destroy did not return typed incomplete: %v", err)
				}
				if elapsed := time.Since(start); elapsed != wantDuration {
					t.Errorf("destroy elapsed=%v, want %v", elapsed, wantDuration)
				}
				if kind == "passive" && len(sender.attempts) != 0 {
					t.Error("drain bypassed transmission suppression")
				}
				if releases != 1 || len(mgr.Sessions()) != 0 {
					t.Errorf("cleanup releases=%d sessions=%d", releases, len(mgr.Sessions()))
				}
				if mgr.discriminators.IsAllocated(s.LocalDiscriminator()) {
					t.Error("incomplete drain leaked discriminator")
				}
				mgr.Close()
				if releases != 1 {
					t.Error("Close released retired sender again")
				}
			})
		})
	}
}

// RFC 5880 Section 6.8.18: the remote system "is obligated to obey the value
// carried in Required Min RX Interval" only "so long as the local system
// continues to transmit"; after a Detection Time without packets it "SHOULD
// reset bfd.RemoteMinRxInterval to its initial value of 1". The Section
// 6.8.16 AdminDown notification then proceeds and the drain completes.
func TestManagerLastClaimDrainCompletesAfterSilentZeroRx(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		releases := 0
		releaseErr := errors.New("injected lease release failure")
		s, err := mgr.CreateSession(t.Context(), ownershipTestConfig("192.0.2.202"), func() (*SenderLease, error) {
			return NewSenderLease(sender, func() error { releases++; return releaseErr }), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		s.mu.Lock()
		s.remoteMinRxInterval = 0
		s.mu.Unlock()
		detection := s.DetectionTime()
		start := time.Now()
		err = mgr.DestroySession(t.Context(), s.LocalDiscriminator())
		if !errors.Is(err, releaseErr) {
			t.Errorf("destroy=%v, want release error", err)
		}
		if _, ok := errors.AsType[*DrainIncompleteError](err); ok {
			t.Errorf("drain stayed incomplete after a silent Detection Time: %v", err)
		}
		if len(sender.successful) == 0 {
			t.Fatal("drain completed without an AdminDown write")
		}
		first := <-sender.successful
		if sent := first.Sub(start); sent < detection || sent > detection+slowTxInterval {
			t.Errorf("first AdminDown write after %v, want within [%v, %v]", sent, detection, detection+slowTxInterval)
		}
		if elapsed := time.Since(start); elapsed != first.Sub(start)+3*time.Second {
			t.Errorf("destroy elapsed=%v, want first write + 3s window", elapsed)
		}
		if releases != 1 || len(mgr.Sessions()) != 0 {
			t.Errorf("cleanup releases=%d sessions=%d", releases, len(mgr.Sessions()))
		}
	})
}

func TestManagerLastClaimDrainCapturesIdentityWithoutLocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		cfg := ownershipTestConfig("192.0.2.203")
		s, err := mgr.CreateSession(t.Context(), cfg, NonOwningSenderLeaseFactory(sender))
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- mgr.DestroySession(t.Context(), s.LocalDiscriminator()) }()
		synctest.Wait()
		if len(sender.successful) == 0 || len(result) != 0 {
			t.Fatal("last claim did not wait for drain")
		}
		if !mgr.mu.TryLock() {
			t.Fatal("drain holds registry lock")
		}
		mgr.mu.Unlock()
		if !mgr.ownershipMu.TryLock() {
			t.Fatal("drain holds ownership lock")
		}
		mgr.ownershipMu.Unlock()
		if !mgr.lifecycleMu.TryLock() {
			t.Fatal("drain holds lifecycle lock")
		}
		mgr.lifecycleMu.Unlock()
		if !mgr.discriminators.IsAllocated(s.LocalDiscriminator()) {
			t.Error("retiring discriminator released before drain")
		}
		replacement, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory())
		if err != nil {
			t.Fatalf("same-key replacement during drain: %v", err)
		}
		if replacement.Ref() == s.Ref() || replacement.LocalDiscriminator() == s.LocalDiscriminator() {
			t.Error("replacement reused retiring identity/discriminator")
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if replacement.State() != StateDown || len(mgr.Sessions()) != 1 {
			t.Error("retired cleanup drained or removed replacement")
		}
	})
}

func TestManagerLastClaimReconcileSharesBoundedBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		desired := make([]ReconcileConfig, 0, 2)
		releases := make(chan struct{}, 2)
		for _, peer := range []string{"192.0.2.208", "192.0.2.209"} {
			cfg := ownershipTestConfig(peer)
			cfg.Role = RolePassive
			desired = append(desired, ReconcileConfig{
				SessionConfig: cfg,
				SenderLeaseFactory: func() (*SenderLease, error) {
					return NewSenderLease(senderLeaseTestSender{}, func() error { releases <- struct{}{}; return nil }), nil
				},
			})
		}
		if _, _, err := mgr.ReconcileSessions(t.Context(), desired); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_, destroyed, err := mgr.ReconcileSessions(context.Background(), nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("batch incomplete=%v", err)
		}
		if _, ok := errors.AsType[*DrainIncompleteError](err); !ok {
			t.Errorf("batch did not preserve typed incomplete: %v", err)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second || destroyed != 2 || len(releases) != 2 {
			t.Errorf("batch elapsed=%v destroyed=%d releases=%d, want 10s/2/2", elapsed, destroyed, len(releases))
		}
	})
}

func TestManagerLastClaimReconcileDrainsBatchAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "rollback"}[rollback], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				senders := make([]*adminDrainSender, 2)
				released := make(chan time.Time, 2)
				desired := make([]ReconcileConfig, 0, 3)
				for i, peer := range []string{"192.0.2.204", "192.0.2.205"} {
					sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
					senders[i] = sender
					desired = append(desired, ReconcileConfig{
						SessionConfig: ownershipTestConfig(peer),
						SenderLeaseFactory: func() (*SenderLease, error) {
							return NewSenderLease(sender, func() error { released <- time.Now(); return nil }), nil
						},
					})
				}
				if rollback {
					desired = append(desired, ReconcileConfig{
						SessionConfig:      ownershipTestConfig("192.0.2.206"),
						SenderLeaseFactory: func() (*SenderLease, error) { return nil, errSenderLeaseTestFactory },
					})
				} else {
					if _, _, err := mgr.ReconcileSessions(t.Context(), desired); err != nil {
						t.Fatal(err)
					}
					desired = nil
				}
				start := time.Now()
				result := make(chan error, 1)
				go func() { _, _, err := mgr.ReconcileSessions(t.Context(), desired); result <- err }()
				synctest.Wait()
				for i, sender := range senders {
					if len(sender.successful) != 1 {
						t.Errorf("session %d did not begin drain concurrently", i)
					}
				}
				if len(released) != 0 || len(result) != 0 {
					t.Error("batch cleanup released sender before drain window")
				}
				err := <-result
				if (!rollback && err != nil) || (rollback && !errors.Is(err, errSenderLeaseTestFactory)) {
					t.Errorf("reconcile=%v", err)
				}
				if elapsed := time.Since(start); elapsed != 3*time.Second || len(released) != 2 {
					t.Errorf("batch elapsed=%v releases=%d, want 3s/2", elapsed, len(released))
				}
			})
		})
	}
}

func TestManagerCloseDoesNotRepeatLastClaimDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		releases := 0
		s, err := mgr.CreateSession(t.Context(), ownershipTestConfig("192.0.2.207"), func() (*SenderLease, error) {
			return NewSenderLease(sender, func() error { releases++; return nil }), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- mgr.DestroySession(t.Context(), s.LocalDiscriminator()) }()
		synctest.Wait()
		if len(sender.successful) == 0 {
			t.Fatal("last claim did not start drain before Close")
		}
		start := time.Now()
		mgr.Close()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 3*time.Second || releases != 1 {
			t.Errorf("Close elapsed=%v releases=%d, want shared 3s/1", elapsed, releases)
		}
		if len(sender.attempts) == 0 {
			t.Error("last claim completed without AdminDown transmission")
		}
	})
}

func TestManagerLastClaimUnsolicitedFailureCleanup(t *testing.T) {
	for _, closeError := range []bool{false, true} {
		t.Run(map[bool]string{false: "released", true: "release-error"}[closeError], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logs bytes.Buffer
				sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
				releases := 0
				releaseErr := errors.New("injected unsolicited release failure")
				mgr := NewManager(slog.New(slog.NewTextHandler(&logs, nil)),
					WithUnsolicitedPolicy(admissionPolicy(1)),
					WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
					WithUnsolicitedSessionSender(func(PacketMeta) (*SenderLease, error) {
						return NewSenderLease(sender, func() error {
							releases++
							if closeError {
								return releaseErr
							}
							return nil
						}), nil
					}),
				)
				defer mgr.Close()
				if _, err := mgr.tryCreateUnsolicited(ownershipDownPacket(101),
					admissionMeta("192.0.2.210", "192.0.2.254"), nil); err != nil {
					t.Fatal(err)
				}
				s, _ := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
				synctest.Wait()
				if s.SetPathDown() != DeliveryQueued {
					t.Fatal("queue unsolicited PathDown")
				}
				synctest.Wait()
				start := time.Now()
				mgr.cleanupUnsolicitedSession(t.Context(), s)
				if elapsed := time.Since(start); elapsed != 0 || releases != 1 || len(mgr.Sessions()) != 0 {
					t.Errorf("unsolicited cleanup elapsed=%v releases=%d sessions=%d", elapsed, releases, len(mgr.Sessions()))
				}
				if s.State() != StateDown || len(sender.attempts) != 0 {
					t.Error("unsolicited failure cleanup resumed Control transmission with AdminDown")
				}
				if closeError && !strings.Contains(logs.String(), releaseErr.Error()) {
					t.Errorf("unsolicited release error was not logged: %s", logs.String())
				}
			})
		})
	}
}
