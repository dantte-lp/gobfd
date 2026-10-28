package toolbootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	e2eSocketTimeout  = 20 * time.Second
	e2eCommandTimeout = 30 * time.Second
	pastaSignalRule   = "  signal (receive) peer=podman,\n"
)

// SetupPodmanE2E prepares the hosted Linux runner without changing other runtime consumers.
func SetupPodmanE2E(ctx context.Context, options RuntimeOptions) (RuntimeReport, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = filepath.Join("/run/user", strconv.Itoa(os.Geteuid()))
	}
	if err := validateE2EEnvironment(options, runtimeDir); err != nil {
		return RuntimeReport{}, err
	}
	for _, directory := range []string{runtimeDir, filepath.Join(runtimeDir, "podman")} {
		if err := privateRuntimeDirectory(directory); err != nil {
			return RuntimeReport{}, err
		}
	}
	report, err := SetupPodmanRuntime(ctx, options)
	if err != nil {
		return RuntimeReport{}, err
	}
	if err = allowPastaSignals(ctx, "/etc/apparmor.d/abstractions/pasta", "/etc/apparmor.d/usr.bin.pasta"); err != nil {
		return RuntimeReport{}, err
	}
	err = finishE2ESocket(ctx, options, report.Compose.Path, runtimeDir, "podman-system-service.log", e2eSocketTimeout)
	if err != nil {
		return RuntimeReport{}, err
	}
	return report, nil
}

func validateE2EEnvironment(options RuntimeOptions, runtimeDir string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("E2E runtime requires Linux: %w", os.ErrInvalid)
	}
	for _, path := range []string{options.GitHubEnv, options.GitHubPath, runtimeDir} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n") {
			return fmt.Errorf("E2E environment requires absolute workflow and runtime paths: %w", os.ErrInvalid)
		}
	}
	for _, path := range []string{options.GitHubEnv, options.GitHubPath} {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect E2E workflow file: %w", err)
		}
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("E2E workflow file is not regular: %w", errInvalidGitHubFile)
		}
	}
	return nil
}

func privateRuntimeDirectory(path string) error {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf("invalid E2E runtime directory: %w", os.ErrInvalid)
	}
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(filepath.Clean(path), current), current)
	for index, component := range components {
		current = filepath.Join(current, component)
		final := index == len(components)-1
		// #nosec G703 -- each absolute-path component is checked for ownership, mode and symlinks before use.
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && final {
			// #nosec G703 -- validated ancestors precede creation of this single private component.
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil {
				return fmt.Errorf("create private E2E runtime directory: %w", mkdirErr)
			}
			// #nosec G703 -- inspect the newly created component before accepting it.
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect E2E runtime directory: %w", err)
		}
		if err := validateComposeDirectoryComponent(current, info, final); err != nil {
			return err
		}
		if final && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("E2E runtime directory must be private: %w", os.ErrPermission)
		}
	}
	return nil
}

func finishE2ESocket(
	ctx context.Context, options RuntimeOptions, provider, runtimeDir, logPath string, timeout time.Duration,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start E2E socket: %w", err)
	}
	if err := privateRuntimeDirectory(runtimeDir); err != nil {
		return err
	}
	if err := privateRuntimeDirectory(filepath.Join(runtimeDir, "podman")); err != nil {
		return err
	}
	socket := filepath.Join(runtimeDir, "podman", "podman.sock")
	environment := replaceEnvironment(providerEnvironment(os.Environ(), provider), map[string]string{
		"XDG_RUNTIME_DIR": runtimeDir,
	})
	service, err := ensureE2ESocket(ctx, socket, logPath, environment, timeout)
	if err != nil {
		return err
	}
	if err := publishE2ESocket(ctx, options, socket, runtimeDir, environment); err != nil {
		if service != nil {
			err = errors.Join(err, service.stop())
		}
		return err
	}
	// Successful CI handoff deliberately outlives this bootstrap process. No
	// cancellation context owns the daemon; failed setup kills and joins only our PID.
	return nil
}

func e2eSocketExists(path string) (bool, error) {
	// #nosec G703 -- inspect the endpoint under the validated private runtime directory without following links.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect E2E socket: %w", err)
	}
	_, owned := composeDirectoryOwnership(info)
	if info.Mode()&os.ModeSocket == 0 || !owned {
		return false, fmt.Errorf("E2E endpoint is not a current-user Unix socket: %w", os.ErrPermission)
	}
	return true, nil
}

func publishE2ESocket(
	ctx context.Context, options RuntimeOptions, socket, runtimeDir string, environment []string,
) error {
	commandCtx, cancel := context.WithTimeout(ctx, e2eCommandTimeout)
	defer cancel()
	for _, args := range [][]string{{"--version"}, {"compose", "version"}} {
		if _, err := commandOutput(commandCtx, "podman", args, environment); err != nil {
			return err
		}
	}
	content := "XDG_RUNTIME_DIR=" + runtimeDir + "\nCONTAINER_HOST=unix://" + socket +
		"\nPODMAN_HOST=unix://" + socket + "\nPODMAN_SOCKET_PATH=" + socket + "\n"
	return errors.Join(ctx.Err(), appendWorkflowFile(options.GitHubEnv, content))
}
