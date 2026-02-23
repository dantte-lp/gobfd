package gobgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// Strategy — configurable BFD->BGP action policy
// -------------------------------------------------------------------------

// Strategy determines how BFD state changes affect BGP.
type Strategy string

const (
	// StrategyDisablePeer disables/enables the BGP peer on BFD Down/Up.
	// This is the recommended default: it causes BGP to send a Notification
	// and cleanly tear down the session, allowing the remote peer to
	// immediately reconverge routes.
	StrategyDisablePeer Strategy = "disable-peer"

	// StrategyWithdrawRoutes withdraws/restores routes on BFD Down/Up.
	// This is a lighter-weight approach that does not tear down the BGP
	// session itself. Use this when you want BFD to affect route
	// advertisement without disrupting the BGP session.
	//
	// NOTE: withdraw-routes is reserved for future implementation.
	// Currently only disable-peer is supported.
	StrategyWithdrawRoutes Strategy = "withdraw-routes"
)

// ValidStrategies lists all recognized strategy strings.
//
//nolint:gochecknoglobals // Lookup table is intentionally package-level.
var ValidStrategies = map[Strategy]bool{
	StrategyDisablePeer:    true,
	StrategyWithdrawRoutes: true,
}

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
// strategy against the GoBGP API. It implements RFC 5882 Section 3.2 by
// applying flap dampening before taking any BGP action.
//
// The handler runs as a single goroutine in the daemon's errgroup,
// consuming from the Manager.StateChanges() channel.
type Handler struct {
	client   Client
	strategy Strategy
	dampener *Dampener
	logger   *slog.Logger
}

// HandlerConfig holds the configuration for a Handler.
type HandlerConfig struct {
	// Client is the GoBGP gRPC client.
	Client Client

	// Strategy determines the BGP action on BFD state changes.
	Strategy Strategy

	// Dampening configures RFC 5882 Section 3.2 flap dampening.
	Dampening DampeningConfig

	// Logger is the parent logger. The handler adds its own component tag.
	Logger *slog.Logger
}

// NewHandler creates a new BFD->BGP handler with the given configuration.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if !ValidStrategies[cfg.Strategy] {
		return nil, fmt.Errorf("handler strategy %q: %w", cfg.Strategy, ErrInvalidStrategy)
	}

	if cfg.Strategy == StrategyWithdrawRoutes {
		return nil, fmt.Errorf("handler strategy %q: %w", cfg.Strategy, ErrUnsupportedStrategy)
	}

	return &Handler{
		client:   cfg.Client,
		strategy: cfg.Strategy,
		dampener: NewDampener(cfg.Dampening, cfg.Logger),
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
		}
	}
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
		h.handleDown(ctx, peerAddr, sc)

	case isTransitionToUp(sc):
		h.handleUp(ctx, peerAddr, sc)

	default:
		// Other transitions (e.g., Down->Init) are informational only.
		h.logger.Debug("ignoring non-actionable state change",
			slog.String("peer", peerAddr),
			slog.String("transition", sc.OldState.String()+"->"+sc.NewState.String()),
		)
	}
}

// handleDown processes a BFD session going Down.
// RFC 5882 Section 4.3: "When BFD for BGP detects a failure, the BGP
// session is torn down.".
func (h *Handler) handleDown(ctx context.Context, peerAddr string, sc bfd.StateChange) {
	// RFC 5882 Section 3.2: apply flap dampening before acting.
	if h.dampener.ShouldSuppress(peerAddr) {
		h.logger.Warn("BFD Down suppressed by flap dampening",
			slog.String("peer", peerAddr),
			slog.String("diag", sc.Diag.String()),
		)
		return
	}

	h.logger.Info("BFD Down, applying BGP action",
		slog.String("peer", peerAddr),
		slog.String("strategy", string(h.strategy)),
		slog.String("diag", sc.Diag.String()),
	)

	if err := h.applyDownAction(ctx, peerAddr, sc); err != nil {
		h.logger.Error("failed to apply BGP Down action",
			slog.String("peer", peerAddr),
			slog.String("error", err.Error()),
		)
	}
}

// handleUp processes a BFD session going Up.
// RFC 5882 Section 4.3: "When the BFD session comes back up, the BGP
// session should be re-established.".
func (h *Handler) handleUp(ctx context.Context, peerAddr string, sc bfd.StateChange) {
	// RFC 5882 Section 3.2: suppress Up if peer is still dampened.
	if h.dampener.ShouldSuppressUp(peerAddr) {
		h.logger.Warn("BFD Up suppressed by flap dampening",
			slog.String("peer", peerAddr),
		)
		return
	}

	h.logger.Info("BFD Up, applying BGP action",
		slog.String("peer", peerAddr),
		slog.String("strategy", string(h.strategy)),
	)

	if err := h.applyUpAction(ctx, peerAddr, sc); err != nil {
		h.logger.Error("failed to apply BGP Up action",
			slog.String("peer", peerAddr),
			slog.String("error", err.Error()),
		)
	}
}

// applyDownAction executes the strategy-specific BGP action for BFD Down.
func (h *Handler) applyDownAction(ctx context.Context, peerAddr string, sc bfd.StateChange) error {
	switch h.strategy {
	case StrategyDisablePeer:
		communication := FormatBFDDownCommunication(sc.Diag)
		if err := h.client.DisablePeer(ctx, peerAddr, communication); err != nil {
			return fmt.Errorf("disable peer %s: %w", peerAddr, err)
		}
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
		if err := h.client.EnablePeer(ctx, peerAddr); err != nil {
			return fmt.Errorf("enable peer %s: %w", peerAddr, err)
		}
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
