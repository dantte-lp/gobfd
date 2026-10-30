package toolbootstrap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const commandWaitDelay = 5 * time.Second

var errCommandNotAllowed = errors.New("bootstrap command is not allowlisted")

type outputRunner interface {
	Output(ctx context.Context, name string, arguments, environment []string) (string, error)
}

type execOutputRunner struct{}

func (execOutputRunner) Output(
	ctx context.Context,
	name string,
	arguments, environment []string,
) (string, error) {
	return commandOutput(ctx, name, arguments, environment)
}

func verifyJQ(ctx context.Context, runner outputRunner) error {
	if _, err := runner.Output(ctx, "jq", []string{"--version"}, nil); err != nil {
		return fmt.Errorf("verify jq runtime: %w", err)
	}
	return nil
}

func commandPath(name string) (string, error) {
	switch name {
	case "jq", "podman", "sudo":
	default:
		return "", fmt.Errorf("resolve bootstrap command %q: %w", name, errCommandNotAllowed)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve bootstrap command %q: %w", name, err)
	}
	return path, nil
}

func commandOutput(ctx context.Context, name string, arguments, environment []string) (string, error) {
	path := name
	if name == "jq" || name == "podman" {
		var err error
		path, err = commandPath(name)
		if err != nil {
			return "", err
		}
	}
	return executeOutput(ctx, path, name, arguments, environment, nil)
}

func commandFileOutput(ctx context.Context, file *os.File, arguments, environment []string) (string, error) {
	if file == nil {
		return "", fmt.Errorf("run nil Compose provider descriptor: %w", os.ErrInvalid)
	}
	return executeOutput(ctx, "/proc/self/fd/3", file.Name(), arguments, environment, []*os.File{file})
}

func executeOutput(
	ctx context.Context,
	path, displayName string,
	arguments, environment []string,
	extraFiles []*os.File,
) (string, error) {
	// #nosec G204 -- path is fixed /proc/self/fd/3 or resolved from the command allowlist.
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = environment
	command.ExtraFiles = extraFiles
	command.WaitDelay = commandWaitDelay
	// Wrapper diagnostics belong to stderr; version checks consume exact stdout.
	output, err := command.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return "", fmt.Errorf("run %s %s; stdout=%s; stderr=%s: %w", displayName, strings.Join(arguments, " "),
			strings.TrimSpace(string(output)), stderr, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func runStreaming(ctx context.Context, name string, arguments, environment []string) error {
	path, err := commandPath(name)
	if err != nil {
		return err
	}
	// #nosec G204 -- path is resolved from the fixed allowlist and no shell is used.
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.WaitDelay = commandWaitDelay
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(arguments, " "), err)
	}
	return nil
}

func allowPastaSignals(ctx context.Context, abstraction, profile string) error {
	// #nosec G304 -- production passes the fixed AppArmor abstraction path; other callers are owned test fixtures.
	data, err := os.ReadFile(abstraction)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pasta abstraction: %w", err)
	}
	if strings.Contains(string(data), "peer=podman") {
		return nil
	}
	path, err := commandPath("sudo")
	if err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, e2eCommandTimeout)
	defer cancel()
	// #nosec G204 -- sudo resolves through the command allowlist; tee argv uses the fixed AppArmor path without a shell.
	command := exec.CommandContext(commandCtx, path, "tee", "-a", abstraction)
	command.Stdin = strings.NewReader(pastaSignalRule)
	command.Stdout, command.Stderr = io.Discard, os.Stderr
	command.WaitDelay = commandWaitDelay
	if err := command.Run(); err != nil {
		return fmt.Errorf("append pasta signal rule: %w", err)
	}
	return runStreaming(commandCtx, "sudo", []string{"apparmor_parser", "-r", profile}, nil)
}

type e2eSocketService struct {
	process *os.Process
	done    <-chan error
}

func (service *e2eSocketService) stop() error {
	killErr := service.process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	<-service.done
	return killErr
}

func ensureE2ESocket(
	ctx context.Context, socket, logPath string, environment []string, timeout time.Duration,
) (*e2eSocketService, error) {
	if ready, err := e2eSocketExists(socket); err != nil || ready {
		return nil, err
	}
	path, err := commandPath("podman")
	if err != nil {
		return nil, err
	}
	logFile, err := openE2EServiceLog(logPath)
	if err != nil {
		return nil, err
	}
	// #nosec G204 G702 -- Podman is allowlisted; argv uses the validated private Unix socket and no shell is invoked.
	command := exec.CommandContext(context.WithoutCancel(ctx), path, "system", "service", "--time=0", "unix://"+socket)
	command.Env, command.Stdout, command.Stderr = environment, logFile, logFile
	startErr := command.Start()
	closeErr := logFile.Close()
	if startErr != nil {
		return nil, fmt.Errorf("start E2E service: %w", errors.Join(startErr, closeErr))
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	service := &e2eSocketService{process: command.Process, done: done}
	if closeErr != nil {
		return nil, errors.Join(closeErr, service.stop())
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return nil, fmt.Errorf("E2E service exited before socket readiness: %w", errors.Join(err, os.ErrClosed))
		case <-waitCtx.Done():
			return nil, errors.Join(fmt.Errorf("wait for E2E socket: %w", waitCtx.Err()), service.stop())
		case <-ticker.C:
			if ready, err := e2eSocketExists(socket); err != nil {
				return nil, errors.Join(err, service.stop())
			} else if ready {
				return service, nil
			}
		}
	}
}

func openE2EServiceLog(path string) (*os.File, error) {
	// #nosec G703 -- the CLI uses one fixed log name; reject existing links, foreign owners and non-private modes.
	if info, err := os.Lstat(path); err == nil {
		_, owned := composeDirectoryOwnership(info)
		if !info.Mode().IsRegular() || !owned || info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("unsafe E2E service log: %w", os.ErrPermission)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect E2E service log: %w", err)
	}
	// #nosec G304 G703 -- the fixed log path passed regular-file, ownership and private-mode guards before private append.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open E2E service log: %w", err)
	}
	return file, nil
}

// AppArmorDenials emits only denial lines; raw kernel messages never reach output or errors.
func AppArmorDenials(ctx context.Context, output io.Writer) error {
	path, err := commandPath("sudo")
	if err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, e2eCommandTimeout)
	defer cancel()
	// #nosec G204 -- sudo resolves through the command allowlist; dmesg is a literal argument and no shell is invoked.
	command := exec.CommandContext(commandCtx, path, "dmesg")
	command.WaitDelay = commandWaitDelay
	pipe, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open AppArmor diagnostics pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start AppArmor diagnostics: %w", errors.Join(err, pipe.Close()))
	}
	scanner := bufio.NewScanner(io.LimitReader(pipe, 8<<20))
	var writeErr error
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `apparmor="DENIED"`) {
			if _, writeErr = fmt.Fprintln(output, scanner.Text()); writeErr != nil {
				break
			}
		}
	}
	closeErr := pipe.Close()
	waitErr := command.Wait()
	if err := errors.Join(scanner.Err(), writeErr, closeErr, waitErr); err != nil {
		return fmt.Errorf("read AppArmor diagnostics: %w", err)
	}
	return nil
}
