package config

import (
	"errors"
	"fmt"
	"net/netip"
)

// ErrAmbiguousListenerInterface indicates conflicting interfaces for one listener key.
var ErrAmbiguousListenerInterface = errors.New("ambiguous listener interface binding")

// ListenerBindingField identifies the bounded configuration field causing a conflict.
type ListenerBindingField uint8

const (
	ListenerBindingControlInterface ListenerBindingField = iota
	ListenerBindingEchoInterface
)

func (f ListenerBindingField) String() string {
	switch f {
	case ListenerBindingControlInterface:
		return "sessions[].interface"
	case ListenerBindingEchoInterface:
		return "echo.peers[].interface"
	default:
		return "unknown_listener_binding"
	}
}

// ListenerBindingConflictError reports only a field ID, never configuration values.
type ListenerBindingConflictError struct {
	field ListenerBindingField
}

func (e *ListenerBindingConflictError) Error() string {
	return fmt.Sprintf("%v: %s", ErrAmbiguousListenerInterface, e.field)
}

func (e *ListenerBindingConflictError) Unwrap() error { return ErrAmbiguousListenerInterface }

// FieldID returns the finite configuration identifier that conflicts.
func (e *ListenerBindingConflictError) FieldID() ListenerBindingField { return e.field }

func validateControlListenerBindings(sessions []SessionConfig) error {
	type listenerKey struct {
		local    netip.Addr
		multiHop bool
	}
	seen := make(map[listenerKey]string, len(sessions))
	for _, session := range sessions {
		local, err := session.LocalAddr()
		if err != nil || !local.IsValid() {
			continue
		}
		key := listenerKey{local: local, multiHop: session.Type == "multi_hop"}
		if previous, exists := seen[key]; exists && previous != session.Interface {
			return &ListenerBindingConflictError{field: ListenerBindingControlInterface}
		}
		seen[key] = session.Interface
	}
	return nil
}

func validateEchoListenerBindings(peers []EchoPeerConfig) error {
	seen := make(map[netip.Addr]string, len(peers))
	for _, peer := range peers {
		local, err := peer.LocalAddr()
		if err != nil || !local.IsValid() {
			continue
		}
		if previous, exists := seen[local]; exists && previous != peer.Interface {
			return &ListenerBindingConflictError{field: ListenerBindingEchoInterface}
		}
		seen[local] = peer.Interface
	}
	return nil
}
