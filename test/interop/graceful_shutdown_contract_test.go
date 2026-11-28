package interop_test

import (
	"fmt"
	"os"
	"os/exec" //nolint:depguard // Contract runs the actual tagged test with an isolated Go-owned Podman fixture.
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGracefulShutdownCaptureFailures(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(t.TempDir(), "shutdown.test")
	build := exec.CommandContext(
		t.Context(), "go", "test", "-c", "-race", "-tags", "interop", "-o", child, "./test/interop",
	)
	build.Dir = root
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build actual graceful shutdown test: %v\n%s", buildErr, output)
	}
	for _, test := range []struct{ mode, failure string }{
		{"baseline failure", "AdminDown baseline"},
		{"total failure", "tshark query for AdminDown packets"},
		{"peer failure", "tshark query for Holo AdminDown"},
		{"status failure", "Thoro/bfd status lookup failed"},
		{"healthy", ""},
		{"known panic", ""},
	} {
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()
			bin, state := t.TempDir(), t.TempDir()
			mode := installFakeCommand(t, bin, "podman", "graceful-shutdown")
			command := exec.CommandContext(
				t.Context(), child, "-test.run=^TestGracefulShutdown$", "-test.v", "-test.timeout=30s",
			)
			command.Env = append(os.Environ(), mode, fakeRaceOptions,
				"PATH="+bin, "INTEROP_PROJECT_NAME=gobfd-interop",
				"GOBFD_SHUTDOWN_FIXTURE="+state, "GOBFD_SHUTDOWN_MODE="+test.mode)
			output, runErr := command.CombinedOutput()
			if (runErr != nil) != (test.failure != "") {
				t.Fatalf("shutdown mode=%s error=%v; output:\n%s", test.mode, runErr, output)
			}
			if test.failure != "" && !strings.Contains(string(output), test.failure) {
				t.Fatalf("missing causal failure %q:\n%s", test.failure, output)
			}
			if test.mode == "baseline failure" {
				if _, statErr := os.Stat(filepath.Join(state, "stopped")); !os.IsNotExist(statErr) {
					t.Fatal("failed baseline allowed the dependent stop")
				}
			}
			const skipMsg = "AdminDown verification skipped: upstream peer panicked"
			if test.mode == "known panic" && !strings.Contains(string(output), skipMsg) {
				t.Fatalf("documented peer exception lost:\n%s", output)
			}
		})
	}
}

func runGracefulShutdownFake(args []string) int {
	if len(args) < 2 {
		return 125
	}
	mode := os.Getenv("GOBFD_SHUTDOWN_MODE")
	stoppedPath := filepath.Join(os.Getenv("GOBFD_SHUTDOWN_FIXTURE"), "stopped")
	_, statErr := os.Stat(stoppedPath)
	stopped := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return 125
	}
	switch args[0] {
	case "inspect":
		if !strings.Contains(strings.Join(args, " "), "com.docker.compose.project") {
			return 125
		}
		fmt.Fprintf(os.Stdout, "owned-%s-id|gobfd-interop\n", args[len(args)-1])
		return 0
	case "stop":
		if args[1] != "owned-gobfd-interop-id" || os.WriteFile(stoppedPath, []byte("stopped"), 0o600) != nil {
			return 125
		}
		return 0
	case "logs":
		return runGracefulShutdownLogsFake(args, mode)
	case "exec":
		return runGracefulShutdownExecFake(args, mode, stopped)
	}
	return 125
}

func runGracefulShutdownLogsFake(args []string, mode string) int {
	if args[len(args)-1] != "owned-thoro-interop-id" {
		return 125
	}
	if mode == "status failure" {
		fmt.Fprintln(os.Stderr, "Thoro logs unavailable")
		return 3
	}
	if mode == "known panic" {
		fmt.Fprintln(os.Stdout, "panic: Not implemented SetDesiredMinTxInterval")
	}
	return 0
}

func runGracefulShutdownExecFake(args []string, mode string, stopped bool) int {
	if args[1] == "owned-frr-interop-id" && slices.Contains(args, "show bfd peers json") {
		status := "up"
		if stopped {
			status = "down"
		}
		fmt.Fprintf(os.Stdout, `[{"peer":"172.20.0.10","status":%q}]`, status)
		return 0
	}
	if args[1] != "owned-tshark-interop-id" || len(args) < 3 || args[2] != "tshark" {
		return 125
	}
	filterIndex := slices.Index(args, "-Y")
	if filterIndex < 0 || filterIndex+1 >= len(args) {
		return 125
	}
	filter := args[filterIndex+1]
	if (mode == "baseline failure" && !stopped) ||
		(mode == "total failure" && stopped && !strings.Contains(filter, "ip.dst")) ||
		(mode == "peer failure" && strings.Contains(filter, "ip.dst == 172.20.0.50")) {
		fmt.Fprintln(os.Stderr, "tshark: You don't have permission to read the file /captures/bfd.pcapng")
		return 3
	}
	if stopped {
		fmt.Fprintln(os.Stdout, "1")
	}
	return 0
}
