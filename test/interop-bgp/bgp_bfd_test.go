//go:build interop_bgp

// Package interop_bgp_test provides BGP+BFD full-cycle interoperability tests
// for GoBFD integrated with GoBGP.
//
// Tests verify the end-to-end chain: BFD failure detection -> GoBGP DisablePeer
// -> BGP session teardown -> route withdrawal, and the reverse on recovery.
//
// Three scenarios:
//  1. GoBFD + GoBGP  <->  FRR (bgpd + bfdd)
//  2. GoBFD + GoBGP  <->  BIRD3 (BGP + bfd on)
//  3. GoBFD + GoBGP  <->  GoBFD + ExaBGP (BFD sidecar)
//
// Container management uses the Podman REST API via unix socket
// (/run/podman/podman.sock), so no podman CLI binary is required.
//
// Run with:
//
//	go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/
//
// Prerequisites:
//   - podman-compose -f test/interop-bgp/compose.yml up --build -d
//   - All containers must be running.
package interop_bgp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Constants
// =========================================================================

const (
	gobfdBGPIP    = "172.21.0.10" // gobfd-bgp + gobgp (shared netns)
	frrBGPIP      = "172.21.0.20" // FRR (bgpd + bfdd)
	bird3BGPIP    = "172.21.0.30" // BIRD3 (BGP + BFD)
	gobfdExaBGPIP = "172.21.0.40" // gobfd-exabgp + exabgp (shared netns)

	frrRoute    = "10.20.0.0/24" // Announced by FRR
	bird3Route  = "10.30.0.0/24" // Announced by BIRD3
	exabgpRoute = "10.40.0.0/24" // Announced by ExaBGP

	// Container names (as set by compose container_name).
	gobgpContainer       = "gobgp-interop"
	gobfdBGPContainer    = "gobfd-bgp-interop"
	frrContainer         = "frr-bgp-interop"
	bird3Container       = "bird3-bgp-interop"
	gobfdExaBGPContainer = "gobfd-exabgp-interop"

	pollInterval = 2 * time.Second

	// Timeouts for waiting on BGP/BFD convergence.
	bgpEstablishTimeout = 90 * time.Second
	bfdUpTimeout        = 30 * time.Second
	routeTimeout        = 30 * time.Second
	failureDetectWait   = 10 * time.Second
)

// =========================================================================
// Infrastructure helpers
// =========================================================================

// gobgpCmd runs the gobgp CLI tool inside the gobgp container.
func gobgpCmd(ctx context.Context, args ...string) (string, error) {
	return containerExec(ctx, gobgpContainer, append([]string{"gobgp"}, args...)...)
}

// GoBGP v3 session state enum values (PeerState_SessionState protobuf).
const (
	bgpStateUnspecified = 0
	bgpStateIdle        = 1
	bgpStateConnect     = 2
	bgpStateActive      = 3
	bgpStateOpenSent    = 4
	bgpStateOpenConfirm = 5
	bgpStateEstablished = 6
)

// bgpSessionStateName maps GoBGP v3 protobuf session_state numbers to names.
var bgpSessionStateName = map[int]string{
	bgpStateUnspecified: "unspecified",
	bgpStateIdle:        "idle",
	bgpStateConnect:     "connect",
	bgpStateActive:      "active",
	bgpStateOpenSent:    "opensent",
	bgpStateOpenConfirm: "openconfirm",
	bgpStateEstablished: "established",
}

// gobgpNeighborState returns the BGP session state for a specific peer.
// Returns lowercase state string: "established", "idle", "active", "opensent", etc.
func gobgpNeighborState(ctx context.Context, peerIP string) (string, error) {
	output, err := gobgpCmd(ctx, "neighbor", "-j")
	if err != nil {
		return "", err
	}

	// GoBGP v3 uses protobuf-style JSON with underscored field names
	// and numeric enums for session_state.
	var neighbors []struct {
		State struct {
			NeighborAddress string `json:"neighbor_address"`
			SessionState    int    `json:"session_state"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(output), &neighbors); err != nil {
		return "", fmt.Errorf("parse gobgp neighbor json: %w: raw=%s", err, output)
	}

	for _, n := range neighbors {
		if n.State.NeighborAddress == peerIP {
			name, ok := bgpSessionStateName[n.State.SessionState]
			if !ok {
				return fmt.Sprintf("unknown(%d)", n.State.SessionState), nil
			}
			return name, nil
		}
	}

	return "", fmt.Errorf("peer %s not found in gobgp neighbor list", peerIP)
}

// gobgpRouteExists checks if a prefix exists in the GoBGP global RIB.
func gobgpRouteExists(ctx context.Context, prefix string) (bool, error) {
	output, err := gobgpCmd(ctx, "global", "rib")
	if err != nil {
		// If no routes exist, gobgp returns an error; treat as empty RIB.
		return false, nil //nolint:nilerr // empty RIB is not an error
	}
	return strings.Contains(output, prefix), nil
}

// frrBFDPeerStatus returns the BFD peer status for gobfd-bgp.
func frrBFDPeerStatus(ctx context.Context) (string, error) {
	output, err := containerExec(ctx, frrContainer, "vtysh", "-c", "show bfd peers json")
	if err != nil {
		return "", fmt.Errorf("vtysh show bfd peers json: %w: %s", err, output)
	}

	jsonStr := strings.TrimSpace(output)
	if idx := strings.Index(output, "\n["); idx >= 0 {
		jsonStr = strings.TrimSpace(output[idx+1:])
	} else if !strings.HasPrefix(jsonStr, "[") {
		return "", fmt.Errorf("no JSON array in vtysh output: %s", output)
	}

	var peers []struct {
		Peer   string `json:"peer"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &peers); err != nil {
		return "", fmt.Errorf("parse bfd peers json: %w: raw=%s", err, jsonStr)
	}

	for _, p := range peers {
		if p.Peer == gobfdBGPIP {
			return strings.ToLower(p.Status), nil
		}
	}

	return "", fmt.Errorf("peer %s not found in FRR BFD peers", gobfdBGPIP)
}

// bird3BGPSessionUp checks if the BIRD3 BGP session to GoBGP is established.
func bird3BGPSessionUp(ctx context.Context) (bool, error) {
	output, err := containerExec(ctx, bird3Container, "birdc", "show", "protocols")
	if err != nil {
		return false, fmt.Errorf("birdc show protocols: %w: %s", err, output)
	}

	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, "gobgp") && strings.Contains(line, "Established") {
			return true, nil
		}
	}

	return false, nil
}

// bird3BFDSessionUp checks if the BIRD3 BFD session to gobfd-bgp is Up.
func bird3BFDSessionUp(ctx context.Context) (bool, error) {
	output, err := containerExec(ctx, bird3Container, "birdc", "show", "bfd", "sessions")
	if err != nil {
		return false, fmt.Errorf("birdc show bfd sessions: %w: %s", err, output)
	}

	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, gobfdBGPIP) && strings.Contains(strings.ToLower(line), "up") {
			return true, nil
		}
	}

	return false, nil
}

// =========================================================================
// Wait helpers
// =========================================================================

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

func waitBGPEstablished(t *testing.T, ctx context.Context, peerIP string, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "BGP Established with "+peerIP, timeout, func() (bool, error) {
		state, err := gobgpNeighborState(ctx, peerIP)
		if err != nil {
			return false, err
		}
		return state == "established", nil
	})
}

func waitRouteExists(t *testing.T, ctx context.Context, prefix string, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "route "+prefix+" in RIB", timeout, func() (bool, error) {
		return gobgpRouteExists(ctx, prefix)
	})
}

func waitRouteGone(t *testing.T, ctx context.Context, prefix string, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "route "+prefix+" withdrawn from RIB", timeout, func() (bool, error) {
		exists, err := gobgpRouteExists(ctx, prefix)
		if err != nil {
			return false, err
		}
		return !exists, nil
	})
}

func waitFRRBFDUp(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "FRR BFD session Up", timeout, func() (bool, error) {
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})
}

func waitBIRD3BFDUp(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "BIRD3 BFD session Up", timeout, func() (bool, error) {
		return bird3BFDSessionUp(ctx)
	})
}

// =========================================================================
// Debug helpers
// =========================================================================

func dumpGoBGPState(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if out, err := gobgpCmd(ctx, "neighbor"); err == nil {
		t.Logf("GoBGP neighbors:\n%s", out)
	}
	if out, err := gobgpCmd(ctx, "global", "rib"); err == nil {
		t.Logf("GoBGP RIB:\n%s", out)
	}
}

func dumpGoBFDLogs(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if out, err := containerLogs(ctx, gobfdBGPContainer, 30); err == nil {
		t.Logf("gobfd-bgp logs (tail):\n%s", out)
	}
}

// =========================================================================
// Test: Baseline — all peers up
// =========================================================================

func TestBGPBFD_AllPeersUp(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBGPState(t)
			dumpGoBFDLogs(t)
		}
	})
	ctx := t.Context()

	t.Log("waiting for BGP sessions to establish...")
	waitBGPEstablished(t, ctx, frrBGPIP, bgpEstablishTimeout)
	t.Log("FRR BGP session Established")

	waitBGPEstablished(t, ctx, bird3BGPIP, bgpEstablishTimeout)
	t.Log("BIRD3 BGP session Established")

	waitBGPEstablished(t, ctx, gobfdExaBGPIP, bgpEstablishTimeout)
	t.Log("ExaBGP BGP session Established")

	waitFRRBFDUp(t, ctx, bfdUpTimeout)
	t.Log("FRR BFD session Up")

	waitBIRD3BFDUp(t, ctx, bfdUpTimeout)
	t.Log("BIRD3 BFD session Up")

	waitRouteExists(t, ctx, frrRoute, routeTimeout)
	t.Logf("Route %s received from FRR", frrRoute)

	waitRouteExists(t, ctx, bird3Route, routeTimeout)
	t.Logf("Route %s received from BIRD3", bird3Route)

	waitRouteExists(t, ctx, exabgpRoute, routeTimeout)
	t.Logf("Route %s received from ExaBGP", exabgpRoute)

	t.Log("all three BGP+BFD peerings and routes verified")
}

// =========================================================================
// Test: Scenario 1 — GoBFD + GoBGP <-> FRR (BGP+BFD)
// =========================================================================

func TestBGPBFD_FRR(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBGPState(t)
			dumpGoBFDLogs(t)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerStart(ctx, frrContainer) //nolint:errcheck
	})
	ctx := t.Context()

	// Phase 1: Establish.
	t.Log("Phase 1: verifying BGP + BFD + route baseline with FRR")
	waitBGPEstablished(t, ctx, frrBGPIP, bgpEstablishTimeout)
	waitFRRBFDUp(t, ctx, bfdUpTimeout)
	waitRouteExists(t, ctx, frrRoute, routeTimeout)
	t.Logf("baseline: BGP Established, BFD Up, route %s present", frrRoute)

	// Phase 2: BFD failure -> BGP disabled.
	t.Log("Phase 2: stopping FRR to trigger BFD failure")
	if err := containerStop(ctx, frrContainer); err != nil {
		t.Fatalf("stop frr-bgp: %v", err)
	}

	time.Sleep(failureDetectWait)

	state, err := gobgpNeighborState(ctx, frrBGPIP)
	if err != nil {
		t.Fatalf("check FRR BGP state: %v", err)
	}
	if state == "established" {
		t.Errorf("FRR BGP session still Established after BFD failure, expected disabled")
	} else {
		t.Logf("FRR BGP session state: %s (expected non-established)", state)
	}

	waitRouteGone(t, ctx, frrRoute, routeTimeout)
	t.Logf("route %s withdrawn after BFD failure", frrRoute)

	bird3State, _ := gobgpNeighborState(ctx, bird3BGPIP)
	if bird3State != "established" {
		t.Errorf("BIRD3 BGP session affected by FRR failure: state=%s", bird3State)
	}

	// Phase 3: Recovery.
	t.Log("Phase 3: starting FRR for recovery")
	if err := containerStart(ctx, frrContainer); err != nil {
		t.Fatalf("start frr-bgp: %v", err)
	}

	waitBGPEstablished(t, ctx, frrBGPIP, bgpEstablishTimeout)
	t.Log("FRR BGP session re-established")

	waitFRRBFDUp(t, ctx, bfdUpTimeout)
	t.Log("FRR BFD session Up")

	waitRouteExists(t, ctx, frrRoute, routeTimeout)
	t.Logf("route %s restored after recovery", frrRoute)
}

// =========================================================================
// Test: Scenario 2 — GoBFD + GoBGP <-> BIRD3 (BGP+BFD)
// =========================================================================

func TestBGPBFD_BIRD3(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBGPState(t)
			dumpGoBFDLogs(t)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerStart(ctx, bird3Container) //nolint:errcheck
	})
	ctx := t.Context()

	// Phase 1: Establish.
	t.Log("Phase 1: verifying BGP + BFD + route baseline with BIRD3")
	waitBGPEstablished(t, ctx, bird3BGPIP, bgpEstablishTimeout)
	waitBIRD3BFDUp(t, ctx, bfdUpTimeout)
	waitRouteExists(t, ctx, bird3Route, routeTimeout)
	t.Logf("baseline: BGP Established, BFD Up, route %s present", bird3Route)

	// Phase 2: BFD failure -> BGP disabled.
	t.Log("Phase 2: stopping BIRD3 to trigger BFD failure")
	if err := containerStop(ctx, bird3Container); err != nil {
		t.Fatalf("stop bird3-bgp: %v", err)
	}

	time.Sleep(failureDetectWait)

	state, err := gobgpNeighborState(ctx, bird3BGPIP)
	if err != nil {
		t.Fatalf("check BIRD3 BGP state: %v", err)
	}
	if state == "established" {
		t.Errorf("BIRD3 BGP session still Established after BFD failure, expected disabled")
	} else {
		t.Logf("BIRD3 BGP session state: %s (expected non-established)", state)
	}

	waitRouteGone(t, ctx, bird3Route, routeTimeout)
	t.Logf("route %s withdrawn after BFD failure", bird3Route)

	frrState, _ := gobgpNeighborState(ctx, frrBGPIP)
	if frrState != "established" {
		t.Errorf("FRR BGP session affected by BIRD3 failure: state=%s", frrState)
	}

	// Phase 3: Recovery.
	t.Log("Phase 3: starting BIRD3 for recovery")
	if err := containerStart(ctx, bird3Container); err != nil {
		t.Fatalf("start bird3-bgp: %v", err)
	}

	waitBGPEstablished(t, ctx, bird3BGPIP, bgpEstablishTimeout)
	t.Log("BIRD3 BGP session re-established")

	waitBIRD3BFDUp(t, ctx, bfdUpTimeout)
	t.Log("BIRD3 BFD session Up")

	waitRouteExists(t, ctx, bird3Route, routeTimeout)
	t.Logf("route %s restored after recovery", bird3Route)
}

// =========================================================================
// Test: Scenario 3 — GoBFD + GoBGP <-> GoBFD + ExaBGP
// =========================================================================

// TestBGPBFD_ExaBGP tests the full cycle with ExaBGP + GoBFD sidecar.
// We use container pause/unpause to freeze only the BFD daemon while keeping
// ExaBGP's BGP session alive, proving BFD detects failure before BGP holdtimer.
func TestBGPBFD_ExaBGP(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBGPState(t)
			dumpGoBFDLogs(t)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerUnpause(ctx, gobfdExaBGPContainer) //nolint:errcheck
	})
	ctx := t.Context()

	// Phase 1: Establish.
	t.Log("Phase 1: verifying BGP + BFD + route baseline with ExaBGP")
	waitBGPEstablished(t, ctx, gobfdExaBGPIP, bgpEstablishTimeout)
	waitRouteExists(t, ctx, exabgpRoute, routeTimeout)
	t.Logf("baseline: BGP Established, route %s present", exabgpRoute)

	// Phase 2: BFD failure via pause.
	t.Log("Phase 2: pausing gobfd-exabgp to trigger BFD failure")
	if err := containerPause(ctx, gobfdExaBGPContainer); err != nil {
		t.Fatalf("pause gobfd-exabgp: %v", err)
	}

	time.Sleep(failureDetectWait)

	state, err := gobgpNeighborState(ctx, gobfdExaBGPIP)
	if err != nil {
		t.Fatalf("check ExaBGP BGP state: %v", err)
	}
	if state == "established" {
		t.Errorf("ExaBGP BGP session still Established after BFD failure, expected disabled")
	} else {
		t.Logf("ExaBGP BGP session state: %s (expected non-established)", state)
	}

	waitRouteGone(t, ctx, exabgpRoute, routeTimeout)
	t.Logf("route %s withdrawn after BFD failure", exabgpRoute)

	// Phase 3: Recovery via unpause.
	t.Log("Phase 3: unpausing gobfd-exabgp for recovery")
	if err := containerUnpause(ctx, gobfdExaBGPContainer); err != nil {
		t.Fatalf("unpause gobfd-exabgp: %v", err)
	}

	waitBGPEstablished(t, ctx, gobfdExaBGPIP, bgpEstablishTimeout)
	t.Log("ExaBGP BGP session re-established")

	waitRouteExists(t, ctx, exabgpRoute, routeTimeout)
	t.Logf("route %s restored after recovery", exabgpRoute)
}
