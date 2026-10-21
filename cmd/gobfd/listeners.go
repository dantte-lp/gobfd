package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// BFD Listeners — receive sockets owned per reconciliation generation
// -------------------------------------------------------------------------

var errListenerOwnerClosed = errors.New("listener owner is closed")

// listenerKey identifies one receive socket.
type listenerKey struct {
	addr     netip.Addr
	ifName   string
	port     uint16
	multiHop bool
}

func listenerKeyOf(cfg netio.ListenerConfig) listenerKey {
	return listenerKey{addr: cfg.Addr, ifName: cfg.IfName, port: cfg.Port, multiHop: cfg.MultiHop}
}

func (k listenerKey) String() string {
	return fmt.Sprintf("%s port %d interface %q multihop=%t", k.addr, k.port, k.ifName, k.multiHop)
}

// deviceBound reports a socket bound to its interface with SO_BINDTODEVICE.
func (k listenerKey) deviceBound() bool {
	return k.ifName != "" && !k.multiHop
}

func (k listenerKey) component() readinessComponent {
	switch k.port {
	case netio.PortEcho:
		return componentEchoBindings
	case netio.PortMicroBFD:
		return componentMicroBindings
	default:
		return componentControlBindings
	}
}

// listenerClaims records who needs a listener: the startup-owned unsolicited
// interfaces and the reconciliation sources, one bit per source.
type listenerClaims uint8

const claimUnsolicited listenerClaims = 1 << 7

func sourceClaim(source reconciliationSource) listenerClaims {
	return listenerClaims(sourceMask(source))
}

type ownedListener struct {
	ln *netio.Listener
	// ifIndex is resolved before the latest bind: an interface recreated in
	// between reads as a stale binding, never as a false ready one.
	ifIndex      int
	claims       listenerClaims
	stop         context.CancelFunc
	rebindFailed bool
}

// listenerOwner is the single lifecycle owner of the BFD receive sockets.
// Startup opens the configured set; each reconciliation source then claims
// the listeners of its desired set before the Manager apply and releases the
// unclaimed ones after a converged apply. A device-bound listener follows an
// interface recreated with a new ifindex by rebinding the same socket.
type listenerOwner struct {
	mu        sync.Mutex
	listeners map[listenerKey]*ownedListener
	order     []listenerKey // bind order; readiness lists bindings in it
	closed    bool

	open       func(netio.ListenerConfig) (*netio.Listener, error)
	rebind     func(*netio.Listener) error
	interfaces interfaceInventory
	sessions   func() []bfd.SessionSnapshot

	delivery  bfd.DeliveryReporter
	control   *netio.Receiver
	echo      *netio.EchoReceiver
	readiness *daemonReadiness
	logger    *slog.Logger

	workers sync.WaitGroup
	lost    chan error
}

func newListenerOwner(mgr *bfd.Manager, readiness *daemonReadiness, logger *slog.Logger) *listenerOwner {
	return &listenerOwner{
		listeners:  make(map[listenerKey]*ownedListener),
		open:       netio.NewListener,
		rebind:     (*netio.Listener).RebindDevice,
		interfaces: net.Interfaces,
		sessions:   mgr.Sessions,
		delivery:   mgr,
		control:    netio.NewReceiver(mgr, logger),
		echo:       netio.NewEchoReceiver(mgr, logger),
		readiness:  readiness,
		logger:     logger,
		lost:       make(chan error, 1),
	}
}

// startListenerOwner opens the startup listeners, then runs the owner's
// fail-stop worker and the periodic binding recheck. Any listener failure
// fails startup.
func startListenerOwner(
	ctx context.Context, g *errgroup.Group, cfg *config.Config, owner *listenerOwner, logger *slog.Logger,
) error {
	if err := owner.openStartup(ctx, cfg); err != nil {
		return err
	}
	g.Go(func() error { return owner.run(ctx) })
	// Interface events re-check bindings promptly; the ticker covers a
	// disabled or stopped interface monitor.
	g.Go(func() error {
		recheckBindings(ctx, bindingRecheckInterval, func() error {
			return owner.checkBindings(net.Interfaces)
		}, logger)
		return nil
	})
	return nil
}

// openStartup opens every configured listener. Wildcard unsolicited listeners
// bind first so sessions with an empty interface keep their receive path.
func (o *listenerOwner) openStartup(ctx context.Context, cfg *config.Config) error {
	for _, step := range []struct {
		claims  listenerClaims
		configs []netio.ListenerConfig
	}{
		{claimUnsolicited, unsolicitedListenerConfigs(cfg)},
		{sourceClaim(sourceBase), sessionListenerConfigs(cfg)},
		{sourceClaim(sourceEcho), echoListenerConfigs(cfg)},
		{sourceClaim(sourceMicro), microListenerConfigs(cfg)},
	} {
		if err := o.acquire(ctx, step.claims, step.configs); err != nil {
			o.close()
			return err
		}
	}
	return nil
}

// acquire adds claims to the listeners of configs and opens the missing
// ones. A failure closes the listeners this call opened. ctx bounds the
// receivers it starts. An absent interface is a typed unavailable resource.
func (o *listenerOwner) acquire(ctx context.Context, claims listenerClaims, configs []netio.ListenerConfig) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errListenerOwnerClosed
	}
	var indexes map[string]int
	var opened []listenerKey
	for _, cfg := range configs {
		key := listenerKeyOf(cfg)
		if owned, ok := o.listeners[key]; ok {
			owned.claims |= claims
			continue
		}
		var index int
		if key.deviceBound() {
			if indexes == nil {
				var err error
				if indexes, err = o.interfaceIndexes(); err != nil {
					o.stopLocked(opened)
					return err
				}
			}
			index = indexes[key.ifName]
		}
		cfg.Delivery = o.delivery
		ln, err := o.open(cfg)
		if err != nil {
			o.stopLocked(opened)
			return listenerOpenError(key, err)
		}
		o.startLocked(ctx, key, ln, index, claims)
		opened = append(opened, key)
	}
	if len(opened) != 0 {
		o.publishLocked()
	}
	return nil
}

func listenerOpenError(key listenerKey, err error) error {
	if key.ifName != "" && errors.Is(err, syscall.ENODEV) {
		return fmt.Errorf("open listener %s: %s: %w", key, err.Error(), bfd.NewResourceUnavailableError(bfd.ResourceRef{
			Kind: bfd.ResourceKindInterface, ID: key.ifName,
		}))
	}
	return fmt.Errorf("open listener %s: %w", key, err)
}

func (o *listenerOwner) interfaceIndexes() (map[string]int, error) {
	current, err := o.interfaces()
	if err != nil {
		return nil, fmt.Errorf("resolve listener interface indexes: %w", err)
	}
	indexes := make(map[string]int, len(current))
	for _, iface := range current {
		indexes[iface.Name] = iface.Index
	}
	return indexes, nil
}

// startLocked runs one required receiver per listener. A deliberate stop
// cancels the receiver context before closing the socket, so only a socket
// lost while its receiver context is live fails the daemon.
func (o *listenerOwner) startLocked(
	ctx context.Context, key listenerKey, ln *netio.Listener, index int, claims listenerClaims,
) {
	receiverCtx, stop := context.WithCancel(ctx)
	o.listeners[key] = &ownedListener{ln: ln, ifIndex: index, claims: claims, stop: stop}
	o.order = append(o.order, key)
	name, run := "control receiver", o.control.Run
	if component := key.component(); component == componentEchoBindings {
		name, run = "echo receiver", o.echo.Run
	} else if component == componentMicroBindings {
		name = "micro-BFD receiver"
	}
	o.workers.Go(func() {
		err := o.readiness.runRequired(receiverCtx, name, func() error { return run(receiverCtx, ln) })
		if errors.Is(err, errRequiredWorkerStopped) {
			select {
			case o.lost <- err:
			default:
			}
		}
	})
	o.logger.Info("BFD listener started", slog.String("addr", key.addr.String()),
		slog.Uint64("port", uint64(key.port)), slog.Bool("multi_hop", key.multiHop),
		slog.String("interface", key.ifName))
}

func (o *listenerOwner) stopLocked(keys []listenerKey) {
	for _, key := range keys {
		owned := o.listeners[key]
		owned.stop()
		if err := owned.ln.Close(); err != nil {
			o.logger.Warn("failed to close BFD listener", slog.String("error", err.Error()))
		}
		delete(o.listeners, key)
		o.order = slices.DeleteFunc(o.order, func(k listenerKey) bool { return k == key })
		o.logger.Info("BFD listener stopped", slog.String("addr", key.addr.String()),
			slog.Uint64("port", uint64(key.port)), slog.String("interface", key.ifName))
	}
}

// release drops claims outside the desired configs and closes each listener
// left without a claim, unless a live session still receives on it.
func (o *listenerOwner) release(claims listenerClaims, configs []netio.ListenerConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	desired := make(map[listenerKey]struct{}, len(configs))
	for _, cfg := range configs {
		desired[listenerKeyOf(cfg)] = struct{}{}
	}
	var unclaimed []listenerKey
	for _, key := range o.order {
		owned := o.listeners[key]
		if _, keep := desired[key]; !keep {
			owned.claims &^= claims
		}
		if owned.claims == 0 && !o.inUseLocked(key) {
			unclaimed = append(unclaimed, key)
		}
	}
	if len(unclaimed) != 0 {
		o.stopLocked(unclaimed)
		o.publishLocked()
	}
}

// inUseLocked keeps a control listener that an API-created session still
// receives on.
func (o *listenerOwner) inUseLocked(key listenerKey) bool {
	if key.port != netio.PortSingleHop && key.port != netio.PortMultiHop {
		return false
	}
	for _, session := range o.sessions() {
		if session.Type != bfd.SessionTypeSingleHop && session.Type != bfd.SessionTypeMultiHop {
			continue
		}
		if session.LocalAddr == key.addr && (session.Type == bfd.SessionTypeMultiHop) == key.multiHop &&
			sessionInterface(session.Interface, session.Type, session.LocalAddr, session.PeerAddr) == key.ifName {
			return true
		}
	}
	return false
}

// checkBindings rebinds every device-bound listener whose interface now
// carries another ifindex, then re-evaluates the binding readiness against
// the same inventory. A failed rebind keeps the binding not ready and is
// retried on the next check.
func (o *listenerOwner) checkBindings(interfaces interfaceInventory) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.ContainsFunc(o.order, listenerKey.deviceBound) {
		return o.readiness.watchBindings(nil, interfaces)
	}
	current, err := interfaces()
	if err != nil {
		return fmt.Errorf("list interfaces for listener bindings: %w", err)
	}
	rebound := false
	for _, key := range o.order {
		owned := o.listeners[key]
		if !key.deviceBound() {
			continue
		}
		index := slices.IndexFunc(current, func(iface net.Interface) bool { return iface.Name == key.ifName })
		if index < 0 || current[index].Index == owned.ifIndex {
			continue
		}
		if err := o.rebind(owned.ln); err != nil {
			if !owned.rebindFailed {
				o.logger.Warn("BFD listener rebind failed; binding stays not ready",
					slog.String("listener", key.String()), slog.String("error", err.Error()))
			}
			owned.rebindFailed = true
			continue
		}
		owned.ifIndex, owned.rebindFailed, rebound = current[index].Index, false, true
		o.logger.Info("BFD listener rebound to recreated interface",
			slog.String("listener", key.String()), slog.Int("ifindex", owned.ifIndex))
	}
	snapshot := func() ([]net.Interface, error) { return current, nil }
	if rebound {
		return o.readiness.watchBindings(o.bindingsLocked(), snapshot)
	}
	return o.readiness.checkBindings(snapshot)
}

func (o *listenerOwner) bindingsLocked() []listenerBinding {
	var bindings []listenerBinding
	for _, key := range o.order {
		if key.deviceBound() {
			bindings = append(bindings, listenerBinding{
				ifName: key.ifName, ifIndex: o.listeners[key].ifIndex, component: key.component(),
			})
		}
	}
	return slices.Compact(bindings)
}

func (o *listenerOwner) publishLocked() {
	if err := o.readiness.watchBindings(o.bindingsLocked(), o.interfaces); err != nil {
		o.logger.Warn("listener interface check failed; readiness unchanged", slog.String("error", err.Error()))
	}
}

// run returns the first receiver loss; the errgroup then stops the daemon.
func (o *listenerOwner) run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-o.lost:
		return err
	}
}

// close stops every listener and waits for its receiver. It is idempotent.
func (o *listenerOwner) close() {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		o.stopLocked(slices.Clone(o.order))
	}
	o.mu.Unlock()
	o.workers.Wait()
}

// unsolicitedListenerConfigs returns the IPv4 and IPv6 wildcard listeners of
// every enabled RFC 9468 interface. They are startup-owned.
func unsolicitedListenerConfigs(cfg *config.Config) []netio.ListenerConfig {
	if !cfg.Unsolicited.Enabled {
		return nil
	}
	names := make([]string, 0, len(cfg.Unsolicited.Interfaces))
	for name, ifCfg := range cfg.Unsolicited.Interfaces {
		if ifCfg.Enabled {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var configs []netio.ListenerConfig
	for _, name := range names {
		for _, addr := range []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()} {
			configs = append(configs, netio.ListenerConfig{
				Addr: addr, IfName: name, Port: netio.PortSingleHop,
				ReadBufferSize: cfg.Socket.ReadBufferSize,
			})
		}
	}
	return configs
}

// sessionListenerConfigs returns the control listeners of the configured
// base sessions: port 3784 for single-hop and 4784 for multi-hop.
func sessionListenerConfigs(cfg *config.Config) []netio.ListenerConfig {
	var configs []netio.ListenerConfig
	for _, sc := range cfg.Sessions {
		localAddr, err := sc.LocalAddr()
		if err != nil || !localAddr.IsValid() {
			continue
		}

		multiHop := sc.Type == "multi_hop"
		peerAddr, err := sc.PeerAddr()
		if err != nil {
			continue // validateSessions rejects an invalid peer.
		}
		lnCfg := netio.ListenerConfig{
			ReadBufferSize: cfg.Socket.ReadBufferSize,
			Addr:           localAddr,
			IfName:         sessionInterface(sc.Interface, configSessionTypeToBFD(sc.Type), localAddr, peerAddr),
			MultiHop:       multiHop,
		}
		if multiHop {
			lnCfg.Port = netio.PortMultiHop
		} else {
			lnCfg.Port = netio.PortSingleHop
		}
		configs = append(configs, lnCfg)
	}
	return configs
}

// controlListenerConfigs puts wildcard listeners before specific listeners so
// configured sessions with an empty interface retain their receive path.
func controlListenerConfigs(cfg *config.Config) []netio.ListenerConfig {
	return append(unsolicitedListenerConfigs(cfg), sessionListenerConfigs(cfg)...)
}

// echoListenerConfigs returns one echo listener per unique local address,
// bound to the interface of the first peer with that address, while Echo is
// enabled.
func echoListenerConfigs(cfg *config.Config) []netio.ListenerConfig {
	if !cfg.Echo.Enabled {
		return nil
	}
	seen := make(map[netip.Addr]struct{})
	var configs []netio.ListenerConfig
	for _, ep := range cfg.Echo.Peers {
		localAddr, err := ep.LocalAddr()
		if err != nil || !localAddr.IsValid() {
			continue
		}
		if _, exists := seen[localAddr]; exists {
			continue
		}
		seen[localAddr] = struct{}{}
		configs = append(configs, netio.ListenerConfig{
			ReadBufferSize: cfg.Socket.ReadBufferSize,
			Addr:           localAddr,
			IfName:         ep.Interface,
			Port:           netio.PortEcho,
			MultiHop:       false,
			ExpectedTTL:    netio.TTLEchoLoopback,
		})
	}
	return configs
}

// compileListenerConfigs returns the receive listeners each reconciliation
// source needs. Overlay backends keep their startup-owned sockets.
func compileListenerConfigs(cfg *config.Config) [sourceCount][]netio.ListenerConfig {
	var configs [sourceCount][]netio.ListenerConfig
	configs[sourceBase] = sessionListenerConfigs(cfg)
	configs[sourceEcho] = echoListenerConfigs(cfg)
	configs[sourceMicro] = microListenerConfigs(cfg)
	return configs
}
