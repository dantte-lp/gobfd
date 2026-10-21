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
	stop   context.CancelFunc
}

// overlayOwner is the lifecycle owner of the VXLAN and Geneve backends. The
// VXLAN and Geneve sources claim the identities of their desired set before
// the Manager apply: a missing local endpoint opens a backend, an existing
// one widens to the union of its current and desired identities. After an
// apply without failed or resource-pending entries the source narrows every
// backend to its desired identities and closes a backend left without one.
// A failed apply keeps the superset, so live sessions keep their identity.
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

// acquire claims scopes of one kind and returns the backend of each scope.
// A failure closes the backends this call opened; widened backends keep
// the superset until the next clean release.
func (o *overlayOwner) acquire(
	kind bfd.TransportScopeKind, scopes []bfd.TransportScope,
) (map[bfd.TransportScope]netio.OverlayConn, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, errOverlayOwnerClosed
	}
	groups := overlayScopeGroups(scopes)
	conns := make(map[bfd.TransportScope]netio.OverlayConn, len(scopes))
	var opened []overlayKey
	for _, local := range slices.SortedFunc(maps.Keys(groups), netip.Addr.Compare) {
		key, group := overlayKey{kind: kind, local: local}, groups[local]
		created, err := o.claimLocked(key, group)
		if err != nil {
			o.stopLocked(opened)
			return nil, err
		}
		if created {
			opened = append(opened, key)
		}
		for _, scope := range group {
			conns[scope] = o.backends[key].conn
		}
	}
	return conns, nil
}

// claimLocked widens the backend of key to include group, or opens it. It
// reports whether it opened a new backend.
func (o *overlayOwner) claimLocked(key overlayKey, group []bfd.TransportScope) (bool, error) {
	if backend, ok := o.backends[key]; ok {
		union := unionOverlayScopes(backend.scopes, group)
		if len(union) == len(backend.scopes) {
			return false, nil
		}
		if err := backend.conn.SetScopes(union); err != nil {
			return false, fmt.Errorf("widen overlay backend %s: %w", key.local, err)
		}
		backend.scopes = union
		return false, nil
	}
	conn, err := o.open(key.kind, key.local, group)
	if err != nil {
		return false, fmt.Errorf("open overlay backend %s: %w", key.local, addressUnavailableError(key.local, err))
	}
	o.startLocked(key, conn, group)
	return true, nil
}

func (o *overlayOwner) startLocked(key overlayKey, conn scopedOverlayConn, scopes []bfd.TransportScope) {
	name := "Geneve receiver"
	if key.kind == bfd.TransportScopeVXLAN {
		name = "VXLAN receiver"
	}
	stop := o.receivers.start(name, o.receive(conn))
	o.backends[key] = &ownedBackend{conn: conn, scopes: scopes, stop: stop}
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

// release narrows every backend of kind to the desired scopes and closes a
// backend left without one.
func (o *overlayOwner) release(kind bfd.TransportScopeKind, scopes []bfd.TransportScope) {
	o.mu.Lock()
	defer o.mu.Unlock()
	groups := overlayScopeGroups(scopes)
	var empty []overlayKey
	for key, backend := range o.backends {
		if key.kind != kind {
			continue
		}
		want := groups[key.local]
		if len(want) == 0 {
			empty = append(empty, key)
			continue
		}
		if len(want) == len(backend.scopes) {
			continue // Claims only widen, so equal size means equal sets.
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

func unionOverlayScopes(current, desired []bfd.TransportScope) []bfd.TransportScope {
	union := slices.Clone(current)
	for _, scope := range desired {
		if !slices.Contains(union, scope) {
			union = append(union, scope)
		}
	}
	return union
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
	byScope, err := c.overlays.acquire(kind, scopes)
	if err != nil {
		return resourceErrorSourceResult(1, fmt.Errorf("claim %s backends: %w", source, err))
	}
	overlay.conns = make([]netio.OverlayConn, len(scopes))
	for i, scope := range scopes {
		overlay.conns[i] = byScope[scope]
	}
	candidate.overlays[index] = overlay
	result := apply(ctx, source, candidate)
	if result.Failed == 0 && result.Pending == result.TimerPending {
		c.overlays.release(kind, scopes)
	}
	return result
}
