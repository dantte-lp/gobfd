package toolbootstrap

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
)

func TestDevelopmentToolsKeepPinnedBuildArgumentsAndStopOnFailure(t *testing.T) {
	t.Parallel()
	environment := []string{
		"BUF_VERSION=v1.75.0", "PROTOC_GEN_GO_VERSION=v1.36.12",
		"PROTOC_GEN_CONNECT_GO_VERSION=v2.0.2",
		"BENCHSTAT_VERSION=v0.0.0-20261009192801-be2c69fb417e", "GOTESTSUM_VERSION=v1.13.0",
	}
	want := []string{
		"github.com/bufbuild/buf/cmd/buf@v1.75.0",
		"google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12",
		"connectrpc.com/connect/v2/cmd/protoc-gen-connect-go@v2.0.2",
		"golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e",
		"gotest.tools/gotestsum@v1.13.0",
	}
	failure := errors.New("tool install failed")
	for failAt := 0; failAt <= len(want); failAt++ {
		calls := 0
		run := func(_ context.Context, name string, arguments, actualEnvironment []string) error {
			calls++
			if calls > len(want) || name != "go" ||
				!slices.Equal(arguments, []string{"install", want[calls-1]}) || !slices.Equal(actualEnvironment, environment) {
				t.Fatalf("tool command %d changed: %s %v", calls, name, arguments)
			}
			if calls == failAt {
				return failure
			}
			return nil
		}
		err := installDevelopmentTools(t.Context(), environment, run)
		wantCalls := len(want)
		if failAt != 0 {
			wantCalls = failAt
			if !errors.Is(err, failure) {
				t.Fatalf("tool failure %d lost: %v", failAt, err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if calls != wantCalls {
			t.Fatalf("tool failure %d ran %d commands, want %d", failAt, calls, wantCalls)
		}
	}
}

func TestDevelopmentToolsRejectFloatingOrAlternateQueriesBeforeInstalling(t *testing.T) {
	t.Parallel()
	for _, version := range []string{
		"", "latest", "main", "v1", "v1.13.0\n", "v1.13.0 @latest", "v1.13.0/other",
		"v01.13.0", "v1.13.0-01", "v1.13.0-a..b", "v1.13.0+metadata", "v1.13.0+incompatible",
	} {
		environment := []string{
			"BUF_VERSION=v1.75.0", "PROTOC_GEN_GO_VERSION=v1.36.12",
			"PROTOC_GEN_CONNECT_GO_VERSION=v2.0.2",
			"BENCHSTAT_VERSION=v0.0.0-20261009192801-be2c69fb417e", "GOTESTSUM_VERSION=" + version,
		}
		run := func(context.Context, string, []string, []string) error {
			t.Fatal("invalid final tool query installed an earlier tool")
			return nil
		}
		if err := installDevelopmentTools(t.Context(), environment, run); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("invalid version %q returned %v", version, err)
		}
	}
}
