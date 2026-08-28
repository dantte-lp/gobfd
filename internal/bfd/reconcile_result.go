package bfd

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// MaxResourceIDLen bounds transient resource identities. Resource-specific
	// adapters may enforce a smaller namespace limit before constructing an
	// unavailable-resource error.
	MaxResourceIDLen = 255
)

var (
	// ErrResourceUnavailable identifies a validated runtime resource that is
	// not currently available. Callers must use ResourceUnavailableError for
	// the bounded resource kind and identity rather than inferring retryability
	// from error text or platform-specific causes.
	ErrResourceUnavailable = errors.New("resource unavailable")
	// ErrInvalidResourceRef identifies a malformed unavailable-resource
	// reference. It is a permanent validation failure, never retryable.
	ErrInvalidResourceRef = errors.New("invalid resource reference")
)

// ResourceKind is a closed classification of retryable runtime resources.
type ResourceKind uint8

const (
	// ResourceKindInterface identifies one validated network interface name.
	ResourceKindInterface ResourceKind = iota + 1
)

// String returns the stable bounded name of a resource kind.
func (k ResourceKind) String() string {
	switch k {
	case ResourceKindInterface:
		return "interface"
	default:
		return "unknown"
	}
}

// ResourceRef identifies one unavailable resource. ID is validated and
// bounded by the adapter that owns the resource namespace.
type ResourceRef struct {
	Kind ResourceKind
	ID   string
}

// ResourceUnavailableError carries typed retry context for one resource.
// It is transient diagnostic data and must not be promoted into metric labels
// or durable status.
type ResourceUnavailableError struct {
	resource ResourceRef
}

// NewResourceUnavailableError validates resource and creates a typed
// unavailable-resource error. Malformed references return a permanent error.
func NewResourceUnavailableError(resource ResourceRef) error {
	if !validResourceRef(resource) {
		return fmt.Errorf("validate unavailable resource reference: %w", ErrInvalidResourceRef)
	}
	return &ResourceUnavailableError{resource: resource}
}

// Error implements error.
func (e *ResourceUnavailableError) Error() string {
	if e == nil || !validResourceRef(e.resource) {
		return ErrInvalidResourceRef.Error()
	}
	return fmt.Sprintf("%s %q: %v", e.resource.Kind, e.resource.ID, ErrResourceUnavailable)
}

// Unwrap exposes the stable unavailable-resource sentinel.
func (e *ResourceUnavailableError) Unwrap() error {
	if e == nil || !validResourceRef(e.resource) {
		return ErrInvalidResourceRef
	}
	return ErrResourceUnavailable
}

// Resource returns the bounded typed resource identity.
func (e *ResourceUnavailableError) Resource() ResourceRef {
	if e == nil {
		return ResourceRef{}
	}
	return e.resource
}

// UnavailableResource classifies a typed unavailable-resource error through
// arbitrary wrapping. Malformed or permanent errors are not classified.
func UnavailableResource(err error) (ResourceRef, bool) {
	var unavailableErr *ResourceUnavailableError
	if !errors.As(err, &unavailableErr) || unavailableErr == nil {
		return ResourceRef{}, false
	}
	resource := unavailableErr.Resource()
	if !validResourceRef(resource) {
		return ResourceRef{}, false
	}
	return resource, true
}

func validResourceRef(resource ResourceRef) bool {
	return resource.Kind == ResourceKindInterface &&
		resource.ID != "" &&
		len(resource.ID) <= MaxResourceIDLen &&
		!strings.ContainsRune(resource.ID, 0)
}

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
