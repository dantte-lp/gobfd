package clabbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

// This runtime-entry fixture builds no payload and proves no successful compilation or execution.
func TestVendorRuntimeEntryRechecksHeldEngine(t *testing.T) {
	const (
		runID         = "runtime-recheck-run"
		artifactError = "runtime requires checkpointed compiler artifacts"
		privateDetail = "fixture-private-container-detail"
	)
	socketRoot := t.TempDir()
	index := 0
	for _, target := range []string{gobfdContainer, frrContainer} {
		for _, kind := range []string{
			"exact", "api-id", "api-owner", "api-run", "cli-id", "rewritten-receipt", "api-error", "canceled-http",
		} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				index++
				root := compilerSourceFixture(t)
				if err := ensureRuntimeDirectory(root); err != nil {
					t.Fatal(err)
				}
				ids := map[string]string{
					gobfdContainer: strings.Repeat("1", 64), frrContainer: strings.Repeat("2", 64),
				}
				topology := lifecycleReceipt{
					SchemaVersion: ownedReceiptV3, RunID: runID, Topology: runtimeTopologyPath(root),
					Network:            lifecycleNetwork{Name: networkName(runID)},
					ExpectedContainers: []string{gobfdContainer, frrContainer}, Containers: maps.Clone(ids),
					Image: lifecycleImage{Reference: gobfdImageRepo + ":" + runID, ID: testImageID},
					FRR:   lifecycleImage{Reference: frrImageRepo + ":" + runID, ID: testFRRImageID},
				}
				if err := createLifecycleReceipt(root, topology); err != nil {
					t.Fatal(err)
				}
				heldTopology, err := loadLifecycleReceipt(root)
				if err != nil {
					t.Fatal(err)
				}
				apiIDs := maps.Clone(ids)
				var mu sync.Mutex
				var changed bool
				var cancelOnProbe context.CancelFunc
				var gobfdProbes, frrProbes, trapProbes atomic.Int64
				handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					name := strings.TrimPrefix(request.URL.Path, "/v5.0.0/containers/")
					name = strings.TrimSuffix(name, "/json")
					if request.Method != http.MethodGet || (name != gobfdContainer && name != frrContainer) {
						http.NotFound(w, request)
						return
					}
					if name == gobfdContainer {
						gobfdProbes.Add(1)
					} else {
						frrProbes.Add(1)
					}
					mu.Lock()
					id, mutated, cancelRequest := apiIDs[name], changed && name == target, cancelOnProbe
					mu.Unlock()
					owner, observedRun := labName, runID
					if mutated {
						switch kind {
						case "api-owner":
							owner = "foreign-owner"
						case "api-run":
							observedRun = "foreign-run"
						case "api-error":
							http.Error(w, privateDetail, http.StatusServiceUnavailable)
							return
						case "canceled-http":
							cancelRequest()
							select {
							case <-request.Context().Done():
							case <-time.After(time.Second):
								t.Error("private API request did not observe context cancellation")
							}
							return
						}
					}
					body := map[string]any{"Id": id, "Config": map[string]any{"Labels": map[string]string{
						ownerLabel: owner, runLabel: observedRun,
					}}}
					if encodeErr := json.NewEncoder(w).Encode(body); encodeErr != nil {
						t.Errorf("write runtime identity fixture: %v", encodeErr)
					}
				})
				socket := filepath.Join(socketRoot, "api-"+strconv.Itoa(index)+".sock")
				startSocketIdentityServer(t, socket, handler)
				t.Setenv("CONTAINER_HOST", "unix://"+filepath.Join(socketRoot, "cli-A.sock"))
				t.Setenv("PODMAN_HOST", "unix://"+socket)
				client, clientErr := podmanapi.NewClientFromEnvironment()
				if clientErr != nil {
					t.Fatal(clientErr)
				}
				runner := &socketIdentityRunner{
					cli:  &testOnlyIdentityRunner{containers: maps.Clone(ids), runID: runID},
					stop: errors.New("runtime create is forbidden in this boundary fixture"),
				}
				options := DefaultOptions(root)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if preErr := validateVendorTestEngine(ctx, options, runner, client, heldTopology); preErr != nil {
					t.Fatalf("matching original CLI/API identities did not pass the precheck: %v", preErr)
				}
				beforeGoBFD, beforeFRR := gobfdProbes.Load(), frrProbes.Load()
				if beforeGoBFD == 0 || beforeFRR == 0 {
					t.Fatal("precheck did not inspect both original API identities")
				}
				trap := filepath.Join(socketRoot, "trap-"+strconv.Itoa(index)+".sock")
				startSocketIdentityServer(t, trap, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					trapProbes.Add(1)
					http.Error(w, "changed environment must not select this engine", http.StatusServiceUnavailable)
				}))
				t.Setenv("PODMAN_HOST", "unix://"+trap)
				runtimeCtx, cancelRuntime := context.WithCancel(ctx)
				defer cancelRuntime()
				mu.Lock()
				changed, cancelOnProbe = true, cancelRuntime
				if kind == "api-id" || kind == "rewritten-receipt" {
					apiIDs[target] = strings.Repeat("f", 64)
				}
				mu.Unlock()
				if kind == "cli-id" || kind == "rewritten-receipt" {
					runner.cli.containers[target] = strings.Repeat("f", 64)
				}
				if kind == "rewritten-receipt" {
					replacement := topology
					replacement.Containers = maps.Clone(ids)
					replacement.Containers[target] = strings.Repeat("f", 64)
					if writeErr := writeLifecycleReceipt(root, replacement); writeErr != nil {
						t.Fatal(writeErr)
					}
				}
				// Empty artifact identities intentionally stop the positive control before create.
				_, err = executeVendorPayload(runtimeCtx, options, runner, &testRunReceipt{}, heldTopology, client)
				if kind == "exact" {
					if !errors.Is(err, errLifecycleState) || !strings.Contains(err.Error(), artifactError) {
						t.Errorf("matching runtime identities did not reach the artifact boundary: %v", err)
					}
				} else if !errors.Is(err, errLifecycleState) || strings.Contains(err.Error(), artifactError) {
					t.Errorf("changed identity reached artifact validation instead of binding refusal: %v", err)
				}
				if kind == "canceled-http" && !errors.Is(err, context.Canceled) {
					t.Errorf("HTTP cancellation cause was lost: %v", err)
				}
				if err != nil && strings.Contains(err.Error(), privateDetail) {
					t.Error("runtime identity error exposed the private API error body")
				}
				if kind != "cli-id" && kind != "rewritten-receipt" {
					if (target == gobfdContainer && gobfdProbes.Load() <= beforeGoBFD) ||
						(target == frrContainer && frrProbes.Load() <= beforeFRR) {
						t.Error("runtime entry did not re-inspect the held API identity")
					}
				}
				if trapProbes.Load() != 0 {
					t.Errorf("runtime entry reselected the engine from changed environment: %d probes",
						trapProbes.Load())
				}
				if runner.creates != 0 || runner.starts != 0 {
					t.Errorf("runtime boundary allowed create=%d start=%d", runner.creates, runner.starts)
				}
			})
		}
	}
}
