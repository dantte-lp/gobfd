package cirunner

import (
	"bytes"
	"context"
	"fmt"
	"go/build/constraint"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	lintEnabledCount            = 92
	containertestPackagePattern = "./test/internal/containertest/..."
	prebuiltLintPath            = "/go/bin/golangci-lint"
	goToolSubcommand            = "tool"
	toolsModuleFlag             = "-modfile=tools/go.mod"
)

type lintProfile struct {
	tag      string
	packages []string
}

func defaultLintProfiles() []lintProfile {
	return []lintProfile{
		{tag: "integration", packages: []string{"./test/integration/..."}},
		{tag: "testcontainers", packages: []string{containertestPackagePattern}},
		{tag: "e2e_core_testcontainers", packages: []string{
			"./test/internal/podmanapi/...", containertestPackagePattern, "./test/e2e/core/...",
		}},
		{tag: "e2e_bgp_failover_testcontainers", packages: []string{
			"./test/internal/podmanapi/...", containertestPackagePattern, "./test/e2e/bgp-failover/...",
		}},
		{tag: "e2e_haproxy_testcontainers", packages: []string{
			"./test/internal/podmanapi/...", containertestPackagePattern, "./test/e2e/haproxy-health/...",
		}},
		{tag: "e2e_observability_testcontainers", packages: []string{
			"./test/internal/podmanapi/...", containertestPackagePattern, "./test/e2e/observability/...",
		}},
		{tag: "e2e_linux", packages: []string{"./test/e2e/linux/..."}},
		{tag: "e2e_overlay", packages: []string{"./test/e2e/overlay/..."}},
		{tag: "e2e_vendor", packages: []string{"./test/e2e/vendor/..."}},
		{tag: "interop", packages: []string{"./test/interop/..."}},
		{tag: "interop_testcontainers", packages: []string{"./test/interop/..."}},
		{tag: "interop_bgp", packages: []string{"./test/interop-bgp/..."}},
		{tag: "interop_bgp_testcontainers", packages: []string{"./test/interop-bgp/..."}},
		{tag: "interop_clab", packages: []string{"./test/interop-clab/..."}},
		{tag: "interop_rfc", packages: []string{"./test/interop-rfc/..."}},
		{tag: "interop_rfc_testcontainers", packages: []string{"./test/interop-rfc/..."}},
		{tag: "dependencyinventory_generate", packages: []string{"./test/cmd/dependencyinventory/..."}},
	}
}

type lintExecution struct {
	root        string
	environment []string
	runner      SpecRunner
	linter      lintCommand
}

type lintCommand struct {
	name      string
	arguments []string
}

// LintOptions supplies the repository root and direct command runner.
type LintOptions struct {
	Root          string
	Environment   []string
	Runner        SpecRunner
	Containerized func() bool
}

// Lint runs the complete bounded lint profile without shell orchestration.
func Lint(ctx context.Context, options LintOptions) error {
	execution, err := prepareLintExecution(options)
	if err != nil {
		return err
	}
	if err := execution.verifyContract(ctx); err != nil {
		return err
	}
	if err := execution.runLinter(ctx, "run", "./..."); err != nil {
		return err
	}
	for _, profile := range defaultLintProfiles() {
		if err := execution.runProfile(ctx, profile); err != nil {
			return err
		}
	}
	return nil
}

func prepareLintExecution(options LintOptions) (lintExecution, error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "lint repository root")
	if err != nil {
		return lintExecution{}, err
	}
	if options.Runner == nil {
		return lintExecution{}, fmt.Errorf("lint command runner is required: %w", errInvalidConfig)
	}
	containerized := options.Containerized
	if containerized == nil {
		containerized = runningInContainer
	}
	if !containerized() {
		return lintExecution{}, fmt.Errorf("lint is container-only; use make lint locally: %w", errInvalidConfig)
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	environment = append(withoutEnvironmentKeys(environment, "GOMAXPROCS", "GOMEMLIMIT"),
		"GOMAXPROCS=2", "GOMEMLIMIT=1500MiB")
	linter, err := resolveLintCommand(environment)
	if err != nil {
		return lintExecution{}, err
	}
	return lintExecution{root: root, environment: environment, runner: options.Runner, linter: linter}, nil
}

func (execution lintExecution) verifyContract(ctx context.Context) error {
	if err := execution.runLinter(ctx, "config", "verify"); err != nil {
		return err
	}
	packageCount, err := execution.goListCount(ctx,
		"-buildvcs=false", "-f", "{{.ImportPath}}", "./...")
	if err != nil {
		return fmt.Errorf("discover base lint packages: %w", err)
	}
	productionCount, err := execution.goListCount(ctx,
		"-buildvcs=false", "-f", lintProductionInputTemplate, "./...")
	if err != nil {
		return fmt.Errorf("discover base production lint inputs: %w", err)
	}
	testCount, err := execution.goListCount(ctx,
		"-buildvcs=false", "-f", lintTestInputTemplate, "./...")
	if err != nil {
		return fmt.Errorf("discover base test lint inputs: %w", err)
	}
	slog.Info("lint base inputs", "packages", packageCount, "production_inputs", productionCount, "test_inputs", testCount)

	moduleVersion, err := execution.output(ctx, "go",
		"list", toolsModuleFlag, "-m", "-f", "{{.Version}}",
		"github.com/golangci/golangci-lint/v2")
	if err != nil {
		return fmt.Errorf("resolve configured golangci-lint version: %w", err)
	}
	binaryVersion, err := execution.linterOutput(ctx, "version", "--short")
	if err != nil {
		return fmt.Errorf("resolve golangci-lint binary version: %w", err)
	}
	if strings.TrimSpace(moduleVersion) != "v"+strings.TrimPrefix(strings.TrimSpace(binaryVersion), "v") {
		return fmt.Errorf("golangci-lint module %q does not match binary %q: %w",
			strings.TrimSpace(moduleVersion), strings.TrimSpace(binaryVersion), errInvalidConfig)
	}
	linters, err := execution.linterOutput(ctx, "linters")
	if err != nil {
		return fmt.Errorf("list configured golangci-lint linters: %w", err)
	}
	enabled, deprecated, err := parseEnabledLinters(linters)
	if err != nil {
		return err
	}
	if enabled != lintEnabledCount || deprecated != 0 {
		return fmt.Errorf("golangci-lint enables %d linters and %d deprecated linters; want %d and 0: %w",
			enabled, deprecated, lintEnabledCount, errInvalidConfig)
	}
	slog.Info("verified golangci-lint contract",
		"enabled", enabled, "version", strings.TrimSpace(binaryVersion))
	return nil
}

func (execution lintExecution) runProfile(ctx context.Context, profile lintProfile) error {
	constrained, err := countBuildTagFiles(execution.root, profile.tag)
	if err != nil {
		return fmt.Errorf("count source files constrained by lint tag %s: %w", profile.tag, err)
	}
	if constrained == 0 {
		return fmt.Errorf("lint tag %s has no constrained source files: %w", profile.tag, errInvalidConfig)
	}
	arguments := make([]string, 0, 5+len(profile.packages))
	arguments = append(arguments, "-buildvcs=false", "-tags", profile.tag, "-f", lintAllInputTemplate)
	arguments = append(arguments, profile.packages...)
	inputs, err := execution.goListCount(ctx, arguments...)
	if err != nil {
		return fmt.Errorf("discover lint inputs for tag %s: %w", profile.tag, err)
	}
	slog.Info("lint tagged inputs", "tag", profile.tag, "constrained_files", constrained, "inputs", inputs)
	if err := execution.runLinter(ctx, "run", "--build-tags", profile.tag, "./..."); err != nil {
		return fmt.Errorf("lint tag %s: %w", profile.tag, err)
	}
	return nil
}

const (
	lintProductionInputTemplate = `{{range .GoFiles}}{{$.ImportPath}}/{{.}}{{"\n"}}{{end}}` +
		`{{range .CgoFiles}}{{$.ImportPath}}/{{.}}{{"\n"}}{{end}}`
	lintTestInputTemplate = `{{range .TestGoFiles}}{{$.ImportPath}}/{{.}}{{"\n"}}{{end}}` +
		`{{range .XTestGoFiles}}{{$.ImportPath}}/{{.}}{{"\n"}}{{end}}`
	lintAllInputTemplate = lintProductionInputTemplate + lintTestInputTemplate
)

func runningInContainer() bool {
	for _, path := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func resolveLintCommand(environment []string) (lintCommand, error) {
	configured := ""
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found && name == "GOLANGCI_LINT" {
			configured = value
		}
	}
	switch configured {
	case "":
		return lintCommand{name: "go", arguments: []string{
			goToolSubcommand, toolsModuleFlag, "golangci-lint",
		}}, nil
	case prebuiltLintPath:
		return lintCommand{name: prebuiltLintPath}, nil
	default:
		return lintCommand{}, fmt.Errorf("unsupported GOLANGCI_LINT path %q: %w", configured, errInvalidConfig)
	}
}

func (execution lintExecution) runLinter(ctx context.Context, arguments ...string) error {
	commandArguments := make([]string, 0, len(execution.linter.arguments)+len(arguments))
	commandArguments = append(commandArguments, execution.linter.arguments...)
	commandArguments = append(commandArguments, arguments...)
	return execution.command(ctx, execution.linter.name, commandArguments...)
}

func (execution lintExecution) linterOutput(ctx context.Context, arguments ...string) (string, error) {
	commandArguments := make([]string, 0, len(execution.linter.arguments)+len(arguments))
	commandArguments = append(commandArguments, execution.linter.arguments...)
	commandArguments = append(commandArguments, arguments...)
	return execution.output(ctx, execution.linter.name, commandArguments...)
}

func (execution lintExecution) command(ctx context.Context, name string, arguments ...string) error {
	if err := execution.runner.RunCommand(ctx, CommandSpec{
		Name: name, Args: arguments, Dir: execution.root, Env: execution.environment,
	}); err != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(arguments, " "), err)
	}
	return nil
}

func (execution lintExecution) output(ctx context.Context, name string, arguments ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := execution.runner.RunCommand(ctx, CommandSpec{
		Name: name, Args: arguments, Dir: execution.root, Env: execution.environment,
		Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("run %s %s; output=%s: %w", name,
			strings.Join(arguments, " "), strings.TrimSpace(stdout.String()+stderr.String()), err)
	}
	return stdout.String(), nil
}

func (execution lintExecution) goListCount(ctx context.Context, arguments ...string) (int, error) {
	output, err := execution.output(ctx, "go", append([]string{"list"}, arguments...)...)
	if err != nil {
		return 0, err
	}
	count := 0
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("go list produced no inputs: %w", errInvalidConfig)
	}
	return count, nil
}

func parseEnabledLinters(output string) (int, int, error) {
	enabledSection := false
	enabled := 0
	deprecated := 0
	for line := range strings.SplitSeq(output, "\n") {
		switch {
		case strings.HasPrefix(line, "Enabled by your configuration"):
			enabledSection = true
		case strings.HasPrefix(line, "Disabled by your configuration"):
			enabledSection = false
		case enabledSection && lintDescription(line):
			enabled++
			if strings.Contains(line, "[deprecated]") {
				deprecated++
			}
		}
	}
	if enabled == 0 {
		return 0, 0, fmt.Errorf("golangci-lint reported no enabled linters: %w", errInvalidConfig)
	}
	return enabled, deprecated, nil
}

func lintDescription(line string) bool {
	name, _, found := strings.Cut(line, ":")
	if !found || name == "" {
		return false
	}
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func countBuildTagFiles(root, tag string) (int, error) {
	count := 0
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor") {
			return fs.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		contains, err := fileContainsBuildTag(filepath.Join(root, path), tag)
		if err != nil {
			return fmt.Errorf("inspect build constraint in %s: %w", path, err)
		}
		if contains {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("walk Go source tree: %w", err)
	}
	return count, nil
}

func fileContainsBuildTag(path, tag string) (bool, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read Go source: %w", err)
	}
	for line := range strings.SplitSeq(string(contents), "\n") {
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !strings.HasPrefix(line, "//go:build ") {
			continue
		}
		expression, err := constraint.Parse(line)
		if err != nil {
			return false, fmt.Errorf("parse build constraint: %w", err)
		}
		return constraintContainsTag(expression, tag), nil
	}
	return false, nil
}

func constraintContainsTag(expression constraint.Expr, tag string) bool {
	switch value := expression.(type) {
	case *constraint.TagExpr:
		return value.Tag == tag
	case *constraint.NotExpr:
		return constraintContainsTag(value.X, tag)
	case *constraint.AndExpr:
		return constraintContainsTag(value.X, tag) || constraintContainsTag(value.Y, tag)
	case *constraint.OrExpr:
		return constraintContainsTag(value.X, tag) || constraintContainsTag(value.Y, tag)
	default:
		return false
	}
}
