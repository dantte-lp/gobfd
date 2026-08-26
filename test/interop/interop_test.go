//go:build interop || interop_testcontainers

// Package interop_test provides Go-driven interoperability tests for GoBFD
// against FRR (bfdd), BIRD3, Holo, and Thoro/bfd, with comprehensive RFC
// 5880/5881 validation via tshark packet capture analysis.
//
// Run with:
//
//	go test -tags interop -v -count=1 -timeout 300s ./test/interop/
//
// Prerequisites:
//   - test/interop/projectctl.sh up
//   - The gobfd, frr, bird3, Holo, Thoro/bfd, and tshark services must be ready.
package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // Interop harness invokes fixed Podman binaries with explicit argument vectors.
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/bfdjitter"
	"github.com/dantte-lp/gobfd/test/internal/frrjson"
)

const (
	defaultInteropProjectName = "gobfd-interop"

	gobfdIP = "172.20.0.10"
	frrIP   = "172.20.0.20"
	bird3IP = "172.20.0.30"
	fuzzIP  = "172.20.0.40"
	holoIP  = "172.20.0.50"
	thoroIP = "172.20.0.60"

	pollInterval = 2 * time.Second
	holoTraffic  = "bfd && ((ip.src == " + holoIP + " && ip.dst == " + gobfdIP +
		") || (ip.src == " + gobfdIP + " && ip.dst == " + holoIP + "))"
	holoDownPackets = "bfd && ip.src == " + gobfdIP + " && ip.dst == " + holoIP +
		" && bfd.sta == 0x01 && bfd.diag == 0x01"
	holoUpPackets = "bfd && ip.src == " + holoIP + " && ip.dst == " + gobfdIP +
		" && bfd.sta == 0x03"

	holoLifecycleBudget    = 180 * time.Second
	holoCleanupBudget      = 75 * time.Second
	holoTestDeadlineMargin = 10 * time.Second
	holoStopTimeout        = 15 * time.Second
	holoStopGraceSeconds   = 5
	holoStartTimeout       = 10 * time.Second
	holoHealthTimeout      = 20 * time.Second
	holoConfigStartTimeout = 15 * time.Second
	holoConfigWaitTimeout  = 10 * time.Second
	holoInspectTimeout     = 5 * time.Second

	// bfdFuzzImage is the image name for the Go BFD invalid-vector generator.
	// Built with podman build (not compose) to avoid compose's "run"
	// behavior that tears down and recreates the entire stack.
	bfdFuzzImage = "gobfd-bfd-fuzz:latest"
)

var (
	errSessionStateNotFound      = errors.New("session state not found")
	errInvalidInteropProjectName = errors.New("invalid interop Compose project name")
	errForeignProjectContainer   = errors.New("container is not owned by the interop Compose project")
	errHoloFrameNotFound         = errors.New("holo BFD frame not found")
	errHoloConfigExitMismatch    = errors.New("holo config exit status mismatch")
	errHoloConfigFailed          = errors.New("holo config failed")
	errInsufficientLifecycleTime = errors.New("insufficient time for Holo lifecycle and cleanup")
)

var interopProjectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type sessionState struct {
	PeerAddress     string `json:"peer_address"`
	LocalState      string `json:"local_state"`
	RemoteState     string `json:"remote_state"`
	LocalDiagnostic string `json:"local_diagnostic"`
}

type holoLifecycleSchedule struct {
	lifecycleDeadline time.Time
	cleanupDeadline   time.Time
}

// =========================================================================
// Infrastructure helpers
// =========================================================================

func resolveInteropProjectName(raw string) (string, error) {
	projectName := raw
	if projectName == "" {
		projectName = defaultInteropProjectName
	}
	if !interopProjectNamePattern.MatchString(projectName) {
		return "", fmt.Errorf("validate INTEROP_PROJECT_NAME %q: %w", projectName, errInvalidInteropProjectName)
	}
	return projectName, nil
}

func interopProjectName() (string, error) {
	return resolveInteropProjectName(os.Getenv("INTEROP_PROJECT_NAME"))
}

func interopNetworkName(projectName string) string {
	return projectName + "_bfdnet"
}

func TestResolveInteropProjectName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw     string
		want    string
		wantErr bool
	}{
		"default": {
			want: defaultInteropProjectName,
		},
		"explicit": {
			raw:  "gobfd-interop_2",
			want: "gobfd-interop_2",
		},
		"uppercase": {
			raw:     "GoBFD-interop",
			wantErr: true,
		},
		"leading separator": {
			raw:     "-gobfd-interop",
			wantErr: true,
		},
		"empty after whitespace": {
			raw:     " ",
			wantErr: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveInteropProjectName(test.raw)
			if test.wantErr {
				if !errors.Is(err, errInvalidInteropProjectName) {
					t.Fatalf("resolveInteropProjectName(%q) error = %v, want errInvalidInteropProjectName", test.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveInteropProjectName(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("resolveInteropProjectName(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestInteropNetworkName(t *testing.T) {
	t.Parallel()

	if got, want := interopNetworkName("custom-name"), "custom-name_bfdnet"; got != want {
		t.Fatalf("interopNetworkName() = %q, want %q", got, want)
	}
}

func parseSessionState(data []byte, peer string) (sessionState, error) {
	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return sessionState{}, fmt.Errorf("decode gobfdctl session state for peer %s: %w", peer, err)
	}
	if state.PeerAddress != peer {
		return sessionState{}, fmt.Errorf(
			"gobfdctl output contains peer %q, want %q: %w",
			state.PeerAddress,
			peer,
			errSessionStateNotFound,
		)
	}
	return state, nil
}

func frameBoundary(filter string, baseline uint64) string {
	return fmt.Sprintf("(%s) && frame.number > %d", filter, baseline)
}

func holoDownBoundary(preStopBaseline uint64) string {
	return frameBoundary(holoDownPackets, preStopBaseline)
}

func planHoloLifecycle(now, testDeadline time.Time, hasTestDeadline bool) (holoLifecycleSchedule, error) {
	schedule := holoLifecycleSchedule{
		lifecycleDeadline: now.Add(holoLifecycleBudget),
		cleanupDeadline:   now.Add(holoLifecycleBudget + holoCleanupBudget),
	}
	if hasTestDeadline && schedule.cleanupDeadline.After(testDeadline.Add(-holoTestDeadlineMargin)) {
		return holoLifecycleSchedule{}, fmt.Errorf(
			"need %v lifecycle plus cleanup budget before test deadline %s: %w",
			holoLifecycleBudget+holoCleanupBudget+holoTestDeadlineMargin,
			testDeadline.Format(time.RFC3339Nano),
			errInsufficientLifecycleTime,
		)
	}
	return schedule, nil
}

func boundedDetachedDeadline(
	now time.Time,
	maxDuration time.Duration,
	latestDeadline time.Time,
	hasLatestDeadline bool,
) time.Time {
	deadline := now.Add(maxDuration)
	if hasLatestDeadline && latestDeadline.Before(deadline) {
		return latestDeadline
	}
	return deadline
}

func boundedDetachedContext(
	parent context.Context,
	maxDuration time.Duration,
	latestDeadline time.Time,
	hasLatestDeadline bool,
) (context.Context, context.CancelFunc) {
	deadline := boundedDetachedDeadline(time.Now(), maxDuration, latestDeadline, hasLatestDeadline)
	return context.WithDeadline(context.WithoutCancel(parent), deadline)
}

func pollUntil[T any](
	ctx context.Context,
	interval time.Duration,
	check func(context.Context) (T, bool, error),
) (T, error) {
	var (
		last    T
		lastErr error
	)
	for {
		value, accepted, err := check(ctx)
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
			last = value
			if accepted {
				return value, nil
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return last, errors.Join(ctx.Err(), lastErr)
			}
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func podmanCommand(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "podman", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := commandContextError(ctx, cmd.Run())
	return buf.String(), err
}

func resolveProjectContainerID(ctx context.Context, containerName string) (string, error) {
	projectName, err := interopProjectName()
	if err != nil {
		return "", err
	}
	output, err := podmanCommand(
		ctx,
		"inspect", "--type", "container",
		"--format", `{{.ID}}|{{ index .Config.Labels "com.docker.compose.project" }}`,
		containerName,
	)
	if err != nil {
		return "", fmt.Errorf("resolve container %s ownership: %w: %s", containerName, err, strings.TrimSpace(output))
	}
	containerID, label, ok := strings.Cut(strings.TrimSpace(output), "|")
	if !ok || containerID == "" || label != projectName {
		return "", fmt.Errorf(
			"resolve container %s: id=%q project=%q, want project %q: %w",
			containerName,
			containerID,
			label,
			projectName,
			errForeignProjectContainer,
		)
	}
	return containerID, nil
}

func projectContainerCommand(
	ctx context.Context,
	containerName string,
	action string,
	args ...string,
) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, containerName)
	if err != nil {
		return "", err
	}
	commandArgs := append([]string{action, containerID}, args...)
	return podmanCommand(ctx, commandArgs...)
}

func projectContainerInspect(ctx context.Context, containerName, format string) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, containerName)
	if err != nil {
		return "", err
	}
	return podmanCommand(ctx, "inspect", "--format", format, containerID)
}

func projectContainerLogs(ctx context.Context, containerName string, args ...string) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, containerName)
	if err != nil {
		return "", err
	}
	commandArgs := append([]string{"logs"}, args...)
	commandArgs = append(commandArgs, containerID)
	return podmanCommand(ctx, commandArgs...)
}

func stopHoloContainer(ctx context.Context) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, "holo-interop")
	if err != nil {
		return "", fmt.Errorf("resolve Holo container for bounded stop: %w", err)
	}
	return podmanCommand(
		ctx,
		"stop", "--time", strconv.Itoa(holoStopGraceSeconds), containerID,
	)
}

func TestProjectContainerCommandUsesExactOwnedID(t *testing.T) {
	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
if [[ "${1:-}" == "inspect" && "$*" == *"index .Config.Labels"* ]]; then
    case "${@: -1}" in
        foreign-interop) printf '%s\n' 'foreign-id|foreign-project' ;;
        *) printf '%s\n' 'immutable-owned-id|gobfd-interop' ;;
    esac
fi
`
	if err := os.WriteFile(filepath.Join(fakeBin, "podman"), []byte(fakePodman), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))
	t.Setenv("INTEROP_FAKE_PODMAN_LOG", commandLog)
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")

	if _, err := resolveProjectContainerID(t.Context(), "foreign-interop"); !errors.Is(err, errForeignProjectContainer) {
		t.Fatalf("resolve foreign container error = %v, want errForeignProjectContainer", err)
	}
	if _, err := projectContainerCommand(t.Context(), "frr-interop", "stop"); err != nil {
		t.Fatalf("stop exact owned container: %v", err)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	if !strings.Contains(string(commands), "stop immutable-owned-id") {
		t.Fatalf("runtime command did not use immutable owned ID; commands:\n%s", commands)
	}
	if strings.Contains(string(commands), "stop frr-interop") {
		t.Fatalf("runtime command used mutable service name; commands:\n%s", commands)
	}
}

func TestStopHoloContainerUsesBoundedExactCommand(t *testing.T) {
	gracePeriod := time.Duration(holoStopGraceSeconds) * time.Second
	if holoStopTimeout <= gracePeriod {
		t.Fatalf("Holo stop outer timeout %v must exceed Podman grace %v", holoStopTimeout, gracePeriod)
	}
	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	startedPath := filepath.Join(t.TempDir(), "stop-started")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
if [[ "${1:-}" == "inspect" && "$*" == *"index .Config.Labels"* ]]; then
    printf '%s\n' 'immutable-holo-id|gobfd-interop'
    exit 0
fi
if [[ "$*" == "stop --time 5 immutable-holo-id" ]]; then
    case "${INTEROP_FAKE_STOP_MODE:-success}" in
        success) printf '%s\n' immutable-holo-id ;;
        failure) printf '%s\n' 'daemon refused bounded stop' >&2; exit 17 ;;
        block) : > "${INTEROP_FAKE_STOP_STARTED}"; exec /bin/sleep 10 ;;
    esac
    exit 0
fi
exit 9
`
	if err := os.WriteFile(filepath.Join(fakeBin, "podman"), []byte(fakePodman), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))
	t.Setenv("INTEROP_FAKE_PODMAN_LOG", commandLog)
	t.Setenv("INTEROP_FAKE_STOP_STARTED", startedPath)
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")

	output, err := stopHoloContainer(t.Context())
	if err != nil || strings.TrimSpace(output) != "immutable-holo-id" {
		t.Fatalf("bounded exact Holo stop output=%q error=%v", output, err)
	}

	t.Setenv("INTEROP_FAKE_STOP_MODE", "failure")
	output, err = stopHoloContainer(t.Context())
	if err == nil || !strings.Contains(output, "daemon refused bounded stop") {
		t.Fatalf("failed bounded Holo stop output=%q error=%v", output, err)
	}

	t.Setenv("INTEROP_FAKE_STOP_MODE", "block")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, stopErr := stopHoloContainer(ctx)
		errCh <- stopErr
	}()
	startDeadline := time.NewTimer(time.Second)
	defer startDeadline.Stop()
	for {
		_, statErr := os.Stat(startedPath)
		if statErr == nil {
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("check bounded Holo stop marker: %v", statErr)
		}
		select {
		case <-startDeadline.C:
			t.Fatal("bounded Holo stop did not start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err = <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled bounded Holo stop error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bounded Holo stop did not return after context cancellation")
	}

	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	if !strings.Contains(string(commands), "stop --time 5 immutable-holo-id") {
		t.Fatalf("Holo stop did not use bounded exact-ID argv; commands:\n%s", commands)
	}
	if strings.Contains(string(commands), "stop immutable-holo-id --time") ||
		strings.Contains(string(commands), "stop holo-interop") {
		t.Fatalf("Holo stop used unsafe argv ordering or mutable name; commands:\n%s", commands)
	}
}

func commandContextError(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	return err
}

func gobfdSessionState(ctx context.Context, peer string) (sessionState, error) {
	output, err := projectContainerCommand(
		ctx,
		"gobfd-interop", "exec",
		"/bin/gobfdctl", "--addr", "127.0.0.1:50051",
		"session", "show", peer, "--format", "json",
	)
	if err != nil {
		return sessionState{}, fmt.Errorf(
			"show current GoBFD session for Holo peer %s: %w: %s",
			peer,
			err,
			strings.TrimSpace(output),
		)
	}

	state, err := parseSessionState([]byte(output), peer)
	if err != nil {
		return sessionState{}, fmt.Errorf("parse current GoBFD session for Holo peer %s: %w", peer, err)
	}
	return state, nil
}

func waitForSessionState(
	ctx context.Context,
	peer string,
	timeout time.Duration,
	accept func(sessionState) bool,
) (sessionState, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	last, err := pollUntil(waitCtx, pollInterval, func(pollCtx context.Context) (sessionState, bool, error) {
		state, err := gobfdSessionState(pollCtx, peer)
		if err != nil {
			return sessionState{}, false, err
		}
		return state, accept(state), nil
	})
	if err != nil {
		return last, fmt.Errorf(
			"wait for current GoBFD session state for peer %s; last state=%+v: %w",
			peer,
			last,
			err,
		)
	}
	return last, nil
}

func waitForHoloHealth(ctx context.Context, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lastStatus, err := pollUntil(waitCtx, pollInterval, func(pollCtx context.Context) (string, bool, error) {
		output, err := projectContainerInspect(pollCtx, "holo-interop", "{{.State.Health.Status}}")
		if err != nil {
			return "", false, err
		}
		status := strings.TrimSpace(output)
		return status, status == "healthy", nil
	})
	if err != nil {
		return fmt.Errorf("wait for Holo health; last status=%q: %w", lastStatus, err)
	}
	return nil
}

func parseContainerExitCode(output string) (uint64, error) {
	status, err := strconv.ParseUint(strings.TrimSpace(output), 10, 8)
	if err != nil {
		return 0, fmt.Errorf("parse container exit code %q: %w", strings.TrimSpace(output), err)
	}
	return status, nil
}

func startAndConfigureHolo(ctx context.Context) error {
	startCtx, cancelStart := context.WithTimeout(ctx, holoStartTimeout)
	output, err := projectContainerCommand(startCtx, "holo-interop", "start")
	cancelStart()
	if err != nil {
		return fmt.Errorf("start Holo service: %w: %s", err, strings.TrimSpace(output))
	}
	if healthErr := waitForHoloHealth(ctx, holoHealthTimeout); healthErr != nil {
		return fmt.Errorf("Holo service did not become ready: %w", healthErr)
	}

	configCtx, cancelConfig := context.WithTimeout(ctx, holoConfigStartTimeout)
	output, err = projectContainerCommand(configCtx, "holo-config-interop", "start")
	cancelConfig()
	if err != nil {
		return fmt.Errorf("restart Holo one-shot configuration service: %w: %s", err, strings.TrimSpace(output))
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, holoConfigWaitTimeout)
	waitOutput, err := projectContainerCommand(waitCtx, "holo-config-interop", "wait")
	cancelWait()
	if err != nil {
		return fmt.Errorf("wait for Holo one-shot configuration service: %w: %s", err, strings.TrimSpace(waitOutput))
	}
	waitStatus, err := parseContainerExitCode(waitOutput)
	if err != nil {
		return fmt.Errorf("read Holo configuration wait status: %w", err)
	}

	inspectCtx, cancelInspect := context.WithTimeout(ctx, holoInspectTimeout)
	inspectOutput, err := projectContainerInspect(inspectCtx, "holo-config-interop", "{{.State.ExitCode}}")
	cancelInspect()
	if err != nil {
		return fmt.Errorf("inspect Holo one-shot configuration service: %w: %s", err, strings.TrimSpace(inspectOutput))
	}
	inspectStatus, err := parseContainerExitCode(inspectOutput)
	if err != nil {
		return fmt.Errorf("read Holo configuration inspect status: %w", err)
	}

	if waitStatus != inspectStatus {
		return fmt.Errorf(
			"Holo configuration wait status %d differs from inspect status %d: %w",
			waitStatus,
			inspectStatus,
			errHoloConfigExitMismatch,
		)
	}
	if inspectStatus != 0 {
		return fmt.Errorf("Holo configuration exited with status %d: %w", inspectStatus, errHoloConfigFailed)
	}
	return nil
}

func frrVtysh(ctx context.Context, command string) (string, error) {
	return projectContainerCommand(ctx, "frr-interop", "exec", "vtysh", "-c", command)
}

// frrVtyshConfig runs a sequence of vtysh commands (e.g., configure terminal,
// bfd, peer ..., shutdown) in a single vtysh session.
func frrVtyshConfig(ctx context.Context, commands ...string) (string, error) {
	args := make([]string, 0, 4+2*len(commands))
	args = append(args, "vtysh")
	for _, cmd := range commands {
		args = append(args, "-c", cmd)
	}
	return projectContainerCommand(ctx, "frr-interop", "exec", args...)
}

func frrBFDPeerStatus(ctx context.Context) (string, error) {
	output, err := frrVtysh(ctx, "show bfd peers json")
	if err != nil {
		return "", fmt.Errorf("vtysh show bfd peers json: %w: %s", err, output)
	}

	jsonStr, err := frrjson.ExtractJSONArray(output)
	if err != nil {
		return "", err
	}

	var peers []struct {
		Peer   string `json:"peer"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &peers); err != nil {
		return "", fmt.Errorf("parse bfd peers json: %w: raw=%s", err, jsonStr)
	}

	for _, p := range peers {
		if p.Peer == gobfdIP {
			return strings.ToLower(p.Status), nil
		}
	}

	return "", fmt.Errorf("peer %s not found in FRR BFD peers", gobfdIP)
}

func bird3BFDSessionUp(ctx context.Context) (bool, error) {
	output, err := projectContainerCommand(ctx, "bird3-interop", "exec", "birdc", "show bfd sessions")
	if err != nil {
		return false, fmt.Errorf("birdc show bfd sessions: %w: %s", err, output)
	}

	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, gobfdIP) && strings.Contains(strings.ToLower(line), "up") {
			return true, nil
		}
	}

	return false, nil
}

func waitForCondition(
	ctx context.Context,
	t *testing.T,
	desc string,
	timeout time.Duration,
	fn func(context.Context) (bool, error),
) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error

	for {
		ok, err := fn(waitCtx)
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
		}
		if ok {
			return
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if lastErr != nil {
				t.Fatalf("condition %q not met within %v: last error: %v", desc, timeout, lastErr)
			}
			t.Fatalf("condition %q not met within %v: %v", desc, timeout, waitCtx.Err())
		case <-timer.C:
		}
	}
}

// waitFRRUp waits for the FRR BFD session to reach Up state.
func waitFRRUp(ctx context.Context, t *testing.T, timeout time.Duration) {
	t.Helper()
	waitForCondition(ctx, t, "FRR BFD session Up", timeout, func(pollCtx context.Context) (bool, error) {
		status, err := frrBFDPeerStatus(pollCtx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})
}

// waitBIRD3Up waits for the BIRD3 BFD session to reach Up state.
func waitBIRD3Up(ctx context.Context, t *testing.T, timeout time.Duration) {
	t.Helper()
	waitForCondition(ctx, t, "BIRD3 BFD session Up", timeout, bird3BFDSessionUp)
}

// holoSessionUp checks the current GoBFD API view of the GoBFD <-> Holo session.
func holoSessionUp(ctx context.Context) (bool, error) {
	state, err := gobfdSessionState(ctx, holoIP)
	if err != nil {
		return false, err
	}
	return state.LocalState == "Up" && state.RemoteState == "Up", nil
}

// waitHoloUp waits for the Holo BFD session to reach Up state.
func waitHoloUp(ctx context.Context, t *testing.T, timeout time.Duration) {
	t.Helper()
	waitForCondition(ctx, t, "Holo BFD session Up", timeout, holoSessionUp)
}

// thoroSessionUp checks if the GoBFD <-> Thoro/bfd session is Up
// by looking for Up packets from Thoro/bfd in the tshark capture.
func thoroSessionUp(ctx context.Context) (bool, error) {
	count, err := tsharkCount(ctx,
		"bfd && ip.src == "+thoroIP+" && ip.dst == "+gobfdIP+" && bfd.sta == 0x03")
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func thoroLogs(ctx context.Context) (string, error) {
	return projectContainerLogs(ctx, "thoro-interop", "--tail", "200")
}

func isThoroUnsupportedPollSequenceCrash(logs string) bool {
	return strings.Contains(logs, "panic: Not implemented") &&
		strings.Contains(logs, "SetDesiredMinTxInterval")
}

func thoroUnsupportedPollSequenceCrash(ctx context.Context) (bool, error) {
	logs, err := thoroLogs(ctx)
	if err != nil {
		return false, err
	}
	return isThoroUnsupportedPollSequenceCrash(logs), nil
}

// waitThoroUp waits for the Thoro/bfd BFD session to reach Up state.
func waitThoroUp(ctx context.Context, t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		crashed, err := thoroUnsupportedPollSequenceCrash(ctx)
		if err != nil {
			lastErr = err
		}
		if crashed {
			t.Skip("Thoro/bfd skipped: upstream peer panicked on unimplemented RFC 5880 poll-sequence interval update")
		}

		ok, err := thoroSessionUp(ctx)
		if err != nil {
			lastErr = err
		}
		if ok {
			return
		}
		time.Sleep(pollInterval)
	}

	if lastErr != nil {
		t.Fatalf("condition %q not met within %v: last error: %v", "Thoro/bfd BFD session Up", timeout, lastErr)
	}
	t.Fatalf("condition %q not met within %v", "Thoro/bfd BFD session Up", timeout)
}

// =========================================================================
// Tshark analysis helpers
// =========================================================================

// tsharkQuery runs tshark on the captured pcapng file and returns stdout.
func tsharkQuery(ctx context.Context, args ...string) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, "tshark-interop")
	if err != nil {
		return "", fmt.Errorf("tshark: resolve capture container: %w", err)
	}
	commandArgs := append([]string{"tshark", "-r", "/captures/bfd.pcapng"}, args...)
	commandArgs = append([]string{"exec", containerID}, commandArgs...)
	cmd := exec.CommandContext(ctx, "podman", commandArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = commandContextError(ctx, cmd.Run())
	if err != nil {
		return "", fmt.Errorf(
			"tshark: %w: stdout: %s; stderr: %s",
			err,
			strings.TrimSpace(stdout.String()),
			strings.TrimSpace(stderr.String()),
		)
	}
	return stdout.String(), nil
}

// tsharkFields extracts specific fields from packets matching a display filter.
// Returns [][]string where each row is one packet's field values.
func tsharkFields(ctx context.Context, filter string, fields []string, maxCount int) ([][]string, error) {
	output, err := tsharkFieldStream(ctx, filter, fields, maxCount)
	if err != nil {
		return nil, err
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	var rows [][]string
	for line := range strings.SplitSeq(output, "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

func tsharkFieldStream(ctx context.Context, filter string, fields []string, maxCount int) (string, error) {
	args := []string{"-Y", filter, "-T", "fields"}
	for _, f := range fields {
		args = append(args, "-e", f)
	}
	args = append(args, "-E", "separator=\t", "-E", "header=n")
	if maxCount > 0 {
		args = append(args, "-c", strconv.Itoa(maxCount))
	}

	return tsharkQuery(ctx, args...)
}

// tsharkCount returns the number of packets matching a display filter.
func tsharkCount(ctx context.Context, filter string) (int, error) {
	output, err := tsharkQuery(ctx, "-Y", filter, "-T", "fields", "-e", "frame.number")
	if err != nil {
		return 0, err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, nil
	}
	return len(strings.Split(output, "\n")), nil
}

func lastFrameNumber(rows [][]string) (uint64, error) {
	var last uint64
	for i, row := range rows {
		if len(row) == 0 || strings.TrimSpace(row[0]) == "" {
			continue
		}
		frame, err := strconv.ParseUint(strings.TrimSpace(row[0]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse captured Holo BFD frame number at row %d: %w", i, err)
		}
		if frame > last {
			last = frame
		}
	}
	if last == 0 {
		return 0, fmt.Errorf("captured frame rows: %w", errHoloFrameNotFound)
	}
	return last, nil
}

func lastFrameForFilter(ctx context.Context, filter string) (uint64, error) {
	rows, err := tsharkFields(ctx, filter, []string{"frame.number"}, 0)
	if err != nil {
		return 0, fmt.Errorf("read captured frame numbers for filter %q: %w", filter, err)
	}
	frame, err := lastFrameNumber(rows)
	if err != nil {
		return 0, fmt.Errorf("parse captured frame numbers for filter %q: %w", filter, err)
	}
	return frame, nil
}

func lastHoloFrame(ctx context.Context) (uint64, error) {
	frame, err := lastFrameForFilter(ctx, holoTraffic)
	if err != nil {
		return 0, fmt.Errorf("read last bidirectional Holo BFD frame: %w", err)
	}
	return frame, nil
}

func waitForFrame(ctx context.Context, filter string, timeout time.Duration) (uint64, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	frame, err := pollUntil(waitCtx, pollInterval, func(pollCtx context.Context) (uint64, bool, error) {
		frame, err := lastFrameForFilter(pollCtx, filter)
		if err != nil {
			if errors.Is(err, errHoloFrameNotFound) {
				return 0, false, nil
			}
			return 0, false, err
		}
		return frame, true, nil
	})
	if err != nil {
		return 0, fmt.Errorf("wait for captured frame with filter %q: %w", filter, err)
	}
	return frame, nil
}

func waitForHoloDownFrame(ctx context.Context, preStopBaseline uint64, timeout time.Duration) (uint64, error) {
	filter := holoDownBoundary(preStopBaseline)
	frame, err := waitForFrame(ctx, filter, timeout)
	if err != nil {
		return 0, fmt.Errorf(
			"wait for GoBFD Down/ControlTimeExpired frame after pre-stop baseline %d: %w",
			preStopBaseline,
			err,
		)
	}
	return frame, nil
}

func waitForNewHoloUpPacket(ctx context.Context, baseline uint64, timeout time.Duration) error {
	filter := frameBoundary(holoUpPackets, baseline)
	if _, err := waitForFrame(ctx, filter, timeout); err != nil {
		return fmt.Errorf("wait for Holo-originated Up frame after Down baseline %d: %w", baseline, err)
	}
	return nil
}

func requireNewHoloUpPacket(ctx context.Context, t *testing.T, baseline uint64, timeout time.Duration) {
	t.Helper()
	if err := waitForNewHoloUpPacket(ctx, baseline, timeout); err != nil {
		t.Fatal(err)
	}
}

// assertNoPackets fails the test if any packets match the display filter.
// Used for negative assertions (e.g., "no packets with TTL != 255").
func assertNoPackets(ctx context.Context, t *testing.T, filter, desc string) {
	t.Helper()
	count, err := tsharkCount(ctx, filter)
	if err != nil {
		t.Fatalf("tshark query failed for %s: %v", desc, err)
	}
	if count > 0 {
		t.Errorf("RFC violation: %s — found %d packets (filter: %s)", desc, count, filter)
	}
}

// assertHasPackets fails the test if no packets match the display filter.
func assertHasPackets(ctx context.Context, t *testing.T, filter, desc string) {
	t.Helper()
	count, err := tsharkCount(ctx, filter)
	if err != nil {
		t.Fatalf("tshark query failed for %s: %v", desc, err)
	}
	if count == 0 {
		t.Errorf("expected packets for %s but found none (filter: %s)", desc, filter)
	}
}

// parseHexOrDec parses a string that may be hex (0x...) or decimal.
func parseHexOrDec(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	return strconv.ParseUint(s, 0, 64)
}

func dumpTsharkCapture(t *testing.T, count int) {
	t.Helper()
	testDeadline, hasTestDeadline := t.Deadline()
	if hasTestDeadline {
		testDeadline = testDeadline.Add(-time.Second)
	}
	ctx, cancel := boundedDetachedContext(t.Context(), 10*time.Second, testDeadline, hasTestDeadline)
	defer cancel()

	output, err := projectContainerCommand(
		ctx, "tshark-interop", "exec",
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd",
		"-c", strconv.Itoa(count),
		"-T", "fields",
		"-e", "frame.time_relative",
		"-e", "ip.src",
		"-e", "ip.dst",
		"-e", "bfd.sta",
		"-e", "bfd.flags",
		"-e", "bfd.my_discriminator",
		"-e", "bfd.your_discriminator",
		"-e", "bfd.desired_min_tx_interval",
		"-e", "bfd.required_min_rx_interval",
		"-E", "header=y",
		"-E", "separator=\t",
	)
	if err != nil {
		t.Logf("tshark dump unavailable: %v", err)
		return
	}
	t.Logf("BFD packet capture (last %d packets):\n%s", count, output)
}

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func TestParseSessionState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json []byte
		peer string
		want sessionState
	}{
		{
			name: "up",
			json: []byte(`{
				"peer_address": "172.20.0.50",
				"local_address": "172.20.0.10",
				"type": "single-hop",
				"local_state": "Up",
				"remote_state": "Up",
				"local_diagnostic": "None",
				"local_discriminator": 101,
				"remote_discriminator": 202
			}`),
			peer: "172.20.0.50",
			want: sessionState{
				PeerAddress:     "172.20.0.50",
				LocalState:      "Up",
				RemoteState:     "Up",
				LocalDiagnostic: "None",
			},
		},
		{
			name: "control detection time expired",
			json: []byte(`{
				"peer_address": "172.20.0.50",
				"local_state": "Down",
				"remote_state": "Up",
				"local_diagnostic": "ControlTimeExpired"
			}`),
			peer: "172.20.0.50",
			want: sessionState{
				PeerAddress:     "172.20.0.50",
				LocalState:      "Down",
				RemoteState:     "Up",
				LocalDiagnostic: "ControlTimeExpired",
			},
		},
		{
			name: "duplicate field keeps last value",
			json: []byte(`{
				"peer_address": "172.20.0.50",
				"local_state": "Down",
				"local_state": "Up",
				"remote_state": "Up",
				"local_diagnostic": "None"
			}`),
			peer: "172.20.0.50",
			want: sessionState{
				PeerAddress:     "172.20.0.50",
				LocalState:      "Up",
				RemoteState:     "Up",
				LocalDiagnostic: "None",
			},
		},
		{
			name: "invalid UTF-8 uses replacement character",
			json: []byte(
				"{\"peer_address\":\"172.20.0.\xff\"," +
					"\"local_state\":\"Down\",\"remote_state\":\"Down\"," +
					"\"local_diagnostic\":\"None\"}",
			),
			peer: "172.20.0.\ufffd",
			want: sessionState{
				PeerAddress:     "172.20.0.\ufffd",
				LocalState:      "Down",
				RemoteState:     "Down",
				LocalDiagnostic: "None",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSessionState(tt.json, tt.peer)
			if err != nil {
				t.Fatalf("parseSessionState() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseSessionState() = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("malformed JSON preserves syntax error", func(t *testing.T) {
		t.Parallel()

		_, err := parseSessionState([]byte(`{"peer_address":`), "172.20.0.50")
		if err == nil {
			t.Fatal("parseSessionState() error = nil, want syntax error")
		}
		if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
			t.Errorf("parseSessionState() error type = %T, want wrapped *json.SyntaxError", err)
		}
	})

	t.Run("different peer is not found", func(t *testing.T) {
		t.Parallel()

		_, err := parseSessionState([]byte(`{
			"peer_address": "172.20.0.60",
			"local_state": "Up",
			"remote_state": "Up",
			"local_diagnostic": "None"
		}`), "172.20.0.50")
		if !errors.Is(err, errSessionStateNotFound) {
			t.Errorf("parseSessionState() error = %v, want errSessionStateNotFound", err)
		}
	})
}

func TestFrameBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   string
		baseline uint64
		want     string
	}{
		{
			name:     "zero baseline",
			filter:   "bfd && ip.src == 172.20.0.50",
			baseline: 0,
			want:     "(bfd && ip.src == 172.20.0.50) && frame.number > 0",
		},
		{
			name:     "captured baseline",
			filter:   "bfd && ip.src == 172.20.0.50 && ip.dst == 172.20.0.10 && bfd.sta == 0x03",
			baseline: 412,
			want:     "(bfd && ip.src == 172.20.0.50 && ip.dst == 172.20.0.10 && bfd.sta == 0x03) && frame.number > 412",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := frameBoundary(tt.filter, tt.baseline); got != tt.want {
				t.Errorf("frameBoundary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHoloDownBoundary(t *testing.T) {
	t.Parallel()

	t.Run("filter is strictly after pre-stop baseline", func(t *testing.T) {
		t.Parallel()

		got := holoDownBoundary(41)
		want := "(bfd && ip.src == 172.20.0.10 && ip.dst == 172.20.0.50 && " +
			"bfd.sta == 0x01 && bfd.diag == 0x01) && frame.number > 41"
		if got != want {
			t.Errorf("holoDownBoundary() = %q, want %q", got, want)
		}
	})

	tests := []struct {
		name    string
		rows    [][]string
		want    uint64
		wantErr func(error) bool
	}{
		{
			name: "returns greatest frame",
			rows: [][]string{{"38"}, {"42"}, {"41"}},
			want: 42,
		},
		{
			name: "malformed frame",
			rows: [][]string{{"not-a-frame"}},
			wantErr: func(err error) bool {
				_, ok := errors.AsType[*strconv.NumError](err)
				return ok
			},
		},
		{
			name: "no frame",
			rows: [][]string{{}, {"  "}},
			wantErr: func(err error) bool {
				return errors.Is(err, errHoloFrameNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := lastFrameNumber(tt.rows)
			if tt.wantErr != nil {
				if !tt.wantErr(err) {
					t.Fatalf("lastFrameNumber() error = %v, want matching error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("lastFrameNumber() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("lastFrameNumber() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLifecycleDeadline(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		testDeadline time.Time
		hasDeadline  bool
		wantErr      bool
	}{
		{
			name:        "no test deadline uses fixed budgets",
			hasDeadline: false,
		},
		{
			name:         "300 second test deadline reserves cleanup and margin",
			testDeadline: now.Add(300 * time.Second),
			hasDeadline:  true,
		},
		{
			name:         "exact required budget",
			testDeadline: now.Add(265 * time.Second),
			hasDeadline:  true,
		},
		{
			name:         "insufficient time before mutation",
			testDeadline: now.Add(264 * time.Second),
			hasDeadline:  true,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			schedule, err := planHoloLifecycle(now, tt.testDeadline, tt.hasDeadline)
			if tt.wantErr {
				if !errors.Is(err, errInsufficientLifecycleTime) {
					t.Fatalf("planHoloLifecycle() error = %v, want errInsufficientLifecycleTime", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("planHoloLifecycle() error = %v", err)
			}
			if got := schedule.lifecycleDeadline.Sub(now); got != 180*time.Second {
				t.Errorf("lifecycle budget = %v, want 180s", got)
			}
			if got := schedule.cleanupDeadline.Sub(now); got != 255*time.Second {
				t.Errorf("cleanup deadline offset = %v, want 255s", got)
			}
			if tt.hasDeadline && schedule.cleanupDeadline.After(tt.testDeadline.Add(-10*time.Second)) {
				t.Errorf(
					"cleanup deadline %v exceeds test deadline margin %v",
					schedule.cleanupDeadline,
					tt.testDeadline.Add(-10*time.Second),
				)
			}
		})
	}

	t.Run("detached diagnostics use the earlier deadline", func(t *testing.T) {
		t.Parallel()

		got := boundedDetachedDeadline(now, 10*time.Second, now.Add(4*time.Second), true)
		if want := now.Add(4 * time.Second); !got.Equal(want) {
			t.Errorf("boundedDetachedDeadline() = %v, want %v", got, want)
		}
	})
}

func TestPoll(t *testing.T) {
	t.Parallel()

	t.Run("returns first accepted value", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		attempts := 0
		got, err := pollUntil(ctx, 0, func(context.Context) (int, bool, error) {
			attempts++
			return attempts, attempts == 2, nil
		})
		if err != nil {
			t.Fatalf("pollUntil() error = %v", err)
		}
		if got != 2 {
			t.Errorf("pollUntil() = %d, want 2", got)
		}
	})

	t.Run("preserves context and last poll error", func(t *testing.T) {
		t.Parallel()

		pollErr := errors.New("poll failed")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := pollUntil(ctx, time.Hour, func(context.Context) (int, bool, error) {
			return 0, false, pollErr
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("pollUntil() error = %v, want context.Canceled", err)
		}
		if !errors.Is(err, pollErr) {
			t.Errorf("pollUntil() error = %v, want poll error", err)
		}
	})
}

func TestTsharkQueryContextError(t *testing.T) {
	tempDir := t.TempDir()
	podmanPath := filepath.Join(tempDir, "podman")
	startedPath := filepath.Join(tempDir, "started")
	commandLog := filepath.Join(tempDir, "podman.log")
	fakePodman := `#!/bin/sh
printf '%s\n' "$*" >> "${TSHARK_QUERY_COMMAND_LOG}"
case "$*" in
    "inspect --type container --format "*" tshark-interop")
        printf '%s\n' 'immutable-tshark-id|gobfd-interop'
        ;;
    "exec immutable-tshark-id tshark -r /captures/bfd.pcapng")
        : > "${TSHARK_QUERY_STARTED}"
        exec /bin/sleep 10
        ;;
    *) exit 9 ;;
esac
`
	if err := os.WriteFile(
		podmanPath,
		[]byte(fakePodman),
		0o700,
	); err != nil {
		t.Fatalf("write fake podman command: %v", err)
	}
	t.Setenv("PATH", tempDir)
	t.Setenv("TSHARK_QUERY_STARTED", startedPath)
	t.Setenv("TSHARK_QUERY_COMMAND_LOG", commandLog)
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := tsharkQuery(ctx)
		errCh <- err
	}()

	startDeadline := time.NewTimer(time.Second)
	defer startDeadline.Stop()
	for {
		_, err := os.Stat(startedPath)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("check fake podman start marker: %v", err)
		}
		select {
		case <-startDeadline.C:
			t.Fatal("fake podman command did not start")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("tsharkQuery() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tsharkQuery() did not return after context cancellation")
	}

	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman command log: %v", err)
	}
	if !strings.Contains(string(commands), "exec immutable-tshark-id tshark -r /captures/bfd.pcapng") {
		t.Fatalf("context test did not cancel exact-ID tshark exec; commands:\n%s", commands)
	}
	if strings.Contains(string(commands), "exec tshark-interop") {
		t.Fatalf("context test used mutable tshark container name; commands:\n%s", commands)
	}
}

func TestTsharkQueryKeepsSuccessfulStderrOutOfRows(t *testing.T) {
	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
if [[ "${1:-}" == "inspect" && "$*" == *"index .Config.Labels"* ]]; then
    printf '%s\n' 'immutable-tshark-id|gobfd-interop'
    exit 0
fi
if [[ "${1:-}" == "exec" && "${2:-}" == "immutable-tshark-id" ]]; then
    printf '%s\n' 'Running as user "root" and group "root". This could be dangerous.' >&2
    if [[ "${INTEROP_FAKE_TSHARK_FAIL:-}" == "true" ]]; then
        printf '%s\n' 'capture read failed' >&2
        exit 17
    fi
    printf '41\t300000\n42\t300000\n'
    exit 0
fi
exit 9
`
	if err := os.WriteFile(filepath.Join(fakeBin, "podman"), []byte(fakePodman), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))
	t.Setenv("INTEROP_FAKE_PODMAN_LOG", commandLog)
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")

	rows, err := tsharkFields(t.Context(), "bfd", []string{"frame.number", "bfd.min_rx"}, 0)
	if err != nil {
		t.Fatalf("query successful tshark rows: %v", err)
	}
	wantRows := [][]string{{"41", "300000"}, {"42", "300000"}}
	if fmt.Sprint(rows) != fmt.Sprint(wantRows) {
		t.Fatalf("tshark rows = %v, want stdout-only %v", rows, wantRows)
	}

	t.Setenv("INTEROP_FAKE_TSHARK_FAIL", "true")
	_, err = tsharkQuery(t.Context(), "-Y", "bfd")
	if err == nil || !strings.Contains(err.Error(), "capture read failed") {
		t.Fatalf("failed tshark query error = %v, want stderr diagnostic", err)
	}

	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	if !strings.Contains(string(commands), "exec immutable-tshark-id tshark") {
		t.Fatalf("tshark query did not use exact owned ID; commands:\n%s", commands)
	}
	if strings.Contains(string(commands), "exec tshark-interop") {
		t.Fatalf("tshark query used mutable container name; commands:\n%s", commands)
	}
}

// =========================================================================
// Test 1-2: Baseline handshake (existing)
// =========================================================================

// TestFRRHandshake verifies that the BFD three-way handshake completes
// between GoBFD and FRR, resulting in both sides reporting session Up.
func TestFRRHandshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	waitFRRUp(t.Context(), t, 60*time.Second)
}

// TestBIRD3Handshake verifies that the BFD three-way handshake completes
// between GoBFD and BIRD3, resulting in both sides reporting session Up.
func TestBIRD3Handshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	waitBIRD3Up(t.Context(), t, 60*time.Second)
}

// TestHoloHandshake verifies that the BFD three-way handshake completes
// between GoBFD and Holo, resulting in both sides reporting session Up.
func TestHoloHandshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	waitHoloUp(t.Context(), t, 60*time.Second)
}

// TestThoroHandshake verifies that the BFD three-way handshake completes
// between GoBFD and Thoro/bfd (Go BFD daemon with gRPC API, RFC 5880/5881).
// No authentication is used because Thoro/bfd does not implement auth
// verification on the receive path.
func TestThoroHandshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	waitThoroUp(t.Context(), t, 60*time.Second)
}

// =========================================================================
// Test 3: Comprehensive RFC 5880/5881 compliance
// =========================================================================

// TestRFCCompliance runs all RFC compliance subtests in a defined order.
// Read-only tshark analysis runs first, then state-changing tests that
// clean up after themselves. The stack should be left in a working state
// for subsequent tests.
func TestRFCCompliance(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 200)
		}
	})
	ctx := t.Context()

	// Prerequisite: all sessions must be Up.
	waitFRRUp(ctx, t, 60*time.Second)
	waitBIRD3Up(ctx, t, 60*time.Second)
	waitHoloUp(ctx, t, 60*time.Second)
	waitThoroUp(ctx, t, 60*time.Second)

	// Allow tshark capture to accumulate data before read-only analysis.
	time.Sleep(3 * time.Second)

	// Filter prefix for packets originated by GoBFD.
	const gobfdPkts = "bfd && ip.src == " + gobfdIP

	// -----------------------------------------------------------------
	// Group A: Packet-Level Invariants (RFC 5880 §4.1, RFC 5881 §4-5)
	// Read-only: analyze existing capture without modifying state.
	// -----------------------------------------------------------------

	t.Run("RFC5880_4.1_Version", func(t *testing.T) {
		// RFC 5880 §4.1: "The version number of the protocol. This
		// document defines protocol version 1."
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.version != 1",
			"all GoBFD packets must have version=1")
	})

	t.Run("RFC5880_4.1_MultipointZero", func(t *testing.T) {
		// RFC 5880 §4.1: "This bit is reserved for future
		// point-to-multipoint extensions. It MUST be zero."
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.flags.m == 1",
			"multipoint bit must always be 0")
	})

	t.Run("RFC5880_4.1_DemandZero", func(t *testing.T) {
		// Demand mode not implemented; D bit must always be 0.
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.flags.d == 1",
			"demand bit must be 0 (not implemented)")
	})

	t.Run("RFC5881_4_EchoIntervalZero", func(t *testing.T) {
		// RFC 5881 §4: "If a BFD implementation does not support the
		// Echo function, it MUST set Required Min Echo RX Interval to 0."
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.required_min_echo_interval != 0",
			"echo not implemented: RequiredMinEchoRxInterval must be 0")
	})

	t.Run("RFC5880_6.8.7_MyDiscrNonZero", func(t *testing.T) {
		// RFC 5880 §6.8.7: "The transmitting system MUST set My
		// Discriminator to a unique, nonzero discriminator value."
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.my_discriminator == 0x00000000",
			"My Discriminator must be nonzero in all packets")
	})

	t.Run("RFC5880_4.1_PacketLength", func(t *testing.T) {
		// Without authentication, BFD Control is exactly 24 bytes.
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.message_length != 24",
			"packet length must be 24 (no auth)")
	})

	t.Run("RFC5881_5_TTL255", func(t *testing.T) {
		// RFC 5881 §5: "BFD Control packets MUST be transmitted with
		// a TTL/Hop Limit value of 255."
		assertNoPackets(ctx, t,
			gobfdPkts+" && ip.ttl != 255",
			"all single-hop packets must have TTL=255 (GTSM)")
	})

	t.Run("RFC5881_4_DstPort3784", func(t *testing.T) {
		// RFC 5881 §4: single-hop BFD uses destination port 3784.
		assertNoPackets(ctx, t,
			gobfdPkts+" && udp.dstport != 3784",
			"destination port must be 3784")
	})

	t.Run("RFC5881_4_SrcPortEphemeral", func(t *testing.T) {
		// RFC 5881 §4: "BFD Control packets MUST be transmitted with
		// a source port in the range 49152 through 65535."
		assertNoPackets(ctx, t,
			gobfdPkts+" && (udp.srcport < 49152 || udp.srcport > 65535)",
			"source port must be in 49152-65535")
	})

	// -----------------------------------------------------------------
	// Group A (continued): Peer-originated packet invariants
	// Validate that each peer also sends RFC-compliant packets.
	// -----------------------------------------------------------------

	t.Run("PeerPacketInvariants", func(t *testing.T) {
		type peer struct {
			name string
			ip   string
			// skipSrcPort: BIRD3 uses a fixed source port outside the
			// ephemeral range (known RFC 5881 §4 deviation).
			skipSrcPort bool
		}
		peers := []peer{
			{"FRR", frrIP, false},
			{"BIRD3", bird3IP, true},
			{"Holo", holoIP, false},
			{"Thoro", thoroIP, false},
		}
		for _, p := range peers {
			t.Run(p.name+"_Version1", func(t *testing.T) {
				assertNoPackets(ctx, t,
					"bfd && ip.src == "+p.ip+" && bfd.version != 1",
					p.name+" packets must have version=1")
			})
			t.Run(p.name+"_TTL255", func(t *testing.T) {
				assertNoPackets(ctx, t,
					"bfd && ip.src == "+p.ip+" && ip.ttl != 255",
					p.name+" packets must have TTL=255 (GTSM)")
			})
			t.Run(p.name+"_DstPort3784", func(t *testing.T) {
				assertNoPackets(ctx, t,
					"bfd && ip.src == "+p.ip+" && udp.dstport != 3784",
					p.name+" packets must use dst port 3784")
			})
			t.Run(p.name+"_SrcPortEphemeral", func(t *testing.T) {
				if p.skipSrcPort {
					t.Skipf("%s uses a fixed source port (known RFC 5881 §4 deviation)", p.name)
				}
				assertNoPackets(ctx, t,
					"bfd && ip.src == "+p.ip+" && (udp.srcport < 49152 || udp.srcport > 65535)",
					p.name+" packets must use ephemeral src port")
			})
		}
	})

	// -----------------------------------------------------------------
	// Group B: Handshake & State Sequence (RFC 5880 §6.2, §6.8.6)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.2_HandshakeSequence", func(t *testing.T) {
		// Verify GoBFD→peer packets follow strict Down→Init→Up
		// for all 4 peers. No state regressions during initial handshake.
		type peer struct {
			name string
			ip   string
		}
		peers := []peer{
			{"FRR", frrIP},
			{"BIRD3", bird3IP},
			{"Holo", holoIP},
			{"Thoro", thoroIP},
		}
		for _, p := range peers {
			t.Run(p.name, func(t *testing.T) {
				rows, err := tsharkFields(ctx,
					"bfd && ip.src == "+gobfdIP+" && ip.dst == "+p.ip,
					[]string{"bfd.sta"}, 0)
				if err != nil {
					t.Fatalf("tshark: %v", err)
				}
				if len(rows) == 0 {
					t.Fatalf("no packets from GoBFD to %s", p.name)
				}

				var maxState uint64
				for i, row := range rows {
					state, err := parseHexOrDec(row[0])
					if err != nil {
						t.Fatalf("parse state at row %d: %v", i, err)
					}
					if state > maxState {
						maxState = state
					} else if state < maxState {
						t.Errorf("state regression at packet %d: state=%d after reaching %d",
							i, state, maxState)
						break
					}
					if state == 3 { // Up
						break
					}
				}
				if maxState != 3 {
					t.Errorf("handshake did not reach Up (max state: %d)", maxState)
				}
			})
		}
	})

	t.Run("RFC5880_6.8.6_DiscrLearning", func(t *testing.T) {
		// RFC 5880 §6.8.6: YourDiscriminator=0 only valid in Down
		// state before learning remote discriminator.
		assertNoPackets(ctx, t,
			gobfdPkts+" && bfd.your_discriminator == 0x00000000 && bfd.sta != 0x01 && bfd.sta != 0x00",
			"YourDiscriminator=0 only valid in Down/AdminDown state")
	})

	t.Run("RFC5880_6.8.1_DiscrUniqueness", func(t *testing.T) {
		// Each session must use a unique local discriminator.
		// Read all packets and filter by state in Go to avoid
		// tshark pcapng read-while-write race conditions.
		findUpDiscr := func(peerIP string) (string, error) {
			rows, err := tsharkFields(ctx,
				"bfd && ip.src == "+gobfdIP+" && ip.dst == "+peerIP,
				[]string{"bfd.sta", "bfd.my_discriminator"}, 0)
			if err != nil {
				return "", err
			}
			for _, row := range rows {
				if len(row) >= 2 {
					state, _ := parseHexOrDec(row[0])
					if state == 3 {
						return row[1], nil
					}
				}
			}
			return "", fmt.Errorf("no Up packets to %s", peerIP)
		}

		frrDiscr, err := findUpDiscr(frrIP)
		if err != nil {
			t.Fatalf("FRR: %v", err)
		}
		birdDiscr, err := findUpDiscr(bird3IP)
		if err != nil {
			t.Fatalf("BIRD3: %v", err)
		}
		holoDiscr, err := findUpDiscr(holoIP)
		if err != nil {
			t.Fatalf("Holo: %v", err)
		}
		thoroDiscr, err := findUpDiscr(thoroIP)
		if err != nil {
			t.Fatalf("Thoro: %v", err)
		}

		discrs := map[string]string{
			"FRR":   frrDiscr,
			"BIRD3": birdDiscr,
			"Holo":  holoDiscr,
			"Thoro": thoroDiscr,
		}
		seen := make(map[string]string)
		for peer, d := range discrs {
			if other, ok := seen[d]; ok {
				t.Errorf("%s and %s sessions use same discriminator: %s", other, peer, d)
			}
			seen[d] = peer
		}
	})

	// -----------------------------------------------------------------
	// Group C: Slow TX Rate (RFC 5880 §6.8.3)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.8.3_SlowTxWhenNotUp", func(t *testing.T) {
		// RFC 5880 §6.8.3: "When bfd.SessionState is not Up, the system
		// MUST set bfd.DesiredMinTxInterval to not less than one second."
		assertNoPackets(ctx, t,
			gobfdPkts+" && (bfd.sta == 0x01 || bfd.sta == 0x02) && bfd.desired_min_tx_interval < 1000000",
			"DesiredMinTxInterval must be >= 1s (1000000us) when not Up")
	})

	t.Run("RFC5880_6.8.3_FastTxOnceUp", func(t *testing.T) {
		// Once Up, configured DesiredMinTxInterval (300ms) is used.
		// Read without bfd.sta filter and find first Up packet in Go
		// to avoid tshark pcapng read-while-write race conditions.
		rows, err := tsharkFields(ctx,
			gobfdPkts,
			[]string{"bfd.sta", "bfd.desired_min_tx_interval"}, 0)
		if err != nil {
			t.Fatalf("tshark: %v", err)
		}
		var interval uint64
		found := false
		for _, row := range rows {
			if len(row) >= 2 {
				state, _ := parseHexOrDec(row[0])
				if state == 3 {
					interval, err = parseHexOrDec(row[1])
					if err != nil {
						t.Fatalf("parse interval: %v", err)
					}
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatal("no Up packets from GoBFD")
		}
		if interval != 300000 {
			t.Errorf("DesiredMinTxInterval in Up state = %d, want 300000", interval)
		}
	})

	// -----------------------------------------------------------------
	// Group D: Diagnostic code — initial state (RFC 5880 §6.8.1)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.8.1_DiagZeroInitial", func(t *testing.T) {
		// RFC 5880 §6.8.1: "bfd.LocalDiag MUST be initialized to 0."
		// The first Down packets from gobfd should have Diag=0 (No Diagnostic).
		rows, err := tsharkFields(ctx,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+frrIP+" && bfd.sta == 0x01",
			[]string{"bfd.diag"}, 3)
		if err != nil || len(rows) == 0 {
			t.Skipf("no initial Down packets captured (session may have started before capture)")
		}
		diag, err := parseHexOrDec(rows[0][0])
		if err != nil {
			t.Fatalf("parse diag: %v", err)
		}
		if diag != 0 {
			t.Errorf("first Down packet diag = %d, want 0 (No Diagnostic)", diag)
		}
	})

	// -----------------------------------------------------------------
	// Group F: Poll/Final during handshake (RFC 5880 §6.5)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.5_PollFinalHandshake", func(t *testing.T) {
		// During the handshake, Poll/Final exchange is expected.
		// Verify at least some P or F bits were set.
		pollCount, err := tsharkCount(ctx, gobfdPkts+" && bfd.flags.p == 1")
		if err != nil {
			t.Fatalf("tshark poll query: %v", err)
		}
		finalCount, err := tsharkCount(ctx, gobfdPkts+" && bfd.flags.f == 1")
		if err != nil {
			t.Fatalf("tshark final query: %v", err)
		}
		t.Logf("GoBFD Poll packets: %d, Final packets: %d", pollCount, finalCount)
		// GoBFD should have sent at least some Final responses
		// (FRR/BIRD3 send Poll during handshake).
		if finalCount == 0 {
			t.Error("GoBFD never sent Final (F=1) — expected during handshake P/F exchange")
		}
	})

	// -----------------------------------------------------------------
	// Group A (continued): Jitter compliance (RFC 5880 §6.8.7)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.8.7_JitterCompliance", func(t *testing.T) {
		// RFC 5880 §6.8.7: "the interval MUST be reduced by a random
		// value of 0 to 25%." So actual TX interval is 75-100% of the
		// negotiated interval. For 300ms: [225ms, 300ms].
		// Verify jitter compliance for all 4 peers.
		type peer struct {
			name string
			ip   string
		}
		peers := []peer{
			{"FRR", frrIP},
			{"BIRD3", bird3IP},
			{"Holo", holoIP},
			{"Thoro", thoroIP},
		}
		for _, p := range peers {
			t.Run(p.name, func(t *testing.T) {
				output, err := tsharkFieldStream(ctx,
					"bfd && ip.src == "+gobfdIP+" && ip.dst == "+p.ip,
					[]string{"frame.time_epoch", "bfd.sta", "bfd.flags.p", "bfd.flags.f"}, 0)
				if err != nil {
					t.Fatalf("read jitter packet fields for %s: %v", p.name, err)
				}
				report, err := bfdjitter.Evaluate(strings.NewReader(output))
				if err != nil {
					t.Fatalf("analyze jitter packet fields for %s: %v", p.name, err)
				}

				switch report.Status {
				case bfdjitter.StatusSkip:
					t.Skipf("%s jitter analysis skipped: %s (Up packets=%d, samples=%d)",
						p.name, report.Reason, report.UpPackets, report.Samples)
				case bfdjitter.StatusFail:
					t.Errorf("%s jitter %s: min=%.3fs max=%.3fs samples=%d",
						p.name, report.Reason, report.MinDelta, report.MaxDelta, report.Samples)
				case bfdjitter.StatusPass:
					t.Logf("%s inter-packet timing: min=%.3fs max=%.3fs samples=%d",
						p.name, report.MinDelta, report.MaxDelta, report.Samples)
				default:
					t.Fatalf("%s jitter analyzer returned unknown status %q", p.name, report.Status)
				}
			})
		}
	})

	// -----------------------------------------------------------------
	// Group E: Session independence (RFC 5880 §6.8.1)
	// Stop FRR, verify BIRD3 unaffected, measure detection time,
	// then verify recovery.
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.8.1_SessionIndependence", func(t *testing.T) {
		// Stop FRR to trigger detection timeout on gobfd→FRR session.
		output, err := projectContainerCommand(ctx, "frr-interop", "stop")
		if err != nil {
			t.Fatalf("stop FRR: %v: %s", err, output)
		}
		t.Cleanup(func() {
			// Always restart FRR.
			projectContainerCommand(ctx, "frr-interop", "start") //nolint:errcheck // Cleanup is best effort.
		})

		// Wait for detection time + margin.
		time.Sleep(3 * time.Second)

		// BIRD3 session must remain Up.
		up, err := bird3BFDSessionUp(ctx)
		if err != nil {
			t.Fatalf("check BIRD3: %v", err)
		}
		if !up {
			t.Error("BIRD3 session went Down when only FRR was stopped — sessions are not independent")
		}

		// Holo session must also remain Up.
		holoUp, err := holoSessionUp(ctx)
		if err != nil {
			t.Fatalf("check Holo: %v", err)
		}
		if !holoUp {
			t.Error("Holo session went Down when only FRR was stopped — sessions are not independent")
		}

		// Thoro/bfd session must also remain Up.
		thoroUp, err := thoroSessionUp(ctx)
		if err != nil {
			t.Fatalf("check Thoro: %v", err)
		}
		if !thoroUp {
			t.Error("Thoro/bfd session went Down when only FRR was stopped — sessions are not independent")
		}
	})

	// Wait for FRR to restart from cleanup above.
	time.Sleep(5 * time.Second)

	t.Run("RFC5880_6.8.4_DiagTimeExpired", func(t *testing.T) {
		// RFC 5880 §6.8.4: After detection timeout, LocalDiag = 1
		// (Control Detection Time Expired).
		assertHasPackets(ctx, t,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+frrIP+" && bfd.sta == 0x01 && bfd.diag == 0x01",
			"GoBFD must send Down with Diag=1 after detection timeout")
	})

	t.Run("RFC5880_6.8.4_DetectionPrecision", func(t *testing.T) {
		// Measure gap between last FRR packet BEFORE the first Down(diag=1)
		// and that Down packet. The capture may contain multiple stop/restart
		// cycles, so we must correlate timestamps properly.
		firstDown, err := tsharkFields(ctx,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+frrIP+" && bfd.sta == 0x01 && bfd.diag == 0x01",
			[]string{"frame.time_epoch"}, 1)
		if err != nil || len(firstDown) == 0 {
			t.Skipf("no Down(diag=1) packets: %v", err)
		}
		downTime, err := strconv.ParseFloat(strings.TrimSpace(firstDown[0][0]), 64)
		if err != nil {
			t.Fatalf("parse down epoch: %v", err)
		}

		// Get all FRR packets and find the last one before the Down packet.
		allFRR, err := tsharkFields(ctx,
			"bfd && ip.src == "+frrIP+" && ip.dst == "+gobfdIP,
			[]string{"frame.time_epoch"}, 0)
		if err != nil || len(allFRR) == 0 {
			t.Skipf("no FRR packets: %v", err)
		}

		var lastFRRTime float64
		found := false
		for _, row := range allFRR {
			ts, err := strconv.ParseFloat(strings.TrimSpace(row[0]), 64)
			if err != nil {
				continue
			}
			if ts < downTime {
				lastFRRTime = ts
				found = true
			}
		}
		if !found {
			t.Skipf("no FRR packets before the Down(diag=1) packet")
		}

		gap := downTime - lastFRRTime
		t.Logf("detection gap: last FRR packet → first Down = %.3f seconds", gap)

		// Expected: 0.9s (3*300ms). Allow up to 3s for container overhead.
		if gap > 3.0 {
			t.Errorf("detection took %.3fs, want < 3.0s (3*300ms + margin)", gap)
		}
	})

	// Wait for FRR session to re-establish after the stop/start.
	waitFRRUp(ctx, t, 60*time.Second)

	t.Run("RFC5880_6.2_SessionRecovery", func(t *testing.T) {
		// After FRR restart, session must recover to Up through the
		// full handshake (Down → Init → Up).
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			t.Fatalf("FRR peer status: %v", err)
		}
		if status != "up" {
			t.Errorf("FRR session did not recover to Up after restart: status=%s", status)
		}

		// Verify BIRD3 is still Up (was never affected).
		up, err := bird3BFDSessionUp(ctx)
		if err != nil {
			t.Fatalf("check BIRD3: %v", err)
		}
		if !up {
			t.Error("BIRD3 session not Up after FRR recovery cycle")
		}

		// Verify Holo is still Up.
		holoUp, err := holoSessionUp(ctx)
		if err != nil {
			t.Fatalf("check Holo: %v", err)
		}
		if !holoUp {
			t.Error("Holo session not Up after FRR recovery cycle")
		}

		// Verify Thoro/bfd is still Up.
		thoroUp, err := thoroSessionUp(ctx)
		if err != nil {
			t.Fatalf("check Thoro: %v", err)
		}
		if !thoroUp {
			t.Error("Thoro/bfd session not Up after FRR recovery cycle")
		}
	})

	// -----------------------------------------------------------------
	// Group G: AdminDown from FRR (RFC 5880 §6.8.6)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.8.6_FRRAdminDown", func(t *testing.T) {
		// FRR sends AdminDown (state=0, diag=7) via vtysh "shutdown".
		// GoBFD must transition to Down with Diag=3 (Neighbor Signaled).
		_, err := frrVtyshConfig(ctx,
			"configure terminal", "bfd", "peer "+gobfdIP, "shutdown")
		if err != nil {
			t.Fatalf("FRR shutdown: %v", err)
		}

		// Wait for the AdminDown to propagate.
		time.Sleep(3 * time.Second)

		// Verify FRR sent AdminDown packets.
		assertHasPackets(ctx, t,
			"bfd && ip.src == "+frrIP+" && bfd.sta == 0x00",
			"FRR must send AdminDown (state=0) packets after shutdown")

		// Verify GoBFD transitioned to Down with Diag=3 (Neighbor Signaled).
		assertHasPackets(ctx, t,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+frrIP+" && bfd.sta == 0x01 && bfd.diag == 0x03",
			"GoBFD must set Diag=3 (Neighbor Signaled) when receiving AdminDown")
	})

	t.Run("RFC5880_6.8.16_FRRAdminDownRecovery", func(t *testing.T) {
		// Clear FRR AdminDown: session must re-establish.
		_, err := frrVtyshConfig(ctx,
			"configure terminal", "bfd", "peer "+gobfdIP, "no shutdown")
		if err != nil {
			t.Fatalf("FRR no shutdown: %v", err)
		}

		// Wait for full handshake recovery.
		waitFRRUp(ctx, t, 30*time.Second)

		t.Log("FRR session recovered after AdminDown cleared")
	})

	// -----------------------------------------------------------------
	// Group F: Poll/Final from FRR parameter change (RFC 5880 §6.5)
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.5_PollFinalParameterChange", func(t *testing.T) {
		// RFC 5880 §6.5: "If DesiredMinTxInterval is changed or
		// RequiredMinRxInterval is changed, a Poll Sequence MUST be
		// initiated."
		//
		// Change FRR's transmit-interval to trigger a Poll from FRR.
		// GoBFD must respond with Final.

		pollBefore, err := tsharkCount(ctx,
			"bfd && ip.src == "+frrIP+" && bfd.flags.p == 1")
		if err != nil {
			t.Fatalf("count polls before: %v", err)
		}
		finalBefore, err := tsharkCount(ctx,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+frrIP+" && bfd.flags.f == 1")
		if err != nil {
			t.Fatalf("count finals before: %v", err)
		}

		// Change FRR interval to trigger Poll Sequence.
		_, err = frrVtyshConfig(ctx,
			"configure terminal", "bfd", "peer "+gobfdIP, "transmit-interval 200")
		if err != nil {
			t.Fatalf("FRR interval change: %v", err)
		}

		// Wait for P/F exchange.
		time.Sleep(5 * time.Second)

		pollAfter, err := tsharkCount(ctx,
			"bfd && ip.src == "+frrIP+" && bfd.flags.p == 1")
		if err != nil {
			t.Fatalf("count polls after: %v", err)
		}
		finalAfter, err := tsharkCount(ctx,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+frrIP+" && bfd.flags.f == 1")
		if err != nil {
			t.Fatalf("count finals after: %v", err)
		}

		if pollAfter <= pollBefore {
			t.Errorf("FRR did not send Poll after interval change (before=%d, after=%d)",
				pollBefore, pollAfter)
		}
		if finalAfter <= finalBefore {
			t.Errorf("GoBFD did not send Final in response to FRR Poll (before=%d, after=%d)",
				finalBefore, finalAfter)
		}

		t.Logf("Poll/Final exchange: FRR polls %d→%d, GoBFD finals %d→%d",
			pollBefore, pollAfter, finalBefore, finalAfter)

		// Restore FRR interval.
		_, err = frrVtyshConfig(ctx,
			"configure terminal", "bfd", "peer "+gobfdIP, "transmit-interval 300")
		if err != nil {
			t.Logf("warning: failed to restore FRR interval: %v", err)
		}
		time.Sleep(3 * time.Second)
	})
}

// TestHoloFailureRecoveryLifecycle verifies fresh RFC 5880 failure and recovery
// evidence without accepting state or packets captured before the failure.
// This test is intentionally serial because it mutates the shared Holo service.
func TestHoloFailureRecoveryLifecycle(t *testing.T) {
	now := time.Now()
	testDeadline, hasTestDeadline := t.Deadline()
	schedule, err := planHoloLifecycle(now, testDeadline, hasTestDeadline)
	if err != nil {
		t.Skipf("Holo lifecycle needs a targeted 300s test invocation before mutation: %v", err)
	}

	lifecycleCtx, cancelLifecycle := context.WithDeadline(t.Context(), schedule.lifecycleDeadline)
	defer cancelLifecycle()
	t.Logf(
		"Holo lifecycle budgets: lifecycle=%v cleanup=%v test-margin=%v",
		holoLifecycleBudget,
		holoCleanupBudget,
		holoTestDeadlineMargin,
	)
	waitHoloUp(lifecycleCtx, t, 30*time.Second)

	preStopCtx, cancelPreStop := context.WithTimeout(lifecycleCtx, 5*time.Second)
	preStopBaseline, err := lastHoloFrame(preStopCtx)
	cancelPreStop()
	if err != nil {
		t.Fatalf("record pre-stop Holo packet baseline: %v", err)
	}
	t.Logf("pre-stop Holo packet baseline: frame.number=%d", preStopBaseline)

	mutated := false
	recoveryReady := false
	t.Cleanup(func() {
		if !mutated || recoveryReady {
			return
		}

		cleanupCtx, cancel := boundedDetachedContext(
			t.Context(),
			holoCleanupBudget,
			schedule.cleanupDeadline,
			true,
		)
		defer cancel()
		if recoveryErr := startAndConfigureHolo(cleanupCtx); recoveryErr != nil {
			t.Logf("best-effort Holo/config recovery failed: %v", recoveryErr)
			return
		}
		if _, waitErr := waitForSessionState(
			cleanupCtx,
			holoIP,
			15*time.Second,
			func(state sessionState) bool {
				return state.LocalState == "Up" && state.RemoteState == "Up"
			},
		); waitErr != nil {
			t.Logf("best-effort Holo session recovery did not reach Up/Up: %v", waitErr)
		}
	})
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 100)
		}
	})

	mutated = true
	stopCtx, cancelStop := context.WithTimeout(lifecycleCtx, holoStopTimeout)
	output, err := stopHoloContainer(stopCtx)
	cancelStop()
	if err != nil {
		t.Fatalf("stop only Holo service: %v: %s", err, strings.TrimSpace(output))
	}

	down, err := waitForSessionState(
		lifecycleCtx,
		holoIP,
		15*time.Second,
		func(state sessionState) bool {
			return state.LocalState == "Down" && state.LocalDiagnostic == "ControlTimeExpired"
		},
	)
	if err != nil {
		t.Fatalf("wait for current Holo timeout state: %v", err)
	}
	t.Logf(
		"current Holo session after stop: local=%s remote=%s diagnostic=%s",
		down.LocalState,
		down.RemoteState,
		down.LocalDiagnostic,
	)

	downFrame, err := waitForHoloDownFrame(lifecycleCtx, preStopBaseline, 10*time.Second)
	if err != nil {
		t.Fatalf("prove post-stop GoBFD Down packet boundary: %v", err)
	}
	t.Logf("proven post-Down packet baseline: frame.number=%d", downFrame)

	if recoveryErr := startAndConfigureHolo(lifecycleCtx); recoveryErr != nil {
		t.Fatalf("restart and configure Holo: %v", recoveryErr)
	}

	up, err := waitForSessionState(
		lifecycleCtx,
		holoIP,
		25*time.Second,
		func(state sessionState) bool {
			return state.LocalState == "Up" && state.RemoteState == "Up"
		},
	)
	if err != nil {
		t.Fatalf("wait for current Holo recovery state: %v", err)
	}
	recoveryReady = true
	t.Logf(
		"current Holo session after recovery: local=%s remote=%s diagnostic=%s",
		up.LocalState,
		up.RemoteState,
		up.LocalDiagnostic,
	)

	requireNewHoloUpPacket(lifecycleCtx, t, downFrame, 10*time.Second)
}

// =========================================================================
// Test 4: Detection timeout (existing, enhanced)
// =========================================================================

// TestFRRDetectionTimeout verifies that GoBFD detects FRR peer failure
// when FRR is stopped.
func TestFRRDetectionTimeout(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 100)
		}
	})
	ctx := t.Context()

	waitFRRUp(ctx, t, 60*time.Second)

	output, err := projectContainerCommand(ctx, "frr-interop", "stop")
	if err != nil {
		t.Fatalf("stop FRR: %v: %s", err, output)
	}

	time.Sleep(5 * time.Second)

	logs, err := projectContainerLogs(ctx, "gobfd-interop")
	if err != nil {
		t.Fatalf("get gobfd logs: %v", err)
	}

	if !strings.Contains(logs, "session state changed") || !strings.Contains(logs, "new_state=Down") {
		t.Error("GoBFD did not log session Down transition after FRR stop")
		t.Logf("gobfd logs (tail):\n%s", lastNLines(logs, 30))
	}

	output, err = projectContainerCommand(ctx, "frr-interop", "start")
	if err != nil {
		t.Fatalf("restart FRR: %v: %s", err, output)
	}
}

// =========================================================================
// Test 5: Graceful shutdown with AdminDown (existing, enhanced)
// BFD Invalid-Vector Robustness
// =========================================================================

// TestBFDInvalidVectors runs the repository-owned Go generator that sends the
// preserved 1055-packet invalid-vector corpus and verifies GoBFD survives.
// Tests RFC 5880 Section 6.8.6 validation robustness.
//
// Uses podman build + podman run directly (NOT podman compose run) because
// podman compose's "run" subcommand tears down and recreates the entire
// compose stack, destroying frr-interop and other containers.
func TestBFDInvalidVectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	projectName, err := interopProjectName()
	if err != nil {
		t.Fatalf("resolve interop Compose project: %v", err)
	}

	// Build directly from the repository root so the Go module and internal BFD
	// codec are available to the bounded multi-stage build context.
	buildOut, err := exec.CommandContext(ctx,
		"podman", "build",
		"-t", bfdFuzzImage,
		"-f", "scapy/Containerfile",
		"../..",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("podman build BFD invalid-vector generator: %v\n%s", err, buildOut)
	}

	// Run on the existing compose network without disturbing other services.
	runOut, err := exec.CommandContext(ctx,
		"podman", "run", "--rm",
		"--name", "scapy-interop",
		"--label", "com.docker.compose.project="+projectName,
		"--network", interopNetworkName(projectName),
		"--ip", fuzzIP,
		"-e", "GOBFD_IP="+gobfdIP,
		bfdFuzzImage,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("BFD invalid-vector generator failed: %v\n%s", err, runOut)
	}

	t.Logf("BFD invalid-vector generator output:\n%s", string(runOut))

	// Verify gobfd is still running after fuzzing.
	out, err := projectContainerInspect(ctx, "gobfd-interop", "{{.State.Running}}")
	if err != nil || strings.TrimSpace(out) != "true" {
		t.Fatal("gobfd crashed after BFD invalid-vector corpus")
	}

	t.Log("GoBFD survived all repository BFD invalid vectors")
}

// =========================================================================
// Must be LAST — stops gobfd container.
// =========================================================================

// TestGracefulShutdown verifies that when GoBFD is stopped gracefully
// (SIGTERM), it sends AdminDown (state=0, diag=7) to all peers.
func TestGracefulShutdown(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 100)
		}
	})
	ctx := t.Context()

	waitFRRUp(ctx, t, 60*time.Second)

	// Record AdminDown packet count before shutdown.
	adminDownBefore, _ := tsharkCount(ctx,
		"bfd && ip.src == "+gobfdIP+" && bfd.sta == 0x00 && bfd.diag == 0x07")

	output, err := projectContainerCommand(ctx, "gobfd-interop", "stop")
	if err != nil {
		t.Fatalf("stop gobfd: %v: %s", err, output)
	}

	time.Sleep(5 * time.Second)

	// Verify AdminDown packets were sent (diag=7, state=0).
	adminDownAfter, err := tsharkCount(ctx,
		"bfd && ip.src == "+gobfdIP+" && bfd.sta == 0x00 && bfd.diag == 0x07")
	switch {
	case err != nil:
		t.Logf("tshark query for AdminDown packets: %v", err)
	case adminDownAfter <= adminDownBefore:
		t.Error("GoBFD did not send AdminDown (state=0, diag=7) packets on SIGTERM")
	default:
		t.Logf("GoBFD sent %d AdminDown packets on graceful shutdown",
			adminDownAfter-adminDownBefore)
	}

	// RFC 5880 §6.8.16: AdminDown must be sent to ALL peers on shutdown.
	peers := []struct {
		name string
		ip   string
	}{
		{"FRR", frrIP},
		{"BIRD3", bird3IP},
		{"Holo", holoIP},
	}
	if crashed, statusErr := thoroUnsupportedPollSequenceCrash(ctx); statusErr != nil {
		t.Logf("Thoro/bfd status lookup failed: %v", statusErr)
	} else if crashed {
		t.Log("Thoro/bfd AdminDown verification skipped: upstream peer panicked on " +
			"unimplemented RFC 5880 poll-sequence interval update")
	} else {
		peers = append(peers, struct {
			name string
			ip   string
		}{"Thoro", thoroIP})
	}
	for _, peer := range peers {
		count, countErr := tsharkCount(ctx,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+peer.ip+
				" && bfd.sta == 0x00 && bfd.diag == 0x07")
		if countErr != nil {
			t.Logf("tshark query for %s AdminDown: %v", peer.name, countErr)
			continue
		}
		if count == 0 {
			t.Errorf("GoBFD did not send AdminDown to %s (%s)", peer.name, peer.ip)
		} else {
			t.Logf("GoBFD sent %d AdminDown packets to %s", count, peer.name)
		}
	}

	// Verify FRR sees session down.
	status, err := frrBFDPeerStatus(ctx)
	if err != nil {
		t.Fatalf("FRR peer status lookup failed: %v", err)
	}
	if status != "down" {
		t.Errorf("FRR BFD peer status = %q after gobfd shutdown, want down", status)
	}
}
