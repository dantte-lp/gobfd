package e2erunner

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/routingartifacts"
)

const (
	mergeOwnerLabelKey = "io.gobfd.e2e.merge-owner"
	baseSuiteName      = "interop"
	bgpSuiteName       = "interop-bgp"
	minimumPacketRows  = 2
)

var (
	imageIDPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errNoPassedTests  = errors.New("go test JSON contains no passed tests")
	errInvalidImageID = errors.New("invalid immutable tshark image ID")
	errNoPacketRows   = errors.New("suite packet CSV has no packet rows")
	errMergeCollision = errors.New("merge ownership label collision")
	errMergeCleanup   = errors.New("merge-owned containers remain")
	errUnsafePcap     = errors.New("copied tshark packet capture is empty or unsafe")
)

type routingSuite struct {
	name          string
	kind          string
	project       string
	environment   string
	composePath   string
	tag           string
	packagePath   string
	tshark        string
	containers    []string
	holoArtifacts bool
}

//nolint:funlen // The linear orchestration keeps both guarded suites and final merge visible in one lifecycle.
func (r *runner) runRouting(ctx context.Context) (runErr error) {
	baseProject := os.Getenv("INTEROP_PROJECT_NAME")
	if baseProject == "" {
		baseProject = "gobfd-interop"
	}
	bgpProject := os.Getenv("INTEROP_BGP_PROJECT_NAME")
	if bgpProject == "" {
		bgpProject = baseProject + "-bgp"
	}
	if !projectNamePattern.MatchString(baseProject) || !projectNamePattern.MatchString(bgpProject) {
		return fmt.Errorf("%w: invalid routing Compose project names %q and %q", errUsage, baseProject, bgpProject)
	}
	for _, suite := range []string{baseSuiteName, bgpSuiteName} {
		if err := os.MkdirAll(filepath.Join(r.reportDir, suite), 0o750); err != nil {
			return fmt.Errorf("create routing suite report directory %s: %w", suite, err)
		}
	}
	for _, name := range []string{goTestJSONName, goTestLogName, containersLogName} {
		file, err := secureFile(filepath.Join(r.reportDir, name))
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close routing artifact %s: %w", name, err)
		}
	}

	defer func() {
		runErr = errors.Join(runErr, r.writeJSON("environment.json", map[string]any{
			environmentTarget: "e2e-routing", environmentRunID: r.runID, environmentDevProject: r.devProject,
			"interop_project": baseProject, "interop_bgp_project": bgpProject,
			environmentRuntime: podmanCommand, "suites": []string{baseSuiteName, bgpSuiteName},
		}), r.writeSummary([]summaryRow{
			{summaryTarget, "`make e2e-routing`"},
			{summaryRunID, "`" + r.runID + "`"},
			{summaryExitCode, fmt.Sprintf("`%d`", exitCode(runErr))},
			{summaryGoTestJSON, "`" + goTestJSONName + "`"},
			{summaryGoTestLog, "`" + goTestLogName + "`"},
			{summaryContainerState, "`" + containersJSONName + "`"},
			{summaryContainerLogs, "`" + containersLogName + "`"},
			{"Packet CSV", "`packets.csv`"},
			{"Merged packet capture", "`packets.pcapng`"},
			{"BFD interop artifacts", "`interop/`"},
			{"BGP+BFD interop artifacts", "`interop-bgp/`"},
		}))
	}()

	suites := []routingSuite{
		{
			name: baseSuiteName, kind: "base", project: baseProject, environment: "INTEROP_COMPOSE_FILE",
			composePath: "test/interop/compose.yml", tag: "interop", packagePath: "./test/interop/",
			tshark: "tshark-interop", holoArtifacts: true,
			containers: []string{
				"gobfd-interop", "frr-interop", "bird3-interop", "tshark-interop", "holo-interop",
				"holo-config-interop", "thoro-interop", "scapy-interop",
			},
		},
		{
			name: bgpSuiteName, kind: "bgp", project: bgpProject, environment: "INTEROP_BGP_COMPOSE_FILE",
			composePath: "test/interop-bgp/compose.yml", tag: "interop_bgp", packagePath: "./test/interop-bgp/",
			tshark: "tshark-bgp-interop",
			containers: []string{
				"gobfd-bgp-interop", "gobgp-interop", "frr-bgp-interop", "bird3-bgp-interop",
				"gobfd-exabgp-interop", "exabgp-interop", "tshark-bgp-interop",
			},
		},
	}
	for _, suite := range suites {
		controller, err := interopproject.NewProject(r.root, suite.project, suite.kind, r.stdout, r.stderr)
		if err != nil {
			return fmt.Errorf("create %s project controller: %w", suite.name, err)
		}
		if err := controller.Lifecycle(ctx, func(lifecycleCtx context.Context, active *interopproject.Controller) error {
			return r.runRoutingSuite(lifecycleCtx, active, suite)
		}); err != nil {
			return fmt.Errorf("run routing suite %s: %w", suite.name, err)
		}
	}
	if err := r.mergeRoutingArtifacts(ctx); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "S10.3 routing E2E artifacts: %s\n", r.reportDir); err != nil {
		return fmt.Errorf("write routing artifact path: %w", err)
	}
	return nil
}

func (r *runner) runRoutingSuite(
	ctx context.Context,
	controller *interopproject.Controller,
	suite routingSuite,
) error {
	if err := wait(ctx, 15*time.Second); err != nil {
		return fmt.Errorf("wait for %s topology: %w", suite.name, err)
	}
	devID, err := controller.ServiceContainerID(ctx, r.devProject, "dev")
	if err != nil {
		return fmt.Errorf("resolve dev container for %s: %w", suite.name, err)
	}
	runErr := r.appendLoggedCommand(ctx, suite.name,
		podmanCommand, "exec", devID, "env", "INTEROP_PROJECT_NAME="+suite.project,
		suite.environment+"=/app/"+suite.composePath, "go", "test", "-tags", suite.tag,
		"-json", "-v", "-count=1", "-timeout", "300s", suite.packagePath,
	)
	if err := passedGoTest(filepath.Join(r.reportDir, suite.name, goTestJSONName)); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("validate %s Go test output: %w", suite.name, err))
	}
	if err := r.collectRoutingLogs(ctx, controller, suite); err != nil {
		runErr = errors.Join(runErr, err)
	}
	if suite.holoArtifacts {
		r.collectHoloDiagnostics(ctx, controller, suite.name)
	}
	if err := r.collectRoutingPcap(ctx, controller, suite); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("collect %s packet artifacts: %w", suite.name, err))
	}
	if err := r.recordRoutingContainers(ctx, controller, suite); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("record %s containers: %w", suite.name, err))
	}
	return runErr
}

func (r *runner) appendLoggedCommand(ctx context.Context, suite string, argv ...string) error {
	paths := []struct {
		path  string
		flags int
	}{
		{filepath.Join(r.reportDir, goTestJSONName), os.O_WRONLY | os.O_APPEND},
		{filepath.Join(r.reportDir, goTestLogName), os.O_WRONLY | os.O_APPEND},
		{filepath.Join(r.reportDir, suite, goTestJSONName), os.O_WRONLY | os.O_CREATE | os.O_TRUNC},
	}
	files := make([]*os.File, 0, len(paths))
	for _, artifact := range paths {
		file, err := os.OpenFile(artifact.path, artifact.flags, 0o600)
		if err != nil {
			return errors.Join(fmt.Errorf("open routing Go test artifact %s: %w", artifact.path, err), closeFiles(files))
		}
		files = append(files, file)
	}
	writers := []io.Writer{r.stdout}
	for _, file := range files {
		writers = append(writers, file)
	}
	runErr := r.command(ctx, testTimeout, io.MultiWriter(writers...), r.stderr, argv...)
	closeErr := closeFiles(files)
	if runErr != nil || closeErr != nil {
		return errors.Join(runErr, closeErr)
	}
	data, err := os.ReadFile(filepath.Join(r.reportDir, suite, goTestJSONName))
	if err != nil {
		return fmt.Errorf("read %s Go test JSON: %w", suite, err)
	}
	//nolint:gosec // Suite is selected from the fixed routingSuite table above.
	if err := os.WriteFile(filepath.Join(r.reportDir, suite, goTestLogName), data, 0o600); err != nil {
		return fmt.Errorf("write %s Go test log: %w", suite, err)
	}
	return nil
}

func closeFiles(files []*os.File) error {
	var result error
	for _, file := range files {
		result = errors.Join(result, file.Close())
	}
	if result != nil {
		return fmt.Errorf("close routing artifact files: %w", result)
	}
	return nil
}

func passedGoTest(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Go test JSON: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for {
		var event struct {
			Action string `json:"Action"` //nolint:tagliatelle // go test -json uses exported Go field names.
			Test   string `json:"Test"`   //nolint:tagliatelle // go test -json uses exported Go field names.
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return errNoPassedTests
		} else if err != nil {
			return fmt.Errorf("decode Go test JSON: %w", err)
		}
		if event.Action == "pass" && event.Test != "" {
			return nil
		}
	}
}

func (r *runner) collectRoutingLogs(
	ctx context.Context,
	controller *interopproject.Controller,
	suite routingSuite,
) error {
	logFile, err := os.OpenFile(filepath.Join(r.reportDir, containersLogName), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open routing container log: %w", err)
	}
	defer logFile.Close()
	for _, name := range suite.containers {
		id, resolveErr := controller.ContainerID(ctx, name)
		if resolveErr != nil {
			continue
		}
		if _, writeErr := fmt.Fprintf(logFile, "\n===== %s container %s =====\n", suite.name, name); writeErr != nil {
			return fmt.Errorf("write routing log header: %w", writeErr)
		}
		if commandErr := r.command(ctx, commandTimeout, logFile, logFile, podmanCommand, "logs", id); commandErr != nil {
			continue
		}
	}
	return nil
}

func (r *runner) collectHoloDiagnostics(
	ctx context.Context,
	controller *interopproject.Controller,
	suite string,
) {
	holoID, err := controller.ContainerID(ctx, "holo-interop")
	if err == nil {
		r.bestEffortFile(ctx, filepath.Join(suite, "holo.log"), filepath.Join(suite, "holo-log.err"),
			podmanCommand, "logs", "--tail", "100", holoID)
		r.bestEffortFile(ctx, filepath.Join(suite, "holod.err"), filepath.Join(suite, "holod-exec.err"),
			podmanCommand, "exec", holoID, "cat", "/tmp/holod.err")
	}
	loaderID, err := controller.ContainerID(ctx, "holo-config-interop")
	if err == nil {
		r.bestEffortFile(ctx, filepath.Join(suite, "holo-config.log"), filepath.Join(suite, "holo-config-log.err"),
			podmanCommand, "logs", "--tail", "100", loaderID)
	}
}

func (r *runner) recordRoutingContainers(
	ctx context.Context,
	controller *interopproject.Controller,
	suite routingSuite,
) error {
	ids := make([]string, 0, len(suite.containers))
	for _, name := range suite.containers {
		id, err := controller.ContainerID(ctx, name)
		if err == nil {
			ids = append(ids, id)
		}
	}
	path := filepath.Join(r.reportDir, suite.name, containersJSONName)
	if len(ids) == 0 {
		if err := os.WriteFile(path, []byte("[]\n"), 0o600); err != nil {
			return fmt.Errorf("write empty %s container inventory: %w", suite.name, err)
		}
		return nil
	}
	arguments := append([]string{podmanCommand, "inspect"}, ids...)
	return r.commandToFile(ctx, path, filepath.Join(r.reportDir, suite.name, "containers.err"),
		arguments...)
}

func (r *runner) collectRoutingPcap(
	ctx context.Context,
	controller *interopproject.Controller,
	suite routingSuite,
) error {
	id, err := controller.ContainerID(ctx, suite.tshark)
	if err != nil {
		return fmt.Errorf("resolve %s tshark container: %w", suite.name, err)
	}
	imageID, err := r.commandText(ctx, podmanCommand, "inspect", "--type", "container", "--format", "{{.Image}}", id)
	if err != nil {
		return fmt.Errorf("inspect immutable tshark image ID: %w", err)
	}
	imageID = strings.TrimSpace(imageID)
	if !imageIDPattern.MatchString(imageID) {
		return fmt.Errorf("%w %q", errInvalidImageID, imageID)
	}
	if imageErr := r.command(
		ctx, commandTimeout, io.Discard, r.stderr, podmanCommand, "image", "exists", imageID,
	); imageErr != nil {
		return fmt.Errorf("immutable tshark image ID is unavailable %s: %w", imageID, imageErr)
	}
	if writeErr := routingartifacts.WriteImageID(
		r.reportDir, filepath.Join(suite.name, "tshark-image-id"), imageID,
	); writeErr != nil {
		return fmt.Errorf("persist %s tshark image ID: %w", suite.name, writeErr)
	}
	pcapPath := filepath.Join(r.reportDir, suite.name, "packets.pcapng")
	if copyErr := r.commandToFile(ctx, pcapPath, filepath.Join(r.reportDir, suite.name, "packets.err"),
		podmanCommand, "exec", id, "cat", "/captures/bfd.pcapng"); copyErr != nil {
		return fmt.Errorf("copy tshark packet capture: %w", copyErr)
	}
	info, err := os.Lstat(pcapPath)
	if err != nil {
		return fmt.Errorf("stat copied tshark packet capture: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errUnsafePcap
	}
	csvPath := filepath.Join(r.reportDir, suite.name, "packets.csv")
	if err := r.commandToFile(ctx, csvPath, filepath.Join(r.reportDir, suite.name, "packets-csv.err"),
		podmanCommand, "exec", id, "tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd", "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst", "-e", "udp.srcport", "-e", "udp.dstport",
		"-e", "bfd.sta", "-e", "bfd.diag", "-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=,"); err != nil {
		return fmt.Errorf("decode tshark packet capture: %w", err)
	}
	if err := appendPacketCSV(r.reportDir, suite.name, csvPath); err != nil {
		return fmt.Errorf("append tshark packet CSV: %w", err)
	}
	return nil
}

func appendPacketCSV(reportDir, suite, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open suite packet CSV: %w", err)
	}
	rows, err := csv.NewReader(file).ReadAll()
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("read suite packet CSV: %w", err), closeErr)
	}
	if len(rows) < minimumPacketRows {
		return errNoPacketRows
	}
	outputPath := filepath.Join(reportDir, "packets.csv")
	_, statErr := os.Stat(outputPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat aggregate packet CSV: %w", statErr)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	output, err := os.OpenFile(outputPath, flags, 0o600)
	if err != nil {
		return fmt.Errorf("open aggregate packet CSV: %w", err)
	}
	writer := csv.NewWriter(output)
	if errors.Is(statErr, os.ErrNotExist) {
		rows[0] = append([]string{"suite"}, rows[0]...)
		if err := writer.Write(rows[0]); err != nil {
			return errors.Join(fmt.Errorf("write aggregate packet header: %w", err), output.Close())
		}
	}
	for _, row := range rows[1:] {
		if err := writer.Write(append([]string{suite}, row...)); err != nil {
			return errors.Join(fmt.Errorf("write aggregate packet row: %w", err), output.Close())
		}
	}
	writer.Flush()
	return errors.Join(writer.Error(), output.Close())
}

func (r *runner) mergeRoutingArtifacts(ctx context.Context) (runErr error) {
	if err := routingartifacts.Merge(r.reportDir, containersJSONName, []routingartifacts.Input{
		{Name: baseSuiteName, Path: filepath.Join(baseSuiteName, containersJSONName)},
		{Name: bgpSuiteName, Path: filepath.Join(bgpSuiteName, containersJSONName)},
	}); err != nil {
		return fmt.Errorf("merge routing container inventories: %w", err)
	}
	baseImage, err := routingartifacts.ReadImageID(r.reportDir, filepath.Join(baseSuiteName, "tshark-image-id"))
	if err != nil {
		return fmt.Errorf("read base tshark image ID: %w", err)
	}
	bgpImage, err := routingartifacts.ReadImageID(r.reportDir, filepath.Join(bgpSuiteName, "tshark-image-id"))
	if err != nil {
		return fmt.Errorf("read BGP tshark image ID: %w", err)
	}
	for _, image := range []string{baseImage, bgpImage} {
		if imageErr := r.command(
			ctx, commandTimeout, io.Discard, r.stderr, podmanCommand, "image", "exists", image,
		); imageErr != nil {
			return fmt.Errorf("routing tshark image %s is unavailable: %w", image, imageErr)
		}
	}
	for _, suite := range []string{baseSuiteName, bgpSuiteName} {
		path := filepath.Join(r.reportDir, suite, "packets.pcapng")
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("required packet capture is missing, empty, or unsafe %s: %w", path, statErr)
		}
	}
	owner := r.runID
	ids, err := r.queryLabelledContainers(ctx, mergeOwnerLabelKey, owner)
	if err != nil {
		return err
	}
	if len(ids) != 0 {
		return fmt.Errorf("%w %s=%s", errMergeCollision, mergeOwnerLabelKey, owner)
	}
	defer func() {
		runErr = errors.Join(runErr, r.removeLabelledContainers(ctx, mergeOwnerLabelKey, owner))
	}()
	return r.commandToFile(ctx, filepath.Join(r.reportDir, "mergecap.out"), filepath.Join(r.reportDir, "mergecap.err"),
		podmanCommand, "run", "--label", mergeOwnerLabelKey+"="+owner, "--entrypoint", "/usr/bin/mergecap",
		"-v", r.reportDir+":/reports:z", baseImage, "-w", "/reports/packets.pcapng",
		"/reports/interop/packets.pcapng", "/reports/interop-bgp/packets.pcapng")
}

func (r *runner) queryLabelledContainers(ctx context.Context, key, value string) ([]string, error) {
	output, err := r.commandText(ctx, podmanCommand, "ps", "-a", "--no-trunc",
		"--filter", "label="+key+"="+value, "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(output), nil
}

func (r *runner) removeLabelledContainers(ctx context.Context, key, value string) error {
	ids, err := r.queryLabelledContainers(ctx, key, value)
	if err != nil {
		return err
	}
	for _, id := range ids {
		label, inspectErr := r.commandText(ctx, podmanCommand, "inspect", "--type", "container", "--format",
			`{{ index .Config.Labels "`+key+`" }}`, id)
		if inspectErr != nil {
			return fmt.Errorf("validate merge container %s ownership: %w", id, inspectErr)
		}
		if strings.TrimSpace(label) != value {
			return fmt.Errorf("%w: container %s has unexpected owner %q", errMergeCleanup, id, label)
		}
		if removeErr := r.command(
			ctx, commandTimeout, io.Discard, r.stderr, podmanCommand, "rm", "-f", "--", id,
		); removeErr != nil {
			return removeErr
		}
	}
	remaining, err := r.queryLabelledContainers(ctx, key, value)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf("%w: %s", errMergeCleanup, strings.Join(remaining, " "))
	}
	return nil
}

func (r *runner) commandText(ctx context.Context, argv ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := r.command(ctx, commandTimeout, &stdout, &stderr, argv...); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (r *runner) commandToFile(ctx context.Context, outputPath, errorPath string, argv ...string) error {
	output, err := secureFile(outputPath)
	if err != nil {
		return err
	}
	errorOutput, err := secureFile(errorPath)
	if err != nil {
		return errors.Join(err, output.Close())
	}
	runErr := r.command(ctx, testTimeout, output, errorOutput, argv...)
	return errors.Join(runErr, output.Close(), errorOutput.Close())
}
