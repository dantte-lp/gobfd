//go:build e2e_bgp_failover_testcontainers

package bgp_failover_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type cleanupStack []func()

func (stack *cleanupStack) register(cleanup func()) {
	*stack = append(*stack, cleanup)
}

func (stack *cleanupStack) run() {
	for _, cleanup := range slices.Backward(*stack) {
		cleanup()
	}
}

type fakeOwnedImageClient struct {
	exists     bool
	inspectErr error
	removeErr  error
	removed    []string
}

func (client *fakeOwnedImageClient) ImageExists(context.Context, string) (bool, error) {
	return client.exists, client.inspectErr
}

func (client *fakeOwnedImageClient) RemoveImage(_ context.Context, image string) error {
	client.removed = append(client.removed, image)
	client.exists = false
	return client.removeErr
}

func TestOwnedImageCleanupPrecedesBuildFailure(t *testing.T) {
	for _, failure := range []string{"late build error", "provider close error after successful build"} {
		t.Run(failure, func(t *testing.T) {
			const imageName = "localhost/gobfd-bgp-failover:test-owned"
			client := new(fakeOwnedImageClient)
			var cleanups cleanupStack
			var reported []error

			err := installOwnedImageCleanup(
				t.Context(), imageName, client, cleanups.register,
				func(err error) { reported = append(reported, err) },
			)
			if err != nil {
				t.Fatalf("install image cleanup: %v", err)
			}
			if len(cleanups) != 1 {
				t.Fatalf("cleanup count before simulated build = %d, want 1", len(cleanups))
			}

			// A late build or provider-close error may still leave the unique tag.
			client.exists = true
			cleanups.run()
			if !slices.Equal(client.removed, []string{imageName}) {
				t.Fatalf("removed images = %v, want exact owned tag", client.removed)
			}
			if len(reported) != 0 {
				t.Fatalf("cleanup reports = %v, want none", reported)
			}
		})
	}
}

func TestOwnedImageCleanupRefusesForeignImage(t *testing.T) {
	client := &fakeOwnedImageClient{exists: true}
	var cleanups cleanupStack
	err := installOwnedImageCleanup(
		t.Context(), "localhost/foreign:present", client, cleanups.register,
		func(err error) { t.Error(err) },
	)
	if err == nil {
		t.Fatal("existing foreign image accepted as test-owned")
	}
	if len(cleanups) != 0 || len(client.removed) != 0 {
		t.Fatalf("foreign image cleanup = callbacks %d removals %v", len(cleanups), client.removed)
	}
}

func TestLateStartupFailureRetainsEvidenceAndOwnedResources(t *testing.T) {
	reportDir := t.TempDir()
	topology := &bgpFailoverTopology{reportDir: reportDir}
	if err := topology.initializeResourceEvidence(); err != nil {
		t.Fatalf("initialize early evidence ownership: %v", err)
	}
	var cleanups cleanupStack
	var order []string
	var reported []error
	writeEvidence := func() error {
		order = append(order, "evidence")
		return topology.writeResourceSnapshot()
	}
	report := func(err error) { reported = append(reported, err) }

	// Evidence ownership exists before the first simulated runtime operation.
	topology.registerEvidenceCleanup(cleanups.register, writeEvidence, report)
	if len(cleanups) != 1 {
		t.Fatalf("initial evidence cleanup count = %d, want 1", len(cleanups))
	}
	if err := topology.recordOwnedImage("localhost/gobfd:test-123"); err != nil {
		t.Fatalf("record owned image before build: %v", err)
	}
	if err := topology.recordOwnedNetwork("gobfd-bgp-failover-123"); err != nil {
		t.Fatalf("record owned network before create: %v", err)
	}
	if err := topology.recordOwnedContainer("frr-123", "immutable-frr-id"); err != nil {
		t.Fatalf("record late-start container identity: %v", err)
	}

	// Simulate the resource cleanup registered by the runtime, then re-arm
	// evidence so LIFO teardown captures diagnostics before deletion.
	cleanups.register(func() { order = append(order, "resource-cleanup") })
	topology.registerEvidenceCleanup(cleanups.register, writeEvidence, report)
	lateErr := errors.New("late FRR startup failure")
	if err := topology.recordStartupFailure(lateErr); err != nil {
		t.Fatalf("record late startup failure: %v", err)
	}
	cleanups.run()

	if !slices.Equal(order, []string{"evidence", "resource-cleanup"}) {
		t.Fatalf("cleanup order = %v, want evidence before resource cleanup", order)
	}
	if len(reported) != 0 {
		t.Fatalf("evidence reports = %v, want none", reported)
	}
	contents, err := os.ReadFile(filepath.Join(reportDir, "resources.json"))
	if err != nil {
		t.Fatalf("read mutable resource snapshot: %v", err)
	}
	var snapshot bgpFailoverResourceSnapshot
	if decodeErr := json.Unmarshal(contents, &snapshot); decodeErr != nil {
		t.Fatalf("decode mutable resource snapshot: %v", decodeErr)
	}
	if !slices.Equal(snapshot.ImageNames, []string{"localhost/gobfd:test-123"}) ||
		!slices.Equal(snapshot.ContainerNames, []string{"frr-123"}) ||
		!slices.Equal(snapshot.ContainerIDs, []string{"immutable-frr-id"}) ||
		snapshot.NetworkName != "gobfd-bgp-failover-123" {
		t.Fatalf("late-start resource snapshot = %+v", snapshot)
	}
	diagnostic, err := os.ReadFile(filepath.Join(reportDir, "containers.err"))
	if err != nil {
		t.Fatalf("read bounded startup diagnostic: %v", err)
	}
	if string(diagnostic) != lateErr.Error()+"\n" || len(diagnostic) > maxDiagnosticBytes {
		t.Fatalf("startup diagnostic = %q", diagnostic)
	}
}

func TestFinalSummaryObservesCleanupAndRemovalFailures(t *testing.T) {
	for _, failure := range []string{"LIFO resource cleanup", "exact removal assertion"} {
		t.Run(failure, func(t *testing.T) {
			var outerCleanups cleanupStack
			failed := false
			var statuses []int
			var order []string
			registerFinalSummary(
				outerCleanups.register,
				func() bool { return failed },
				func(status int) error {
					order = append(order, "summary")
					statuses = append(statuses, status)
					return nil
				},
				func(err error) { t.Error(err) },
			)

			switch failure {
			case "LIFO resource cleanup":
				outerCleanups.register(func() {
					order = append(order, "resource-cleanup")
					failed = true
				})
			case "exact removal assertion":
				order = append(order, "exact-removal")
				failed = true
			}
			outerCleanups.run()

			if !slices.Equal(statuses, []int{1}) {
				t.Fatalf("summary statuses = %v, want final failure", statuses)
			}
			if order[len(order)-1] != "summary" {
				t.Fatalf("summary order = %v, want summary last", order)
			}
		})
	}
}
