package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sync"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/server"
)

const (
	bfdServiceName      = "bfd.v1.BfdService"
	echoServiceName     = "bfd.v1.EchoService"
	microBFDServiceName = "bfd.v1.MicroBFDService"
)

var (
	errOverlayBackendUnavailable       = errors.New("overlay backend is not running")
	errNilControlSessionCandidate      = errors.New("nil control-session candidate")
	errUnknownReconciliationSource     = errors.New("unknown reconciliation source")
	errInvalidInterfacePreflightResult = errors.New("invalid interface availability preflight result")
)

type reconciliationSource uint8

const (
	sourceBase reconciliationSource = iota
	sourceEcho
	sourceMicro
	sourceVXLAN
	sourceGeneve
)

const sourceCount = int(sourceGeneve) + 1

type reconciliationSourceMask uint8

// allReconciliationSources selects every reconciliation source.
const allReconciliationSources reconciliationSourceMask = 1<<sourceCount - 1

type interfaceEventAvailability uint8

const (
	interfaceEventUnavailable interfaceEventAvailability = iota
	interfaceEventAvailable
)

func sourceMask(source reconciliationSource) reconciliationSourceMask {
	if int(source) >= sourceCount {
		return 0
	}
	return 1 << source
}

func (m reconciliationSourceMask) contains(source reconciliationSource) bool {
	return m&sourceMask(source) != 0
}

func reconciliationSources() [sourceCount]reconciliationSource {
	return [sourceCount]reconciliationSource{
		sourceBase,
		sourceEcho,
		sourceMicro,
		sourceVXLAN,
		sourceGeneve,
	}
}

func (s reconciliationSource) String() string {
	switch s {
	case sourceBase:
		return "base"
	case sourceEcho:
		return "echo"
	case sourceMicro:
		return "micro"
	case sourceVXLAN:
		return "vxlan"
	case sourceGeneve:
		return "geneve"
	default:
		return "unknown"
	}
}

// reconciliationErrorHistogram is a fixed-size, value-copyable histogram of
// the bounded bfd.ReconcileErrorCode values. Raw errors are intentionally not
// retained in runtime status.
type reconciliationErrorHistogram [int(bfd.ReconcileErrorCleanup) + 1]uint32

func (h reconciliationErrorHistogram) Count(code bfd.ReconcileErrorCode) uint32 {
	if int(code) >= len(h) {
		return 0
	}
	return h[code]
}

type sourceReceipt struct {
	Source       reconciliationSource
	Created      int
	Updated      int
	Released     int
	Pending      int
	TimerPending int
	Failed       int
	Errors       reconciliationErrorHistogram
}

type generationReceipt struct {
	Generation uint64
	Sources    [sourceCount]sourceReceipt
}

type reconciliationSnapshot struct {
	DesiredGeneration uint64
	AppliedGeneration uint64
	Stale             bool
	Pending           int
	Failed            int
	LastReceipt       generationReceipt
}

type sourceApplyResult struct {
	Created      int
	Updated      int
	Released     int
	Pending      int
	TimerPending int
	Failed       int
	Errors       reconciliationErrorHistogram
	Err          error
	TimerUpdates []bfd.TimerUpdateRef
}

type sourceApplyFunc func(
	context.Context,
	reconciliationSource,
	compiledControlSessionCandidate,
) sourceApplyResult

type interfaceAvailabilityChecker func([]string) (int, error)

// reconciliationCoordinator serializes complete five-source applies,
// generation-scoped pending-source retries, and interface availability events
// while publishing status through a separately readable immutable snapshot.
type reconciliationCoordinator struct {
	// Lock order is applyMu then statusMu. Snapshot readers take only
	// statusMu; health and logging callbacks run after statusMu is released.
	applyMu              sync.Mutex
	retainedCandidate    compiledControlSessionCandidate
	hasRetainedCandidate bool
	timerUpdates         []bfd.TimerUpdateRef

	statusMu              sync.RWMutex
	snapshot              reconciliationSnapshot
	startup               startupRuntimeContract
	onConfiguredPeers     func([]netip.Addr)
	interfaceDependencies [sourceCount][]string
	// unboundSources resolve receive interfaces from local addresses, so any
	// interface event or resync may resolve their pending sessions.
	unboundSources reconciliationSourceMask

	logger *slog.Logger
	// readiness is the single writer of aggregate and bfd.v1 named health.
	readiness *daemonReadiness
	// listeners owns the receive sockets; nil when no daemon runtime exists.
	listeners *listenerOwner
	// overlays owns the VXLAN and Geneve backends; nil when no daemon runtime
	// exists, so a non-empty overlay source fails as backend-unavailable.
	overlays *overlayOwner
}

func newReconciliationCoordinator(
	cfg *config.Config,
	logger *slog.Logger,
	checker *grpchealth.StaticChecker,
) *reconciliationCoordinator {
	coordinator := &reconciliationCoordinator{
		snapshot:  reconciliationSnapshot{Stale: true},
		startup:   newStartupRuntimeContract(cfg),
		logger:    logger,
		readiness: newDaemonReadiness(checker),
	}
	return coordinator
}

func (c *reconciliationCoordinator) Snapshot() reconciliationSnapshot {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.snapshot
}

func (c *reconciliationCoordinator) reconcile(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logLevel *slog.LevelVar,
) error {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	if fields := c.startup.changedFields(cfg); len(fields) != 0 {
		return &startupConfigChangeError{fields: fields}
	}
	candidate, err := compileControlSessionCandidate(cfg)
	if err != nil {
		return err
	}
	var retained, working compiledControlSessionCandidate
	var generation uint64
	if err := mgr.AdmitSessionReload(
		baseReconcileConfigs(c.retainedCandidate.base), baseReconcileConfigs(candidate.base), func() {
			retained, working, generation = c.acceptCandidateLocked(candidate, logLevel)
		},
	); err != nil {
		return fmt.Errorf("validate session reload: %w", err)
	}
	if c.onConfiguredPeers != nil {
		c.onConfiguredPeers(goBGPManagedPeers(cfg.Sessions))
	}
	c.applyAcceptedCandidateLocked(ctx, retained, working, generation, c.withListeners(func(
		ctx context.Context,
		source reconciliationSource,
		candidate compiledControlSessionCandidate,
	) sourceApplyResult {
		return applyCompiledSource(ctx, source, candidate, mgr, sf, c.logger, c.listeners)
	}))
	return nil
}

// withListeners makes the receive listeners part of each source apply: the
// source claims its listeners before the Manager apply, so no generation
// converges on senders alone, and releases the ones it no longer needs after
// an apply without failed or resource-pending entries. A listener that cannot
// open leaves the source pending (absent interface) or failed, unapplied.
func (c *reconciliationCoordinator) withListeners(apply sourceApplyFunc) sourceApplyFunc {
	return func(
		ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate,
	) sourceApplyResult {
		if source == sourceVXLAN || source == sourceGeneve {
			if c.overlays == nil {
				return apply(ctx, source, candidate)
			}
			return c.claimOverlaySource(ctx, source, candidate, apply)
		}
		if c.listeners == nil {
			return apply(ctx, source, candidate)
		}
		configs := candidate.listeners[source]
		//nolint:contextcheck // Receivers are owner-scoped, not bound to one reconciliation pass.
		if err := c.listeners.acquire(sourceClaim(source), configs); err != nil {
			return resourceErrorSourceResult(1, fmt.Errorf("claim %s listeners: %w", source, err))
		}
		result := apply(ctx, source, candidate)
		if result.Failed == 0 && result.Pending == result.TimerPending {
			c.listeners.release(sourceClaim(source), configs)
		}
		return result
	}
}

// checkListenerBindings rebinds listeners of recreated interfaces and
// re-evaluates the binding readiness.
func (c *reconciliationCoordinator) checkListenerBindings(interfaces interfaceInventory) error {
	if c.listeners == nil {
		return c.readiness.checkBindings(interfaces)
	}
	return c.listeners.checkBindings(interfaces)
}

func (c *reconciliationCoordinator) apply(
	ctx context.Context,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	return c.applyCandidate(ctx, compiledControlSessionCandidate{}, applySource)
}

func (c *reconciliationCoordinator) applyCandidate(
	ctx context.Context,
	candidate compiledControlSessionCandidate,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	return c.applyCandidateLocked(ctx, candidate, nil, applySource)
}

func (c *reconciliationCoordinator) applyCandidateLocked(
	ctx context.Context,
	candidate compiledControlSessionCandidate,
	logLevel *slog.LevelVar,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	retained, working, generation := c.acceptCandidateLocked(candidate, logLevel)
	return c.applyAcceptedCandidateLocked(ctx, retained, working, generation, applySource)
}

// acceptCandidateLocked publishes only local state; it must not call Manager
// or resource owners while session admission holds ownershipMu.
func (c *reconciliationCoordinator) acceptCandidateLocked(
	candidate compiledControlSessionCandidate,
	logLevel *slog.LevelVar,
) (compiledControlSessionCandidate, compiledControlSessionCandidate, uint64) {
	retainedCandidate := cloneCompiledControlSessionCandidate(candidate)
	workingCandidate := cloneCompiledControlSessionCandidate(retainedCandidate)

	c.statusMu.Lock()
	generation := c.snapshot.DesiredGeneration + 1
	c.snapshot.DesiredGeneration = generation
	c.snapshot.Stale = true
	for i, source := range reconciliationSources() {
		c.interfaceDependencies[i] = compiledSourceInterfaceDependencies(source, retainedCandidate)
	}
	c.unboundSources = unboundInterfaceSources(retainedCandidate)
	c.statusMu.Unlock()
	c.readiness.beginCapabilities(retainedCandidate.capabilities)
	for i := range retainedCandidate.base {
		retainedCandidate.base[i].reconcile.DesiredGeneration = generation
		workingCandidate.base[i].reconcile.DesiredGeneration = generation
	}
	if logLevel != nil {
		logLevel.Set(workingCandidate.logLevel)
	}
	return retainedCandidate, workingCandidate, generation
}

func (c *reconciliationCoordinator) applyAcceptedCandidateLocked(
	ctx context.Context,
	retainedCandidate, workingCandidate compiledControlSessionCandidate,
	generation uint64,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	receipt := generationReceipt{Generation: generation}
	c.timerUpdates = nil
	transientErrors := make([]error, 0, sourceCount)
	for i, source := range reconciliationSources() {
		result := applySource(ctx, source, workingCandidate)
		if source == sourceBase {
			c.timerUpdates = slices.Clone(result.TimerUpdates)
		}
		receipt.Sources[i] = sourceReceipt{
			Source: source, Created: result.Created, Updated: result.Updated, Released: result.Released,
			Pending: result.Pending, Failed: result.Failed, Errors: result.Errors,
			TimerPending: result.TimerPending,
		}
		if result.Err != nil {
			transientErrors = append(transientErrors, result.Err)
		}
	}
	c.retainedCandidate = retainedCandidate
	c.hasRetainedCandidate = true

	return c.publishReceipt(receipt, errors.Join(transientErrors...))
}

func (c *reconciliationCoordinator) retryPendingSources(
	ctx context.Context,
	expectedGeneration uint64,
	selected reconciliationSourceMask,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	return c.retryPendingSourcesLocked(ctx, expectedGeneration, selected, applySource)
}

func (c *reconciliationCoordinator) retryPendingSourcesLocked(
	ctx context.Context,
	expectedGeneration uint64,
	selected reconciliationSourceMask,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	snapshot := c.Snapshot()
	if !c.hasRetainedCandidate || snapshot.DesiredGeneration != expectedGeneration ||
		snapshot.LastReceipt.Generation != expectedGeneration {
		return snapshot
	}
	receipt := snapshot.LastReceipt
	workingCandidate := cloneCompiledControlSessionCandidate(c.retainedCandidate)
	transientErrors := make([]error, 0, sourceCount)
	for i, source := range reconciliationSources() {
		prior := receipt.Sources[i]
		// A source pending only on admitted Poll transactions waits for no
		// resource; its timer observer completes it.
		if !selected.contains(source) || prior.Pending <= prior.TimerPending || prior.Failed != 0 {
			continue
		}
		result := applySource(ctx, source, workingCandidate)
		if source == sourceBase {
			c.timerUpdates = slices.Clone(result.TimerUpdates)
		}
		receipt.Sources[i] = mergedSourceReceipt(source, prior, result)
		if result.Err != nil {
			transientErrors = append(transientErrors, result.Err)
		}
	}
	if receipt == snapshot.LastReceipt {
		return snapshot
	}

	return c.publishReceipt(receipt, errors.Join(transientErrors...))
}

func (c *reconciliationCoordinator) reconcileInterfaceEvent(
	ctx context.Context,
	expectedGeneration uint64,
	ifName string,
	availability interfaceEventAvailability,
	checkInterfaces interfaceAvailabilityChecker,
	applySource sourceApplyFunc,
) reconciliationSnapshot {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	snapshot := c.Snapshot()
	if !c.hasRetainedCandidate || snapshot.DesiredGeneration != expectedGeneration ||
		snapshot.LastReceipt.Generation != expectedGeneration || ifName == "" {
		return snapshot
	}
	selected := compiledCandidateInterfaceSourceMask(c.retainedCandidate, ifName)
	if selected == 0 {
		return snapshot
	}

	switch availability {
	case interfaceEventUnavailable:
		return c.reconcileUnavailableInterfaceLocked(snapshot, selected, checkInterfaces)
	case interfaceEventAvailable:
		return c.retryPendingSourcesLocked(ctx, expectedGeneration, selected, applySource)
	default:
		return snapshot
	}
}

func (c *reconciliationCoordinator) reconcileUnavailableInterfaceLocked(
	snapshot reconciliationSnapshot,
	selected reconciliationSourceMask,
	checkInterfaces interfaceAvailabilityChecker,
) reconciliationSnapshot {
	receipt := snapshot.LastReceipt
	transientErrors := make([]error, 0, sourceCount)
	for i, source := range reconciliationSources() {
		prior := receipt.Sources[i]
		if !selected.contains(source) || prior.Failed != 0 {
			continue
		}
		result := preflightCompiledSourceInterfaces(
			source, c.retainedCandidate, checkInterfaces,
		)
		if result.Err == nil {
			continue
		}
		receipt.Sources[i] = mergedSourceReceipt(source, prior, result)
		transientErrors = append(transientErrors, result.Err)
	}
	if receipt == snapshot.LastReceipt {
		return snapshot
	}
	return c.publishReceipt(receipt, errors.Join(transientErrors...))
}

func mergedSourceReceipt(
	source reconciliationSource,
	prior sourceReceipt,
	result sourceApplyResult,
) sourceReceipt {
	return sourceReceipt{
		Source:  source,
		Created: prior.Created + result.Created, Released: prior.Released + result.Released,
		// Manager reports the generation's confirmed changed sessions, not a
		// consumable event count. Reinspection must not double-count them.
		Updated: max(prior.Updated, result.Updated),
		Pending: result.Pending, Failed: result.Failed, Errors: result.Errors,
		TimerPending: result.TimerPending,
	}
}

func (c *reconciliationCoordinator) publishReceipt(
	receipt generationReceipt,
	transientErr error,
) reconciliationSnapshot {
	var pending, failed int
	for _, sourceReceipt := range receipt.Sources {
		pending += sourceReceipt.Pending
		failed += sourceReceipt.Failed
	}

	c.statusMu.Lock()
	c.snapshot.LastReceipt = receipt
	c.snapshot.Pending = pending
	c.snapshot.Failed = failed
	if pending == 0 && failed == 0 {
		c.snapshot.AppliedGeneration = receipt.Generation
		c.snapshot.Stale = false
	} else {
		c.snapshot.Stale = true
	}
	snapshot := c.snapshot
	c.statusMu.Unlock()

	c.readiness.setSources(readySources(receipt))
	c.logResult(snapshot, transientErr)
	return snapshot
}

func cloneCompiledControlSessionCandidate(
	candidate compiledControlSessionCandidate,
) compiledControlSessionCandidate {
	cloned := candidate
	cloned.base = slices.Clone(candidate.base)
	for i := range cloned.base {
		cloned.base[i].senderOpts = slices.Clone(candidate.base[i].senderOpts)
	}
	cloned.echo = slices.Clone(candidate.echo)
	for i := range cloned.listeners {
		cloned.listeners[i] = slices.Clone(candidate.listeners[i])
	}
	cloned.microGroups = slices.Clone(candidate.microGroups)
	for i := range cloned.microGroups {
		cloned.microGroups[i].Config.MemberLinks = slices.Clone(
			candidate.microGroups[i].Config.MemberLinks,
		)
	}
	for i := range cloned.overlays {
		cloned.overlays[i].desired = slices.Clone(candidate.overlays[i].desired)
	}
	return cloned
}

// readySources selects the sources without pending or failed entries; all
// five are ready exactly when the snapshot is not stale.
func readySources(receipt generationReceipt) reconciliationSourceMask {
	var ready reconciliationSourceMask
	for i, source := range receipt.Sources {
		if source.Pending == 0 && source.Failed == 0 {
			ready |= sourceMask(reconciliationSource(i))
		}
	}
	return ready
}

func (c *reconciliationCoordinator) logResult(snapshot reconciliationSnapshot, transientErr error) {
	if c.logger == nil {
		return
	}
	attrs := []any{
		slog.Uint64("desired_generation", snapshot.DesiredGeneration),
		slog.Uint64("applied_generation", snapshot.AppliedGeneration),
		slog.Bool("stale", snapshot.Stale),
		slog.Int("pending", snapshot.Pending),
		slog.Int("failed", snapshot.Failed),
	}
	for _, receipt := range snapshot.LastReceipt.Sources {
		attrs = append(attrs, sourceReceiptLogGroup(receipt))
	}
	if snapshot.Stale {
		if transientErr != nil {
			attrs = append(attrs, slog.Any("error", transientErr))
		}
		c.logger.Error("configuration reconciliation incomplete", attrs...)
		return
	}
	c.logger.Info("configuration reconciliation converged", attrs...)
}

func sourceReceiptLogGroup(receipt sourceReceipt) slog.Attr {
	return slog.Group(receipt.Source.String(),
		slog.Int("created", receipt.Created),
		slog.Int("updated", receipt.Updated),
		slog.Int("released", receipt.Released),
		slog.Int("pending", receipt.Pending),
		slog.Int("timer_pending", receipt.TimerPending),
		slog.Int("failed", receipt.Failed),
		slog.Group("errors",
			slog.Uint64("lifecycle", uint64(receipt.Errors.Count(bfd.ReconcileErrorLifecycle))),
			slog.Uint64("invalid", uint64(receipt.Errors.Count(bfd.ReconcileErrorInvalid))),
			slog.Uint64("conflict", uint64(receipt.Errors.Count(bfd.ReconcileErrorConflict))),
			slog.Uint64("create", uint64(receipt.Errors.Count(bfd.ReconcileErrorCreate))),
			slog.Uint64("release", uint64(receipt.Errors.Count(bfd.ReconcileErrorRelease))),
			slog.Uint64("rollback", uint64(receipt.Errors.Count(bfd.ReconcileErrorRollback))),
			slog.Uint64("cleanup", uint64(receipt.Errors.Count(bfd.ReconcileErrorCleanup))),
			slog.Uint64("timer_failed", uint64(receipt.Errors.Count(bfd.ReconcileErrorTimerFailed))),
		),
	)
}

type compiledControlSessionCandidate struct {
	capabilities server.CapabilitySnapshot
	base         []baseSessionCandidate
	echo         []echoSessionCandidate
	microGroups  []bfd.MicroBFDReconcileConfig
	overlays     [2]compiledOverlayCandidate
	logLevel     slog.Level
	// listeners are the receive sockets each source needs.
	listeners [sourceCount][]netio.ListenerConfig
}

// compiledOverlayCandidate holds a validated overlay desired set. conns is
// filled per apply by the overlay owner, one backend per desired entry.
type compiledOverlayCandidate struct {
	owner   bfd.SessionOwner
	conns   []netio.OverlayConn
	desired []bfd.ReconcileConfig
}

func compileControlSessionCandidate(cfg *config.Config) (compiledControlSessionCandidate, error) {
	if cfg == nil {
		return compiledControlSessionCandidate{}, errNilControlSessionCandidate
	}
	base, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		return compiledControlSessionCandidate{}, err
	}
	echo, err := compileEchoSessionCandidates(cfg)
	if err != nil {
		return compiledControlSessionCandidate{}, err
	}
	microGroups, err := compileMicroBFDCandidates(cfg)
	if err != nil {
		return compiledControlSessionCandidate{}, err
	}

	var overlays [2]compiledOverlayCandidate
	for i, overlay := range buildOverlayTunnelParams(cfg) {
		desired, compileErr := compileOverlaySessionCandidates(overlay)
		if compileErr != nil {
			return compiledControlSessionCandidate{}, compileErr
		}
		overlays[i] = compiledOverlayCandidate{owner: overlay.owner, desired: desired}
	}

	return compiledControlSessionCandidate{
		base: base, echo: echo, microGroups: microGroups,
		capabilities: server.CapabilitySnapshot{
			Unsolicited: server.CapabilityRuntime{Known: true, Enabled: cfg.Unsolicited.Enabled},
			Echo:        server.CapabilityRuntime{Known: true, Enabled: cfg.Echo.Enabled},
			MicroBFD:    server.CapabilityRuntime{Known: true, Enabled: cfg.MicroBFD.Enabled},
			VXLAN:       server.CapabilityRuntime{Known: true, Enabled: cfg.VXLAN.Enabled},
			Geneve:      server.CapabilityRuntime{Known: true, Enabled: cfg.Geneve.Enabled},
		},
		overlays: overlays, logLevel: config.ParseLogLevel(cfg.Log.Level),
		listeners: compileListenerConfigs(cfg),
	}, nil
}

func applyCompiledSource(
	ctx context.Context,
	source reconciliationSource,
	candidate compiledControlSessionCandidate,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
	listeners *listenerOwner,
) sourceApplyResult {
	return applyCompiledSourceWithInterfaceChecker(
		ctx, source, candidate, mgr, sf, logger, listeners, netio.CheckInterfacesAvailable,
	)
}

func applyCompiledSourceWithInterfaceChecker(
	ctx context.Context,
	source reconciliationSource,
	candidate compiledControlSessionCandidate,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
	listeners *listenerOwner,
	checkInterfaces interfaceAvailabilityChecker,
) sourceApplyResult {
	if result := preflightCompiledSourceInterfaces(source, candidate, checkInterfaces); result.Err != nil {
		return result
	}

	switch source {
	case sourceBase:
		return sourceResultFromBFD(applyBaseSessionCandidates(
			ctx, candidate.base, mgr, sf, logger,
		))
	case sourceEcho:
		return sourceResultFromBFD(applyEchoSessionCandidates(
			ctx, candidate.echo, mgr, sf, logger, listeners,
		))
	case sourceMicro:
		return sourceResultFromBFD(applyMicroBFDCandidates(
			ctx, candidate.microGroups, mgr, sf, logger,
		))
	case sourceVXLAN:
		return applyCompiledOverlay(ctx, mgr, source, candidate.overlays[0])
	case sourceGeneve:
		return applyCompiledOverlay(ctx, mgr, source, candidate.overlays[1])
	default:
		return failedSourceResult(
			bfd.ReconcileErrorInvalid,
			fmt.Errorf("reconciliation source %d: %w", source, errUnknownReconciliationSource),
		)
	}
}

func preflightCompiledSourceInterfaces(
	source reconciliationSource,
	candidate compiledControlSessionCandidate,
	checkInterfaces interfaceAvailabilityChecker,
) sourceApplyResult {
	dependencies := compiledSourceInterfaceDependencies(source, candidate)
	if len(dependencies) == 0 {
		return sourceApplyResult{}
	}
	pendingClaims, err := checkInterfaces(dependencies)
	if pendingClaims < 0 || pendingClaims > len(dependencies) ||
		(err == nil && pendingClaims != 0) {
		return failedSourceResult(
			bfd.ReconcileErrorCreate,
			fmt.Errorf("preflight %s returned pending count %d for %d dependencies: %w",
				source, pendingClaims, len(dependencies), errInvalidInterfacePreflightResult),
		)
	}
	if err != nil {
		return resourceErrorSourceResult(
			pendingClaims,
			fmt.Errorf("preflight %s interface dependencies: %w", source, err),
		)
	}
	return sourceApplyResult{}
}

func compiledSourceInterfaceDependencies(
	source reconciliationSource,
	candidate compiledControlSessionCandidate,
) []string {
	switch source {
	case sourceBase:
		return collectInterfaceDependencies(len(candidate.base), func(i int) string {
			return candidate.base[i].config.Interface
		})
	case sourceEcho:
		return collectInterfaceDependencies(len(candidate.echo), func(i int) string {
			return candidate.echo[i].config.Interface
		})
	case sourceMicro:
		return microInterfaceDependencies(candidate.microGroups)
	case sourceVXLAN, sourceGeneve:
		return nil
	default:
		return nil
	}
}

func compiledCandidateInterfaceSourceMask(
	candidate compiledControlSessionCandidate,
	ifName string,
) reconciliationSourceMask {
	if ifName == "" {
		return 0
	}
	selected := unboundInterfaceSources(candidate)
	for _, source := range reconciliationSources() {
		if slices.Contains(compiledSourceInterfaceDependencies(source, candidate), ifName) {
			selected |= sourceMask(source)
		}
	}
	return selected
}

// unboundInterfaceSources selects the base source when a single-hop session
// has no interface: its receive interface is the owner of its local address,
// which no named dependency describes (RFC 5881 Section 3).
func unboundInterfaceSources(candidate compiledControlSessionCandidate) reconciliationSourceMask {
	for _, base := range candidate.base {
		if !base.multiHop && base.config.Interface == "" {
			return sourceMask(sourceBase)
		}
	}
	return 0
}

// microInterfaceDependencies lists every LAG and member link of the groups:
// a group and its member sessions apply as one unit.
func microInterfaceDependencies(groups []bfd.MicroBFDReconcileConfig) []string {
	var dependencies []string
	for _, group := range groups {
		for _, ifName := range append([]string{group.Config.LAGInterface}, group.Config.MemberLinks...) {
			if ifName != "" {
				dependencies = append(dependencies, ifName)
			}
		}
	}
	return dependencies
}

func collectInterfaceDependencies(count int, interfaceAt func(int) string) []string {
	dependencies := make([]string, 0, count)
	for i := range count {
		ifName := interfaceAt(i)
		if ifName != "" {
			dependencies = append(dependencies, ifName)
		}
	}
	return dependencies
}

func applyCompiledOverlay(
	ctx context.Context,
	mgr *bfd.Manager,
	source reconciliationSource,
	candidate compiledOverlayCandidate,
) sourceApplyResult {
	if len(candidate.desired) != len(candidate.conns) {
		return failedSourceResult(bfd.ReconcileErrorCreate, overlayBackendUnavailableError(source))
	}
	for _, conn := range candidate.conns {
		if conn == nil {
			return failedSourceResult(bfd.ReconcileErrorCreate, overlayBackendUnavailableError(source))
		}
	}
	desired := make([]bfd.ReconcileConfig, len(candidate.desired))
	copy(desired, candidate.desired)
	for i := range desired {
		if desired[i].SenderLeaseFactory == nil {
			sender := netio.NewOverlaySender(candidate.conns[i], desired[i].SessionConfig.TransportScope)
			desired[i].SenderLeaseFactory = bfd.NonOwningSenderLeaseFactory(sender)
		}
	}
	return sourceResultFromBFD(applyOverlayDesiredSessions(
		ctx, mgr, candidate.owner, desired,
	))
}

func sourceResultFromBFD(result bfd.ReconcileResult) sourceApplyResult {
	converted := sourceApplyResult{
		Created: result.Created, Released: result.Released,
		Updated:      result.Updated,
		TimerPending: result.Pending, TimerUpdates: result.TimerUpdates,
		Pending: result.Pending + result.ResourcePending, Failed: result.Failed, Err: result.Err(),
	}
	for _, reconcileErr := range result.Errors {
		if int(reconcileErr.Code) < len(converted.Errors) {
			converted.Errors[reconcileErr.Code]++
		}
	}
	return converted
}

// runTimerUpdateCompletion observes durable, generation-qualified session
// receipts. The initial inspection closes the registration/completion race;
// the capacity-one wake-up never carries state and duplicates may coalesce.
func (c *reconciliationCoordinator) runTimerUpdateCompletion(
	ctx context.Context, mgr *bfd.Manager,
) {
	wake := mgr.TimerUpdates()
	for {
		if ctx.Err() != nil {
			return
		}
		snapshot := c.Snapshot()
		c.observeTimerUpdates(snapshot.DesiredGeneration, mgr)
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

func (c *reconciliationCoordinator) observeTimerUpdates(expectedGeneration uint64, mgr *bfd.Manager) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	snapshot := c.Snapshot()
	prior := snapshot.LastReceipt.Sources[sourceBase]
	if snapshot.DesiredGeneration != expectedGeneration ||
		snapshot.LastReceipt.Generation != expectedGeneration || prior.TimerPending == 0 {
		return
	}
	result := sourceResultFromBFD(mgr.ObserveTimerUpdates(c.timerUpdates))
	receipt := snapshot.LastReceipt
	base := &receipt.Sources[sourceBase]
	// Completed references retire from the observation set, so each terminal
	// receipt contributes once, alongside failures from original admission.
	base.Updated += result.Updated
	base.Pending += result.Pending - base.TimerPending
	base.TimerPending = result.Pending
	base.Failed += result.Failed
	base.Errors[bfd.ReconcileErrorTimerFailed] += result.Errors[bfd.ReconcileErrorTimerFailed]
	c.timerUpdates = result.TimerUpdates
	if receipt != snapshot.LastReceipt {
		c.publishReceipt(receipt, result.Err)
	}
}

func failedSourceResult(code bfd.ReconcileErrorCode, err error) sourceApplyResult {
	var result sourceApplyResult
	result.Failed = 1
	result.Err = err
	if int(code) < len(result.Errors) {
		result.Errors[code] = 1
	}
	return result
}

func resourceErrorSourceResult(pendingClaims int, err error) sourceApplyResult {
	if pendingClaims > 0 && onlyUnavailableResourceErrors(err) {
		return sourceApplyResult{Pending: pendingClaims, Err: err}
	}
	return failedSourceResult(bfd.ReconcileErrorCreate, err)
}

func onlyUnavailableResourceErrors(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyUnavailableResourceErrors(child) {
				return false
			}
		}
		return true
	}
	unavailableErr, unavailable := errors.AsType[*bfd.ResourceUnavailableError](err)
	_, wrappedUnavailable := errors.AsType[*bfd.ResourceUnavailableError](errors.Unwrap(err))
	if unavailable && !wrappedUnavailable {
		_, valid := bfd.UnavailableResource(unavailableErr)
		return valid
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return onlyUnavailableResourceErrors(wrapped.Unwrap())
	}
	return false
}

func startReloadAfterStartup(reconcileStartup, startReload func()) {
	reconcileStartup()
	startReload()
}

func newDaemonHealthChecker() *grpchealth.StaticChecker {
	checker := grpchealth.NewStaticChecker(
		grpchealth.HealthV1ServiceName,
		bfdServiceName,
		echoServiceName,
		microBFDServiceName,
	)
	// Fail closed until daemonReadiness publishes component-derived status.
	for _, service := range []string{"", bfdServiceName, echoServiceName, microBFDServiceName} {
		checker.SetStatus(service, grpchealth.StatusNotServing)
	}
	return checker
}

func overlayBackendUnavailableError(source reconciliationSource) error {
	return fmt.Errorf("%s: %w", source, errOverlayBackendUnavailable)
}
