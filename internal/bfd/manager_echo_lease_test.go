package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type echoLeaseSender struct{}

func (echoLeaseSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

type echoLeaseCounter struct {
	opens  atomic.Int32
	closes atomic.Int32
}

func (c *echoLeaseCounter) factory(factoryErr error) SenderLeaseFactory {
	return func() (*SenderLease, error) {
		c.opens.Add(1)
		return NewSenderLease(echoLeaseSender{}, func() error {
			c.closes.Add(1)
			return nil
		}), factoryErr
	}
}

func echoLeaseConfig(peer string) EchoSessionConfig {
	return EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr(peer),
		LocalAddr:        netip.MustParseAddr("192.0.2.1"),
		Interface:        "eth0",
		TxInterval:       time.Hour,
		DetectMultiplier: 3,
	}
}

func newEchoLeaseManager() *Manager {
	return NewManager(slog.New(slog.DiscardHandler))
}

func TestManagerReconcileEchoSessionsUnchangedDoesNotOpenLease(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()

	counter := &echoLeaseCounter{}
	desired := []EchoReconcileConfig{{
		Key:                "untrusted-first-key",
		EchoSessionConfig:  echoLeaseConfig("192.0.2.10"),
		SenderLeaseFactory: counter.factory(nil),
	}}
	if created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), desired); err != nil {
		t.Fatalf("first ReconcileEchoSessions: %v", err)
	} else if created != 1 || destroyed != 0 {
		t.Fatalf("first ReconcileEchoSessions = (%d, %d), want (1, 0)", created, destroyed)
	}

	desired[0].Key = "different-untrusted-key"
	if created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), desired); err != nil {
		t.Fatalf("unchanged ReconcileEchoSessions: %v", err)
	} else if created != 0 || destroyed != 0 {
		t.Fatalf("unchanged ReconcileEchoSessions = (%d, %d), want (0, 0)", created, destroyed)
	}
	if got := counter.opens.Load(); got != 1 {
		t.Errorf("sender lease opens = %d, want 1", got)
	}
}

func TestManagerReconcileEchoSessionsNthFailureRollsBackNewLeases(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()

	factoryErr := errors.New("third sender unavailable")
	first := &echoLeaseCounter{}
	second := &echoLeaseCounter{}
	third := &echoLeaseCounter{}
	fourth := &echoLeaseCounter{}
	desired := []EchoReconcileConfig{
		{EchoSessionConfig: echoLeaseConfig("192.0.2.11"), SenderLeaseFactory: first.factory(nil)},
		{EchoSessionConfig: echoLeaseConfig("192.0.2.12"), SenderLeaseFactory: second.factory(nil)},
		{EchoSessionConfig: echoLeaseConfig("192.0.2.13"), SenderLeaseFactory: third.factory(factoryErr)},
		{EchoSessionConfig: echoLeaseConfig("192.0.2.14"), SenderLeaseFactory: fourth.factory(nil)},
	}
	created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), desired)
	if !errors.Is(err, factoryErr) {
		t.Fatalf("ReconcileEchoSessions error = %v, want %v", err, factoryErr)
	}
	if created != 0 || destroyed != 0 {
		t.Errorf("ReconcileEchoSessions = (%d, %d), want (0, 0)", created, destroyed)
	}
	if got := len(mgr.EchoSessions()); got != 0 {
		t.Errorf("echo sessions after rollback = %d, want 0", got)
	}
	for name, counter := range map[string]*echoLeaseCounter{
		"first": first, "second": second, "third": third,
	} {
		if got := counter.opens.Load(); got != 1 {
			t.Errorf("%s sender opens = %d, want 1", name, got)
		}
		if got := counter.closes.Load(); got != 1 {
			t.Errorf("%s sender closes = %d, want 1", name, got)
		}
	}
	if got := fourth.opens.Load(); got != 0 {
		t.Errorf("fourth sender opens = %d, want 0", got)
	}
}

func TestManagerReconcileEchoSessionsInvalidCandidateDoesNotOpenOrMutate(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()

	existing := &echoLeaseCounter{}
	existingConfig := echoLeaseConfig("192.0.2.20")
	if _, _, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{{
		EchoSessionConfig: existingConfig, SenderLeaseFactory: existing.factory(nil),
	}}); err != nil {
		t.Fatalf("seed ReconcileEchoSessions: %v", err)
	}

	candidate := &echoLeaseCounter{}
	invalid := echoLeaseConfig("192.0.2.22")
	invalid.TxInterval = 0
	_, _, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{
		{EchoSessionConfig: echoLeaseConfig("192.0.2.21"), SenderLeaseFactory: candidate.factory(nil)},
		{EchoSessionConfig: invalid, SenderLeaseFactory: candidate.factory(nil)},
	})
	if !errors.Is(err, ErrInvalidEchoTxInterval) {
		t.Fatalf("ReconcileEchoSessions error = %v, want ErrInvalidEchoTxInterval", err)
	}
	if got := candidate.opens.Load(); got != 0 {
		t.Errorf("candidate sender opens = %d, want 0", got)
	}
	sessions := mgr.EchoSessions()
	if len(sessions) != 1 || sessions[0].PeerAddr != existingConfig.PeerAddr {
		t.Errorf("echo sessions after invalid candidate = %+v, want only %s", sessions, existingConfig.PeerAddr)
	}
}

func TestManagerReconcileEchoSessionsEmptyRemovesOnlyDeclarativeLease(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()

	apiConfig := echoLeaseConfig("192.0.2.30")
	if _, err := mgr.CreateEchoSession(context.Background(), apiConfig, echoLeaseSender{}); err != nil {
		t.Fatalf("CreateEchoSession: %v", err)
	}
	declarative := &echoLeaseCounter{}
	if _, _, err := mgr.ReconcileEchoSessions(context.Background(), []EchoReconcileConfig{{
		EchoSessionConfig:  echoLeaseConfig("192.0.2.31"),
		SenderLeaseFactory: declarative.factory(nil),
	}}); err != nil {
		t.Fatalf("seed ReconcileEchoSessions: %v", err)
	}

	created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty ReconcileEchoSessions: %v", err)
	}
	if created != 0 || destroyed != 1 {
		t.Errorf("empty ReconcileEchoSessions = (%d, %d), want (0, 1)", created, destroyed)
	}
	if got := declarative.closes.Load(); got != 1 {
		t.Errorf("declarative sender closes = %d, want 1", got)
	}
	sessions := mgr.EchoSessions()
	if len(sessions) != 1 || sessions[0].PeerAddr != apiConfig.PeerAddr {
		t.Errorf("echo sessions after empty desired = %+v, want only API peer %s", sessions, apiConfig.PeerAddr)
	}

	mgr.Close()
	if got := declarative.closes.Load(); got != 1 {
		t.Errorf("declarative sender closes after Manager.Close = %d, want 1", got)
	}
}

func TestManagerEchoLeaseDestroyAndCloseReleaseExactlyOnce(t *testing.T) {
	t.Run("destroy", func(t *testing.T) {
		mgr := newEchoLeaseManager()
		counter := &echoLeaseCounter{}
		discr, err := mgr.CreateEchoSessionWithSenderLease(
			context.Background(), echoLeaseConfig("192.0.2.40"), counter.factory(nil),
		)
		if err != nil {
			t.Fatalf("CreateEchoSessionWithSenderLease: %v", err)
		}
		if err := mgr.DestroyEchoSession(discr); err != nil {
			t.Fatalf("DestroyEchoSession: %v", err)
		}
		mgr.Close()
		if got := counter.closes.Load(); got != 1 {
			t.Errorf("sender closes = %d, want 1", got)
		}
	})

	t.Run("close", func(t *testing.T) {
		mgr := newEchoLeaseManager()
		counter := &echoLeaseCounter{}
		if _, err := mgr.CreateEchoSessionWithSenderLease(
			context.Background(), echoLeaseConfig("192.0.2.41"), counter.factory(nil),
		); err != nil {
			t.Fatalf("CreateEchoSessionWithSenderLease: %v", err)
		}
		mgr.Close()
		mgr.Close()
		if got := counter.closes.Load(); got != 1 {
			t.Errorf("sender closes = %d, want 1", got)
		}
	})
}

func TestManagerDestroyEchoSessionKeepsOperationActiveThroughLeaseCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		callbackEntered := make(chan struct{})
		callbackRelease := make(chan struct{})
		var ownershipUnlocked atomic.Bool
		var lifecycleMutationUnlocked atomic.Bool
		leaseFactory := func() (*SenderLease, error) {
			return NewSenderLease(echoLeaseSender{}, func() error {
				ownershipUnlocked.Store(mgr.ownershipMu.TryLock())
				if ownershipUnlocked.Load() {
					mgr.ownershipMu.Unlock()
				}
				lifecycleMutationUnlocked.Store(mgr.lifecycleMu.TryLock())
				if lifecycleMutationUnlocked.Load() {
					mgr.lifecycleMu.Unlock()
				}
				_ = mgr.EchoSessions()
				close(callbackEntered)
				<-callbackRelease
				return nil
			}), nil
		}
		discr, err := mgr.CreateEchoSessionWithSenderLease(
			context.Background(), echoLeaseConfig("192.0.2.50"), leaseFactory,
		)
		if err != nil {
			t.Fatalf("CreateEchoSessionWithSenderLease: %v", err)
		}

		destroyDone := make(chan error, 1)
		go func() { destroyDone <- mgr.DestroyEchoSession(discr) }()
		<-callbackEntered
		if !ownershipUnlocked.Load() {
			t.Error("sender release callback could not reacquire ownershipMu")
		}
		if !lifecycleMutationUnlocked.Load() {
			t.Error("sender release callback ran while lifecycle mutation lock was held")
		}

		closeDone := make(chan struct{})
		go func() {
			mgr.Close()
			close(closeDone)
		}()
		synctest.Wait()
		select {
		case <-closeDone:
			t.Fatal("Manager.Close returned before active echo cleanup callback completed")
		default:
		}
		close(callbackRelease)
		synctest.Wait()
		if err := <-destroyDone; err != nil {
			t.Fatalf("DestroyEchoSession: %v", err)
		}
		<-closeDone
	})
}
