//go:build linux

package toolbootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("GOBFD_BOOTSTRAP_E2E_FIXTURE") == "1" {
		if os.Getenv("GOBFD_BOOTSTRAP_E2E_PARENT") == "1" {
			os.Exit(runBootstrapParentFixture())
		}
		os.Exit(runBootstrapE2EFixture())
	}
	os.Exit(m.Run())
}

func TestE2ESocketReusesExistingServiceWithoutTakingOwnership(t *testing.T) {
	state := installBootstrapE2EFixture(t, "version-fail")
	runtimeDir := filepath.Join(state, "runtime")
	t.Setenv("GOBFD_BOOTSTRAP_E2E_EXPECT_RUNTIME", runtimeDir)
	if err := privateRuntimeDirectory(runtimeDir); err != nil {
		t.Fatal(err)
	}
	if err := privateRuntimeDirectory(filepath.Join(runtimeDir, "podman")); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(runtimeDir, "podman", "podman.sock")
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	err = finishE2ESocket(t.Context(), e2eFixtureOptions(t), "/fixture/provider", runtimeDir,
		filepath.Join(state, "service.log"), time.Second)
	if err == nil {
		t.Fatal("version failure was ignored")
	}
	var dialer net.Dialer
	connection, err := dialer.DialContext(t.Context(), "unix", socket)
	if err != nil {
		t.Fatalf("existing service was stopped: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("existing service caused a new child: %v", err)
	}
}

func TestE2ESocketHandoffSurvivesBootstrapParentExitAndPublishesEnvironment(t *testing.T) {
	state := installBootstrapE2EFixture(t, "ready")
	options, runtimeDir := e2eFixtureOptions(t), filepath.Join(state, "runtime")
	t.Setenv("GOBFD_BOOTSTRAP_E2E_EXPECT_RUNTIME", runtimeDir)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	environment := append(os.Environ(), "GOBFD_BOOTSTRAP_E2E_PARENT=1",
		"GITHUB_ENV="+options.GitHubEnv, "GITHUB_PATH="+options.GitHubPath)
	output, commandErr := executeOutput(ctx, self, "bootstrap-parent fixture", nil, environment, nil)
	if commandErr != nil {
		serviceLog, readErr := os.ReadFile(filepath.Join(state, "service.log"))
		t.Fatalf("bootstrap parent failed: %v; %s; service log read=%v: %s", commandErr, output, readErr, serviceLog)
	}
	pid := bootstrapFixturePID(t, state)
	if dead, statusErr := bootstrapFixtureDead(pid); statusErr != nil || dead {
		t.Fatalf("service did not survive bootstrap-parent exit: dead=%v, error=%v", dead, statusErr)
	}
	socket := filepath.Join(runtimeDir, "podman", "podman.sock")
	dialer := net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		t.Fatalf("service socket did not survive bootstrap-parent exit: %v", err)
	}
	if closeErr := connection.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	data, err := os.ReadFile(options.GitHubEnv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"XDG_RUNTIME_DIR=" + runtimeDir + "\n", "CONTAINER_HOST=unix://" + socket + "\n",
		"PODMAN_HOST=unix://" + socket + "\n", "PODMAN_SOCKET_PATH=" + socket + "\n",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing exact environment publication %q in %q", want, data)
		}
	}
}

func TestE2ESocketStartupCancellationAfterPIDRegistrationKillsAndJoinsChild(t *testing.T) {
	state := installBootstrapE2EFixture(t, "never-ready")
	runtimeDir := filepath.Join(state, "runtime")
	t.Setenv("GOBFD_BOOTSTRAP_E2E_EXPECT_RUNTIME", runtimeDir)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	options := e2eFixtureOptions(t)
	go func() {
		done <- finishE2ESocket(ctx, options, "/fixture/provider", runtimeDir,
			filepath.Join(state, "service.log"), e2eSocketTimeout)
	}()
	registration, stopRegistration := context.WithTimeout(t.Context(), 3*time.Second)
	defer stopRegistration()
	pid, err := waitBootstrapFixturePID(registration, state)
	if err != nil {
		cancel()
		startupErr := <-done
		t.Fatalf("service PID registration failed before cancellation: %v; startup result: %v", err, startupErr)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation lost: %v", err)
		}
	case <-registration.Done():
		t.Fatal("startup cancellation did not finish before the external watchdog")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("canceled startup did not kill and join its child: pid=%d, error=%v", pid, err)
	}
}

func TestE2ESocketFailuresKillAndJoinOnlyStartedChild(t *testing.T) {
	for _, mode := range []string{"never-ready", "early-exit", "version-fail", "compose-version-fail", "env-fail"} {
		t.Run(mode, func(t *testing.T) {
			state := installBootstrapE2EFixture(t, mode)
			options := e2eFixtureOptions(t)
			runtimeDir := filepath.Join(state, "runtime")
			t.Setenv("GOBFD_BOOTSTRAP_E2E_EXPECT_RUNTIME", runtimeDir)
			if mode == "env-fail" {
				options.GitHubEnv = t.TempDir()
			}
			logPath := filepath.Join(state, "service.log")
			if err := os.WriteFile(logPath, []byte("retained diagnostic\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := finishE2ESocket(t.Context(), options, "/fixture/provider", runtimeDir, logPath, time.Second)
			if err == nil {
				t.Fatal("failed startup or publication accepted")
			}
			if mode == "never-ready" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("startup deadline lost: %v", err)
			}
			pid := bootstrapFixturePID(t, state)
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("owned child not joined: pid=%d, error=%v", pid, err)
			}
			if mode == "compose-version-fail" {
				data, err := os.ReadFile(filepath.Join(state, "versions"))
				if err != nil || string(data) != "--version\ncompose version\n" {
					t.Fatalf("Compose failure did not follow successful Podman version check: %q, %v", data, err)
				}
			}
			if data, err := os.ReadFile(logPath); err != nil || !bytes.HasPrefix(data, []byte("retained diagnostic\n")) {
				t.Fatalf("existing diagnostic log was deleted/truncated: %q, %v", data, err)
			}
		})
	}
}

func TestE2EBootstrapRejectsInvalidEnvironmentBeforeSetup(t *testing.T) {
	for _, path := range []string{"", "relative", "/tmp/injected\nFIELD=value"} {
		options := e2eFixtureOptions(t)
		options.GitHubEnv = path
		if err := validateE2EEnvironment(options, t.TempDir()); err == nil {
			t.Errorf("invalid GitHub path accepted: %q", path)
		}
	}
	options := e2eFixtureOptions(t)
	link := filepath.Join(t.TempDir(), "environment")
	if err := os.Symlink(options.GitHubEnv, link); err != nil {
		t.Fatal(err)
	}
	options.GitHubEnv = link
	if err := validateE2EEnvironment(options, t.TempDir()); err == nil {
		t.Fatal("linked workflow output accepted")
	}
	if err := validateE2EEnvironment(e2eFixtureOptions(t), "relative"); err == nil {
		t.Fatal("relative runtime directory accepted")
	}
}

func TestE2ERuntimeDirectoryAndSocketGuardsPreserveForeignPaths(t *testing.T) {
	root, foreign := t.TempDir(), t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(link, "child"), shared} {
		if err := privateRuntimeDirectory(path); err == nil {
			t.Errorf("unsafe runtime directory accepted: %q", path)
		}
	}
	if info, err := os.Stat(shared); err != nil || info.Mode().Perm() != 0o777 {
		t.Fatalf("rejected shared directory mode changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "child")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink ancestor caused a foreign directory mutation: %v", err)
	}
	endpoint := filepath.Join(root, "podman.sock")
	if err := os.WriteFile(endpoint, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eSocketExists(endpoint); err == nil {
		t.Fatal("ordinary file accepted as an API socket")
	}
	if data, err := os.ReadFile(endpoint); err != nil || string(data) != "keep" {
		t.Fatalf("foreign endpoint modified: %q, %v", data, err)
	}
	state := installBootstrapE2EFixture(t, "ready")
	logLink := filepath.Join(root, "service.log")
	if err := os.Symlink(endpoint, logLink); err != nil {
		t.Fatal(err)
	}
	_, startupErr := ensureE2ESocket(t.Context(), filepath.Join(root, "absent.sock"), logLink, os.Environ(), time.Second)
	if startupErr == nil {
		t.Fatal("linked diagnostic log accepted")
	}
	if _, err := os.Stat(filepath.Join(state, "pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe log reached service startup: %v", err)
	}
}

func TestPastaSignalAdjustmentIsConditionalIdempotentAndPropagatesFailure(t *testing.T) {
	state := installBootstrapE2EFixture(t, "ready")
	abstraction, profile := filepath.Join(state, "pasta"), filepath.Join(state, "profile")
	if err := allowPastaSignals(t.Context(), abstraction, profile); err != nil {
		t.Fatalf("absent abstraction should need no adjustment: %v", err)
	}
	if err := os.WriteFile(abstraction, []byte("# original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := allowPastaSignals(t.Context(), abstraction, profile); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(abstraction)
	if err != nil || string(data) != "# original\n"+pastaSignalRule {
		t.Fatalf("signal rule was duplicated or source replaced: %q, %v", data, err)
	}
	if err := os.WriteFile(abstraction, []byte("# original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBFD_BOOTSTRAP_E2E_MODE", "parser-fail")
	if err := allowPastaSignals(t.Context(), abstraction, profile); err == nil {
		t.Fatal("profile reload failure ignored")
	}
}

func TestAppArmorDiagnosticsFilterRawKernelMessagesAndExposeFailure(t *testing.T) {
	installBootstrapE2EFixture(t, "ready")
	var output bytes.Buffer
	if err := AppArmorDenials(t.Context(), &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "apparmor=\"DENIED\" operation=signal\n" {
		t.Fatalf("raw kernel messages escaped filter: %q", output.String())
	}
	t.Setenv("GOBFD_BOOTSTRAP_E2E_MODE", "dmesg-fail")
	output.Reset()
	if err := AppArmorDenials(t.Context(), &output); err == nil || strings.Contains(err.Error(), "raw-private") {
		t.Fatalf("diagnostic failure missing or exposed raw output: %v", err)
	}
}

func installBootstrapE2EFixture(t *testing.T, mode string) string {
	t.Helper()
	state, err := os.MkdirTemp("", "gobfd-tb-") //nolint:usetesting // Test-name prefixes exceed Unix socket path limits.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(state); cleanupErr != nil {
			t.Errorf("remove owned fixture directory: %v", cleanupErr)
		}
	})
	commands := t.TempDir()
	t.Cleanup(func() { cleanupBootstrapFixture(t, state, filepath.Join(commands, "podman")) })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"podman", "sudo"} {
		if err := os.Symlink(self, filepath.Join(commands, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOBFD_BOOTSTRAP_E2E_FIXTURE", "1")
	t.Setenv("GOBFD_BOOTSTRAP_E2E_STATE", state)
	t.Setenv("GOBFD_BOOTSTRAP_E2E_MODE", mode)
	t.Setenv("PODMAN_COMPOSE_PROVIDER", "stale-parent-provider")
	t.Setenv("PODMAN_COMPOSE_WARNING_LOGS", "true")
	t.Setenv("DOCKER_BUILDKIT", "1")
	t.Setenv("XDG_RUNTIME_DIR", "stale-parent-runtime")
	t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return state
}

func e2eFixtureOptions(t *testing.T) RuntimeOptions {
	t.Helper()
	root := t.TempDir()
	return RuntimeOptions{GitHubEnv: filepath.Join(root, "environment"), GitHubPath: filepath.Join(root, "path")}
}

func bootstrapFixturePID(t *testing.T, state string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitBootstrapFixturePID(ctx context.Context, state string) (int, error) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(filepath.Join(state, "pid")); err == nil {
			if pid, parseErr := strconv.Atoi(string(data)); parseErr == nil && pid > 0 {
				return pid, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
}

func bootstrapFixtureDead(pid int) (bool, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_, status, found := strings.Cut(string(data), ") ")
	if !found || status == "" {
		return false, errors.New("fixture process status unavailable")
	}
	return status[0] == 'Z' || status[0] == 'X', nil
}

func cleanupBootstrapFixture(t *testing.T, state, command string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "pid"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	pid, parseErr := strconv.Atoi(string(data))
	if err != nil || parseErr != nil || pid <= 0 || pid == os.Getpid() {
		t.Errorf("cannot identify owned fixture PID: read=%v, parse=%v", err, parseErr)
		return
	}
	if dead, statusErr := bootstrapFixtureDead(pid); statusErr != nil || dead {
		if statusErr != nil {
			t.Errorf("inspect fixture cleanup status: %v", statusErr)
		}
		return
	}
	argv, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || !bytes.HasPrefix(argv, append([]byte(command), 0)) {
		t.Errorf("refuse cleanup of PID %d without exact fixture executable: %v", pid, err)
		return
	}
	shutdownBootstrapFixtureSocket(t.Context(), state)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if dead, err := bootstrapFixtureDead(pid); err == nil && dead {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("kill owned fixture PID: %v", err)
		return
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if dead, err := bootstrapFixtureDead(pid); err == nil && dead {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("owned fixture PID did not reach dead state after fallback cleanup")
}

func shutdownBootstrapFixtureSocket(ctx context.Context, state string) {
	data, err := os.ReadFile(filepath.Join(state, "socket"))
	if err != nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	connection, err := dialer.DialContext(cleanupCtx, "unix", string(data))
	if err != nil {
		return
	}
	_, _ = io.WriteString(connection, "shutdown\n")
	_ = connection.Close()
}

func runBootstrapParentFixture() int {
	if err := os.Unsetenv("GOBFD_BOOTSTRAP_E2E_PARENT"); err != nil {
		return 14
	}
	options := RuntimeOptions{GitHubEnv: os.Getenv("GITHUB_ENV"), GitHubPath: os.Getenv("GITHUB_PATH")}
	err := finishE2ESocket(context.Background(), options, "/fixture/provider",
		os.Getenv("GOBFD_BOOTSTRAP_E2E_EXPECT_RUNTIME"),
		filepath.Join(os.Getenv("GOBFD_BOOTSTRAP_E2E_STATE"), "service.log"), time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 14
	}
	return 0
}

func runBootstrapE2EFixture() int {
	args, mode := os.Args[1:], os.Getenv("GOBFD_BOOTSTRAP_E2E_MODE")
	if filepath.Base(os.Args[0]) == "sudo" {
		switch args[0] {
		case "tee":
			if len(args) != 3 || args[1] != "-a" {
				return 13
			}
			file, err := os.OpenFile(args[2], os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return 2
			}
			_, copyErr := io.Copy(file, os.Stdin)
			if errors.Join(copyErr, file.Close()) != nil {
				return 2
			}
		case "apparmor_parser":
			if len(args) != 3 || args[1] != "-r" {
				return 13
			}
			if mode == "parser-fail" {
				return 3
			}
		case "dmesg":
			fmt.Fprintln(os.Stdout, "raw-private kernel message")
			fmt.Fprintln(os.Stdout, `apparmor="DENIED" operation=signal`)
			if mode == "dmesg-fail" {
				return 4
			}
		default:
			return 5
		}
		return 0
	}
	for name, want := range map[string]string{
		"PODMAN_COMPOSE_PROVIDER": "/fixture/provider", "PODMAN_COMPOSE_WARNING_LOGS": "false",
		"DOCKER_BUILDKIT": "0", "XDG_RUNTIME_DIR": os.Getenv("GOBFD_BOOTSTRAP_E2E_EXPECT_RUNTIME"),
	} {
		if os.Getenv(name) != want || name == "XDG_RUNTIME_DIR" && !filepath.IsAbs(want) {
			fmt.Fprintln(os.Stderr, "fixture Podman environment mismatch:", name)
			return 15
		}
	}
	if args[0] != "system" {
		if joined := strings.Join(args, " "); joined != "--version" && joined != "compose version" {
			return 13
		}
		file, err := os.OpenFile(filepath.Join(os.Getenv("GOBFD_BOOTSTRAP_E2E_STATE"), "versions"),
			os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return 16
		}
		_, writeErr := fmt.Fprintln(file, strings.Join(args, " "))
		if errors.Join(writeErr, file.Close()) != nil {
			return 16
		}
		if mode == "version-fail" && args[0] == "--version" || mode == "compose-version-fail" && args[0] == "compose" {
			return 6
		}
		fmt.Fprintln(os.Stdout, "fixture version")
		return 0
	}
	if len(args) != 4 || args[1] != "service" || args[2] != "--time=0" {
		return 13
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("GOBFD_BOOTSTRAP_E2E_STATE"), "pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return 7
	}
	if mode == "early-exit" {
		return 8
	}
	if mode == "never-ready" {
		time.Sleep(time.Hour)
		return 9
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "unix", strings.TrimPrefix(args[len(args)-1], "unix://"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture Unix listen:", err)
		return 10
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("GOBFD_BOOTSTRAP_E2E_STATE"), "socket"),
		[]byte(strings.TrimPrefix(args[len(args)-1], "unix://")), 0o600); err != nil {
		_ = listener.Close()
		return 10
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			return 11
		}
		data, readErr := io.ReadAll(io.LimitReader(connection, 9))
		if errors.Join(readErr, connection.Close()) != nil {
			return 12
		}
		if string(data) == "shutdown\n" {
			if err := listener.Close(); err != nil {
				return 12
			}
			return 0
		}
	}
}
