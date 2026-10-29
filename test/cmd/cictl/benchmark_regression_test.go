package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

// Only copied Go test executables in this process test use the helper dispatch.
func TestMain(m *testing.M) {
	if os.Getenv("GOBFD_BENCH_REPORT_HELPER") == "1" && strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "go" {
		if len(os.Args) > 1 && os.Args[1] == "benchmark-report" {
			main()
			os.Exit(0)
		}
		data := []byte("benchmark comparison\n")
		if slices.Contains(os.Args, "-format") {
			var err error
			data, err = os.ReadFile(os.Getenv("GOBFD_BENCH_REPORT_CSV"))
			if err != nil {
				os.Exit(2)
			}
		}
		if _, err := os.Stdout.Write(data); err != nil {
			os.Exit(2)
		}
		if _, err := io.WriteString(os.Stderr, "fixture benchstat note\n"); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBenchmarkReportProcessExitRetainsReports(t *testing.T) {
	// Serial: the existing ExecRunner resolves the scoped PATH fixture in this process.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, test := range []struct {
		name, delta, head, input string
		exit                     int
	}{
		{"critical", "+20.00%", "2.4e-7", "valid", 1},
		{"good", "+5.00%", "2.1e-7", "valid", 0},
		{"noisy", "~", "2.4e-7", "valid", 0},
		{"invalid", "~", "2e-7", "BenchmarkBroken-2 0 200 ns/op\n", 1},
		{"empty", "~", "2e-7", "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			const raw = "goos: linux\ngoarch: amd64\npkg: fixture\n" +
				"BenchmarkRecvDecodeFSM-2 64 200 ns/op 0 B/op 0 allocs/op\nPASS\n"
			for _, file := range []string{"old.txt", "new.txt"} {
				value := strings.Repeat(raw, 6)
				if file == "new.txt" && test.input != "valid" {
					value = test.input
				}
				if err := os.WriteFile(filepath.Join(root, file), []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			csv := ",old.txt,,new.txt,,,\n,sec/op,CI,sec/op,CI,vs base,P\nRecvDecodeFSM-2,2e-7,1%," +
				test.head + ",1%," + test.delta + ",p=0.001 n=6\n"
			csvPath := filepath.Join(root, "fixture.csv")
			if err := os.WriteFile(csvPath, []byte(csv), 0o600); err != nil {
				t.Fatal(err)
			}
			summary := filepath.Join(root, "summary")
			if err := os.WriteFile(summary, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			runner := cirunner.ExecRunner{Stdout: &output, Stderr: &output}
			err := runner.RunCommand(ctx, cirunner.CommandSpec{
				Name: "go", Args: []string{"benchmark-report"}, Dir: root,
				Env: append(os.Environ(), "GOBFD_BENCH_REPORT_HELPER=1", "GOBFD_BENCH_REPORT_CSV="+csvPath,
					"GITHUB_STEP_SUMMARY="+summary), WaitDelay: time.Second,
			})
			code := 0
			if err != nil {
				var processError interface{ ExitCode() int }
				if !errors.As(err, &processError) {
					t.Fatalf("non-exit command error: %v; %s", err, output.String())
				}
				code = processError.ExitCode()
			}
			if code != test.exit {
				t.Errorf("actual CLI exit=%d, want %d: %s", code, test.exit, output.String())
			}
			if test.input != "valid" {
				return
			}
			for _, file := range []string{
				"bench-report.md", "bench-comparison.html", "bench-comparison.json",
				"bench-regression/bench-csv.txt", "bench-regression/benchstat-notes.txt",
			} {
				info, statErr := os.Stat(filepath.Join(root, file))
				if statErr != nil || info.Size() == 0 {
					t.Errorf("retained CLI artifact %s: %v", file, statErr)
				}
			}
			if test.name == "critical" && !strings.Contains(output.String(), "RecvDecodeFSM-2,+20.00%") {
				t.Error("critical command exited without its retained warning")
			}
		})
	}
}
