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
	// than the one recorded before its latest bind or rebind.
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
		{echoServiceName, 1<<sourceEcho | 1<<componentRuntime | 1<<componentEchoBindings},
		{microBFDServiceName, 1<<sourceMicro | 1<<componentRuntime | 1<<componentMicroBindings},
	}
}

// interfaceInventory lists the current interfaces; net.Interfaces in production.
type interfaceInventory func() ([]net.Interface, error)

// listenerBinding is one device-bound listener interface and the ifindex the
// kernel bound it to.
type listenerBinding struct {
	ifName    string
	ifIndex   int
	component readinessComponent
}

// daemonReadiness is the single writer of the aggregate "" health status and
// the bfd.v1 named services. Each status is the AND of the components the
// service needs. gobfd.gobgp and gobfd.management keep their own writers and
// stay outside the aggregate.
type daemonReadiness struct {
	mu       sync.Mutex
	ready    readinessMask
	bindings []listenerBinding
	checker  *grpchealth.StaticChecker
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

func (r *daemonReadiness) set(component readinessComponent, ready bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setLocked(1<<component, ready)
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

// bindingRecheckInterval bounds how long a listener interface change can go
// unnoticed when the interface monitor is disabled, stopped, or loses events.
const bindingRecheckInterval = 2 * time.Second

// recheckBindings runs check on every tick until ctx is cancelled. A failed
// inventory read keeps the previous state.
func recheckBindings(ctx context.Context, interval time.Duration, check func() error, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := check(); err != nil {
				logger.Warn("listener interface recheck failed; readiness unchanged",
					slog.String("error", err.Error()))
			}
		}
	}
}

// checkBindings re-evaluates every device-bound listener interface. A
// SO_BINDTODEVICE socket stays bound to the bind-time ifindex, so an interface
// recreated under the same name keeps the listener deaf until the listener
// owner rebinds it and records the new index. An inventory error leaves the
// state unchanged for the next check.
func (r *daemonReadiness) checkBindings(interfaces interfaceInventory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bindings) == 0 {
		// The last device-bound listener closed: nothing can be deaf.
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
		iface, ok := byName[binding.ifName]
		if !ok || iface.Index != binding.ifIndex ||
			iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 {
			ready &^= 1 << binding.component
		}
	}
	r.setLocked(bindingComponents, false)
	r.setLocked(ready, true)
	r.publishLocked()
	return nil
}

// runRequired runs a worker the dataplane cannot lose. A return while ctx is
// live marks the daemon NOT_SERVING, then the error cancels the errgroup.
func (r *daemonReadiness) runRequired(ctx context.Context, name string, run func() error) error {
	err := run()
	if ctx.Err() != nil {
		return err
	}
	r.set(componentRuntime, false)
	return fmt.Errorf("%s: %w", name, errors.Join(errRequiredWorkerStopped, err))
}
