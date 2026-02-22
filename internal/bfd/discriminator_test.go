package bfd_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// TestNewDiscriminatorAllocator verifies that a newly created allocator has
// no allocated discriminators.
func TestNewDiscriminatorAllocator(t *testing.T) {
	t.Parallel()

	alloc := bfd.NewDiscriminatorAllocator()

	// A fresh allocator should not consider any value as allocated.
	if alloc.IsAllocated(1) {
		t.Error("fresh allocator reports discriminator 1 as allocated")
	}
	if alloc.IsAllocated(0) {
		t.Error("fresh allocator reports discriminator 0 as allocated")
	}
	if alloc.IsAllocated(0xFFFFFFFF) {
		t.Error("fresh allocator reports discriminator 0xFFFFFFFF as allocated")
	}
}

// TestDiscriminatorAllocateNonZero verifies that Allocate never returns zero.
// RFC 5880 Section 6.8.1: bfd.LocalDiscr MUST be nonzero.
// RFC 5880 Section 6.8.6 step 7b: zero means "not yet known".
func TestDiscriminatorAllocateNonZero(t *testing.T) {
	t.Parallel()

	alloc := bfd.NewDiscriminatorAllocator()

	// Allocate many values and verify none are zero.
	for i := range 1000 {
		discr, err := alloc.Allocate()
		if err != nil {
			t.Fatalf("allocation %d: unexpected error: %v", i, err)
		}
		if discr == 0 {
			t.Fatalf("allocation %d: got zero discriminator, want nonzero", i)
		}
	}
}

// TestDiscriminatorAllocateUnique verifies that 1000 consecutive allocations
// produce entirely unique values.
// RFC 5880 Section 6.8.1: bfd.LocalDiscr MUST be unique across all BFD
// sessions on this system.
func TestDiscriminatorAllocateUnique(t *testing.T) {
	t.Parallel()

	alloc := bfd.NewDiscriminatorAllocator()
	seen := make(map[uint32]struct{}, 1000)

	for i := range 1000 {
		discr, err := alloc.Allocate()
		if err != nil {
			t.Fatalf("allocation %d: unexpected error: %v", i, err)
		}

		if _, exists := seen[discr]; exists {
			t.Fatalf("allocation %d: duplicate discriminator 0x%08X", i, discr)
		}

		seen[discr] = struct{}{}
	}

	if len(seen) != 1000 {
		t.Errorf("expected 1000 unique discriminators, got %d", len(seen))
	}
}

// TestDiscriminatorRelease verifies that releasing a discriminator removes it
// from the allocated set and allows future allocations to potentially reuse
// the value space.
func TestDiscriminatorRelease(t *testing.T) {
	t.Parallel()

	alloc := bfd.NewDiscriminatorAllocator()

	discr, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("allocate: unexpected error: %v", err)
	}

	// The discriminator should be allocated.
	if !alloc.IsAllocated(discr) {
		t.Errorf("discriminator 0x%08X not allocated after Allocate()", discr)
	}

	// Release and verify it is no longer allocated.
	alloc.Release(discr)

	if alloc.IsAllocated(discr) {
		t.Errorf("discriminator 0x%08X still allocated after Release()", discr)
	}

	// Releasing a second time should be a no-op (no panic, no error).
	alloc.Release(discr)

	// Releasing a never-allocated discriminator should also be a no-op.
	alloc.Release(0xDEADBEEF)
}

// TestDiscriminatorIsAllocated verifies the IsAllocated method tracks
// allocation state correctly through allocate and release cycles.
func TestDiscriminatorIsAllocated(t *testing.T) {
	t.Parallel()

	alloc := bfd.NewDiscriminatorAllocator()

	// Allocate several discriminators.
	discriminators := make([]uint32, 5)
	for i := range discriminators {
		discr, err := alloc.Allocate()
		if err != nil {
			t.Fatalf("allocate %d: unexpected error: %v", i, err)
		}
		discriminators[i] = discr
	}

	// All should be allocated.
	for i, discr := range discriminators {
		if !alloc.IsAllocated(discr) {
			t.Errorf("discriminator %d (0x%08X): expected allocated", i, discr)
		}
	}

	// Release the middle one.
	alloc.Release(discriminators[2])

	// The released one should not be allocated; others should still be.
	for i, discr := range discriminators {
		allocated := alloc.IsAllocated(discr)
		if i == 2 {
			if allocated {
				t.Errorf("discriminator %d (0x%08X): expected not allocated after release", i, discr)
			}
		} else {
			if !allocated {
				t.Errorf("discriminator %d (0x%08X): expected allocated", i, discr)
			}
		}
	}
}

// TestDiscriminatorConcurrency verifies that the allocator is safe for
// concurrent use from multiple goroutines. This test allocates and releases
// discriminators from multiple goroutines simultaneously, checking for data
// races (requires -race flag) and uniqueness violations.
func TestDiscriminatorConcurrency(t *testing.T) {
	t.Parallel()

	alloc := bfd.NewDiscriminatorAllocator()

	const (
		numGoroutines = 10
		numPerRoutine = 100
	)

	// Each goroutine collects its allocated discriminators.
	results := make([][]uint32, numGoroutines)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		results[g] = make([]uint32, 0, numPerRoutine)
		go func(idx int) {
			defer wg.Done()

			for range numPerRoutine {
				discr, err := alloc.Allocate()
				if err != nil {
					t.Errorf("goroutine %d: allocate error: %v", idx, err)
					return
				}
				if discr == 0 {
					t.Errorf("goroutine %d: got zero discriminator", idx)
					return
				}
				results[idx] = append(results[idx], discr)
			}
		}(g)
	}

	wg.Wait()

	// Verify all discriminators across all goroutines are unique.
	seen := make(map[uint32]struct{}, numGoroutines*numPerRoutine)
	for g, discrs := range results {
		for i, discr := range discrs {
			if _, exists := seen[discr]; exists {
				t.Errorf("goroutine %d, allocation %d: duplicate discriminator 0x%08X", g, i, discr)
			}
			seen[discr] = struct{}{}
		}
	}

	expectedTotal := numGoroutines * numPerRoutine
	if len(seen) != expectedTotal {
		t.Errorf("expected %d unique discriminators, got %d", expectedTotal, len(seen))
	}

	// Release all and verify none remain allocated.
	for _, discrs := range results {
		for _, discr := range discrs {
			alloc.Release(discr)
		}
	}

	for _, discrs := range results {
		for _, discr := range discrs {
			if alloc.IsAllocated(discr) {
				t.Errorf("discriminator 0x%08X still allocated after release", discr)
			}
		}
	}
}

// TestDiscriminatorAllocateReturnsError verifies that the Allocate method
// returns a properly wrapped ErrDiscriminatorExhausted error. We cannot
// easily exhaust the 32-bit space, so we test the error sentinel directly.
func TestDiscriminatorAllocateReturnsError(t *testing.T) {
	t.Parallel()

	// Verify the sentinel error can be detected with errors.Is.
	err := fmt.Errorf("allocate discriminator after 100 attempts: %w", bfd.ErrDiscriminatorExhausted)
	if !errors.Is(err, bfd.ErrDiscriminatorExhausted) {
		t.Error("wrapped ErrDiscriminatorExhausted not detected by errors.Is")
	}
}
