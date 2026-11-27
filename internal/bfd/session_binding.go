package bfd

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// InterfaceRebinder restores a sender's existing socket to the expected
// incarnation of its configured device without changing its source port.
type InterfaceRebinder interface {
	RebindInterface(ctx context.Context, ifName string, ifIndex int) error
}

// ErrInterfaceRebindUnsupported requires explicit operator recovery.
var ErrInterfaceRebindUnsupported = errors.New("sender does not support retained interface rebinding")

// receiveBinding is immutable; pointer identity is the RX incarnation token.
type receiveBinding struct {
	ifIndex int
	pending bool
}

type interfaceBindingRequest struct {
	apply  func(context.Context, *time.Timer, *time.Timer) error
	result chan error
}

func (s *Session) rebindInterface(
	ctx context.Context, index int, commit func(context.Context, *receiveBinding, *receiveBinding) error,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rebind session interface: %w", err)
	}
	if index <= 0 {
		return fmt.Errorf("rebind session interface index %d: %w", index, ErrInterfaceUnresolved)
	}
	pending, err := s.prepareReceiveBinding(index)
	if err != nil {
		return err
	}
	if !pending.pending {
		return nil
	}
	request := s.newInterfaceBindingRequest(ctx, index, pending, commit)
	if err := s.enqueueInterfaceBinding(ctx, pending, request); err != nil {
		return err
	}
	select {
	case err := <-request.result:
		return err
	case <-s.ownerDone:
		return ErrSessionStopped
	case <-ctx.Done():
		return fmt.Errorf("wait for interface binding: %w", ctx.Err())
	}
}

func (s *Session) prepareReceiveBinding(index int) (*receiveBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.receiveBinding.Load()
	if s.updateClosed {
		return nil, ErrSessionStopped
	}
	if prior != nil && !prior.pending && prior.ifIndex == index {
		return prior, nil
	}
	pending := &receiveBinding{pending: true}
	if prior != nil {
		pending.ifIndex = prior.ifIndex
	}
	s.receiveBinding.Store(pending)
	if s.adminDrain != nil {
		s.adminDrain.hadPendingBinding = true
	}
	return pending, nil
}

func (s *Session) newInterfaceBindingRequest(
	ctx context.Context, index int, pending *receiveBinding,
	commit func(context.Context, *receiveBinding, *receiveBinding) error,
) *interfaceBindingRequest {
	request := &interfaceBindingRequest{result: make(chan error, 1)}
	request.apply = func(ownerCtx context.Context, tx, detect *time.Timer) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("start interface binding: %w", err)
		}
		if err := ownerCtx.Err(); err != nil {
			return fmt.Errorf("interface binding owner: %w", err)
		}
		if s.receiveBinding.Load() != pending {
			return ErrInterfaceUnresolved
		}
		if s.cachedState != StateAdminDown && (s.cachedState != StateDown || s.LocalDiag() != DiagPathDown) {
			s.handlePathDown(ownerCtx, tx, detect)
		}
		rebinder, ok := s.sender.(InterfaceRebinder)
		if !ok || s.ifName == "" {
			return ErrInterfaceRebindUnsupported
		}
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(ownerCtx, cancel)
		defer stop()
		defer cancel()
		if err := rebinder.RebindInterface(callCtx, s.ifName, index); err != nil {
			return fmt.Errorf("rebind session sender: %w", err)
		}
		if err := callCtx.Err(); err != nil {
			return fmt.Errorf("commit interface binding: %w", err)
		}
		next := &receiveBinding{ifIndex: index}
		if commit != nil {
			return commit(callCtx, pending, next)
		}
		return s.commitReceiveBinding(callCtx, pending, next)
	}
	return request
}

func (s *Session) enqueueInterfaceBinding(
	ctx context.Context, pending *receiveBinding, request *interfaceBindingRequest,
) error {
	defer s.signalControlSpace()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("admit interface binding: %w", err)
		}
		s.mu.Lock()
		if s.updateClosed || s.receiveBinding.Load() != pending {
			s.mu.Unlock()
			return ErrSessionStopped
		}
		select {
		case s.ctrlCh <- sessionControl{kind: controlInterfaceBinding, binding: request}:
			s.mu.Unlock()
			return nil
		default:
			s.mu.Unlock()
		}
		select {
		case <-s.ctrlSpace:
		case <-s.ownerDone:
			return ErrSessionStopped
		case <-ctx.Done():
			return fmt.Errorf("admit interface binding: %w", ctx.Err())
		}
	}
}

func (s *Session) commitReceiveBinding(ctx context.Context, pending, next *receiveBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("commit receive binding: %w", err)
	}
	if s.updateClosed {
		return ErrSessionStopped
	}
	if s.receiveBinding.Load() != pending {
		return ErrInterfaceUnresolved
	}
	s.receiveBinding.Store(next)
	return nil
}

func (s *Session) suspendReceiveBinding() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateClosed {
		return ErrSessionStopped
	}
	prior := s.receiveBinding.Load()
	if prior != nil && prior.pending {
		return nil
	}
	pending := &receiveBinding{pending: true}
	if prior != nil {
		pending.ifIndex = prior.ifIndex
	}
	s.receiveBinding.Store(pending)
	if s.adminDrain != nil {
		s.adminDrain.hadPendingBinding = true
	}
	return nil
}

func (s *Session) suspendInterfacePath() error {
	if err := s.suspendReceiveBinding(); err != nil {
		return err
	}
	if s.State() == StateAdminDown || (s.State() == StateDown && s.LocalDiag() == DiagPathDown) {
		return nil
	}
	if s.SetPathDown() == DeliveryClosed {
		return ErrSessionStopped
	}
	return nil
}
