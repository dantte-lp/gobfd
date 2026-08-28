//go:build interop_bgp || interop_bgp_testcontainers

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
//   - Set INTEROP_PROJECT_NAME to the validated project used to create the stack.
//   - podman compose -p "$INTEROP_PROJECT_NAME" -f test/interop-bgp/compose.yml up --build -d
//   - All containers must be running.
//
// Every runtime operation resolves the fixed name to an exact project-labelled
// immutable container ID before using the Podman API.
package interop_bgp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopcheck"
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

// gobgpNeighborState returns the BGP session state for a specific peer.
// Returns lowercase state string: "established", "idle", "active", "opensent", etc.
func gobgpNeighborState(ctx context.Context, peerIP string) (string, error) {
	output, err := gobgpCmd(ctx, "neighbor", "-j")
	if err != nil {
		return "", err
	}

	state, err := interopcheck.GoBGPNeighborState([]byte(output), peerIP)
	if err != nil {
		return "", fmt.Errorf("parse gobgp neighbor json: %w: raw=%s", err, output)
	}
	return state, nil
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

	status, err := interopcheck.FRRBFDPeerStatus([]byte(output), gobfdBGPIP)
	if err != nil {
		return "", fmt.Errorf("parse FRR BFD peer JSON: %w: raw=%s", err, output)
	}
	return status, nil
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

func waitBGPEstablished(ctx context.Context, t *testing.T, peerIP string) {
	t.Helper()
	waitForCondition(t, "BGP Established with "+peerIP, bgpEstablishTimeout, func() (bool, error) {
		state, err := gobgpNeighborState(ctx, peerIP)
		if err != nil {
			return false, err
		}
		return state == "established", nil
	})
}

func waitRouteExists(ctx context.Context, t *testing.T, prefix string) {
	t.Helper()
	waitForCondition(t, "route "+prefix+" in RIB", routeTimeout, func() (bool, error) {
		return gobgpRouteExists(ctx, prefix)
	})
}

func waitRouteGone(ctx context.Context, t *testing.T, prefix string, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "route "+prefix+" withdrawn from RIB", timeout, func() (bool, error) {
		exists, err := gobgpRouteExists(ctx, prefix)
		if err != nil {
			return false, err
		}
		return !exists, nil
	})
}

func waitFRRBFDUp(ctx context.Context, t *testing.T, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "FRR BFD session Up", timeout, func() (bool, error) {
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})
}

func waitBIRD3BFDUp(ctx context.Context, t *testing.T, timeout time.Duration) {
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
	waitBGPEstablished(ctx, t, frrBGPIP)
	t.Log("FRR BGP session Established")

	waitBGPEstablished(ctx, t, bird3BGPIP)
	t.Log("BIRD3 BGP session Established")

	waitBGPEstablished(ctx, t, gobfdExaBGPIP)
	t.Log("ExaBGP BGP session Established")

	waitFRRBFDUp(ctx, t, bfdUpTimeout)
	t.Log("FRR BFD session Up")

	waitBIRD3BFDUp(ctx, t, bfdUpTimeout)
	t.Log("BIRD3 BFD session Up")

	waitRouteExists(ctx, t, frrRoute)
	t.Logf("Route %s received from FRR", frrRoute)

	waitRouteExists(ctx, t, bird3Route)
	t.Logf("Route %s received from BIRD3", bird3Route)

	waitRouteExists(ctx, t, exabgpRoute)
	t.Logf("Route %s received from ExaBGP", exabgpRoute)

	t.Log("all three BGP+BFD peerings and routes verified")
}

// =========================================================================
// Test: Scenario 1 — GoBFD + GoBGP <-> FRR (BGP+BFD)
// =========================================================================

type bgpBFDLifecycleScenario struct {
	peerName        string
	container       string
	neighborIP      string
	route           string
	otherPeerName   string
	otherNeighborIP string
	waitBFDUp       func(context.Context, *testing.T, time.Duration)
}

func runBGPBFDLifecycle(t *testing.T, scenario bgpBFDLifecycleScenario) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBGPState(t)
			dumpGoBFDLogs(t)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerStart(ctx, scenario.container) //nolint:errcheck // Cleanup is best-effort after the primary test result.
	})
	ctx := t.Context()

	// Phase 1: Establish.
	t.Logf("Phase 1: verifying BGP + BFD + route baseline with %s", scenario.peerName)
	waitBGPEstablished(ctx, t, scenario.neighborIP)
	scenario.waitBFDUp(ctx, t, bfdUpTimeout)
	waitRouteExists(ctx, t, scenario.route)
	t.Logf("baseline: BGP Established, BFD Up, route %s present", scenario.route)

	// Phase 2: BFD failure -> BGP disabled.
	t.Logf("Phase 2: stopping %s to trigger BFD failure", scenario.peerName)
	if err := containerStop(ctx, scenario.container); err != nil {
		t.Fatalf("stop %s: %v", scenario.container, err)
	}

	time.Sleep(failureDetectWait)

	state, err := gobgpNeighborState(ctx, scenario.neighborIP)
	if err != nil {
		t.Fatalf("check %s BGP state: %v", scenario.peerName, err)
	}
	if state == "established" {
		t.Errorf("%s BGP session still Established after BFD failure, expected disabled", scenario.peerName)
	} else {
		t.Logf("%s BGP session state: %s (expected non-established)", scenario.peerName, state)
	}

	waitRouteGone(ctx, t, scenario.route, routeTimeout)
	t.Logf("route %s withdrawn after BFD failure", scenario.route)

	otherState, _ := gobgpNeighborState(ctx, scenario.otherNeighborIP)
	if otherState != "established" {
		t.Errorf(
			"%s BGP session affected by %s failure: state=%s",
			scenario.otherPeerName,
			scenario.peerName,
			otherState,
		)
	}

	// Phase 3: Recovery.
	t.Logf("Phase 3: starting %s for recovery", scenario.peerName)
	if err := containerStart(ctx, scenario.container); err != nil {
		t.Fatalf("start %s: %v", scenario.container, err)
	}

	waitBGPEstablished(ctx, t, scenario.neighborIP)
	t.Logf("%s BGP session re-established", scenario.peerName)

	scenario.waitBFDUp(ctx, t, bfdUpTimeout)
	t.Logf("%s BFD session Up", scenario.peerName)

	waitRouteExists(ctx, t, scenario.route)
	t.Logf("route %s restored after recovery", scenario.route)
}

func TestBGPBFD_FRR(t *testing.T) {
	runBGPBFDLifecycle(t, bgpBFDLifecycleScenario{
		peerName:        "FRR",
		container:       frrContainer,
		neighborIP:      frrBGPIP,
		route:           frrRoute,
		otherPeerName:   "BIRD3",
		otherNeighborIP: bird3BGPIP,
		waitBFDUp:       waitFRRBFDUp,
	})
}

// =========================================================================
// Test: Scenario 2 — GoBFD + GoBGP <-> BIRD3 (BGP+BFD)
// =========================================================================

func TestBGPBFD_BIRD3(t *testing.T) {
	runBGPBFDLifecycle(t, bgpBFDLifecycleScenario{
		peerName:        "BIRD3",
		container:       bird3Container,
		neighborIP:      bird3BGPIP,
		route:           bird3Route,
		otherPeerName:   "FRR",
		otherNeighborIP: frrBGPIP,
		waitBFDUp:       waitBIRD3BFDUp,
	})
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
		containerUnpause(ctx, gobfdExaBGPContainer) //nolint:errcheck // Cleanup is best-effort after the primary test result.
	})
	ctx := t.Context()

	// Phase 1: Establish.
	t.Log("Phase 1: verifying BGP + BFD + route baseline with ExaBGP")
	waitBGPEstablished(ctx, t, gobfdExaBGPIP)
	waitRouteExists(ctx, t, exabgpRoute)
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

	waitRouteGone(ctx, t, exabgpRoute, routeTimeout)
	t.Logf("route %s withdrawn after BFD failure", exabgpRoute)

	// Phase 3: Recovery via unpause.
	t.Log("Phase 3: unpausing gobfd-exabgp for recovery")
	if err := containerUnpause(ctx, gobfdExaBGPContainer); err != nil {
		t.Fatalf("unpause gobfd-exabgp: %v", err)
	}

	waitBGPEstablished(ctx, t, gobfdExaBGPIP)
	t.Log("ExaBGP BGP session re-established")

	waitRouteExists(ctx, t, exabgpRoute)
	t.Logf("route %s restored after recovery", exabgpRoute)
}
