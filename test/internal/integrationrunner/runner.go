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

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const (
	commandTimeout      = 2 * time.Minute
	buildTimeout        = 10 * time.Minute
	cleanupTimeout      = 2 * time.Minute
	httpTimeout         = 5 * time.Second
	maxHTTPBodySize     = 1 << 20
	maxImageIDOutput    = 128
	importReceiptSchema = 1
	kubeconfigPath      = "/etc/rancher/k3s/k3s.yaml"
	haproxyProject      = "gobfd-haproxy-health"
)

var (
	errUsage                     = errors.New("invalid integrationctl usage")
	errRootRequired              = errors.New("root privileges are required")
	errK3sNotReady               = errors.New("K3s cluster did not become ready")
	errHTTPStatus                = errors.New("unexpected HTTP status")
	errHTTPBodyLarge             = errors.New("HTTP response body exceeds limit")
	errHAProxyResponse           = errors.New("HAProxy response does not identify nginx")
	errKubernetesArchiveIdentity = errors.New("temporary Kubernetes image archive identity changed")
	errKubernetesImageIdentity   = errors.New("invalid Kubernetes source image identity")
	errKubernetesOutputLimit     = errors.New("kubernetes command output exceeds limit")
)

type kubernetesImportReceipt struct {
	Schema         int    `json:"schema"`
	Image          string `json:"image"`
	SourceID       string `json:"source_id"`
	ManifestDigest string `json:"manifest_digest"`
	ArchiveDigest  string `json:"archive_digest"`
	ServerUUID     string `json:"server_uuid"`
}

// ExitError preserves the exit status of a failed external command.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

type runner struct {
	root         string
	stdout       io.Writer
	stderr       io.Writer
	http         *http.Client
	hostIdentity func() (string, string, error)
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
		controller, err := interopproject.NewProject(root, haproxyProject, "haproxy-health", stdout, stderr)
		if err != nil {
			return fmt.Errorf("create HAProxy project controller: %w", err)
		}
		runErr := controller.Lifecycle(ctx, r.haproxy)
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			return &ExitError{Code: exitErr.ExitCode(), Err: runErr}
		}
		if runErr != nil {
			return fmt.Errorf("HAProxy lifecycle: %w", runErr)
		}
		return nil
	case "observability":
		controller, err := interopproject.NewProject(root, "gobfd-observability", "observability", stdout, stderr)
		if err != nil {
			return fmt.Errorf("create observability controller: %w", err)
		}
		runErr := controller.Lifecycle(ctx, r.observability)
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			return &ExitError{Code: exitErr.ExitCode(), Err: runErr}
		}
		if runErr != nil {
			return fmt.Errorf("observability lifecycle: %w", runErr)
		}
		return nil
	case "exabgp-anycast":
		return r.exabgpAnycastLifecycle(ctx)
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

func (r *runner) haproxy(ctx context.Context, controller *interopproject.Controller) (runErr error) {
	if err := sleep(ctx, 10*time.Second); err != nil {
		return err
	}
	r.info("Checking HAProxy traffic")
	if err := r.requests(ctx, 1); err != nil {
		return fmt.Errorf("check required HAProxy traffic: %w", err)
	}

	r.info("Checking BFD sessions")
	monitorID, monitorErr := controller.ContainerID(ctx, "gobfd-monitor")
	if monitorErr != nil {
		return fmt.Errorf("resolve owned HAProxy monitor: %w", monitorErr)
	}
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr,
		"podman", "exec", monitorID, "/bin/gobfdctl", "--addr", "127.0.0.1:50052", "session", "list",
	); err != nil {
		return fmt.Errorf("query required HAProxy BFD sessions: %w", err)
	}
	r.captureHAProxy(ctx, controller, 10)
	sidecarID, sidecarErr := controller.ContainerID(ctx, "backend1-bfd")
	if sidecarErr != nil {
		return fmt.Errorf("resolve owned HAProxy BFD sidecar: %w", sidecarErr)
	}

	restoreSidecar := true
	defer func() {
		if restoreSidecar {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			defer cancel()
			runErr = errors.Join(runErr, r.command(
				cleanupCtx, cleanupTimeout, r.stdout, r.stderr, "podman", "unpause", sidecarID,
			))
		}
	}()
	r.info("Pausing backend1 BFD sidecar")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "pause", sidecarID); err != nil {
		return err
	}
	if err := sleep(ctx, 5*time.Second); err != nil {
		return err
	}
	if err := r.requests(ctx, 3); err != nil {
		return fmt.Errorf("check HAProxy traffic during BFD failure: %w", err)
	}

	r.info("Unpausing backend1 BFD sidecar")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "unpause", sidecarID); err != nil {
		return err
	}
	restoreSidecar = false
	if err := sleep(ctx, 8*time.Second); err != nil {
		return err
	}
	if err := r.requests(ctx, 3); err != nil {
		return fmt.Errorf("check HAProxy traffic after BFD recovery: %w", err)
	}
	r.captureHAProxy(ctx, controller, 0)
	r.info("HAProxy demo complete")
	return nil
}

func (r *runner) captureHAProxy(ctx context.Context, controller *interopproject.Controller, count int) {
	containerID, err := controller.ContainerID(ctx, "tshark-haproxy")
	if err != nil {
		r.warn("tshark capture is not available: %v", err)
		return
	}
	r.capture(ctx, containerID, count)
}

func (r *runner) observability(ctx context.Context, controller *interopproject.Controller) error {
	frrID, err := controller.ContainerID(ctx, "frr-observability")
	if err != nil {
		return fmt.Errorf("resolve owned observability FRR: %w", err)
	}
	captureID, err := controller.ContainerID(ctx, "tshark-observability")
	if err != nil {
		return fmt.Errorf("resolve owned observability capture: %w", err)
	}
	if waitErr := sleep(ctx, 5*time.Second); waitErr != nil {
		return waitErr
	}
	r.info("Waiting for a healthy Prometheus target")
	healthy := false
	for range 20 {
		body, getErr := r.httpGet(ctx, "http://localhost:9090/api/v1/targets")
		if getErr == nil && bytes.Contains(body, []byte(`"health":"up"`)) {
			healthy = true
			break
		}
		if retryErr := sleep(ctx, time.Second); retryErr != nil {
			return retryErr
		}
	}
	if healthy {
		r.info("Prometheus is scraping GoBFD metrics")
	} else {
		r.warn("Prometheus target is not healthy yet")
	}
	r.printJSONEndpoint(ctx, "http://localhost:9090/api/v1/query?query=gobfd_bfd_sessions")
	r.capture(ctx, captureID, 5)

	r.info("Stopping FRR to trigger BFDSessionDownTransition")
	if stopErr := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "stop", frrID); stopErr != nil {
		return stopErr
	}
	if downErr := sleep(ctx, 30*time.Second); downErr != nil {
		return downErr
	}
	r.printJSONEndpoint(ctx, "http://localhost:9090/api/v1/alerts")

	r.info("Starting FRR for recovery")
	if startErr := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "start", frrID); startErr != nil {
		return startErr
	}
	if recErr := sleep(ctx, 15*time.Second); recErr != nil {
		return recErr
	}
	r.printJSONEndpoint(ctx, "http://localhost:9090/api/v1/alerts")
	r.capture(ctx, captureID, 0)
	r.info("Grafana dashboard: http://localhost:3000 (admin/admin)")
	r.info("Prometheus: http://localhost:9090")
	return nil
}

func (r *runner) kubernetes(ctx context.Context) error {
	if err := r.kubernetesUp(ctx); err != nil {
		return err
	}
	if err := r.kubectl(
		ctx, "-n", kubernetesNamespaceName, "rollout", "status", "daemonset/gobfd", "--timeout=120s",
	); err != nil {
		return err
	}
	if err := r.kubectl(ctx, "-n", kubernetesNamespaceName, "get", "pods", "-o", "wide"); err != nil {
		return err
	}
	localNode, _, nodeErr := r.kubernetesLocalNode(ctx)
	if nodeErr != nil {
		return fmt.Errorf("resolve local Kubernetes node for pod checks: %w", nodeErr)
	}
	pod, podErr := r.selectKubernetesLocalPod(ctx, localNode.Name)
	if podErr != nil {
		return fmt.Errorf("resolve active Ready GoBFD pod on %s: %w", localNode.Name, podErr)
	}
	r.info("GoBFD pod: %s", pod)
	if err := r.kubernetesPodChecks(ctx, pod); err != nil {
		return err
	}
	if captureErr := r.kubectl(
		ctx, "-n", kubernetesNamespaceName, "exec", pod, "-c", kubernetesNamespaceName, "--",
		"tshark", "-i", "any", "-c", "10", "-Y", "bfd",
	); captureErr != nil {
		// fail-silent-ok: capture tooling is optional; packet acceptance is a separate gate.
		r.info("tshark is unavailable in the GoBFD image; use host capture tooling")
	}
	r.info("Kubernetes demo complete; run make int-k8s-down to delete only namespace gobfd")
	return nil
}

func (r *runner) kubernetesUp(ctx context.Context) error {
	if err := r.kubernetesSetup(ctx); err != nil {
		return err
	}
	return r.kubernetesBuildAndApply(ctx)
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
	readyCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		if _, err := r.kubernetesNodes(readyCtx); err != nil {
			if waitErr := sleep(readyCtx, time.Second); waitErr != nil {
				return errors.Join(fmt.Errorf("%w within 60 seconds: %w", errK3sNotReady, err), waitErr)
			}
			continue
		}
		break
	}
	return r.kubectl(ctx, "get", "nodes", "-o", "wide")
}

type kubernetesOutput struct {
	buffer   bytes.Buffer
	maxBytes int
}

func (output *kubernetesOutput) String() string { return output.buffer.String() }

func (output *kubernetesOutput) Write(data []byte) (int, error) {
	if len(data) > output.maxBytes-output.buffer.Len() {
		return 0, errKubernetesOutputLimit
	}
	n, err := output.buffer.Write(data)
	if err != nil {
		return n, fmt.Errorf("write bounded kubernetes output: %w", err)
	}
	return n, nil
}

func (r *runner) kubernetesImageID(ctx context.Context, image string) (string, error) {
	output := kubernetesOutput{maxBytes: maxImageIDOutput}
	if err := r.command(ctx, commandTimeout, &output, r.stderr,
		"podman", "image", "inspect", "--format", "{{.ID}}", "--", image); err != nil {
		return "", errors.Join(errKubernetesImageIdentity, fmt.Errorf("inspect Kubernetes source image: %w", err))
	}
	id := "sha256:" + strings.TrimPrefix(strings.TrimSpace(output.String()), "sha256:")
	if !containertest.IsImageDigest(id) {
		return "", fmt.Errorf("inspect Kubernetes source image: %w", errKubernetesImageIdentity)
	}
	return id, nil
}

func (r *runner) importKubernetesImage(
	ctx context.Context,
	store *kubernetesContainerdStore,
	image, expectedID, receiptDirectory string,
) (_ string, runErr error) {
	if !containertest.IsImageDigest(expectedID) {
		return "", errKubernetesImageIdentity
	}
	if store == nil || store.connection == nil {
		return "", errKubernetesContainerdStore
	}
	if idErr := r.verifySourceImageID(ctx, image, expectedID); idErr != nil {
		return "", idErr
	}
	archive, archivePath, archiveInfo, createErr := createTempKubernetesArchive()
	if createErr != nil {
		return "", createErr
	}
	defer func() {
		runErr = errors.Join(runErr, cleanupTempKubernetesArchive(archive, archivePath, archiveInfo))
	}()
	verified, exportErr := r.exportAndVerifyKubernetesArchive(ctx, archive, archivePath, archiveInfo, image, expectedID)
	if exportErr != nil {
		return "", exportErr
	}
	if receiptErr := writeKubernetesArchiveReceipt(receiptDirectory, expectedID, verified.Evidence); receiptErr != nil {
		return "", receiptErr
	}
	manifestDigest, importErr := store.importArchive(ctx, archive, image, verified)
	if importErr != nil {
		return "", importErr
	}
	importReceipt := kubernetesImportReceipt{
		Schema:         importReceiptSchema,
		Image:          image,
		SourceID:       expectedID,
		ManifestDigest: manifestDigest,
		ArchiveDigest:  verified.Evidence.Digest,
		ServerUUID:     store.serverUUID,
	}
	if pubReceiptErr := writeKubernetesImportReceipt(receiptDirectory, expectedID, importReceipt); pubReceiptErr != nil {
		return "", pubReceiptErr
	}
	r.info("Imported %s (%s) into containerd image store", image, manifestDigest)
	return manifestDigest, nil
}

func (r *runner) verifySourceImageID(ctx context.Context, image, expectedID string) error {
	id, err := r.kubernetesImageID(ctx, image)
	if err != nil {
		return err
	}
	if id != expectedID {
		return fmt.Errorf("source image changed before export: %w", errKubernetesImageIdentity)
	}
	return nil
}

func createTempKubernetesArchive() (*os.File, string, os.FileInfo, error) {
	archive, err := os.CreateTemp("", "gobfd-k8s-image-*.tar")
	if err != nil {
		return nil, "", nil, fmt.Errorf("create temporary image archive: %w", err)
	}
	info, statErr := archive.Stat()
	if statErr != nil {
		closeErr := archive.Close()
		removeErr := os.Remove(archive.Name())
		return nil, "", nil, fmt.Errorf(
			"identify temporary image archive: %w",
			errors.Join(statErr, closeErr, removeErr),
		)
	}
	return archive, archive.Name(), info, nil
}

func cleanupTempKubernetesArchive(archive *os.File, archivePath string, archiveInfo os.FileInfo) error {
	var cleanupErr error
	if closeErr := archive.Close(); closeErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close temporary image archive: %w", closeErr))
	}
	current, statErr := os.Lstat(archivePath)
	if errors.Is(statErr, os.ErrNotExist) {
		return cleanupErr
	}
	if statErr != nil {
		return errors.Join(
			cleanupErr,
			fmt.Errorf("inspect temporary image archive %s before cleanup: %w", archivePath, statErr),
		)
	}
	if archiveInfo == nil || !current.Mode().IsRegular() || !os.SameFile(archiveInfo, current) {
		return errors.Join(
			cleanupErr,
			fmt.Errorf("preserve replaced temporary image archive %s: %w", archivePath, errKubernetesArchiveIdentity),
		)
	}
	if removeErr := os.Remove(archivePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		cleanupErr = errors.Join(
			cleanupErr,
			fmt.Errorf("remove temporary image archive %s: %w", archivePath, removeErr),
		)
	}
	return cleanupErr
}

func (r *runner) exportAndVerifyKubernetesArchive(
	ctx context.Context,
	archive *os.File,
	archivePath string,
	archiveInfo os.FileInfo,
	image, expectedID string,
) (verifiedKubernetesArchive, error) {
	var empty verifiedKubernetesArchive
	saveErr := r.command(
		ctx, buildTimeout, r.stdout, r.stderr,
		"podman", "save", "--format", "docker-archive", "--multi-image-archive=false",
		"--output", archivePath, expectedID, image,
	)
	if saveErr != nil {
		return empty, saveErr
	}
	current, statErr := os.Lstat(archivePath)
	if statErr != nil {
		return empty, fmt.Errorf("inspect exported image archive before import: %w", statErr)
	}
	if !current.Mode().IsRegular() || !os.SameFile(archiveInfo, current) {
		return empty, fmt.Errorf("preserve replaced exported image archive: %w", errKubernetesArchiveIdentity)
	}
	validationCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	verified, validationErr := inspectKubernetesArchive(validationCtx, archive, image, expectedID)
	cancel()
	if validationErr != nil {
		return empty, fmt.Errorf("verify exported Kubernetes image archive: %w", validationErr)
	}
	return verified, nil
}

func writeKubernetesArchiveReceipt(
	receiptDirectory, expectedID string,
	evidence kubernetesArchiveEvidence,
) error {
	data, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("encode Kubernetes archive evidence: %w", err)
	}
	name := "archive-" + strings.TrimPrefix(expectedID, "sha256:") + ".json"
	if writeErr := interopproject.CreatePrivateReceipt(filepath.Join(receiptDirectory, name), data); writeErr != nil {
		return fmt.Errorf("write Kubernetes archive evidence receipt: %w", writeErr)
	}
	return nil
}

func writeKubernetesImportReceipt(
	receiptDirectory, expectedID string,
	receipt kubernetesImportReceipt,
) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode Kubernetes import receipt: %w", err)
	}
	name := "import-" + strings.TrimPrefix(expectedID, "sha256:") + ".json"
	if writeErr := interopproject.CreatePrivateReceipt(filepath.Join(receiptDirectory, name), data); writeErr != nil {
		return fmt.Errorf("write Kubernetes import receipt: %w", writeErr)
	}
	return nil
}

func (r *runner) kubernetesDown(ctx context.Context) error {
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return fmt.Errorf("inspect K3s kubeconfig %s: %w", kubeconfigPath, err)
	}
	return r.deleteKubernetesNamespace(ctx)
}

func (r *runner) requests(ctx context.Context, count int) error {
	for i := 1; i <= count; i++ {
		body, err := r.httpGet(ctx, "http://localhost:8080")
		if err != nil {
			return fmt.Errorf("request %d: %w", i, err)
		}
		if !bytes.Contains(body, []byte("nginx")) {
			return fmt.Errorf("request %d: %w", i, errHAProxyResponse)
		}
		r.info("Request %d: OK", i)
	}
	return nil
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
	if indentErr := json.Indent(&formatted, body, "", "  "); indentErr != nil {
		fmt.Fprintln(r.stdout, string(body))
		return
	}
	formatted.WriteByte('\n')
	if _, writeErr := formatted.WriteTo(r.stdout); writeErr != nil {
		r.warn("Could not write formatted JSON: %v", writeErr)
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

func (r *runner) commandEnv(
	ctx context.Context,
	timeout time.Duration,
	stdout, stderr io.Writer,
	environment []string,
	name string,
	args ...string,
) error {
	return r.commandInputEnv(ctx, timeout, nil, stdout, stderr, environment, name, args...)
}

func (r *runner) commandInputEnv(
	ctx context.Context,
	timeout time.Duration,
	stdin io.Reader,
	stdout, stderr io.Writer,
	environment []string,
	name string,
	args ...string,
) error {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// #nosec G204 -- Integration target implementations select executable/argv; no shell command string is evaluated.
	cmd := exec.CommandContext(commandCtx, name, args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), environment...)
	cmd.Stdin = stdin
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
