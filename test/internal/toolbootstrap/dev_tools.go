package toolbootstrap

import (
	"context"
	"fmt"
	"os"
	"regexp"
)

// InstallDevelopmentTools preserves the image's five explicit version selections.
func InstallDevelopmentTools(ctx context.Context, environment []string) error {
	return installDevelopmentTools(ctx, environment, runStreaming)
}

func installDevelopmentTools(ctx context.Context, environment []string,
	run func(context.Context, string, []string, []string) error,
) error {
	// Noncanonical versions can resolve as branches; metadata is unnecessary for these modules.
	const numeric = `(0|[1-9][0-9]*)`
	const identifier = `(` + numeric + `|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)`
	pinnedQuery := regexp.MustCompile(`^v` + numeric + `\.` + numeric + `\.` + numeric +
		`(-` + identifier + `(\.` + identifier + `)*)?$`)
	tools := []struct {
		variable string
		path     string
	}{
		{"BUF_VERSION", "github.com/bufbuild/buf/cmd/buf"},
		{"PROTOC_GEN_GO_VERSION", "google.golang.org/protobuf/cmd/protoc-gen-go"},
		{"PROTOC_GEN_CONNECT_GO_VERSION", "connectrpc.com/connect/v2/cmd/protoc-gen-connect-go"},
		{"BENCHSTAT_VERSION", "golang.org/x/perf/cmd/benchstat"},
		{"GOTESTSUM_VERSION", "gotest.tools/gotestsum"},
	}
	queries := make([]string, 0, len(tools))
	for _, tool := range tools {
		version := environmentValue(environment, tool.variable)
		if !pinnedQuery.MatchString(version) {
			return fmt.Errorf("%s requires an exact Go module version: %w", tool.variable, os.ErrInvalid)
		}
		queries = append(queries, tool.path+"@"+version)
	}
	for _, query := range queries {
		if err := run(ctx, "go", []string{"install", query}, environment); err != nil {
			return fmt.Errorf("install development tool %s: %w", query, err)
		}
	}
	return nil
}
