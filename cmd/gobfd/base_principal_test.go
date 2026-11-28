package main

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestReceiveSenderLeaseFailureReleasesOnlyPreparedClaim(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                      string
		sourceClaim, partialLease, receiveFailure bool
	}{
		{"new listener", false, false, false},
		{"source listener", true, false, false},
		{"partial sender", false, true, false},
		{"source and partial sender", true, true, false},
		{"receive failure", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAPIListenerFixture(t)
			listener := singleHopListener("192.0.2.1", "eth1")
			key := listenerKeyOf(listener)
			if tc.sourceClaim {
				if err := fixture.owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{listener}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.receiveFailure {
				fixture.opener.fail["eth1"] = errInjectedSenderCreation
			}
			acquired, closed := 0, 0
			factory := receiveSenderLeaseFactory(fixture.owner, listener, func() (*bfd.SenderLease, error) {
				acquired++
				var partial *bfd.SenderLease
				if tc.partialLease {
					partial = bfd.NewSenderLease(candidateTestSender{}, func() error { closed++; return nil })
				}
				return partial, errInjectedSenderCreation
			})
			cfg := bfd.SessionConfig{
				PeerAddr: netip.MustParseAddr("192.0.2.9"), LocalAddr: listener.Addr, Interface: "eth1",
				Type: bfd.SessionTypeSingleHop, DesiredMinTxInterval: time.Second,
				RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
			}
			session, err := fixture.mgr.CreateSession(t.Context(), cfg, factory)
			if session != nil || !errors.Is(err, errInjectedSenderCreation) || len(fixture.mgr.Sessions()) != 0 {
				t.Fatalf("failed acquisition published a session: session=%v error=%v", session, err)
			}
			wantAcquire, wantClose := 1, 0
			if tc.receiveFailure {
				wantAcquire = 0
			}
			if tc.partialLease {
				wantClose = 1
			}
			if acquired != wantAcquire || closed != wantClose || fixture.openListener(t, key) != tc.sourceClaim {
				t.Fatalf("failed acquisition leaked or released another claim: acquired=%d closed=%d", acquired, closed)
			}
			fixture.owner.release(sourceClaim(sourceBase), nil)
			if fixture.openListener(t, key) {
				t.Fatal("last source release retained a receive claim after acquisition failure")
			}
		})
	}
}

func TestReceiveSenderLeaseCloseIsIdempotentAndPreservesSource(t *testing.T) {
	t.Parallel()
	fixture := newAPIListenerFixture(t)
	listener := singleHopListener("192.0.2.1", "eth1")
	key := listenerKeyOf(listener)
	if err := fixture.owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{listener}); err != nil {
		t.Fatal(err)
	}
	closeFailure := errors.New("sender close failed")
	closed := 0
	factory := receiveSenderLeaseFactory(fixture.owner, listener, func() (*bfd.SenderLease, error) {
		return bfd.NewSenderLease(candidateTestSender{}, func() error { closed++; return closeFailure }), nil
	})
	lease, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := lease.Close(); !errors.Is(closeErr, closeFailure) {
			t.Error(closeErr)
		}
	})
	for range 2 {
		if closeErr := lease.Close(); !errors.Is(closeErr, closeFailure) {
			t.Fatalf("close failure was lost: %v", closeErr)
		}
	}
	fixture.owner.mu.Lock()
	receiveClaims := fixture.owner.listeners[key].apiClaims
	fixture.owner.mu.Unlock()
	if closed != 1 || receiveClaims != 0 || !fixture.openListener(t, key) {
		t.Fatalf("sender release replayed or consumed source claim: closed=%d RXclaims=%d", closed, receiveClaims)
	}
	fixture.owner.release(sourceClaim(sourceBase), nil)
	if fixture.openListener(t, key) {
		t.Fatal("source release retained the closed sender's receive claim")
	}
}

func TestBasePrincipalSharedReceiveLifetime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                    string
		configFirst, multiHop, withdrawAPIFirst bool
	}{
		{"single/config-first/config-withdrawal", true, false, false},
		{"single/config-first/API-withdrawal", true, false, true},
		{"single/API-first/config-withdrawal", false, false, false},
		{"single/API-first/API-withdrawal", false, false, true},
		{"multi/config-first/config-withdrawal", true, true, false},
		{"multi/config-first/API-withdrawal", true, true, true},
		{"multi/API-first/config-withdrawal", false, true, false},
		{"multi/API-first/API-withdrawal", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAPIListenerFixture(t)
			cfg := config.DefaultConfig()
			cfg.Sessions = []config.SessionConfig{{
				Peer: "192.0.2.9", Local: "192.0.2.1", Interface: "eth1",
				DesiredMinTx: time.Second, RequiredMinRx: time.Second, DetectMult: 3,
			}}
			request := apiSessionRequest("eth1")
			if tc.multiHop {
				cfg.Sessions[0].Type, cfg.Sessions[0].Interface = "multi_hop", ""
				request.Type, request.InterfaceName = bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, ""
			}
			candidates, err := compileBaseSessionCandidates(cfg)
			if err != nil {
				t.Fatal(err)
			}
			listeners := sessionListenerConfigs(cfg)
			key := listenerKeyOf(listeners[0])
			if acquireErr := fixture.owner.acquire(sourceClaim(sourceBase), listeners); acquireErr != nil {
				t.Fatal(acquireErr)
			}
			factory := newNthFailureDeclarativeSenderFactory(0)
			claimConfig := func() {
				t.Helper()
				result := applyBaseSessionCandidates(
					t.Context(), candidates, fixture.mgr, factory, slog.New(slog.DiscardHandler), fixture.owner,
				)
				if reconcileErr := result.Err(); reconcileErr != nil {
					t.Fatal(reconcileErr)
				}
			}
			if tc.configFirst {
				claimConfig()
			}
			created, err := fixture.bfd.AddSession(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.configFirst {
				claimConfig()
			}
			claimConfig()
			before := fixture.mgr.Sessions()
			if len(before) != 1 || before[0].LocalDiscr != created.GetSession().GetLocalDiscriminator() {
				t.Fatal("config/API claims did not share the base wire")
			}
			fixture.owner.mu.Lock()
			receiveClaims := fixture.owner.listeners[key].apiClaims
			fixture.owner.mu.Unlock()
			if receiveClaims != 1 {
				t.Fatalf("physical RX claims=%d, want one despite repeated config", receiveClaims)
			}
			withdrawConfig := func() {
				t.Helper()
				result := applyBaseSessionCandidates(
					t.Context(), nil, fixture.mgr, factory, slog.New(slog.DiscardHandler), fixture.owner,
				)
				if reconcileErr := result.Err(); reconcileErr != nil {
					t.Fatal(reconcileErr)
				}
				fixture.owner.release(sourceClaim(sourceBase), nil)
			}
			withdrawAPI := func() {
				t.Helper()
				if _, deleteErr := fixture.bfd.DeleteSession(t.Context(), &bfdv1.DeleteSessionRequest{
					LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
				}); deleteErr != nil {
					t.Fatal(deleteErr)
				}
			}
			if tc.withdrawAPIFirst {
				withdrawAPI()
			} else {
				withdrawConfig()
			}
			after := fixture.mgr.Sessions()
			if len(after) != 1 || after[0].Ref != before[0].Ref || !fixture.openListener(t, key) {
				t.Fatal("first claim withdrawal lost the surviving owner's wire or receiver")
			}
			if tc.configFirst && (len(factory.open) != 1 || len(factory.closed) != 0) {
				t.Fatal("first withdrawal closed the retained physical sender")
			}
			if tc.withdrawAPIFirst {
				withdrawConfig()
			} else {
				withdrawAPI()
			}
			if len(fixture.mgr.Sessions()) != 0 || fixture.openListener(t, key) || len(factory.open) != 0 {
				t.Fatal("last base claim release leaked its wire or receive listener")
			}
			wantAcquisitions := 0
			if tc.configFirst {
				wantAcquisitions = 1
			}
			if factory.calls != wantAcquisitions || len(factory.closed) != wantAcquisitions {
				t.Fatalf("physical sender acquire=%d close=%d, want=%d",
					factory.calls, len(factory.closed), wantAcquisitions)
			}
		})
	}
}
