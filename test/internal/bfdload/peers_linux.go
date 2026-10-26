//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// PeerConfig gives the independent generator the target's local/peer tuples.
type PeerConfig struct {
	Profile          Profile         `json:"profile"`
	Sessions         []OwnerEndpoint `json:"sessions"`
	SourcePortBase   uint16          `json:"source_port_base"`
	ControlDirectory string          `json:"control_directory,omitempty"`
}

// PeerSessionObservation counts actual writes separately from scheduled omissions.
type PeerSessionObservation struct {
	ID                      uint64                `json:"id"`
	Endpoint                OwnerEndpoint         `json:"endpoint"`
	SourcePort              uint16                `json:"source_port"`
	State                   string                `json:"state"`
	RemoteDiscriminator     uint32                `json:"remote_discriminator"`
	Scheduled               uint64                `json:"scheduled"`
	WriteAttempts           uint64                `json:"write_attempts"`
	Written                 uint64                `json:"written"`
	WriteErrors             uint64                `json:"write_errors"`
	WriteCanceled           uint64                `json:"write_canceled"`
	RandomOmitted           uint64                `json:"random_omitted"`
	BlackholeOmitted        uint64                `json:"blackhole_omitted"`
	Read                    uint64                `json:"read"`
	ReadErrors              uint64                `json:"read_errors"`
	Accepted                uint64                `json:"accepted"`
	Rejected                uint64                `json:"rejected"`
	ReceiveRandomOmitted    uint64                `json:"receive_random_omitted"`
	ReceiveBlackholeOmitted uint64                `json:"receive_blackhole_omitted"`
	ReceiveNotProcessed     uint64                `json:"receive_not_processed"`
	KernelDropSamples       uint64                `json:"kernel_drop_samples"`
	KernelDropInvalid       uint64                `json:"kernel_drop_invalid"`
	KernelDropCounterRaw    uint32                `json:"kernel_drop_counter_raw"`
	UpTransitions           uint64                `json:"up_transitions"`
	DownTransitions         uint64                `json:"down_transitions"`
	DetectionExpirations    uint64                `json:"detection_expirations"`
	BlackholeWindows        []PeerBlackholeWindow `json:"blackhole_windows"`
	BlackholeEventsOmitted  uint64                `json:"blackhole_events_omitted"`
}

// PeerObservation is generator evidence, never a daemon or reference verdict.
// ElapsedNS includes setup and cleanup; kernel drop counters are raw samples.
type PeerObservation struct {
	Scope              string                   `json:"scope"`
	Profile            Profile                  `json:"profile"`
	StartedAtUTC       time.Time                `json:"started_at_utc"`
	ElapsedNS          int64                    `json:"elapsed_ns"`
	Completed          bool                     `json:"completed"`
	Stopped            bool                     `json:"stopped"`
	ReferenceQualified bool                     `json:"reference_qualified"`
	Peers              []PeerSessionObservation `json:"peers"`
	Environment        Environment              `json:"environment"`
	Error              string                   `json:"error,omitempty"`
	Phase              *Phase                   `json:"phase,omitempty"`
}

// RunPeers runs separate reference state machines over actual UDP until the
// requested duration, cancellation, or a worker error, then joins all workers.
func RunPeers(ctx context.Context, cfg PeerConfig, logger *slog.Logger) (PeerObservation, error) {
	result := PeerObservation{Scope: "udp-peer-generator", Profile: cfg.Profile}
	if err := ctx.Err(); err != nil {
		runErr := fmt.Errorf("start peer generator: %w", err)
		result.Error = runErr.Error()
		return result, runErr
	}
	if err := cfg.Validate(); err != nil {
		result.Error = err.Error()
		return result, err
	}
	if logger == nil {
		err := fmt.Errorf("peer generator logger is nil: %w", ErrProfile)
		result.Error = err.Error()
		return result, err
	}
	group := peerGroup{started: time.Now()}
	completed, runErr := group.run(ctx, cfg, logger)
	return group.finish(ctx, result, completed, runErr)
}

type peerGroup struct {
	peers     []*peerRuntime
	workers   sync.WaitGroup
	started   time.Time
	cancel    context.CancelFunc
	stopIO    func() bool
	closeOnce sync.Once
	closeErr  error
	phase     atomic.Pointer[phaseStart]
}

func (g *peerGroup) closeIO() {
	g.closeOnce.Do(func() {
		for _, peer := range g.peers {
			g.closeErr = errors.Join(g.closeErr, peer.sender.Close(), peer.listener.Close())
		}
	})
}

func (g *peerGroup) stop() {
	g.cancel()
	if g.stopIO != nil {
		g.stopIO()
	}
	g.closeIO()
	g.workers.Wait()
}

func (g *peerGroup) run(ctx context.Context, cfg PeerConfig, logger *slog.Logger) (bool, error) {
	runCtx, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	defer g.stop()
	for index, endpoint := range cfg.Sessions {
		if err := runCtx.Err(); err != nil {
			return false, fmt.Errorf("bind peer generator: %w", err)
		}
		peer, err := bindPeer(runCtx, cfg, index, endpoint, logger)
		if err != nil {
			return false, err
		}
		g.peers = append(g.peers, peer)
	}
	// The resource slice is immutable before cancellation can close its sockets.
	g.stopIO = context.AfterFunc(runCtx, g.closeIO)
	if cfg.ControlDirectory != "" {
		if err := waitListenerReady(runCtx, cfg.ControlDirectory, true); err != nil {
			return false, err
		}
	}
	trafficStart := time.Now()
	for _, peer := range g.peers {
		peer.started = true
		if cfg.ControlDirectory != "" {
			peer.phase = &g.phase
			peer.duration = cfg.Profile.Duration
		}
		g.workers.Go(func() { peer.read(runCtx, cancel) })
		g.workers.Go(func() { peer.run(runCtx, cancel, trafficStart) })
	}
	if cfg.ControlDirectory != "" {
		return g.runControlled(runCtx, cfg)
	}
	timer := time.NewTimer(cfg.Profile.Duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true, nil
	case <-runCtx.Done():
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("peer workload interrupted: %w", err)
		}
		return false, nil
	}
}

func (g *peerGroup) finish(
	ctx context.Context, result PeerObservation, completed bool, runErr error,
) (PeerObservation, error) {
	for _, peer := range g.peers {
		if peer.started {
			for range peer.input {
				peer.observed.ReceiveNotProcessed++
			}
		}
		peer.observed.State, peer.observed.RemoteDiscriminator = peer.state.state.String(), peer.state.remoteDiscriminator
		result.Peers = append(result.Peers, peer.observed)
		runErr = errors.Join(runErr, peer.readErr, peer.loopErr)
		if omitted := peer.observed.BlackholeEventsOmitted; omitted != 0 {
			runErr = errors.Join(runErr, fmt.Errorf("peer %d blackhole journal omitted %d events: %w",
				peer.observed.ID, omitted, ErrProfile))
		}
	}
	result.Stopped, result.Completed = true, completed
	if phase := g.phase.Load(); phase != nil {
		result.Phase = phase.observation()
	}
	result.StartedAtUTC, result.ElapsedNS = g.started.UTC(), int64(time.Since(g.started))
	observeCtx, observeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer observeCancel()
	var observeErr error
	result.Environment, observeErr = CaptureEnvironment(observeCtx)
	runErr = errors.Join(runErr, g.closeErr, observeErr)
	if runErr != nil {
		result.Completed, result.Error = false, runErr.Error()
	}
	return result, runErr
}

// Validate checks the complete wire topology before any socket is opened.
func (cfg PeerConfig) Validate() error {
	if cfg.ControlDirectory != "" && !filepath.IsAbs(cfg.ControlDirectory) {
		return fmt.Errorf("control directory must be absolute: %w", ErrControl)
	}
	if err := cfg.Profile.Validate(); err != nil {
		return err
	}
	if len(cfg.Sessions) != cfg.Profile.Sessions || cfg.SourcePortBase < 49152 ||
		int(cfg.SourcePortBase)+len(cfg.Sessions)-1 > math.MaxUint16 {
		return fmt.Errorf("peer count or stable source-port range: %w", ErrProfile)
	}
	if err := validateOwnerConfig(OwnerConfig{
		Sessions: cfg.Sessions, Interval: cfg.Profile.Interval, DetectMultiplier: cfg.Profile.DetectMultiplier,
	}); err != nil {
		return fmt.Errorf("peer wire identities: %w: %w", ErrProfile, err)
	}
	return validatePeerBindings(cfg)
}

func validatePeerBindings(cfg PeerConfig) error {
	seen := make(map[netip.AddrPort]struct{}, len(cfg.Sessions))
	ipv4 := 0
	for _, endpoint := range cfg.Sessions {
		if endpoint.Local == endpoint.Peer {
			return fmt.Errorf("peer and target receive bindings coincide: %w", ErrProfile)
		}
		if _, duplicate := seen[endpoint.Peer]; duplicate {
			return fmt.Errorf("duplicate peer receive binding %s: %w", endpoint.Peer, ErrProfile)
		}
		seen[endpoint.Peer] = struct{}{}
		if endpoint.Local.Addr().Is4() {
			ipv4++
		}
		wrongPort := endpoint.Local.Port() != netio.PortSingleHop || endpoint.Peer.Port() != netio.PortSingleHop
		if cfg.Profile.Kind != fixtureProfile && (wrongPort || endpoint.Interface == "") {
			return fmt.Errorf("qualification peer requires RFC 5881 Control ports and interface binding: %w", ErrProfile)
		}
	}
	if cfg.Profile.Kind != fixtureProfile && (ipv4 == 0 || ipv4 == len(cfg.Sessions)) {
		return fmt.Errorf("qualification peers require both IPv4 and IPv6: %w", ErrProfile)
	}
	return nil
}
