package bfd

import (
	"testing"
	"testing/synctest"
)

func TestRawLossRecoverySnapshotsCrossFamilyReplacement(t *testing.T) {
	for _, oldKind := range []SessionType{SessionTypeEcho, SessionTypeSingleHop} {
		t.Run(oldKind.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := newEchoLeaseManager()
				defer mgr.Close()
				cfg := echoLeaseConfig("192.0.2.86")
				var control *Session
				var discr uint32
				var err error
				if oldKind == SessionTypeEcho {
					discr, err = mgr.CreateEchoSession(t.Context(), cfg, echoLeaseSender{})
				} else {
					control, err = mgr.CreateSession(t.Context(), senderLeaseTestConfig("192.0.2.86"), ownershipSenderLeaseFactory())
					if err == nil {
						discr = control.LocalDiscriminator()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				sub, err := mgr.SubscribeStateChanges(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if control == nil {
					if _, recvErr := mgr.DemuxEcho(discr); recvErr != nil {
						t.Fatal(recvErr)
					}
				} else {
					pkt := control.buildControlPacket()
					pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, 99, discr
					if control.RecvPacket(&pkt) != DeliveryQueued {
						t.Fatal("old owner receive refused")
					}
				}
				synctest.Wait()
				var delayed StateChange
				select {
				case delayed = <-mgr.rawNotifyCh:
				default:
					t.Fatal("old owner published no Up")
				}
				fillRawLossQueue(t, mgr, sub)
				if control == nil {
					synctest.Sleep(3 * cfg.TxInterval)
					err = mgr.DestroyEchoSession(discr)
				} else {
					if control.SetPathDown() != DeliveryQueued {
						t.Fatal("old owner PathDown refused")
					}
					synctest.Wait()
					err = mgr.DestroySession(t.Context(), discr)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(mgr.publicLossWake) != 1 {
					t.Fatal("retirement lost the committed public failure hint")
				}
				deleted := <-sub
				if !deleted.Deleted || deleted.Ref != delayed.Ref {
					t.Fatalf("old owner deletion = %+v", deleted)
				}
				// Force the same discriminator through the accepted registration paths.
				if control != nil {
					es, createErr := NewEchoSession(cfg, discr, echoLeaseSender{}, mgr.rawNotifyCh, mgr.logger, withEchoDelivery(mgr))
					if createErr != nil {
						t.Fatal(createErr)
					}
					mgr.ownershipMu.Lock()
					mgr.registerEchoAndStart(t.Context(), es, NewSenderLease(echoLeaseSender{}, nil),
						canonicalEchoSessionKey(cfg), echoSessionSourceCompatibility)
					mgr.ownershipMu.Unlock()
				} else {
					ccfg := senderLeaseTestConfig("192.0.2.86")
					lease := NewSenderLease(echoLeaseSender{}, nil)
					replacement, createErr := NewSession(ccfg, discr, lease.Sender(), mgr.rawNotifyCh, mgr.logger,
						withManagerDelivery(mgr))
					if createErr != nil {
						t.Fatal(createErr)
					}
					key, keyErr := sessionKeyFromConfig(ccfg)
					if keyErr != nil {
						t.Fatal(keyErr)
					}
					effective, configErr := normalizeEffectiveSessionConfig(ccfg)
					if configErr != nil {
						t.Fatal(configErr)
					}
					if _, _, registerErr := mgr.registerAndStart(t.Context(), key, ccfg, effective,
						compatibilityAPISessionOwner(), false, discr, replacement, lease); registerErr != nil {
						t.Fatal(registerErr)
					}
				}
				added := <-sub
				go mgr.RunDispatch(t.Context())
				synctest.Wait()
				gap := takeRecoveryGap(t, sub)
				snaps, covered := mgr.WatchSessionsWithRevision()
				if len(snaps) != 1 || snaps[0].Ref != added.Ref || added.Ref == delayed.Ref ||
					snaps[0].Type == oldKind || snaps[0].State != StateDown ||
					snaps[0].ObservationVersion <= deleted.ObservationVersion || covered < gap.Revision {
					t.Fatalf("pending old-owner loss resnapshot did not contain only the replacement: %+v", snaps)
				}
				mgr.dispatchStateChange(t.Context(), delayed)
				if len(sub) != 0 || deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryStale) != notifyChSize+1 {
					t.Fatal("old owner's held raw Up crossed the replacement recovery boundary")
				}
			})
		})
	}
}
