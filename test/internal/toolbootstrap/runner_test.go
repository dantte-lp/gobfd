package toolbootstrap

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestVerifyJQRunsFixedVersionCommand(t *testing.T) {
	t.Parallel()

	runner := &recordingOutputRunner{output: "jq-1.8.1"}
	if err := verifyJQ(context.Background(), runner); err != nil {
		t.Fatalf("verifyJQ() error = %v", err)
	}
	want := []outputInvocation{{name: "jq", arguments: []string{"--version"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("jq invocations = %#v, want %#v", runner.calls, want)
	}
}

func TestVerifyJQWrapsCommandFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("jq failed")
	err := verifyJQ(context.Background(), &recordingOutputRunner{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("verifyJQ() error = %v, want wrapped command error", err)
	}
	if !strings.Contains(err.Error(), "verify jq runtime") {
		t.Errorf("verifyJQ() error = %q, want operation context", err)
	}
}

type outputInvocation struct {
	name      string
	arguments []string
}

type recordingOutputRunner struct {
	calls  []outputInvocation
	output string
	err    error
}

func (r *recordingOutputRunner) Output(
	_ context.Context,
	name string,
	arguments, _ []string,
) (string, error) {
	r.calls = append(r.calls, outputInvocation{name: name, arguments: append([]string(nil), arguments...)})
	return r.output, r.err
}
