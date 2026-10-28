package clabbootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const testEnvOption = "--env"

type vendorTestEvent struct {
	Action  string `json:"Action"`  //nolint:tagliatelle // test2json wire field.
	Test    string `json:"Test"`    //nolint:tagliatelle // test2json wire field.
	Package string `json:"Package"` //nolint:tagliatelle // test2json wire field.
}

func suiteCompileArguments() []string {
	return []string{
		"go", "test", "-c", "-race", "-trimpath", "-mod=readonly", "-tags", "interop_clab",
		"-o", "/out/interop-clab.test", "./test/interop-clab",
	}
}

func converterCompileArguments() []string {
	return []string{"go", "build", "-trimpath", "-mod=readonly", "-o", "/out/test2json", "cmd/test2json"}
}

func runIsolatedVendorTests(ctx context.Context, options Options, runner Runner) (returnErr error) {
	var client *podmanapi.Client
	var topology lifecycleReceipt
	var runtimeResult Result
	receipt := testRunReceipt{RunID: "dry-run", SourceSHA: strings.Repeat("0", 64), Containers: make(map[string]string)}
	if !options.DryRun {
		var err error
		topology, client, err = resolveVendorTestEngine(options.ProjectRoot)
		if err != nil {
			return err
		}
		if engineErr := validateVendorTestEngine(ctx, options, runner, client, topology); engineErr != nil {
			return engineErr
		}
		prepared, err := prepareTestRun(options.ProjectRoot)
		if err != nil {
			return err
		}
		receipt = prepared
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ownedCleanupTimeout)
			defer cancel()
			returnErr = errors.Join(returnErr, cleanupTestRun(cleanupCtx, options, runner, receipt))
			proof := struct {
				Receipt  testRunReceipt `json:"receipt"`
				ExitCode int            `json:"exit_code"`
				Output   string         `json:"output"`
				Stderr   string         `json:"stderr"`
				Error    string         `json:"error,omitempty"`
			}{
				Receipt: receipt, ExitCode: runtimeResult.ExitCode,
				Output: runtimeResult.Stdout, Stderr: runtimeResult.Stderr,
			}
			if returnErr != nil {
				proof.Error = returnErr.Error()
			}
			path := filepath.Join(runtimeDirectory(options.ProjectRoot), "test-result-"+receipt.RunID+".json")
			returnErr = errors.Join(returnErr, createPrivateReceipt(path, proof))
		}()
	}
	if err := compileVendorPayload(ctx, options, runner, &receipt); err != nil {
		return err
	}
	runtimeResult, err := executeVendorPayload(ctx, options, runner, &receipt, topology, client)
	if !options.DryRun {
		if err == nil {
			err = validateVendorTestResult(runtimeResult.Stdout)
		}
	}
	return err
}

func resolveVendorTestEngine(projectRoot string) (lifecycleReceipt, *podmanapi.Client, error) {
	topology, err := loadLifecycleReceipt(projectRoot)
	if err != nil {
		return lifecycleReceipt{}, nil, err
	}
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return lifecycleReceipt{}, nil, fmt.Errorf("resolve test runtime socket: %w", err)
	}
	return topology, client, nil
}

func validateVendorTestEngine(
	ctx context.Context, options Options, runner Runner, client *podmanapi.Client, topology lifecycleReceipt,
) error {
	if options.DryRun {
		return nil
	}
	if topology.SchemaVersion != ownedReceiptV3 || !validLifecycleReceipt(topology) ||
		topology.Topology != runtimeTopologyPath(options.ProjectRoot) ||
		len(topology.ExpectedContainers) != managedApplicationCount {
		return fmt.Errorf("test engine requires a complete owned topology: %w", errLifecycleState)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := validateDeployedContainerIdentities(checkCtx, options, runner, topology); err != nil {
		return err
	}
	for _, name := range topology.ExpectedContainers {
		wantID := topology.Containers[name]
		if !networkIDRE.MatchString(wantID) {
			return fmt.Errorf("test engine container %s lacks a full checkpointed ID: %w", name, errLifecycleState)
		}
		body, err := client.Inspect(checkCtx, name)
		if err != nil {
			// Inspection error bodies may contain private container configuration.
			return fmt.Errorf("inspect selected engine container %s: %w", name,
				errors.Join(errLifecycleState, checkCtx.Err()))
		}
		var inspection struct {
			ID     string `json:"Id"` //nolint:tagliatelle // Docker-compatible API field.
			Config struct {
				Labels map[string]string `json:"Labels"` //nolint:tagliatelle // Docker-compatible API field.
			} `json:"Config"` //nolint:tagliatelle // Docker-compatible API field.
		}
		if !body.IsValid() || json.Unmarshal(body, &inspection) != nil {
			return fmt.Errorf("invalid selected engine identity for %s: %w", name, errLifecycleState)
		}
		if inspection.ID != wantID || inspection.Config.Labels[ownerLabel] != labName ||
			inspection.Config.Labels[runLabel] != topology.RunID {
			return fmt.Errorf("selected engine container %s differs from the owned topology: %w", name, errLifecycleState)
		}
	}
	return nil
}

func compileVendorPayload(ctx context.Context, options Options, runner Runner, receipt *testRunReceipt) error {
	if !options.DryRun {
		if err := verifyTestSourceSnapshot(options.ProjectRoot, *receipt); err != nil {
			return err
		}
	}
	name := testRunNames(receipt.RunID)[0]
	directory := testRunDirectory(options.ProjectRoot, receipt.RunID)
	args := testControlArguments(name, receipt.RunID)
	args = append(args, "--workdir", "/src",
		"--tmpfs", "/root/.cache/go-build:rw,nosuid,nodev,size=512m",
		"--tmpfs", "/go/pkg/mod:rw,nosuid,nodev,size=512m",
		testEnvOption, "GOTOOLCHAIN=local", testEnvOption, "GOPROXY=off",
		testEnvOption, "GOWORK=off", testEnvOption, "GOFLAGS=-p=1",
		"--mount", "type=bind,src="+filepath.Join(directory, "src")+",dst=/src,ro=true",
		"--mount", "type=bind,src="+filepath.Join(directory, "out")+",dst=/out",
		testCompilerImage, "sleep", "900")
	id, err := createTestControl(ctx, options, runner, receipt, name, args)
	if err != nil {
		return err
	}
	if err := runOwnedTestCommand(ctx, options, runner, receipt, id, []string{"start", id}); err != nil {
		return err
	}
	for index, command := range [][]string{suiteCompileArguments(), converterCompileArguments()} {
		cgo := "1"
		if index != 0 {
			cgo = "0"
		}
		args := append([]string{
			"exec", testEnvOption, "CGO_ENABLED=" + cgo,
			testEnvOption, "GOOS=linux", testEnvOption, "GOARCH=" + runtime.GOARCH, id,
		}, command...)
		if err := runOwnedTestCommand(ctx, options, runner, receipt, id, args); err != nil {
			return err
		}
	}
	if !options.DryRun {
		if err := verifyTestArtifacts(options, receipt); err != nil {
			return err
		}
		if err := writeTestRun(options.ProjectRoot, *receipt); err != nil {
			return err
		}
	}
	return runOwnedTestCommand(ctx, options, runner, receipt, id, []string{"stop", "--time", "5", id})
}

func executeVendorPayload(
	ctx context.Context, options Options, runner Runner, receipt *testRunReceipt,
	topology lifecycleReceipt, client *podmanapi.Client,
) (Result, error) {
	if err := validateVendorTestEngine(ctx, options, runner, client, topology); err != nil {
		return Result{}, err
	}
	socket := "/run/podman/podman.sock"
	if !options.DryRun {
		socket = client.SocketPath()
		if receipt.BinarySHA == "" || receipt.ConverterSHA == "" {
			return Result{}, fmt.Errorf("runtime requires checkpointed compiler artifacts: %w", errLifecycleState)
		}
		if err := verifyTestArtifacts(options, receipt); err != nil {
			return Result{}, err
		}
	}
	name := testRunNames(receipt.RunID)[1]
	directory := testRunDirectory(options.ProjectRoot, receipt.RunID)
	args := testControlArguments(name, receipt.RunID)
	args = append(args, "--workdir", "/workspace/test/interop-clab",
		testEnvOption, "PODMAN_HOST=unix:///run/podman/podman.sock",
		"--mount", "type=bind,src="+filepath.Join(directory, "out")+",dst=/artifacts,ro=true",
		"--mount", "type=bind,src="+filepath.Join(directory, "out")+",dst=/workspace/test/interop-clab,ro=true",
		"--mount", "type=bind,src="+filepath.Join(directory, "src", "test", "e2e", "vendor")+
			",dst=/workspace/test/e2e/vendor,ro=true",
		"--mount", "type=bind,src="+socket+",dst=/run/podman/podman.sock,ro=true",
		testRuntimeImage, "/artifacts/test2json", "-p", testPackagePath, "-t", "/artifacts/interop-clab.test",
		"-test.v=test2json", "-test.timeout=600s", "-test.count=1")
	id, err := createTestControl(ctx, options, runner, receipt, name, args)
	if err != nil {
		return Result{}, err
	}
	if !options.DryRun {
		if identityErr := verifyTestControlIdentity(ctx, runner, id, receipt.RunID); identityErr != nil {
			return Result{}, identityErr
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	result, err := runner.Run(runCtx, Command{
		Executable: executablePodman, Arguments: []string{"start", "--attach", id}, DryRun: options.DryRun,
	})
	if err != nil {
		return result, fmt.Errorf("run precompiled vendor suite: %w", err)
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("vendor runtime exit %d: %s: %w", result.ExitCode, result.Stderr, ErrBootstrapFailed)
	}
	if !options.DryRun {
		if err := verifyTestControlIdentity(ctx, runner, id, receipt.RunID); err != nil {
			return result, err
		}
		if err := verifyTestControlExit(ctx, runner, id); err != nil {
			return result, err
		}
		if err := verifyTestArtifacts(options, receipt); err != nil {
			return result, err
		}
	}
	return result, nil
}

func testControlArguments(name, runID string) []string {
	return []string{
		"create", "--name", name, "--pull", "never", "--platform", "linux/" + runtime.GOARCH,
		"--network", "none", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--security-opt", "label=disable", "--image-volume", "ignore",
		"--cpus", "2", "--memory", "2g", "--memory-swap", "2g", "--pids-limit", "512",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=512m",
		testEnvOption, "GOMAXPROCS=2", testEnvOption, "GOMEMLIMIT=512MiB",
		"--label", ownerLabel + "=" + labName, "--label", runLabel + "=" + runID,
	}
}

func createTestControl(
	ctx context.Context, options Options, runner Runner, receipt *testRunReceipt, name string, args []string,
) (string, error) {
	result, err := runner.Run(ctx, Command{Executable: executablePodman, Arguments: args, DryRun: options.DryRun})
	if err != nil {
		return "", fmt.Errorf("create test control container: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("create test control container exit %d: %w", result.ExitCode, ErrBootstrapFailed)
	}
	if options.DryRun {
		return name, nil
	}
	id := strings.TrimSpace(result.Stdout)
	if len(id) != 64 || !validImageID(id) {
		return "", fmt.Errorf("invalid test container ID: %w", errLifecycleState)
	}
	observed, err := inspectContainerIdentity(ctx, runner, name, receipt.RunID, true)
	if err != nil {
		return "", err
	}
	if observed != id {
		return "", fmt.Errorf("created test container changed identity: %w", errLifecycleState)
	}
	receipt.Containers[name] = id
	if err := writeTestRun(options.ProjectRoot, *receipt); err != nil {
		return "", err
	}
	return id, nil
}

func runTestCommand(ctx context.Context, options Options, runner Runner, args []string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return runCommand(commandCtx, runner, Command{
		Executable: executablePodman, Arguments: args, DryRun: options.DryRun,
	})
}

func runOwnedTestCommand(
	ctx context.Context, options Options, runner Runner, receipt *testRunReceipt, id string, args []string,
) error {
	if !options.DryRun {
		if err := verifyTestControlIdentity(ctx, runner, id, receipt.RunID); err != nil {
			return err
		}
	}
	return runTestCommand(ctx, options, runner, args)
}

func verifyTestControlIdentity(ctx context.Context, runner Runner, id, runID string) error {
	observed, err := inspectContainerIdentity(ctx, runner, id, runID, true)
	if err != nil {
		return err
	}
	if observed != id {
		return fmt.Errorf("test control identity changed: %w", errLifecycleState)
	}
	return nil
}

func verifyTestControlExit(ctx context.Context, runner Runner, id string) error {
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{podmanInspect, "--type", "container", networkFormatOption, "{{json .State}}", id},
	})
	if err != nil {
		return fmt.Errorf("inspect test runtime completion: %w", err)
	}
	if result.ExitCode != 0 || !jsontext.Value(result.Stdout).IsValid() {
		return fmt.Errorf("invalid test runtime completion: %w", errLifecycleState)
	}
	var state struct {
		Status    string `json:"Status"`    //nolint:tagliatelle // Podman State wire field.
		Running   *bool  `json:"Running"`   //nolint:tagliatelle // Podman State wire field.
		ExitCode  *int   `json:"ExitCode"`  //nolint:tagliatelle // Podman State wire field.
		OOMKilled *bool  `json:"OOMKilled"` //nolint:tagliatelle // Podman State wire field.
	}
	if err := json.Unmarshal([]byte(result.Stdout), &state); err != nil {
		return fmt.Errorf("decode test runtime completion: %w", err)
	}
	if state.Status != "exited" || state.Running == nil || *state.Running || state.ExitCode == nil ||
		*state.ExitCode != 0 || state.OOMKilled == nil || *state.OOMKilled {
		return fmt.Errorf("test runtime did not complete successfully: %w", ErrBootstrapFailed)
	}
	return nil
}

func verifyTestArtifacts(options Options, receipt *testRunReceipt) error {
	if err := verifyTestSourceSnapshot(options.ProjectRoot, *receipt); err != nil {
		return err
	}
	directory := filepath.Join(testRunDirectory(options.ProjectRoot, receipt.RunID), "out")
	binary, err := verifyCompiledArtifact(filepath.Join(directory, "interop-clab.test"), testPackagePath+".test", true)
	if err != nil {
		return err
	}
	converter, err := verifyCompiledArtifact(filepath.Join(directory, "test2json"), "cmd/test2json", false)
	if err != nil {
		return err
	}
	if (receipt.BinarySHA != "" && receipt.BinarySHA != binary) ||
		(receipt.ConverterSHA != "" && receipt.ConverterSHA != converter) {
		return fmt.Errorf("compiled artifact changed identity: %w", errLifecycleState)
	}
	receipt.BinarySHA, receipt.ConverterSHA = binary, converter
	return nil
}

func validateVendorTestResult(output string) error {
	packagePassed := false
	required := map[string]bool{
		"TestVendorBFD_SessionEstablish/frr":    false,
		"TestVendorBFD_SessionEstablish/frr-v6": false,
		"TestVendorBFD_FailureDetection/frr":    false,
		"TestVendorBFD_FailureDetection/frr-v6": false,
		"TestVendorBFD_TimerNegotiation/frr":    false,
		"TestVendorBFD_TimerNegotiation/frr-v6": false,
		"TestVendorBFD_DetectionTiming/frr":     false,
		"TestVendorBFD_DetectionTiming/frr-v6":  false,
		"TestVendorBFD_RFC5880_PacketFormat":    false,
		"TestVendorBFD_AllAvailable":            false,
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 4096), maxCommandOutput)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !jsontext.Value(line).IsValid() {
			return fmt.Errorf("invalid vendor test event: %w", errLifecycleState)
		}
		var event vendorTestEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decode vendor test event: %w", err)
		}
		passed, err := recordVendorTestEvent(event, required)
		if err != nil {
			return err
		}
		packagePassed = packagePassed || passed
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read vendor test events: %w", err)
	}
	if !packagePassed {
		return fmt.Errorf("vendor suite has no successful package verdict: %w", ErrBootstrapFailed)
	}
	for name, passed := range required {
		if !passed {
			return fmt.Errorf("required wire test %s did not pass: %w", name, ErrBootstrapFailed)
		}
	}
	return nil
}

func recordVendorTestEvent(event vendorTestEvent, required map[string]bool) (bool, error) {
	if event.Action == "fail" {
		return false, fmt.Errorf("vendor test %s failed: %w", event.Test, ErrBootstrapFailed)
	}
	if event.Action == "skip" {
		if event.Package == testPackagePath && event.Test == "TestVendorBFD_SessionIndependence" {
			return false, nil
		}
		return false, fmt.Errorf("vendor test %s was skipped: %w", event.Test, ErrBootstrapFailed)
	}
	if event.Package != testPackagePath {
		return false, nil
	}
	if event.Action != "pass" {
		return false, nil
	}
	if _, requiredWire := required[event.Test]; requiredWire {
		required[event.Test] = true
	}
	return event.Test == "", nil
}
