package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"sync"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

var errOverlayOwnerClosed = errors.New("overlay owner is closed")

// receiverGroup runs owner-scoped required receivers. A receiver lives until
// its owner stops it, independently of the request that opened its socket.
// A deliberate stop cancels the receiver before its socket closes, so only a
// socket lost while its receiver is live reaches run and fails the daemon.
type receiverGroup struct {
	readiness *daemonReadiness
	workers   sync.WaitGroup
	lost      chan error
}

func newReceiverGroup(readiness *daemonReadiness) *receiverGroup {
	return &receiverGroup{readiness: readiness, lost: make(chan error, 1)}
}

// start runs one required receiver and returns its deliberate stop.
func (g *receiverGroup) start(name string, run func(context.Context) error) context.CancelFunc {
	ctx, stop := context.WithCancel(context.Background())
	g.workers.Go(func() {
		err := g.readiness.runRequired(ctx, name, func() error { return run(ctx) })
		if errors.Is(err, errRequiredWorkerStopped) {
			select {
			case g.lost <- err:
			default:
			}
		}
	})
	return stop
}

// run returns the first receiver loss; the errgroup then stops the daemon.
func (g *receiverGroup) run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-g.lost:
		return err
	}
}

func (g *receiverGroup) wait() {
	g.workers.Wait()
}

// scopedOverlayConn is an overlay socket whose exact tunnel identities change
// in place, so adding or removing an identity never replaces a live socket.
type scopedOverlayConn interface {
	netio.OverlayConn
	SetScopes(scopes []bfd.TransportScope) error
}

// overlayKey identifies one overlay backend: one UDP socket per tunnel kind
// and outer local address (RFC 8971 port 4789, RFC 9521 port 6081).
type overlayKey struct {
	kind  bfd.TransportScopeKind
	local netip.Addr
}

type ownedBackend struct {
	conn   scopedOverlayConn
	scopes []bfd.TransportScope
	refs   map[bfd.TransportScope]int
	stop   context.CancelFunc
}

// overlayOwner is the lifecycle owner of the VXLAN and Geneve backends. The
// VXLAN and Geneve sources claim the identities of their desired set before
// the Manager apply: a missing local endpoint opens a backend, an existing
// one retains the union of its live and desired identities. After an
// apply without failed or resource-pending entries the source narrows every
// backend to its desired identities and closes a backend left without one.
// A failed apply keeps live scopes and current retry preparations.
type overlayOwner struct {
	mu       sync.Mutex
	backends map[overlayKey]*ownedBackend
	closed   bool

	open      func(kind bfd.TransportScopeKind, local netip.Addr, scopes []bfd.TransportScope) (scopedOverlayConn, error)
	receive   func(netio.OverlayConn) func(context.Context) error
	receivers *receiverGroup
	logger    *slog.Logger
}

// newOverlayOwner builds userspace-udp backends with the startup-owned
// backend type and socket buffers. Each backend leases its inner source port
// from the shared RFC 5881 allocator and returns it on close.
func newOverlayOwner(
	cfg *config.Config, sf *udpSenderFactory, mgr *bfd.Manager, readiness *daemonReadiness, logger *slog.Logger,
) *overlayOwner {
	return &overlayOwner{
		backends: make(map[overlayKey]*ownedBackend),
		open: func(kind bfd.TransportScopeKind, local netip.Addr, scopes []bfd.TransportScope) (scopedOverlayConn, error) {
			return openOverlayBackend(cfg, sf, kind, local, scopes, logger)
		},
		receive: func(conn netio.OverlayConn) func(context.Context) error {
			return netio.NewOverlayReceiver(conn, mgr, logger).Run
		},
		receivers: newReceiverGroup(readiness),
		logger:    logger,
	}
}

func openOverlayBackend(
	cfg *config.Config, sf *udpSenderFactory, kind bfd.TransportScopeKind,
	local netip.Addr, scopes []bfd.TransportScope, logger *slog.Logger,
) (scopedOverlayConn, error) {
	return leaseOverlayBackend(sf.portAlloc, func(srcPort uint16) (netio.OverlayConn, error) {
		if kind == bfd.TransportScopeVXLAN {
			return netio.NewVXLANOverlayBackend(netio.VXLANOverlayBackendConfig{
				Backend: netio.OverlayBackendType(cfg.VXLAN.Backend), LocalAddr: local,
				SourcePort: srcPort, Logger: logger, Scopes: scopes,
				ReadBufferSize: cfg.Socket.ReadBufferSize, WriteBufferSize: cfg.Socket.WriteBufferSize,
			})
		}
		return netio.NewGeneveOverlayBackend(netio.GeneveOverlayBackendConfig{
			Backend: netio.OverlayBackendType(cfg.Geneve.Backend), LocalAddr: local,
			SourcePort: srcPort, Logger: logger, Scopes: scopes,
			ReadBufferSize: cfg.Socket.ReadBufferSize, WriteBufferSize: cfg.Socket.WriteBufferSize,
		})
	})
}

// leaseOverlayBackend leases the inner source port a backend sends from and
// ties its release to the backend's close; a failed build returns it.
func leaseOverlayBackend(
	alloc *netio.SourcePortAllocator, build func(srcPort uint16) (netio.OverlayConn, error),
) (scopedOverlayConn, error) {
	srcPort, err := alloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("allocate overlay inner source port: %w", err)
	}
	conn, err := build(srcPort)
	if err != nil {
		alloc.Release(srcPort)
		return nil, fmt.Errorf("open overlay backend: %w", err)
	}
	return &leasedOverlayConn{OverlayConn: conn, portAlloc: alloc, port: srcPort}, nil
}

// overlayClaim is the outcome of one claim: the backend of every claimed
// scope and the error of every local endpoint that could not claim.
type overlayClaim struct {
	conns    map[bfd.TransportScope]netio.OverlayConn
	failures map[netip.Addr]error
}

// acquire claims scopes of one kind per local endpoint. An endpoint that
// cannot open or widen reports its error and leaves its new scopes
// unclaimed. Live scopes stay claimed; obsolete unused preparations are pruned
// after pure preflight. Endpoint errors are classified before a source apply.
func (o *overlayOwner) acquire(kind bfd.TransportScopeKind, scopes []bfd.TransportScope) (overlayClaim, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return overlayClaim{}, errOverlayOwnerClosed
	}
	groups := overlayScopeGroups(scopes)
	if err := o.validateScopeChangesLocked(kind, groups); err != nil {
		return overlayClaim{}, err
	}
	o.pruneLocked(kind, scopes)
	claim := overlayClaim{
		conns:    make(map[bfd.TransportScope]netio.OverlayConn, len(scopes)),
		failures: make(map[netip.Addr]error),
	}
	for _, local := range slices.SortedFunc(maps.Keys(groups), netip.Addr.Compare) {
		key, group := overlayKey{kind: kind, local: local}, groups[local]
		err := o.claimLocked(key, group)
		if err != nil {
			claim.failures[local] = err
		}
		backend, ok := o.backends[key]
		if !ok {
			continue
		}
		for _, scope := range group {
			if err == nil || slices.Contains(backend.scopes, scope) {
				claim.conns[scope] = backend.conn
			}
		}
	}
	return claim, nil
}

// Same-wire scopes cannot coexist during the old session's drain.
func (o *overlayOwner) validateScopeChangesLocked(
	kind bfd.TransportScopeKind, groups map[netip.Addr][]bfd.TransportScope,
) error {
	for local, group := range groups {
		backend := o.backends[overlayKey{kind: kind, local: local}]
		for i, scope := range group {
			if slices.ContainsFunc(group[:i], func(other bfd.TransportScope) bool {
				return netio.SameOverlayWireIdentity(scope, other)
			}) {
				return fmt.Errorf("duplicate overlay wire identity: %w", bfd.ErrSessionParameterConflict)
			}
			if backend != nil && slices.ContainsFunc(backend.scopes, func(current bfd.TransportScope) bool {
				return backend.refs[current] > 0 && current != scope && netio.SameOverlayWireIdentity(current, scope)
			}) {
				return fmt.Errorf("overlay scope replacement requires separate removal: %w", bfd.ErrSessionParameterConflict)
			}
		}
	}
	return nil
}

// claimLocked merges group into the backend of key, or opens it.
func (o *overlayOwner) claimLocked(key overlayKey, group []bfd.TransportScope) error {
	if backend, ok := o.backends[key]; ok {
		merged := mergeOverlayScopes(backend.scopes, group)
		if sameOverlayScopeSet(merged, backend.scopes) {
			return nil
		}
		if err := backend.conn.SetScopes(merged); err != nil {
			return fmt.Errorf("widen overlay backend %s: %w", key.local, err)
		}
		backend.scopes = merged
		return nil
	}
	conn, err := o.open(key.kind, key.local, group)
	if err != nil {
		return fmt.Errorf("open overlay backend %s: %w", key.local, addressUnavailableError(key.local, err))
	}
	o.startLocked(key, conn, group)
	return nil
}

func (o *overlayOwner) startLocked(key overlayKey, conn scopedOverlayConn, scopes []bfd.TransportScope) {
	name := "Geneve receiver"
	if key.kind == bfd.TransportScopeVXLAN {
		name = "VXLAN receiver"
	}
	stop := o.receivers.start(name, o.receive(conn))
	o.backends[key] = &ownedBackend{conn: conn, scopes: scopes, refs: make(map[bfd.TransportScope]int), stop: stop}
	o.logger.Info("overlay backend started", slog.String("receiver", name),
		slog.String("local", key.local.String()), slog.Int("identities", len(scopes)))
}

func (o *overlayOwner) stopLocked(keys []overlayKey) {
	for _, key := range keys {
		backend := o.backends[key]
		backend.stop()
		if err := backend.conn.Close(); err != nil {
			o.logger.Warn("failed to close overlay backend", slog.String("error", err.Error()))
		}
		delete(o.backends, key)
		o.logger.Info("overlay backend stopped", slog.String("local", key.local.String()))
	}
}

// release prunes scopes absent from desired after their last session lease closes.
func (o *overlayOwner) release(kind bfd.TransportScopeKind, scopes []bfd.TransportScope) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pruneLocked(kind, scopes)
}

func (o *overlayOwner) pruneLocked(kind bfd.TransportScopeKind, scopes []bfd.TransportScope) {
	groups := overlayScopeGroups(scopes)
	var empty []overlayKey
	for key, backend := range o.backends {
		if key.kind != kind {
			continue
		}
		want := make([]bfd.TransportScope, 0, len(backend.scopes))
		for _, scope := range backend.scopes {
			if backend.refs[scope] > 0 || slices.Contains(groups[key.local], scope) {
				want = append(want, scope)
			}
		}
		if len(want) == 0 {
			empty = append(empty, key)
			continue
		}
		if sameOverlayScopeSet(want, backend.scopes) {
			continue
		}
		if err := backend.conn.SetScopes(want); err != nil {
			o.logger.Warn("failed to narrow overlay backend; keeping its identities",
				slog.String("local", key.local.String()), slog.String("error", err.Error()))
			continue
		}
		backend.scopes = want
	}
	o.stopLocked(empty)
}

// A physical session retains its exact scope through retirement and sender cleanup.
func (o *overlayOwner) senderLeaseFactory(conn netio.OverlayConn, scope bfd.TransportScope) bfd.SenderLeaseFactory {
	return func() (*bfd.SenderLease, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.closed {
			return nil, fmt.Errorf("lease overlay scope: %w", errOverlayOwnerClosed)
		}
		backend := o.backends[overlayKey{kind: scope.Kind, local: scope.OuterLocalAddr.Unmap()}]
		if backend == nil || backend.conn != conn || !slices.Contains(backend.scopes, scope) {
			return nil, fmt.Errorf("lease overlay scope: %w", netio.ErrOverlayIdentityMismatch)
		}
		backend.refs[scope]++
		return bfd.NewSenderLease(netio.NewOverlaySender(conn, scope), func() error {
			o.mu.Lock()
			defer o.mu.Unlock()
			if backend.refs[scope] <= 1 {
				delete(backend.refs, scope)
			} else {
				backend.refs[scope]--
			}
			return nil
		}), nil
	}
}

// run returns the first backend receiver loss.
func (o *overlayOwner) run(ctx context.Context) error {
	return o.receivers.run(ctx)
}

// close stops every backend and waits for its receiver. It is idempotent.
func (o *overlayOwner) close() {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		o.stopLocked(slices.Collect(maps.Keys(o.backends)))
	}
	o.mu.Unlock()
	o.receivers.wait()
}

func overlayScopeGroups(scopes []bfd.TransportScope) map[netip.Addr][]bfd.TransportScope {
	groups := make(map[netip.Addr][]bfd.TransportScope)
	for _, scope := range scopes {
		local := scope.OuterLocalAddr.Unmap()
		groups[local] = append(groups[local], scope)
	}
	return groups
}

// mergeOverlayScopes widens a prevalidated identity set. Preflight excludes
// unequal scopes sharing one wire identity; exact duplicates are retained once.
func mergeOverlayScopes(current, desired []bfd.TransportScope) []bfd.TransportScope {
	merged := slices.Clone(desired)
	for _, scope := range current {
		if !slices.ContainsFunc(merged, func(kept bfd.TransportScope) bool {
			return netio.SameOverlayWireIdentity(kept, scope)
		}) {
			merged = append(merged, scope)
		}
	}
	return merged
}

func sameOverlayScopeSet(a, b []bfd.TransportScope) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(scope bfd.TransportScope) bool {
		return !slices.Contains(b, scope)
	})
}

// overlayScopeKind maps an overlay reconciliation source to its scope kind.
func overlayScopeKind(source reconciliationSource) bfd.TransportScopeKind {
	if source == sourceVXLAN {
		return bfd.TransportScopeVXLAN
	}
	return bfd.TransportScopeGeneve
}

// claimOverlaySource wraps an overlay source apply with backend claims; the
// conns are resolved at apply time, so a retry never reuses a closed one.
// An incomplete acquisition leaves the previous source untouched: pending for
// unavailable addresses, failed otherwise. Acquired supersets stay owned for retry.
func (c *reconciliationCoordinator) claimOverlaySource(
	ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate,
	apply sourceApplyFunc,
) sourceApplyResult {
	index := 0
	if source == sourceGeneve {
		index = 1
	}
	overlay := candidate.overlays[index]
	scopes := make([]bfd.TransportScope, 0, len(overlay.desired))
	for _, desired := range overlay.desired {
		scopes = append(scopes, desired.SessionConfig.TransportScope)
	}
	kind := overlayScopeKind(source)
	//nolint:contextcheck // Backend receivers are owner-scoped, not bound to one reconciliation pass.
	claim, err := c.overlays.acquire(kind, scopes)
	if err != nil {
		if errors.Is(err, bfd.ErrSessionParameterConflict) {
			return failedSourceResult(bfd.ReconcileErrorConflict, fmt.Errorf("claim %s backends: %w", source, err))
		}
		return failedSourceResult(bfd.ReconcileErrorCreate, fmt.Errorf("claim %s backends: %w", source, err))
	}
	claimed := compiledOverlayCandidate{owner: overlay.owner}
	appliedScopes := make([]bfd.TransportScope, 0, len(scopes))
	var unclaimed sourceApplyResult
	for i, desired := range overlay.desired {
		conn, ok := claim.conns[scopes[i]]
		if !ok {
			if _, unavailable := bfd.UnavailableResource(claim.failures[scopes[i].OuterLocalAddr.Unmap()]); unavailable {
				unclaimed.Pending++
			} else {
				unclaimed.Failed++
				unclaimed.Errors[bfd.ReconcileErrorCreate]++
			}
			continue
		}
		desired.SenderLeaseFactory = c.overlays.senderLeaseFactory(conn, scopes[i])
		claimed.desired = append(claimed.desired, desired)
		claimed.conns = append(claimed.conns, conn)
		appliedScopes = append(appliedScopes, scopes[i])
	}
	failures := make([]error, 0, len(claim.failures))
	for _, local := range slices.SortedFunc(maps.Keys(claim.failures), netip.Addr.Compare) {
		failures = append(failures, fmt.Errorf("claim %s backend: %w", source, claim.failures[local]))
	}
	if unclaimed.Pending != 0 || unclaimed.Failed != 0 {
		unclaimed.Err = errors.Join(failures...)
		return unclaimed
	}
	candidate.overlays[index] = claimed
	result := apply(ctx, source, candidate)
	if result.Failed == 0 && result.Pending == result.TimerPending {
		c.overlays.release(kind, appliedScopes)
	}
	result.Err = errors.Join(append([]error{result.Err}, failures...)...)
	return result
}
