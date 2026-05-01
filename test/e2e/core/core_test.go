//go:build e2e_core

// Package core_test validates the S10.2 GoBFD-to-GoBFD daemon E2E contract.
package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	gobfdAIP     = "172.30.10.10"
	gobfdBIP     = "172.30.10.20"
	pollInterval = 500 * time.Millisecond
)

type sessionView struct {
	PeerAddress         string `json:"peer_address"`
	LocalAddress        string `json:"local_address"`
	Type                string `json:"type"`
	LocalState          string `json:"local_state"`
	RemoteState         string `json:"remote_state"`
	LocalDiagnostic     string `json:"local_diagnostic"`
	LocalDiscriminator  uint32 `json:"local_discriminator"`
	RemoteDiscriminator uint32 `json:"remote_discriminator"`
	AuthType            string `json:"auth_type"`
}

func TestCoreDaemonE2E(t *testing.T) {
	ctx := context.Background()

	t.Run("sessions reach up with static auth", func(t *testing.T) {
		waitSession(t, ctx, "gobfd-a", gobfdBIP, func(s sessionView) bool {
			return s.LocalState == "Up" &&
				s.RemoteState == "Up" &&
				s.AuthType == "SimplePassword" &&
				s.LocalDiscriminator != 0 &&
				s.RemoteDiscriminator != 0
		})
		waitSession(t, ctx, "gobfd-b", gobfdAIP, func(s sessionView) bool {
			return s.LocalState == "Up" &&
				s.RemoteState == "Up" &&
				s.AuthType == "SimplePassword" &&
				s.LocalDiscriminator != 0 &&
				s.RemoteDiscriminator != 0
		})
	})

	t.Run("cli list show and monitor return current state", func(t *testing.T) {
		sessions := listSessions(t, ctx, "gobfd-a")
		if len(sessions) != 1 {
			t.Fatalf("gobfd-a sessions = %d, want 1", len(sessions))
		}
		if sessions[0].PeerAddress != gobfdBIP || sessions[0].LocalState != "Up" {
			t.Fatalf("gobfd-a session = %+v, want peer %s Up", sessions[0], gobfdBIP)
		}

		shown := showSession(t, ctx, "gobfd-a", gobfdBIP)
		if shown.PeerAddress != gobfdBIP || shown.LocalState != "Up" {
			t.Fatalf("shown session = %+v, want peer %s Up", shown, gobfdBIP)
		}

		output, err := gobfdctlStream(ctx, "gobfd-a", 3*time.Second, "monitor", "--current")
		if err == nil {
			t.Fatal("monitor --current exited before timeout; want streaming command timeout")
		}
		if !strings.Contains(output, gobfdBIP) || !strings.Contains(output, "Up") {
			t.Fatalf("monitor output = %q, want current Up event for %s", output, gobfdBIP)
		}
	})

	t.Run("metrics endpoints expose session metrics", func(t *testing.T) {
		assertMetrics(t, ctx, "gobfd-a")
		assertMetrics(t, ctx, "gobfd-b")
	})

	t.Run("sighup reload keeps session up and records reload", func(t *testing.T) {
		configPath := os.Getenv("E2E_CORE_A_CONFIG_IN_CONTAINER")
		if configPath == "" {
			t.Fatal("E2E_CORE_A_CONFIG_IN_CONTAINER is not set")
		}
		writeCoreConfig(t, configPath, gobfdAIP, gobfdBIP, "info")
		if _, err := podmanCompose(ctx, "kill", "-s", "HUP", "gobfd-a"); err != nil {
			t.Fatalf("send SIGHUP: %v", err)
		}
		waitSession(t, ctx, "gobfd-a", gobfdBIP, func(s sessionView) bool {
			return s.LocalState == "Up" && s.RemoteState == "Up"
		})
		logs, err := podmanCompose(ctx, "logs", "gobfd-a")
		if err != nil {
			t.Fatalf("read gobfd-a logs: %v", err)
		}
		if !strings.Contains(logs, "configuration reloaded") {
			t.Fatalf("gobfd-a logs do not contain reload event: %s", logs)
		}
	})

	t.Run("packet capture records up and graceful admindown packets", func(t *testing.T) {
		assertPacketCount(t, ctx,
			"bfd && ip.src == "+gobfdAIP+" && ip.dst == "+gobfdBIP+" && bfd.sta == 0x03",
			"Up packets from gobfd-a to gobfd-b",
		)
		if _, err := podmanCompose(ctx, "stop", "-t", "10", "gobfd-a"); err != nil {
			t.Fatalf("stop gobfd-a: %v", err)
		}
		time.Sleep(2 * time.Second)
		assertPacketCount(t, ctx,
			"bfd && ip.src == "+gobfdAIP+" && ip.dst == "+gobfdBIP+" && bfd.sta == 0x00 && bfd.diag == 0x07",
			"AdminDown packets from gobfd-a to gobfd-b",
		)
	})
}

func listSessions(t *testing.T, ctx context.Context, service string) []sessionView {
	t.Helper()
	out, err := gobfdctl(ctx, service, "session", "list")
	if err != nil {
		t.Fatalf("%s session list: %v: %s", service, err, out)
	}
	var sessions []sessionView
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("decode %s session list: %v: %s", service, err, out)
	}
	return sessions
}

func showSession(t *testing.T, ctx context.Context, service, peer string) sessionView {
	t.Helper()
	out, err := gobfdctl(ctx, service, "session", "show", peer)
	if err != nil {
		t.Fatalf("%s session show %s: %v: %s", service, peer, err, out)
	}
	var session sessionView
	if err := json.Unmarshal([]byte(out), &session); err != nil {
		t.Fatalf("decode %s session show: %v: %s", service, err, out)
	}
	return session
}

func waitSession(t *testing.T, ctx context.Context, service, peer string, ok func(sessionView) bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last []sessionView
	for time.Now().Before(deadline) {
		last = listSessions(t, ctx, service)
		for _, session := range last {
			if session.PeerAddress == peer && ok(session) {
				return
			}
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("%s session with peer %s did not reach expected state; last=%+v", service, peer, last)
}

func assertMetrics(t *testing.T, ctx context.Context, service string) {
	t.Helper()
	addr := mappedPort(t, ctx, service, "9100")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/metrics", nil)
	if err != nil {
		t.Fatalf("build metrics request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s metrics: %v", service, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s metrics: %v", service, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s metrics status = %d, body=%s", service, resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("gobfd_bfd_sessions")) {
		t.Fatalf("%s metrics missing gobfd_bfd_sessions: %s", service, body)
	}
}

func assertPacketCount(t *testing.T, ctx context.Context, filter, desc string) {
	t.Helper()
	out, err := tshark(ctx, "-r", "/captures/bfd.pcapng", "-Y", filter, "-T", "fields", "-e", "frame.number")
	if err != nil {
		t.Fatalf("%s: tshark failed: %v: %s", desc, err, out)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	if count == 0 {
		t.Fatalf("%s: no packets found with filter %q", desc, filter)
	}
}

func gobfdctl(ctx context.Context, service string, args ...string) (string, error) {
	addr := serviceGRPCAddr(service)
	all := []string{"--addr", addr, "--format", "json"}
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, gobfdctlBinary(), all...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func gobfdctlStream(ctx context.Context, service string, timeout time.Duration, args ...string) (string, error) {
	timeoutSeconds := fmt.Sprintf("%ds", int(timeout.Seconds()))
	all := []string{timeoutSeconds, gobfdctlBinary(), "--addr", serviceGRPCAddr(service), "--format", "json"}
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, "timeout", all...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func gobfdctlBinary() string {
	if binary := os.Getenv("E2E_CORE_GOBFDCTL"); binary != "" {
		return binary
	}
	return "/bin/gobfdctl"
}

func serviceGRPCAddr(service string) string {
	switch service {
	case "gobfd-a":
		if port := os.Getenv("E2E_CORE_A_GRPC_PORT"); port != "" {
			return net.JoinHostPort("127.0.0.1", port)
		}
	case "gobfd-b":
		if port := os.Getenv("E2E_CORE_B_GRPC_PORT"); port != "" {
			return net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "127.0.0.1:50051"
}

func tshark(ctx context.Context, args ...string) (string, error) {
	all := append([]string{"run", "--rm", "--no-deps", "analyzer"}, args...)
	return podmanComposeWithProfiles(ctx, []string{"tools"}, all...)
}

func mappedPort(t *testing.T, ctx context.Context, service, port string) string {
	t.Helper()
	out, err := podmanCompose(ctx, "port", service, port)
	if err != nil {
		t.Fatalf("podman-compose port %s %s: %v: %s", service, port, err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatalf("podman-compose port %s %s returned empty output", service, port)
	}
	_, mapped, err := net.SplitHostPort(strings.TrimSpace(lines[len(lines)-1]))
	if err != nil && strings.Contains(err.Error(), "missing port in address") {
		mapped = strings.TrimSpace(lines[len(lines)-1])
	} else if err != nil {
		t.Fatalf("parse mapped port %q: %v", out, err)
	}
	return net.JoinHostPort("127.0.0.1", mapped)
}

func podmanCompose(ctx context.Context, args ...string) (string, error) {
	return podmanComposeWithProfiles(ctx, nil, args...)
}

func podmanComposeWithProfiles(ctx context.Context, profiles []string, args ...string) (string, error) {
	project := os.Getenv("E2E_CORE_PROJECT")
	if project == "" {
		project = "gobfd-e2e-core"
	}
	composeFile := os.Getenv("E2E_CORE_COMPOSE_FILE")
	if composeFile == "" {
		composeFile = "test/e2e/core/compose.yml"
	}
	all := []string{"-p", project, "-f", composeFile}
	for _, profile := range profiles {
		all = append(all, "--profile", profile)
	}
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, "podman-compose", all...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func writeCoreConfig(t *testing.T, path, localIP, peerIP, logLevel string) {
	t.Helper()
	content := fmt.Sprintf(`grpc:
  addr: ":50051"

metrics:
  addr: ":9100"
  path: "/metrics"

log:
  level: "%s"
  format: "text"

bfd:
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3

sessions:
  - peer: "%s"
    local: "%s"
    type: single_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
    auth:
      type: simple_password
      key_id: 7
      secret: "s10-core-auth"
`, logLevel, peerIP, localIP)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}
