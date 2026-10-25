package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/server"
)

// errRequiredWorkerStopped reports that a dataplane worker returned while the
// daemon context was live. The daemon fails stop instead of reporting a
// degraded state; the service manager restarts it.
var errRequiredWorkerStopped = errors.New("required dataplane worker stopped while the daemon is running")

// readinessComponent indexes one readiness input. Components below
// sourceCount are the reconciliation sources, in reconciliationSource order.
type readinessComponent uint8

const (
	// componentRuntime is false after a required worker stops or shutdown begins.
	componentRuntime readinessComponent = readinessComponent(sourceCount) + iota
	// The binding components are false while a device-bound listener's
	// interface is missing, not up and running, or carries an ifindex other
	// than the one recorded before its latest bind or rebind; local bind
	// addresses must also remain available.
	componentControlBindings
	componentEchoBindings
	componentMicroBindings
	componentCount
)

type readinessMask uint16

const (
	allSourceComponents    readinessMask = 1<<sourceCount - 1
	allReadinessComponents readinessMask = 1<<componentCount - 1
	bindingComponents      readinessMask = 1<<componentControlBindings | 1<<componentEchoBindings |
		1<<componentMicroBindings
	echoReadinessComponents readinessMask = 1<<sourceEcho | 1<<componentRuntime | 1<<componentEchoBindings
)

type readinessService struct {
	name  string
	needs readinessMask
}

// readinessServices maps each health service to the components it needs.
func readinessServices() [4]readinessService {
	return [...]readinessService{
		{"", allReadinessComponents},
		{bfdServiceName, 1<<sourceBase | 1<<sourceVXLAN | 1<<sourceGeneve |
			1<<componentRuntime | 1<<componentControlBindings},
		{echoServiceName, echoReadinessComponents},
		{microBFDServiceName, 1<<sourceMicro | 1<<componentRuntime | 1<<componentMicroBindings},
	}
}

// interfaceInventory lists the current interfaces; net.Interfaces in production.
type interfaceInventory func() ([]net.Interface, error)

// listenerBinding records a local receive address and optional device binding.
type listenerBinding struct {
	ifName    string
	ifIndex   int
	component readinessComponent
	address   netio.LocalAddress
}

// daemonReadiness is the single writer of the aggregate "" health status and
// the bfd.v1 named services. Each status is the AND of the components the
// service needs. gobfd.gobgp and gobfd.management keep their own writers and
// stay outside the aggregate.
type daemonReadiness struct {
	mu             sync.Mutex
	ready          readinessMask
	bindings       []listenerBinding
	checker        *grpchealth.StaticChecker
	capabilities   server.CapabilitySnapshot
	checkAddresses localAddressAvailabilityChecker
}

func (r *daemonReadiness) setEchoEnabled(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.capabilities.Echo.Enabled = enabled
}

func (r *daemonReadiness) echoAPIReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.capabilities.Echo.Enabled && r.ready&echoReadinessComponents == echoReadinessComponents
}

// Begin a validated generation without exposing new flags with old source bits.
func (r *daemonReadiness) beginCapabilities(snapshot server.CapabilitySnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready &^= allSourceComponents
	r.capabilities = snapshot
	r.publishLocked()
}

// newDaemonReadiness starts with every reconciliation source not ready; the
// runtime and binding components start ready because listener creation fails
// the process before any health endpoint serves.
func newDaemonReadiness(checker *grpchealth.StaticChecker) *daemonReadiness {
	r := &daemonReadiness{checker: checker, ready: allReadinessComponents &^ allSourceComponents}
	r.publishLocked()
	return r
}

// setSources replaces the reconciliation source components.
func (r *daemonReadiness) setSources(ready reconciliationSourceMask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = r.ready&^allSourceComponents | readinessMask(ready)
	r.publishLocked()
}

func (r *daemonReadiness) setNotReady(component readinessComponent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setLocked(1<<component, false)
	r.publishLocked()
}

func (r *daemonReadiness) setLocked(mask readinessMask, ready bool) {
	if ready {
		r.ready |= mask
	} else {
		r.ready &^= mask
	}
}

// publishLocked runs under mu so concurrent writers cannot publish out of
// order; StaticChecker.SetStatus never calls back into the aggregator.
func (r *daemonReadiness) publishLocked() {
	if r.checker == nil {
		return
	}
	for _, service := range readinessServices() {
		status := grpchealth.StatusNotServing
		if r.ready&service.needs == service.needs {
			status = grpchealth.StatusServing
		}
		r.checker.SetStatus(service.name, status)
	}
}

// watchBindings registers the device-bound listener interfaces and checks
// them against the current inventory, which also covers an interface event
// lost between the bind and the registration.
func (r *daemonReadiness) watchBindings(bindings []listenerBinding, interfaces interfaceInventory) error {
	r.mu.Lock()
	r.bindings = bindings
	r.mu.Unlock()
	return r.checkBindings(interfaces)
}

// bindingRecheckInterval schedules resource checks even without interface events.
const bindingRecheckInterval = 2 * time.Second

// recheckBindings runs check on every tick until ctx is cancelled.
func recheckBindings(ctx context.Context, interval time.Duration, check func() error, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := check(); err != nil {
				logger.Warn("listener resource recheck failed",
					slog.String("error", err.Error()))
			}
		}
	}
}

// checkBindings re-evaluates listener addresses and device-bound interfaces. A
// SO_BINDTODEVICE socket stays bound to the bind-time ifindex, so an interface
// recreated under the same name keeps the listener deaf until the listener
// owner rebinds it and records the new index. Interface inventory errors retain
// the previous state; address inventory errors make the affected component unready.
func (r *daemonReadiness) checkBindings(interfaces interfaceInventory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bindings) == 0 {
		// The last listener closed: no binding dependencies remain.
		if r.ready&bindingComponents != bindingComponents {
			r.setLocked(bindingComponents, true)
			r.publishLocked()
		}
		return nil
	}
	current, err := interfaces()
	if err != nil {
		return fmt.Errorf("list interfaces for listener bindings: %w", err)
	}
	byName := make(map[string]net.Interface, len(current))
	for _, iface := range current {
		byName[iface.Name] = iface
	}
	ready := bindingComponents
	for _, binding := range r.bindings {
		if binding.ifName == "" {
			continue
		}
		iface, ok := byName[binding.ifName]
		if !ok || iface.Index != binding.ifIndex ||
			iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 {
			ready &^= 1 << binding.component
		}
	}
	ready, addressErr := r.bindingAddressReadiness(ready)
	r.setLocked(bindingComponents, false)
	r.setLocked(ready, true)
	r.publishLocked()
	return addressErr
}

func (r *daemonReadiness) bindingAddressReadiness(ready readinessMask) (readinessMask, error) {
	if r.checkAddresses == nil {
		return ready, nil
	}
	var addressErrors []error
	for component := componentControlBindings; component <= componentMicroBindings; component++ {
		var addresses []netio.LocalAddress
		for _, binding := range r.bindings {
			if binding.component == component {
				addresses = append(addresses, binding.address)
			}
		}
		if _, err := r.checkAddresses(addresses); err != nil {
			ready &^= 1 << component
			if !errors.Is(err, bfd.ErrResourceUnavailable) {
				addressErrors = append(addressErrors, err)
			}
		}
	}
	return ready, errors.Join(addressErrors...)
}

// runRequired runs a worker the dataplane cannot lose. A return while ctx is
// live marks the daemon NOT_SERVING, then the error cancels the errgroup.
func (r *daemonReadiness) runRequired(ctx context.Context, name string, run func() error) error {
	err := run()
	if ctx.Err() != nil {
		return err
	}
	r.setNotReady(componentRuntime)
	return fmt.Errorf("%s: %w", name, errors.Join(errRequiredWorkerStopped, err))
}
