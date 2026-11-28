package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type runtimeStatusRunner struct {
	state            string
	stateInspections int
}

func (runner *runtimeStatusRunner) Run(_ context.Context, command Command) (Result, error) {
	switch command.Arguments[0] {
	case "inspect":
		format := command.Arguments[slices.Index(command.Arguments, "--format")+1]
		if format == "{{json .State}}" {
			runner.stateInspections++
			return Result{Stdout: runner.state}, nil
		}
		return Result{}, fmt.Errorf("unexpected runtime inspect format: %s", format)
	default:
		return Result{}, fmt.Errorf("unexpected runtime status command: %v", command.Arguments)
	}
}

func TestVendorRuntimeRequiresConfirmedSuccessfulExit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, state string
		wantErr     bool
	}{
		{"successful completion", `{"Status":"exited","Running":false,"ExitCode":0,"OOMKilled":false}`, false},
		{"nonzero exit", `{"Status":"exited","Running":false,"ExitCode":42,"OOMKilled":false}`, true},
		{"OOM despite zero exit", `{"Status":"exited","Running":false,"ExitCode":0,"OOMKilled":true}`, true},
		{"still running", `{"Status":"running","Running":true,"ExitCode":0,"OOMKilled":false}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := &runtimeStatusRunner{state: tc.state}
			err := verifyTestControlExit(t.Context(), runner, strings.Repeat("2", 64))
			if (tc.wantErr && !errors.Is(err, ErrBootstrapFailed)) || (!tc.wantErr && err != nil) ||
				runner.stateInspections != 1 {
				t.Fatalf("runtime status not confirmed: error=%v inspections=%d", err, runner.stateInspections)
			}
		})
	}
}
