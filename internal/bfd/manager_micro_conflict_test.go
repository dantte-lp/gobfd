package bfd_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type microBFDConfigConflict interface {
	error
	ConflictingLAGInterface() string
}

type reconcileLogHandler struct {
	mu       sync.Mutex
	messages []string
}

func (*reconcileLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *reconcileLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	h.messages = append(h.messages, record.Message)
	h.mu.Unlock()
	return nil
}

func (h *reconcileLogHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *reconcileLogHandler) WithGroup(string) slog.Handler {
	return h
}

func (h *reconcileLogHandler) reset() {
	h.mu.Lock()
	h.messages = nil
	h.mu.Unlock()
}

func (h *reconcileLogHandler) contains(message string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Contains(h.messages, message)
}

func TestManagerReconcileMicroBFDGroupsIdenticalSameKeyIsNoOp(t *testing.T) {
	handler := &reconcileLogHandler{}
	mgr := bfd.NewManager(slog.New(handler))
	defer mgr.Close()

	cfg := defaultMicroBFDConfig()
	createdGroup, err := mgr.CreateMicroBFDGroup(cfg)
	if err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	handler.reset()

	desired := cfg
	desired.MemberLinks = []string{"eth1", "eth0"}
	created, destroyed, err := mgr.ReconcileMicroBFDGroups([]bfd.MicroBFDReconcileConfig{{
		Key: desired.LAGInterface, Config: desired,
	}})
	if err != nil {
		t.Fatalf("ReconcileMicroBFDGroups: %v", err)
	}
	if created != 0 || destroyed != 0 {
		t.Fatalf("ReconcileMicroBFDGroups counts = (%d, %d), want (0, 0)", created, destroyed)
	}

	gotGroup, ok := mgr.LookupMicroBFDGroup(cfg.LAGInterface)
	if !ok {
		t.Fatal("existing micro-BFD group was removed")
	}
	if gotGroup != createdGroup {
		t.Fatal("identical same-key reconciliation replaced the existing group")
	}
}

func TestManagerReconcileMicroBFDGroupsRejectsDivergentSameKey(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*bfd.MicroBFDConfig)
	}{
		{
			name: "peer address",
			mutate: func(cfg *bfd.MicroBFDConfig) {
				cfg.PeerAddr = netip.MustParseAddr("10.0.0.9")
			},
		},
		{
			name: "local address",
			mutate: func(cfg *bfd.MicroBFDConfig) {
				cfg.LocalAddr = netip.MustParseAddr("10.0.0.10")
			},
		},
		{
			name: "member links",
			mutate: func(cfg *bfd.MicroBFDConfig) {
				cfg.MemberLinks = []string{"eth0", "eth2"}
			},
		},
		{
			name: "minimum active links",
			mutate: func(cfg *bfd.MicroBFDConfig) {
				cfg.MinActiveLinks = 2
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &reconcileLogHandler{}
			mgr := bfd.NewManager(slog.New(handler))
			defer mgr.Close()

			original := defaultMicroBFDConfig()
			originalGroup, err := mgr.CreateMicroBFDGroup(original)
			if err != nil {
				t.Fatalf("CreateMicroBFDGroup: %v", err)
			}
			before := originalGroup.Snapshot()
			handler.reset()

			divergent := original
			divergent.MemberLinks = slices.Clone(original.MemberLinks)
			tt.mutate(&divergent)
			created, destroyed, reconcileErr := mgr.ReconcileMicroBFDGroups(
				[]bfd.MicroBFDReconcileConfig{{Key: divergent.LAGInterface, Config: divergent}},
			)
			if reconcileErr == nil {
				t.Fatal("ReconcileMicroBFDGroups error = nil, want typed configuration conflict")
			}
			if !errors.Is(reconcileErr, bfd.ErrMicroBFDConfigConflict) {
				t.Fatalf("ReconcileMicroBFDGroups error = %v, want ErrMicroBFDConfigConflict", reconcileErr)
			}
			_, ok := errors.AsType[*bfd.MicroBFDConfigConflictError](reconcileErr)
			if !ok {
				t.Fatalf("ReconcileMicroBFDGroups error type = %T, want *MicroBFDConfigConflictError", reconcileErr)
			}
			var conflict microBFDConfigConflict
			if !errors.As(reconcileErr, &conflict) {
				t.Fatalf("ReconcileMicroBFDGroups error type = %T, want micro-BFD configuration conflict", reconcileErr)
			}
			if got := conflict.ConflictingLAGInterface(); got != original.LAGInterface {
				t.Errorf("conflicting LAG = %q, want %q", got, original.LAGInterface)
			}
			if created != 0 || destroyed != 0 {
				t.Errorf("ReconcileMicroBFDGroups counts = (%d, %d), want (0, 0)", created, destroyed)
			}

			gotGroup, ok := mgr.LookupMicroBFDGroup(original.LAGInterface)
			if !ok {
				t.Fatal("conflicting reconciliation removed the existing group")
			}
			if gotGroup != originalGroup {
				t.Error("conflicting reconciliation replaced the existing group")
			}
			if got := gotGroup.Snapshot(); !microBFDGroupSnapshotsEqual(got, before) {
				t.Errorf("group snapshot after conflict = %#v, want %#v", got, before)
			}
			if handler.contains("micro-BFD group reconciliation complete") {
				t.Error("conflicting reconciliation emitted a success completion log")
			}
		})
	}
}

func TestManagerReconcileMicroBFDGroupsRejectsInvalidDesiredSetBeforeMutation(t *testing.T) {
	tests := []struct {
		name    string
		desired func(bfd.MicroBFDConfig) []bfd.MicroBFDReconcileConfig
		wantErr error
	}{
		{
			name: "duplicate member",
			desired: func(cfg bfd.MicroBFDConfig) []bfd.MicroBFDReconcileConfig {
				cfg.MemberLinks = []string{"eth0", "eth0"}
				return []bfd.MicroBFDReconcileConfig{{Key: cfg.LAGInterface, Config: cfg}}
			},
			wantErr: bfd.ErrMicroBFDDuplicateMember,
		},
		{
			name: "key mismatch",
			desired: func(cfg bfd.MicroBFDConfig) []bfd.MicroBFDReconcileConfig {
				return []bfd.MicroBFDReconcileConfig{{Key: "bond-other", Config: cfg}}
			},
			wantErr: bfd.ErrMicroBFDConfigConflict,
		},
		{
			name: "duplicate key",
			desired: func(cfg bfd.MicroBFDConfig) []bfd.MicroBFDReconcileConfig {
				return []bfd.MicroBFDReconcileConfig{
					{Key: cfg.LAGInterface, Config: cfg},
					{Key: cfg.LAGInterface, Config: cfg},
				}
			},
			wantErr: bfd.ErrMicroBFDGroupExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
			defer mgr.Close()

			original := defaultMicroBFDConfig()
			group, err := mgr.CreateMicroBFDGroup(original)
			if err != nil {
				t.Fatalf("CreateMicroBFDGroup: %v", err)
			}
			before := group.Snapshot()

			created, destroyed, reconcileErr := mgr.ReconcileMicroBFDGroups(tt.desired(original))
			if !errors.Is(reconcileErr, tt.wantErr) {
				t.Fatalf("ReconcileMicroBFDGroups error = %v, want %v", reconcileErr, tt.wantErr)
			}
			if created != 0 || destroyed != 0 {
				t.Errorf("ReconcileMicroBFDGroups counts = (%d, %d), want (0, 0)", created, destroyed)
			}
			got, ok := mgr.LookupMicroBFDGroup(original.LAGInterface)
			if !ok || got != group {
				t.Fatal("invalid desired set replaced or removed the existing group")
			}
			if snapshot := got.Snapshot(); !microBFDGroupSnapshotsEqual(snapshot, before) {
				t.Errorf("group snapshot after invalid candidate = %#v, want %#v", snapshot, before)
			}
		})
	}
}

func microBFDGroupSnapshotsEqual(a, b bfd.MicroBFDGroupSnapshot) bool {
	return a.LAGInterface == b.LAGInterface &&
		a.PeerAddr == b.PeerAddr &&
		a.LocalAddr == b.LocalAddr &&
		a.AggregateUp == b.AggregateUp &&
		a.UpCount == b.UpCount &&
		a.MemberCount == b.MemberCount &&
		a.MinActiveLinks == b.MinActiveLinks &&
		memberStateSetsEqual(a.Members, b.Members)
}

func memberStateSetsEqual(a, b []bfd.MemberLinkState) bool {
	if len(a) != len(b) {
		return false
	}
	for _, member := range a {
		if !slices.Contains(b, member) {
			return false
		}
	}
	return true
}
