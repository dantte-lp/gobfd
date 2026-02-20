package bfdmetrics_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs all tests in the bfdmetrics_test package and checks for
// goroutine leaks after all tests complete. Any leaked goroutine causes
// a test failure.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
