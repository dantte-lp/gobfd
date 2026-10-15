package gobgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// exponential backoff; an action held by dampening is re-checked at a fixed
// interval until suppression lifts.
const (
	actionRetryFirst   = time.Second
	actionRetryCeiling = 30 * time.Second

	// ponytail: fixed re-check, so a suppressed action lands up to one
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
// transient GoBGP failure is retried with backoff, and an action held by
// dampening is applied once suppression lifts, without waiting for another
// BFD event. A newer event for the peer replaces the pending action.
type Handler struct {
	client   Client
	strategy Strategy
	dampener *Dampener
	timeout  time.Duration
	managed  func(peerAddr string) bool
	onAction func(rpc string, err error)
	logger   *slog.Logger

	// pending is owned by the Run goroutine.
	pending map[string]*pendingAction

	// disabled holds the peers this process disabled (owned by the Run
	// goroutine). GoBFD enables only these: a peer the operator disabled,
	// or one disabled before a restart, is never enabled (.8.2.4.1).
	disabled map[string]struct{}
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

	// Managed reports whether GoBFD still has a BFD session to the peer.
	// Session deletion publishes no state change, so the handler drops a
	// pending Down for an unmanaged peer instead of applying it late. Nil
	// treats every peer as managed.
	Managed func(peerAddr string) bool

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
		client:   cfg.Client,
		strategy: strategy,
		dampener: NewDampener(cfg.Dampening, cfg.Logger),
		timeout:  timeout,
		managed:  cfg.Managed,
		onAction: cfg.OnAction,
		pending:  make(map[string]*pendingAction),
		disabled: make(map[string]struct{}),
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
// On BFD Down (with dampening filter):
//   - disable-peer: calls GoBGP DisablePeer
//
// On BFD Up (with dampening filter):
//   - disable-peer: calls GoBGP EnablePeer
func (h *Handler) Run(ctx context.Context, events <-chan bfd.StateChange) error {
	h.logger.Info("handler started, consuming BFD state changes")

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
	if due.down && h.managed != nil && !h.managed(peerAddr) {
		delete(h.pending, peerAddr)
		h.logger.Info("pending BGP Down dropped, peer has no BFD session",
			slog.String("peer", peerAddr),
		)
		return
	}

	// ShouldSuppressUp records no penalty and applies suppression expiry,
	// so it releases a held action of either direction.
	if h.dampener.ShouldSuppressUp(peerAddr) {
		due.due = time.Now().Add(suppressionRecheck)
		return
	}
	h.apply(ctx, peerAddr, due)
}

// handleStateChange processes a single BFD state transition.
func (h *Handler) handleStateChange(ctx context.Context, sc bfd.StateChange) {
	peerAddr := sc.PeerAddr.String()

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

	case sc.NewState == bfd.StateAdminDown:
		// RFC 5882 Section 4.2: no control protocol action on AdminDown,
		// so a Down still pending for this peer is dropped. A pending Up
		// stays: it re-enables a peer GoBFD itself disabled.
		if p, ok := h.pending[peerAddr]; ok && p.down {
			delete(h.pending, peerAddr)
			h.logger.Info("pending BGP Down dropped on BFD AdminDown",
				slog.String("peer", peerAddr),
			)
		}

	default:
		// Other transitions (e.g., Down->Init) are informational only.
		h.logger.Debug("ignoring non-actionable state change",
			slog.String("peer", peerAddr),
			slog.String("transition", sc.OldState.String()+"->"+sc.NewState.String()),
		)
	}
}

// handleDown processes a BFD session going Down. RFC 5882 Sections 4.2
// and 10.2: a failure transition other than AdminDown should tear the EBGP
// session down. Section 3.1 allows hiding a short Down from clients but
// requires notification when the session stays Down, so a Down held by
// dampening stays pending and is applied when suppression lifts.
func (h *Handler) handleDown(ctx context.Context, peerAddr string, p *pendingAction) {
	h.pending[peerAddr] = p

	// Apply the implementation-defined flap dampening policy before acting.
	if h.dampener.ShouldSuppress(peerAddr) {
		h.logger.Warn("BFD Down suppressed by flap dampening",
			slog.String("peer", peerAddr),
			slog.String("diag", p.sc.Diag.String()),
		)
		p.due = time.Now().Add(suppressionRecheck)
		return
	}

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
		(sc.OldState == bfd.StateUp || sc.OldState == bfd.StateInit)
}

// isTransitionToUp returns true if the state change represents a session
// reaching the Up state from a non-Up state.
func isTransitionToUp(sc bfd.StateChange) bool {
	return sc.NewState == bfd.StateUp && sc.OldState != bfd.StateUp
}
