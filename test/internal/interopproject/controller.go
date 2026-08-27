// Package interopproject owns the manual Compose interoperability lifecycle.
package interopproject

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // The controller invokes fixed Podman commands with explicit argument vectors.
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultProjectName = "gobfd-interop"
	projectLabelKey    = "com.docker.compose.project"
	holoCLIVersion     = "Holo command-line interface 0.5.0"
	commandTimeout     = 2 * time.Minute
	cleanupTimeout     = 5 * time.Minute
)

var (
	projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	errUsage           = errors.New("invalid interopctl usage")
	errControl         = errors.New("interop project control failed")
)

// UsageError identifies a command-line contract violation.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

func (e *UsageError) Unwrap() error { return errUsage }

// ChildExitError preserves the status of a command executed by lock-run.
type ChildExitError struct {
	Code int
	Err  error
}

func (e *ChildExitError) Error() string { return e.Err.Error() }

func (e *ChildExitError) Unwrap() error { return e.Err }

// Controller owns one exact Compose project.
type Controller struct {
	root        string
	composeFile string
	projectName string
	required    []string
	optional    []string
	stdout      io.Writer
	stderr      io.Writer
	lock        *os.File
	mutation    bool
	keepProject bool
}

// New constructs a controller from the repository root and environment.
func New(root string, stdout, stderr io.Writer) (*Controller, error) {
	projectName := os.Getenv("INTEROP_PROJECT_NAME")
	if projectName == "" {
		projectName = defaultProjectName
	}
	if !projectNamePattern.MatchString(projectName) {
		return nil, &UsageError{Message: fmt.Sprintf(
			"invalid INTEROP_PROJECT_NAME %q: use lowercase letters, digits, dashes, and underscores",
			projectName,
		)}
	}

	var required, optional []string
	switch kind := os.Getenv("INTEROP_PROJECT_KIND"); kind {
	case "", "base":
		required = []string{
			"gobfd-interop", "frr-interop", "bird3-interop", "tshark-interop",
			"holo-interop", "holo-config-interop", "thoro-interop",
		}
		optional = []string{"scapy-interop"}
	case "bgp":
		required = []string{
			"gobfd-bgp-interop", "gobgp-interop", "tshark-bgp-interop", "frr-bgp-interop",
			"bird3-bgp-interop", "gobfd-exabgp-interop", "exabgp-interop",
		}
	default:
		return nil, &UsageError{Message: fmt.Sprintf(
			"invalid INTEROP_PROJECT_KIND %q: use base or bgp", kind,
		)}
	}

	return &Controller{
		root:        root,
		composeFile: filepath.Join(root, "test", "interop", "compose.yml"),
		projectName: projectName,
		required:    required,
		optional:    optional,
		stdout:      stdout,
		stderr:      stderr,
	}, nil
}

// Run executes one controller action.
func (c *Controller) Run(ctx context.Context, args []string) (runErr error) {
	defer func() {
		if c.mutation && !c.keepProject {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			runErr = errors.Join(runErr, c.cleanup(cleanupCtx))
			cancel()
		}
		runErr = errors.Join(runErr, c.releaseLock())
	}()

	if len(args) == 0 {
		return c.usage()
	}
	switch args[0] {
	case "up":
		if len(args) != 1 {
			return c.usage()
		}
		return c.start(ctx)
	case "down":
		if len(args) != 1 {
			return c.usage()
		}
		return c.stop(ctx)
	case "logs":
		if len(args) != 1 {
			return c.usage()
		}
		return c.logs(ctx)
	case "capture", "pcap", "summary":
		if len(args) != 1 {
			return c.usage()
		}
		return c.tshark(ctx, args[0])
	case "lock-run":
		return c.lockRun(ctx, args[1:])
	case "dev-exec":
		return c.devExec(ctx, args[1:])
	default:
		return c.usage()
	}
}

func (c *Controller) usage() error {
	return &UsageError{Message: "usage: interopctl {up|down|logs|capture|pcap|summary|lock-run|dev-exec}"}
}

//nolint:cyclop // The linear fail-closed provider gate mirrors the lifecycle contract.
func (c *Controller) start(ctx context.Context) error {
	if err := c.acquireLock(); err != nil {
		return err
	}
	resources, err := c.queryProjectResources(ctx)
	if err != nil {
		return err
	}
	if !resources.empty() {
		return fmt.Errorf("%w: Compose project %s already owns resources; refusing collision\n%s", errControl,
			c.projectName, resources.String())
	}
	if opErr := c.assertFixedNamesAvailable(ctx); opErr != nil {
		return opErr
	}
	c.mutation = true
	if opErr := c.compose(ctx, 10*time.Minute, "build"); opErr != nil {
		return opErr
	}
	if opErr := c.compose(ctx, commandTimeout, "up", "-d", "holo", "holo-config"); opErr != nil {
		return opErr
	}
	loaderID, err := c.resolveContainerID(ctx, "holo-config-interop")
	if err != nil {
		return err
	}
	waitStatus, err := c.podmanText(ctx, 45*time.Second, "wait", loaderID)
	if err != nil {
		return fmt.Errorf("wait for holo-config provider: %w", err)
	}
	inspectStatus, err := c.podmanText(ctx, 10*time.Second, "inspect", "--format", "{{.State.ExitCode}}", loaderID)
	if err != nil {
		return fmt.Errorf("inspect holo-config provider exit status: %w", err)
	}
	waitCode, waitErr := strconv.Atoi(strings.TrimSpace(waitStatus))
	inspectCode, inspectErr := strconv.Atoi(strings.TrimSpace(inspectStatus))
	if waitErr != nil || inspectErr != nil || waitCode != inspectCode || waitCode != 0 {
		return fmt.Errorf("%w: holo-config provider gate failed: wait=%s inspect=%s", errControl,
			strings.TrimSpace(waitStatus), strings.TrimSpace(inspectStatus))
	}
	if err := c.verifyHoloConfiguration(ctx, loaderID); err != nil {
		return err
	}
	if opErr := c.compose(
		ctx, commandTimeout, "up", "-d", "--no-deps", "gobfd", "frr", "bird3", "tshark", "thoro",
	); opErr != nil {
		return opErr
	}
	c.keepProject = true
	return nil
}

func (c *Controller) stop(ctx context.Context) error {
	if err := c.acquireLock(); err != nil {
		return err
	}
	return c.cleanup(ctx)
}

func (c *Controller) lockRun(ctx context.Context, args []string) error {
	command, err := commandAfterSeparator("lock-run", args)
	if err != nil {
		return err
	}
	if err := c.acquireLock(); err != nil {
		return err
	}
	if err := c.assertExistingProject(ctx); err != nil {
		return err
	}
	//nolint:gosec // lock-run intentionally executes caller argv after ownership checks.
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = c.root
	cmd.Stdin = os.Stdin
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return &ChildExitError{Code: exitErr.ExitCode(), Err: err}
		}
		return fmt.Errorf("run locked command: %w", err)
	}
	return nil
}

func (c *Controller) devExec(ctx context.Context, args []string) error {
	command, err := commandAfterSeparator("dev-exec", args)
	if err != nil {
		return err
	}
	devProject := os.Getenv("COMPOSE_PROJECT_NAME")
	if !projectNamePattern.MatchString(devProject) {
		return &UsageError{Message: fmt.Sprintf("invalid COMPOSE_PROJECT_NAME %q for dev exec", devProject)}
	}
	devID, err := c.resolveServiceContainerID(ctx, devProject, "dev")
	if err != nil {
		return err
	}
	return c.podmanStream(ctx, commandTimeout, append([]string{"exec", devID}, command...)...)
}

func commandAfterSeparator(action string, args []string) ([]string, error) {
	if len(args) < 2 || args[0] != "--" {
		return nil, &UsageError{Message: fmt.Sprintf("usage: interopctl %s -- command [args...]", action)}
	}
	return args[1:], nil
}

func (c *Controller) logs(ctx context.Context) error {
	if err := c.acquireLock(); err != nil {
		return err
	}
	for _, name := range append(append([]string{}, c.required...), c.optional...) {
		id, err := c.resolveContainerID(ctx, name)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(c.stdout, "\n===== %s =====\n", name); err != nil {
			return fmt.Errorf("write log header: %w", err)
		}
		if err := c.podmanStream(ctx, commandTimeout, "logs", id); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) tshark(ctx context.Context, mode string) error {
	if err := c.acquireLock(); err != nil {
		return err
	}
	id, err := c.resolveContainerID(ctx, "tshark-interop")
	if err != nil {
		return err
	}
	args := []string{"exec", id, "tshark"}
	switch mode {
	case "capture":
		args = append(args, "-i", "any", "-f", "udp port 3784", "-V")
	case "pcap":
		args = append(args, "-r", "/captures/bfd.pcapng", "-V", "-Y", "bfd")
	case "summary":
		args = append(args,
			"-r", "/captures/bfd.pcapng", "-Y", "bfd", "-T", "fields",
			"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst",
			"-e", "bfd.version", "-e", "bfd.diag", "-e", "bfd.sta", "-e", "bfd.flags",
			"-e", "bfd.detect_time_multiplier", "-e", "bfd.my_discriminator",
			"-e", "bfd.your_discriminator", "-e", "bfd.desired_min_tx_interval",
			"-e", "bfd.required_min_rx_interval", "-E", "header=y", "-E", "separator=,",
		)
	}
	return c.podmanStream(ctx, commandTimeout, args...)
}

func (c *Controller) compose(ctx context.Context, timeout time.Duration, args ...string) error {
	full := append([]string{"compose", "-p", c.projectName, "-f", c.composeFile}, args...)
	return c.podmanStream(ctx, timeout, full...)
}

func (c *Controller) podmanText(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	//nolint:gosec // Binary is fixed; argv is never shell-expanded.
	cmd := exec.CommandContext(commandCtx, "podman", args...)
	cmd.Dir = c.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf(
			"podman %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()),
		)
	}
	return stdout.String(), nil
}

func (c *Controller) podmanStream(ctx context.Context, timeout time.Duration, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "podman", args...)
	cmd.Dir = c.root
	cmd.Stdin = os.Stdin
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (c *Controller) acquireLock() error {
	if c.lock != nil {
		return nil
	}
	lockDir, err := lockDirectory()
	if err != nil {
		return err
	}
	lockPath := filepath.Join(lockDir, c.projectName+".lock")
	if validateErr := validateLockFile(lockPath, true); validateErr != nil {
		return validateErr
	}
	file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open interop lock %s: %w", lockPath, err)
	}
	if err := validateOpenLockFile(lockPath, file); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.Join(
			fmt.Errorf("%w: Compose project %s is locked by another runner: %w", errControl, c.projectName, err),
			file.Close(),
		)
	}
	c.lock = file
	return nil
}

func (c *Controller) releaseLock() error {
	if c.lock == nil {
		return nil
	}
	err := errors.Join(unix.Flock(int(c.lock.Fd()), unix.LOCK_UN), c.lock.Close())
	c.lock = nil
	if err != nil {
		return fmt.Errorf("release Compose project lock: %w", err)
	}
	return nil
}

func lockDirectory() (string, error) {
	uid := os.Getuid()
	candidates := []string{os.Getenv("XDG_RUNTIME_DIR"), filepath.Join("/run/user", strconv.Itoa(uid))}
	var base string
	for _, candidate := range candidates {
		if validatePreferredBase(candidate, uid) == nil {
			base = candidate
			break
		}
	}
	if base == "" {
		base = os.Getenv("TMPDIR")
		if base == "" {
			base = "/tmp"
		}
		if err := validateFallbackBase(base, uid); err != nil {
			return "", err
		}
	}
	lockDir := filepath.Join(base, fmt.Sprintf("gobfd-interop-%d.locks", uid))
	//nolint:gosec // The parent is ownership-validated before this fixed child is created.
	if err := os.Mkdir(lockDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create interop lock directory %s: %w", lockDir, err)
	}
	if err := validateOwnedDirectory(lockDir, uid, 0o700); err != nil {
		return "", fmt.Errorf("unsafe interop lock directory %s: %w", lockDir, err)
	}
	return lockDir, nil
}

func validatePreferredBase(path string, uid int) error {
	if path == "" {
		return fmt.Errorf("%w: preferred lock base is empty", errControl)
	}
	return validateOwnedDirectory(path, uid, 0o700)
}

func validateFallbackBase(path string, uid int) error {
	info, stat, err := lstatInfo(path)
	if err != nil {
		return fmt.Errorf("unsafe interop fallback lock base %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"%w: unsafe interop fallback lock base %s: require writable non-symlink directory",
			errControl, path,
		)
	}
	mode := info.Mode().Perm()
	if int(stat.Uid) == uid && mode&0o022 == 0 {
		return nil
	}
	if stat.Uid == 0 && info.Mode()&os.ModeSticky != 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: unsafe interop fallback lock base %s owner %d mode %o", errControl, path, stat.Uid, mode,
	)
}

func validateOwnedDirectory(path string, uid int, mode os.FileMode) error {
	info, stat, err := lstatInfo(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || int(stat.Uid) != uid || info.Mode().Perm() != mode {
		return fmt.Errorf("%w: require owned non-symlink directory mode %o", errControl, mode)
	}
	return nil
}

func validateLockFile(path string, mayBeMissing bool) error {
	info, stat, err := lstatInfo(path)
	if errors.Is(err, os.ErrNotExist) && mayBeMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		int(stat.Uid) != os.Getuid() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: unsafe interop lock file %s", errControl, path)
	}
	return nil
}

func validateOpenLockFile(path string, file *os.File) error {
	if err := validateLockFile(path, false); err != nil {
		return err
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat opened lock path: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened lock file: %w", err)
	}
	if !os.SameFile(pathInfo, fileInfo) {
		return fmt.Errorf("%w: unsafe interop lock file %s after open", errControl, path)
	}
	return nil
}

func lstatInfo(path string) (os.FileInfo, *syscall.Stat_t, error) {
	info, err := os.Lstat(path) //nolint:gosec // Callers validate ownership and type before mutation.
	if err != nil {
		return nil, nil, fmt.Errorf("lstat %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, nil, fmt.Errorf("%w: stat owner for %s", errControl, path)
	}
	return info, stat, nil
}

type projectResources struct {
	containers []string
	networks   []string
	volumes    []string
}

func (r projectResources) empty() bool {
	return len(r.containers) == 0 && len(r.networks) == 0 && len(r.volumes) == 0
}

func (r projectResources) String() string {
	lines := make([]string, 0, len(r.containers)+len(r.networks)+len(r.volumes))
	for _, id := range r.containers {
		lines = append(lines, "container:"+id)
	}
	for _, id := range r.networks {
		lines = append(lines, "network:"+id)
	}
	for _, name := range r.volumes {
		lines = append(lines, "volume:"+name)
	}
	return strings.Join(lines, "\n")
}

func (c *Controller) queryProjectResources(ctx context.Context) (projectResources, error) {
	label := "label=" + projectLabelKey + "=" + c.projectName
	containers, err := c.podmanText(
		ctx, 30*time.Second, "ps", "-a", "--no-trunc", "--filter", label, "--format", "{{.ID}}",
	)
	if err != nil {
		return projectResources{}, err
	}
	networks, err := c.podmanText(
		ctx, 30*time.Second, "network", "ls", "--no-trunc", "--filter", label, "--format", "{{.ID}}",
	)
	if err != nil {
		return projectResources{}, err
	}
	volumes, err := c.podmanText(ctx, 30*time.Second, "volume", "ls", "--filter", label, "--format", "{{.Name}}")
	if err != nil {
		return projectResources{}, err
	}
	return projectResources{lines(containers), lines(networks), lines(volumes)}, nil
}

func lines(value string) []string {
	return strings.Fields(value)
}

func (c *Controller) assertFixedNamesAvailable(ctx context.Context) error {
	for _, name := range append(append([]string{}, c.required...), c.optional...) {
		exists, err := c.containerExists(ctx, name)
		if err != nil {
			return fmt.Errorf("check fixed container name %s: %w", name, err)
		}
		if !exists {
			continue
		}
		label, err := c.podmanText(ctx, commandTimeout, "inspect", "--type", "container", "--format",
			`{{ index .Config.Labels "com.docker.compose.project" }}`, name)
		if err != nil {
			return err
		}
		owner := strings.TrimSpace(label)
		if owner == "" || owner == "<no value>" {
			owner = "<unlabelled>"
		}
		return fmt.Errorf(
			"%w: fixed container name %s belongs to Compose project %s; refusing collision with %s",
			errControl, name, owner, c.projectName,
		)
	}
	return nil
}

func (c *Controller) assertExistingProject(ctx context.Context) error {
	resources, err := c.queryProjectResources(ctx)
	if err != nil {
		return err
	}
	if resources.empty() {
		return fmt.Errorf("%w: Compose project %s has no exact-labelled resources", errControl, c.projectName)
	}
	for _, name := range c.required {
		if _, err := c.resolveContainerID(ctx, name); err != nil {
			return fmt.Errorf("required container %s is absent or foreign for Compose project %s: %w",
				name, c.projectName, err)
		}
	}
	for _, name := range c.optional {
		exists, err := c.containerExists(ctx, name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := c.resolveContainerID(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) resolveContainerID(ctx context.Context, name string) (string, error) {
	exists, err := c.containerExists(ctx, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: container %s does not exist", errControl, name)
	}
	details, err := c.podmanText(ctx, commandTimeout, "inspect", "--type", "container", "--format",
		`{{.ID}}|{{ index .Config.Labels "com.docker.compose.project" }}`, name)
	if err != nil {
		return "", err
	}
	id, label, ok := strings.Cut(strings.TrimSpace(details), "|")
	if !ok || id == "" || label != c.projectName {
		return "", fmt.Errorf(
			"%w: refusing foreign container %s with Compose project label %s",
			errControl, name, valueOr(label, "<unlabelled>"),
		)
	}
	return id, nil
}

func (c *Controller) resolveServiceContainerID(ctx context.Context, projectName, serviceName string) (string, error) {
	output, err := c.podmanText(ctx, 30*time.Second, "ps", "-a", "--no-trunc",
		"--filter", "label="+projectLabelKey+"="+projectName,
		"--filter", "label=com.docker.compose.service="+serviceName,
		"--format", "{{.ID}}")
	if err != nil {
		return "", err
	}
	ids := lines(output)
	if len(ids) != 1 {
		return "", fmt.Errorf(
			"%w: resolve Compose project %s service %s: found %d exact-labelled containers",
			errControl, projectName, serviceName, len(ids),
		)
	}
	return ids[0], nil
}

func (c *Controller) containerExists(ctx context.Context, name string) (bool, error) {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "podman", "container", "exists", name)
	cmd.Dir = c.root
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("podman container exists %s: %w", name, err)
}

func (c *Controller) cleanup(ctx context.Context) error {
	resources, err := c.queryProjectResources(ctx)
	if err != nil {
		return err
	}
	if len(resources.volumes) != 0 {
		return fmt.Errorf(
			"%w: guarded interop projects must use container storage or bind mounts; "+
				"refusing mutable labelled volumes for Compose project %s: %s",
			errControl, c.projectName, strings.Join(resources.volumes, " "),
		)
	}
	if validateErr := c.validateContainerSnapshot(ctx, resources.containers); validateErr != nil {
		return validateErr
	}
	if removeErr := c.removeContainerSnapshot(ctx, resources.containers); removeErr != nil {
		return removeErr
	}
	for _, id := range resources.networks {
		//nolint:gosec // Absence is verified after best-effort removal.
		_, _ = c.podmanText(ctx, 30*time.Second, "network", "rm", "--", id)
	}
	remaining, err := c.queryProjectResources(ctx)
	if err != nil {
		return err
	}
	if !remaining.empty() {
		return fmt.Errorf("%w: owned-resource leak for Compose project %s:\n%s",
			errControl, c.projectName, remaining.String())
	}
	return nil
}

func (c *Controller) validateContainerSnapshot(ctx context.Context, ids []string) error {
	for _, id := range ids {
		output, err := c.podmanText(ctx, 30*time.Second, "inspect", "--type", "container", "--format", "{{json .}}", id)
		if err != nil {
			return fmt.Errorf("inspect exact container ID %s before cleanup: %w", id, err)
		}
		var inspected struct {
			ID     string `json:"Id"` //nolint:tagliatelle // Podman inspect field.
			Config struct {
				Labels map[string]string `json:"Labels"` //nolint:tagliatelle // Podman inspect field.
			} `json:"Config"` //nolint:tagliatelle // Podman inspect field.
			Mounts []struct {
				Type string `json:"Type"` //nolint:tagliatelle // Podman inspect field.
			} `json:"Mounts"` //nolint:tagliatelle // Podman inspect field.
		}
		if err := decodeSingleJSON([]byte(output), &inspected); err != nil ||
			inspected.ID != id || inspected.Config.Labels[projectLabelKey] != c.projectName {
			return fmt.Errorf("%w: container ownership or volume-mount preflight failed for exact ID %s",
				errControl, id)
		}
		for _, mount := range inspected.Mounts {
			if mount.Type == "" || mount.Type == "volume" {
				return fmt.Errorf("%w: container ownership or volume-mount preflight failed for exact ID %s",
					errControl, id)
			}
		}
	}
	return nil
}

func (c *Controller) removeContainerSnapshot(ctx context.Context, ids []string) error {
	remaining := append([]string(nil), ids...)
	for pass := 0; pass < len(ids) && len(remaining) > 0; pass++ {
		next := make([]string, 0, len(remaining))
		progress := false
		for _, id := range remaining {
			//nolint:gosec // Existence is verified after each best-effort removal.
			_, _ = c.podmanText(ctx, 30*time.Second, "rm", "-f", "--", id)
			exists, err := c.containerExists(ctx, id)
			if err != nil {
				return fmt.Errorf("verify exact container ID %s after removal attempt: %w", id, err)
			}
			if exists {
				next = append(next, id)
			} else {
				progress = true
			}
		}
		if len(next) == 0 {
			return nil
		}
		if !progress {
			return fmt.Errorf("%w: no progress removing exact container snapshot; remaining IDs: %s",
				errControl, strings.Join(next, " "))
		}
		remaining = next
	}
	if len(remaining) != 0 {
		return fmt.Errorf("%w: bounded exact container cleanup exhausted; remaining IDs: %s",
			errControl, strings.Join(remaining, " "))
	}
	return nil
}

func (c *Controller) verifyHoloConfiguration(ctx context.Context, loaderID string) error {
	logs, err := c.podmanText(ctx, commandTimeout, "logs", loaderID)
	if err != nil {
		return fmt.Errorf("%w: failed to inspect Holo configuration loader logs", errControl)
	}
	loaderError := ""
	for line := range strings.Lines(logs) {
		if strings.HasPrefix(line, "% ") {
			loaderError = "Holo configuration loader reported parser or commit errors"
			break
		}
	}
	if loaderError == "" && strings.TrimSpace(logs) != "" {
		loaderError = "Holo configuration loader produced unexpected output"
	}
	holoID, err := c.resolveContainerID(ctx, "holo-interop")
	if err != nil {
		return fmt.Errorf("holo-interop is absent or not owned by %s: %w", c.projectName, err)
	}
	version, err := c.podmanText(ctx, commandTimeout, "exec", holoID, "holo-cli", "--version")
	if err != nil {
		return fmt.Errorf("%w: failed to inspect Holo CLI version", errControl)
	}
	if strings.TrimSpace(version) != holoCLIVersion {
		return fmt.Errorf("%w: unexpected Holo CLI version: %s", errControl, strings.TrimSpace(version))
	}
	running, err := c.podmanText(ctx, commandTimeout, "exec", holoID,
		"holo-cli", "--no-colors", "--no-pager", "--address", "http://127.0.0.1:50051",
		"--command", "show running format json")
	if err != nil {
		return fmt.Errorf("%w: failed to inspect Holo running configuration", errControl)
	}
	if !validHoloRunningConfiguration([]byte(running)) {
		return fmt.Errorf("%w: Holo running configuration is missing the required BFD session", errControl)
	}
	if loaderError != "" {
		return fmt.Errorf("%w: %s", errControl, loaderError)
	}
	return nil
}

func validHoloRunningConfiguration(data []byte) bool {
	var root map[string]any
	if decodeSingleJSON(data, &root) != nil {
		return false
	}
	interfaceMatches := matchingHoloInterfaces(root)
	protocolMatches, sessionMatches := matchingHoloSessions(root)
	return interfaceMatches == 1 && protocolMatches == 1 && sessionMatches == 1
}

func matchingHoloInterfaces(root map[string]any) int {
	interfaces := nestedSlice(root, "ietf-interfaces:interfaces", "interface")
	interfaceMatches := 0
	for _, candidate := range interfaces {
		if candidate["name"] == "eth0" && candidate["type"] == "iana-if-type:ethernetCsmacd" {
			if _, ok := candidate["ietf-ip:ipv4"].(map[string]any); ok {
				interfaceMatches++
			}
		}
	}
	return interfaceMatches
}

func matchingHoloSessions(root map[string]any) (int, int) {
	protocols := nestedSlice(root, "ietf-routing:routing", "control-plane-protocols", "control-plane-protocol")
	protocolMatches := 0
	sessionMatches := 0
	for _, protocol := range protocols {
		if protocol["type"] != "ietf-bfd-types:bfdv1" || protocol["name"] != "main" {
			continue
		}
		protocolMatches++
		for _, session := range nestedSlice(protocol, "ietf-bfd:bfd", "ietf-bfd-ip-sh:ip-sh", "sessions", "session") {
			if session["interface"] == "eth0" && session["dest-addr"] == "172.20.0.10" &&
				session["source-addr"] == "172.20.0.50" && numberEquals(session["local-multiplier"], 3) &&
				numberEquals(session["desired-min-tx-interval"], 300000) &&
				numberEquals(session["required-min-rx-interval"], 300000) {
				sessionMatches++
			}
		}
	}
	return protocolMatches, sessionMatches
}

func nestedSlice(root map[string]any, path ...string) []map[string]any {
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	values, ok := current.([]any)
	if !ok {
		return nil
	}
	objects := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			objects = append(objects, object)
		}
	}
	return objects
}

func numberEquals(value any, want float64) bool {
	number, ok := value.(float64)
	return ok && number == want
}

func decodeSingleJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON value: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", errControl)
		}
		return fmt.Errorf("decode trailing JSON value: %w", err)
	}
	return nil
}

func valueOr(value, fallback string) string {
	if value == "" || value == "<no value>" {
		return fallback
	}
	return value
}
