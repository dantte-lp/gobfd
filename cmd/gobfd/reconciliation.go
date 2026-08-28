package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"connectrpc.com/grpchealth"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const (
	bfdServiceName      = "bfd.v1.BfdService"
	echoServiceName     = "bfd.v1.EchoService"
	microBFDServiceName = "bfd.v1.MicroBFDService"
)

var (
	errOverlayBackendUnavailable   = errors.New("overlay backend is not running")
	errNilControlSessionCandidate  = errors.New("nil control-session candidate")
	errUnknownReconciliationSource = errors.New("unknown reconciliation source")
)

type reconciliationSource uint8

const (
	sourceBase reconciliationSource = iota
	sourceEcho
	sourceMicroGroup
	sourceMicroMember
	sourceVXLAN
	sourceGeneve
)

const sourceCount = int(sourceGeneve) + 1

type reconciliationSourceMask uint8

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
		sourceMicroGroup,
		sourceMicroMember,
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
	case sourceMicroGroup:
		return "micro_group"
	case sourceMicroMember:
		return "micro_member"
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
	Source   reconciliationSource
	Created  int
	Released int
	Pending  int
	Failed   int
	Errors   reconciliationErrorHistogram
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
	Created  int
	Released int
	Pending  int
	Failed   int
	Errors   reconciliationErrorHistogram
	Err      error
}

type sourceApplyFunc func(
	context.Context,
	reconciliationSource,
	compiledControlSessionCandidate,
) sourceApplyResult

// reconciliationCoordinator serializes complete six-source applies and
// generation-scoped pending-source retries while publishing status through a
// separately readable immutable value snapshot.
type reconciliationCoordinator struct {
	// Lock order is applyMu then statusMu. Snapshot readers take only
	// statusMu; health and logging callbacks run after statusMu is released.
	applyMu              sync.Mutex
	retainedCandidate    compiledControlSessionCandidate
	hasRetainedCandidate bool

	statusMu sync.RWMutex
	snapshot reconciliationSnapshot
	startup  startupRuntimeContract

	logger  *slog.Logger
	checker *grpchealth.StaticChecker
}

func newReconciliationCoordinator(
	cfg *config.Config,
	logger *slog.Logger,
	checker *grpchealth.StaticChecker,
) *reconciliationCoordinator {
	coordinator := &reconciliationCoordinator{
		snapshot: reconciliationSnapshot{Stale: true},
		startup:  newStartupRuntimeContract(cfg),
		logger:   logger,
		checker:  checker,
	}
	coordinator.setReady(false)
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
	overlayRuntime *overlayRuntime,
	logLevel *slog.LevelVar,
) error {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	if fields := c.startup.changedFields(cfg); len(fields) != 0 {
		return &startupConfigChangeError{fields: fields}
	}
	candidate, err := compileControlSessionCandidate(cfg, overlayRuntime)
	if err != nil {
		return err
	}
	c.applyCandidateLocked(ctx, candidate, logLevel, func(
		ctx context.Context,
		source reconciliationSource,
		candidate compiledControlSessionCandidate,
	) sourceApplyResult {
		return applyCompiledSource(ctx, source, candidate, mgr, sf, c.logger)
	})
	return nil
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
	retainedCandidate := cloneCompiledControlSessionCandidate(candidate)
	workingCandidate := cloneCompiledControlSessionCandidate(retainedCandidate)

	c.statusMu.Lock()
	generation := c.snapshot.DesiredGeneration + 1
	c.snapshot.DesiredGeneration = generation
	c.snapshot.Stale = true
	c.statusMu.Unlock()
	c.setReady(false)

	if logLevel != nil {
		logLevel.Set(workingCandidate.logLevel)
	}

	receipt := generationReceipt{Generation: generation}
	transientErrors := make([]error, 0, sourceCount)
	for i, source := range reconciliationSources() {
		result := applySource(ctx, source, workingCandidate)
		receipt.Sources[i] = sourceReceipt{
			Source: source, Created: result.Created, Released: result.Released,
			Pending: result.Pending, Failed: result.Failed, Errors: result.Errors,
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

	snapshot := c.Snapshot()
	if !c.hasRetainedCandidate || snapshot.DesiredGeneration != expectedGeneration ||
		snapshot.LastReceipt.Generation != expectedGeneration {
		return snapshot
	}

	receipt := snapshot.LastReceipt
	workingCandidate := cloneCompiledControlSessionCandidate(c.retainedCandidate)
	transientErrors := make([]error, 0, sourceCount)
	applied := false
	for i, source := range reconciliationSources() {
		prior := receipt.Sources[i]
		if !selected.contains(source) || prior.Pending <= 0 || prior.Failed != 0 {
			continue
		}
		result := applySource(ctx, source, workingCandidate)
		receipt.Sources[i] = sourceReceipt{
			Source:  source,
			Created: prior.Created + result.Created, Released: prior.Released + result.Released,
			Pending: result.Pending, Failed: result.Failed, Errors: result.Errors,
		}
		if result.Err != nil {
			transientErrors = append(transientErrors, result.Err)
		}
		applied = true
	}
	if !applied {
		return snapshot
	}

	return c.publishReceipt(receipt, errors.Join(transientErrors...))
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

	c.setReady(!snapshot.Stale)
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
	cloned.microGroups = slices.Clone(candidate.microGroups)
	for i := range cloned.microGroups {
		cloned.microGroups[i].Config.MemberLinks = slices.Clone(
			candidate.microGroups[i].Config.MemberLinks,
		)
	}
	cloned.microMembers = slices.Clone(candidate.microMembers)
	for i := range cloned.overlays {
		cloned.overlays[i].desired = slices.Clone(candidate.overlays[i].desired)
	}
	return cloned
}

func (c *reconciliationCoordinator) setReady(ready bool) {
	if c.checker == nil {
		return
	}
	status := grpchealth.StatusNotServing
	if ready {
		status = grpchealth.StatusServing
	}
	c.checker.SetStatus("", status)
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
		slog.Int("released", receipt.Released),
		slog.Int("pending", receipt.Pending),
		slog.Int("failed", receipt.Failed),
		slog.Group("errors",
			slog.Uint64("lifecycle", uint64(receipt.Errors.Count(bfd.ReconcileErrorLifecycle))),
			slog.Uint64("invalid", uint64(receipt.Errors.Count(bfd.ReconcileErrorInvalid))),
			slog.Uint64("conflict", uint64(receipt.Errors.Count(bfd.ReconcileErrorConflict))),
			slog.Uint64("create", uint64(receipt.Errors.Count(bfd.ReconcileErrorCreate))),
			slog.Uint64("release", uint64(receipt.Errors.Count(bfd.ReconcileErrorRelease))),
			slog.Uint64("rollback", uint64(receipt.Errors.Count(bfd.ReconcileErrorRollback))),
			slog.Uint64("cleanup", uint64(receipt.Errors.Count(bfd.ReconcileErrorCleanup))),
		),
	)
}

type compiledControlSessionCandidate struct {
	base         []baseSessionCandidate
	echo         []echoSessionCandidate
	microGroups  []bfd.MicroBFDReconcileConfig
	microMembers []microBFDMemberCandidate
	overlays     [2]compiledOverlayCandidate
	logLevel     slog.Level
}

type compiledOverlayCandidate struct {
	owner   bfd.SessionOwner
	conn    netio.OverlayConn
	desired []bfd.ReconcileConfig
}

func compileControlSessionCandidate(
	cfg *config.Config,
	runtime *overlayRuntime,
) (compiledControlSessionCandidate, error) {
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
	microGroups, microMembers, err := compileMicroBFDCandidates(cfg)
	if err != nil {
		return compiledControlSessionCandidate{}, err
	}

	params := buildOverlayTunnelParams(cfg, runtime)
	var overlays [2]compiledOverlayCandidate
	for i, overlay := range params {
		desired, compileErr := compileOverlaySessionCandidates(overlay)
		if compileErr != nil {
			return compiledControlSessionCandidate{}, compileErr
		}
		overlays[i] = compiledOverlayCandidate{
			owner: overlay.owner, conn: overlay.conn, desired: desired,
		}
	}

	return compiledControlSessionCandidate{
		base: base, echo: echo, microGroups: microGroups, microMembers: microMembers,
		overlays: overlays, logLevel: config.ParseLogLevel(cfg.Log.Level),
	}, nil
}

func applyCompiledSource(
	ctx context.Context,
	source reconciliationSource,
	candidate compiledControlSessionCandidate,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
) sourceApplyResult {
	switch source {
	case sourceBase:
		return sourceResultFromBFD(applyBaseSessionCandidates(
			ctx, candidate.base, mgr, sf, logger,
		))
	case sourceEcho:
		return sourceResultFromBFD(applyEchoSessionCandidates(
			ctx, candidate.echo, mgr, sf, logger,
		))
	case sourceMicroGroup:
		return sourceResultFromBFD(applyMicroBFDGroupCandidates(candidate.microGroups, mgr))
	case sourceMicroMember:
		return sourceResultFromBFD(applyMicroBFDMemberCandidates(
			ctx, candidate.microMembers, mgr, sf, logger,
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

func applyCompiledOverlay(
	ctx context.Context,
	mgr *bfd.Manager,
	source reconciliationSource,
	candidate compiledOverlayCandidate,
) sourceApplyResult {
	if len(candidate.desired) > 0 && candidate.conn == nil {
		return failedSourceResult(
			bfd.ReconcileErrorCreate,
			overlayBackendUnavailableError(source),
		)
	}
	desired := make([]bfd.ReconcileConfig, len(candidate.desired))
	copy(desired, candidate.desired)
	if len(desired) > 0 {
		sender := netio.NewOverlaySender(candidate.conn)
		for i := range desired {
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
		Pending: result.Pending, Failed: result.Failed, Err: result.Err(),
	}
	for _, reconcileErr := range result.Errors {
		if int(reconcileErr.Code) < len(converted.Errors) {
			converted.Errors[reconcileErr.Code]++
		}
	}
	return converted
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
	checker.SetStatus("", grpchealth.StatusNotServing)
	return checker
}

func overlayBackendUnavailableError(source reconciliationSource) error {
	return fmt.Errorf("%s: %w", source, errOverlayBackendUnavailable)
}
