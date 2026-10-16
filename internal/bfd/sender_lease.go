package bfd

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type writeInterrupter interface{ InterruptWrite() error }

// SenderLease couples a session's packet sender with the resource release
// operation that owns its lifetime. Close is idempotent and returns the same
// result to every caller.
type SenderLease struct {
	sender        PacketSender
	release       func() error
	once          sync.Once
	err           error
	interruptStop func() bool
	interruptDone chan struct{}
	interruptJoin sync.Once
	interruptErr  error
}

func (l *SenderLease) bindInterrupt(ctx context.Context) {
	if l == nil || l.release == nil {
		return
	}
	sender, ok := l.sender.(writeInterrupter)
	if !ok {
		return
	}
	l.interruptDone = make(chan struct{})
	l.interruptStop = context.AfterFunc(ctx, func() {
		defer close(l.interruptDone)
		if err := sender.InterruptWrite(); err != nil {
			l.interruptErr = fmt.Errorf("interrupt owned sender write: %w", err)
		}
	})
}

func (l *SenderLease) stopInterrupt() {
	if l == nil || l.interruptStop == nil {
		return
	}
	l.interruptJoin.Do(func() {
		if !l.interruptStop() {
			<-l.interruptDone
		}
	})
}

// NewSenderLease creates an owning sender lease. A nil release function makes
// the lease explicitly non-owning, which is appropriate for shared transport
// backends whose lifetime is managed outside an individual BFD session.
func NewSenderLease(sender PacketSender, release func() error) *SenderLease {
	return &SenderLease{sender: sender, release: release}
}

// Sender returns the packet sender held by the lease.
func (l *SenderLease) Sender() PacketSender {
	if l == nil {
		return nil
	}
	return l.sender
}

// Close releases the sender resources at most once.
func (l *SenderLease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.stopInterrupt()
		l.err = l.interruptErr
		if l.release != nil {
			l.err = errors.Join(l.err, l.release())
		}
	})
	return l.err
}

// SenderLeaseFactory lazily acquires the sender for one new physical session.
// If a factory returns both a non-nil lease and an error, Manager closes the
// partial lease before returning the error.
type SenderLeaseFactory func() (*SenderLease, error)

// NonOwningSenderLeaseFactory returns a factory for a sender whose lifetime is
// managed by another component, such as a shared overlay backend.
func NonOwningSenderLeaseFactory(sender PacketSender) SenderLeaseFactory {
	return func() (*SenderLease, error) {
		return NewSenderLease(sender, nil), nil
	}
}
