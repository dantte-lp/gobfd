package bfd

import "errors"

// ReconcileErrorCode is a closed, bounded classification of one failed
// reconciliation operation. Callers may persist or label the code, but must
// not promote the wrapped error text into a metric label or durable status.
type ReconcileErrorCode uint8

const (
	// ReconcileErrorLifecycle reports that Manager cannot start an operation.
	ReconcileErrorLifecycle ReconcileErrorCode = iota + 1
	// ReconcileErrorInvalid reports an invalid owner or desired candidate.
	ReconcileErrorInvalid
	// ReconcileErrorConflict reports a desired/live parameter conflict.
	ReconcileErrorConflict
	// ReconcileErrorCreate reports one failed source-resource creation.
	ReconcileErrorCreate
	// ReconcileErrorRelease reports one failed source-resource release.
	ReconcileErrorRelease
	// ReconcileErrorRollback reports one failed new-resource rollback.
	ReconcileErrorRollback
	// ReconcileErrorCleanup reports cleanup failure after state detachment.
	ReconcileErrorCleanup
)

// String returns the stable bounded name of a reconciliation error code.
func (c ReconcileErrorCode) String() string {
	switch c {
	case ReconcileErrorLifecycle:
		return "lifecycle"
	case ReconcileErrorInvalid:
		return "invalid"
	case ReconcileErrorConflict:
		return "conflict"
	case ReconcileErrorCreate:
		return "create"
	case ReconcileErrorRelease:
		return "release"
	case ReconcileErrorRollback:
		return "rollback"
	case ReconcileErrorCleanup:
		return "cleanup"
	default:
		return "unknown"
	}
}

// ReconcileError records the bounded failure class and its transient cause.
// Err is for immediate diagnostics and errors.Is/errors.As inspection only.
type ReconcileError struct {
	Code ReconcileErrorCode
	Err  error
}

// ReconcileResult describes the net source-owned resource changes from one
// reconciliation pass. Created and Released count source claims/resources,
// not physical shared wire sessions. Failed always equals len(Errors).
//
// Pending is intentionally always zero until a later slice introduces typed
// retryable errors and an automatic retry owner. Runtime errors are never
// guessed into pending through strings or platform-specific errno values.
type ReconcileResult struct {
	Created  int
	Released int
	Pending  int
	Failed   int
	Errors   []ReconcileError

	wireCreated   int
	wireDestroyed int
}

// Err joins the transient causes for compatibility callers and immediate
// diagnostics. It returns nil when every operation converged.
func (r ReconcileResult) Err() error {
	if len(r.Errors) == 1 {
		return r.Errors[0].Err
	}
	errs := make([]error, 0, len(r.Errors))
	for _, reconcileErr := range r.Errors {
		errs = append(errs, reconcileErr.Err)
	}
	return errors.Join(errs...)
}

func addReconcileError(r *ReconcileResult, code ReconcileErrorCode, err error) {
	if err == nil {
		return
	}
	r.Errors = append(r.Errors, ReconcileError{Code: code, Err: err})
	r.Failed = len(r.Errors)
}

func failedReconcileResult(code ReconcileErrorCode, err error) ReconcileResult {
	var result ReconcileResult
	addReconcileError(&result, code, err)
	return result
}
