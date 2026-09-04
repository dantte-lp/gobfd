package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const helperProcessEnv = "GOBFD_PACKAGE_LIFECYCLE_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperProcessEnv) == "1" {
		runHelperProcess()
		return
	}
	os.Exit(m.Run())
}

func TestPlanLifecycle(t *testing.T) {
	t.Parallel()

	install := []command{
		{name: "systemd-sysusers", args: []string{"/usr/lib/sysusers.d/gobfd.conf"}},
		{name: "systemd-tmpfiles", args: []string{"--create", "/usr/lib/tmpfiles.d/gobfd.conf"}},
		{name: "systemctl", args: []string{"daemon-reload"}, optional: true},
	}
	finalRemove := []command{
		{name: "systemctl", args: []string{"stop", "gobfd"}, optional: true, tolerateFailure: true},
		{name: "systemctl", args: []string{"disable", "gobfd"}, optional: true, tolerateFailure: true},
		{name: "systemctl", args: []string{"daemon-reload"}, optional: true},
	}

	tests := []struct {
		name       string
		invocation string
		args       []string
		want       []command
	}{
		{
			name:       "Debian fresh configure",
			invocation: "/var/lib/dpkg/info/gobfd.postinst",
			args:       []string{"configure", ""},
			want:       install,
		},
		{name: "Debian upgrade configure", invocation: "gobfd.postinst", args: []string{"configure", "0.6.4"}, want: install},
		{name: "Debian final remove", invocation: "gobfd.prerm", args: []string{"remove"}, want: finalRemove},
		{name: "Debian upgrade pre-remove", invocation: "gobfd.prerm", args: []string{"upgrade", "0.6.5"}},
		{
			name: "RPM fresh install", invocation: "gobfd-postinstall",
			args: []string{"/var/tmp/rpm-script.1", "1"}, want: install,
		},
		{
			name: "RPM upgrade install", invocation: "gobfd-postinstall",
			args: []string{"/var/tmp/rpm-script.2", "2"}, want: install,
		},
		{
			name: "RPM final erase", invocation: "gobfd-preremove",
			args: []string{"/var/tmp/rpm-script.3", "0"}, want: finalRemove,
		},
		{name: "RPM upgrade pre-remove", invocation: "gobfd-preremove", args: []string{"/var/tmp/rpm-script.4", "1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := plan(tt.invocation, tt.args)
			if err != nil {
				t.Fatalf("plan lifecycle: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("plan = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPlanRejectsMalformedInvocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		invocation string
		args       []string
	}{
		{name: "unknown basename", invocation: "gobfd-lifecycle", args: []string{"configure"}},
		{name: "postinst missing phase", invocation: "gobfd.postinst"},
		{name: "postinst missing version", invocation: "gobfd.postinst", args: []string{"configure"}},
		{name: "postinst unknown phase", invocation: "gobfd.postinst", args: []string{"abort-upgrade"}},
		{name: "postinst too many arguments", invocation: "gobfd.postinst", args: []string{"configure", "0.6.4", "extra"}},
		{name: "prerm remove extra argument", invocation: "gobfd.prerm", args: []string{"remove", "extra"}},
		{name: "prerm upgrade missing version", invocation: "gobfd.prerm", args: []string{"upgrade"}},
		{name: "RPM missing arguments", invocation: "gobfd-postinstall"},
		{name: "RPM empty scriptlet path", invocation: "gobfd-postinstall", args: []string{"", "1"}},
		{name: "RPM nonnumeric count", invocation: "gobfd-preremove", args: []string{"/var/tmp/rpm-script", "many"}},
		{name: "RPM negative count", invocation: "gobfd-preremove", args: []string{"/var/tmp/rpm-script", "-1"}},
		{name: "RPM extra argument", invocation: "gobfd-preremove", args: []string{"/var/tmp/rpm-script", "0", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := plan(tt.invocation, tt.args); err == nil {
				t.Fatal("plan unexpectedly accepted malformed invocation")
			}
		})
	}
}

func TestSystemdDeclarativeInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "sysusers",
			path: "deployments/systemd/gobfd.sysusers",
			want: "u gobfd - \"GoBFD daemon\" /nonexistent -\n",
		},
		{
			name: "tmpfiles",
			path: "deployments/systemd/gobfd.tmpfiles",
			want: "d /etc/gobfd 0750 root gobfd - -\n",
		},
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join(repositoryRoot, tt.path))
			if err != nil {
				t.Fatalf("read %s: %v", tt.path, err)
			}
			if got := string(data); got != tt.want {
				t.Fatalf("%s contents = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestExecuteInstallCommands(t *testing.T) {
	binDir, logPath := installHelperCommands(t, "systemd-sysusers", "systemd-tmpfiles", "systemctl")
	t.Setenv("PATH", binDir)

	commands, err := plan("gobfd.postinst", []string{"configure", ""})
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	if err := execute(t.Context(), commands, discardLogger()); err != nil {
		t.Fatalf("execute install: %v", err)
	}

	want := strings.Join([]string{
		"systemd-sysusers /usr/lib/sysusers.d/gobfd.conf",
		"systemd-tmpfiles --create /usr/lib/tmpfiles.d/gobfd.conf",
		"systemctl daemon-reload",
	}, "\n") + "\n"
	if got := readLog(t, logPath); got != want {
		t.Fatalf("command log = %q, want %q", got, want)
	}
}

func TestExecuteSkipsAbsentSystemctl(t *testing.T) {
	binDir, logPath := installHelperCommands(t, "systemd-sysusers", "systemd-tmpfiles")
	t.Setenv("PATH", binDir)

	commands, err := plan("gobfd.postinst", []string{"configure", ""})
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	if err := execute(t.Context(), commands, discardLogger()); err != nil {
		t.Fatalf("execute without systemctl: %v", err)
	}

	want := "systemd-sysusers /usr/lib/sysusers.d/gobfd.conf\n" +
		"systemd-tmpfiles --create /usr/lib/tmpfiles.d/gobfd.conf\n"
	if got := readLog(t, logPath); got != want {
		t.Fatalf("command log = %q, want %q", got, want)
	}
}

func TestExecuteRejectsAbsentRequiredCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	commands, err := plan("gobfd.postinst", []string{"configure", ""})
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	if err := execute(t.Context(), commands, discardLogger()); err == nil {
		t.Fatal("execute accepted absent systemd-sysusers")
	}
}

func TestExecuteWrapsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	commands, err := plan("gobfd.postinst", []string{"configure", ""})
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	if err := execute(ctx, commands, discardLogger()); !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error = %v, want context cancellation", err)
	}
}

func TestExecuteToleratesStopAndDisableFailures(t *testing.T) {
	binDir, logPath := installHelperCommands(t, "systemctl")
	t.Setenv("PATH", binDir)
	t.Setenv("GOBFD_PACKAGE_LIFECYCLE_FAIL_ARGS", "stop,disable")

	commands, err := plan("gobfd.prerm", []string{"remove"})
	if err != nil {
		t.Fatalf("plan removal: %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := execute(t.Context(), commands, logger); err != nil {
		t.Fatalf("execute removal: %v", err)
	}

	want := "systemctl stop gobfd\n" +
		"systemctl disable gobfd\n" +
		"systemctl daemon-reload\n"
	if got := readLog(t, logPath); got != want {
		t.Fatalf("command log = %q, want %q", got, want)
	}
	if got := strings.Count(logs.String(), "command failed; continuing"); got != 2 {
		t.Fatalf("tolerated failure log count = %d, want 2; logs: %s", got, logs.String())
	}
}

func TestExecuteRejectsDaemonReloadFailure(t *testing.T) {
	binDir, logPath := installHelperCommands(t, "systemctl")
	t.Setenv("PATH", binDir)
	t.Setenv("GOBFD_PACKAGE_LIFECYCLE_FAIL_ARGS", "daemon-reload")

	commands, err := plan("gobfd.prerm", []string{"remove"})
	if err != nil {
		t.Fatalf("plan removal: %v", err)
	}
	if err := execute(t.Context(), commands, discardLogger()); err == nil {
		t.Fatal("execute accepted daemon-reload failure")
	}

	want := "systemctl stop gobfd\n" +
		"systemctl disable gobfd\n" +
		"systemctl daemon-reload\n"
	if got := readLog(t, logPath); got != want {
		t.Fatalf("command log = %q, want %q", got, want)
	}
}

func TestExecuteInheritsStandardStreams(t *testing.T) {
	binDir, _ := installHelperCommands(t, "systemctl")
	t.Setenv("PATH", binDir)
	t.Setenv("GOBFD_PACKAGE_LIFECYCLE_STREAMS", "1")

	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("create stdin: %v", err)
	}
	defer stdin.Close()
	if _, writeErr := stdin.WriteString("package-input"); writeErr != nil {
		t.Fatalf("write stdin: %v", writeErr)
	}
	if _, seekErr := stdin.Seek(0, 0); seekErr != nil {
		t.Fatalf("rewind stdin: %v", seekErr)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer stdoutReader.Close()
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	defer stderrReader.Close()

	originalStdin, originalStdout, originalStderr := os.Stdin, os.Stdout, os.Stderr
	t.Cleanup(func() {
		os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr
	})
	os.Stdin, os.Stdout, os.Stderr = stdin, stdoutWriter, stderrWriter

	err = execute(t.Context(), []command{{name: "systemctl", args: []string{"daemon-reload"}}}, discardLogger())
	os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr
	if closeErr := stdoutWriter.Close(); closeErr != nil {
		t.Fatalf("close stdout writer: %v", closeErr)
	}
	if closeErr := stderrWriter.Close(); closeErr != nil {
		t.Fatalf("close stderr writer: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if got, want := string(stdout), "stdout:package-input"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := string(stderr), "stderr:package-input"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func installHelperCommands(t *testing.T, names ...string) (string, string) {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	binDir := t.TempDir()
	for _, name := range names {
		if err := os.Symlink(executable, filepath.Join(binDir, name)); err != nil {
			t.Fatalf("link helper command %s: %v", name, err)
		}
	}
	logPath := filepath.Join(t.TempDir(), "commands.log")
	t.Setenv(helperProcessEnv, "1")
	t.Setenv("GOBFD_PACKAGE_LIFECYCLE_LOG", logPath)
	return binDir, logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	return string(data)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func runHelperProcess() {
	logPath := os.Getenv("GOBFD_PACKAGE_LIFECYCLE_LOG")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(97)
	}
	line := filepath.Base(os.Args[0]) + " " + strings.Join(os.Args[1:], " ") + "\n"
	if _, err := logFile.WriteString(line); err != nil {
		_ = logFile.Close()
		os.Exit(98)
	}
	if err := logFile.Close(); err != nil {
		os.Exit(99)
	}
	if os.Getenv("GOBFD_PACKAGE_LIFECYCLE_STREAMS") == "1" {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(96)
		}
		if _, err := os.Stdout.Write(append([]byte("stdout:"), input...)); err != nil {
			os.Exit(95)
		}
		if _, err := os.Stderr.Write(append([]byte("stderr:"), input...)); err != nil {
			os.Exit(94)
		}
	}

	failArgs := strings.Split(os.Getenv("GOBFD_PACKAGE_LIFECYCLE_FAIL_ARGS"), ",")
	if len(os.Args) > 1 {
		for _, arg := range failArgs {
			if os.Args[1] == arg {
				os.Exit(23)
			}
		}
	}
	os.Exit(0)
}
