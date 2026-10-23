//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/netio"
)

var errOwnerConfig = errors.New("invalid owner workload configuration")

// OwnerEndpoint identifies a receive binding and its remote receive endpoint.
// Alternate ports support isolated component fixtures, not RFC qualification.
type OwnerEndpoint struct {
	Local     netip.AddrPort `json:"local"`
	Peer      netip.AddrPort `json:"peer"`
	Interface string         `json:"interface,omitempty"`
}

// OwnerConfig configures the production core; it does not admit reference load.
type OwnerConfig struct {
	Sessions         []OwnerEndpoint
	Interval         time.Duration
	DetectMultiplier uint8
}

// OwnerSessionObservation separates sampled counters from the causal tuple.
type OwnerSessionObservation struct {
	Endpoint           OwnerEndpoint   `json:"endpoint"`
	LocalDiscriminator uint32          `json:"local_discriminator"`
	SourcePort         uint16          `json:"source_port"`
	Observation        bfd.Observation `json:"observation"`
	Sent               uint64          `json:"sent"`
	Received           uint64          `json:"received"`
	NegotiatedTXNS     int64           `json:"negotiated_tx_ns"`
	DetectionNS        int64           `json:"detection_ns"`
}

// OwnerObservation is sequential telemetry, not a reference or daemon verdict.
// Metrics retain the real collector DTOs, not a second implementation of them.
type OwnerObservation struct {
	Scope                  string                    `json:"scope"`
	Stopped                bool                      `json:"stopped"`
	ReferenceQualified     bool                      `json:"reference_qualified"`
	TimingUncertaintyNS    *uint64                   `json:"timing_uncertainty_ns"`
	TimingUncertaintyError string                    `json:"timing_uncertainty_error"`
	Environment            Environment               `json:"environment"`
	Sessions               []OwnerSessionObservation `json:"sessions"`
	Delivery               []bfd.DeliveryCounter     `json:"delivery"`
	Metrics                []*dto.MetricFamily       `json:"metrics"`
}

type ownerSession struct {
	endpoint   OwnerEndpoint
	session    *bfd.Session
	sourcePort uint16
}

// Owner owns UDP listeners, senders and production receiver/session workers.
// Done closes after cleanup; Close is required to observe any shutdown error.
type Owner struct {
	manager      *bfd.Manager
	registry     *prometheus.Registry
	ports        *netio.SourcePortAllocator
	listeners    []*netio.Listener
	leases       []*bfd.SenderLease
	sessions     []ownerSession
	receiveErrs  []error
	workers      sync.WaitGroup
	cancel       context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
	closeErr     error
	closeIOOnce  sync.Once
	closeIOError error
}

// StartOwner starts the actual UDP-to-session-owner path with real metrics.
// It does not create addresses, change host networking, or qualify support.
func StartOwner(ctx context.Context, cfg OwnerConfig, logger *slog.Logger) (*Owner, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start owner workload: %w", err)
	}
	if err := validateOwnerConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		return nil, fmt.Errorf("owner logger is nil: %w", errOwnerConfig)
	}
	runCtx, cancel := context.WithCancel(ctx)
	registry := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(registry)
	owner := &Owner{
		manager: bfd.NewManager(logger, bfd.WithManagerMetrics(collector),
			bfd.WithSessionInterfaceResolver(netio.ResolveSessionIfIndex)),
		registry: registry, ports: netio.NewSourcePortAllocator(), cancel: cancel, done: make(chan struct{}),
	}
	if err := owner.start(runCtx, cfg, logger); err != nil {
		return nil, errors.Join(err, owner.Close())
	}
	context.AfterFunc(runCtx, func() {
		if err := owner.Close(); err != nil {
			logger.Error("owner workload shutdown failed", slog.Any("error", err))
		}
	})
	if err := runCtx.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("owner startup canceled: %w", err), owner.Close())
	}
	return owner, nil
}

func validateOwnerConfig(cfg OwnerConfig) error {
	const sourcePortCapacity = 16384
	if len(cfg.Sessions) == 0 || len(cfg.Sessions) > sourcePortCapacity || cfg.DetectMultiplier == 0 ||
		!ownerIntervalFitsWire(cfg.Interval) {
		return fmt.Errorf("owner session count or wire intervals: %w", errOwnerConfig)
	}
	type identity struct{ local, peer netip.Addr }
	seen := make(map[identity]struct{}, len(cfg.Sessions))
	interfaces := make(map[netip.AddrPort]string)
	for _, endpoint := range cfg.Sessions {
		local, peer := endpoint.Local.Addr(), endpoint.Peer.Addr()
		if !ownerUnicast(local) || !ownerUnicast(peer) || local.Is4() != peer.Is4() ||
			!ownerReceivePort(endpoint.Local.Port()) || endpoint.Peer.Port() == 0 {
			return fmt.Errorf("owner endpoint %s to %s: %w", endpoint.Local, endpoint.Peer, errOwnerConfig)
		}
		key := identity{local: local, peer: peer}
		if _, found := seen[key]; found {
			return fmt.Errorf("duplicate owner wire identity %s to %s: %w", local, peer, errOwnerConfig)
		}
		seen[key] = struct{}{}
		if iface, found := interfaces[endpoint.Local]; found && iface != endpoint.Interface {
			return fmt.Errorf("conflicting owner listener interfaces for %s: %w", endpoint.Local, errOwnerConfig)
		}
		interfaces[endpoint.Local] = endpoint.Interface
	}
	return nil
}

func ownerUnicast(addr netip.Addr) bool {
	return addr.IsValid() && !addr.IsUnspecified() && !addr.IsMulticast() &&
		!addr.Is4In6() && addr.Zone() == "" && addr != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func ownerIntervalFitsWire(interval time.Duration) bool {
	return interval > 0 && interval%time.Microsecond == 0 && interval/time.Microsecond <= math.MaxUint32
}

func ownerReceivePort(port uint16) bool {
	const firstSourcePort = 49152
	// Echo classification and the TX allocator must never share Control RX bindings.
	return port > 0 && port < firstSourcePort && port != netio.PortEcho
}

func (o *Owner) start(ctx context.Context, cfg OwnerConfig, logger *slog.Logger) error {
	bound := make(map[netip.AddrPort]struct{})
	for _, endpoint := range cfg.Sessions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("bind owner listeners: %w", err)
		}
		if _, found := bound[endpoint.Local]; found {
			continue
		}
		//nolint:contextcheck // Socket API has no context; per-bind cancellation and rollback are owner-scoped.
		listener, err := netio.NewListener(netio.ListenerConfig{
			Addr: endpoint.Local.Addr(), Port: endpoint.Local.Port(),
			IfName: endpoint.Interface, Delivery: o.manager,
		})
		if err != nil {
			return fmt.Errorf("bind owner listener %s: %w", endpoint.Local, err)
		}
		o.listeners = append(o.listeners, listener)
		bound[endpoint.Local] = struct{}{}
	}
	o.workers.Go(func() { o.manager.RunDispatch(ctx) })
	for _, endpoint := range cfg.Sessions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("create owner sessions: %w", err)
		}
		if err := o.createSession(ctx, cfg, endpoint, logger); err != nil {
			return err
		}
	}
	receiver := netio.NewReceiver(o.manager, logger)
	o.receiveErrs = make([]error, len(o.listeners))
	for i, listener := range o.listeners {
		o.workers.Go(func() {
			o.receiveErrs[i] = receiver.Run(ctx, listener)
			if o.receiveErrs[i] != nil {
				o.cancel()
			}
		})
	}
	return nil
}

func (o *Owner) createSession(ctx context.Context, cfg OwnerConfig, endpoint OwnerEndpoint, logger *slog.Logger) error {
	var sourcePort uint16
	session, err := o.manager.CreateSession(ctx, bfd.SessionConfig{
		LocalAddr: endpoint.Local.Addr(), PeerAddr: endpoint.Peer.Addr(), Interface: endpoint.Interface,
		Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
		DesiredMinTxInterval: cfg.Interval, RequiredMinRxInterval: cfg.Interval, DetectMultiplier: cfg.DetectMultiplier,
	}, func() (*bfd.SenderLease, error) { //nolint:contextcheck // Socket API has no context; lease is owner-scoped.
		port, err := o.ports.Allocate()
		if err != nil {
			return nil, fmt.Errorf("allocate owner source port: %w", err)
		}
		sender, err := netio.NewUDPSender(endpoint.Local.Addr(), port, false, logger,
			netio.WithDstPort(endpoint.Peer.Port()), netio.WithBindDevice(endpoint.Interface))
		if err != nil {
			o.ports.Release(port)
			return nil, fmt.Errorf("bind owner sender %s:%d: %w", endpoint.Local.Addr(), port, err)
		}
		lease := bfd.NewSenderLease(sender, func() error {
			closeErr := sender.Close()
			o.ports.Release(port)
			if closeErr != nil {
				return fmt.Errorf("close owner sender %s:%d: %w", endpoint.Local.Addr(), port, closeErr)
			}
			return nil
		})
		sourcePort = port
		o.leases = append(o.leases, lease)
		return lease, nil
	})
	if err != nil {
		return fmt.Errorf("create owner session %s to %s: %w", endpoint.Local, endpoint.Peer, err)
	}
	o.sessions = append(o.sessions, ownerSession{endpoint: endpoint, session: session, sourcePort: sourcePort})
	return nil
}

// Done closes after cancellation or receive failure has completed cleanup.
func (o *Owner) Done() <-chan struct{} { return o.done }

func (o *Owner) closeListeners() {
	o.closeIOOnce.Do(func() {
		for _, listener := range o.listeners {
			o.closeIOError = errors.Join(o.closeIOError, listener.Close())
		}
	})
}

// Close cancels and joins every owned worker before releasing sender leases.
func (o *Owner) Close() error {
	o.closeOnce.Do(func() {
		o.cancel()
		o.closeListeners()
		o.workers.Wait()
		o.manager.Close()
		o.closeErr = errors.Join(o.closeIOError, errors.Join(o.receiveErrs...))
		for _, lease := range o.leases {
			o.closeErr = errors.Join(o.closeErr, lease.Close())
		}
		close(o.done)
	})
	return o.closeErr
}

// Snapshot samples real session state, the delivery ledger and collector.
// Missing calibration precision remains unknown even when timestamps are valid.
func (o *Owner) Snapshot(ctx context.Context) (OwnerObservation, error) {
	environment, err := CaptureEnvironment(ctx)
	if err != nil {
		return OwnerObservation{}, fmt.Errorf("sample owner environment: %w", err)
	}
	report := OwnerObservation{
		Scope: "production-core-owner-component", Environment: environment,
		TimingUncertaintyError: "listener calibration does not expose a measurement uncertainty bound",
		Delivery:               o.manager.DeliveryCounters(), Sessions: make([]OwnerSessionObservation, len(o.sessions)),
	}
	select {
	case <-o.done:
		report.Stopped = true
	default:
	}
	for i, entry := range o.sessions {
		report.Sessions[i] = OwnerSessionObservation{
			Endpoint: entry.endpoint, LocalDiscriminator: entry.session.LocalDiscriminator(), SourcePort: entry.sourcePort,
			Observation: entry.session.Observation(),
			Sent:        entry.session.PacketsSent(), Received: entry.session.PacketsReceived(),
			NegotiatedTXNS: int64(entry.session.NegotiatedTxInterval()), DetectionNS: int64(entry.session.DetectionTime()),
		}
	}
	report.Metrics, err = o.registry.Gather()
	if err != nil {
		return OwnerObservation{}, fmt.Errorf("gather actual owner collector: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return OwnerObservation{}, fmt.Errorf("finish owner observation: %w", err)
	}
	return report, nil
}
