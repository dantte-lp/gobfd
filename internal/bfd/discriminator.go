package bfd

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// maxAllocAttempts is the maximum number of random generation attempts before
// returning ErrDiscriminatorExhausted. With a 32-bit random space and typical
// session counts (<10k), collisions are astronomically unlikely; this limit
// exists as a safety net against degenerate states.
const maxAllocAttempts = 100

// ErrDiscriminatorExhausted indicates that the allocator could not generate a
// unique nonzero discriminator after the maximum number of attempts. This
// should never occur in practice given the 32-bit random space.
var ErrDiscriminatorExhausted = errors.New("discriminator allocator exhausted")

// DiscriminatorAllocator generates unique, nonzero, random local discriminators
// for BFD sessions.
//
// RFC 5880 Section 6.8.1: bfd.LocalDiscr "MUST be unique across all BFD
// sessions on this system, and nonzero. It SHOULD be set to a random
// (but still unique) value to improve security."
//
// Implementation: generates random uint32 values using crypto/rand and checks
// them against a set of allocated values. The zero value is never returned
// because RFC 5880 Section 6.8.6 uses zero as "Your Discriminator not yet
// known." Thread-safe via sync.Mutex.
type DiscriminatorAllocator struct {
	mu        sync.Mutex
	allocated map[uint32]struct{}
}

// NewDiscriminatorAllocator creates a new DiscriminatorAllocator with an empty
// allocation set.
func NewDiscriminatorAllocator() *DiscriminatorAllocator {
	return &DiscriminatorAllocator{
		allocated: make(map[uint32]struct{}),
	}
}

// Allocate generates a unique, nonzero, random local discriminator.
//
// The returned value satisfies the requirements of RFC 5880 Section 6.8.1:
// it is nonzero and unique across all sessions managed by this allocator.
// Randomness is provided by crypto/rand to improve security as recommended
// by the RFC (SHOULD).
//
// Returns ErrDiscriminatorExhausted if a unique value cannot be found after
// a reasonable number of attempts.
func (d *DiscriminatorAllocator) Allocate() (uint32, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var buf [4]byte

	for range maxAllocAttempts {
		if _, err := rand.Read(buf[:]); err != nil {
			return 0, fmt.Errorf("generate random discriminator: %w", err)
		}

		discr := binary.BigEndian.Uint32(buf[:])

		// RFC 5880 Section 6.8.1: discriminator MUST be nonzero.
		// Zero is reserved as "Your Discriminator not yet known"
		// (RFC 5880 Section 6.8.6 step 7b).
		if discr == 0 {
			continue
		}

		// RFC 5880 Section 6.8.1: MUST be unique across all BFD sessions.
		if _, exists := d.allocated[discr]; exists {
			continue
		}

		d.allocated[discr] = struct{}{}

		return discr, nil
	}

	return 0, fmt.Errorf("allocate discriminator after %d attempts: %w",
		maxAllocAttempts, ErrDiscriminatorExhausted)
}

// Release removes a previously allocated discriminator from the allocation
// set, making the value available for future allocations. This is called
// during session teardown to prevent discriminator leaks.
//
// Releasing a discriminator that was not allocated is a no-op.
func (d *DiscriminatorAllocator) Release(discr uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.allocated, discr)
}

// IsAllocated reports whether a discriminator is currently allocated.
func (d *DiscriminatorAllocator) IsAllocated(discr uint32) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, exists := d.allocated[discr]
	return exists
}
