package bfd

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
)

func TestManagerDeletionPublishesWithoutDispatch(t *testing.T) {
	for _, removal := range []string{"api", "reconcile", "unsolicited"} {
		t.Run(removal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler),
					WithUnsolicitedPolicy(admissionPolicy(1)),
					WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
					WithUnsolicitedSessionSender(nonOwningSessionSender(ownershipNoopSender{})),
				)
				defer mgr.Close()
				ctx := context.Background()
				cfg := senderLeaseTestConfig("192.0.2.1")
				var remove func()
				switch removal {
				case "api":
					if _, err := mgr.CreateSession(ctx, cfg, ownershipSenderLeaseFactory()); err != nil {
						t.Fatalf("CreateSession: %v", err)
					}
					remove = func() {
						if err := mgr.DestroySession(ctx, mgr.Sessions()[0].LocalDiscr); err != nil {
							t.Fatalf("DestroySession: %v", err)
						}
					}
				case "reconcile":
					if _, _, err := mgr.ReconcileSessions(ctx, []ReconcileConfig{{
						Key: "configured", SessionConfig: cfg, SenderLeaseFactory: ownershipSenderLeaseFactory(),
					}}); err != nil {
						t.Fatalf("ReconcileSessions(create): %v", err)
					}
					remove = func() {
						if _, destroyed, err := mgr.ReconcileSessions(ctx, nil); err != nil || destroyed != 1 {
							t.Fatalf("ReconcileSessions(remove): destroyed=%d, err=%v", destroyed, err)
						}
					}
				case "unsolicited":
					if err := mgr.tryCreateUnsolicited(ownershipDownPacket(101),
						admissionMeta("192.0.2.1", "192.0.2.254"), nil); err != nil {
						t.Fatalf("tryCreateUnsolicited: %v", err)
					}
					remove = func() {
						session, _ := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
						mgr.cleanupUnsolicitedSession(ctx, session)
					}
				}
				snap := mgr.Sessions()[0]
				sess, _ := mgr.LookupByDiscriminator(snap.LocalDiscr)
				synctest.Wait()
				if removal == "unsolicited" {
					if !sess.SetPathDown() {
						t.Fatal("queue unsolicited owner failure")
					}
					synctest.Wait()
				} else {
					sess.mu.Lock()
					sess.localPair.Store(packStateDiag(StateDown, DiagNone))
					sess.observation.State = StateDown
					sess.mu.Unlock()
				}
				// Removal must not wait for the dispatcher or a free raw queue slot.
				for len(mgr.rawNotifyCh) < cap(mgr.rawNotifyCh) {
					sess.emitNotification(FSMResult{OldState: StateDown, NewState: StateDown}, sess.Observation())
				}
				subs := make([]<-chan StateChange, 2)
				for i := range subs {
					var err error
					subs[i], err = mgr.SubscribeStateChanges(ctx)
					if err != nil {
						t.Fatalf("SubscribeStateChanges: %v", err)
					}
				}
				remove()
				for i, sub := range subs {
					select {
					case sc := <-sub:
						if !sc.Deleted || sc.LocalDiscr != snap.LocalDiscr || sc.Revision == 0 ||
							sc.PeerAddr != snap.PeerAddr || sc.LocalAddr != snap.LocalAddr ||
							sc.Interface != snap.Interface || sc.Type != snap.Type ||
							sc.Status.ObservationVersion != sc.ObservationVersion || sc.Status.State != sc.NewState ||
							sc.Status.Ref != sc.Ref || sc.Status.DesiredMinTx != sess.DesiredMinTxInterval() {
							t.Fatalf("subscriber %d deletion = %+v, want identity %+v and revision", i, sc, snap)
						}
					default:
						t.Fatalf("subscriber %d received no deletion", i)
					}
				}
				if snapshots, revision := mgr.SessionsWithRevision(); len(snapshots) != 0 || revision == 0 {
					t.Fatalf("snapshot after deletion = %+v at %d, want empty revisioned snapshot", snapshots, revision)
				}
			})
		})
	}
}

func TestManagerDeletionKeepsSharedOwnerSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx := context.Background()
		cfg := senderLeaseTestConfig("192.0.2.1")
		alice, bob := APISessionOwner("alice"), APISessionOwner("bob")
		sess, err := mgr.CreateSessionAs(ctx, cfg, ownershipSenderLeaseFactory(), alice)
		if err != nil {
			t.Fatalf("CreateSessionAs(alice): %v", err)
		}
		if _, err = mgr.CreateSessionAs(ctx, cfg, ownershipSenderLeaseFactory(), bob); err != nil {
			t.Fatalf("CreateSessionAs(bob): %v", err)
		}
		registration := mgr.Revision()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		if err := mgr.DestroySessionAs(ctx, sess.LocalDiscriminator(), alice); err != nil {
			t.Fatalf("DestroySessionAs(alice): %v", err)
		}
		if mgr.Revision() != registration || len(mgr.Sessions()) != 1 || len(sub) != 0 {
			t.Fatal("shared claim release published deletion or removed the session")
		}
		if err := mgr.DestroySessionAs(ctx, sess.LocalDiscriminator(), bob); err != nil {
			t.Fatalf("DestroySessionAs(bob): %v", err)
		}
		if len(sub) != 1 || mgr.Revision() != registration+1 || len(mgr.Sessions()) != 0 {
			t.Fatal("last owner release did not publish one deletion")
		}
	})
}

func TestManagerDeletionAfterDispatchCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(context.Background())
		go mgr.RunDispatch(ctx)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if _, ok := <-mgr.StateChanges(); ok {
			t.Fatal("legacy channel remains open after dispatcher cancellation")
		}
		sess, err := mgr.CreateSession(context.Background(), senderLeaseTestConfig("192.0.2.1"),
			ownershipSenderLeaseFactory())
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		sub, err := mgr.SubscribeStateChanges(context.Background())
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		if err := mgr.DestroySession(context.Background(), sess.LocalDiscriminator()); err != nil {
			t.Fatalf("DestroySession: %v", err)
		}
		if len(sub) != 1 {
			t.Fatal("subscriber received no deletion after dispatcher cancellation")
		}
	})
}

func TestManagerDeletionSuppressesRetiredSessionNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		session := notificationTestSession(t, mgr)
		sub, err := mgr.SubscribeStateChanges(context.Background())
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		session.emitNotification(FSMResult{OldState: StateDown, NewState: StateDown}, session.Observation())
		if err = mgr.DestroySession(context.Background(), session.LocalDiscriminator()); err != nil {
			t.Fatalf("DestroySession: %v", err)
		}
		deletion := <-sub
		if !deletion.Deleted || deletion.ObservationVersion == 0 || deletion.Ref != session.Ref() {
			t.Fatalf("first event = %+v, want deletion", deletion)
		}

		// Register a real replacement with the same discriminator to exercise
		// queued identity checks without depending on random allocator reuse.
		cfg := senderLeaseTestConfig("192.0.2.1")
		lease := NewSenderLease(ownershipNoopSender{}, nil)
		replacement, err := NewSession(cfg, session.LocalDiscriminator(), lease.Sender(), mgr.rawNotifyCh, mgr.logger)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		// Registration must stamp independently of constructor initialization.
		replacement.observation.ObservationVersion = 0
		key, err := sessionKeyFromConfig(cfg)
		if err != nil {
			t.Fatalf("sessionKeyFromConfig: %v", err)
		}
		effective, err := normalizeEffectiveSessionConfig(cfg)
		if err != nil {
			t.Fatalf("normalizeEffectiveSessionConfig: %v", err)
		}
		if _, created, err := mgr.registerAndStart(context.Background(), key, cfg, effective,
			compatibilityAPISessionOwner(), false, session.LocalDiscriminator(), replacement, lease); err != nil || !created {
			t.Fatalf("registerAndStart: created=%t, err=%v", created, err)
		}
		replacementVersion := replacement.Observation().ObservationVersion
		if replacementVersion <= deletion.ObservationVersion || replacement.Ref() == session.Ref() {
			t.Fatalf("replacement v%d <= tombstone v%d or reused identity", replacementVersion, deletion.ObservationVersion)
		}
		added := <-sub
		if !added.Added || added.Ref != replacement.Ref() || added.Revision != deletion.Revision+1 {
			t.Fatalf("replacement registration = %+v", added)
		}
		// A delayed cleanup for the retired unsolicited target must also ignore
		// a replacement using that discriminator.
		mgr.mu.Lock()
		mgr.sessions[replacement.LocalDiscriminator()].unsolicited = true
		mgr.sessions[replacement.LocalDiscriminator()].owners = map[SessionOwner]struct{}{unsolicitedSessionOwner(): {}}
		mgr.mu.Unlock()
		mgr.cleanupUnsolicitedSession(context.Background(), session)
		if _, exists := mgr.LookupByDiscriminator(replacement.LocalDiscriminator()); !exists {
			t.Fatal("stale cleanup deleted the replacement session")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go mgr.RunDispatch(ctx)
		synctest.Wait()
		if len(sub) != 0 || mgr.Revision() != added.Revision {
			t.Fatal("retired state change was published after deletion")
		}
		replacement.emitNotification(FSMResult{OldState: StateDown, NewState: StateDown}, replacement.Observation())
		synctest.Wait()
		if got := <-sub; got.Deleted || got.Added || got.Revision != added.Revision+1 || got.Ref != replacement.Ref() {
			t.Fatalf("replacement event = %+v, want live replacement revision %d", got, added.Revision+1)
		}
	})
}

func TestManagerDeletionOverflowsToGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		session := notificationTestSession(t, mgr)
		registration := mgr.Revision()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		go mgr.RunDispatch(ctx)
		for range notifyChSize - 1 {
			session.emitNotification(FSMResult{OldState: StateDown, NewState: StateDown}, session.Observation())
			synctest.Wait()
		}
		if err := mgr.DestroySession(context.Background(), session.LocalDiscriminator()); err != nil {
			t.Fatalf("DestroySession: %v", err)
		}
		for range notifyChSize - 1 {
			<-sub
		}
		gap := <-sub
		if !gap.Gap || gap.Revision != registration+notifyChSize {
			t.Fatalf("last event = %+v, want deletion gap at %d", gap, registration+notifyChSize)
		}
		if snapshots, revision := mgr.SessionsWithRevision(); len(snapshots) != 0 || revision != gap.Revision {
			t.Fatalf("gap resync snapshot=%+v revision=%d, want empty snapshot at %d", snapshots, revision, gap.Revision)
		}
	})
}
