package bfd

import (
	"context"
	"log/slog"
	"net/netip"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

func unsolicitedPathFailure(t *testing.T, mgr *Manager, sess *Session) StateChange {
	t.Helper()
	if sess.SetPathDown() != DeliveryQueued {
		t.Fatal("owner did not admit PathDown")
	}
	synctest.Wait()
	var failure StateChange
	for len(mgr.rawNotifyCh) > 0 {
		sc := <-mgr.rawNotifyCh
		if sc.Ref == sess.Ref() && sc.NewState == StateDown && sc.Cause == CausePathDown {
			failure = sc
		}
	}
	if failure.unsolicitedAttempt == 0 {
		t.Fatal("owner published no committed PathDown failure")
	}
	return failure
}

func TestUnsolicitedSetupCleanupSurvivesFullRawQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := admissionPolicy(1)
		policy.CleanupTimeout = time.Second
		counter := &echoLeaseCounter{}
		factory := counter.factory(nil)
		mgr := NewManager(slog.New(slog.DiscardHandler),
			WithUnsolicitedPolicy(policy),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
			WithUnsolicitedSessionSender(func(PacketMeta) (*SenderLease, error) { return factory() }),
		)
		defer mgr.Close()
		pkt := ownershipDownPacket(101)
		pkt.DesiredMinTxInterval = 2_000_000
		outcome, err := mgr.tryCreateUnsolicited(pkt, admissionMeta("192.0.2.211", "192.0.2.254"), nil)
		if err != nil || outcome != DeliveryQueued {
			t.Fatalf("initial unsolicited admission = %s, %v", outcome, err)
		}
		synctest.Wait()
		snapshots := mgr.Sessions()
		if len(snapshots) != 1 {
			t.Fatalf("unsolicited sessions = %d, want 1", len(snapshots))
		}
		discr := snapshots[0].LocalDiscr
		sess, ok := mgr.LookupByDiscriminator(discr)
		if !ok || sess.State() != StateInit {
			t.Fatal("initial Down packet did not establish an Init owner")
		}
		for len(mgr.rawNotifyCh) > 0 {
			<-mgr.rawNotifyCh
		}
		// Foreign non-Down events cannot deliver this owner's cleanup signal.
		for range cap(mgr.rawNotifyCh) {
			mgr.rawNotifyCh <- StateChange{LocalDiscr: 0, NewState: StateUp}
		}
		for range 5 {
			synctest.Sleep(time.Second)
			if outcome := sess.RecvPacket(pkt); outcome != DeliveryQueued {
				t.Fatalf("refresh receive admission = %s, want queued", outcome)
			}
			synctest.Wait()
		}
		synctest.Sleep(time.Second)
		sess.mu.RLock()
		expired := sess.unsolicitedStopped && sess.unsolicitedDeadline.IsZero() && sess.unsolicitedAttempt == 1
		sess.mu.RUnlock()
		if !expired || sess.State() != StateInit || len(mgr.rawNotifyCh) != cap(mgr.rawNotifyCh) {
			t.Fatal("real setup expiry did not stop the Init attempt with its raw notification refused")
		}
		if mgr.unsolicited.sessionCount.Load() != 1 || counter.opens.Load() != 1 || counter.closes.Load() != 0 {
			t.Fatal("setup expiry released the claim or sender before cleanup retention")
		}
		before := sess.PacketsSent()
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		synctest.Sleep(time.Second - time.Nanosecond)
		if current, ok := mgr.LookupByDiscriminator(discr); !ok || current != sess || counter.closes.Load() != 0 {
			t.Fatal("cleanup did not retain the failed attempt for CleanupTimeout")
		}
		// This deadline is 7s; natural RX Detection Time cannot expire before 11s.
		synctest.Sleep(time.Nanosecond)
		if _, ok := mgr.LookupByDiscriminator(discr); ok {
			t.Error("lost private setup notification prevented cleanup without a fresh peer packet")
		}
		if quota, closes := mgr.unsolicited.sessionCount.Load(), counter.closes.Load(); quota != 0 || closes != 1 {
			t.Errorf("cleanup quota=%d sender closes=%d, want 0 and 1", quota, closes)
		}
		if mgr.discriminators.IsAllocated(discr) || sess.PacketsSent() != before {
			t.Error("cleanup retained its discriminator or resumed Control transmission")
		}
	})
}

func TestUnsolicitedCleanupReplacementAllocations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, _ := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		if sess.SetPathDown() != DeliveryQueued {
			t.Fatal("owner did not admit PathDown")
		}
		synctest.Wait()
		var failure StateChange
		for len(mgr.rawNotifyCh) > 0 {
			sc := <-mgr.rawNotifyCh
			if sc.Ref == sess.Ref() && sc.Cause == CausePathDown && sc.NewState == StateDown {
				failure = sc
			}
		}
		if failure.unsolicitedAttempt == 0 {
			t.Fatal("owner published no committed PathDown failure")
		}
		mgr.scheduleUnsolicitedCleanup(t.Context(), failure)
		synctest.Wait() // Initialize the first worker and timer before measuring replacements.
		ctx := t.Context()
		before := time.Now()
		allocs := testing.AllocsPerRun(100, func() {
			sess.mu.Lock()
			sess.unsolicitedAttempt++
			failure.unsolicitedAttempt = sess.unsolicitedAttempt
			sess.mu.Unlock()
			mgr.scheduleUnsolicitedCleanup(ctx, failure)
		})
		synctest.Wait()
		if !time.Now().Equal(before) {
			t.Fatal("allocation measurement advanced virtual time")
		}
		sess.mu.RLock()
		eligible := sess.observation.State == StateDown && sess.observation.Cause == CausePathDown &&
			sess.unsolicitedCleanupAllowedLocked(failure.unsolicitedAttempt)
		sess.mu.RUnlock()
		if !eligible {
			t.Fatal("replacement attempts did not preserve a committed cleanup-eligible failure")
		}
		if allocs != 0 {
			t.Fatalf("direct cleanup attempt replacement = %g allocs/op, want 0", allocs)
		}
	})
}

func TestUnsolicitedCleanupDuplicateDoesNotExtendRetention(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, _ := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		failure := unsolicitedPathFailure(t, mgr, sess)
		mgr.scheduleUnsolicitedCleanup(t.Context(), failure)
		synctest.Wait()
		synctest.Sleep(3 * time.Second)
		mgr.scheduleUnsolicitedCleanup(t.Context(), failure)
		synctest.Wait()
		synctest.Sleep(7*time.Second - time.Nanosecond)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); !ok {
			t.Fatal("duplicate cleanup shortened retention")
		}
		synctest.Sleep(time.Nanosecond)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); ok {
			t.Fatal("duplicate cleanup extended the original deadline")
		}
	})
}

func TestUnsolicitedCleanupParkedWorkerHandlesLaterFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		mgr.scheduleUnsolicitedCleanup(t.Context(), unsolicitedPathFailure(t, mgr, sess))
		synctest.Wait()
		for _, state := range []State{StateDown, StateInit} {
			pkt.State = state
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		if sess.State() != StateUp {
			t.Fatal("later attempt did not establish")
		}
		for range 3 {
			synctest.Sleep(3 * time.Second)
			pkt.State = StateUp
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		synctest.Sleep(time.Second)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); !ok {
			t.Fatal("obsolete failure removed the active attempt")
		}
		mgr.scheduleUnsolicitedCleanup(t.Context(), unsolicitedPathFailure(t, mgr, sess))
		synctest.Wait()
		synctest.Sleep(10 * time.Second)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); ok {
			t.Fatal("parked cleanup worker missed the later failure")
		}
	})
}

func TestUnsolicitedCleanupPreservesFences(t *testing.T) {
	for _, name := range []string{"AdminDown", "shared", "deleted", "canceled"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr, sess, _ := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
				defer mgr.Close()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				mgr.scheduleUnsolicitedCleanup(ctx, unsolicitedPathFailure(t, mgr, sess))
				synctest.Wait()
				switch name {
				case "AdminDown":
					if err := sess.SetAdminDown(t.Context()); err != nil {
						t.Fatal(err)
					}
				case "shared":
					cfg := unsolicitedSessionConfig(admissionMeta("192.0.2.211", "192.0.2.254"), mgr.unsolicited.policy)
					shared, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory())
					if err != nil || shared != sess {
						t.Fatalf("configured claim did not share the failed owner: %v", err)
					}
				case "deleted":
					mgr.cleanupUnsolicitedSession(t.Context(), sess)
				case "canceled":
					cancel()
				}
				synctest.Wait()
				synctest.Sleep(10 * time.Second)
				current, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator())
				if name == "deleted" {
					if ok || mgr.unsolicited.sessionCount.Load() != 0 {
						t.Fatal("deleted cleanup target survived or regained quota")
					}
				} else if !ok || current != sess {
					t.Fatal("cleanup ignored its administrative, ownership or cancellation fence")
				}
				if name == "shared" && mgr.unsolicited.sessionCount.Load() != 0 {
					t.Fatal("shared cleanup retained the private claim quota")
				}
			})
		})
	}
}

func TestUnsolicitedCleanupZeroAttemptTimeoutSurvivesRawLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := admissionPolicy(1)
		policy.CleanupTimeout = time.Second
		mgr := NewManager(slog.New(slog.DiscardHandler), WithUnsolicitedPolicy(policy),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
			WithUnsolicitedSessionSender(nonOwningSessionSender(ownershipNoopSender{})))
		defer mgr.Close()
		pkt := ownershipDownPacket(101)
		pkt.DesiredMinTxInterval = 0
		outcome, err := mgr.tryCreateUnsolicited(pkt, admissionMeta("192.0.2.211", "192.0.2.254"), nil)
		if err != nil || outcome != DeliveryQueued {
			t.Fatalf("zero-TX packet queue admission=%s err=%v", outcome, err)
		}
		synctest.Wait()
		sess, ok := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
		if !ok || sess.PacketsReceived() != 0 {
			t.Fatal("invalid first packet unexpectedly committed a receive")
		}
		for range cap(mgr.rawNotifyCh) {
			mgr.rawNotifyCh <- StateChange{NewState: StateUp}
		}
		synctest.Sleep(3 * time.Second)
		sess.mu.RLock()
		failed := sess.unsolicitedAttempt == 0 && sess.unsolicitedStopped &&
			sess.observation.State == StateDown && sess.observation.Cause == CauseDetectionTimeout
		sess.mu.RUnlock()
		if !failed || len(mgr.rawNotifyCh) != cap(mgr.rawNotifyCh) {
			t.Fatal("attempt-zero real DetectionTimeout did not commit while raw queue was full")
		}
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		synctest.Sleep(time.Second)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); ok || mgr.unsolicited.sessionCount.Load() != 0 {
			t.Fatal("attempt-zero committed timeout was excluded from raw-loss recovery")
		}
	})
}

func TestUnsolicitedCleanupScannerPreservesPendingFirstPacket(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := admissionPolicy(1)
		mgr := NewManager(slog.New(slog.DiscardHandler), WithUnsolicitedPolicy(policy),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")))
		defer mgr.Close()
		pkt, meta := ownershipDownPacket(101), admissionMeta("192.0.2.211", "192.0.2.254")
		if err := mgr.unsolicited.admit(pkt, meta, nil); err != nil {
			t.Fatal(err)
		}
		lease := NewSenderLease(ownershipNoopSender{}, nil)
		discr, sess, err := mgr.allocateAndBuild(unsolicitedSessionConfig(meta, policy), lease.Sender())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		entry := &sessionEntry{
			session: sess, senderLease: lease, cancel: cancel, done: make(chan struct{}),
			owners: map[SessionOwner]struct{}{unsolicitedSessionOwner(): {}}, unsolicited: true,
		}
		sess.senderLease = lease
		mgr.mu.Lock()
		mgr.sessions[discr] = entry
		entry.publishUnsolicitedOwnership()
		sess.mu.Lock()
		sess.processUnsolicitedLocked(time.Now())
		sess.mu.Unlock()
		mgr.mu.Unlock()
		start := make(chan struct{})
		mgr.workers.Go(func() {
			defer close(entry.done)
			select {
			case <-start:
			case <-ctx.Done():
			}
			sess.Run(ctx)
		})
		if sess.RecvPacket(pkt) != DeliveryQueued {
			t.Fatal("first valid packet was not queued")
		}
		mgr.scanUnsolicitedCleanup(t.Context())
		synctest.Wait()
		if _, ok := mgr.LookupByDiscriminator(discr); !ok || len(sess.recvCh) != 1 {
			t.Fatal("startup scan retired an attempt-zero initial claim before its valid queued receive")
		}
		close(start)
		synctest.Wait()
		if sess.PacketsReceived() != 1 || sess.State() != StateInit || mgr.unsolicited.sessionCount.Load() != 1 {
			t.Fatal("preserved initial claim did not process its first valid packet")
		}
	})
}

func TestUnsolicitedCleanupPreservesReplacementDuringDueRetirement(t *testing.T) {
	done, watchdogErr := realUnsolicitedWatchdog(t, 5*time.Second)
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		mgr.unsolicited.policy.CleanupTimeout = time.Second
		old := unsolicitedPathFailure(t, mgr, sess)
		mgr.scheduleUnsolicitedCleanup(t.Context(), old)
		synctest.Wait()
		mgr.ownershipMu.Lock()
		locked := true
		defer func() {
			if locked {
				mgr.ownershipMu.Unlock()
			}
		}()
		// Wait cannot quiesce a cleanup worker blocked on ownershipMu.
		time.Sleep(time.Second)
		for mgr.lifecycleMu.TryLock() {
			mgr.lifecycleMu.Unlock()
			select {
			case <-done:
				t.Fatal("due cleanup did not reach its lifecycle-protected retirement: ", watchdogErr())
			default:
				runtime.Gosched()
			}
		}
		heldAt := time.Now()
		receive := func(state State) StateChange {
			event, ok := awaitUnsolicitedObservation(done, mgr.rawNotifyCh, sess.Ref(), state)
			if !ok {
				t.Fatalf("owner did not publish committed %s while old retirement waited: %v",
					state, watchdogErr())
			}
			return event
		}
		pkt.State = StateDown
		if sess.RecvPacket(pkt) != DeliveryQueued {
			t.Fatal("new attempt was not admitted while old retirement waited")
		}
		init := receive(StateInit)
		if init.unsolicitedAttempt <= old.unsolicitedAttempt || init.ObservationVersion <= old.ObservationVersion {
			t.Fatal("new Init did not publish a fresh attempt and observation")
		}
		if sess.SetPathDown() != DeliveryQueued {
			t.Fatal("later attempt did not admit its failure")
		}
		later := receive(StateDown)
		if later.Cause != CausePathDown || later.unsolicitedAttempt != init.unsolicitedAttempt ||
			later.ObservationVersion <= init.ObservationVersion {
			t.Fatal("later failure was not owner-committed PathDown")
		}
		mgr.scheduleUnsolicitedCleanup(t.Context(), later)
		if !time.Now().Equal(heldAt) {
			t.Fatal("observation wait advanced fake time while old retirement waited")
		}
		mgr.ownershipMu.Unlock()
		locked = false
		synctest.Wait()
		synctest.Sleep(time.Second)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); ok ||
			mgr.unsolicited.sessionCount.Load() != 0 || mgr.discriminators.IsAllocated(sess.LocalDiscriminator()) {
			t.Fatal("completed obsolete retirement erased the later pending failure")
		}
	})
}
