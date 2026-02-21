//go:build interop

// Package interop_test provides Go-driven interoperability tests for GoBFD
// against FRR (bfdd) and BIRD3, with comprehensive RFC 5880/5881 validation
// via tshark packet capture analysis.
//
// Run with:
//
//	go test -tags interop -v -count=1 -timeout 300s ./test/interop/
//
// Prerequisites:
//   - podman-compose -f test/interop/compose.yml up --build -d
//   - All four containers (gobfd, frr, bird3, tshark) must be running.
package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	gobfdIP  = "172.20.0.10"
	frrIP    = "172.20.0.20"
	bird3IP  = "172.20.0.30"
	scapyIP  = "172.20.0.40"
	aiobfdIP = "172.20.0.50"
	thoroIP  = "172.20.0.60"

	pollInterval = 2 * time.Second

	// scapyImage is the image name for the Scapy BFD fuzzer.
	// Built with podman build (not compose) to avoid compose's "run"
	// behavior that tears down and recreates the entire stack.
	scapyImage = "gobfd-scapy-fuzz:latest"
)

// =========================================================================
// Infrastructure helpers
// =========================================================================

func composeFile() string {
	if f := os.Getenv("INTEROP_COMPOSE_FILE"); f != "" {
		return f
	}
	return "test/interop/compose.yml"
}

func podmanCompose(ctx context.Context, args ...string) (string, error) {
	allArgs := append([]string{"-f", composeFile()}, args...)
	cmd := exec.CommandContext(ctx, "podman-compose", allArgs...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func frrVtysh(ctx context.Context, command string) (string, error) {
	return podmanCompose(ctx, "exec", "-T", "frr", "vtysh", "-c", command)
}

// frrVtyshConfig runs a sequence of vtysh commands (e.g., configure terminal,
// bfd, peer ..., shutdown) in a single vtysh session.
func frrVtyshConfig(ctx context.Context, commands ...string) (string, error) {
	args := []string{"exec", "-T", "frr", "vtysh"}
	for _, cmd := range commands {
		args = append(args, "-c", cmd)
	}
	return podmanCompose(ctx, args...)
}

func frrBFDPeerStatus(ctx context.Context) (string, error) {
	output, err := frrVtysh(ctx, "show bfd peers json")
	if err != nil {
		return "", fmt.Errorf("vtysh show bfd peers json: %w: %s", err, output)
	}

	// vtysh may emit warnings (e.g., "% Can't open configuration file
	// [/etc/frr/frr.conf]...") before the JSON array. Find the JSON array
	// by looking for "[\n" (array on its own line) to avoid false matches.
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
		if p.Peer == gobfdIP {
			return strings.ToLower(p.Status), nil
		}
	}

	return "", fmt.Errorf("peer %s not found in FRR BFD peers", gobfdIP)
}

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

// waitFRRUp waits for the FRR BFD session to reach Up state.
func waitFRRUp(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "FRR BFD session Up", timeout, func() (bool, error) {
		status, err := frrBFDPeerStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == "up", nil
	})
}

// waitBIRD3Up waits for the BIRD3 BFD session to reach Up state.
func waitBIRD3Up(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "BIRD3 BFD session Up", timeout, func() (bool, error) {
		return bird3BFDSessionUp(ctx)
	})
}

// aiobfdSessionUp checks if the GoBFD <-> aiobfd session is Up
// by looking for Up packets from aiobfd in the tshark capture.
func aiobfdSessionUp(ctx context.Context) (bool, error) {
	count, err := tsharkCount(ctx,
		"bfd && ip.src == "+aiobfdIP+" && ip.dst == "+gobfdIP+" && bfd.sta == 0x03")
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// waitAiobfdUp waits for the aiobfd BFD session to reach Up state.
func waitAiobfdUp(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "aiobfd BFD session Up", timeout, func() (bool, error) {
		return aiobfdSessionUp(ctx)
	})
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

// waitThoroUp waits for the Thoro/bfd BFD session to reach Up state.
func waitThoroUp(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	waitForCondition(t, "Thoro/bfd BFD session Up", timeout, func() (bool, error) {
		return thoroSessionUp(ctx)
	})
}

// =========================================================================
// Tshark analysis helpers
// =========================================================================

// tsharkQuery runs tshark on the captured pcapng file and returns stdout.
func tsharkQuery(ctx context.Context, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", "tshark-interop", "tshark",
		"-r", "/captures/bfd.pcapng"}, args...)
	cmd := exec.CommandContext(ctx, "podman", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tshark: %w: %s", err, stderr.String())
	}
	return stdout.String(), nil
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

// assertNoPackets fails the test if any packets match the display filter.
// Used for negative assertions (e.g., "no packets with TTL != 255").
func assertNoPackets(t *testing.T, ctx context.Context, filter, desc string) {
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
func assertHasPackets(t *testing.T, ctx context.Context, filter, desc string) {
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

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
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
	waitFRRUp(t, t.Context(), 60*time.Second)
}

// TestBIRD3Handshake verifies that the BFD three-way handshake completes
// between GoBFD and BIRD3, resulting in both sides reporting session Up.
func TestBIRD3Handshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	waitBIRD3Up(t, t.Context(), 60*time.Second)
}

// TestAiobfdHandshake verifies that the BFD three-way handshake completes
// between GoBFD and aiobfd (Python asyncio BFD daemon, RFC 5880/5881).
func TestAiobfdHandshake(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			dumpTsharkCapture(t, 50)
		}
	})
	waitAiobfdUp(t, t.Context(), 60*time.Second)
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
	waitThoroUp(t, t.Context(), 60*time.Second)
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
	waitFRRUp(t, ctx, 60*time.Second)
	waitBIRD3Up(t, ctx, 60*time.Second)
	waitAiobfdUp(t, ctx, 60*time.Second)
	waitThoroUp(t, ctx, 60*time.Second)

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
		assertNoPackets(t, ctx,
			gobfdPkts+" && bfd.version != 1",
			"all GoBFD packets must have version=1")
	})

	t.Run("RFC5880_4.1_MultipointZero", func(t *testing.T) {
		// RFC 5880 §4.1: "This bit is reserved for future
		// point-to-multipoint extensions. It MUST be zero."
		assertNoPackets(t, ctx,
			gobfdPkts+" && bfd.flags.m == 1",
			"multipoint bit must always be 0")
	})

	t.Run("RFC5880_4.1_DemandZero", func(t *testing.T) {
		// Demand mode not implemented; D bit must always be 0.
		assertNoPackets(t, ctx,
			gobfdPkts+" && bfd.flags.d == 1",
			"demand bit must be 0 (not implemented)")
	})

	t.Run("RFC5881_4_EchoIntervalZero", func(t *testing.T) {
		// RFC 5881 §4: "If a BFD implementation does not support the
		// Echo function, it MUST set Required Min Echo RX Interval to 0."
		assertNoPackets(t, ctx,
			gobfdPkts+" && bfd.required_min_echo_interval != 0",
			"echo not implemented: RequiredMinEchoRxInterval must be 0")
	})

	t.Run("RFC5880_6.8.7_MyDiscrNonZero", func(t *testing.T) {
		// RFC 5880 §6.8.7: "The transmitting system MUST set My
		// Discriminator to a unique, nonzero discriminator value."
		assertNoPackets(t, ctx,
			gobfdPkts+" && bfd.my_discriminator == 0x00000000",
			"My Discriminator must be nonzero in all packets")
	})

	t.Run("RFC5880_4.1_PacketLength", func(t *testing.T) {
		// Without authentication, BFD Control is exactly 24 bytes.
		assertNoPackets(t, ctx,
			gobfdPkts+" && bfd.message_length != 24",
			"packet length must be 24 (no auth)")
	})

	t.Run("RFC5881_5_TTL255", func(t *testing.T) {
		// RFC 5881 §5: "BFD Control packets MUST be transmitted with
		// a TTL/Hop Limit value of 255."
		assertNoPackets(t, ctx,
			gobfdPkts+" && ip.ttl != 255",
			"all single-hop packets must have TTL=255 (GTSM)")
	})

	t.Run("RFC5881_4_DstPort3784", func(t *testing.T) {
		// RFC 5881 §4: single-hop BFD uses destination port 3784.
		assertNoPackets(t, ctx,
			gobfdPkts+" && udp.dstport != 3784",
			"destination port must be 3784")
	})

	t.Run("RFC5881_4_SrcPortEphemeral", func(t *testing.T) {
		// RFC 5881 §4: "BFD Control packets MUST be transmitted with
		// a source port in the range 49152 through 65535."
		assertNoPackets(t, ctx,
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
			{"aiobfd", aiobfdIP, false},
			{"Thoro", thoroIP, false},
		}
		for _, p := range peers {
			t.Run(p.name+"_Version1", func(t *testing.T) {
				assertNoPackets(t, ctx,
					"bfd && ip.src == "+p.ip+" && bfd.version != 1",
					p.name+" packets must have version=1")
			})
			t.Run(p.name+"_TTL255", func(t *testing.T) {
				assertNoPackets(t, ctx,
					"bfd && ip.src == "+p.ip+" && ip.ttl != 255",
					p.name+" packets must have TTL=255 (GTSM)")
			})
			t.Run(p.name+"_DstPort3784", func(t *testing.T) {
				assertNoPackets(t, ctx,
					"bfd && ip.src == "+p.ip+" && udp.dstport != 3784",
					p.name+" packets must use dst port 3784")
			})
			t.Run(p.name+"_SrcPortEphemeral", func(t *testing.T) {
				if p.skipSrcPort {
					t.Skipf("%s uses a fixed source port (known RFC 5881 §4 deviation)", p.name)
				}
				assertNoPackets(t, ctx,
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
			{"FRR", frrIP}, {"BIRD3", bird3IP},
			{"aiobfd", aiobfdIP}, {"Thoro", thoroIP},
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
		assertNoPackets(t, ctx,
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
		aiobfdDiscr, err := findUpDiscr(aiobfdIP)
		if err != nil {
			t.Fatalf("aiobfd: %v", err)
		}
		thoroDiscr, err := findUpDiscr(thoroIP)
		if err != nil {
			t.Fatalf("Thoro: %v", err)
		}

		discrs := map[string]string{
			"FRR":    frrDiscr,
			"BIRD3":  birdDiscr,
			"aiobfd": aiobfdDiscr,
			"Thoro":  thoroDiscr,
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
		assertNoPackets(t, ctx,
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
	// Group E: Session independence (RFC 5880 §6.8.1)
	// Stop FRR, verify BIRD3 unaffected, measure detection time,
	// then verify recovery.
	// -----------------------------------------------------------------

	t.Run("RFC5880_6.8.1_SessionIndependence", func(t *testing.T) {
		// Stop FRR to trigger detection timeout on gobfd→FRR session.
		output, err := podmanCompose(ctx, "stop", "frr")
		if err != nil {
			t.Fatalf("stop FRR: %v: %s", err, output)
		}
		t.Cleanup(func() {
			// Always restart FRR.
			podmanCompose(ctx, "start", "frr") //nolint:errcheck
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

		// aiobfd session must also remain Up.
		aiobfdUp, err := aiobfdSessionUp(ctx)
		if err != nil {
			t.Fatalf("check aiobfd: %v", err)
		}
		if !aiobfdUp {
			t.Error("aiobfd session went Down when only FRR was stopped — sessions are not independent")
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
		assertHasPackets(t, ctx,
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
	waitFRRUp(t, ctx, 60*time.Second)

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

		// Verify aiobfd is still Up.
		aiobfdUp, err := aiobfdSessionUp(ctx)
		if err != nil {
			t.Fatalf("check aiobfd: %v", err)
		}
		if !aiobfdUp {
			t.Error("aiobfd session not Up after FRR recovery cycle")
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
		assertHasPackets(t, ctx,
			"bfd && ip.src == "+frrIP+" && bfd.sta == 0x00",
			"FRR must send AdminDown (state=0) packets after shutdown")

		// Verify GoBFD transitioned to Down with Diag=3 (Neighbor Signaled).
		assertHasPackets(t, ctx,
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
		waitFRRUp(t, ctx, 30*time.Second)

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
			{"FRR", frrIP}, {"BIRD3", bird3IP},
			{"aiobfd", aiobfdIP}, {"Thoro", thoroIP},
		}
		for _, p := range peers {
			t.Run(p.name, func(t *testing.T) {
				rows, err := tsharkFields(ctx,
					"bfd && ip.src == "+gobfdIP+" && ip.dst == "+p.ip+" && bfd.sta == 0x03",
					[]string{"frame.time_epoch"}, 200)
				if err != nil || len(rows) < 10 {
					t.Skipf("insufficient Up packets to %s for jitter analysis: %d", p.name, len(rows))
				}

				var deltas []float64
				var prev float64
				for i, row := range rows {
					ts, err := strconv.ParseFloat(strings.TrimSpace(row[0]), 64)
					if err != nil {
						t.Fatalf("parse epoch at row %d: %v", i, err)
					}
					if i > 0 {
						delta := ts - prev
						// Filter out sub-100ms deltas: these are state
						// transition artifacts (session cycles, P/F bursts)
						// not steady-state jitter.
						if delta >= 0.100 {
							deltas = append(deltas, delta)
						}
					}
					prev = ts
				}

				if len(deltas) < 5 {
					t.Skipf("insufficient steady-state deltas to %s: %d", p.name, len(deltas))
				}

				var minDelta, maxDelta float64
				minDelta = math.MaxFloat64
				for _, d := range deltas {
					if d < minDelta {
						minDelta = d
					}
					if d > maxDelta {
						maxDelta = d
					}
				}

				t.Logf("%s inter-packet timing: min=%.3fs max=%.3fs samples=%d",
					p.name, minDelta, maxDelta, len(deltas))

				// Expected: 75-100% of 300ms = 0.225-0.300s.
				// Allow slack for container scheduling: 0.150-0.400s.
				if minDelta < 0.150 {
					t.Errorf("min inter-packet interval %.3fs is too short (< 150ms)", minDelta)
				}
				if maxDelta > 0.400 {
					t.Errorf("max inter-packet interval %.3fs is too long (> 400ms)", maxDelta)
				}
			})
		}
	})
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

	waitFRRUp(t, ctx, 60*time.Second)

	output, err := podmanCompose(ctx, "stop", "frr")
	if err != nil {
		t.Fatalf("stop FRR: %v: %s", err, output)
	}

	time.Sleep(5 * time.Second)

	logs, err := podmanCompose(ctx, "logs", "gobfd")
	if err != nil {
		t.Fatalf("get gobfd logs: %v", err)
	}

	if !strings.Contains(logs, "session state changed") || !strings.Contains(logs, "new_state=Down") {
		t.Error("GoBFD did not log session Down transition after FRR stop")
		t.Logf("gobfd logs (tail):\n%s", lastNLines(logs, 30))
	}

	output, err = podmanCompose(ctx, "start", "frr")
	if err != nil {
		t.Fatalf("restart FRR: %v: %s", err, output)
	}
}

// =========================================================================
// Test 5: Graceful shutdown with AdminDown (existing, enhanced)
// Scapy Protocol Fuzzing
// =========================================================================

// TestScapyFuzzing runs the Scapy BFD fuzzer container that sends ~1000+
// crafted/invalid BFD packets to GoBFD and verifies it survives.
// Tests RFC 5880 Section 6.8.6 validation robustness.
//
// Uses podman build + podman run directly (NOT podman-compose run) because
// podman-compose's "run" subcommand tears down and recreates the entire
// compose stack, destroying frr-interop and other containers.
func TestScapyFuzzing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Clean up leftover container from a previous failed run.
	_ = exec.CommandContext(ctx, "podman", "rm", "-f", "scapy-interop").Run()

	// Build the scapy image directly.
	// Go test runs from the package directory (test/interop/),
	// so the scapy build context is relative to that.
	buildOut, err := exec.CommandContext(ctx,
		"podman", "build",
		"-t", scapyImage,
		"-f", "scapy/Containerfile",
		"scapy/",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("podman build scapy: %v\n%s", err, buildOut)
	}

	// Run on the existing compose network without disturbing other services.
	runOut, err := exec.CommandContext(ctx,
		"podman", "run", "--rm",
		"--name", "scapy-interop",
		"--network", "interop_bfdnet",
		"--ip", scapyIP,
		"--cap-add", "NET_RAW",
		"--cap-add", "NET_ADMIN",
		"-e", "GOBFD_IP="+gobfdIP,
		scapyImage,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("scapy fuzzer failed: %v\n%s", err, runOut)
	}

	t.Logf("Scapy fuzzer output:\n%s", string(runOut))

	// Verify gobfd is still running after fuzzing.
	out, err := exec.CommandContext(ctx,
		"podman", "ps", "--filter", "name=gobfd-interop",
		"--format", "{{.Names}}").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "gobfd-interop") {
		t.Fatal("gobfd crashed after Scapy fuzzing")
	}

	t.Log("GoBFD survived all Scapy fuzz packets")
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

	waitFRRUp(t, ctx, 60*time.Second)

	// Record AdminDown packet count before shutdown.
	adminDownBefore, _ := tsharkCount(ctx,
		"bfd && ip.src == "+gobfdIP+" && bfd.sta == 0x00 && bfd.diag == 0x07")

	output, err := podmanCompose(ctx, "stop", "gobfd")
	if err != nil {
		t.Fatalf("stop gobfd: %v: %s", err, output)
	}

	time.Sleep(5 * time.Second)

	// Verify AdminDown packets were sent (diag=7, state=0).
	adminDownAfter, err := tsharkCount(ctx,
		"bfd && ip.src == "+gobfdIP+" && bfd.sta == 0x00 && bfd.diag == 0x07")
	if err != nil {
		t.Logf("tshark query for AdminDown packets: %v", err)
	} else if adminDownAfter <= adminDownBefore {
		t.Error("GoBFD did not send AdminDown (state=0, diag=7) packets on SIGTERM")
	} else {
		t.Logf("GoBFD sent %d AdminDown packets on graceful shutdown",
			adminDownAfter-adminDownBefore)
	}

	// RFC 5880 §6.8.16: AdminDown must be sent to ALL peers on shutdown.
	for _, peer := range []struct {
		name string
		ip   string
	}{
		{"FRR", frrIP}, {"BIRD3", bird3IP},
		{"aiobfd", aiobfdIP}, {"Thoro", thoroIP},
	} {
		count, err := tsharkCount(ctx,
			"bfd && ip.src == "+gobfdIP+" && ip.dst == "+peer.ip+
				" && bfd.sta == 0x00 && bfd.diag == 0x07")
		if err != nil {
			t.Logf("tshark query for %s AdminDown: %v", peer.name, err)
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
