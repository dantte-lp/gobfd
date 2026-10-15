package apiclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// WatchSessions projects revisioned Watch events into causal session views.
// STATE_CHANGE rows are partial: configuration, timers and counters are not
// current status after a row replacement. Registration coverage is separate.
// A nil view invalidates health on startup, GAP and disconnect. Snapshot rows
// become usable only at SYNC_COMPLETE. Consumer errors are terminal.
func WatchSessions(ctx context.Context, client bfdv1connect.BfdServiceClient,
	consume func([]*bfdv1.BfdSession) error, logger *slog.Logger,
) error {
	const maxBackoff = 30 * time.Second
	backoff := time.Second
	for {
		if err := consume(nil); err != nil {
			return fmt.Errorf("invalidate session view: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("watch sessions: %w", err)
		}
		synced, terminal, err := watchSessionStream(ctx, client, consume)
		if terminal {
			return err
		}
		if invalidateErr := consume(nil); invalidateErr != nil {
			return fmt.Errorf("invalidate disconnected session view: %w", invalidateErr)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("watch sessions: %w", ctx.Err())
		}
		if synced {
			backoff = time.Second
		}
		logger.Warn("session Watch disconnected, reconnecting", slog.Any("error", err), slog.Duration("backoff", backoff))
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for session Watch reconnect: %w", ctx.Err())
		case <-timer.C:
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// watchSessionStream distinguishes consumer failures from recoverable stream
// failures; synced resets backoff after a successfully committed snapshot.
func watchSessionStream(ctx context.Context, client bfdv1connect.BfdServiceClient,
	consume func([]*bfdv1.BfdSession) error,
) (bool, bool, error) {
	stream, err := client.WatchSessionEvents(ctx, &bfdv1.WatchSessionEventsRequest{IncludeCurrent: true})
	if err != nil {
		return false, false, fmt.Errorf("open session Watch: %w", err)
	}
	defer stream.Close()
	synced := false
	view := sessionView{sessions: make(map[uint32]*bfdv1.BfdSession), syncing: true}
	for {
		msg, receiveErr := stream.Receive()
		if receiveErr != nil {
			return synced, false, fmt.Errorf("receive session Watch: %w", receiveErr)
		}
		changed, applyErr := view.apply(msg)
		if applyErr != nil {
			return synced, false, applyErr
		}
		if !changed {
			continue
		}
		if !view.syncing {
			synced = true
		}
		var sessions []*bfdv1.BfdSession
		if !view.syncing {
			sessions = make([]*bfdv1.BfdSession, 0, len(view.sessions))
			for _, session := range view.sessions {
				sessions = append(sessions, session)
			}
		}
		if consumeErr := consume(sessions); consumeErr != nil {
			return synced, true, fmt.Errorf("consume session view: %w", consumeErr)
		}
	}
}

type sessionView struct {
	sessions map[uint32]*bfdv1.BfdSession
	syncing  bool
	covered  uint64
}

func (v *sessionView) apply(msg *bfdv1.WatchSessionEventsResponse) (bool, error) {
	switch msg.GetType() {
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP:
		clear(v.sessions)
		v.syncing = true
		v.covered = 0
		return true, nil
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE:
		v.syncing = false
		v.covered = msg.GetRevision()
		return true, nil
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED,
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE,
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED:
		if !v.syncing && msg.GetRevision() <= v.covered {
			return false, nil
		}
		session := msg.GetSession()
		if session == nil || session.GetLocalDiscriminator() == 0 {
			return false, fmt.Errorf("session Watch event without identity: %w", errors.ErrUnsupported)
		}
		if coveredSessionObservation(v.sessions[session.GetLocalDiscriminator()], session) {
			if !v.syncing {
				v.covered = msg.GetRevision()
			}
			return false, nil
		}
		if msg.GetType() == bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED {
			delete(v.sessions, session.GetLocalDiscriminator())
		} else {
			v.sessions[session.GetLocalDiscriminator()] = session
		}
		if !v.syncing {
			v.covered = msg.GetRevision()
		}
		return !v.syncing, nil
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_UNSPECIFIED:
		return false, nil
	default:
		return false, nil
	}
}

func coveredSessionObservation(current, incoming *bfdv1.BfdSession) bool {
	return current.GetObservationVersion() != 0 && incoming.GetObservationVersion() != 0 &&
		incoming.GetObservationVersion() <= current.GetObservationVersion()
}
