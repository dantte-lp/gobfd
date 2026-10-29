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
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const sourcePortCapacity = 16384

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
	Echo             bool
	Sessions         []OwnerEndpoint
	Interval         time.Duration
	DetectMultiplier uint8
}

// OwnerSessionObservation separates sampled counters from the causal tuple.
type OwnerSessionObservation struct {
	Endpoint           OwnerEndpoint   `json:"endpoint"`
	Generation         uint64          `json:"generation"`
	Registered         bool            `json:"registered"`
	LocalDiscriminator uint32          `json:"local_discriminator"`
	SourcePort         uint16          `json:"source_port"`
	Observation        bfd.Observation `json:"observation"`
	Sent               uint64          `json:"sent"`
	Received           uint64          `json:"received"`
	NegotiatedTXNS     int64           `json:"negotiated_tx_ns"`
	DetectionNS        int64           `json:"detection_ns"`
}

// OwnerClockObservation associates a clock measurement with its receive binding.
type OwnerClockObservation struct {
	Binding     netip.AddrPort         `json:"binding"`
	Calibration bfd.ReceiveCalibration `json:"calibration"`
}

// OwnerObservation is sequential telemetry, not a reference or daemon verdict.
// Metrics retain the real collector DTOs, not a second implementation of them.
type OwnerObservation struct {
	stoppedAt                      time.Time                 `json:"-"`
	Scope                          string                    `json:"scope"`
	Echo                           bool                      `json:"echo"`
	Stopped                        bool                      `json:"stopped"`
	Closing                        bool                      `json:"closing"`
	StoppedAtUTC                   time.Time                 `json:"stopped_at_utc"`
	ReferenceQualified             bool                      `json:"reference_qualified"`
	TimingUncertaintyNS            *uint64                   `json:"timing_uncertainty_ns"`
	TimingUncertaintyError         string                    `json:"timing_uncertainty_error"`
	ConditionalTimingUncertaintyNS *uint64                   `json:"conditional_timing_uncertainty_ns"`
	Environment                    Environment               `json:"environment"`
	Sessions                       []OwnerSessionObservation `json:"sessions"`
	Delivery                       []bfd.DeliveryCounter     `json:"delivery"`
	Metrics                        []*dto.MetricFamily       `json:"metrics"`
	ClockCalibrations              []OwnerClockObservation   `json:"clock_calibrations"`
	ClockEpoch                     *ClockEpochObservation    `json:"clock_epoch"`
	Registrations                  OwnerRegistrations        `json:"registrations"`
	RetiredSessions                uint64                    `json:"retired_sessions"`
	RetiredSent                    uint64                    `json:"retired_sent"`
	RetiredReceived                uint64                    `json:"retired_received"`
	TerminalPackets                uint64                    `json:"terminal_packets"`
}

type ownerSession struct {
	endpoint   OwnerEndpoint
	session    workloadSession
	sourcePort uint16
	lease      *bfd.SenderLease
	generation uint64
}

type ownerListener struct {
	*netio.Listener

	binding netip.AddrPort
}

// Owner owns UDP listeners, senders and production receiver/session workers.
// Done closes after cleanup; Close is required to observe any shutdown error.
type Owner struct {
	manager      *bfd.Manager
	registry     *prometheus.Registry
	ports        *netio.SourcePortAllocator
	listeners    []ownerListener
	logger       *slog.Logger
	socket       config.SocketConfig
	inventory    sync.RWMutex
	closing      bool
	echo         bool
	retired      uint64
	retiredSent  uint64
	retiredRX    uint64
	sessions     []ownerSession
	receiveErrs  []error
	workers      sync.WaitGroup
	cancel       context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
	closeErr     error
	closeIOOnce  sync.Once
	closeIOError error
	stoppedAt    time.Time
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
	notifications, err := bfd.WithNotificationPopulation(len(cfg.Sessions))
	if err != nil {
		return nil, fmt.Errorf("owner notification population: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	registry := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(registry)
	owner := &Owner{
		echo:   cfg.Echo,
		socket: config.DefaultConfig().Socket,
		manager: bfd.NewManager(logger, notifications, bfd.WithManagerMetrics(collector),
			bfd.WithSessionInterfaceResolver(netio.ResolveSessionIfIndex)),
		registry: registry, ports: netio.NewSourcePortAllocator(), logger: logger, cancel: cancel, done: make(chan struct{}),
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
	if len(cfg.Sessions) == 0 || len(cfg.Sessions) > sourcePortCapacity || cfg.DetectMultiplier == 0 ||
		!ownerIntervalFitsWire(cfg.Interval) {
		return fmt.Errorf("owner session count or wire intervals: %w", errOwnerConfig)
	}
	type identity struct{ local, peer netip.Addr }
	seen := make(map[identity]struct{}, len(cfg.Sessions))
	interfaces := make(map[netip.AddrPort]string)
	for _, endpoint := range cfg.Sessions {
		local, peer := endpoint.Local.Addr(), endpoint.Peer.Addr()
		portValid := ownerReceivePort(endpoint.Local.Port())
		if cfg.Echo {
			portValid = endpoint.Local.Port() == netio.PortEcho
		}
		if !ownerUnicast(local) || !ownerUnicast(peer) || local.Is4() != peer.Is4() ||
			!portValid || endpoint.Peer.Port() == 0 {
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
		listenerConfig := netio.ListenerConfig{
			Addr: endpoint.Local.Addr(), Port: endpoint.Local.Port(),
			IfName: endpoint.Interface, Delivery: o.manager,
			ReadBufferSize: o.socket.ReadBufferSize,
		}
		if cfg.Echo {
			listenerConfig.ExpectedTTL = netio.TTLEchoLoopback
		}
		//nolint:contextcheck // Socket API has no context; cancellation and rollback are owner-scoped.
		listener, err := netio.NewListener(listenerConfig)
		if err != nil {
			return fmt.Errorf("bind owner listener %s: %w", endpoint.Local, err)
		}
		o.listeners = append(o.listeners, ownerListener{Listener: listener, binding: endpoint.Local})
		bound[endpoint.Local] = struct{}{}
	}
	o.workers.Go(func() { o.manager.RunDispatch(ctx) })
	for _, endpoint := range cfg.Sessions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("create owner sessions: %w", err)
		}
		entry, err := o.createSession(ctx, cfg, endpoint)
		if err != nil {
			return err
		}
		entry.generation = 1
		o.sessions = append(o.sessions, entry)
	}
	receive := netio.NewReceiver(o.manager, logger).Run
	if cfg.Echo {
		receive = netio.NewEchoReceiver(o.manager, logger).Run
	}
	o.receiveErrs = make([]error, len(o.listeners))
	for i, listener := range o.listeners {
		o.workers.Go(func() {
			o.receiveErrs[i] = receive(ctx, listener.Listener)
			if o.receiveErrs[i] != nil {
				o.cancel()
			}
		})
	}
	return nil
}

// createSession runs before publication or while the caller holds inventory.
func (o *Owner) createSession(ctx context.Context, cfg OwnerConfig, endpoint OwnerEndpoint) (ownerSession, error) {
	entry := ownerSession{endpoint: endpoint}
	factory := func() (*bfd.SenderLease, error) { //nolint:contextcheck // Socket API lacks context; leases own cleanup.
		port, err := o.ports.Allocate()
		if err != nil {
			return nil, fmt.Errorf("allocate owner source port: %w", err)
		}
		sender, err := netio.NewUDPSender(endpoint.Local.Addr(), port, false, o.logger,
			netio.WithDstPort(endpoint.Peer.Port()), netio.WithBindDevice(endpoint.Interface),
			netio.WithWriteBuffer(o.socket.WriteBufferSize))
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
		entry.sourcePort, entry.lease = port, lease
		return lease, nil
	}
	var err error
	if cfg.Echo {
		var session *bfd.EchoSession
		session, err = o.manager.CreateEchoSessionAs(ctx, bfd.EchoSessionConfig{
			LocalAddr: endpoint.Local.Addr(), PeerAddr: endpoint.Peer.Addr(), Interface: endpoint.Interface,
			TxInterval: cfg.Interval, DetectMultiplier: cfg.DetectMultiplier,
		}, factory, bfd.APISessionOwner("bfdload"))
		if err == nil {
			entry.session = echoOwnerSession{session}
		}
	} else {
		entry.session, err = o.manager.CreateSession(ctx, bfd.SessionConfig{
			LocalAddr: endpoint.Local.Addr(), PeerAddr: endpoint.Peer.Addr(), Interface: endpoint.Interface,
			Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
			DesiredMinTxInterval: cfg.Interval, RequiredMinRxInterval: cfg.Interval, DetectMultiplier: cfg.DetectMultiplier,
		}, factory)
	}
	if err != nil {
		if entry.lease != nil {
			err = errors.Join(err, entry.lease.Close())
		}
		return ownerSession{}, fmt.Errorf("create owner session %s to %s: %w", endpoint.Local, endpoint.Peer, err)
	}
	return entry, nil
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
		o.inventory.Lock()
		o.closing = true
		o.inventory.Unlock()
		o.cancel()
		o.closeListeners()
		o.workers.Wait()
		o.inventory.Lock()
		defer o.inventory.Unlock()
		o.manager.Close()
		o.closeErr = errors.Join(o.closeIOError, errors.Join(o.receiveErrs...))
		for _, entry := range o.sessions {
			if entry.lease != nil {
				o.closeErr = errors.Join(o.closeErr, entry.lease.Close())
			}
		}
		o.stoppedAt = time.Now()
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
	o.inventory.RLock()
	defer o.inventory.RUnlock()
	report := OwnerObservation{
		Scope: "production-core-owner-component", Environment: environment, Echo: o.echo,
		TimingUncertaintyError: "total clock uncertainty has not been independently established",
		Delivery:               o.manager.DeliveryCounters(), Sessions: make([]OwnerSessionObservation, 0, len(o.sessions)),
		ClockCalibrations: make([]OwnerClockObservation, len(o.listeners)),
		Closing:           o.closing, RetiredSessions: o.retired, RetiredSent: o.retiredSent, RetiredReceived: o.retiredRX,
	}
	select {
	case <-o.done:
		report.Stopped = true
		report.stoppedAt = o.stoppedAt
		report.StoppedAtUTC = o.stoppedAt.UTC()
	default:
	}
	statuses, _ := o.manager.WatchSessionsWithRevision()
	registered := make(map[bfd.SessionRef]bool, len(statuses))
	for _, status := range statuses {
		registered[status.Ref] = true
	}
	for _, entry := range o.sessions {
		if entry.session == nil {
			continue
		}
		report.Sessions = append(report.Sessions, OwnerSessionObservation{
			Endpoint: entry.endpoint, LocalDiscriminator: entry.session.LocalDiscriminator(), SourcePort: entry.sourcePort,
			Generation: entry.generation, Registered: registered[entry.session.Ref()],
			Observation: entry.session.Observation(),
			Sent:        entry.session.PacketsSent(), Received: entry.session.PacketsReceived(),
			NegotiatedTXNS: int64(entry.session.NegotiatedTxInterval()), DetectionNS: int64(entry.session.DetectionTime()),
		})
	}
	for i, listener := range o.listeners {
		report.ClockCalibrations[i] = OwnerClockObservation{
			Binding: listener.binding, Calibration: listener.ClockCalibration(),
		}
	}
	report.Metrics, err = o.registry.Gather()
	if err != nil {
		return OwnerObservation{}, fmt.Errorf("gather actual owner collector: %w", err)
	}
	report.Registrations, err = observeOwnerRegistrations(report.Metrics, len(statuses))
	if err != nil {
		return OwnerObservation{}, err
	}
	report.TerminalPackets, err = terminalPacketCount(report)
	if err != nil {
		return OwnerObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return OwnerObservation{}, fmt.Errorf("finish owner observation: %w", err)
	}
	return report, nil
}
