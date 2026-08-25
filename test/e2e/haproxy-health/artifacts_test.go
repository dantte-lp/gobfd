//go:build e2e_haproxy_testcontainers

package haproxy_health_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

type haproxyResourceSnapshot struct {
	ContainerNames []string `json:"container_names"`
	ContainerIDs   []string `json:"container_ids"`
	ImageNames     []string `json:"image_names"`
	ImageIDs       []string `json:"image_ids"`
	NetworkName    string   `json:"network_name"`
	StartupError   string   `json:"startup_error,omitempty"`
}

func (topology *haproxyHealthTopology) initializeResourceEvidence() error {
	if err := initializeHAProxyDiagnostics(topology.reportDir); err != nil {
		return err
	}
	return topology.writeResourceSnapshot()
}

func (topology *haproxyHealthTopology) registerEvidenceCleanup(
	register func(func()), write func() error, report func(error),
) {
	register(func() {
		topology.evidenceOnce.Do(func() {
			if err := write(); err != nil {
				report(fmt.Errorf("write HAProxy health evidence before cleanup: %w", err))
			}
		})
	})
}

func (topology *haproxyHealthTopology) armEvidenceCleanup(t *testing.T) {
	t.Helper()
	topology.registerEvidenceCleanup(t.Cleanup, topology.writeEvidence, func(err error) {
		t.Errorf("%v", err)
	})
}

func (topology *haproxyHealthTopology) recordOwnedImage(name string) error {
	if name != "" && !slices.Contains(topology.imageNames, name) {
		topology.imageNames = append(topology.imageNames, name)
	}
	return topology.writeResourceSnapshot()
}

func (topology *haproxyHealthTopology) recordOwnedImageID(imageID string) error {
	if imageID != "" && !slices.Contains(topology.imageIDs, imageID) {
		topology.imageIDs = append(topology.imageIDs, imageID)
	}
	return topology.writeResourceSnapshot()
}

func (topology *haproxyHealthTopology) recordOwnedContainer(name, containerID string) error {
	if name != "" && !slices.Contains(topology.containerNames, name) {
		topology.containerNames = append(topology.containerNames, name)
	}
	if containerID != "" && !slices.Contains(topology.containerIDs, containerID) {
		topology.containerIDs = append(topology.containerIDs, containerID)
	}
	return topology.writeResourceSnapshot()
}

func (topology *haproxyHealthTopology) recordStartupFailure(startupErr error) error {
	topology.startupError = startupErr.Error()
	diagnosticErr := writeHAProxyDiagnostic(topology.reportDir, "containers.err", topology.startupError+"\n")
	return errors.Join(diagnosticErr, topology.writeResourceSnapshot())
}

func (topology *haproxyHealthTopology) writeResourceSnapshot() error {
	snapshot := haproxyResourceSnapshot{
		ContainerNames: slices.Clone(topology.containerNames),
		ContainerIDs:   slices.Clone(topology.containerIDs),
		ImageNames:     slices.Clone(topology.imageNames),
		ImageIDs:       slices.Clone(topology.imageIDs),
		NetworkName:    topology.networkName,
		StartupError:   topology.startupError,
	}
	contents, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mutable HAProxy health resource snapshot: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maxDiagnosticBytes {
		return fmt.Errorf("HAProxy health resource snapshot exceeds %d bytes", maxDiagnosticBytes)
	}
	if err := writeAtomicEvidence(topology.reportDir, "resources.json", contents); err != nil {
		return fmt.Errorf("write mutable HAProxy health resource snapshot: %w", err)
	}
	return nil
}

func registerHAProxyFinalSummary(
	register func(func()), failed func() bool, write func(int) error, report func(error),
) {
	register(func() {
		status := 0
		if failed() {
			status = 1
		}
		if err := write(status); err != nil {
			report(err)
		}
	})
}

func buildHAProxyOwnedImage(
	ctx context.Context, t *testing.T, topology *haproxyHealthTopology, buildContext, imageName string,
) (string, error) {
	t.Helper()
	repository, tag, found := strings.Cut(imageName, ":")
	if !found {
		return "", fmt.Errorf("split test-owned image %q", imageName)
	}
	if err := installHAProxyOwnedImageCleanup(ctx, imageName, topology.client, t.Cleanup, func(err error) {
		t.Errorf("%v", err)
	}); err != nil {
		return "", err
	}
	if err := topology.recordOwnedImage(imageName); err != nil {
		return "", err
	}
	topology.armEvidenceCleanup(t)
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		return "", fmt.Errorf("create explicit Podman image provider: %w", err)
	}
	dockerProvider, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		return "", errors.Join(fmt.Errorf("Podman provider type = %T", provider), provider.Close())
	}
	//nolint:modernize // ContainerRequest implements ImageBuildInfo in testcontainers v0.44.
	_, buildErr := dockerProvider.BuildImage(ctx, &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context: buildContext, Dockerfile: "Containerfile", Repo: repository, Tag: tag, KeepImage: true,
		},
	})
	if buildErr != nil {
		return "", fmt.Errorf(
			"build bounded test-owned image %s: %w", imageName,
			errors.Join(buildErr, provider.Close()),
		)
	}
	inspectErr := inspectAndRecordBuiltImage(ctx, imageName, topology.client, topology.recordOwnedImageID)
	if err := errors.Join(inspectErr, provider.Close()); err != nil {
		return "", fmt.Errorf("build bounded test-owned image %s: %w", imageName, err)
	}
	return imageName, nil
}

func inspectAndRecordBuiltImage(
	ctx context.Context, imageName string, client ownedImageClient, record func(string) error,
) error {
	imageID, err := client.ImageID(ctx, imageName)
	if err != nil {
		return fmt.Errorf("inspect exact built image tag %s: %w", imageName, err)
	}
	if err := validateSHA256ContentID(imageID); err != nil {
		return fmt.Errorf("validate exact built image tag %s: %w", imageName, err)
	}
	if err := record(imageID); err != nil {
		return fmt.Errorf("record exact built image tag %s content ID: %w", imageName, err)
	}
	return nil
}

func validateSHA256ContentID(imageID string) error {
	digest := strings.TrimPrefix(imageID, "sha256:")
	if len(digest) != 64 || len(digest) == len(imageID) {
		return fmt.Errorf("image ID = %q, want sha256 content ID", imageID)
	}
	for _, character := range digest {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return fmt.Errorf("image ID = %q, want lowercase hexadecimal", imageID)
		}
	}
	return nil
}

func installHAProxyOwnedImageCleanup(
	ctx context.Context, imageName string, client ownedImageClient,
	register func(func()), report func(error),
) error {
	exists, err := client.ImageExists(ctx, imageName)
	if err != nil {
		return fmt.Errorf("inspect unique test-owned image %s before build: %w", imageName, err)
	}
	if exists {
		return fmt.Errorf("refuse ownership of pre-existing image %s", imageName)
	}
	register(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		exists, inspectErr := client.ImageExists(cleanupCtx, imageName)
		if inspectErr != nil {
			report(fmt.Errorf("inspect exact test-owned image %s during cleanup: %w", imageName, inspectErr))
			return
		}
		if exists {
			if removeErr := client.RemoveImage(cleanupCtx, imageName); removeErr != nil {
				report(fmt.Errorf("remove exact test-owned image %s: %w", imageName, removeErr))
			}
		}
	})
	return nil
}

func (topology *haproxyHealthTopology) writeEvidence() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var evidenceErr error
	if topology.capture != nil && topology.analyzer != nil && topology.client != nil {
		evidenceErr = errors.Join(evidenceErr, topology.collectPacketEvidence(ctx))
	} else {
		evidenceErr = errors.Join(evidenceErr, writeHAProxyDiagnostic(
			topology.reportDir, "packets.err", "packet evidence unavailable: topology startup incomplete\n",
		))
	}
	evidenceErr = errors.Join(evidenceErr, topology.writeContainerLogs(ctx))
	evidenceErr = errors.Join(evidenceErr, topology.writeContainerSnapshot(ctx))
	evidenceErr = errors.Join(evidenceErr, topology.writeEnvironment())
	evidenceErr = errors.Join(evidenceErr, topology.writeResourceSnapshot())
	return evidenceErr
}

func (topology *haproxyHealthTopology) collectPacketEvidence(ctx context.Context) error {
	stopTimeout := 3 * time.Second
	if err := topology.capture.Stop(ctx, &stopTimeout); err != nil {
		return topology.packetEvidenceError(fmt.Errorf("stop exact tshark capture: %w", err))
	}
	reader, err := topology.capture.CopyFileFromContainer(ctx, "/captures/bfd.pcapng")
	if err != nil {
		return topology.packetEvidenceError(fmt.Errorf("copy BFD packet capture: %w", err))
	}
	packetData, readErr := io.ReadAll(io.LimitReader(reader, maxPacketBytes+1))
	closeErr := reader.Close()
	if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
		return topology.packetEvidenceError(fmt.Errorf("read BFD packet capture: %w", joinedErr))
	}
	if len(packetData) == 0 || len(packetData) > maxPacketBytes {
		return topology.packetEvidenceError(fmt.Errorf(
			"BFD packet capture size %d is outside 1..%d", len(packetData), maxPacketBytes,
		))
	}
	if writeErr := os.WriteFile(
		filepath.Join(topology.reportDir, "packets.pcapng"), packetData, 0o600,
	); writeErr != nil {
		return topology.packetEvidenceError(fmt.Errorf("write BFD packet capture: %w", writeErr))
	}
	if copyErr := topology.analyzer.CopyToContainer(
		ctx, packetData, "/captures/bfd.pcapng", 0o600,
	); copyErr != nil {
		return topology.packetEvidenceError(fmt.Errorf("copy packet capture to exact analyzer: %w", copyErr))
	}
	filter := fmt.Sprintf(
		"bfd && ((ip.src == %s && (ip.dst == %s || ip.dst == %s)) || "+
			"(ip.dst == %s && (ip.src == %s || ip.src == %s)))",
		topology.contract.monitorIP, topology.contract.backend1IP, topology.contract.backend2IP,
		topology.contract.monitorIP, topology.contract.backend1IP, topology.contract.backend2IP,
	)
	result, err := topology.client.Exec(ctx, topology.analyzer.GetContainerID(), []string{
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", filter, "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst",
		"-e", "udp.srcport", "-e", "udp.dstport", "-e", "bfd.sta", "-e", "bfd.diag",
		"-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=,",
	})
	decoded, diagnostic, decodeErr := decodedPacketEvidence(result, err)
	if diagnosticErr := writeHAProxyDiagnostic(topology.reportDir, "packets.err", diagnostic); diagnosticErr != nil {
		return errors.Join(decodeErr, diagnosticErr)
	}
	if decodeErr != nil {
		return decodeErr
	}
	if len(strings.Split(strings.TrimSpace(string(decoded)), "\n")) < 2 {
		rowErr := errors.New("decoded BFD packet evidence has no packet row")
		return errors.Join(rowErr, writeHAProxyDiagnostic(
			topology.reportDir, "packets.err", diagnostic+rowErr.Error()+"\n",
		))
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, "packets.csv"), decoded, 0o600); err != nil {
		return topology.packetEvidenceError(fmt.Errorf("write decoded BFD packet evidence: %w", err))
	}
	topology.packetEvidence = true
	return nil
}

func decodedPacketEvidence(result podmanapi.ExecResult, execErr error) ([]byte, string, error) {
	if execErr == nil {
		if len(result.Stdout) == 0 || len(result.Stdout) > maxDiagnosticBytes {
			err := fmt.Errorf(
				"decoded tshark stdout size %d is outside 1..%d", len(result.Stdout), maxDiagnosticBytes,
			)
			return nil, boundedPacketFailureDiagnostic(err, result.Stdout, result.Stderr), err
		}
		if len(result.Stderr) > maxDiagnosticBytes {
			err := fmt.Errorf("decoded tshark stderr exceeds %d bytes", maxDiagnosticBytes)
			return nil, boundedPacketFailureDiagnostic(err, result.Stdout, result.Stderr), err
		}
		return []byte(result.Stdout), result.Stderr, nil
	}
	err := fmt.Errorf("decode exact BFD packet evidence: %w", execErr)
	return nil, boundedPacketFailureDiagnostic(err, result.Stdout, result.Stderr), err
}

func boundedPacketFailureDiagnostic(err error, stdout, stderr string) string {
	const fieldLimit = maxDiagnosticBytes / 3
	if len(stdout) > fieldLimit {
		stdout = stdout[:fieldLimit]
	}
	if len(stderr) > fieldLimit {
		stderr = stderr[:fieldLimit]
	}
	diagnostic := fmt.Sprintf("error: %v\nstdout:\n%s\nstderr:\n%s\n", err, stdout, stderr)
	if len(diagnostic) > maxDiagnosticBytes {
		return diagnostic[:maxDiagnosticBytes]
	}
	return diagnostic
}

func (topology *haproxyHealthTopology) packetEvidenceError(err error) error {
	diagnosticErr := writeHAProxyDiagnostic(topology.reportDir, "packets.err", err.Error()+"\n")
	return errors.Join(err, diagnosticErr)
}

func (topology *haproxyHealthTopology) writeContainerLogs(ctx context.Context) error {
	var output strings.Builder
	for _, item := range topology.containers {
		logs, err := item.container.Logs(ctx)
		if err != nil {
			return fmt.Errorf("open %s logs: %w", item.name, err)
		}
		contents, readErr := io.ReadAll(io.LimitReader(logs, maxDiagnosticBytes+1))
		closeErr := logs.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("read bounded %s logs: %w", item.name, err)
		}
		if len(contents) > maxDiagnosticBytes {
			return fmt.Errorf("%s logs exceed %d bytes", item.name, maxDiagnosticBytes)
		}
		fmt.Fprintf(&output, "===== %s (%s) =====\n%s\n", item.name, item.container.GetContainerID(), contents)
	}
	if output.Len() == 0 && len(topology.containers) != 0 {
		return errors.New("container log evidence is unexpectedly empty")
	}
	if err := os.WriteFile(
		filepath.Join(topology.reportDir, "containers.log"), []byte(output.String()), 0o600,
	); err != nil {
		return fmt.Errorf("write bounded container logs: %w", err)
	}
	return nil
}

func (topology *haproxyHealthTopology) writeContainerSnapshot(ctx context.Context) error {
	inspections := make([]json.RawMessage, 0, len(topology.containerIDs))
	for _, containerID := range topology.containerIDs {
		if topology.client == nil {
			return topology.containerSnapshotError(errors.New("exact-endpoint Podman client unavailable"))
		}
		inspection, err := topology.client.Inspect(ctx, containerID)
		if err != nil {
			return topology.containerSnapshotError(fmt.Errorf("inspect exact owned container %s: %w", containerID, err))
		}
		if len(inspection) == 0 || len(inspection) > maxDiagnosticBytes {
			return topology.containerSnapshotError(fmt.Errorf(
				"exact owned container %s inspection size %d exceeds allowed range 1..%d",
				containerID, len(inspection), maxDiagnosticBytes,
			))
		}
		inspections = append(inspections, inspection)
	}
	contents, err := json.MarshalIndent(inspections, "", "  ")
	if err != nil {
		return topology.containerSnapshotError(fmt.Errorf("encode exact container snapshot: %w", err))
	}
	contents = append(contents, '\n')
	if len(contents) > maxDiagnosticBytes {
		return topology.containerSnapshotError(fmt.Errorf(
			"aggregate container snapshot size %d exceeds %d bytes", len(contents), maxDiagnosticBytes,
		))
	}
	if err := writeAtomicEvidence(topology.reportDir, "containers.json", contents); err != nil {
		return topology.containerSnapshotError(fmt.Errorf("write exact container snapshot: %w", err))
	}
	var diagnostic strings.Builder
	if topology.startupError != "" {
		diagnostic.WriteString(topology.startupError)
		diagnostic.WriteByte('\n')
	}
	return writeHAProxyDiagnostic(topology.reportDir, "containers.err", diagnostic.String())
}

func writeAtomicEvidence(reportDir, name string, contents []byte) (returnErr error) {
	if filepath.Base(name) != name || name == "." {
		return fmt.Errorf("evidence name %q is not a local file", name)
	}
	root, openErr := os.OpenRoot(reportDir)
	if openErr != nil {
		return fmt.Errorf("open HAProxy health report root: %w", openErr)
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()

	var random [8]byte
	if _, randomErr := rand.Read(random[:]); randomErr != nil {
		return fmt.Errorf("generate evidence temporary name: %w", randomErr)
	}
	temporaryName := "." + name + ".tmp-" + hex.EncodeToString(random[:])
	temporary, createErr := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if createErr != nil {
		return fmt.Errorf("create rooted temporary evidence %s: %w", name, createErr)
	}
	keepTemporary := true
	defer func() {
		if keepTemporary {
			returnErr = errors.Join(returnErr, root.Remove(temporaryName))
		}
	}()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		return errors.Join(fmt.Errorf("chmod temporary evidence %s: %w", name, chmodErr), temporary.Close())
	}
	if _, writeErr := temporary.Write(contents); writeErr != nil {
		return errors.Join(fmt.Errorf("write temporary evidence %s: %w", name, writeErr), temporary.Close())
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		return errors.Join(fmt.Errorf("sync temporary evidence %s: %w", name, syncErr), temporary.Close())
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return fmt.Errorf("close temporary evidence %s: %w", name, closeErr)
	}
	if renameErr := root.Rename(temporaryName, name); renameErr != nil {
		return fmt.Errorf("atomically replace evidence %s: %w", name, renameErr)
	}
	keepTemporary = false
	published, statErr := root.Lstat(name)
	if statErr != nil {
		return fmt.Errorf("stat published evidence %s: %w", name, statErr)
	}
	if !published.Mode().IsRegular() || published.Mode().Perm() != 0o600 {
		return fmt.Errorf("published evidence %s mode/type = %v, want regular 0600", name, published.Mode())
	}
	return nil
}

func (topology *haproxyHealthTopology) containerSnapshotError(snapshotErr error) error {
	var diagnostic strings.Builder
	if topology.startupError != "" {
		diagnostic.WriteString(topology.startupError)
		diagnostic.WriteByte('\n')
	}
	diagnostic.WriteString(snapshotErr.Error())
	diagnostic.WriteByte('\n')
	writeErr := writeHAProxyDiagnostic(topology.reportDir, "containers.err", diagnostic.String())
	return errors.Join(snapshotErr, writeErr)
}

func (topology *haproxyHealthTopology) writeEnvironment() error {
	document := struct {
		Target         string   `json:"target"`
		RunID          string   `json:"run_id"`
		PodmanEndpoint string   `json:"podman_endpoint"`
		Network        string   `json:"network"`
		ContainerIDs   []string `json:"container_ids"`
		ImageIDs       []string `json:"image_ids"`
	}{
		Target: "haproxy-health-testcontainers", RunID: topology.runID,
		PodmanEndpoint: topology.endpoint, Network: topology.networkName,
		ContainerIDs: topology.containerIDs, ImageIDs: topology.imageIDs,
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode HAProxy health environment: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(topology.reportDir, "environment.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write HAProxy health environment: %w", err)
	}
	return nil
}

func (topology *haproxyHealthTopology) writeSummary(status int) error {
	contents := fmt.Sprintf("# HAProxy Health Testcontainers Summary\n\n"+
		"| Field | Value |\n|---|---|\n"+
		"| Target | `make int-haproxy-testcontainers` |\n"+
		"| Run ID | `%s` |\n| Exit code | %d |\n"+
		"| Packet capture | `packets.pcapng` |\n| Packet CSV | `packets.csv` |\n"+
		"| Container evidence | `containers.json`, `containers.log` |\n",
		topology.runID, status)
	if err := os.WriteFile(filepath.Join(topology.reportDir, "summary.md"), []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write HAProxy health summary: %w", err)
	}
	return nil
}

func writeHAProxyDiagnostic(reportDir, name, contents string) error {
	truncated := len(contents) > maxDiagnosticBytes
	if truncated {
		contents = contents[:maxDiagnosticBytes]
	}
	file, err := os.OpenFile(filepath.Join(reportDir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create HAProxy health diagnostic %s: %w", name, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod HAProxy health diagnostic %s: %w", name, errors.Join(err, file.Close()))
	}
	_, writeErr := io.WriteString(file, contents)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write HAProxy health diagnostic %s: %w", name, err)
	}
	if truncated {
		return fmt.Errorf("HAProxy health diagnostic %s truncated to %d bytes", name, maxDiagnosticBytes)
	}
	return nil
}

func initializeHAProxyDiagnostics(reportDir string) error {
	var diagnosticsErr error
	for _, name := range []string{"containers.err", "packets.err"} {
		diagnosticsErr = errors.Join(diagnosticsErr, writeHAProxyDiagnostic(reportDir, name, ""))
	}
	if diagnosticsErr != nil {
		return fmt.Errorf("initialize HAProxy health diagnostics: %w", diagnosticsErr)
	}
	return nil
}

func haproxyReportDirectory(t *testing.T, root string) (string, string) {
	t.Helper()
	runID := time.Now().UTC().Format(haproxyReportRunTime)
	reportDir := strings.TrimSpace(os.Getenv("E2E_HAPROXY_TESTCONTAINERS_ARTIFACT_DIR"))
	switch {
	case reportDir == "":
		reportDir = filepath.Join(root, "reports/e2e/haproxy-health", runID)
	case !filepath.IsAbs(reportDir):
		t.Fatalf("HAProxy health artifact directory %q must be absolute", reportDir)
	default:
		runID = filepath.Base(filepath.Clean(reportDir))
	}
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		t.Fatalf("create HAProxy health artifact directory: %v", err)
	}
	return runID, reportDir
}

func assertHAProxyContainerNamesRemoved(t *testing.T, endpoint string, names []string) {
	t.Helper()
	if len(names) == 0 {
		return
	}
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create exact-endpoint container-name cleanup client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		containers, listErr := client.Containers(ctx)
		if listErr != nil {
			t.Fatalf("list containers while verifying exact owned names: %v", listErr)
		}
		remaining := remainingHAProxyContainerNames(names, containers)
		if len(remaining) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("containers with exact owned names %v still exist after cleanup: %v", remaining, ctx.Err())
		case <-ticker.C:
		}
	}
}

func remainingHAProxyContainerNames(names []string, containers []podmanapi.ContainerSummary) []string {
	var remaining []string
	for _, ownedName := range names {
		for _, existing := range containers {
			for _, existingName := range existing.Names {
				if strings.TrimPrefix(existingName, "/") == ownedName {
					remaining = append(remaining, ownedName)
				}
			}
		}
	}
	return remaining
}
