package clabbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type socketIdentityRunner struct {
	cli     *testOnlyIdentityRunner
	stop    error
	creates int
	starts  int
	first   Command
}

func (runner *socketIdentityRunner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == executablePodman && len(command.Arguments) != 0 {
		switch command.Arguments[0] {
		case "create":
			runner.creates++
			if runner.creates == 1 {
				runner.first = command
			}
			return Result{}, runner.stop
		case "start":
			runner.starts++
		}
	}
	return runner.cli.Run(ctx, command)
}

func TestVendorTestDriverBindsSelectedAPISocket(t *testing.T) {
	const runID = "socket-identity-run"
	socketRoot := t.TempDir()
	index := 0
	for _, target := range []string{gobfdContainer, frrContainer} {
		for _, kind := range []string{
			"exact", "mismatched-id", "missing-name", "wrong-owner", "wrong-run", "malformed", "API-error",
			"duplicate-id", "duplicate-config", "duplicate-owner", "invalid-UTF8", "second-document",
			"missing-id", "null-config", "wrong-type-id", "wrong-type-labels",
		} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				index++
				root := compilerSourceFixture(t)
				if err := ensureRuntimeDirectory(root); err != nil {
					t.Fatal(err)
				}
				containers := map[string]string{
					gobfdContainer: strings.Repeat("1", 64), frrContainer: strings.Repeat("2", 64),
				}
				receipt := lifecycleReceipt{
					SchemaVersion: ownedReceiptV3, RunID: runID, Topology: runtimeTopologyPath(root),
					Network:            lifecycleNetwork{Name: networkName(runID)},
					ExpectedContainers: []string{gobfdContainer, frrContainer}, Containers: containers,
					Image: lifecycleImage{Reference: gobfdImageRepo + ":" + runID, ID: testImageID},
					FRR:   lifecycleImage{Reference: frrImageRepo + ":" + runID, ID: testFRRImageID},
				}
				if err := createLifecycleReceipt(root, receipt); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(receiptPath(root))
				if err != nil {
					t.Fatal(err)
				}
				var gobfdProbes, frrProbes, apiMutations atomic.Int64
				handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodGet {
						apiMutations.Add(1)
						http.Error(w, "fixture permits inspection only", http.StatusMethodNotAllowed)
						return
					}
					name := strings.TrimPrefix(request.URL.Path, "/v5.0.0/containers/")
					name = strings.TrimSuffix(name, "/json")
					switch name {
					case gobfdContainer:
						gobfdProbes.Add(1)
					case frrContainer:
						frrProbes.Add(1)
					default:
						http.NotFound(w, request)
						return
					}
					id, owner, observedRun := containers[name], labName, runID
					if name == target {
						switch kind {
						case "mismatched-id":
							id = strings.Repeat("f", 64)
						case "missing-name":
							http.NotFound(w, request)
							return
						case "wrong-owner":
							owner = "foreign-lab"
						case "wrong-run":
							observedRun = "foreign-run"
						case "malformed":
							if _, writeErr := w.Write([]byte(`{"Id":`)); writeErr != nil {
								t.Errorf("write malformed API fixture: %v", writeErr)
							}
							return
						case "API-error":
							http.Error(w, "private fixture unavailable", http.StatusServiceUnavailable)
							return
						}
					}
					body := map[string]any{
						"Id": id, "Name": "/" + name,
						"Config": map[string]any{"Labels": map[string]string{
							ownerLabel: owner, runLabel: observedRun,
						}},
					}
					if name == target {
						switch kind {
						case "missing-id":
							delete(body, "Id")
						case "null-config":
							body["Config"] = nil
						case "wrong-type-id":
							body["Id"] = true
						case "wrong-type-labels":
							body["Config"] = map[string]any{"Labels": []string{labName}}
						}
					}
					raw, encodeErr := json.Marshal(body)
					if encodeErr != nil {
						t.Errorf("encode API identity fixture: %v", encodeErr)
						return
					}
					if name == target {
						switch kind {
						case "duplicate-id":
							raw = []byte(strings.Replace(string(raw), `"Id":`, `"Id":"foreign","Id":`, 1))
						case "duplicate-config":
							raw = []byte(strings.Replace(string(raw), `"Config":`, `"Config":{},"Config":`, 1))
						case "duplicate-owner":
							key := `"` + ownerLabel + `":`
							raw = []byte(strings.Replace(string(raw), key, key+`"foreign",`+key, 1))
						case "invalid-UTF8":
							raw = append([]byte("{\"Ignored\":\"\xff\","), raw[1:]...)
						case "second-document":
							raw = append(raw, '\n', '{', '}')
						}
					}
					if _, writeErr := w.Write(raw); writeErr != nil {
						t.Errorf("write API identity fixture: %v", writeErr)
					}
				})
				socket := filepath.Join(socketRoot, "api-"+strconv.Itoa(index)+".sock")
				startSocketIdentityServer(t, socket, handler)
				// Engine A is the CLI runner fixture; its endpoint is never opened.
				t.Setenv("CONTAINER_HOST", "unix://"+filepath.Join(socketRoot, "cli-A.sock"))
				t.Setenv("PODMAN_HOST", "unix://"+socket)
				stop := errors.New("private fixture stopped first test-control create")
				runner := &socketIdentityRunner{
					cli: &testOnlyIdentityRunner{containers: containers, runID: runID}, stop: stop,
				}
				options := DefaultOptions(root)
				options.TestOnly = true
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				err = runTopologyLocked(ctx, options, runner)
				if kind == "exact" {
					if !errors.Is(err, stop) || runner.creates != 1 || runner.starts != 0 {
						t.Errorf("matching engines error=%v create=%d start=%d; want create sentinel only",
							err, runner.creates, runner.starts)
					}
					if gobfdProbes.Load() == 0 || frrProbes.Load() == 0 {
						t.Errorf("matching API identities were not both inspected: GoBFD=%d FRR=%d",
							gobfdProbes.Load(), frrProbes.Load())
					}
					if !strings.Contains(strings.Join(runner.first.Arguments, " "), "gobfd-clab-compile-") {
						t.Error("positive control did not reach the first compiler test-control create")
					}
				} else {
					if !errors.Is(err, errLifecycleState) && !errors.Is(err, ErrBootstrapFailed) {
						t.Errorf("unbound API identity error=%v, want ownership/binding failure", err)
					}
					if errors.Is(err, stop) || runner.creates != 0 || runner.starts != 0 {
						t.Errorf("unbound socket reached test-control effects: error=%v create=%d start=%d",
							err, runner.creates, runner.starts)
					}
					if gobfdProbes.Load()+frrProbes.Load() == 0 {
						t.Error("driver rejected without inspecting the selected API socket")
					}
					if (target == gobfdContainer && gobfdProbes.Load() == 0) ||
						(target == frrContainer && frrProbes.Load() == 0) {
						t.Error("driver did not inspect the identity selected for this negative fixture")
					}
				}
				if apiMutations.Load() != 0 {
					t.Errorf("identity admission issued %d API mutations", apiMutations.Load())
				}
				after, readErr := os.ReadFile(receiptPath(root))
				if readErr != nil || string(after) != string(before) {
					t.Errorf("identity admission changed the deployed lifecycle receipt: %v", readErr)
				}
			})
		}
	}
}

func startSocketIdentityServer(t *testing.T, socket string, handler http.Handler) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("close private API fixture: %v", closeErr)
		}
		select {
		case serveErr := <-done:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				t.Errorf("private API fixture serve error: %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("private API fixture did not stop within its cleanup bound")
		}
	})
}
