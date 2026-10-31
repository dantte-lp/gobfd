package stepca

import "errors"

// ErrCredential identifies an unsafe, changed or malformed local credential.
var ErrCredential = errors.New("invalid local certificate credential")

// ErrCommitted means replacement succeeded but subsequent durability or cleanup
// failed. The old certificate must not be assumed to remain in place.
var ErrCommitted = errors.New("certificate replaced; durability or cleanup failed")
