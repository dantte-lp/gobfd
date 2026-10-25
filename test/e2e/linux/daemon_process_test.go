//go:build e2e_linux

package linux_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

type daemonReceipt struct {
	Desired uint64 `json:"desired_generation"`
	Applied uint64 `json:"applied_generation"`
	Pending int    `json:"pending"`
	Failed  int    `json:"failed"`
	Stale   bool   `json:"stale"`
	Base    struct {
		InterfacePending bool `json:"interface_pending"`
		AddressPending   bool `json:"address_pending"`
		Errors           struct {
			Create int `json:"create"`
		} `json:"errors"`
	} `json:"base"`
}

type daemonObservation struct {
	Phase        string              `json:"phase"`
	Receipt      daemonReceipt       `json:"receipt"`
	ReceiptCount int                 `json:"receipt_count"`
	Rejected     int                 `json:"rejected_candidates"`
	Serving      bool                `json:"serving"`
	Sessions     map[string]uint32   `json:"sessions"`
	UDPSockets   []string            `json:"owned_udp_sockets"`
	Addresses    map[string][]string `json:"interface_addresses"`
	FDs          int                 `json:"fds"`
	Threads      int                 `json:"threads"`
}

type pendingDaemon struct {
	process                         *os.Process
	conn                            *grpc.ClientConn
	configPath, logPath, binaryHash string
	done                            chan struct{}
	state                           *os.ProcessState
	waitErr                         error
	stopped                         bool
	observations                    []daemonObservation
}

func startPendingDaemon(t *testing.T, local netip.Addr) *pendingDaemon {
	t.Helper()
	binary := os.Getenv("E2E_LINUX_BFD_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("E2E_LINUX_BFD_BINARY must name the prebuilt daemon")
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, file)
	if closeErr := file.Close(); hashErr != nil || closeErr != nil {
		t.Fatalf("daemon identity: %v %v", hashErr, closeErr)
	}
	dir := t.TempDir()
	d := &pendingDaemon{
		configPath: filepath.Join(dir, "gobfd.yml"), logPath: filepath.Join(dir, "daemon.log"),
		binaryHash: hex.EncodeToString(hash.Sum(nil)), done: make(chan struct{}),
	}
	d.writeConfig(t, "/metrics", local)
	output, err := os.OpenFile(d.logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	d.process, err = os.StartProcess(binary, []string{binary, "-config", d.configPath}, &os.ProcAttr{
		Dir: dir, Env: os.Environ(), Files: []*os.File{os.Stdin, output, output},
	})
	closeErr := output.Close()
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	go func() { d.state, d.waitErr = d.process.Wait(); close(d.done) }()
	if closeErr != nil {
		d.stop(t)
		t.Fatal(closeErr)
	}
	d.conn, err = grpc.NewClient("unix://"+filepath.Join(dir, "management.sock"),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		d.stop(t)
		t.Fatal(err)
	}
	return d
}

func (d *pendingDaemon) writeConfig(t *testing.T, metricsPath string, locals ...netip.Addr) {
	t.Helper()
	var data strings.Builder
	fmt.Fprintf(&data, "grpc:\n  socket:\n    path: %q\nmetrics:\n  path: %q\nsessions:\n",
		filepath.Join(filepath.Dir(d.configPath), "management.sock"), metricsPath)
	for i, local := range locals {
		iface := pendingLink
		if i == 0 {
			iface = "gobfdp0"
		}
		fmt.Fprintf(&data, "  - peer: %q\n    local: %q\n    interface: %q\n    type: single_hop\n",
			local.Next().String(), local.String(), iface)
	}
	candidate := d.configPath + ".candidate"
	if err := os.WriteFile(candidate, []byte(data.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(candidate, d.configPath); err != nil {
		t.Fatal(err)
	}
}

func (d *pendingDaemon) reload(t *testing.T, metricsPath string, locals ...netip.Addr) {
	t.Helper()
	d.writeConfig(t, metricsPath, locals...)
	if err := d.process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
}

func (d *pendingDaemon) await(
	t *testing.T, phase string, count int, serving bool, accept func(daemonObservation) bool,
) daemonObservation {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last daemonObservation
	var lastErr error
	for {
		select {
		case <-d.done:
			t.Fatalf("daemon exited during %s: %v %v", phase, d.state, d.waitErr)
		default:
		}
		last, lastErr = d.observe(t.Context())
		if lastErr == nil && last.ReceiptCount > 0 && last.Receipt.Desired > 0 && len(last.Sessions) == count &&
			last.Serving == serving && (accept == nil || accept(last)) {
			last.Phase = phase
			if len(last.UDPSockets) != 2*count || last.Threads > 64 || last.FDs > 32 {
				t.Fatalf("unbounded or duplicated resources: %+v", last)
			}
			d.observations = append(d.observations, last)
			return last
		}
		select {
		case <-deadline.C:
			t.Fatalf("%s not reached: snapshot=%+v error=%v", phase, last, lastErr)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-ticker.C:
		}
	}
}

func (d *pendingDaemon) observe(parent context.Context) (daemonObservation, error) {
	var result daemonObservation
	ctx, cancel := context.WithTimeout(parent, 300*time.Millisecond)
	defer cancel()
	response := new(bfdv1.ListSessionsResponse)
	if err := d.conn.Invoke(ctx, "/"+bfdv1connect.BfdServiceName+"/ListSessions",
		&bfdv1.ListSessionsRequest{}, response); err != nil {
		return result, err
	}
	result.Sessions = make(map[string]uint32)
	for _, session := range response.GetSessions() {
		if session.GetLocalDiscriminator() == 0 {
			return result, errors.New("zero discriminator")
		}
		result.Sessions[session.GetPeerAddress()] = session.GetLocalDiscriminator()
	}
	if len(result.Sessions) != len(response.GetSessions()) {
		return result, errors.New("duplicate physical session")
	}
	health, err := healthv1.NewHealthClient(d.conn).Check(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		return result, err
	}
	result.Serving = health.GetStatus() == healthv1.HealthCheckResponse_SERVING
	management, err := healthv1.NewHealthClient(d.conn).Check(ctx,
		&healthv1.HealthCheckRequest{Service: "gobfd.management"})
	if err != nil {
		return result, err
	}
	if management.GetStatus() != healthv1.HealthCheckResponse_SERVING {
		return result, errors.New("secure management health is not SERVING")
	}
	socket, err := os.Stat(filepath.Join(filepath.Dir(d.configPath), "management.sock"))
	if err != nil {
		return result, err
	}
	if socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm() != 0o660 {
		return result, fmt.Errorf("management socket mode = %v", socket.Mode())
	}
	logs, err := d.readLogs()
	if err != nil {
		return result, err
	}
	for _, line := range strings.SplitAfter(string(logs), "\n") {
		if !strings.HasSuffix(line, "\n") {
			continue
		}
		var event struct {
			daemonReceipt

			Message string `json:"msg"`
		}
		if decodeErr := json.Unmarshal([]byte(line), &event); decodeErr != nil {
			return result, fmt.Errorf("decode daemon event: %w", decodeErr)
		}
		switch event.Message {
		case "configuration reconciliation incomplete", "configuration reconciliation converged":
			result.Receipt = event.daemonReceipt
			result.ReceiptCount++
		case "invalid control-session candidate, keeping current settings":
			result.Rejected++
		}
	}
	result.UDPSockets, result.FDs, result.Threads, err = observeDaemonResources(d.process.Pid)
	if err == nil {
		result.Addresses, err = observeDaemonAddresses()
	}
	return result, err
}

func (d *pendingDaemon) readLogs() ([]byte, error) {
	file, err := os.Open(d.logPath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > 4<<20 {
		return nil, errors.New("daemon log exceeds 4 MiB bound")
	}
	return data, nil
}

func (d *pendingDaemon) stop(t *testing.T) {
	t.Helper()
	if d.stopped {
		return
	}
	d.stopped = true
	if d.conn != nil {
		if err := d.conn.Close(); err != nil {
			t.Error(err)
		}
	}
	if err := d.process.Signal(syscall.SIGTERM); err != nil {
		select {
		case <-d.done:
		default:
			t.Error(err)
		}
	}
	select {
	case <-d.done:
	case <-time.After(15 * time.Second):
		t.Error("daemon did not join after SIGTERM")
		if err := d.process.Kill(); err != nil {
			t.Error(err)
		}
		select {
		case <-d.done:
		case <-time.After(5 * time.Second):
			t.Fatal("daemon did not join after SIGKILL")
		}
	}
	if d.waitErr != nil || d.state == nil || !d.state.Success() {
		t.Errorf("daemon shutdown: %v %v", d.state, d.waitErr)
	}
}

func (d *pendingDaemon) artifacts(t *testing.T, ipv6 bool) {
	t.Helper()
	family := "ipv4"
	if ipv6 {
		family = "ipv6"
	}
	logs, err := d.readLogs()
	if err != nil {
		t.Error(err)
		return
	}
	kernel, kernelErr := os.ReadFile("/proc/sys/kernel/osrelease")
	if kernelErr != nil || len(kernel) == 0 {
		t.Errorf("kernel identity: %v", kernelErr)
	}
	joined := false
	select {
	case <-d.done:
		joined = true
	default:
	}
	exitCode := -1
	if joined && d.state != nil {
		exitCode = d.state.ExitCode()
	}
	writeJSONArtifact(t, "pending-daemon-"+family+".json", map[string]any{
		"daemon_sha256": d.binaryHash, "compiler": runtime.Version(), "kernel": strings.TrimSpace(string(kernel)),
		"pid": d.process.Pid, "joined": joined, "observations": d.observations, "exit_code": exitCode,
	})
	logPath := filepath.Join(os.Getenv("E2E_LINUX_REPORT_DIR"), "pending-daemon-"+family+".log")
	if err := os.WriteFile(logPath, logs, 0o600); err != nil {
		t.Error(err)
	}
}
