package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

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
	mgr := NewManager(slog.New(slog.DiscardHandler))
	cfg := lifecycleMicroConfig("bond-linearized")
	mgr.mu.Lock()
	reconcileStarted := make(chan struct{})
	reconcileDone := make(chan error, 1)
	go func() {
		close(reconcileStarted)
		_, _, err := mgr.ReconcileMicroBFDGroups([]MicroBFDReconcileConfig{{
			Key: cfg.LAGInterface, Config: cfg,
		}})
		reconcileDone <- err
	}()
	<-reconcileStarted

	if !waitForLifecycleReader(mgr) {
		mgr.mu.Unlock()
		<-reconcileDone
		mgr.Close()
		t.Fatal("ReconcileMicroBFDGroups did not register one top-level lifecycle operation")
	}

	closeStarted := make(chan struct{})
	closeDone := make(chan struct{})
	go func() {
		close(closeStarted)
		mgr.Close()
		close(closeDone)
	}()
	<-closeStarted
	if !waitForLifecycleWriter(mgr) {
		mgr.mu.Unlock()
		t.Fatal("Manager.Close did not queue its lifecycle transition")
	}
	mgr.mu.Unlock()

	if err := <-reconcileDone; err != nil {
		t.Errorf("ReconcileMicroBFDGroups: %v", err)
	}
	<-closeDone
}

func TestManagerClosingRejectsEchoReconciliation(t *testing.T) {
	mgr, finishClose := newLifecycleClosingManager(t)
	defer finishClose()

	cfg := lifecycleEchoConfig()
	created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{{
		Key: "echo-closing", EchoSessionConfig: cfg, Sender: senderLeaseTestSender{},
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
	mgr := NewManager(slog.New(slog.DiscardHandler))
	cfg := lifecycleEchoConfig()
	mgr.mu.Lock()
	reconcileStarted := make(chan struct{})
	reconcileDone := make(chan error, 1)
	go func() {
		close(reconcileStarted)
		_, _, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{{
			Key: "echo-linearized", EchoSessionConfig: cfg, Sender: senderLeaseTestSender{},
		}})
		reconcileDone <- err
	}()
	<-reconcileStarted

	if !waitForLifecycleReader(mgr) {
		mgr.mu.Unlock()
		<-reconcileDone
		mgr.Close()
		t.Fatal("ReconcileEchoSessions did not register one top-level lifecycle operation")
	}

	closeStarted := make(chan struct{})
	closeDone := make(chan struct{})
	go func() {
		close(closeStarted)
		mgr.Close()
		close(closeDone)
	}()
	<-closeStarted
	if !waitForLifecycleWriter(mgr) {
		mgr.mu.Unlock()
		t.Fatal("Manager.Close did not queue its lifecycle transition")
	}
	mgr.mu.Unlock()

	if err := <-reconcileDone; err != nil {
		t.Errorf("ReconcileEchoSessions: %v", err)
	}
	<-closeDone
}

func waitForLifecycleReader(mgr *Manager) bool {
	for range 10_000 {
		if !mgr.lifecycleMu.TryLock() {
			return true
		}
		mgr.lifecycleMu.Unlock()
		runtime.Gosched()
	}
	return false
}

func waitForLifecycleWriter(mgr *Manager) bool {
	for range 10_000 {
		if !mgr.lifecycleMu.TryRLock() {
			return true
		}
		mgr.lifecycleMu.RUnlock()
		runtime.Gosched()
	}
	return false
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
