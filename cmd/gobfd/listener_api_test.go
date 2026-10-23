package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	bfdv1connect "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// fakeAPISenderFactory stands in for udpSenderFactory behind the API.
type fakeAPISenderFactory struct {
	mu   sync.Mutex
	next uint16
	open map[uint16]struct{}
}

func (f *fakeAPISenderFactory) CreateSender(
	netip.Addr, string, bool, bool, *slog.Logger,
) (bfd.PacketSender, uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.open == nil {
		f.open = make(map[uint16]struct{})
	}
	f.next++
	port := 49000 + f.next
	f.open[port] = struct{}{}
	return candidateTestSender{}, port, nil
}

func (f *fakeAPISenderFactory) CloseSender(port uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.open, port)
	return nil
}

type apiListenerFixture struct {
	owner  *listenerOwner
	opener *fakeListenerOpener
	mgr    *bfd.Manager
	bfd    bfdv1connect.BfdServiceClient
	echo   bfdv1connect.EchoServiceClient
}

func newAPIListenerFixture(t *testing.T) *apiListenerFixture {
	t.Helper()
	coordinator, _ := newConvergedReadinessCoordinator(t)
	coordinator.readiness.setEchoEnabled(true)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth1", 1), upInterface("eth8", 8))
	owner, mgr := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	inner := &fakeAPISenderFactory{}
	logger := slog.New(slog.DiscardHandler)
	srv := newGRPCServer(config.GRPCConfig{UnsafeLoopbackMutations: true}, false, mgr,
		newAPIListenerSenderFactory(inner, owner, false), newAPIListenerSenderFactory(inner, owner, true),
		newDaemonHealthChecker(), nil, logger, nil)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, srv, ln)
	client := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+ln.Addr().String()))
	return &apiListenerFixture{
		owner: owner, opener: opener, mgr: mgr,
		bfd: bfdv1connect.NewBfdServiceClient(client), echo: bfdv1connect.NewEchoServiceClient(client),
	}
}

func apiSessionRequest(ifName string) *bfdv1.AddSessionRequest {
	return &bfdv1.AddSessionRequest{
		PeerAddress: "192.0.2.9", LocalAddress: "192.0.2.1", InterfaceName: ifName,
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(time.Second),
		RequiredMinRxInterval: durationpb.New(time.Second),
		DetectMultiplier:      3,
	}
}

func (f *apiListenerFixture) openListener(t *testing.T, key listenerKey) bool {
	t.Helper()
	f.owner.mu.Lock()
	_, open := f.owner.listeners[key]
	f.owner.mu.Unlock()
	return open && !fakeConnClosed(f.opener.conn(t, key))
}

// An API session created after a reload closed the config listener reopens
// it through its own claim, and its destruction releases that claim.
func TestAPISessionClaimsListenerAfterReloadClosedIt(t *testing.T) {
	t.Parallel()

	f := newAPIListenerFixture(t)
	cfg := singleHopListener("192.0.2.1", "eth1")
	key := listenerKeyOf(cfg)
	if err := f.owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	f.owner.release(sourceClaim(sourceBase), nil)
	if f.openListener(t, key) {
		t.Fatal("config release kept the listener without a claim")
	}

	created, err := f.bfd.AddSession(t.Context(), apiSessionRequest("eth1"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.openListener(t, key) {
		t.Fatal("API session was created without its receive listener")
	}
	// A reload that does not claim the listener keeps the API claim.
	f.owner.release(sourceClaim(sourceBase), nil)
	if !f.openListener(t, key) {
		t.Fatal("config release closed the listener of a live API session")
	}

	if _, err := f.bfd.DeleteSession(t.Context(), &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
	}); err != nil {
		t.Fatal(err)
	}
	if f.openListener(t, key) {
		t.Fatal("listener leaked after its last API claim was released")
	}
}

func TestAPISessionListenerFailureIsTypedAndCreatesNoSession(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"absent interface", fmt.Errorf("set SO_BINDTODEVICE(eth3): %w", syscall.ENODEV), connect.CodeUnavailable},
		{"unassigned address", fmt.Errorf("listen UDP: %w", syscall.EADDRNOTAVAIL), connect.CodeUnavailable},
		{"address in use", fmt.Errorf("listen UDP: %w", syscall.EADDRINUSE), connect.CodeFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAPIListenerFixture(t)
			f.opener.fail["eth3"] = tc.err
			_, err := f.bfd.AddSession(t.Context(), apiSessionRequest("eth3"))
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("AddSession error = %v (code %s), want code %s", err, got, tc.want)
			}
			if sessions := f.mgr.Sessions(); len(sessions) != 0 {
				t.Fatalf("sessions after listener failure = %+v, want none", sessions)
			}
		})
	}
}

// An API Echo session claims the per-address Echo listener the config
// opened, as config Echo peers share one listener per local address.
func TestAPIEchoSessionClaimsPerAddressListener(t *testing.T) {
	t.Parallel()

	f := newAPIListenerFixture(t)
	cfg := echoListener("192.0.2.5", "eth8")
	key := listenerKeyOf(cfg)
	if err := f.owner.acquire(sourceClaim(sourceEcho), []netio.ListenerConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	created, err := f.echo.AddEchoSession(t.Context(), &bfdv1.AddEchoSessionRequest{
		PeerAddress: "192.0.2.6", LocalAddress: "192.0.2.5", InterfaceName: "eth8",
		TxInterval: durationpb.New(time.Hour), DetectMultiplier: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.opener.openedKeys()); got != 1 {
		t.Fatalf("opened listeners = %d, want 1 (API reuses the per-address Echo listener)", got)
	}
	f.owner.release(sourceClaim(sourceEcho), nil)
	if !f.openListener(t, key) {
		t.Fatal("Echo release closed the listener of a live API Echo session")
	}
	if _, err := f.echo.DeleteEchoSession(t.Context(), &bfdv1.DeleteEchoSessionRequest{
		LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
	}); err != nil {
		t.Fatal(err)
	}
	if f.openListener(t, key) {
		t.Fatal("Echo listener leaked after its last claim was released")
	}
}

// A claim taken concurrently with a source release never observes a closed
// socket; run with -race.
func TestAPIListenerClaimRacesSourceRelease(t *testing.T) {
	t.Parallel()

	coordinator, _ := newConvergedReadinessCoordinator(t)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth1", 1))
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	cfg := singleHopListener("192.0.2.1", "eth1")
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	wg.Go(func() {
		for range rounds {
			if err := owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{cfg}); err != nil {
				errs <- err
				return
			}
			owner.release(sourceClaim(sourceBase), nil)
		}
	})
	wg.Go(func() {
		for range rounds {
			key, err := owner.claimAPI(cfg)
			if err != nil {
				errs <- err
				return
			}
			opener.mu.Lock()
			closed := fakeConnClosed(opener.conns[key])
			opener.mu.Unlock()
			if closed {
				errs <- errors.New("claimed listener socket is closed")
			}
			owner.releaseAPI(key)
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
