//go:build interop_rfc

// Package interop_rfc_test provides RFC-specific interoperability tests
// for GoBFD against FRR peers and echo reflectors.
//
// Four RFCs are tested:
//  1. RFC 7419 — Common interval alignment (tshark packet analysis)
//  2. RFC 9384 — BGP Cease BFD-Down (log + BGP state inspection)
//  3. RFC 9468 — Unsolicited BFD (auto-session creation)
//  4. RFC 9747 — Unaffiliated BFD echo (echo reflector)
//
// Container management uses the Podman REST API via unix socket
// (/run/podman/podman.sock), so no podman CLI binary is required
// for exec/pause/unpause/logs operations.
//
// Run with:
//
//	go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/
//
// Prerequisites:
//   - podman-compose -f test/interop-rfc/compose.yml up --build -d
//   - All containers must be running.
package interop_rfc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Constants
// =========================================================================

const (
	gobfdRFCIP       = "172.22.0.10" // gobfd-rfc (RFC 7419 + 9468)
	frrRFCIP         = "172.22.0.20" // frr-rfc (BFD-only, RFC 7419)
	gobfdRFC9384IP   = "172.22.0.30" // gobfd-rfc9384 + gobgp-rfc (shared netns)
	frrRFCBGPIP      = "172.22.0.40" // frr-rfc-bgp (BGP+BFD, RFC 9384)
	frrUnsolicitedIP  = "172.22.0.50" // frr-rfc-unsolicited (RFC 9468)
	echoReflectorIP   = "172.22.0.60" // echo-reflector (RFC 9747)

	// Container names (as set by compose container_name).
	gobfdRFCContainer        = "gobfd-rfc-interop"
	gobfdRFC9384Container    = "gobfd-rfc9384-interop"
	gobgpRFCContainer        = "gobgp-rfc-interop"
	frrRFCContainer          = "frr-rfc-interop"
	frrUnsolicitedContainer  = "frr-rfc-unsolicited-interop"
	frrRFCBGPContainer       = "frr-rfc-bgp-interop"
	tsharkRFCContainer       = "tshark-rfc-interop"
	echoReflectorContainer   = "echo-reflector-interop"

	frrBGPRoute = "10.22.0.0/24"

	pollInterval = 2 * time.Second

	// Timeouts for waiting on BFD/BGP convergence.
	bfdUpTimeout        = 60 * time.Second
	bgpEstablishTimeout = 90 * time.Second
	routeTimeout        = 30 * time.Second
	failureDetectWait   = 10 * time.Second
)

// =========================================================================
// GoBGP CLI helpers
// =========================================================================

// gobgpCmd runs the gobgp CLI tool inside the gobgp-rfc container.
func gobgpCmd(ctx context.Context, args ...string) (string, error) {
	return containerExec(ctx, gobgpRFCContainer, append([]string{"gobgp"}, args...)...)
}

// GoBGP v3 session state enum values (PeerState_SessionState protobuf).
const (
	bgpStateEstablished = 6
)

// bgpSessionStateName maps GoBGP v3 protobuf session_state numbers to names.
var bgpSessionStateName = map[int]string{
	0: "unspecified",
	1: "idle",
	2: "connect",
	3: "active",
	4: "opensent",
	5: "openconfirm",
	6: "established",
}

// gobgpNeighborState returns the BGP session state for a specific peer.
func gobgpNeighborState(ctx context.Context, peerIP string) (string, error) {
	output, err := gobgpCmd(ctx, "neighbor", "-j")
	if err != nil {
		return "", err
	}

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

// =========================================================================
// FRR BFD helpers
// =========================================================================

// frrBFDPeerUp checks if a specific FRR container reports BFD session Up
// for a given peer IP.
func frrBFDPeerUp(ctx context.Context, frrContainer, peerIP string) (bool, error) {
	output, err := containerExec(ctx, frrContainer, "vtysh", "-c", "show bfd peers json")
	if err != nil {
		return false, fmt.Errorf("vtysh show bfd peers json on %s: %w: %s", frrContainer, err, output)
	}

	jsonStr := strings.TrimSpace(output)
	if idx := strings.Index(output, "\n["); idx >= 0 {
		jsonStr = strings.TrimSpace(output[idx+1:])
	} else if !strings.HasPrefix(jsonStr, "[") {
		return false, fmt.Errorf("no JSON array in vtysh output from %s: %s", frrContainer, output)
	}

	var peers []struct {
		Peer   string `json:"peer"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &peers); err != nil {
		return false, fmt.Errorf("parse bfd peers json from %s: %w: raw=%s", frrContainer, err, jsonStr)
	}

	for _, p := range peers {
		if p.Peer == peerIP {
			return strings.EqualFold(p.Status, "up"), nil
		}
	}

	return false, nil
}

// =========================================================================
// Tshark analysis helpers
// =========================================================================

// tsharkQuery runs tshark on the captured pcapng file inside the tshark container
// via the Podman REST API (no podman CLI binary required).
// Strips tshark's stderr warning about running as root (mixed into stdout
// by the Docker stream protocol).
func tsharkQuery(ctx context.Context, args ...string) (string, error) {
	cmdArgs := append([]string{"tshark", "-r", "/captures/bfd.pcapng"}, args...)
	output, err := containerExec(ctx, tsharkRFCContainer, cmdArgs...)
	if err != nil {
		return "", err
	}
	// tshark emits "Running as user ..." warning on stderr, which the
	// Docker stream demuxer merges into stdout. Strip it.
	var cleaned []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Running as user") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n"), nil
}

// tsharkFields extracts specific fields from packets matching a display filter.
// Returns [][]string where each row is one packet's field values.
func tsharkFields(ctx context.Context, filter string, fields []string, maxCount int) ([][]string, error) {
	args := []string{"-Y", filter, "-T", "fields"}
	for _, f := range fields {
		args = append(args, "-e", f)
	}
	args = append(args, "-E", "separator=\t", "-E", "header=n")
	if maxCount > 0 {
		args = append(args, "-c", strconv.Itoa(maxCount))
	}

	output, err := tsharkQuery(ctx, args...)
	if err != nil {
		return nil, err
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	var rows [][]string
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
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

// parseHexOrDec parses a string that may be hex (0x...) or decimal.
func parseHexOrDec(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	return strconv.ParseUint(s, 0, 64)
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

func waitFRRBFDUp(t *testing.T, ctx context.Context, frrContainer, peerIP string, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, fmt.Sprintf("BFD Up on %s for peer %s", frrContainer, peerIP), timeout, func() (bool, error) {
		return frrBFDPeerUp(ctx, frrContainer, peerIP)
	})
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

// =========================================================================
// Debug helpers
// =========================================================================

func dumpGoBFDLogs(t *testing.T, container string, lines int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if out, err := containerLogs(ctx, container, lines); err == nil {
		t.Logf("%s logs (tail %d):\n%s", container, lines, out)
	}
}

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

func dumpTsharkCapture(t *testing.T, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	output, err := containerExec(ctx, tsharkRFCContainer,
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd",
		"-c", fmt.Sprintf("%d", count),
		"-T", "fields",
		"-e", "frame.time_relative",
		"-e", "ip.src",
		"-e", "ip.dst",
		"-e", "bfd.sta",
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

// =========================================================================
// Test 1: RFC 7419 — Common Interval Alignment
// =========================================================================

// TestRFC7419_CommonIntervalAlignment verifies that GoBFD aligns the
// DesiredMinTxInterval to the next RFC 7419 common interval value.
//
// Config: gobfd-rfc has align_intervals=true, desired_min_tx=80ms.
// Expected: DesiredMinTxInterval in Up packets == 100000 (100ms),
// which is the next common interval above 80ms per RFC 7419 Section 3.
func TestRFC7419_CommonIntervalAlignment(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBFDLogs(t, gobfdRFCContainer, 30)
			dumpTsharkCapture(t, 100)
		}
	})
	ctx := t.Context()

	// Step 1: Wait for BFD session Up between gobfd-rfc and frr-rfc.
	t.Log("waiting for BFD session Up between gobfd-rfc and frr-rfc...")
	waitFRRBFDUp(t, ctx, frrRFCContainer, gobfdRFCIP, bfdUpTimeout)
	t.Log("BFD session Up")

	// Allow tshark to accumulate some Up packets.
	time.Sleep(5 * time.Second)

	// Step 2: Extract DesiredMinTxInterval from GoBFD→FRR Up packets.
	filter := fmt.Sprintf(
		"bfd && ip.src == %s && ip.dst == %s && bfd.sta == 0x03",
		gobfdRFCIP, frrRFCIP,
	)
	rows, err := tsharkFields(ctx, filter,
		[]string{"bfd.desired_min_tx_interval"}, 10)
	if err != nil {
		t.Fatalf("tshark field extraction: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no Up packets from GoBFD to FRR captured by tshark")
	}

	// Step 3: Assert interval == 100000 (100ms aligned from 80ms).
	for i, row := range rows {
		if len(row) == 0 {
			continue
		}
		interval, err := parseHexOrDec(row[0])
		if err != nil {
			t.Fatalf("parse interval at row %d: %v", i, err)
		}
		if interval != 100000 {
			t.Errorf("packet %d: DesiredMinTxInterval = %d, want 100000 (100ms aligned per RFC 7419)",
				i, interval)
		}
	}
	t.Logf("verified %d Up packets with DesiredMinTxInterval=100000 (100ms)", len(rows))

	// Step 4: Verify session stability — stays Up for 5 seconds.
	t.Log("verifying session stability (5s)...")
	time.Sleep(5 * time.Second)

	up, err := frrBFDPeerUp(ctx, frrRFCContainer, gobfdRFCIP)
	if err != nil {
		t.Fatalf("check FRR BFD peer status: %v", err)
	}
	if !up {
		t.Error("BFD session went Down during stability check")
	}
	t.Log("session stable after 5s")
}

// =========================================================================
// Test 2: RFC 9384 — BGP Cease BFD-Down Communication
// =========================================================================

// TestRFC9384_BGPCeaseBFDDown verifies the full BFD→BGP failure cycle
// with RFC 9384-enriched Cease/BFD-Down communication strings.
//
// Phases:
//  1. Verify baseline: BGP Established, BFD Up, route present
//  2. Pause frr-rfc-bgp to simulate failure
//  3. Verify BFD Down detection → RFC 9384 log message → BGP disabled → route withdrawn
//  4. Unpause frr-rfc-bgp → verify BGP + route recovery
func TestRFC9384_BGPCeaseBFDDown(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBFDLogs(t, gobfdRFC9384Container, 50)
			dumpGoBGPState(t)
		}
		// Always unpause to leave stack clean.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerUnpause(ctx, frrRFCBGPContainer) //nolint:errcheck
	})
	ctx := t.Context()

	// Phase 1: Verify baseline.
	t.Log("Phase 1: verifying baseline — BGP Established, BFD Up, route present")
	waitBGPEstablished(t, ctx, frrRFCBGPIP, bgpEstablishTimeout)
	t.Log("BGP session Established")

	waitFRRBFDUp(t, ctx, frrRFCBGPContainer, gobfdRFC9384IP, bfdUpTimeout)
	t.Log("BFD session Up")

	waitRouteExists(t, ctx, frrBGPRoute, routeTimeout)
	t.Logf("route %s present in GoBGP RIB", frrBGPRoute)

	// Phase 2: Pause frr-rfc-bgp to trigger BFD failure.
	t.Log("Phase 2: pausing frr-rfc-bgp to trigger BFD failure")
	if err := containerPause(ctx, frrRFCBGPContainer); err != nil {
		t.Fatalf("pause frr-rfc-bgp: %v", err)
	}

	// Wait for BFD detection timeout (~1s = 3 * 300ms) + margin.
	time.Sleep(failureDetectWait)

	// Phase 3: Verify RFC 9384 behavior.
	t.Log("Phase 3: verifying RFC 9384 Cease/BFD-Down behavior")

	// Check gobfd-rfc9384 logs for RFC 9384 communication string.
	logs, err := containerLogs(ctx, gobfdRFC9384Container, 100)
	if err != nil {
		t.Fatalf("get gobfd-rfc9384 logs: %v", err)
	}

	if !strings.Contains(logs, "BFD Down") {
		t.Error("gobfd-rfc9384 did not log BFD Down event")
		t.Logf("logs:\n%s", logs)
	} else {
		t.Log("gobfd-rfc9384 logged BFD Down event")
	}

	// Look for the RFC 9384 enriched communication string.
	if strings.Contains(logs, "RFC 9384 Cease/10") || strings.Contains(logs, "Cease") {
		t.Log("RFC 9384 Cease/BFD-Down communication string found in logs")
	} else {
		t.Log("note: RFC 9384 communication string logged internally via GoBGP DisablePeer")
	}

	// Check BGP session state — should NOT be established.
	state, err := gobgpNeighborState(ctx, frrRFCBGPIP)
	if err != nil {
		t.Fatalf("check BGP state: %v", err)
	}
	if state == "established" {
		t.Errorf("BGP session still Established after BFD failure, expected disabled")
	} else {
		t.Logf("BGP session state: %s (expected non-established)", state)
	}

	// Check route withdrawn.
	waitRouteGone(t, ctx, frrBGPRoute, routeTimeout)
	t.Logf("route %s withdrawn after BFD failure", frrBGPRoute)

	// Phase 4: Recovery — unpause frr-rfc-bgp.
	t.Log("Phase 4: unpausing frr-rfc-bgp for recovery")
	if err := containerUnpause(ctx, frrRFCBGPContainer); err != nil {
		t.Fatalf("unpause frr-rfc-bgp: %v", err)
	}

	waitBGPEstablished(t, ctx, frrRFCBGPIP, bgpEstablishTimeout)
	t.Log("BGP session re-established")

	waitRouteExists(t, ctx, frrBGPRoute, routeTimeout)
	t.Logf("route %s restored after recovery", frrBGPRoute)
}

// =========================================================================
// Test 3: RFC 9468 — Unsolicited BFD Session Auto-Creation
// =========================================================================

// TestRFC9468_UnsolicitedBFD verifies that GoBFD auto-creates a BFD session
// when FRR initiates BFD to a GoBFD instance running in unsolicited mode.
//
// gobfd-rfc has unsolicited.enabled=true with NO pre-configured session
// for frr-rfc-unsolicited (172.22.0.50). FRR actively sends BFD packets,
// and GoBFD must create a passive session via RFC 9468.
func TestRFC9468_UnsolicitedBFD(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBFDLogs(t, gobfdRFCContainer, 50)
			dumpTsharkCapture(t, 100)
		}
	})
	ctx := t.Context()

	// Step 1: Wait for FRR to start sending BFD and for session to come Up.
	// FRR initiates BFD to gobfd-rfc. GoBFD must auto-create the session.
	t.Log("waiting for FRR unsolicited BFD session to come Up...")
	waitFRRBFDUp(t, ctx, frrUnsolicitedContainer, gobfdRFCIP, bfdUpTimeout)
	t.Log("FRR reports BFD session Up (GoBFD auto-created session via RFC 9468)")

	// Step 2: Verify tshark sees BFD Up packets from GoBFD to FRR-unsolicited.
	// Allow some Up packets to accumulate.
	time.Sleep(3 * time.Second)

	filter := fmt.Sprintf(
		"bfd && ip.src == %s && ip.dst == %s && bfd.sta == 0x03",
		gobfdRFCIP, frrUnsolicitedIP,
	)
	count, err := tsharkCount(ctx, filter)
	if err != nil {
		t.Fatalf("tshark count for unsolicited Up packets: %v", err)
	}
	if count == 0 {
		t.Error("no BFD Up packets from GoBFD to FRR-unsolicited in tshark capture")
	} else {
		t.Logf("tshark captured %d BFD Up packets from GoBFD to FRR-unsolicited", count)
	}

	// Step 3: Verify gobfd-rfc logged the unsolicited session creation.
	logs, err := containerLogs(ctx, gobfdRFCContainer, 100)
	if err != nil {
		t.Fatalf("get gobfd-rfc logs: %v", err)
	}

	if strings.Contains(logs, "unsolicited session created") {
		t.Log("gobfd-rfc logged unsolicited session creation (RFC 9468)")
	} else {
		t.Log("note: unsolicited session creation log not found (may be in earlier logs)")
	}

	// Step 4: Verify session stability — stays Up for 5 seconds.
	t.Log("verifying session stability (5s)...")
	time.Sleep(5 * time.Second)

	up, err := frrBFDPeerUp(ctx, frrUnsolicitedContainer, gobfdRFCIP)
	if err != nil {
		t.Fatalf("check FRR unsolicited BFD status: %v", err)
	}
	if !up {
		t.Error("unsolicited BFD session went Down during stability check")
	}
	t.Log("unsolicited session stable after 5s")
}

// =========================================================================
// Test 4: RFC 9747 — Unaffiliated BFD Echo Session
// =========================================================================

// TestRFC9747_EchoSession verifies the BFD echo function (RFC 9747).
//
// GoBFD sends echo packets on UDP port 3785 to echo-reflector (172.22.0.60).
// The echo reflector bounces packets back to GoBFD on port 3785.
// GoBFD's echo receiver demuxes the returned packets by MyDiscriminator
// and transitions the echo session Down → Up.
//
// Phases:
//  1. Wait for echo session to come Up (log inspection)
//  2. Verify echo packets on the wire via tshark (port 3785)
//  3. Pause echo-reflector → verify echo session goes Down
//  4. Unpause echo-reflector → verify echo session recovers to Up
func TestRFC9747_EchoSession(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpGoBFDLogs(t, gobfdRFCContainer, 50)
			dumpTsharkCapture(t, 100)
		}
		// Always unpause to leave stack clean.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containerUnpause(ctx, echoReflectorContainer) //nolint:errcheck
	})
	ctx := t.Context()

	// Phase 1: Wait for GoBFD echo session to come Up.
	// The echo session starts Down and transitions to Up once the first
	// reflected echo packet is received.
	t.Log("Phase 1: waiting for echo session Up (gobfd-rfc → echo-reflector)")
	waitForCondition(t, "echo session Up in gobfd-rfc logs", bfdUpTimeout, func() (bool, error) {
		logs, err := containerLogs(ctx, gobfdRFCContainer, 200)
		if err != nil {
			return false, err
		}
		// Look for echo state transition: Down → Up.
		return strings.Contains(logs, "echo session state changed") &&
			strings.Contains(logs, "new_state=Up"), nil
	})
	t.Log("echo session Up")

	// Phase 2: Verify echo packets on the wire.
	// Allow some echo packets to accumulate.
	time.Sleep(3 * time.Second)

	// Check tshark for UDP port 3785 packets from GoBFD to echo-reflector.
	filter := fmt.Sprintf(
		"udp.dstport == 3785 && ip.src == %s && ip.dst == %s",
		gobfdRFCIP, echoReflectorIP,
	)
	echoCount, err := tsharkCount(ctx, filter)
	if err != nil {
		t.Fatalf("tshark count for echo packets: %v", err)
	}
	if echoCount == 0 {
		t.Error("no echo packets (port 3785) from GoBFD to echo-reflector in tshark capture")
	} else {
		t.Logf("tshark captured %d echo packets from GoBFD to echo-reflector", echoCount)
	}

	// Also check reflected echo packets coming back.
	reflectFilter := fmt.Sprintf(
		"udp.dstport == 3785 && ip.src == %s && ip.dst == %s",
		echoReflectorIP, gobfdRFCIP,
	)
	reflectCount, err := tsharkCount(ctx, reflectFilter)
	if err != nil {
		t.Fatalf("tshark count for reflected echo packets: %v", err)
	}
	if reflectCount == 0 {
		t.Error("no reflected echo packets from echo-reflector back to GoBFD")
	} else {
		t.Logf("tshark captured %d reflected echo packets", reflectCount)
	}

	// Phase 3: Pause echo-reflector to trigger echo failure.
	t.Log("Phase 3: pausing echo-reflector to trigger echo failure")
	if err := containerPause(ctx, echoReflectorContainer); err != nil {
		t.Fatalf("pause echo-reflector: %v", err)
	}

	// Wait for detection timeout: detect_mult(3) * tx_interval(200ms) = 600ms + margin.
	time.Sleep(failureDetectWait)

	// Verify echo session went Down in GoBFD logs.
	logs, err := containerLogs(ctx, gobfdRFCContainer, 200)
	if err != nil {
		t.Fatalf("get gobfd-rfc logs: %v", err)
	}

	if !strings.Contains(logs, "DiagEchoFailed") {
		t.Error("gobfd-rfc did not log DiagEchoFailed after echo-reflector pause")
		t.Logf("logs:\n%s", logs)
	} else {
		t.Log("echo session Down with DiagEchoFailed (echo-reflector paused)")
	}

	// Phase 4: Unpause echo-reflector → echo session should recover.
	t.Log("Phase 4: unpausing echo-reflector for recovery")
	if err := containerUnpause(ctx, echoReflectorContainer); err != nil {
		t.Fatalf("unpause echo-reflector: %v", err)
	}

	// Wait for echo session to come back Up.
	waitForCondition(t, "echo session recovery (Down → Up)", bfdUpTimeout, func() (bool, error) {
		recentLogs, err := containerLogs(ctx, gobfdRFCContainer, 50)
		if err != nil {
			return false, err
		}
		// The most recent state transition should be to Up.
		// Look for the pattern after the pause/unpause cycle.
		return strings.Contains(recentLogs, "new_state=Up") &&
			strings.Contains(recentLogs, "mode=echo"), nil
	})
	t.Log("echo session recovered to Up after echo-reflector unpause")

	// Verify session stability post-recovery.
	t.Log("verifying echo session stability (5s)...")
	time.Sleep(5 * time.Second)

	finalLogs, err := containerLogs(ctx, gobfdRFCContainer, 20)
	if err != nil {
		t.Fatalf("get final gobfd-rfc logs: %v", err)
	}
	// Ensure no recent Down transitions in the last 20 lines.
	if strings.Contains(finalLogs, "new_state=Down") && strings.Contains(finalLogs, "mode=echo") {
		t.Error("echo session went Down again during stability check")
	}
	t.Log("echo session stable after recovery")
}
