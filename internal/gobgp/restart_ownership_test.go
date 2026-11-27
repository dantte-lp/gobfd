//go:build linux

package gobgp_test

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

type simulatedPeer struct {
	confAdminDown bool
	adminState    gobgpapi.PeerState_AdminState
	sessionState  gobgpapi.PeerState_SessionState
}

// simulatedGoBGPServer emulates GoBGP v4.10.0 GetBgp, ListPeer, DisablePeer,
// EnablePeer, and WatchEvent(PEER) over Unix or TCP sockets.
type simulatedGoBGPServer struct {
	gobgpapi.UnimplementedGoBgpServiceServer

	mu           sync.Mutex
	asn          uint32
	routerID     string
	peers        map[string]*simulatedPeer
	disableCount int
	enableCount  int
	watcherTotal int
	peerWatchers map[chan *gobgpapi.WatchEventResponse]struct{}
	blockDisable chan struct{}
}

func newSimulatedGoBGPServer() *simulatedGoBGPServer {
	return &simulatedGoBGPServer{
		asn:          65010,
		routerID:     "198.18.42.10",
		peers:        make(map[string]*simulatedPeer),
		peerWatchers: make(map[chan *gobgpapi.WatchEventResponse]struct{}),
	}
}

func (s *simulatedGoBGPServer) setPeer(addr string, admin gobgpapi.PeerState_AdminState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := gobgpapi.PeerState_SESSION_STATE_ESTABLISHED
	if admin != gobgpapi.PeerState_ADMIN_STATE_UP {
		sess = gobgpapi.PeerState_SESSION_STATE_IDLE
	}
	s.peers[addr] = &simulatedPeer{
		adminState:   admin,
		sessionState: sess,
	}
}

func (s *simulatedGoBGPServer) peerAdminState(addr string) gobgpapi.PeerState_AdminState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[addr]
	if p == nil {
		return gobgpapi.PeerState_ADMIN_STATE_UNSPECIFIED
	}
	return p.adminState
}

func (s *simulatedGoBGPServer) counts() (disables, enables int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disableCount, s.enableCount
}

func (s *simulatedGoBGPServer) operatorSetConfAdminDown(addr string, down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.peers[addr]; p != nil {
		p.confAdminDown = down
	}
}

func (s *simulatedGoBGPServer) operatorTransition(addr string, admin gobgpapi.PeerState_AdminState) {
	s.mu.Lock()
	p := s.peers[addr]
	if p != nil {
		p.adminState = admin
		if admin == gobgpapi.PeerState_ADMIN_STATE_UP {
			p.sessionState = gobgpapi.PeerState_SESSION_STATE_ESTABLISHED
		} else {
			p.sessionState = gobgpapi.PeerState_SESSION_STATE_IDLE
		}
	}
	watchers := make([]chan *gobgpapi.WatchEventResponse, 0, len(s.peerWatchers))
	for ch := range s.peerWatchers {
		watchers = append(watchers, ch)
	}
	s.mu.Unlock()

	ev := &gobgpapi.WatchEventResponse{
		Event: &gobgpapi.WatchEventResponse_Peer{
			Peer: &gobgpapi.WatchEventResponse_PeerEvent{
				Type: gobgpapi.WatchEventResponse_PeerEvent_TYPE_STATE,
				Peer: &gobgpapi.Peer{
					Conf: &gobgpapi.PeerConf{NeighborAddress: addr},
					State: &gobgpapi.PeerState{
						NeighborAddress: addr,
						AdminState:      admin,
					},
				},
			},
		},
	}
	for _, ch := range watchers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *simulatedGoBGPServer) GetBgp(context.Context, *gobgpapi.GetBgpRequest) (*gobgpapi.GetBgpResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &gobgpapi.GetBgpResponse{
		Global: &gobgpapi.Global{
			Asn:      s.asn,
			RouterId: s.routerID,
		},
	}, nil
}

func (s *simulatedGoBGPServer) ListPeer(
	req *gobgpapi.ListPeerRequest,
	stream grpc.ServerStreamingServer[gobgpapi.ListPeerResponse],
) error {
	s.mu.Lock()
	peers := make([]*gobgpapi.Peer, 0, len(s.peers))
	for addr, p := range s.peers {
		if req.GetAddress() != "" && req.GetAddress() != addr {
			continue
		}
		// Match GoBGP v4.10.0 server.go:1962-1989: Timers.State is zeroed on adminStateDown,
		// and Communication is omitted from ListPeer.
		peers = append(peers, &gobgpapi.Peer{
			Conf: &gobgpapi.PeerConf{
				NeighborAddress: addr,
				AdminDown:       p.confAdminDown,
			},
			State: &gobgpapi.PeerState{
				NeighborAddress: addr,
				AdminState:      p.adminState,
				SessionState:    p.sessionState,
			},
			Timers: &gobgpapi.Timers{
				State: &gobgpapi.TimersState{
					Uptime:   timestamppb.New(time.Unix(0, 0)),
					Downtime: timestamppb.New(time.Unix(0, 0)),
				},
			},
		})
	}
	s.mu.Unlock()

	for _, peer := range peers {
		if err := stream.Send(&gobgpapi.ListPeerResponse{Peer: peer}); err != nil {
			return err
		}
	}
	return nil
}

func (s *simulatedGoBGPServer) DisablePeer(
	ctx context.Context, req *gobgpapi.DisablePeerRequest,
) (*gobgpapi.DisablePeerResponse, error) {
	s.mu.Lock()
	block := s.blockDisable
	s.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-block:
		}
	}

	s.mu.Lock()
	if p := s.peers[req.GetAddress()]; p != nil {
		p.adminState = gobgpapi.PeerState_ADMIN_STATE_DOWN
		p.sessionState = gobgpapi.PeerState_SESSION_STATE_IDLE
	}
	s.disableCount++
	s.mu.Unlock()
	s.operatorTransition(req.GetAddress(), gobgpapi.PeerState_ADMIN_STATE_DOWN)
	return &gobgpapi.DisablePeerResponse{}, nil
}

func (s *simulatedGoBGPServer) EnablePeer(
	_ context.Context, req *gobgpapi.EnablePeerRequest,
) (*gobgpapi.EnablePeerResponse, error) {
	s.mu.Lock()
	if p := s.peers[req.GetAddress()]; p != nil {
		p.adminState = gobgpapi.PeerState_ADMIN_STATE_UP
		p.sessionState = gobgpapi.PeerState_SESSION_STATE_ESTABLISHED
	}
	s.enableCount++
	s.mu.Unlock()
	s.operatorTransition(req.GetAddress(), gobgpapi.PeerState_ADMIN_STATE_UP)
	return &gobgpapi.EnablePeerResponse{}, nil
}

func (s *simulatedGoBGPServer) WatchEvent(
	req *gobgpapi.WatchEventRequest,
	stream grpc.ServerStreamingServer[gobgpapi.WatchEventResponse],
) error {
	if req.GetPeer() == nil {
		<-stream.Context().Done()
		return nil
	}
	ch := make(chan *gobgpapi.WatchEventResponse, 16)
	s.mu.Lock()
	s.watcherTotal++
	s.peerWatchers[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peerWatchers, ch)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case ev := <-ch:
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

func serveUnixGoBGP(t *testing.T, sockPath string, svc *simulatedGoBGPServer) (stop func()) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sockPath, err)
	}
	server := grpc.NewServer()
	gobgpapi.RegisterGoBgpServiceServer(server, svc)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	stopped := false
	stopFn := func() {
		if stopped {
			return
		}
		stopped = true
		server.Stop()
		<-served
		_ = os.Remove(sockPath)
	}
	t.Cleanup(stopFn)
	return stopFn
}

func runHandlerInstance(
	t *testing.T,
	target string,
	stateFile string,
) (events chan<- bfd.StateChange, shutdown func()) {
	t.Helper()

	client, err := gobgp.NewGRPCClient(gobgp.GRPCClientConfig{Addr: target}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewGRPCClient(%s): %v", target, err)
	}
	store, recovered, err := gobgp.OpenOwnershipStore(stateFile)
	if err != nil {
		_ = client.Close()
		t.Fatalf("OpenOwnershipStore(%s): %v", stateFile, err)
	}
	handler, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:            client,
		Strategy:          gobgp.StrategyDisablePeer,
		ActionTimeout:     2 * time.Second,
		OwnershipStore:    store,
		RecoveredReceipts: recovered,
		Logger:            slog.New(slog.DiscardHandler),
	})
	if err != nil {
		_ = client.Close()
		t.Fatalf("NewHandler: %v", err)
	}

	ch := make(chan bfd.StateChange, 8)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, ch)
	}()

	cleaned := false
	stopInstance := func() {
		if cleaned {
			return
		}
		cleaned = true
		cancel()
		<-done
		_ = client.Close()
	}
	t.Cleanup(stopInstance)
	return ch, stopInstance
}

func waitCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

// Issue #131: GoBFD disables peer over Unix IPC -> controller crashes/restarts
// while gobgpd stays up -> BFD Up with a new discriminator recovers EnablePeer.
// Operator-disabled, Conf.AdminDown, restarted-speaker and TCP peers stay disabled.
func TestHandlerRecoversProvablyOwnedDisableAcrossRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "speaker.sock")
	stateFile := filepath.Join(dir, "state", "gobgp-ownership.json")
	svc := newSimulatedGoBGPServer()
	svc.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
	svc.setPeer("198.18.42.12", gobgpapi.PeerState_ADMIN_STATE_DOWN) // Operator-disabled beforehand.
	serveUnixGoBGP(t, sockPath, svc)
	target := "unix://" + sockPath

	// Controller generation 1: BFD Down on owned peer (.11) and operator-disabled peer (.12).
	events1, crashController1 := runHandlerInstance(t, target, stateFile)
	events1 <- bfd.StateChange{
		LocalDiscr: 2163669106,
		PeerAddr:   netip.MustParseAddr("198.18.42.11"),
		Type:       bfd.SessionTypeSingleHop,
		OldState:   bfd.StateUp,
		NewState:   bfd.StateDown,
		Diag:       bfd.DiagControlTimeExpired,
		Cause:      bfd.CauseDetectionTimeout,
		Timestamp:  time.Now(),
	}
	events1 <- bfd.StateChange{
		LocalDiscr: 2163669107,
		PeerAddr:   netip.MustParseAddr("198.18.42.12"),
		Type:       bfd.SessionTypeSingleHop,
		OldState:   bfd.StateUp,
		NewState:   bfd.StateDown,
		Diag:       bfd.DiagControlTimeExpired,
		Cause:      bfd.CauseDetectionTimeout,
		Timestamp:  time.Now(),
	}
	waitCondition(t, "DisablePeer receipt on 198.18.42.11", func() bool {
		disables, _ := svc.counts()
		_, loaded, loadErr := gobgp.OpenOwnershipStore(stateFile)
		return disables == 1 &&
			svc.peerAdminState("198.18.42.11") == gobgpapi.PeerState_ADMIN_STATE_DOWN &&
			loadErr == nil && len(loaded) == 1
	})

	// Crash controller 1 while fault is still active; gobgpd stays up with same PID & socket.
	crashController1()

	// Controller generation 2 starts and sees BFD Up with new local discriminators.
	events2, stopController2 := runHandlerInstance(t, target, stateFile)
	events2 <- bfd.StateChange{
		LocalDiscr: 296767000,
		PeerAddr:   netip.MustParseAddr("198.18.42.11"),
		Type:       bfd.SessionTypeSingleHop,
		OldState:   bfd.StateDown,
		NewState:   bfd.StateUp,
		Timestamp:  time.Now(),
	}
	events2 <- bfd.StateChange{
		LocalDiscr: 296767001,
		PeerAddr:   netip.MustParseAddr("198.18.42.12"),
		Type:       bfd.SessionTypeSingleHop,
		OldState:   bfd.StateDown,
		NewState:   bfd.StateUp,
		Timestamp:  time.Now(),
	}

	waitCondition(t, "EnablePeer recovery on 198.18.42.11", func() bool {
		_, enables := svc.counts()
		return enables == 1 && svc.peerAdminState("198.18.42.11") == gobgpapi.PeerState_ADMIN_STATE_UP
	})
	time.Sleep(50 * time.Millisecond)

	if got := svc.peerAdminState("198.18.42.12"); got != gobgpapi.PeerState_ADMIN_STATE_DOWN {
		t.Fatalf("operator-disabled peer 198.18.42.12 state = %v, want ADMIN_STATE_DOWN", got)
	}
	if _, enables := svc.counts(); enables != 1 {
		t.Fatalf("EnablePeer count = %d, want 1", enables)
	}
	stopController2()

	_, remaining, err := gobgp.OpenOwnershipStore(stateFile)
	if err != nil {
		t.Fatalf("OpenOwnershipStore after recovery: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining receipts after recovery = %v, want empty", remaining)
	}
}

func TestHandlerRefusesAmbiguousOrOperatorModifiedDisablesAfterRestart(t *testing.T) {
	t.Parallel()

	t.Run("static Conf.AdminDown prevents recovery", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		sockPath := filepath.Join(dir, "speaker.sock")
		stateFile := filepath.Join(dir, "gobgp-ownership.json")
		svc := newSimulatedGoBGPServer()
		svc.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
		serveUnixGoBGP(t, sockPath, svc)
		target := "unix://" + sockPath

		events1, stop1 := runHandlerInstance(t, target, stateFile)
		events1 <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateUp, NewState: bfd.StateDown,
			Diag: bfd.DiagControlTimeExpired, Cause: bfd.CauseDetectionTimeout,
		}
		waitCondition(t, "DisablePeer receipt persisted", func() bool {
			d, _ := svc.counts()
			_, loaded, loadErr := gobgp.OpenOwnershipStore(stateFile)
			return d == 1 && loadErr == nil && len(loaded) == 1
		})
		stop1()

		// Operator sets static admin-down = true while controller is stopped.
		svc.operatorSetConfAdminDown("198.18.42.11", true)

		events2, stop2 := runHandlerInstance(t, target, stateFile)
		events2 <- bfd.StateChange{
			LocalDiscr: 2, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateDown, NewState: bfd.StateUp,
		}
		time.Sleep(100 * time.Millisecond)
		stop2()

		if _, enables := svc.counts(); enables != 0 {
			t.Fatalf("EnablePeer called %d times on Conf.AdminDown=true peer, want 0", enables)
		}
	})

	t.Run("recreated speaker socket inode invalidates receipt", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		sockPath := filepath.Join(dir, "speaker.sock")
		stateFile := filepath.Join(dir, "gobgp-ownership.json")
		svc1 := newSimulatedGoBGPServer()
		svc1.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
		stopSpeaker1 := serveUnixGoBGP(t, sockPath, svc1)
		target := "unix://" + sockPath

		events1, stop1 := runHandlerInstance(t, target, stateFile)
		events1 <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateUp, NewState: bfd.StateDown,
			Diag: bfd.DiagControlTimeExpired, Cause: bfd.CauseDetectionTimeout,
		}
		waitCondition(t, "DisablePeer receipt persisted", func() bool {
			d, _ := svc1.counts()
			_, loaded, loadErr := gobgp.OpenOwnershipStore(stateFile)
			return d == 1 && loadErr == nil && len(loaded) == 1
		})
		stop1()
		info1, statErr := os.Stat(sockPath)
		if statErr != nil {
			t.Fatalf("stat initial speaker socket: %v", statErr)
		}
		retiredSock := sockPath + ".retired"
		if renameErr := os.Rename(sockPath, retiredSock); renameErr != nil {
			t.Fatalf("retain retired speaker socket inode: %v", renameErr)
		}
		t.Cleanup(func() { _ = os.Remove(retiredSock) })
		stopSpeaker1()

		// Simulated gobgpd restart creates a new socket inode with peer disabled by operator.
		svc2 := newSimulatedGoBGPServer()
		svc2.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_DOWN)
		serveUnixGoBGP(t, sockPath, svc2)
		info2, statErr := os.Stat(sockPath)
		if statErr != nil || os.SameFile(info1, info2) {
			t.Fatalf("recreated speaker socket reused inode or failed stat: err=%v", statErr)
		}

		events2, stop2 := runHandlerInstance(t, target, stateFile)
		events2 <- bfd.StateChange{
			LocalDiscr: 2, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateDown, NewState: bfd.StateUp,
		}
		time.Sleep(100 * time.Millisecond)
		stop2()

		if _, enables := svc2.counts(); enables != 0 {
			t.Fatalf("EnablePeer called %d times after gobgpd socket recreation, want 0", enables)
		}
	})

	t.Run("tcp target lacks speaker epoch proof across restart", func(t *testing.T) {
		t.Parallel()

		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen tcp: %v", err)
		}
		svc := newSimulatedGoBGPServer()
		svc.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
		server := grpc.NewServer()
		gobgpapi.RegisterGoBgpServiceServer(server, svc)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)

		stateFile := filepath.Join(t.TempDir(), "gobgp-ownership.json")
		target := listener.Addr().String()

		events1, stop1 := runHandlerInstance(t, target, stateFile)
		events1 <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateUp, NewState: bfd.StateDown,
			Diag: bfd.DiagControlTimeExpired, Cause: bfd.CauseDetectionTimeout,
		}
		waitCondition(t, "DisablePeer over TCP", func() bool {
			d, _ := svc.counts()
			return d == 1
		})
		stop1()

		events2, stop2 := runHandlerInstance(t, target, stateFile)
		events2 <- bfd.StateChange{
			LocalDiscr: 2, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateDown, NewState: bfd.StateUp,
		}
		time.Sleep(100 * time.Millisecond)
		stop2()

		if _, enables := svc.counts(); enables != 0 {
			t.Fatalf("EnablePeer called %d times over TCP after restart, want 0", enables)
		}
	})

	t.Run("external WatchEvent recovery revokes persisted receipt before operator re-disable", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		sockPath := filepath.Join(dir, "speaker.sock")
		stateFile := filepath.Join(dir, "gobgp-ownership.json")
		svc := newSimulatedGoBGPServer()
		svc.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
		serveUnixGoBGP(t, sockPath, svc)
		target := "unix://" + sockPath

		events1, stop1 := runHandlerInstance(t, target, stateFile)
		events1 <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateUp, NewState: bfd.StateDown,
			Diag: bfd.DiagControlTimeExpired, Cause: bfd.CauseDetectionTimeout,
		}
		waitCondition(t, "DisablePeer receipt persisted", func() bool {
			d, _ := svc.counts()
			_, loaded, loadErr := gobgp.OpenOwnershipStore(stateFile)
			return d == 1 && loadErr == nil && len(loaded) == 1
		})
		stop1()
		waitCondition(t, "controller 1 watcher disconnected", func() bool {
			svc.mu.Lock()
			defer svc.mu.Unlock()
			return len(svc.peerWatchers) == 0
		})

		events2, stop2 := runHandlerInstance(t, target, stateFile)
		// Wait until controller 2 subscribes to WatchEvent.
		waitCondition(t, "WatchEvent subscriber 2 registered", func() bool {
			svc.mu.Lock()
			defer svc.mu.Unlock()
			return svc.watcherTotal >= 2 && len(svc.peerWatchers) == 1
		})

		// Operator manually enables and then disables peer while BFD is still Down.
		svc.operatorTransition("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
		waitCondition(t, "receipt revoked on external enable", func() bool {
			_, loaded, err := gobgp.OpenOwnershipStore(stateFile)
			return err == nil && len(loaded) == 0
		})
		svc.operatorTransition("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_DOWN)

		events2 <- bfd.StateChange{
			LocalDiscr: 2, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateDown, NewState: bfd.StateUp,
		}
		time.Sleep(100 * time.Millisecond)
		stop2()

		if _, enables := svc.counts(); enables != 0 {
			t.Fatalf("EnablePeer called %d times after external WatchEvent revocation, want 0", enables)
		}
	})

	t.Run("unconfirmed crash during in-flight DisablePeer stays disabled", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		sockPath := filepath.Join(dir, "speaker.sock")
		stateFile := filepath.Join(dir, "gobgp-ownership.json")
		svc := newSimulatedGoBGPServer()
		svc.blockDisable = make(chan struct{})
		svc.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_UP)
		serveUnixGoBGP(t, sockPath, svc)
		target := "unix://" + sockPath

		events1, crash1 := runHandlerInstance(t, target, stateFile)
		events1 <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateUp, NewState: bfd.StateDown,
			Diag: bfd.DiagControlTimeExpired, Cause: bfd.CauseDetectionTimeout,
		}
		time.Sleep(50 * time.Millisecond)
		// Crash controller 1 before DisablePeer returns confirmation; peer becomes down externally.
		crash1()
		svc.setPeer("198.18.42.11", gobgpapi.PeerState_ADMIN_STATE_DOWN)

		events2, stop2 := runHandlerInstance(t, target, stateFile)
		events2 <- bfd.StateChange{
			LocalDiscr: 2, PeerAddr: netip.MustParseAddr("198.18.42.11"),
			Type: bfd.SessionTypeSingleHop, OldState: bfd.StateDown, NewState: bfd.StateUp,
		}
		time.Sleep(100 * time.Millisecond)
		stop2()

		if _, enables := svc.counts(); enables != 0 {
			t.Fatalf("EnablePeer called %d times for unconfirmed mid-RPC crash, want 0", enables)
		}
	})
}
