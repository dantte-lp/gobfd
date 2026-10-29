package gobgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/internal/bfd"
	strategycontract "github.com/dantte-lp/gobfd/internal/gobgp/strategy"
)

// -------------------------------------------------------------------------
// Strategy — configurable BFD->BGP action policy
// -------------------------------------------------------------------------

// Strategy determines how BFD state changes affect BGP.
type Strategy = strategycontract.Strategy

const (
	// StrategyDisablePeer disables/enables the BGP peer on BFD Down/Up.
	// This is the recommended default: it causes BGP to send a Notification
	// and cleanly tear down the session, allowing the remote peer to
	// immediately reconverge routes.
	StrategyDisablePeer = strategycontract.DisablePeer

	// StrategyWithdrawRoutes is the reserved identifier for a future route
	// withdrawal strategy. It is recognized for migration diagnostics but is
	// not implemented.
	StrategyWithdrawRoutes = strategycontract.WithdrawRoutes
)

// DefaultActionTimeout is the maximum duration of a GoBGP action when no timeout is configured.
const DefaultActionTimeout = 5 * time.Second

// Retry timing for pending GoBGP actions. A failed action is retried with
// exponential backoff; a recovery held by dampening is re-checked at a
// fixed interval until suppression lifts.
const (
	actionRetryFirst   = time.Second
	actionRetryCeiling = 30 * time.Second

	// Fixed re-check, so a held recovery lands up to one
	// interval after suppression ends; compute the exact release time if
	// that lag matters.
	suppressionRecheck = time.Second
)

// -------------------------------------------------------------------------
// Sentinel Errors
// -------------------------------------------------------------------------

var (
	// ErrInvalidStrategy indicates the configured strategy is not recognized.
	ErrInvalidStrategy = errors.New("invalid gobgp strategy")

	// ErrUnsupportedStrategy indicates the strategy is recognized but not
	// yet implemented.
	ErrUnsupportedStrategy = errors.New("unsupported gobgp strategy")
)

// -------------------------------------------------------------------------
// Handler — BFD->BGP state change consumer
// -------------------------------------------------------------------------

// Handler consumes BFD state change events and applies the configured
// strategy against the GoBGP API. It applies GoBFD's implementation-defined
// flap dampening policy before taking any BGP action. RFC 5882 Section 3.1
// permits session-state hysteresis but does not define this algorithm.
//
// The handler runs as a single goroutine in the daemon's errgroup,
// consuming from the Manager.StateChanges() channel.
//
// Each peer keeps its newest unapplied action until GoBGP accepts it: a
// transient GoBGP failure is retried with backoff, and a recovery held by
// dampening is applied once suppression lifts, without waiting for another
// BFD event. Dampening never holds a Down (H-27). A newer event for the
// peer replaces the pending action.
type Handler struct {
	client   Client
	strategy Strategy
	dampener *Dampener
	timeout  time.Duration
	actuates func(sc bfd.StateChange) bool
	snapshot func() ([]bfd.SessionSnapshot, uint64)
	onAction func(rpc string, err error)
	logger   *slog.Logger

	// pending is owned by the Run goroutine.
	pending map[string]*pendingAction
	// pendingStatus is a separately published value copy for concurrent observers.
	statusMu      sync.RWMutex
	pendingStatus map[string]PeerStatus

	// disabled holds the peers this process disabled (owned by the Run
	// goroutine). GoBFD enables only these: a peer the operator disabled,
	// or one disabled before a restart, is never enabled (.8.2.4.1).
	disabled map[string]struct{}

	// last is the last state seen per base session (owned by the Run
	// goroutine); a resync rebuilds lost edges from it.
	last map[uint32]bfd.StateChange

	// resynced holds sessions whose state the last resync set; their next
	// event is dropped when it only repeats that state.
	resynced map[uint32]struct{}

	// covered is the revision of the last resync snapshot; events up to it
	// are already applied. Zero before the first resync.
	covered uint64
}

// pendingAction is the newest BGP action not yet applied for a peer.
type pendingAction struct {
	sc       bfd.StateChange
	down     bool
	due      time.Time
	backoff  time.Duration
	failures int
}

// HandlerConfig holds the configuration for a Handler.
type HandlerConfig struct {
	// Client is the GoBGP gRPC client.
	Client Client

	// Strategy determines the BGP action on BFD state changes.
	Strategy Strategy

	// Dampening configures GoBFD's penalty-based flap dampening policy.
	Dampening DampeningConfig

	// ActionTimeout bounds each GoBGP API action. When unset, the handler
	// uses DefaultActionTimeout.
	ActionTimeout time.Duration

	// Actuates reports whether a single-hop or multi-hop session is the
	// canonical BFD session for its BGP neighbor and still exists. The
	// handler ignores a Down it rejects, and before applying a late Down
	// asks again with the pending event. Deletion cancels pending actions
	// for that session. Up is not gated. Nil accepts every base session.
	Actuates func(sc bfd.StateChange) bool

	// Snapshot returns the current sessions and the revision they cover.
	// After a subscriber gap the handler resyncs from it: a session last
	// seen Up and now Down is disabled, an Up re-enables a peer GoBFD
	// disabled, and a session first seen Down causes no action. Nil leaves
	// lost events unrecovered.
	Snapshot func() ([]bfd.SessionSnapshot, uint64)

	// OnAction, when set, receives the GoBGP RPC name and result of every
	// action attempt, including retries. It runs on the handler goroutine.
	OnAction func(rpc string, err error)

	// Logger is the parent logger. The handler adds its own component tag.
	Logger *slog.Logger
}

// NewHandler creates a new BFD->BGP handler with the given configuration.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	strategy, recognized := strategycontract.Parse(string(cfg.Strategy))
	if !recognized {
		return nil, fmt.Errorf("handler strategy %q: %w", cfg.Strategy, ErrInvalidStrategy)
	}

	if !strategy.Implemented() {
		return nil, fmt.Errorf("handler strategy %q: %w", cfg.Strategy, ErrUnsupportedStrategy)
	}

	timeout := cfg.ActionTimeout
	if timeout <= 0 {
		timeout = DefaultActionTimeout
	}

	return &Handler{
		client:        cfg.Client,
		strategy:      strategy,
		dampener:      NewDampener(cfg.Dampening, cfg.Logger),
		timeout:       timeout,
		actuates:      cfg.Actuates,
		snapshot:      cfg.Snapshot,
		last:          make(map[uint32]bfd.StateChange),
		resynced:      make(map[uint32]struct{}),
		onAction:      cfg.OnAction,
		pending:       make(map[string]*pendingAction),
		pendingStatus: make(map[string]PeerStatus),
		disabled:      make(map[string]struct{}),
		logger: cfg.Logger.With(
			slog.String("component", "gobgp.handler"),
			slog.String("strategy", string(cfg.Strategy)),
		),
	}, nil
}

// Run consumes BFD state changes and applies BGP actions. It blocks until
// the context is cancelled or the events channel is closed.
//
// This method is designed to run as an errgroup goroutine:
//
//	g.Go(func() error {
//	    return handler.Run(gCtx, mgr.StateChanges())
//	})
//
// On BFD Down (never held by dampening):
//   - disable-peer: calls GoBGP DisablePeer
//
// On BFD Up (held while dampening suppresses the peer):
//   - disable-peer: calls GoBGP EnablePeer
func (h *Handler) Run(ctx context.Context, events <-chan bfd.StateChange) error {
	h.logger.Info("handler started, consuming BFD state changes")
	defer h.publishPendingStatus(nil)

	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("handler stopped")
			return nil

		case sc, ok := <-events:
			if !ok {
				h.logger.Info("state change channel closed, handler stopping")
				return nil
			}
			h.handleStateChange(ctx, sc)

		case <-retry.C:
			// One GoBGP call per wakeup keeps the event channel drained.
			h.retryDue(ctx)
		}
		h.publishPendingStatus(h.pending)
		h.armRetry(retry)
	}
}

// armRetry points the retry timer at the earliest pending action.
func (h *Handler) armRetry(retry *time.Timer) {
	var next time.Time
	for _, p := range h.pending {
		if next.IsZero() || p.due.Before(next) {
			next = p.due
		}
	}
	if next.IsZero() {
		retry.Stop()
		return
	}
	retry.Reset(max(0, time.Until(next)))
}

// retryDue attempts the earliest pending action that is due.
func (h *Handler) retryDue(ctx context.Context) {
	var (
		peerAddr string
		due      *pendingAction
	)
	for addr, p := range h.pending {
		if due == nil || p.due.Before(due.due) {
			peerAddr, due = addr, p
		}
	}
	if due == nil || time.Now().Before(due.due) {
		return
	}
	if due.down && !h.canDisable(due.sc) {
		delete(h.pending, peerAddr)
		h.logger.Info("pending BGP Down dropped, session no longer actuates the peer",
			slog.String("peer", peerAddr),
		)
		return
	}

	// ShouldSuppressUp records no penalty and applies suppression expiry,
	// so it releases a held recovery. A Down is never held (H-27).
	if !due.down && h.dampener.ShouldSuppressUp(peerAddr) {
		due.due = time.Now().Add(suppressionRecheck)
		return
	}
	h.apply(ctx, peerAddr, due)
}

// handleStateChange processes a single BFD state transition.
func (h *Handler) handleStateChange(ctx context.Context, sc bfd.StateChange) {
	if sc.Gap {
		h.resync(ctx, sc.Revision)
		return
	}
	if h.covered > 0 && sc.Revision <= h.covered {
		return // applied by the last resync
	}
	if !isBaseSession(sc.Type) {
		return
	}
	previous, seen := h.last[sc.LocalDiscr]
	if seen && staleObservation(sc, previous) {
		return
	}
	if sc.Deleted {
		h.forgetSession(sc.LocalDiscr)
		return
	}
	if _, ok := h.resynced[sc.LocalDiscr]; ok {
		delete(h.resynced, sc.LocalDiscr)
		if sc.ObservationVersion == 0 && previous.NewState == sc.NewState {
			return // repeats state the resync already applied
		}
	}
	h.last[sc.LocalDiscr] = sc
	if samePathFailure(previous, sc) {
		return // Diagnostic-only updates retain the original pending failure and penalty.
	}
	h.act(ctx, sc)
}

func (h *Handler) forgetSession(discr uint32) {
	delete(h.last, discr)
	delete(h.resynced, discr)
	for peer, pending := range h.pending {
		if pending.sc.LocalDiscr == discr {
			delete(h.pending, peer)
		}
	}
}

// isBaseSession reports whether a session describes a BGP neighbor (H-31):
// a Micro-BFD member link, overlay or Echo session to the same address
// does not.
func isBaseSession(t bfd.SessionType) bool {
	return t == bfd.SessionTypeSingleHop || t == bfd.SessionTypeMultiHop
}

// act applies the BGP action for one base-session transition.
func (h *Handler) act(ctx context.Context, sc bfd.StateChange) {
	peerAddr := sc.PeerAddr.Unmap().String()
	if administrative(sc) {
		// Administrative intent cancels obsolete failure, preserving owned recovery.
		if p := h.pending[peerAddr]; p != nil && p.down {
			delete(h.pending, peerAddr)
		}
		h.dampener.Reset(peerAddr)
		return
	}
	// Only a Down is gated: an Up re-enables only a peer GoBFD disabled, and
	// an ambiguity that later clears publishes no event to re-enable it.
	if isTransitionToDown(sc) && !h.canDisable(sc) {
		h.logger.Debug("ignoring BFD Down, session does not actuate the BGP peer",
			slog.String("peer", peerAddr),
			slog.Uint64("local_discr", uint64(sc.LocalDiscr)),
		)
		return
	}

	h.logger.Debug("received BFD state change",
		slog.String("peer", peerAddr),
		slog.String("old_state", sc.OldState.String()),
		slog.String("new_state", sc.NewState.String()),
		slog.String("diag", sc.Diag.String()),
	)

	switch {
	case isTransitionToDown(sc):
		h.handleDown(ctx, peerAddr, &pendingAction{sc: sc, down: true})

	case isTransitionToUp(sc):
		h.handleUp(ctx, peerAddr, &pendingAction{sc: sc})

	default:
		// Other transitions (e.g., Down->Init) are informational only.
		h.logger.Debug("ignoring non-actionable state change",
			slog.String("peer", peerAddr),
			slog.String("transition", sc.OldState.String()+"->"+sc.NewState.String()),
		)
	}
}

// resync rebuilds the edges lost in a subscriber gap from the snapshot and
// the last state seen per session (.8.2.4.1). A session not seen before
// starts from Down, so its Down causes no action, as at startup.
func (h *Handler) resync(ctx context.Context, gapRevision uint64) {
	if h.snapshot == nil {
		h.logger.Error("BFD state changes lost and no snapshot to resync from",
			slog.Uint64("revision", gapRevision))
		return
	}
	snaps, rev := h.snapshot()
	h.logger.Warn("BFD state changes lost, resyncing GoBGP actions from snapshot",
		slog.Uint64("gap_revision", gapRevision),
		slog.Uint64("snapshot_revision", rev),
	)
	present := make(map[uint32]struct{}, len(snaps))
	clear(h.resynced)
	for _, snap := range snaps {
		if !isBaseSession(snap.Type) {
			continue
		}
		present[snap.LocalDiscr] = struct{}{}
		h.resynced[snap.LocalDiscr] = struct{}{}
		previous, ok := h.last[snap.LocalDiscr]
		if snap.ObservationVersion != previous.ObservationVersion &&
			coveredObservation(snap.ObservationVersion, previous.ObservationVersion) {
			continue
		}
		prev := bfd.StateDown
		if ok && previous.Ref == snap.Ref {
			prev = previous.NewState
		}
		sc := bfd.StateChange{
			LocalDiscr:         snap.LocalDiscr,
			PeerAddr:           snap.PeerAddr,
			LocalAddr:          snap.LocalAddr,
			OldState:           prev,
			NewState:           snap.State,
			Diag:               snap.LocalDiag,
			Timestamp:          time.Now(),
			Type:               snap.Type,
			Interface:          snap.Interface,
			RemoteKnown:        snap.RemoteKnown,
			RemoteState:        snap.RemoteState,
			RemoteDiag:         snap.RemoteDiag,
			LocalC:             snap.LocalC,
			RemoteC:            snap.RemoteC,
			Cause:              snap.Cause,
			ObservationVersion: snap.ObservationVersion,
			Ref:                snap.Ref,
		}
		h.last[snap.LocalDiscr] = sc
		if prev != snap.State || administrative(sc) ||
			(sc.Cause == bfd.CausePathDown && !samePathFailure(previous, sc)) {
			h.act(ctx, sc)
		}
	}
	for discr := range h.last {
		if _, ok := present[discr]; !ok {
			h.forgetSession(discr)
		}
	}
	h.covered = rev
}

// handleDown processes a BFD session going Down. RFC 5882 Sections 4.2
// and 10.2: a failure transition other than AdminDown should tear the EBGP
// session down. The v1 dampening contract applies every qualifying Down at
// once; dampening holds only the recovery that follows.
func (h *Handler) handleDown(ctx context.Context, peerAddr string, p *pendingAction) {
	// ShouldSuppress records the flap penalty. v1 dampening contract
	// (H-27): it never holds a qualifying Down; while suppressed it only
	// makes a Down redundant for a peer GoBFD already disabled, and that
	// Down cancels a held recovery.
	suppressed := h.dampener.ShouldSuppress(peerAddr)
	if _, ours := h.disabled[peerAddr]; ours && suppressed {
		delete(h.pending, peerAddr)
		h.logger.Info("BFD Down while dampened; peer stays disabled",
			slog.String("peer", peerAddr),
			slog.String("diag", p.sc.Diag.String()),
		)
		return
	}
	h.pending[peerAddr] = p

	h.logger.Info("BFD Down, applying BGP action",
		slog.String("peer", peerAddr),
		slog.String("strategy", string(h.strategy)),
		slog.String("diag", p.sc.Diag.String()),
	)
	h.apply(ctx, peerAddr, p)
}

// handleUp processes a BFD session going Up. Re-enabling the BGP peer is
// GoBFD policy for the disable-peer strategy; RFC 5882 leaves
// re-establishment to the control protocol.
func (h *Handler) handleUp(ctx context.Context, peerAddr string, p *pendingAction) {
	h.pending[peerAddr] = p

	// Hold Up while the peer is still dampened by local policy.
	if h.dampener.ShouldSuppressUp(peerAddr) {
		h.logger.Warn("BFD Up suppressed by flap dampening",
			slog.String("peer", peerAddr),
		)
		p.due = time.Now().Add(suppressionRecheck)
		return
	}

	h.logger.Info("BFD Up, applying BGP action",
		slog.String("peer", peerAddr),
		slog.String("strategy", string(h.strategy)),
	)
	h.apply(ctx, peerAddr, p)
}

// apply runs the pending action once. Success or a permanent error clears
// it; a transient error schedules a retry with exponential backoff.
func (h *Handler) apply(ctx context.Context, peerAddr string, p *pendingAction) {
	var err error
	rpc := "EnablePeer"
	if p.down {
		rpc = "DisablePeer"
		err = h.applyDownAction(ctx, peerAddr, p.sc)
	} else {
		err = h.applyUpAction(ctx, peerAddr, p.sc)
	}
	if h.onAction != nil && ctx.Err() == nil {
		h.onAction(rpc, err)
	}

	switch {
	case err == nil:
		delete(h.pending, peerAddr)
		if p.failures > 0 {
			h.logger.Info("BGP action applied after retry",
				slog.String("peer", peerAddr),
				slog.Bool("down", p.down),
				slog.Int("failures", p.failures),
			)
		}

	case ctx.Err() != nil:
		// Shutdown; Run returns on its next select.

	case !retryable(err):
		delete(h.pending, peerAddr)
		h.logger.Error("failed to apply BGP action, not retrying",
			slog.String("peer", peerAddr),
			slog.Bool("down", p.down),
			slog.String("error", err.Error()),
		)

	default:
		p.failures++
		p.backoff = min(max(2*p.backoff, actionRetryFirst), actionRetryCeiling)
		p.due = time.Now().Add(p.backoff)
		level := slog.LevelDebug
		if p.failures == 1 {
			level = slog.LevelError
		}
		h.logger.Log(ctx, level, "failed to apply BGP action, retrying",
			slog.String("peer", peerAddr),
			slog.Bool("down", p.down),
			slog.Int("failures", p.failures),
			slog.Duration("retry_in", p.backoff),
			slog.String("error", err.Error()),
		)
	}
}

// retryable reports whether a GoBGP action error may succeed on retry.
// GoBGP v4 reports an unknown neighbor with codes.Unknown, which is retried
// because the neighbor can appear after a GoBGP restart.
func retryable(err error) bool {
	if errors.Is(err, ErrClientClosed) || errors.Is(err, ErrUnsupportedStrategy) ||
		errors.Is(err, ErrInvalidStrategy) {
		return false
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.Unimplemented, codes.PermissionDenied, codes.Unauthenticated:
		return false
	default:
		return true
	}
}

// applyDownAction executes the strategy-specific BGP action for BFD Down.
func (h *Handler) applyDownAction(ctx context.Context, peerAddr string, sc bfd.StateChange) error {
	if !h.canDisable(sc) {
		return nil
	}
	switch h.strategy {
	case StrategyDisablePeer:
		actionCtx, cancel := context.WithTimeout(ctx, h.timeout)
		defer cancel()

		if _, ours := h.disabled[peerAddr]; !ours {
			adminDown, err := h.client.PeerAdminDown(actionCtx, peerAddr)
			if err != nil {
				return fmt.Errorf("disable peer %s: %w", peerAddr, err)
			}
			if adminDown {
				h.logger.Info("BGP peer already administratively down; leaving it to the operator",
					slog.String("peer", peerAddr))
				return nil
			}
		}
		// A PeerAdminDown read may block while the owner commits AdminDown.
		// This local preflight cannot make the remote RPC atomic with future commits.
		if !h.canDisable(sc) {
			return nil
		}
		communication := FormatBFDDownCommunication(sc.Diag)
		if err := h.client.DisablePeer(actionCtx, peerAddr, communication); err != nil {
			return fmt.Errorf("disable peer %s: %w", peerAddr, err)
		}
		h.disabled[peerAddr] = struct{}{}
		return nil

	case StrategyWithdrawRoutes:
		// Reserved for future implementation.
		return fmt.Errorf("apply down action for peer %s: %w", peerAddr, ErrUnsupportedStrategy)

	default:
		return fmt.Errorf("apply down action for peer %s: strategy %q: %w", peerAddr, h.strategy, ErrInvalidStrategy)
	}
}

// applyUpAction executes the strategy-specific BGP action for BFD Up.
func (h *Handler) applyUpAction(ctx context.Context, peerAddr string, _ bfd.StateChange) error {
	switch h.strategy {
	case StrategyDisablePeer:
		if _, ours := h.disabled[peerAddr]; !ours {
			h.logger.Debug("BFD Up for a peer GoBFD did not disable; not enabling it",
				slog.String("peer", peerAddr))
			return nil
		}
		actionCtx, cancel := context.WithTimeout(ctx, h.timeout)
		defer cancel()

		if err := h.client.EnablePeer(actionCtx, peerAddr); err != nil {
			return fmt.Errorf("enable peer %s: %w", peerAddr, err)
		}
		delete(h.disabled, peerAddr)
		return nil

	case StrategyWithdrawRoutes:
		// Reserved for future implementation.
		return fmt.Errorf("apply up action for peer %s: %w", peerAddr, ErrUnsupportedStrategy)

	default:
		return fmt.Errorf("apply up action for peer %s: strategy %q: %w", peerAddr, h.strategy, ErrInvalidStrategy)
	}
}

// -------------------------------------------------------------------------
// State transition helpers
// -------------------------------------------------------------------------

// isTransitionToDown returns true if the state change represents a session
// going Down from a previously operational state (Init or Up).
// AdminDown is excluded because it is an intentional local action, not a
// failure detection event.
func isTransitionToDown(sc bfd.StateChange) bool {
	return sc.NewState == bfd.StateDown &&
		(sc.Cause == bfd.CausePathDown || sc.OldState == bfd.StateUp || sc.OldState == bfd.StateInit)
}

func administrative(sc bfd.StateChange) bool {
	if sc.Cause == bfd.CausePathDown {
		return false
	}
	return sc.NewState == bfd.StateAdminDown || sc.Cause == bfd.CauseLocalAdmin ||
		sc.Cause == bfd.CauseRemoteAdminDown || (sc.RemoteKnown && sc.RemoteState == bfd.StateAdminDown)
}

func coveredObservation(version, previous uint64) bool {
	return version != 0 && previous != 0 && version <= previous
}

func staleObservation(current, previous bfd.StateChange) bool {
	return coveredObservation(current.ObservationVersion, previous.ObservationVersion) ||
		(current.Deleted && current.Ref != previous.Ref)
}

func samePathFailure(previous, current bfd.StateChange) bool {
	return previous.Ref == current.Ref && previous.NewState == bfd.StateDown &&
		current.NewState == bfd.StateDown && previous.Cause == bfd.CausePathDown && current.Cause == bfd.CausePathDown
}

func (h *Handler) canDisable(sc bfd.StateChange) bool {
	if administrative(sc) {
		return false
	}
	if current, ok := h.last[sc.LocalDiscr]; ok &&
		(current.Ref != sc.Ref || administrative(current)) {
		return false
	}
	return h.actuates == nil || h.actuates(sc)
}

// isTransitionToUp returns true if the state change represents a session
// reaching the Up state from a non-Up state.
func isTransitionToUp(sc bfd.StateChange) bool {
	return sc.NewState == bfd.StateUp && sc.OldState != bfd.StateUp
}
