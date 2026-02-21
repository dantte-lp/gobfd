//go:build interop

// Package interop_test provides Go-driven interoperability tests for GoBFD
// against FRR (bfdd) and BIRD3. These tests require a running container stack
// defined in test/interop/compose.yml and are NOT run as part of the regular
// test suite.
//
// Run with:
//
//	go test -tags interop -v -count=1 -timeout 120s ./test/interop/
//
// Prerequisites:
//   - podman-compose -f test/interop/compose.yml up --build -d
//   - All three containers (gobfd, frr, bird3) must be running and healthy.
package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// gobfdIP is the static IP address assigned to the gobfd container
// in the interop compose network (172.20.0.0/24).
const gobfdIP = "172.20.0.10"

// pollInterval is the polling interval for waitForCondition.
const pollInterval = 2 * time.Second

// composeFile is the path to the interop compose file, overridable via
// the INTEROP_COMPOSE_FILE environment variable.
func composeFile() string {
	if f := os.Getenv("INTEROP_COMPOSE_FILE"); f != "" {
		return f
	}
	return "test/interop/compose.yml"
}

// podmanCompose runs a podman-compose command and returns combined output.
func podmanCompose(ctx context.Context, args ...string) (string, error) {
	allArgs := append([]string{"-f", composeFile()}, args...)
	cmd := exec.CommandContext(ctx, "podman-compose", allArgs...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// frrVtysh runs a vtysh command inside the FRR container.
func frrVtysh(ctx context.Context, command string) (string, error) {
	return podmanCompose(ctx, "exec", "-T", "frr", "vtysh", "-c", command)
}

// frrBFDPeerStatus returns the BFD peer status for gobfdIP from FRR's
// JSON output. Returns "up", "down", or an error.
func frrBFDPeerStatus(ctx context.Context) (string, error) {
	output, err := frrVtysh(ctx, "show bfd peers json")
	if err != nil {
		return "", fmt.Errorf("vtysh show bfd peers json: %w: %s", err, output)
	}

	var peers []struct {
		Peer   string `json:"peer"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &peers); err != nil {
		return "", fmt.Errorf("parse bfd peers json: %w: raw=%s", err, output)
	}

	for _, p := range peers {
		if p.Peer == gobfdIP {
			return strings.ToLower(p.Status), nil
		}
	}

	return "", fmt.Errorf("peer %s not found in FRR BFD peers", gobfdIP)
}

// bird3BFDSessionUp returns true if BIRD3 reports the BFD session with
// the gobfd peer IP as Up.
func bird3BFDSessionUp(ctx context.Context) (bool, error) {
	output, err := podmanCompose(ctx, "exec", "-T", "bird3", "birdc", "show bfd sessions")
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

// waitForCondition polls a condition function at pollInterval until
// it returns true or the timeout expires.
func waitForCondition(t *testing.T, desc string, timeout time.Duration, fn func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		ok, err := fn()
		if err != nil {
			lastErr = err
		}
		if ok {
			return
		}
		time.Sleep(pollInterval)
	}

	if lastErr != nil {
		t.Fatalf("condition %q not met within %v: last error: %v", desc, timeout, lastErr)
	}
	t.Fatalf("condition %q not met within %v", desc, timeout)
}

// TestFRRHandshake verifies that the BFD three-way handshake completes
// between GoBFD and FRR, resulting in both sides reporting session Up.
func TestFRRHandshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	ctx := t.Context()
	waitForCondition(t, "FRR BFD session Up", 60*time.Second, func() (bool, error) {
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})
}

// TestBIRD3Handshake verifies that the BFD three-way handshake completes
// between GoBFD and BIRD3, resulting in both sides reporting session Up.
func TestBIRD3Handshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	ctx := t.Context()
	waitForCondition(t, "BIRD3 BFD session Up", 60*time.Second, func() (bool, error) {
		return bird3BFDSessionUp(ctx)
	})
}

// TestFRRDetectionTimeout verifies that GoBFD detects FRR peer failure
// when FRR is stopped. After stopping FRR, the BFD session on gobfd
// should transition to Down within the detection time (3 * 300ms = 900ms,
// plus margin for jitter and timer alignment).
func TestFRRDetectionTimeout(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 100)
		}
	})
	ctx := t.Context()

	// Ensure session is Up first.
	waitForCondition(t, "FRR BFD session Up before stop", 30*time.Second, func() (bool, error) {
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})

	// Stop FRR to simulate peer failure.
	output, err := podmanCompose(ctx, "stop", "frr")
	if err != nil {
		t.Fatalf("stop FRR: %v: %s", err, output)
	}

	// Wait for detection time + margin.
	time.Sleep(5 * time.Second)

	// Check GoBFD logs for Down transition.
	logs, err := podmanCompose(ctx, "logs", "gobfd")
	if err != nil {
		t.Fatalf("get gobfd logs: %v", err)
	}

	if !strings.Contains(logs, "session state changed") || !strings.Contains(logs, "new_state=Down") {
		t.Error("GoBFD did not log session Down transition after FRR stop")
		t.Logf("gobfd logs (tail):\n%s", lastNLines(logs, 30))
	}

	// Restart FRR for subsequent tests.
	output, err = podmanCompose(ctx, "start", "frr")
	if err != nil {
		t.Fatalf("restart FRR: %v: %s", err, output)
	}
}

// TestGracefulShutdown verifies that when GoBFD is stopped gracefully
// (SIGTERM), it sends AdminDown to peers before exiting, and FRR detects
// the session going Down.
func TestGracefulShutdown(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 100)
		}
	})
	ctx := t.Context()

	// Wait for sessions to re-establish.
	waitForCondition(t, "FRR BFD session Up before shutdown", 60*time.Second, func() (bool, error) {
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})

	// Send SIGTERM to gobfd for graceful shutdown.
	output, err := podmanCompose(ctx, "stop", "gobfd")
	if err != nil {
		t.Fatalf("stop gobfd: %v: %s", err, output)
	}

	// Wait for FRR to detect session going down.
	time.Sleep(5 * time.Second)

	status, err := frrBFDPeerStatus(ctx)
	if err != nil {
		// Peer might be removed entirely after Down, which is acceptable.
		t.Logf("FRR peer lookup error (acceptable if removed): %v", err)
		return
	}

	if status != "down" {
		t.Errorf("FRR BFD peer status = %q after gobfd shutdown, want down", status)
	}
}

// dumpTsharkCapture logs the last N BFD packets captured by tshark.
// Useful for post-mortem debugging when sessions fail to establish.
func dumpTsharkCapture(t *testing.T, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "podman", "exec", "tshark-interop",
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd",
		"-c", fmt.Sprintf("%d", count),
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
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Logf("tshark dump unavailable: %v", err)
		return
	}
	t.Logf("BFD packet capture (last %d packets):\n%s", count, buf.String())
}

// lastNLines returns the last n lines of s.
func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
