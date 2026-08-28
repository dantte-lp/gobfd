package repoquality

import (
	"context"
	"errors"
	"testing"
)

func TestCheckGoplsRejectsEmptyDiscovery(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		goListOutput string
		want         error
	}{
		"no packages": {want: errNoGoPackages},
		"no Go inputs": {
			goListOutput: `{"ImportPath":"example.invalid/empty","Dir":"/tmp/empty"}`,
			want:         errNoGoInputs,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			goplsCalled := false
			runner := goplsRunnerFunc(func(_ context.Context, command goplsCommand) (goplsCommandResult, error) {
				switch command.executable {
				case "go":
					return goplsCommandResult{stdout: test.goListOutput}, nil
				case "gopls":
					goplsCalled = true
					return goplsCommandResult{}, nil
				default:
					return goplsCommandResult{}, errors.New("unexpected command")
				}
			})
			_, err := checkGopls(t.Context(), GoplsOptions{
				Root:        t.TempDir(),
				Profiles:    []string{"integration"},
				Environment: []string{"PATH=/usr/bin"},
			}, runner)
			if !errors.Is(err, test.want) {
				t.Fatalf("checkGopls() error = %v, want %v", err, test.want)
			}
			if goplsCalled {
				t.Fatal("gopls ran after empty discovery")
			}
		})
	}
}

type goplsRunnerFunc func(context.Context, goplsCommand) (goplsCommandResult, error)

func (run goplsRunnerFunc) run(ctx context.Context, command goplsCommand) (goplsCommandResult, error) {
	return run(ctx, command)
}
