package cirunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errGeneratedProtoDrift = errors.New("generated protobuf files differ from committed sources")

// ProtoOptions configures generated protobuf verification and its test seams.
type ProtoOptions struct {
	Root        string
	RunnerTemp  string
	Environment []string
	Runner      SpecRunner
}

// ProtoVerify builds pinned generators, regenerates protobuf code, and rejects drift.
func ProtoVerify(ctx context.Context, options ProtoOptions) error {
	if options.Runner == nil {
		return fmt.Errorf("protobuf command runner is required: %w", errInvalidConfig)
	}
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	runnerTemp, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	toolsRoot := filepath.Join(runnerTemp, "gobfd-proto-tools")
	binDir := filepath.Join(toolsRoot, "bin")
	if err := ensureDirectory(toolsRoot, "protobuf tools"); err != nil {
		return err
	}
	if err := ensureDirectory(binDir, "protobuf tools bin"); err != nil {
		return err
	}

	commands := []struct {
		context string
		spec    CommandSpec
	}{
		{
			context: "build protoc-gen-go",
			spec: CommandSpec{Name: "go", Dir: root, Args: []string{
				"build", toolsModuleFlag, "-o", filepath.Join(binDir, "protoc-gen-go"),
				"google.golang.org/protobuf/cmd/protoc-gen-go",
			}},
		},
		{
			context: "build protoc-gen-connect-go",
			spec: CommandSpec{Name: "go", Dir: root, Args: []string{
				"build", toolsModuleFlag, "-o", filepath.Join(binDir, "protoc-gen-connect-go"),
				"connectrpc.com/connect/v2/cmd/protoc-gen-connect-go",
			}},
		},
		{
			context: "generate protobuf code",
			spec: CommandSpec{
				Name: "buf", Args: []string{"generate"}, Dir: root,
				Env: prependPath(options.Environment, binDir),
			},
		},
		{
			context: "verify generated protobuf code",
			spec:    CommandSpec{Name: "git", Args: []string{"diff", "--exit-code", "--", "pkg/bfdpb"}, Dir: root},
		},
	}
	for _, command := range commands {
		if err := options.Runner.RunCommand(ctx, command.spec); err != nil {
			return fmt.Errorf("%s: %w", command.context, err)
		}
	}
	return verifyGeneratedProtoStatus(ctx, root, options.Runner)
}

func verifyGeneratedProtoStatus(ctx context.Context, root string, runner SpecRunner) error {
	var status bytes.Buffer
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "git", Dir: root, Stdout: &status,
		Args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--", "pkg/bfdpb"},
	}); err != nil {
		return fmt.Errorf("inspect generated protobuf code: %w", err)
	}
	if status.Len() != 0 {
		return fmt.Errorf("verify generated protobuf code: %w", errGeneratedProtoDrift)
	}
	return nil
}

func validateAbsoluteExistingDirectory(path, purpose string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be absolute: %w", purpose, errInvalidConfig)
	}
	absolute, err := validateSafeDirectory(path, purpose, true)
	if err != nil {
		return "", err
	}
	if err := inspectDirectoryTree(absolute, purpose); err != nil {
		return "", err
	}
	return absolute, nil
}

func prependPath(environment []string, directory string) []string {
	result := make([]string, 0, len(environment)+1)
	path := ""
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found && name == "PATH" {
			path = value
			continue
		}
		result = append(result, entry)
	}
	if path == "" {
		result = append(result, "PATH="+directory)
	} else {
		result = append(result, "PATH="+directory+string(os.PathListSeparator)+path)
	}
	return result
}
