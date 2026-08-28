package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type lifecycleLogBarrier struct {
	target  string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *lifecycleLogBarrier) Enabled(context.Context, slog.Level) bool {
	return true
}

func (b *lifecycleLogBarrier) Handle(_ context.Context, record slog.Record) error {
	if record.Message == b.target {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return nil
}

func (b *lifecycleLogBarrier) WithAttrs([]slog.Attr) slog.Handler {
	return b
}

func (b *lifecycleLogBarrier) WithGroup(string) slog.Handler {
	return b
}

func lifecycleMicroConfig(lag string) MicroBFDConfig {
	return MicroBFDConfig{
		LAGInterface:          lag,
		MemberLinks:           []string{"eth0", "eth1"},
		PeerAddr:              netip.MustParseAddr("192.0.2.101"),
		LocalAddr:             netip.MustParseAddr("192.0.2.102"),
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
		MinActiveLinks:        1,
	}
}

func lifecycleEchoConfig() EchoSessionConfig {
	return EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("192.0.2.111"),
		LocalAddr:        netip.MustParseAddr("192.0.2.112"),
		Interface:        "eth0",
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}
}

func newLifecycleClosingManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	releaseEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	closeDone := make(chan struct{})
	mgr := NewManager(
		slog.New(slog.DiscardHandler),
		WithUnsolicitedSenderLease(NewSenderLease(senderLeaseTestSender{}, func() error {
			close(releaseEntered)
			<-releaseCallback
			return nil
		})),
	)
	go func() {
		mgr.Close()
		close(closeDone)
	}()
	<-releaseEntered
	return mgr, func() {
		close(releaseCallback)
		<-closeDone
	}
}

func TestManagerClosingRejectsMicroBFDGroupMutations(t *testing.T) {
	mgr, finishClose := newLifecycleClosingManager(t)
	defer finishClose()

	cfg := lifecycleMicroConfig("bond-closing")
	if _, err := mgr.CreateMicroBFDGroup(cfg); !errors.Is(err, ErrManagerClosing) {
		t.Errorf("CreateMicroBFDGroup error = %v, want ErrManagerClosing", err)
	}
	if err := mgr.DestroyMicroBFDGroup(cfg.LAGInterface); !errors.Is(err, ErrManagerClosing) {
		t.Errorf("DestroyMicroBFDGroup error = %v, want ErrManagerClosing", err)
	}
	created, destroyed, err := mgr.ReconcileMicroBFDGroups([]MicroBFDReconcileConfig{{
		Key: cfg.LAGInterface, Config: cfg,
	}})
	if !errors.Is(err, ErrManagerClosing) || created != 0 || destroyed != 0 {
		t.Errorf("ReconcileMicroBFDGroups = (%d, %d, %v), want (0, 0, ErrManagerClosing)",
			created, destroyed, err)
	}
	if got := len(mgr.MicroBFDGroups()); got != 0 {
		t.Errorf("micro-BFD groups after Closing mutations = %d, want 0", got)
	}
}

func TestManagerReconcileMicroBFDGroupsIsOneLifecycleOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		barrier := &lifecycleLogBarrier{
			target:  "reconcile: creating new micro-BFD group",
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		mgr := NewManager(slog.New(barrier))
		cfg := lifecycleMicroConfig("bond-linearized")
		reconcileDone := make(chan error, 1)
		go func() {
			_, _, err := mgr.ReconcileMicroBFDGroups([]MicroBFDReconcileConfig{{
				Key: cfg.LAGInterface, Config: cfg,
			}})
			reconcileDone <- err
		}()
		<-barrier.entered

		if mgr.lifecycleMu.TryLock() {
			mgr.lifecycleMu.Unlock()
			close(barrier.release)
			<-reconcileDone
			mgr.Close()
			t.Fatal("Micro-BFD reconciliation did not register a top-level lifecycle operation")
		}
		mgr.lifecycleState = managerClosing
		close(barrier.release)
		synctest.Wait()
		if err := <-reconcileDone; err != nil {
			t.Errorf("ReconcileMicroBFDGroups: %v", err)
		}
		mgr.lifecycleState = managerOpen
		mgr.Close()
	})
}

func TestManagerMicroBFDGroupMutationsHoldOwnershipLock(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		barrier := newLifecycleLogBarrier("micro-BFD group created")
		mgr := NewManager(slog.New(barrier))
		defer mgr.Close()

		done := make(chan error, 1)
		go func() {
			_, err := mgr.CreateMicroBFDGroup(lifecycleMicroConfig("bond-create"))
			done <- err
		}()
		assertOwnershipLockedAtBarrier(t, mgr, barrier, done)
	})

	t.Run("destroy", func(t *testing.T) {
		barrier := newLifecycleLogBarrier("micro-BFD group destroyed")
		mgr := NewManager(slog.New(barrier))
		defer mgr.Close()
		cfg := lifecycleMicroConfig("bond-destroy")
		if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
			t.Fatalf("CreateMicroBFDGroup: %v", err)
		}

		done := make(chan error, 1)
		go func() {
			done <- mgr.DestroyMicroBFDGroup(cfg.LAGInterface)
		}()
		assertOwnershipLockedAtBarrier(t, mgr, barrier, done)
	})

	t.Run("reconcile", func(t *testing.T) {
		barrier := newLifecycleLogBarrier("reconcile: creating new micro-BFD group")
		mgr := NewManager(slog.New(barrier))
		defer mgr.Close()
		cfg := lifecycleMicroConfig("bond-reconcile")

		done := make(chan error, 1)
		go func() {
			_, _, err := mgr.ReconcileMicroBFDGroups([]MicroBFDReconcileConfig{{
				Key: cfg.LAGInterface, Config: cfg,
			}})
			done <- err
		}()
		assertOwnershipLockedAtBarrier(t, mgr, barrier, done)
	})
}

func newLifecycleLogBarrier(target string) *lifecycleLogBarrier {
	return &lifecycleLogBarrier{
		target:  target,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func assertOwnershipLockedAtBarrier(
	t *testing.T,
	mgr *Manager,
	barrier *lifecycleLogBarrier,
	done <-chan error,
) {
	t.Helper()
	<-barrier.entered
	if mgr.ownershipMu.TryLock() {
		mgr.ownershipMu.Unlock()
		close(barrier.release)
		<-done
		t.Fatal("Micro-BFD mutation did not hold ownershipMu")
	}
	close(barrier.release)
	if err := <-done; err != nil {
		t.Errorf("Micro-BFD mutation: %v", err)
	}
}

func TestManagerClosingRejectsEchoReconciliation(t *testing.T) {
	mgr, finishClose := newLifecycleClosingManager(t)
	defer finishClose()

	cfg := lifecycleEchoConfig()
	created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{{
		Key: "echo-closing", EchoSessionConfig: cfg,
		SenderLeaseFactory: NonOwningSenderLeaseFactory(senderLeaseTestSender{}),
	}})
	//nolint:errorlint // Require the direct lifecycle sentinel rather than a wrapped error.
	if err != ErrManagerClosing || created != 0 || destroyed != 0 {
		t.Errorf("ReconcileEchoSessions = (%d, %d, %v), want (0, 0, ErrManagerClosing)",
			created, destroyed, err)
	}
	if got := len(mgr.EchoSessions()); got != 0 {
		t.Errorf("echo sessions after Closing reconciliation = %d, want 0", got)
	}
}

func TestManagerReconcileEchoSessionsIsOneLifecycleOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		barrier := &lifecycleLogBarrier{
			target:  "reconcile: creating new echo session",
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		mgr := NewManager(slog.New(barrier))
		cfg := lifecycleEchoConfig()
		reconcileDone := make(chan error, 1)
		go func() {
			_, _, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{{
				Key: "echo-linearized", EchoSessionConfig: cfg,
				SenderLeaseFactory: NonOwningSenderLeaseFactory(senderLeaseTestSender{}),
			}})
			reconcileDone <- err
		}()
		<-barrier.entered

		if mgr.lifecycleMu.TryLock() {
			mgr.lifecycleMu.Unlock()
			close(barrier.release)
			<-reconcileDone
			mgr.Close()
			t.Fatal("echo reconciliation did not register a top-level lifecycle operation")
		}
		mgr.lifecycleState = managerClosing
		close(barrier.release)
		synctest.Wait()
		if err := <-reconcileDone; err != nil {
			t.Errorf("ReconcileEchoSessions: %v", err)
		}
		mgr.lifecycleState = managerOpen
		mgr.Close()
	})
}

func TestManagerCloseCallbackCanReenterManagerAPIs(t *testing.T) {
	var createErr error
	var snapshotCount int
	var mgr *Manager
	lease := NewSenderLease(senderLeaseTestSender{}, func() error {
		snapshotCount = len(mgr.Sessions())
		_, createErr = mgr.CreateSession(
			context.Background(),
			senderLeaseTestConfig("192.0.2.120"),
			NonOwningSenderLeaseFactory(senderLeaseTestSender{}),
		)
		return nil
	})
	mgr = NewManager(slog.New(slog.DiscardHandler), WithUnsolicitedSenderLease(lease))

	mgr.Close()

	if snapshotCount != 0 {
		t.Errorf("session snapshots during Closing callback = %d, want 0", snapshotCount)
	}
	if !errors.Is(createErr, ErrManagerClosing) {
		t.Errorf("CreateSession during Closing callback error = %v, want ErrManagerClosing", createErr)
	}
}
