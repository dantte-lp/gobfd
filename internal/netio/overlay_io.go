package netio

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// lockOverlaySend bounds both buffer ownership and queued sends by cancellation
// or Close. The caller must release the token after its entire write completes.
func lockOverlaySend(ctx context.Context, token chan struct{}, closed <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("overlay send context: %w", err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("overlay send context: %w", ctx.Err())
	case <-closed:
		return ErrOverlayRecvClosed
	case token <- struct{}{}:
	}
	// Close or cancellation can race acquisition of an available token.
	select {
	case <-closed:
		<-token
		return ErrOverlayRecvClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		<-token
		return fmt.Errorf("overlay send context: %w", err)
	}
	return nil
}

// overlayIO interrupts one serialized read or write without closing a healthy
// socket. Join the deadline callback before reset so it cannot poison the next
// operation. Reads have one owner; writes hold the connection's send token.
func overlayIO(
	ctx context.Context,
	setDeadline func(time.Time) error,
	closeConn func() error,
	operation func() error,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("overlay I/O context: %w", err)
	}
	if ctx.Done() == nil {
		return operation()
	}
	done := make(chan struct{})
	var deadlineErr error
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		if err := setDeadline(time.Now()); err != nil {
			// A failed deadline cannot leave the operation blocked indefinitely.
			deadlineErr = errors.Join(err, closeConn())
		}
	})
	err := operation()
	if stop() {
		return err
	}
	<-done
	return fmt.Errorf("overlay I/O canceled: %w", errors.Join(ctx.Err(), err, deadlineErr, setDeadline(time.Time{})))
}
