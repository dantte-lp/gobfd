//go:build e2e_observability_testcontainers

package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func (topology *observabilityTopology) armEvidenceCleanup(ctx context.Context, t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		topology.evidenceOnce.Do(func() {
			evidenceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			defer cancel()
			if err := topology.writeEvidence(evidenceCtx); err != nil {
				t.Errorf("write observability evidence before cleanup: %v", err)
			}
		})
	})
}

func buildOwnedImage(
	ctx context.Context,
	t *testing.T,
	topology *observabilityTopology,
	buildContext, imageName string,
) (string, error) {
	t.Helper()
	repository, tag, found := strings.Cut(imageName, ":")
	if !found {
		return "", fmt.Errorf("split test-owned image %q", imageName)
	}
	if err := installOwnedImageCleanup(ctx, imageName, topology.client, t.Cleanup, func(err error) {
		t.Errorf("%v", err)
	}); err != nil {
		return "", err
	}
	if err := topology.recordOwnedImage(imageName); err != nil {
		return "", err
	}
	topology.armEvidenceCleanup(ctx, t)
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		return "", fmt.Errorf("create explicit Podman image provider: %w", err)
	}
	dockerProvider, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		return "", errors.Join(
			fmt.Errorf("Podman provider type = %T, want *testcontainers.DockerProvider", provider),
			provider.Close(),
		)
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
	imageID, inspectErr := topology.client.ImageID(ctx, imageName)
	if inspectErr == nil {
		inspectErr = validateSHA256ContentID(imageID)
	}
	if inspectErr == nil {
		inspectErr = topology.recordOwnedImageID(imageID)
	}
	if err := errors.Join(inspectErr, provider.Close()); err != nil {
		return "", fmt.Errorf("inspect exact built image tag %s: %w", imageName, err)
	}
	return imageName, nil
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

func installOwnedImageCleanup(
	ctx context.Context,
	imageName string,
	client ownedImageClient,
	register func(func()),
	report func(error),
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

func (topology *observabilityTopology) writeEvidence(ctx context.Context) error {
	var evidenceErr error
	if topology.capture != nil && topology.analyzer != nil && topology.client != nil {
		evidenceErr = errors.Join(evidenceErr, topology.collectPacketEvidence(ctx))
	} else {
		evidenceErr = errors.Join(evidenceErr, writeDiagnostic(
			topology.reportDir, "packets.err", "packet evidence unavailable: topology startup incomplete\n",
		))
	}
	evidenceErr = errors.Join(evidenceErr, topology.writeContainerLogs(ctx))
	evidenceErr = errors.Join(evidenceErr, topology.writeContainerSnapshot(ctx))
	evidenceErr = errors.Join(evidenceErr, topology.writeEnvironment())
	evidenceErr = errors.Join(evidenceErr, topology.writeResourceSnapshot())
	evidenceErr = errors.Join(evidenceErr, topology.writePendingSummary())
	return evidenceErr
}

func (topology *observabilityTopology) collectPacketEvidence(ctx context.Context) error {
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
		"bfd && ((ip.src == %s && ip.dst == %s) || (ip.src == %s && ip.dst == %s))",
		topology.contract.gobfdIP, topology.contract.frrIP,
		topology.contract.frrIP, topology.contract.gobfdIP,
	)
	result, err := topology.client.Exec(ctx, topology.analyzer.GetContainerID(), []string{
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", filter, "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst",
		"-e", "udp.srcport", "-e", "udp.dstport", "-e", "bfd.sta", "-e", "bfd.diag",
		"-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=,",
	})
	if err != nil {
		return topology.packetEvidenceError(fmt.Errorf(
			"decode exact BFD packet evidence: %w; stdout=%s; stderr=%s",
			err, boundString(result.Stdout), boundString(result.Stderr),
		))
	}
	if len(result.Stdout) == 0 || len(result.Stdout) > maxDiagnosticBytes {
		return topology.packetEvidenceError(fmt.Errorf(
			"decoded tshark stdout size %d is outside 1..%d", len(result.Stdout), maxDiagnosticBytes,
		))
	}
	if len(strings.Split(strings.TrimSpace(result.Stdout), "\n")) < 2 {
		return topology.packetEvidenceError(errors.New("decoded BFD packet evidence has no packet row"))
	}
	if err := os.WriteFile(
		filepath.Join(topology.reportDir, "packets.csv"), []byte(result.Stdout), 0o600,
	); err != nil {
		return topology.packetEvidenceError(fmt.Errorf("write decoded BFD packet evidence: %w", err))
	}
	if err := writeDiagnostic(topology.reportDir, "packets.err", result.Stderr); err != nil {
		return err
	}
	topology.packetEvidence = true
	return nil
}

func (topology *observabilityTopology) packetEvidenceError(packetErr error) error {
	return errors.Join(packetErr, writeDiagnostic(topology.reportDir, "packets.err", packetErr.Error()+"\n"))
}

func (topology *observabilityTopology) writeContainerLogs(ctx context.Context) error {
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
		if output.Len() > maxDiagnosticBytes {
			return fmt.Errorf("aggregate container logs exceed %d bytes", maxDiagnosticBytes)
		}
	}
	if err := os.WriteFile(
		filepath.Join(topology.reportDir, "containers.log"), []byte(output.String()), 0o600,
	); err != nil {
		return fmt.Errorf("write bounded container logs: %w", err)
	}
	return nil
}

func (topology *observabilityTopology) writeContainerSnapshot(ctx context.Context) error {
	inspections := make([]json.RawMessage, 0, len(topology.containerIDs))
	for _, containerID := range topology.containerIDs {
		inspection, err := topology.client.Inspect(ctx, containerID)
		if err != nil {
			return topology.containerSnapshotError(fmt.Errorf("inspect exact owned container %s: %w", containerID, err))
		}
		if len(inspection) == 0 || len(inspection) > maxDiagnosticBytes {
			return topology.containerSnapshotError(fmt.Errorf(
				"exact owned container %s inspection size %d is outside 1..%d",
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
			"aggregate container snapshot size %d exceeds %d", len(contents), maxDiagnosticBytes,
		))
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, "containers.json"), contents, 0o600); err != nil {
		return topology.containerSnapshotError(fmt.Errorf("write exact container snapshot: %w", err))
	}
	return writeDiagnostic(topology.reportDir, "containers.err", topology.startupError)
}

func (topology *observabilityTopology) containerSnapshotError(snapshotErr error) error {
	return errors.Join(
		snapshotErr,
		writeDiagnostic(topology.reportDir, "containers.err", topology.startupError+"\n"+snapshotErr.Error()+"\n"),
	)
}

func (topology *observabilityTopology) writeEnvironment() error {
	document := struct {
		Target         string   `json:"target"`
		RunID          string   `json:"run_id"`
		PodmanEndpoint string   `json:"podman_endpoint"`
		Network        string   `json:"network"`
		ContainerIDs   []string `json:"container_ids"`
		ImageIDs       []string `json:"image_ids"`
	}{
		Target: "int-observability-testcontainers", RunID: topology.runID,
		PodmanEndpoint: topology.endpoint, Network: topology.networkName,
		ContainerIDs: topology.containerIDs, ImageIDs: topology.imageIDs,
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode observability environment: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(topology.reportDir, "environment.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write observability environment: %w", err)
	}
	return nil
}

func (topology *observabilityTopology) writeJSONEvidence(name string, contents []byte) error {
	if len(contents) == 0 || len(contents) > maxDiagnosticBytes {
		return fmt.Errorf("JSON evidence %s size %d is outside 1..%d", name, len(contents), maxDiagnosticBytes)
	}
	if err := preflightStrictJSON(contents); err != nil {
		return fmt.Errorf("validate JSON evidence %s: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, name), contents, 0o600); err != nil {
		return fmt.Errorf("write JSON evidence %s: %w", name, err)
	}
	return nil
}

func (topology *observabilityTopology) writeSummary(status int) error {
	return topology.writeSummaryResult(strconv.Itoa(status))
}

func (topology *observabilityTopology) writePendingSummary() error {
	return topology.writeSummaryResult("pending (cleanup and post-cleanup assertions incomplete)")
}

func (topology *observabilityTopology) writeSummaryResult(result string) error {
	contents := fmt.Sprintf("# Observability Testcontainers Summary\n\n"+
		"| Field | Value |\n|---|---|\n"+
		"| Target | `make int-observability-testcontainers` |\n"+
		"| Run ID | `%s` |\n| Exit code | %s |\n"+
		"| Packet evidence | `packets.pcapng`, `packets.csv` |\n"+
		"| Container evidence | `containers.json`, `containers.log` |\n"+
		"| API evidence | `prometheus-*.json`, `grafana-*.json`, `bfd-session-*.json` |\n"+
		"| Ownership evidence | `environment.json`, `resources.json` |\n",
		topology.runID, result)
	if err := os.WriteFile(filepath.Join(topology.reportDir, "summary.md"), []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write observability summary: %w", err)
	}
	return nil
}

func registerFinalSummary(
	register func(func()),
	failed func() bool,
	write func(int) error,
	report func(error),
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

func writeDiagnostic(reportDir, name, contents string) error {
	if len(contents) > maxDiagnosticBytes {
		contents = contents[:maxDiagnosticBytes]
	}
	if err := os.WriteFile(filepath.Join(reportDir, name), []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write observability diagnostic %s: %w", name, err)
	}
	return nil
}

func initializeDiagnostics(reportDir string) error {
	var diagnosticsErr error
	for _, name := range []string{"containers.err", "packets.err"} {
		diagnosticsErr = errors.Join(diagnosticsErr, writeDiagnostic(reportDir, name, ""))
	}
	if diagnosticsErr != nil {
		return fmt.Errorf("initialize observability diagnostics: %w", diagnosticsErr)
	}
	return nil
}

func observabilityReportDirectory(t *testing.T, root string) (string, string) {
	t.Helper()
	reportDir := strings.TrimSpace(os.Getenv("E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR"))
	owner := strings.TrimSpace(os.Getenv("E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER"))
	if reportDir == "" {
		parent := filepath.Join(root, "reports/e2e/observability")
		if !filepath.IsAbs(parent) {
			t.Fatalf("observability artifact parent %q must be absolute", parent)
		}
		if err := os.MkdirAll(parent, 0o700); err != nil {
			t.Fatalf("create observability artifact parent: %v", err)
		}
		var err error
		reportDir, err = os.MkdirTemp(parent, "run.") //nolint:usetesting // The report must use the fixed same parent.
		if err != nil {
			t.Fatalf("create exclusive observability artifact directory: %v", err)
		}
		if err := os.Chmod(reportDir, 0o700); err != nil {
			t.Fatalf("chmod exclusive observability artifact directory: %v", err)
		}
		owner = filepath.Base(reportDir)
		if err := createObservabilityReportMarker(reportDir, owner); err != nil {
			t.Fatalf("create observability artifact ownership marker: %v", err)
		}
	} else if !filepath.IsAbs(reportDir) {
		t.Fatalf("observability artifact directory %q must be absolute", reportDir)
	} else if err := validateObservabilityReportDirectory(reportDir, owner); err != nil {
		t.Fatalf("validate owned observability artifact directory: %v", err)
	}
	return owner, filepath.Clean(reportDir)
}

func createObservabilityReportMarker(reportDir, owner string) (returnErr error) {
	root, err := os.OpenRoot(reportDir)
	if err != nil {
		return fmt.Errorf("open observability report root: %w", err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close observability report root: %w", closeErr))
		}
	}()
	marker, err := root.OpenFile(
		".gobfd-observability-owner", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
	)
	if err != nil {
		return fmt.Errorf("create exclusive observability ownership marker: %w", err)
	}
	if err := marker.Chmod(0o600); err != nil {
		return errors.Join(
			fmt.Errorf("chmod observability ownership marker: %w", err), marker.Close(),
		)
	}
	if _, err := io.WriteString(marker, owner+"\n"); err != nil {
		return errors.Join(fmt.Errorf("write observability ownership marker: %w", err), marker.Close())
	}
	if err := marker.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync observability ownership marker: %w", err), marker.Close())
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("close observability ownership marker: %w", err)
	}
	return nil
}

func validateObservabilityReportDirectory(reportDir, owner string) (returnErr error) {
	if owner == "" || filepath.Base(owner) != owner || owner == "." || filepath.Base(filepath.Clean(reportDir)) != owner {
		return fmt.Errorf("artifact owner %q does not match directory %q", owner, reportDir)
	}
	info, err := os.Lstat(reportDir)
	if err != nil {
		return fmt.Errorf("lstat observability artifact directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("observability artifact directory mode/type = %v, want directory 0700", info.Mode())
	}
	root, err := os.OpenRoot(reportDir)
	if err != nil {
		return fmt.Errorf("open owned observability report root: %w", err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close owned observability report root: %w", closeErr))
		}
	}()
	markerInfo, err := root.Lstat(".gobfd-observability-owner")
	if err != nil {
		return fmt.Errorf("lstat observability ownership marker: %w", err)
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm() != 0o600 {
		return fmt.Errorf("observability ownership marker mode/type = %v, want regular 0600", markerInfo.Mode())
	}
	marker, err := root.ReadFile(".gobfd-observability-owner")
	if err != nil {
		return fmt.Errorf("read observability ownership marker: %w", err)
	}
	if string(marker) != owner+"\n" {
		return fmt.Errorf("observability ownership marker does not match owner %q", owner)
	}
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		return fmt.Errorf("read observability artifact directory: %w", err)
	}
	for _, entry := range entries {
		switch entry.Name() {
		case ".gobfd-observability-owner", "go-test.json", "go-test.log":
		default:
			return fmt.Errorf("observability artifact directory contains unowned entry %q", entry.Name())
		}
	}
	return nil
}

func assertContainerNamesRemoved(t *testing.T, endpoint string, names []string) {
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

func boundString(value string) string {
	const limit = maxDiagnosticBytes / 4
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
