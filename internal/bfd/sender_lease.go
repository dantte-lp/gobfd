package bfd

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type writeInterrupter interface{ InterruptWrite() error }

func closedSenderLeaseDone() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// SenderLease couples a session's packet sender with the resource release
// operation that owns its lifetime. Close is idempotent, blocks concurrent
// callers until teardown completes, and returns the same result to every
// caller. Synchronous release callbacks must use StartClose (and
// Manager.InitiateClose when requesting Manager shutdown) instead of
// recursively calling Close.
type SenderLease struct {
	sender        PacketSender
	release       func() error
	closeMu       sync.Mutex
	closing       bool
	closeDone     chan struct{}
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

func (l *SenderLease) beginClose() (bool, <-chan struct{}) {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	if l.closeDone == nil {
		l.closeDone = make(chan struct{})
	}
	if l.closing {
		return false, l.closeDone
	}
	l.closing = true
	return true, l.closeDone
}

func (l *SenderLease) runClose() {
	defer close(l.closeDone)
	l.stopInterrupt()
	err := l.interruptErr
	if l.release != nil {
		err = errors.Join(err, l.release())
	}
	l.err = err
}

// StartClose initiates sender release asynchronously if it has not already
// begun and returns a channel closed once teardown finishes. Unlike Close,
// StartClose never blocks and is safe to call from inside the lease's own
// synchronous release callback.
func (l *SenderLease) StartClose() <-chan struct{} {
	if l == nil {
		return closedSenderLeaseDone()
	}
	first, done := l.beginClose()
	if first {
		go l.runClose()
	}
	return done
}

// Close releases the sender resources at most once, blocking until completion
// and returning the same error to every caller. Do not call Close recursively
// from inside the lease's synchronous release callback; use StartClose instead.
func (l *SenderLease) Close() error {
	if l == nil {
		return nil
	}
	first, done := l.beginClose()
	if first {
		l.runClose()
	} else {
		<-done
	}
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
