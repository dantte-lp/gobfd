// Package e2erunner owns repository E2E execution and artifact collection.
package e2erunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // The runner invokes fixed Go, Podman, and Compose argument vectors.
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	commandTimeout        = 2 * time.Minute
	testTimeout           = 6 * time.Minute
	podmanCommand         = "podman"
	environmentTarget     = "target"
	environmentRunID      = "run_id"
	environmentDevProject = "dev_project"
	environmentRuntime    = "podman_runtime"
	summaryTarget         = "Target"
	summaryRunID          = "Run ID"
	summaryExitCode       = "Exit code"
	summaryGoTestJSON     = "Go test JSON"
	summaryGoTestLog      = "Go test log"
	summaryContainerState = "Container state"
	summaryContainerLogs  = "Container logs"
	goTestJSONName        = "go-test.json"
	goTestLogName         = "go-test.log"
	containersJSONName    = "containers.json"
	containersLogName     = "containers.log"
)

var (
	projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	errUsage           = errors.New("invalid e2ectl usage")
)

// ExitError preserves the exit status of a failed E2E producer.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

type runner struct {
	root       string
	target     string
	runID      string
	reportRel  string
	reportDir  string
	devProject string
	stdout     io.Writer
	stderr     io.Writer
}

// Run executes one E2E target.
func Run(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: usage: e2ectl {linux|overlay|rfc|routing|vendor}", errUsage)
	}
	target := args[0]
	if !validTarget(target) {
		return fmt.Errorf("%w: unknown target %q", errUsage, target)
	}

	project := os.Getenv("COMPOSE_PROJECT_NAME")
	if project == "" {
		project = normalizeProject(filepath.Base(root))
	}
	if !projectNamePattern.MatchString(project) {
		return fmt.Errorf("%w: invalid Compose project name %q", errUsage, project)
	}

	runID := time.Now().UTC().Format("20060102T150405Z")
	if target == "routing" {
		runID = fmt.Sprintf("%s-%d", time.Now().UTC().Format("20060102T150405000000000Z"), os.Getpid())
	}
	reportRel := filepath.ToSlash(filepath.Join("reports", "e2e", target, runID))
	r := &runner{
		root:       root,
		target:     target,
		runID:      runID,
		reportRel:  reportRel,
		reportDir:  filepath.Join(root, filepath.FromSlash(reportRel)),
		devProject: project,
		stdout:     stdout,
		stderr:     stderr,
	}
	if err := os.MkdirAll(r.reportDir, 0o750); err != nil {
		return fmt.Errorf("create %s report directory: %w", target, err)
	}

	switch target {
	case "linux":
		return r.runLinux(ctx)
	case "overlay":
		return r.runOverlay(ctx)
	case "rfc":
		return r.runRFC(ctx)
	case "routing":
		return r.runRouting(ctx)
	case "vendor":
		return r.runVendor(ctx)
	default:
		panic("validated E2E target is unhandled")
	}
}

func validTarget(target string) bool {
	switch target {
	case "linux", "overlay", "rfc", "routing", "vendor":
		return true
	default:
		return false
	}
}

func normalizeProject(name string) string {
	name = strings.ToLower(name)
	var result strings.Builder
	result.Grow(len(name))
	lastDash := false
	for _, char := range name {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-'
		if valid {
			result.WriteRune(char)
			lastDash = char == '-'
			continue
		}
		if result.Len() > 0 && !lastDash {
			result.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func (r *runner) runLinux(ctx context.Context) (runErr error) {
	containerName := "e2e-linux-" + r.runID
	testImage := os.Getenv("E2E_LINUX_IMAGE")
	if testImage == "" {
		testImage = "localhost/" + r.devProject + "_dev:latest"
	}
	defer func() {
		r.bestEffortFile(
			ctx, containersJSONName, "containers.err", podmanCommand,
			"ps", "-a", "--filter", "name="+containerName, "--format", "json",
		)
		r.bestEffortFile(ctx, containersLogName, "containers-log.err", podmanCommand, "logs", containerName)
		r.bestEffortCommand(ctx, podmanCommand, "rm", "-f", containerName)
		runErr = errors.Join(runErr, r.writeJSON("environment.json", map[string]any{
			environmentTarget:     "e2e-linux",
			environmentRunID:      r.runID,
			environmentDevProject: r.devProject,
			"test_image":          testImage,
			environmentRuntime:    podmanCommand,
			"isolation":           "podman --network none --cap-add NET_ADMIN --cap-add NET_RAW",
		}), r.writeSummary([]summaryRow{
			{summaryTarget, "`make e2e-linux`"},
			{summaryRunID, "`" + r.runID + "`"},
			{summaryExitCode, fmt.Sprintf("`%d`", exitCode(runErr))},
			{"Isolation", "`podman --network none`"},
			{summaryGoTestJSON, "`" + goTestJSONName + "`"},
			{summaryGoTestLog, "`" + goTestLogName + "`"},
			{summaryContainerState, "`" + containersJSONName + "`"},
			{summaryContainerLogs, "`" + containersLogName + "`"},
			{"Linux events", "`link-events.json`"},
			{"LAG backend evidence", "`lag-backends.json`"},
		}))
	}()

	if err := r.command(ctx, testTimeout, r.stdout, r.stderr, r.composeDev(
		"exec", "-T", "dev", "env", "CGO_ENABLED=0", "go", "test", "-tags", "e2e_linux", "-c",
		"-o", "/app/"+r.reportRel+"/e2e-linux.test", "./test/e2e/linux/",
	)...); err != nil {
		return err
	}
	if err := r.command(ctx, commandTimeout, io.Discard, r.stderr, podmanCommand, "create",
		"--name", containerName, "--network", "none", "--cap-drop", "ALL", "--cap-add", "NET_ADMIN",
		"--cap-add", "NET_RAW", "--security-opt", "label=disable", "--workdir", "/report",
		"-e", "E2E_LINUX_REPORT_DIR=/report", "-v", r.reportDir+":/report:z", testImage,
		"go", "tool", "test2json", "-t", "./e2e-linux.test", "-test.v", "-test.timeout=120s",
	); err != nil {
		return err
	}
	if err := r.loggedCommand(ctx, podmanCommand, "start", "-a", containerName); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "S10.5 Linux E2E artifacts: %s\n", r.reportDir); err != nil {
		return fmt.Errorf("write Linux artifact path: %w", err)
	}
	return nil
}

func (r *runner) runOverlay(ctx context.Context) (runErr error) {
	defer func() {
		r.collectDevDiagnostics(ctx)
		runErr = errors.Join(runErr, r.writeJSON("environment.json", map[string]any{
			environmentTarget: "e2e-overlay", environmentRunID: r.runID, environmentDevProject: r.devProject,
			environmentRuntime:  podmanCommand,
			"reserved_backends": []string{"kernel", "ovs", "ovn", "cilium", "calico", "nsx"},
		}), r.writeSummary([]summaryRow{
			{summaryTarget, "`make e2e-overlay`"},
			{summaryRunID, "`" + r.runID + "`"},
			{summaryExitCode, fmt.Sprintf("`%d`", exitCode(runErr))},
			{summaryGoTestJSON, "`" + goTestJSONName + "`"},
			{summaryGoTestLog, "`" + goTestLogName + "`"},
			{summaryContainerState, "`" + containersJSONName + "`"},
			{summaryContainerLogs, "`" + containersLogName + "`"},
			{"Packet evidence", "`packets.csv`"},
		}))
	}()

	err := r.loggedCommand(ctx, r.composeDev(
		"exec", "-T", "dev", "env", "E2E_OVERLAY_PACKET_CSV=/app/"+r.reportRel+"/packets.csv",
		"go", "test", "-tags", "e2e_overlay", "-json", "-v", "-count=1", "./test/e2e/overlay/",
	)...)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(r.stdout, "S10.4 overlay E2E artifacts: %s\n", r.reportDir); err != nil {
		return fmt.Errorf("write overlay artifact path: %w", err)
	}
	return nil
}

func (r *runner) runRFC(ctx context.Context) (runErr error) {
	composeFile := filepath.Join(r.root, "test", "interop-rfc", "compose.yml")
	compose := func(args ...string) []string {
		return append([]string{podmanCommand, "compose", "-f", composeFile}, args...)
	}
	defer func() {
		r.bestEffortFile(ctx, containersLogName, "containers-log.err", compose("logs")...)
		r.bestEffortFile(ctx, containersJSONName, "containers.err", podmanCommand, "inspect",
			"gobfd-rfc-interop", "tshark-rfc-interop", "frr-rfc-interop", "gobfd-rfc9384-interop",
			"gobgp-rfc-interop", "frr-rfc-bgp-interop", "frr-rfc-unsolicited-interop", "echo-reflector-interop")
		r.bestEffortFile(
			ctx, "packets.pcapng", "packets.err", podmanCommand,
			"exec", "tshark-rfc-interop", "cat", "/captures/bfd.pcapng",
		)
		r.bestEffortFile(ctx, "packets.csv", "packets-csv.err", podmanCommand, "exec", "tshark-rfc-interop",
			"tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd || udp.port == 3785", "-T", "fields",
			"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst", "-e", "udp.srcport", "-e", "udp.dstport",
			"-e", "bfd.sta", "-e", "bfd.diag", "-e", "bfd.desired_min_tx_interval", "-e", "bfd.required_min_rx_interval",
			"-e", "bfd.required_min_echo_interval", "-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
			"-E", "header=y", "-E", "separator=,")
		r.bestEffortCommand(ctx, compose("down", "--volumes", "--remove-orphans")...)
		runErr = errors.Join(runErr, r.writeJSON("environment.json", map[string]any{
			environmentTarget: "e2e-rfc", environmentRunID: r.runID, environmentDevProject: r.devProject,
			"compose_file": composeFile, environmentRuntime: podmanCommand,
			"rfc_scenarios": []string{"RFC 7419", "RFC 9384", "RFC 9468", "RFC 9747"},
		}), r.writeSummary([]summaryRow{
			{summaryTarget, "`make e2e-rfc`"},
			{summaryRunID, "`" + r.runID + "`"},
			{summaryExitCode, fmt.Sprintf("`%d`", exitCode(runErr))},
			{"RFC coverage", "RFC 7419, RFC 9384, RFC 9468, RFC 9747"},
			{summaryGoTestJSON, "`" + goTestJSONName + "`"},
			{summaryGoTestLog, "`" + goTestLogName + "`"},
			{summaryContainerState, "`" + containersJSONName + "`"},
			{summaryContainerLogs, "`" + containersLogName + "`"},
			{"Packet capture", "`packets.pcapng`"},
			{"Packet CSV", "`packets.csv`"},
		}))
	}()

	if err := r.command(ctx, 10*time.Minute, r.stdout, r.stderr, compose("build", "--no-cache")...); err != nil {
		return err
	}
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, compose("up", "-d")...); err != nil {
		return err
	}
	if err := wait(ctx, 15*time.Second); err != nil {
		return fmt.Errorf("wait for RFC topology: %w", err)
	}
	if err := r.loggedCommand(ctx, r.composeDev(
		"exec", "-T", "dev", "env", "INTEROP_RFC_COMPOSE_FILE=/app/test/interop-rfc/compose.yml",
		"go", "test", "-tags", "interop_rfc", "-json", "-v", "-count=1", "-timeout", "300s", "./test/interop-rfc/",
	)...); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "S10.4 RFC E2E artifacts: %s\n", r.reportDir); err != nil {
		return fmt.Errorf("write RFC artifact path: %w", err)
	}
	return nil
}

func (r *runner) runVendor(ctx context.Context) (runErr error) {
	defer func() {
		r.collectDevDiagnostics(ctx)
		runErr = errors.Join(runErr, r.writeJSON("environment.json", map[string]any{
			environmentTarget: "e2e-vendor", environmentRunID: r.runID, environmentDevProject: r.devProject,
			environmentRuntime: podmanCommand, "containerlab_runtime": podmanCommand,
			"topology": "test/interop-clab/gobfd-vendors.clab.yml", "public_ci_default": "skip-topology",
		}), r.writeSummary([]summaryRow{
			{summaryTarget, "`make e2e-vendor`"},
			{summaryRunID, "`" + r.runID + "`"},
			{summaryExitCode, fmt.Sprintf("`%d`", exitCode(runErr))},
			{"Runtime", "`podman`"},
			{"Containerlab runtime", "`podman`"},
			{"Topology", "`test/interop-clab/gobfd-vendors.clab.yml`"},
			{summaryGoTestJSON, "`" + goTestJSONName + "`"},
			{summaryGoTestLog, "`" + goTestLogName + "`"},
			{summaryContainerState, "`" + containersJSONName + "`"},
			{summaryContainerLogs, "`" + containersLogName + "`"},
			{"Vendor profiles", "`vendor-profiles.json`"},
			{"Image availability", "`vendor-images.json`"},
			{"Skip summary", "`skip-summary.json`"},
			{"Primary profiles", "`arista-ceos,nokia-srlinux,sonic-vs,vyos`"},
			{"Deferred profiles", "`cisco-xrd`"},
		}))
	}()

	if err := r.writeVendorImages(ctx); err != nil {
		return err
	}
	if err := r.loggedCommand(ctx, r.composeDev(
		"exec", "-T", "dev", "env", "E2E_VENDOR_REPORT_DIR=/app/"+r.reportRel,
		"go", "test", "-tags", "e2e_vendor", "-json", "-v", "-count=1", "-timeout", "120s", "./test/e2e/vendor/",
	)...); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "S10.6 vendor E2E artifacts: %s\n", r.reportDir); err != nil {
		return fmt.Errorf("write vendor artifact path: %w", err)
	}
	return nil
}

type vendorManifest struct {
	Profiles []vendorProfile `json:"profiles"`
}

type vendorProfile struct {
	ID           string   `json:"id"`
	ProfileClass string   `json:"profile_class"`
	Images       []string `json:"images"`
	SkipPolicy   string   `json:"skip_policy"`
}

type vendorImageEvidence struct {
	ProfileID    string   `json:"profile_id"`
	ProfileClass string   `json:"profile_class"`
	Images       []string `json:"images"`
	Available    []string `json:"available"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason"`
}

func (r *runner) writeVendorImages(ctx context.Context) error {
	manifestPath := filepath.Join(r.root, "test", "e2e", "vendor", "profiles.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read vendor profiles: %w", err)
	}
	var manifest vendorManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode vendor profiles: %w", err)
	}
	evidence := make([]vendorImageEvidence, 0, len(manifest.Profiles))
	for _, profile := range manifest.Profiles {
		item := vendorImageEvidence{
			ProfileID: profile.ID, ProfileClass: profile.ProfileClass, Images: profile.Images,
			Available: []string{}, Status: "skipped", Reason: profile.SkipPolicy,
		}
		for _, image := range profile.Images {
			if err := r.command(
				ctx, commandTimeout, io.Discard, io.Discard, podmanCommand, "image", "exists", image,
			); err == nil {
				item.Available = append(item.Available, image)
			}
		}
		if len(item.Available) > 0 {
			item.Status = "available"
			item.Reason = ""
		}
		evidence = append(evidence, item)
	}
	return r.writeJSON("vendor-images.json", evidence)
}

func (r *runner) composeDev(args ...string) []string {
	base := make([]string, 0, 6+len(args))
	base = append(base,
		podmanCommand, "compose", "-p", r.devProject,
		"-f", filepath.Join(r.root, "deployments", "compose", "compose.dev.yml"),
	)
	return append(base, args...)
}

func (r *runner) collectDevDiagnostics(ctx context.Context) {
	r.bestEffortFile(ctx, containersJSONName, "containers.err", podmanCommand, "ps", "-a",
		"--filter", "label=io.podman.compose.project="+r.devProject, "--format", "json")
	r.bestEffortFile(ctx, containersLogName, "containers-log.err", r.composeDev("logs")...)
}

func (r *runner) loggedCommand(ctx context.Context, argv ...string) error {
	jsonFile, err := secureFile(filepath.Join(r.reportDir, goTestJSONName))
	if err != nil {
		return err
	}
	defer jsonFile.Close()
	logFile, err := secureFile(filepath.Join(r.reportDir, goTestLogName))
	if err != nil {
		return err
	}
	defer logFile.Close()
	return r.command(ctx, testTimeout, io.MultiWriter(r.stdout, jsonFile, logFile), r.stderr, argv...)
}

func (r *runner) command(
	ctx context.Context,
	timeout time.Duration,
	stdout io.Writer,
	stderr io.Writer,
	argv ...string,
) error {
	if len(argv) == 0 {
		return fmt.Errorf("run empty command: %w", errUsage)
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	//nolint:gosec // Executables and argument layouts are selected by fixed target implementations.
	cmd := exec.CommandContext(commandCtx, argv[0], argv[1:]...)
	cmd.Dir = r.root
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if commandCtx.Err() != nil {
			return fmt.Errorf("run %s: %w", argv[0], commandCtx.Err())
		}
		if processErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return &ExitError{Code: processErr.ExitCode(), Err: fmt.Errorf("run %s: %w", argv[0], err)}
		}
		return fmt.Errorf("run %s: %w", argv[0], err)
	}
	return nil
}

func (r *runner) bestEffortFile(ctx context.Context, outputName, errorName string, argv ...string) {
	output, err := secureFile(filepath.Join(r.reportDir, outputName))
	if err != nil {
		return
	}
	defer output.Close()
	errOutput, err := secureFile(filepath.Join(r.reportDir, errorName))
	if err != nil {
		return
	}
	defer errOutput.Close()
	if err := r.command(ctx, commandTimeout, output, errOutput, argv...); err != nil {
		if _, writeErr := fmt.Fprintf(errOutput, "artifact collection failed: %v\n", err); writeErr != nil {
			return
		}
	}
}

func (r *runner) bestEffortCommand(ctx context.Context, argv ...string) {
	if err := r.command(ctx, commandTimeout, io.Discard, io.Discard, argv...); err != nil {
		if _, writeErr := fmt.Fprintf(r.stderr, "best-effort cleanup failed: %v\n", err); writeErr != nil {
			return
		}
	}
}

func secureFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create artifact %s: %w", path, err)
	}
	return file, nil
}

func (r *runner) writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(r.reportDir, name), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

type summaryRow struct {
	field string
	value string
}

func (r *runner) writeSummary(rows []summaryRow) error {
	var contents strings.Builder
	fmt.Fprintf(&contents, "# e2e-%s Summary\n\n| Field | Value |\n|---|---|\n", r.target)
	for _, row := range rows {
		fmt.Fprintf(&contents, "| %s | %s |\n", row.field, row.value)
	}
	if err := os.WriteFile(filepath.Join(r.reportDir, "summary.md"), []byte(contents.String()), 0o600); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr.Code
	}
	return 1
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait interrupted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
