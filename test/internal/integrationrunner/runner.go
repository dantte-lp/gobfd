// Package integrationrunner owns repository integration demo orchestration.
package integrationrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec" //nolint:depguard // Fixed integration-tool argument vectors are the boundary owned by this package.
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	commandTimeout  = 2 * time.Minute
	buildTimeout    = 10 * time.Minute
	cleanupTimeout  = 2 * time.Minute
	httpTimeout     = 5 * time.Second
	maxHTTPBodySize = 1 << 20
	kubeconfigPath  = "/etc/rancher/k3s/k3s.yaml"
	composeVersion  = "5.5.0"
)

var (
	errUsage          = errors.New("invalid integrationctl usage")
	errEmptyPodName   = errors.New("kubectl returned an empty pod name")
	errRootRequired   = errors.New("root privileges are required")
	errK3sNotReady    = errors.New("K3s cluster did not become ready")
	errHTTPStatus     = errors.New("unexpected HTTP status")
	errHTTPBodyLarge  = errors.New("HTTP response body exceeds limit")
	errEmptyArgv      = errors.New("empty command argument vector")
	errComposeVersion = errors.New("unexpected Docker Compose provider version")
)

// ExitError preserves the exit status of a failed external command.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

type runner struct {
	root   string
	stdout io.Writer
	stderr io.Writer
	http   *http.Client
}

// Run executes one integration demo or Kubernetes lifecycle action.
func Run(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return usage()
	}
	r := &runner{
		root: root, stdout: stdout, stderr: stderr,
		http: &http.Client{Timeout: httpTimeout},
	}
	switch args[0] {
	case "haproxy":
		return r.composeLifecycle(ctx, "haproxy-health", r.haproxy)
	case "observability":
		return r.composeLifecycle(ctx, "observability", r.observability)
	case "exabgp-anycast":
		return r.composeLifecycle(ctx, "exabgp-anycast", r.exabgpAnycast)
	case "k8s":
		return r.kubernetes(ctx)
	case "k8s-up":
		return r.kubernetesUp(ctx)
	case "k8s-down":
		return r.kubernetesDown(ctx)
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("%w: usage: integrationctl {haproxy|observability|exabgp-anycast|k8s|k8s-up|k8s-down}", errUsage)
}

func (r *runner) composeLifecycle(
	ctx context.Context,
	scenario string,
	operation func(context.Context) error,
) (runErr error) {
	composeFile := filepath.Join(r.root, "deployments", "integrations", scenario, "compose.yml")
	compose := func(args ...string) []string {
		return append([]string{"podman", "compose", "-f", composeFile}, args...)
	}
	composeEnv := composeEnvironment()
	version, err := r.outputArgvEnv(ctx, commandTimeout, composeEnv, compose("version", "--short"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(version) != composeVersion {
		return fmt.Errorf("%w: got %q, want %q", errComposeVersion, strings.TrimSpace(version), composeVersion)
	}
	if err := r.commandArgvEnv(
		ctx, commandTimeout, io.Discard, r.stderr, composeEnv, compose("config"),
	); err != nil {
		return fmt.Errorf("render %s Compose topology: %w", scenario, err)
	}

	r.info("Building and starting %s topology", scenario)
	if err := r.commandArgvEnv(
		ctx, buildTimeout, r.stdout, r.stderr, composeEnv, compose("up", "--build", "-d"),
	); err != nil {
		return err
	}
	defer func() {
		r.info("Cleaning up %s topology", scenario)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		runErr = errors.Join(runErr, r.commandArgvEnv(
			cleanupCtx, cleanupTimeout, r.stdout, r.stderr, composeEnv,
			compose("down", "--volumes", "--remove-orphans"),
		))
	}()

	return operation(ctx)
}

func (r *runner) haproxy(ctx context.Context) error {
	if err := sleep(ctx, 10*time.Second); err != nil {
		return err
	}
	r.info("Checking HAProxy traffic")
	if body, err := r.httpGet(ctx, "http://localhost:8080"); err != nil || !bytes.Contains(body, []byte("nginx")) {
		r.warn("HAProxy is not ready yet")
	} else {
		r.info("HAProxy is serving traffic from backends")
	}

	r.info("Checking BFD sessions")
	if err := r.tryCommands(ctx,
		[]string{"podman", "exec", "gobfd-monitor", "gobfdctl", "sessions"},
		[]string{"podman", "exec", "gobfd-monitor", "/bin/gobfdctl", "sessions"},
	); err != nil {
		r.warn("Could not query BFD sessions: %v", err)
	}
	r.capture(ctx, "tshark-haproxy", 10)

	r.info("Pausing backend1")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "pause", "backend1"); err != nil {
		return err
	}
	if err := sleep(ctx, 5*time.Second); err != nil {
		return err
	}
	r.requests(ctx, 3)

	r.info("Unpausing backend1")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "unpause", "backend1"); err != nil {
		return err
	}
	if err := sleep(ctx, 8*time.Second); err != nil {
		return err
	}
	r.requests(ctx, 3)
	r.capture(ctx, "tshark-haproxy", 0)
	r.info("HAProxy demo complete")
	return nil
}

func (r *runner) observability(ctx context.Context) error {
	if err := sleep(ctx, 5*time.Second); err != nil {
		return err
	}
	r.info("Waiting for a healthy Prometheus target")
	healthy := false
	for range 20 {
		body, err := r.httpGet(ctx, "http://localhost:9090/api/v1/targets")
		if err == nil && bytes.Contains(body, []byte(`"health":"up"`)) {
			healthy = true
			break
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	if healthy {
		r.info("Prometheus is scraping GoBFD metrics")
	} else {
		r.warn("Prometheus target is not healthy yet")
	}
	r.printJSONEndpoint(ctx, "http://localhost:9090/api/v1/query?query=gobfd_bfd_sessions")
	r.capture(ctx, "tshark-observability", 5)

	r.info("Stopping FRR to trigger BFDSessionDownTransition")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "stop", "frr-observability"); err != nil {
		return err
	}
	if err := sleep(ctx, 30*time.Second); err != nil {
		return err
	}
	r.printJSONEndpoint(ctx, "http://localhost:9090/api/v1/alerts")

	r.info("Starting FRR for recovery")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "start", "frr-observability"); err != nil {
		return err
	}
	if err := sleep(ctx, 15*time.Second); err != nil {
		return err
	}
	r.printJSONEndpoint(ctx, "http://localhost:9090/api/v1/alerts")
	r.capture(ctx, "tshark-observability", 0)
	r.info("Grafana dashboard: http://localhost:3000 (admin/admin)")
	r.info("Prometheus: http://localhost:9090")
	return nil
}

func (r *runner) exabgpAnycast(ctx context.Context) error {
	r.info("Waiting for BGP route 198.51.100.1/32")
	announced := false
	for range 30 {
		output, err := r.output(ctx, commandTimeout, "podman", "exec", "gobgp-anycast", "gobgp", "global", "rib")
		if err == nil && strings.Contains(output, "198.51.100.1/32") {
			announced = true
			break
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	if announced {
		r.info("Route 198.51.100.1/32 announced")
	} else {
		r.warn("Route is not announced yet")
		r.bestEffort(ctx, "podman", "exec", "gobgp-anycast", "gobgp", "neighbor")
	}
	r.bestEffort(ctx, "podman", "exec", "gobgp-anycast", "gobgp", "global", "rib")
	r.capture(ctx, "tshark-anycast", 5)

	r.info("Pausing gobfd-anycast")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "pause", "gobfd-anycast"); err != nil {
		return err
	}
	if err := sleep(ctx, 8*time.Second); err != nil {
		return err
	}
	output, routeErr := r.output(ctx, commandTimeout, "podman", "exec", "gobgp-anycast", "gobgp", "global", "rib")
	switch {
	case routeErr != nil:
		r.warn("Could not query route withdrawal: %v", routeErr)
	case strings.Contains(output, "198.51.100.1/32"):
		r.warn("Route is still present")
	default:
		r.info("Route withdrawn; anycast failover succeeded")
	}

	r.info("Unpausing gobfd-anycast")
	if commandErr := r.command(
		ctx, commandTimeout, r.stdout, r.stderr, "podman", "unpause", "gobfd-anycast",
	); commandErr != nil {
		return commandErr
	}
	if waitErr := sleep(ctx, 15*time.Second); waitErr != nil {
		return waitErr
	}
	output, routeErr = r.output(ctx, commandTimeout, "podman", "exec", "gobgp-anycast", "gobgp", "global", "rib")
	if routeErr == nil && strings.Contains(output, "198.51.100.1/32") {
		r.info("Route restored; recovery succeeded")
	} else {
		r.warn("Route is not restored yet")
	}
	r.capture(ctx, "tshark-anycast", 0)
	return nil
}

func (r *runner) kubernetes(ctx context.Context) error {
	if err := r.kubernetesUp(ctx); err != nil {
		return err
	}
	if err := r.kubectl(ctx, "-n", "gobfd", "rollout", "status", "daemonset/gobfd", "--timeout=120s"); err != nil {
		return err
	}
	if err := r.kubectl(ctx, "-n", "gobfd", "get", "pods", "-o", "wide"); err != nil {
		return err
	}
	pod, err := r.outputKube(
		ctx, "-n", "gobfd", "get", "pods", "-l", "app=gobfd",
		"-o", "jsonpath={.items[0].metadata.name}",
	)
	if err != nil {
		return err
	}
	pod = strings.TrimSpace(pod)
	if pod == "" {
		return fmt.Errorf("resolve GoBFD pod: %w", errEmptyPodName)
	}
	r.info("GoBFD pod: %s", pod)
	if sessionErr := r.kubectl(
		ctx, "-n", "gobfd", "exec", pod, "-c", "gobfd", "--", "/bin/gobfdctl", "sessions",
	); sessionErr != nil {
		r.warn("No sessions are configured yet: %v", sessionErr)
	}
	if gobgpErr := r.kubectl(
		ctx, "-n", "gobfd", "exec", pod, "-c", "gobgp", "--", "gobgp", "global",
	); gobgpErr != nil {
		r.warn("GoBGP is not ready yet: %v", gobgpErr)
	}
	metrics, err := r.outputKube(
		ctx, "-n", "gobfd", "exec", pod, "-c", "gobfd", "--",
		"wget", "-qO-", "http://127.0.0.1:9100/metrics",
	)
	if err != nil {
		r.warn("Metrics endpoint is not responding: %v", err)
	} else {
		written := 0
		for line := range strings.Lines(metrics) {
			if strings.HasPrefix(line, "gobfd_bfd_") && written < 10 {
				fmt.Fprint(r.stdout, line)
				written++
			}
		}
	}
	if captureErr := r.kubectl(
		ctx, "-n", "gobfd", "exec", pod, "-c", "gobfd", "--",
		"tshark", "-i", "any", "-c", "10", "-Y", "bfd",
	); captureErr != nil {
		r.info("tshark is unavailable in the GoBFD image; use host capture tooling")
	}
	r.info("Kubernetes demo complete; run make int-k8s-down to delete only namespace gobfd")
	return nil
}

func (r *runner) kubernetesUp(ctx context.Context) error {
	if err := r.kubernetesSetup(ctx); err != nil {
		return err
	}
	manifests := filepath.Join(r.root, "deployments", "integrations", "kubernetes", "manifests")
	return r.kubectl(ctx, "apply", "-f", manifests)
}

func (r *runner) kubernetesSetup(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("access K3s kubeconfig and containerd: %w", errRootRequired)
	}
	if _, err := exec.LookPath("k3s"); err != nil {
		return fmt.Errorf("k3s is not installed; provision the host cluster outside this repository: %w", err)
	}
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return fmt.Errorf("inspect K3s kubeconfig %s: %w", kubeconfigPath, err)
	}
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "k3s", "--version"); err != nil {
		return err
	}
	r.info("Waiting for the existing K3s cluster")
	ready := false
	for range 60 {
		if _, err := r.outputKube(ctx, "get", "nodes"); err == nil {
			ready = true
			break
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	if !ready {
		return fmt.Errorf("%w within 60 seconds", errK3sNotReady)
	}
	if err := r.kubectl(ctx, "get", "nodes", "-o", "wide"); err != nil {
		return err
	}

	r.info("Building gobfd:local")
	containerfile := filepath.Join(r.root, "deployments", "docker", "Containerfile")
	if err := r.command(ctx, buildTimeout, r.stdout, r.stderr,
		"podman", "build", "-f", containerfile, "-t", "gobfd:local", r.root,
	); err != nil {
		return err
	}
	return r.importKubernetesImage(ctx)
}

func (r *runner) importKubernetesImage(ctx context.Context) error {
	archive, err := os.CreateTemp("/var/tmp", "gobfd-local-*.tar")
	if err != nil {
		return fmt.Errorf("create temporary image archive: %w", err)
	}
	archivePath := archive.Name()
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close temporary image archive: %w", err)
	}
	defer func() {
		if removeErr := os.Remove(archivePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			r.warn("Could not remove temporary image archive %s: %v", archivePath, removeErr)
		}
	}()
	if err := r.command(
		ctx, buildTimeout, r.stdout, r.stderr,
		"podman", "save", "--output", archivePath, "gobfd:local",
	); err != nil {
		return err
	}
	if err := r.command(ctx, buildTimeout, r.stdout, r.stderr, "k3s", "ctr", "images", "import", archivePath); err != nil {
		return err
	}
	r.info("Imported gobfd:local into the existing K3s image store")
	return nil
}

func (r *runner) kubernetesDown(ctx context.Context) error {
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return fmt.Errorf("inspect K3s kubeconfig %s: %w", kubeconfigPath, err)
	}
	namespace, err := r.outputKube(ctx, "get", "namespace", "gobfd", "--ignore-not-found", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(namespace) == "" {
		r.info("Namespace gobfd does not exist")
		return nil
	}
	r.info("Deleting namespace gobfd; the host K3s cluster is preserved")
	return r.kubectl(ctx, "delete", "namespace", "gobfd", "--timeout=30s")
}

func (r *runner) requests(ctx context.Context, count int) {
	for i := 1; i <= count; i++ {
		if _, err := r.httpGet(ctx, "http://localhost:8080"); err != nil {
			r.warn("Request %d failed: %v", i, err)
		} else {
			r.info("Request %d: OK", i)
		}
	}
}

func (r *runner) capture(ctx context.Context, container string, count int) {
	args := []string{"exec", container, "tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd"}
	if count > 0 {
		args = append(args, "-c", strconv.Itoa(count))
	}
	args = append(args,
		"-T", "fields", "-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst",
		"-e", "bfd.version", "-e", "bfd.diag", "-e", "bfd.sta", "-e", "bfd.flags",
		"-E", "header=y", "-E", "separator=,",
	)
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", args...); err != nil {
		r.warn("tshark capture is not available: %v", err)
	}
}

func (r *runner) printJSONEndpoint(ctx context.Context, endpoint string) {
	body, err := r.httpGet(ctx, endpoint)
	if err != nil {
		r.warn("Could not query %s: %v", endpoint, err)
		return
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, body, "", "  "); err != nil {
		fmt.Fprintln(r.stdout, string(body))
		return
	}
	formatted.WriteByte('\n')
	if _, err := formatted.WriteTo(r.stdout); err != nil {
		r.warn("Could not write formatted JSON: %v", err)
	}
}

func (r *runner) httpGet(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET %s request: %w", endpoint, err)
	}
	response, err := r.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: %w: %s", endpoint, errHTTPStatus, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read GET %s response: %w", endpoint, err)
	}
	if len(body) > maxHTTPBodySize {
		return nil, fmt.Errorf("GET %s: %w: %d bytes", endpoint, errHTTPBodyLarge, maxHTTPBodySize)
	}
	return body, nil
}

func (r *runner) tryCommands(ctx context.Context, commands ...[]string) error {
	var lastErr error
	for _, argv := range commands {
		err := r.commandArgv(ctx, commandTimeout, r.stdout, r.stderr, argv)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

func (r *runner) bestEffort(ctx context.Context, name string, args ...string) {
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, name, args...); err != nil {
		r.warn("%s failed: %v", name, err)
	}
}

func (r *runner) kubectl(ctx context.Context, args ...string) error {
	return r.commandEnv(
		ctx, commandTimeout, r.stdout, r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath}, "kubectl", args...,
	)
}

func (r *runner) outputKube(ctx context.Context, args ...string) (string, error) {
	return r.outputEnv(ctx, commandTimeout, []string{"KUBECONFIG=" + kubeconfigPath}, "kubectl", args...)
}

func (r *runner) output(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return r.outputEnv(ctx, timeout, nil, name, args...)
}

func (r *runner) outputEnv(
	ctx context.Context,
	timeout time.Duration,
	environment []string,
	name string,
	args ...string,
) (string, error) {
	var output bytes.Buffer
	err := r.commandEnv(ctx, timeout, &output, &output, environment, name, args...)
	return output.String(), err
}

func (r *runner) command(
	ctx context.Context,
	timeout time.Duration,
	stdout, stderr io.Writer,
	name string,
	args ...string,
) error {
	return r.commandEnv(ctx, timeout, stdout, stderr, nil, name, args...)
}

func (r *runner) commandArgv(
	ctx context.Context,
	timeout time.Duration,
	stdout, stderr io.Writer,
	argv []string,
) error {
	if len(argv) == 0 {
		return fmt.Errorf("run command: %w", errEmptyArgv)
	}
	return r.command(ctx, timeout, stdout, stderr, argv[0], argv[1:]...)
}

func (r *runner) commandArgvEnv(
	ctx context.Context,
	timeout time.Duration,
	stdout, stderr io.Writer,
	environment, argv []string,
) error {
	if len(argv) == 0 {
		return fmt.Errorf("run command: %w", errEmptyArgv)
	}
	return r.commandEnv(ctx, timeout, stdout, stderr, environment, argv[0], argv[1:]...)
}

func (r *runner) outputArgvEnv(
	ctx context.Context,
	timeout time.Duration,
	environment, argv []string,
) (string, error) {
	var output bytes.Buffer
	err := r.commandArgvEnv(ctx, timeout, &output, &output, environment, argv)
	return output.String(), err
}

func (r *runner) commandEnv(
	ctx context.Context,
	timeout time.Duration,
	stdout, stderr io.Writer,
	environment []string,
	name string,
	args ...string,
) error {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), environment...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return &ExitError{Code: exitErr.ExitCode(), Err: fmt.Errorf("run %s: %w", name, err)}
		}
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}

//nolint:goprintffuncname // The name matches the human-readable integration log level.
func (r *runner) info(format string, args ...any) {
	fmt.Fprintf(r.stdout, "[INFO] "+format+"\n", args...)
}

//nolint:goprintffuncname // The name matches the human-readable integration log level.
func (r *runner) warn(format string, args ...any) {
	fmt.Fprintf(r.stderr, "[WARN] "+format+"\n", args...)
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait %s: %w", duration, ctx.Err())
	case <-timer.C:
		return nil
	}
}

func composeEnvironment() []string {
	provider := os.Getenv("PODMAN_COMPOSE_PROVIDER")
	if provider == "" {
		provider = "docker-compose"
	}
	return []string{
		"PODMAN_COMPOSE_PROVIDER=" + provider,
		"PODMAN_COMPOSE_WARNING_LOGS=false",
		"DOCKER_BUILDKIT=0",
	}
}
