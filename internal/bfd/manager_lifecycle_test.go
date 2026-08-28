package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type lifecycleBlockingSender struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type lifecycleOrderMetrics struct {
	noopMetrics

	manager *Manager
	discr   uint32
	events  chan string
}

func (m *lifecycleOrderMetrics) UnregisterSession(netip.Addr, netip.Addr, string) {
	if m.manager.discriminators.IsAllocated(m.discr) {
		m.events <- "metrics-before-discriminator-release"
		return
	}
	m.events <- "metrics-after-discriminator-release"
}

func (s *lifecycleBlockingSender) SendPacket(context.Context, []byte, netip.Addr) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}

func TestManagerCloseWaitsForSessionExitBeforeSenderRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &lifecycleBlockingSender{
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		callbackCalled := make(chan struct{})
		mgr := NewManager(slog.New(slog.DiscardHandler))
		sess, err := mgr.CreateSession(
			context.Background(),
			senderLeaseTestConfig("192.0.2.60"),
			func() (*SenderLease, error) {
				return NewSenderLease(sender, func() error {
					close(callbackCalled)
					return nil
				}), nil
			},
		)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		synctest.Wait()
		if !sess.running.Load() {
			t.Fatal("session goroutine did not start")
		}
		sess.SetAdminDown()
		<-sender.entered

		closeDone := make(chan struct{})
		go func() {
			mgr.Close()
			close(closeDone)
		}()
		synctest.Wait()

		select {
		case <-callbackCalled:
			t.Error("sender release callback ran before the blocked session goroutine exited")
		default:
		}
		select {
		case <-closeDone:
			t.Error("Manager.Close returned before the blocked session goroutine exited")
		default:
		}

		close(sender.release)
		synctest.Wait()
		select {
		case <-callbackCalled:
		default:
			t.Error("sender release callback did not run after the session goroutine exited")
		}
		select {
		case <-closeDone:
		default:
			t.Error("Manager.Close did not return after the session goroutine exited")
		}
	})
}

func TestManagerCloseInvokesSenderReleaseOutsideManagerLocks(t *testing.T) {
	var managerUnlocked, ownershipUnlocked bool
	var mgr *Manager
	lease := NewSenderLease(senderLeaseTestSender{}, func() error {
		managerUnlocked = mgr.mu.TryRLock()
		if managerUnlocked {
			mgr.mu.RUnlock()
		}
		ownershipUnlocked = mgr.ownershipMu.TryLock()
		if ownershipUnlocked {
			mgr.ownershipMu.Unlock()
		}
		return nil
	})
	mgr = NewManager(
		slog.New(slog.DiscardHandler),
		WithUnsolicitedSenderLease(lease),
	)

	mgr.Close()

	if !managerUnlocked {
		t.Error("sender release callback could not reacquire manager.mu")
	}
	if !ownershipUnlocked {
		t.Error("sender release callback could not reacquire ownershipMu")
	}
}

func TestManagerDetachedSessionReleaseCanReenterClose(t *testing.T) {
	testCases := []struct {
		name    string
		release func(*Manager, uint32) error
	}{
		{
			name: "DestroySession",
			release: func(mgr *Manager, discr uint32) error {
				return mgr.DestroySession(context.Background(), discr)
			},
		},
		{
			name: "ReconcileSessions",
			release: func(mgr *Manager, _ uint32) error {
				_, _, err := mgr.ReconcileSessions(context.Background(), nil)
				return err
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				var callbackCalls atomic.Int32
				var sess *Session
				factory := func() (*SenderLease, error) {
					return NewSenderLease(senderLeaseTestSender{}, func() error {
						callbackCalls.Add(1)
						mgr.Close()
						return nil
					}), nil
				}
				var err error
				if tt.name == "ReconcileSessions" {
					_, _, err = mgr.ReconcileSessions(context.Background(), []ReconcileConfig{{
						Key: "config", SessionConfig: senderLeaseTestConfig("192.0.2.65"),
						SenderLeaseFactory: factory,
					}})
					if err == nil {
						snapshot := mgr.Sessions()
						if len(snapshot) != 1 {
							t.Fatalf("seed sessions = %d, want 1", len(snapshot))
						}
						sess, _ = mgr.LookupByDiscriminator(snapshot[0].LocalDiscr)
					}
				} else {
					sess, err = mgr.CreateSession(
						context.Background(), senderLeaseTestConfig("192.0.2.65"), factory,
					)
				}
				if err != nil {
					t.Fatalf("seed session: %v", err)
				}

				releaseDone := make(chan error, 1)
				go func() {
					releaseDone <- tt.release(mgr, sess.LocalDiscriminator())
				}()
				synctest.Wait()
				select {
				case releaseErr := <-releaseDone:
					if releaseErr != nil {
						t.Errorf("%s: %v", tt.name, releaseErr)
					}
				default:
					t.Errorf("%s sender release callback deadlocked on reentrant Close", tt.name)
				}
				if got := callbackCalls.Load(); got != 1 {
					t.Errorf("sender release callbacks = %d, want 1", got)
				}
			})
		})
	}
}

func TestManagerDetachedSessionCleanupPreservesResourceDiscriminatorMetricsOrder(t *testing.T) {
	events := make(chan string, 2)
	metrics := &lifecycleOrderMetrics{events: events}
	mgr := NewManager(slog.New(slog.DiscardHandler), WithManagerMetrics(metrics))
	metrics.manager = mgr
	sess, err := mgr.CreateSession(
		context.Background(),
		senderLeaseTestConfig("192.0.2.66"),
		func() (*SenderLease, error) {
			return NewSenderLease(senderLeaseTestSender{}, func() error {
				if mgr.discriminators.IsAllocated(metrics.discr) {
					events <- "resource-before-discriminator-release"
				} else {
					events <- "resource-after-discriminator-release"
				}
				return nil
			}), nil
		},
	)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	metrics.discr = sess.LocalDiscriminator()

	if err := mgr.DestroySession(context.Background(), metrics.discr); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	mgr.Close()

	if got := <-events; got != "resource-before-discriminator-release" {
		t.Errorf("first cleanup event = %q, want resource release", got)
	}
	if got := <-events; got != "metrics-after-discriminator-release" {
		t.Errorf("second cleanup event = %q, want metrics after discriminator release", got)
	}
}

func TestManagerStateChangesReachesEOFAfterDispatchStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
		go mgr.RunDispatch(dispatchCtx)
		synctest.Wait()

		cancelDispatch()
		synctest.Wait()

		select {
		case _, ok := <-mgr.StateChanges():
			if ok {
				t.Error("StateChanges returned an event after dispatch stopped")
			}
		default:
			t.Error("StateChanges remained open after dispatch stopped")
		}
		mgr.Close()
	})
}

type lifecycleOperationResult struct {
	created   int
	destroyed int
	err       error
	ch        <-chan StateChange
}

func TestManagerClosingRejectsNewClaimsAndSubscriptions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := &UnsolicitedPolicy{
			Enabled:     true,
			MaxSessions: 2,
			Interfaces: map[string]UnsolicitedInterfaceConfig{
				"eth0": {Enabled: true},
			},
			SessionDefaults: UnsolicitedSessionDefaults{
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
		}
		mgr := NewManager(
			slog.New(slog.DiscardHandler),
			WithUnsolicitedPolicy(policy),
			WithUnsolicitedSender(senderLeaseTestSender{}),
		)
		releaseEntered := make(chan struct{})
		releaseCallback := make(chan struct{})
		_, err := mgr.CreateSession(
			context.Background(),
			senderLeaseTestConfig("192.0.2.70"),
			func() (*SenderLease, error) {
				return NewSenderLease(senderLeaseTestSender{}, func() error {
					close(releaseEntered)
					<-releaseCallback
					return nil
				}), nil
			},
		)
		if err != nil {
			t.Fatalf("seed CreateSession: %v", err)
		}

		closeDone := make(chan struct{})
		go func() {
			mgr.Close()
			close(closeDone)
		}()
		<-releaseEntered

		var senderOpens atomic.Int32
		factory := func() (*SenderLease, error) {
			senderOpens.Add(1)
			return NewSenderLease(senderLeaseTestSender{}, nil), context.Cause(t.Context())
		}
		createResult := make(chan lifecycleOperationResult, 1)
		go func() {
			_, createErr := mgr.CreateSession(
				context.Background(), senderLeaseTestConfig("192.0.2.71"), factory,
			)
			createResult <- lifecycleOperationResult{err: createErr}
		}()
		reconcileResult := make(chan lifecycleOperationResult, 1)
		go func() {
			created, destroyed, reconcileErr := mgr.ReconcileSessions(
				context.Background(),
				[]ReconcileConfig{{
					Key: "config", SessionConfig: senderLeaseTestConfig("192.0.2.72"),
					SenderLeaseFactory: factory,
				}},
			)
			reconcileResult <- lifecycleOperationResult{
				created: created, destroyed: destroyed, err: reconcileErr,
			}
		}()
		ownerResult := make(chan lifecycleOperationResult, 1)
		go func() {
			created, destroyed, reconcileErr := mgr.ReconcileSessionsForOwner(
				context.Background(),
				MicroBFDReconciliationOwner(),
				[]ReconcileConfig{{
					Key: "micro", SessionConfig: senderLeaseTestConfig("192.0.2.73"),
					SenderLeaseFactory: factory,
				}},
			)
			ownerResult <- lifecycleOperationResult{
				created: created, destroyed: destroyed, err: reconcileErr,
			}
		}()
		unsolicitedResult := make(chan lifecycleOperationResult, 1)
		go func() {
			unsolicitedErr := mgr.tryCreateUnsolicited(
				ownershipDownPacket(700),
				PacketMeta{
					SrcAddr: netip.MustParseAddr("192.0.2.74"),
					DstAddr: netip.MustParseAddr("192.0.2.254"),
					IfName:  "eth0",
					TTL:     255,
				},
				nil,
			)
			unsolicitedResult <- lifecycleOperationResult{err: unsolicitedErr}
		}()
		subscribeResult := make(chan lifecycleOperationResult, 1)
		go func() {
			ch, subscribeErr := mgr.SubscribeStateChanges(context.Background())
			subscribeResult <- lifecycleOperationResult{ch: ch, err: subscribeErr}
		}()

		synctest.Wait()
		for name, resultCh := range map[string]<-chan lifecycleOperationResult{
			"CreateSession":                createResult,
			"ReconcileSessions":            reconcileResult,
			"ReconcileSessionsForOwner":    ownerResult,
			"unsolicited session creation": unsolicitedResult,
			"SubscribeStateChanges":        subscribeResult,
		} {
			select {
			case result := <-resultCh:
				if !errors.Is(result.err, ErrManagerClosing) {
					t.Errorf("%s error = %v, want ErrManagerClosing", name, result.err)
				}
				if result.created != 0 || result.destroyed != 0 {
					t.Errorf("%s mutated counts = (%d, %d), want (0, 0)",
						name, result.created, result.destroyed)
				}
				if result.ch != nil {
					t.Errorf("%s channel is non-nil after Closing", name)
				}
			default:
				t.Errorf("%s blocked instead of rejecting Closing", name)
			}
		}
		if got := senderOpens.Load(); got != 0 {
			t.Errorf("sender factories opened after Closing = %d, want 0", got)
		}

		close(releaseCallback)
		synctest.Wait()
		<-closeDone
		mgr.Close()
	})
}

func TestManagerConcurrentCloseIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var releases atomic.Int32
		releaseEntered := make(chan struct{})
		releaseCallback := make(chan struct{})
		mgr := NewManager(slog.New(slog.DiscardHandler))
		_, err := mgr.CreateSession(
			context.Background(),
			senderLeaseTestConfig("192.0.2.80"),
			func() (*SenderLease, error) {
				return NewSenderLease(senderLeaseTestSender{}, func() error {
					releases.Add(1)
					close(releaseEntered)
					<-releaseCallback
					return nil
				}), nil
			},
		)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		const closers = 4
		closeResults := make(chan struct{}, closers)
		go func() {
			mgr.Close()
			closeResults <- struct{}{}
		}()
		<-releaseEntered
		for range closers - 1 {
			go func() {
				mgr.Close()
				closeResults <- struct{}{}
			}()
		}
		synctest.Wait()
		if got := len(closeResults); got != 0 {
			t.Errorf("Close calls returned before shutdown completed = %d, want 0", got)
		}

		close(releaseCallback)
		synctest.Wait()
		if got := len(closeResults); got != closers {
			t.Errorf("completed Close calls = %d, want %d", got, closers)
		}
		if got := releases.Load(); got != 1 {
			t.Errorf("sender release callbacks = %d, want 1", got)
		}
	})
}

func TestManagerRunDispatchIsSingleRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
		go mgr.RunDispatch(dispatchCtx)
		synctest.Wait()

		secondDone := make(chan struct{})
		go func() {
			mgr.RunDispatch(context.Background())
			close(secondDone)
		}()
		synctest.Wait()
		select {
		case <-secondDone:
		default:
			t.Error("second RunDispatch call did not return")
		}

		want := StateChange{LocalDiscr: 801, OldState: StateDown, NewState: StateInit}
		mgr.rawNotifyCh <- want
		synctest.Wait()
		select {
		case got, ok := <-mgr.StateChanges():
			if !ok {
				t.Fatal("second RunDispatch call closed active StateChanges channel")
			}
			if got != want {
				t.Errorf("StateChanges event = %+v, want %+v", got, want)
			}
		default:
			t.Error("active RunDispatch did not forward state change")
		}

		cancelDispatch()
		synctest.Wait()
		mgr.Close()
	})
}

func TestManagerSubscribeRejectsCanceledContext(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := mgr.SubscribeStateChanges(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SubscribeStateChanges error = %v, want context.Canceled", err)
	}
	if ch != nil {
		t.Fatal("SubscribeStateChanges returned a channel for canceled context")
	}
}

func TestManagerClosedRejectsCreateReconcileAndSubscribe(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	mgr.Close()

	if _, err := mgr.CreateSession(
		context.Background(),
		senderLeaseTestConfig("192.0.2.82"),
		NonOwningSenderLeaseFactory(senderLeaseTestSender{}),
	); !errors.Is(err, ErrManagerClosed) {
		t.Errorf("CreateSession error = %v, want ErrManagerClosed", err)
	}
	if created, destroyed, err := mgr.ReconcileSessions(context.Background(), nil); !errors.Is(
		err, ErrManagerClosed,
	) || created != 0 || destroyed != 0 {
		t.Errorf("ReconcileSessions = (%d, %d, %v), want (0, 0, ErrManagerClosed)",
			created, destroyed, err)
	}
	if ch, err := mgr.SubscribeStateChanges(context.Background()); !errors.Is(err, ErrManagerClosed) || ch != nil {
		t.Errorf("SubscribeStateChanges = (%v, %v), want (nil, ErrManagerClosed)", ch, err)
	}
}

func TestManagerCloseAndSubscriberCancellationCloseChannelOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
		go mgr.RunDispatch(dispatchCtx)
		synctest.Wait()

		subCtx, cancelSub := context.WithCancel(context.Background())
		sub, err := mgr.SubscribeStateChanges(subCtx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}

		mgr.subMu.Lock()
		mgr.rawNotifyCh <- StateChange{LocalDiscr: 901, OldState: StateDown, NewState: StateInit}
		closeDone := make(chan struct{})
		go func() {
			mgr.Close()
			close(closeDone)
		}()
		cancelSub()
		mgr.subMu.Unlock()
		synctest.Wait()

		select {
		case <-closeDone:
		default:
			t.Error("Manager.Close did not wait for subscriber shutdown")
		}
		var received int
		for range sub {
			received++
		}
		if received > 1 {
			t.Errorf("subscriber received %d events, want at most 1", received)
		}
		cancelDispatch()
	})
}

func TestManagerCloseWaitsForUnsolicitedCleanupWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := &UnsolicitedPolicy{
			Enabled:     true,
			MaxSessions: 1,
			Interfaces: map[string]UnsolicitedInterfaceConfig{
				"eth0": {Enabled: true},
			},
			SessionDefaults: UnsolicitedSessionDefaults{
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
		}
		mgr := NewManager(
			slog.New(slog.DiscardHandler),
			WithUnsolicitedPolicy(policy),
			WithUnsolicitedSender(senderLeaseTestSender{}),
		)
		cleanupEntered := make(chan struct{})
		finishCleanup := make(chan struct{})
		mgr.unsolicitedSenderFactory = func() (*SenderLease, error) {
			return NewSenderLease(senderLeaseTestSender{}, func() error {
				close(cleanupEntered)
				<-finishCleanup
				return nil
			}), nil
		}
		meta := PacketMeta{
			SrcAddr: netip.MustParseAddr("192.0.2.90"),
			DstAddr: netip.MustParseAddr("192.0.2.254"),
			IfName:  "eth0",
			TTL:     255,
		}
		if err := mgr.tryCreateUnsolicited(ownershipDownPacket(900), meta, nil); err != nil {
			t.Fatalf("tryCreateUnsolicited: %v", err)
		}
		snapshot := mgr.Sessions()
		if len(snapshot) != 1 {
			t.Fatalf("unsolicited sessions = %d, want 1", len(snapshot))
		}
		synctest.Wait()
		mgr.mu.RLock()
		mgr.sessions[snapshot[0].LocalDiscr].session.state.Store(uint32(StateDown))
		mgr.mu.RUnlock()

		mgr.scheduleUnsolicitedCleanup(context.Background(), StateChange{
			LocalDiscr: snapshot[0].LocalDiscr,
			NewState:   StateDown,
		})
		<-cleanupEntered
		closeDone := make(chan struct{})
		go func() {
			mgr.Close()
			close(closeDone)
		}()
		synctest.Wait()
		select {
		case <-closeDone:
			t.Error("Manager.Close returned before unsolicited cleanup worker exited")
		default:
		}

		close(finishCleanup)
		synctest.Wait()
		select {
		case <-closeDone:
		default:
			t.Error("Manager.Close did not return after unsolicited cleanup worker exited")
		}
	})
}
