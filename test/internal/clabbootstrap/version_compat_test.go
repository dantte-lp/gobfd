package clabbootstrap

import (
	"context"
	"errors"
	"testing"
)

type versionJSONRunner struct {
	recordingRunner

	versionJSON string
}

func (runner *versionJSONRunner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == executableContainerlab {
		return Result{Stdout: runner.versionJSON}, nil
	}
	return runner.recordingRunner.Run(ctx, command)
}

func TestPreflightContainerlabReleaseJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version string
		wantErr bool
	}{
		{
			name: "accept current release fields",
			version: `{"version":"0.82.0","commit":"4272331ab","date":"2026-10-23T07:57:28Z",` +
				`"repository":"https://github.com/srl-labs/containerlab","releaseNotes":"https://containerlab.dev/rn/0.82/"}`,
		},
		{name: "reject previous release", version: `{"version":"0.79.0"}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := DefaultOptions(t.TempDir())
			options.SkipBuild = true
			runner := &versionJSONRunner{versionJSON: test.version}
			err := runPreflight(t.Context(), options, runner)
			if test.wantErr {
				if !errors.Is(err, errInvalidBootstrapOptions) {
					t.Fatalf("preflight previous release = %v, want invalid options", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("preflight current release JSON: %v", err)
			}
		})
	}
}
