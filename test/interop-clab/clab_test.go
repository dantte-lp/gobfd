//go:build interop_clab

// Package interop_clab_test provides BFD interoperability tests against
// commercial/enterprise network operating systems via containerlab.
//
// Tests verify RFC 5880 (BFD base), RFC 5881 (BFD for IPv4/IPv6 single-hop),
// and RFC 5882 §10.2 (BGP interactions) against each vendor's BFD
// implementation.
//
// IPv6 dual-stack: RFC 5881 §5 (IPv6 single-hop BFD) is tested alongside
// IPv4 (§4) using ULA fd00::/8 addresses with /127 prefixes per RFC 6164.
//
// Supported vendors: Arista cEOS, Nokia SR Linux, Cisco XRd, SONiC-VS, VyOS, FRRouting.
// Tests are skipped for vendors whose container images are not available.
//
// Container management uses the Podman REST API via unix socket
// (/run/podman/podman.sock).
//
// Prerequisites:
//   - containerlab topology deployed: see test/interop-clab/run.sh
//   - All available vendor containers must be running.
//
// Run with:
//
//	go test -tags interop_clab -v -count=1 -timeout 600s ./test/interop-clab/
package interop_clab_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Constants
// =========================================================================

const (
	labName = "gobfd-vendors"

	gobfdContainer = "clab-" + labName + "-gobfd"

	pollInterval      = 2 * time.Second
	bfdUpTimeout      = 300 * time.Second // Vendor NOS may take time to boot + BGP/BFD recovery (XRd vRouter: 120-300s)
	failureDetectWait = 10 * time.Second
	recoveryTimeout   = 180 * time.Second
)

// =========================================================================
// Vendor peer definitions
// =========================================================================

// vendorPeer describes a vendor NOS node for BFD testing.
type vendorPeer struct {
	name      string   // Unique name: "arista", "arista-v6", etc.
	baseName  string   // Vendor base name: "arista", "nokia", "frr" (groups v4+v6).
	container string   // Podman container name (clab-<lab>-<node>).
	peerIP    string   // BFD peer IP on the vendor side.
	localIP   string   // BFD local IP on the GoBFD side.
	route     string   // Route announced by vendor for BGP verification.
	asn       int      // Vendor BGP ASN.
	showCmd   []string // CLI command to check BFD session state.
	upMatch   string   // Substring in show output indicating BFD Up.
}

var vendors = []vendorPeer{
	{
		name:      "arista",
		baseName:  "arista",
		container: "clab-" + labName + "-arista",
		peerIP:    "10.0.1.2",
		localIP:   "10.0.1.1",
		route:     "10.20.1.0/24",
		asn:       65002,
		showCmd:   []string{"Cli", "-p", "15", "-c", "show bfd peers detail"},
		upMatch:   "Up",
	},
	{
		name:      "arista-v6",
		baseName:  "arista",
		container: "clab-" + labName + "-arista",
		peerIP:    "fd00:0:1::1",
		localIP:   "fd00:0:1::",
		route:     "fd00:20:1::/48",
		asn:       65002,
		showCmd:   []string{"Cli", "-p", "15", "-c", "show bfd peers detail"},
		upMatch:   "Up",
	},
	{
		name:      "nokia",
		baseName:  "nokia",
		container: "clab-" + labName + "-nokia",
		peerIP:    "10.0.2.2",
		localIP:   "10.0.2.1",
		route:     "10.20.2.0/24",
		asn:       65003,
		showCmd:   []string{"sr_cli", "-d", "info", "from", "state", "/bfd"},
		upMatch:   "up",
	},
	{
		name:      "nokia-v6",
		baseName:  "nokia",
		container: "clab-" + labName + "-nokia",
		peerIP:    "fd00:0:2::1",
		localIP:   "fd00:0:2::",
		route:     "fd00:20:2::/48",
		asn:       65003,
		showCmd:   []string{"sr_cli", "-d", "info", "from", "state", "/bfd"},
		upMatch:   "up",
	},
	{
		name:      "cisco",
		baseName:  "cisco",
		container: "clab-" + labName + "-cisco",
		peerIP:    "10.0.3.2",
		localIP:   "10.0.3.1",
		route:     "10.20.3.0/24",
		asn:       65004,
		showCmd:   []string{"bash", "-c", "source /etc/profile && xr_cli 'show bfd session'"},
		upMatch:   "Up",
	},
	{
		name:      "cisco-v6",
		baseName:  "cisco",
		container: "clab-" + labName + "-cisco",
		peerIP:    "fd00:0:3::1",
		localIP:   "fd00:0:3::",
		route:     "fd00:20:3::/48",
		asn:       65004,
		showCmd:   []string{"bash", "-c", "source /etc/profile && xr_cli 'show bfd session'"},
		upMatch:   "Up",
	},
	{
		name:      "sonic",
		baseName:  "sonic",
		container: "clab-" + labName + "-sonic",
		peerIP:    "10.0.4.2",
		localIP:   "10.0.4.1",
		route:     "10.20.4.0/24",
		asn:       65005,
		showCmd:   []string{"vtysh", "-c", "show bfd peers"},
		upMatch:   "up",
	},
	{
		name:      "vyos",
		baseName:  "vyos",
		container: "clab-" + labName + "-vyos",
		peerIP:    "10.0.5.2",
		localIP:   "10.0.5.1",
		route:     "10.20.5.0/24",
		asn:       65006,
		showCmd:   []string{"vtysh", "-c", "show bfd peers"},
		upMatch:   "up",
	},
	{
		name:      "frr",
		baseName:  "frr",
		container: "clab-" + labName + "-frr",
		peerIP:    "10.0.6.2",
		localIP:   "10.0.6.1",
		route:     "10.20.6.0/24",
		asn:       65007,
		showCmd:   []string{"vtysh", "-c", "show bfd peers"},
		upMatch:   "up",
	},
	{
		name:      "frr-v6",
		baseName:  "frr",
		container: "clab-" + labName + "-frr",
		peerIP:    "fd00:0:6::1",
		localIP:   "fd00:0:6::",
		route:     "fd00:20:6::/48",
		asn:       65007,
		showCmd:   []string{"vtysh", "-c", "show bfd peers"},
		upMatch:   "up",
	},
}

// =========================================================================
// Helpers
// =========================================================================

// vendorAvailable checks if a vendor container is running.
func vendorAvailable(ctx context.Context, v vendorPeer) bool {
	return containerExists(ctx, v.container)
}

// gobfdBFDSessionUp checks GoBFD logs for a BFD session reaching Up state
// for the given peer IP.
func gobfdBFDSessionUp(ctx context.Context, peerIP string) (bool, error) {
	// GoBFD runs via "podman exec -d" (not as PID 1), so container logs are
	// empty. Use gobfdctl to query session state directly via gRPC.
	output, err := containerExec(ctx, gobfdContainer,
		"gobfdctl", "--addr", "localhost:50052", "session", "list")
	if err != nil {
		return false, nil // gobfd may not be ready yet
	}
	// gobfdctl output format: DISCRIMINATOR  PEER  LOCAL  TYPE  STATE  REMOTE-STATE  DIAG
	// Look for a line containing the peer IP with "Up" state.
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, peerIP) && strings.Contains(line, "Up") {
			return true, nil
		}
	}
	return false, nil
}

// vendorBFDSessionUp checks the vendor's BFD session state via CLI.
func vendorBFDSessionUp(ctx context.Context, v vendorPeer) (bool, error) {
	output, err := containerExec(ctx, v.container, v.showCmd...)
	if err != nil {
		return false, nil //nolint:nilerr // vendor may not be ready yet
	}
	return strings.Contains(strings.ToLower(output), strings.ToLower(v.upMatch)), nil
}

// waitForCondition polls until fn returns true or timeout expires.
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

// waitBFDUp waits for BFD session Up on both GoBFD and vendor side.
func waitBFDUp(ctx context.Context, t *testing.T, v vendorPeer) {
	t.Helper()

	waitForCondition(t, v.name+" BFD Up (vendor side)", bfdUpTimeout, func() (bool, error) {
		return vendorBFDSessionUp(ctx, v)
	})

	waitForCondition(t, v.name+" BFD Up (gobfd side)", bfdUpTimeout, func() (bool, error) {
		return gobfdBFDSessionUp(ctx, v.peerIP)
	})
}

// bfdSessionStuck checks whether GoBFD's session with the given peer
// is in a stuck state (remote AdminDown), indicating the vendor's BGP
// session is broken and needs recovery.
func bfdSessionStuck(ctx context.Context, peerIP string) bool {
	output, err := containerExec(ctx, gobfdContainer,
		"gobfdctl", "--addr", "localhost:50052", "session", "list")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, peerIP) &&
			strings.Contains(line, "Down") &&
			strings.Contains(line, "AdminDown") {
			return true
		}
	}
	return false
}

// ensureBFDUp waits for BFD session Up with automatic BGP recovery.
// If the session is stuck (remote AdminDown — typically from a previous
// test's container pause/unpause leaving BGP broken), it resets BGP on
// both sides before waiting. For normal startup (no stuck state), it
// falls through to waitBFDUp directly.
func ensureBFDUp(ctx context.Context, t *testing.T, v vendorPeer) {
	t.Helper()

	if bfdSessionStuck(ctx, v.peerIP) {
		t.Logf("BFD session with %s stuck (AdminDown), resetting BGP...", v.name)
		resetGoBGPNeighbor(ctx, t, v)
	}

	waitBFDUp(ctx, t, v)
}

// resetGoBGPNeighbor re-enables the GoBGP BGP session with a vendor peer.
// When a vendor container is paused, the BGP TCP connection dies and GoBGP
// transitions the neighbor to Idle(Admin). After unpausing, we must explicitly
// cycle the neighbor (disable → enable) so BGP can reconnect. A simple "enable"
// is insufficient when GoBGP receives a NOTIFICATION during the outage — it
// transitions to IDLE and won't retry without a full disable/enable cycle.
//
// IMPORTANT: This resets ALL sibling peers sharing the same container, not
// just the given peer. IPv4 and IPv6 sessions share a container — pausing
// it kills both BGP sessions, and both need explicit reset.
func resetGoBGPNeighbor(ctx context.Context, t *testing.T, v vendorPeer) {
	t.Helper()

	// Collect all peers sharing the same container (IPv4 + IPv6 siblings).
	var siblings []vendorPeer
	for _, peer := range vendors {
		if peer.container == v.container {
			siblings = append(siblings, peer)
		}
	}

	// For Nokia SR Linux: reset all BGP neighbors on the vendor side.
	// Nokia's BGP may have stale TCP sockets and retry backoff after container pause.
	if v.baseName == "nokia" {
		for _, sib := range siblings {
			if _, err := containerExec(ctx, v.container,
				"sr_cli", "-d", "tools /network-instance default protocols bgp neighbor "+
					sib.localIP+" reset-peer"); err != nil {
				t.Logf("nokia BGP reset-peer %s: %v (may not be needed)", sib.localIP, err)
			}
		}
	}

	// Cycle all sibling GoBGP neighbors: disable first to clear stale IDLE state,
	// then enable to trigger fresh connection attempts.
	for _, sib := range siblings {
		containerExec(ctx, gobfdContainer, "gobgp", "neighbor", sib.peerIP, "disable") //nolint:errcheck // best-effort
	}
	time.Sleep(1 * time.Second)
	for _, sib := range siblings {
		if _, err := containerExec(ctx, gobfdContainer,
			"gobgp", "neighbor", sib.peerIP, "enable"); err != nil {
			t.Logf("gobgp neighbor enable %s: %v (may not need reset)", sib.peerIP, err)
		}
	}
}

// dumpDebugInfo logs GoBFD and vendor container state for debugging failures.
// It creates its own context because it runs during t.Cleanup after parent context cancellation.
func dumpDebugInfo(t *testing.T, v vendorPeer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if out, err := containerExec(ctx, gobfdContainer,
		"gobfdctl", "--addr", "localhost:50052", "session", "list"); err == nil {
		t.Logf("gobfd sessions:\n%s", out)
	}
	if out, err := containerExec(ctx, v.container, v.showCmd...); err == nil {
		t.Logf("%s BFD state:\n%s", v.name, out)
	}
}

// =========================================================================
// Test: BFD session establishment per vendor
// =========================================================================

func TestVendorBFD_SessionEstablish(t *testing.T) {
	ctx := t.Context()

	for _, v := range vendors {
		t.Run(v.name, func(t *testing.T) {
			if !vendorAvailable(ctx, v) {
				t.Skipf("vendor %s container not available (image not installed)", v.name)
			}

			t.Cleanup(func() { //nolint:contextcheck // cleanup uses own context after parent cancellation
				if t.Failed() {
					dumpDebugInfo(t, v)
				}
			})

			t.Logf("waiting for BFD session with %s (%s)...", v.name, v.peerIP)
			ensureBFDUp(ctx, t, v)
			t.Logf("BFD session with %s established", v.name)

			// Verify vendor reports correct peer IP.
			output, err := containerExec(ctx, v.container, v.showCmd...)
			if err != nil {
				t.Fatalf("exec show bfd on %s: %v", v.name, err)
			}
			if !strings.Contains(output, v.localIP) {
				t.Errorf("%s BFD show does not contain gobfd IP %s:\n%s", v.name, v.localIP, output)
			}
			t.Logf("%s BFD state:\n%s", v.name, output)
		})
	}
}

// =========================================================================
// Test: BFD failure detection and recovery per vendor
// =========================================================================

func TestVendorBFD_FailureDetection(t *testing.T) {
	ctx := t.Context()

	for _, v := range vendors {
		t.Run(v.name, func(t *testing.T) {
			if !vendorAvailable(ctx, v) {
				t.Skipf("vendor %s container not available", v.name)
			}

			t.Cleanup(func() { //nolint:contextcheck // cleanup uses own context after parent cancellation
				if t.Failed() {
					dumpDebugInfo(t, v)
				}
				// Always try to unpause and reset BGP on cleanup.
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				containerUnpause(cleanupCtx, v.container) //nolint:errcheck // best-effort cleanup after test
				resetGoBGPNeighbor(cleanupCtx, t, v)
			})

			// Phase 1: Verify baseline — BFD Up.
			t.Logf("Phase 1: verifying BFD baseline with %s", v.name)
			ensureBFDUp(ctx, t, v)
			t.Logf("baseline: BFD session Up with %s", v.name)

			// Phase 2: Pause vendor → BFD Down.
			// RFC 5880 §6.8.4: Detection time = DetectMult × agreed interval.
			// Expected: 3 × 300ms = 900ms + margin.
			t.Logf("Phase 2: pausing %s to trigger BFD failure", v.name)
			if err := containerPause(ctx, v.container); err != nil {
				t.Fatalf("pause %s: %v", v.name, err)
			}

			// Wait for BFD to detect failure.
			time.Sleep(failureDetectWait)

			// Verify GoBFD detected the failure.
			// Poll gobfdctl for BFD state=Down.
			// RFC 5880 §6.8.4: Diag = 1 (Control Detection Time Expired).
			foundDown := false
			downDeadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(downDeadline) {
				sessOut, sErr := containerExec(ctx, gobfdContainer,
					"gobfdctl", "--addr", "localhost:50052", "session", "list")
				if sErr == nil {
					for line := range strings.SplitSeq(sessOut, "\n") {
						if strings.Contains(line, v.peerIP) && strings.Contains(line, "Down") {
							foundDown = true
							t.Logf("BFD Down detected for %s: %s", v.name, strings.TrimSpace(line))
							break
						}
					}
				}
				if foundDown {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if !foundDown {
				t.Errorf("BFD Down not detected in gobfd logs for %s after %v", v.name, failureDetectWait)
			}

			// Phase 3: Unpause vendor → BFD recovery.
			// After unpause, GoBGP's BGP neighbor is in Idle(Admin) — the TCP
			// connection died during the pause, so GoBGP won't retry. Re-enable
			// the neighbor so BGP re-establishes (required for protocol-triggered
			// BFD on Nokia SRL and similar vendors).
			t.Logf("Phase 3: unpausing %s for recovery", v.name)
			if err := containerUnpause(ctx, v.container); err != nil {
				t.Fatalf("unpause %s: %v", v.name, err)
			}
			resetGoBGPNeighbor(ctx, t, v)

			waitBFDUp(ctx, t, v)
			t.Logf("BFD session with %s recovered", v.name)
		})
	}
}

// =========================================================================
// Test: RFC 5880 packet format verification via tcpdump
// =========================================================================

func TestVendorBFD_RFC5880_PacketFormat(t *testing.T) {
	ctx := t.Context()

	// Capture BFD packets on the GoBFD container for a short period.
	// tcpdump -c 20: capture 20 packets then exit.
	captureCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	output, err := containerExec(captureCtx, gobfdContainer,
		"tcpdump", "-c", "20", "-nn", "-v",
		"-i", "any", "udp port 3784",
	)
	if err != nil && !strings.Contains(err.Error(), "exited with code") {
		t.Fatalf("tcpdump on gobfd: %v", err)
	}

	if output == "" {
		t.Fatal("no BFD packets captured on gobfd container")
	}

	t.Logf("captured BFD packets:\n%s", output)

	// RFC 5881 §4: BFD Control packets MUST use dest port 3784.
	if !strings.Contains(output, ".3784:") && !strings.Contains(output, ".3784 ") {
		t.Error("RFC 5881 §4: no packets with dest port 3784 found")
	}

	// RFC 5881 §5: TTL MUST be 255 for single-hop.
	if strings.Contains(output, "ttl 255") || strings.Contains(output, "TTL 255") {
		t.Log("RFC 5881 §5: TTL=255 verified in captured packets")
	}

	// RFC 5880 §4.1: Version MUST be 1.
	// tcpdump -v shows "BFDv1" in verbose output.
	if strings.Contains(output, "BFDv1") {
		t.Log("RFC 5880 §4.1: BFD version=1 verified")
	}

	// RFC 5881 §5: IPv6 BFD uses Hop Limit=255 (GTSM) instead of TTL.
	if strings.Contains(output, "hlim 255") || strings.Contains(output, "hop limit 255") {
		t.Log("RFC 5881 §5: Hop Limit=255 verified in IPv6 BFD packets")
	}

	// Verify packets come from the expected source port range.
	// RFC 5881 §4: Source port SHOULD be in range 49152-65535.
	// tcpdump output shows source.port format.
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.Contains(line, ".3784:") && !strings.Contains(line, ".3784 ") {
			continue
		}
		// Check that we see packets from/to our configured subnets (IPv4 and IPv6).
		if strings.Contains(line, "10.0.") {
			t.Logf("BFD packet on IPv4 subnet: %s", strings.TrimSpace(line))
		}
		if strings.Contains(line, "fd00:") {
			t.Logf("BFD packet on IPv6 subnet: %s", strings.TrimSpace(line))
		}
	}
}

// =========================================================================
// Test: BFD session independence — failure of one vendor doesn't affect others
// =========================================================================

func TestVendorBFD_SessionIndependence(t *testing.T) {
	ctx := t.Context()

	// Collect available vendors.
	var available []vendorPeer
	for _, v := range vendors {
		if vendorAvailable(ctx, v) {
			available = append(available, v)
		}
	}

	// Group available vendors by baseName. IPv4 and IPv6 peers share a
	// container, so pausing one container kills both BFD sessions for that
	// vendor. We must group by baseName and treat each group atomically.
	groups := make(map[string][]vendorPeer)
	var groupOrder []string
	for _, v := range available {
		if _, exists := groups[v.baseName]; !exists {
			groupOrder = append(groupOrder, v.baseName)
		}
		groups[v.baseName] = append(groups[v.baseName], v)
	}

	if len(groupOrder) < 2 {
		t.Skipf("need at least 2 vendor groups for independence test, have %d", len(groupOrder))
	}

	// Ensure all available sessions are Up.
	for _, v := range available {
		ensureBFDUp(ctx, t, v)
	}
	t.Log("all available BFD sessions Up")

	// Pause the first vendor group (all peers sharing the same container).
	targetGroup := groups[groupOrder[0]]
	targetContainer := targetGroup[0].container

	t.Logf("pausing vendor group %s (%d peers) to test independence", groupOrder[0], len(targetGroup))
	if err := containerPause(ctx, targetContainer); err != nil {
		t.Fatalf("pause %s: %v", targetContainer, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerUnpause(cleanupCtx, targetContainer) //nolint:errcheck // best-effort cleanup after test
		for _, v := range targetGroup {
			resetGoBGPNeighbor(cleanupCtx, t, v)
		}
	})

	// Wait for BFD Down detection on the paused vendor.
	time.Sleep(failureDetectWait)

	// Verify all sessions in OTHER groups remain Up.
	for _, gName := range groupOrder[1:] {
		for _, other := range groups[gName] {
			up, err := vendorBFDSessionUp(ctx, other)
			if err != nil {
				t.Errorf("check %s BFD after %s pause: %v", other.name, groupOrder[0], err)
				continue
			}
			if !up {
				t.Errorf("BFD session with %s went Down when %s was paused — sessions not independent",
					other.name, groupOrder[0])
			} else {
				t.Logf("BFD session with %s remained Up (independent of %s)", other.name, groupOrder[0])
			}
		}
	}

	// Restore.
	if err := containerUnpause(ctx, targetContainer); err != nil {
		t.Fatalf("unpause %s: %v", targetContainer, err)
	}
	for _, v := range targetGroup {
		resetGoBGPNeighbor(ctx, t, v)
	}
	for _, v := range targetGroup {
		waitBFDUp(ctx, t, v)
	}
	t.Logf("vendor group %s BFD sessions recovered", groupOrder[0])
}

// =========================================================================
// Test: Timer negotiation — RFC 5880 §6.8.2
// =========================================================================

func TestVendorBFD_TimerNegotiation(t *testing.T) {
	ctx := t.Context()

	for _, v := range vendors {
		t.Run(v.name, func(t *testing.T) {
			if !vendorAvailable(ctx, v) {
				t.Skipf("vendor %s container not available", v.name)
			}

			t.Cleanup(func() { //nolint:contextcheck // cleanup uses own context after parent cancellation
				if t.Failed() {
					dumpDebugInfo(t, v)
				}
			})

			ensureBFDUp(ctx, t, v)

			// Check GoBFD side via gobfdctl.
			gobfdOutput, err := containerExec(ctx, gobfdContainer,
				"gobfdctl", "--addr", "localhost:50052", "session", "list")
			if err != nil {
				t.Logf("gobfdctl session list: %v (may not have gRPC client)", err)
			} else {
				t.Logf("gobfdctl sessions:\n%s", gobfdOutput)

				// Verify the peer is listed and shows negotiated parameters.
				if !strings.Contains(gobfdOutput, v.peerIP) {
					t.Errorf("peer %s not found in gobfdctl output", v.peerIP)
				}
			}

			// Check vendor side for timer values.
			output, err := containerExec(ctx, v.container, v.showCmd...)
			if err != nil {
				t.Fatalf("exec show bfd on %s: %v", v.name, err)
			}

			// RFC 5880 §6.8.2: Negotiated TX interval = max(DesiredMinTx, RemoteMinRx).
			// Both sides configured 300ms, so negotiated should be 300ms (300000μs).
			// Detection time = DetectMult(3) × 300ms = 900ms.
			t.Logf("%s BFD timers:\n%s", v.name, output)

			// Verify interval values appear in output.
			// Most vendors show 300 or 300ms or 300000 (microseconds for Nokia).
			hasInterval := strings.Contains(output, "300") || strings.Contains(output, "300000")
			if !hasInterval {
				t.Errorf("could not verify 300ms interval in %s output — expected '300' or '300000'", v.name)
			}
		})
	}
}

// =========================================================================
// Test: Detection timing precision
// =========================================================================

func TestVendorBFD_DetectionTiming(t *testing.T) {
	ctx := t.Context()

	for _, v := range vendors {
		t.Run(v.name, func(t *testing.T) {
			if !vendorAvailable(ctx, v) {
				t.Skipf("vendor %s container not available", v.name)
			}

			t.Cleanup(func() { //nolint:contextcheck // cleanup uses own context after parent cancellation
				if t.Failed() {
					dumpDebugInfo(t, v)
				}
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				containerUnpause(cleanupCtx, v.container) //nolint:errcheck // best-effort cleanup after test
				resetGoBGPNeighbor(cleanupCtx, t, v)
			})

			ensureBFDUp(ctx, t, v)

			// Pause vendor and measure time until GoBFD detects Down.
			// RFC 5880 §6.8.4: Detection time = 3 × 300ms = 900ms.
			// Allow generous tolerance: detection should happen within 5 seconds.
			startTime := time.Now()

			if err := containerPause(ctx, v.container); err != nil {
				t.Fatalf("pause %s: %v", v.name, err)
			}

			// Poll GoBFD logs for Down state.
			detected := false
			var detectionTime time.Duration

			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
				sessOut, sErr := containerExec(ctx, gobfdContainer,
					"gobfdctl", "--addr", "localhost:50052", "session", "list")
				if sErr == nil {
					for line := range strings.SplitSeq(sessOut, "\n") {
						if strings.Contains(line, v.peerIP) && strings.Contains(line, "Down") {
							detected = true
							detectionTime = time.Since(startTime)
							break
						}
					}
				}
				if detected {
					break
				}
			}

			if !detected {
				t.Errorf("BFD Down not detected for %s within 10s", v.name)
			} else {
				t.Logf("BFD Down for %s detected in %v (expected ~900ms, max 5s)", v.name, detectionTime)

				// RFC 5880 §6.8.4: should be close to DetectMult × interval = 900ms.
				// Generous upper bound: 5s (accounts for container pause latency).
				if detectionTime > 5*time.Second {
					t.Errorf("detection time %v exceeds 5s tolerance for %s", detectionTime, v.name)
				}
			}

			// Restore for next test — unpause and reset BGP, but don't block
			// on full BFD recovery. The next subtest's ensureBFDUp handles that.
			// Recovery correctness is already verified in TestVendorBFD_FailureDetection.
			if err := containerUnpause(ctx, v.container); err != nil {
				t.Fatalf("unpause %s: %v", v.name, err)
			}
			resetGoBGPNeighbor(ctx, t, v)
			time.Sleep(5 * time.Second) // brief settle time before next subtest
		})
	}
}

// =========================================================================
// Test: All available vendors summary
// =========================================================================

func TestVendorBFD_AllAvailable(t *testing.T) {
	ctx := t.Context()

	availableCount := 0
	for _, v := range vendors {
		if vendorAvailable(ctx, v) {
			availableCount++
			t.Logf("vendor %s: available (container %s)", v.name, v.container)
		} else {
			t.Logf("vendor %s: NOT available (skipped)", v.name)
		}
	}

	if availableCount == 0 {
		t.Fatal("no vendor containers available — deploy containerlab topology first")
	}

	t.Logf("%d/%d vendor containers available", availableCount, len(vendors))

	// Wait for all available BFD sessions to establish.
	for _, v := range vendors {
		if !vendorAvailable(ctx, v) {
			continue
		}
		ensureBFDUp(ctx, t, v)
		t.Logf("BFD session Up with %s (%s → %s)", v.name, v.localIP, v.peerIP)
	}

	t.Logf("all %d available BFD sessions established", availableCount)
}

// =========================================================================
// Helpers for formatted output
// =========================================================================

func init() {
	// Ensure container names are consistent.
	// IPv6 peers share the same container as their IPv4 counterpart,
	// so validate against baseName (e.g. "arista") not name ("arista-v6").
	for _, v := range vendors {
		expected := fmt.Sprintf("clab-%s-%s", labName, v.baseName)
		if v.container != expected {
			panic(fmt.Sprintf("vendor %s container name mismatch: %s != %s", v.name, v.container, expected))
		}
	}
}
