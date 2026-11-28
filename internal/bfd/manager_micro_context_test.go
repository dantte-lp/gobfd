package bfd

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

type microActuatorFunc func(context.Context, MicroBFDMemberEvent) error

func (f microActuatorFunc) HandleMicroBFDMemberEvent(ctx context.Context, ev MicroBFDMemberEvent) error {
	return f(ctx, ev)
}

func TestManagerMicroBFDActuatorRetainedContextIsImmutable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		type contextKey struct{}
		key := contextKey{}
		var first context.Context
		var firstDone <-chan struct{}
		var firstDeadline time.Time
		var firstErr error
		checkFirst := func() {
			t.Helper()
			deadline, ok := first.Deadline()
			if !ok || !deadline.Equal(firstDeadline) || first.Done() != firstDone ||
				!errors.Is(first.Err(), context.Canceled) || first.Value(key) != "first" {
				t.Fatal("retained actuator context changed after its callback returned")
			}
			//nolint:errorlint // Context.Err must keep the exact cancellation error after returning it.
			if first.Err() != firstErr {
				t.Fatal("retained actuator context replaced its cancellation error")
			}
			select {
			case <-firstDone:
			default:
				t.Fatal("actuator context was not canceled after its callback returned")
			}
		}
		calls := 0
		actuator := microActuatorFunc(func(ctx context.Context, ev MicroBFDMemberEvent) error {
			calls++
			deadline, ok := ctx.Deadline()
			if !ok || !deadline.Equal(time.Now().Add(5*time.Second)) || ctx.Err() != nil ||
				ctx.Value(key) != ev.MemberInterface {
				t.Fatal("actuator callback lost its fresh deadline, active state or parent value")
			}
			select {
			case <-ctx.Done():
				t.Fatal("actuator callback received a canceled context")
			default:
			}
			if calls == 1 {
				//nolint:fatcontext // Retain the finished callback context to verify its immutable lifetime.
				first, firstDone, firstDeadline = ctx, ctx.Done(), deadline
				return nil
			}
			checkFirst()
			if ctx.Done() == firstDone {
				t.Fatal("actuator callbacks share their cancellation channel")
			}
			// Reach the first call's original deadline while the second still has budget.
			synctest.Sleep(4 * time.Second)
			checkFirst()
			if ctx.Err() != nil {
				t.Fatalf("first callback canceled the second: %v", ctx.Err())
			}
			select {
			case <-ctx.Done():
				t.Fatal("second callback lost its independent cancellation budget")
			default:
			}
			return nil
		})
		mgr := NewManager(slog.New(slog.DiscardHandler), WithMicroBFDActuator(actuator))
		defer mgr.Close()
		mgr.handleMicroBFDActuator(context.WithValue(t.Context(), key, "first"),
			MicroBFDMemberEvent{MemberInterface: "first"})
		if calls != 1 {
			t.Fatalf("actuator calls=%d, want 1", calls)
		}
		firstErr = first.Err()
		checkFirst()
		synctest.Sleep(time.Second)
		mgr.handleMicroBFDActuator(context.WithValue(t.Context(), key, "second"),
			MicroBFDMemberEvent{MemberInterface: "second"})
		checkFirst()
		if calls != 2 {
			t.Fatalf("actuator calls=%d, want 2", calls)
		}
	})
}
