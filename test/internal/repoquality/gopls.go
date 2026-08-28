package repoquality

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const goplsCommandWaitDelay = 5 * time.Second

var (
	errNoGoPackages           = errors.New("no packages discovered")
	errNoGoInputs             = errors.New("no Go inputs discovered")
	errGoplsDiagnostics       = errors.New("gopls diagnostics found")
	errGoplsCommandNotAllowed = errors.New("gopls command is not allowlisted")
	errGoplsRootNotDirectory  = errors.New("gopls root is not a directory")
	errInvalidGoplsProfile    = errors.New("invalid empty or whitespace-containing gopls profile")
)

// GoplsOptions defines one repository-wide diagnostics pass.
type GoplsOptions struct {
	Root        string
	GOOS        string
	GOARCH      string
	Profiles    []string
	Environment []string
}

// GoplsReport records the exact discovery scope checked by gopls.
type GoplsReport struct {
	ProfileCount int
	PackageCount int
	InputCount   int
}

// DefaultGoplsProfiles returns the mutually compatible build-tag profiles
// required to cover every repository-owned Go surface.
func DefaultGoplsProfiles() []string {
	return []string{
		"integration,testcontainers,interop,interop_bgp,interop_rfc,interop_clab,e2e_overlay,e2e_linux,e2e_vendor",
		"e2e_core_testcontainers",
		"e2e_bgp_failover_testcontainers",
		"e2e_haproxy_testcontainers",
		"e2e_observability_testcontainers",
		"interop_testcontainers",
		"interop_bgp_testcontainers",
		"interop_rfc_testcontainers",
		"dependencyinventory_generate",
	}
}

// CheckGopls discovers all Go inputs and runs one bounded gopls process for
// each build-tag profile.
func CheckGopls(ctx context.Context, options GoplsOptions) (GoplsReport, error) {
	return checkGopls(ctx, options, osGoplsRunner{})
}

//nolint:tagliatelle // The go command emits exported Go field names as its fixed JSON contract.
type goListPackage struct {
	ImportPath   string   `json:"ImportPath"`
	Dir          string   `json:"Dir"`
	GoFiles      []string `json:"GoFiles"`
	TestGoFiles  []string `json:"TestGoFiles"`
	XTestGoFiles []string `json:"XTestGoFiles"`
}

type goplsCommand struct {
	executable  string
	arguments   []string
	directory   string
	environment []string
}

type goplsCommandResult struct {
	stdout string
	stderr string
}

type goplsRunner interface {
	run(ctx context.Context, command goplsCommand) (goplsCommandResult, error)
}

type osGoplsRunner struct{}

func (osGoplsRunner) run(ctx context.Context, command goplsCommand) (goplsCommandResult, error) {
	if command.executable != "go" && command.executable != "gopls" {
		return goplsCommandResult{}, fmt.Errorf("execute %q: %w", command.executable, errGoplsCommandNotAllowed)
	}
	executable, err := exec.LookPath(command.executable)
	if err != nil {
		return goplsCommandResult{}, fmt.Errorf("resolve %s executable: %w", command.executable, err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// #nosec G204 -- the executable is resolved from a fixed allowlist and every argument is passed without a shell.
	process := exec.CommandContext(ctx, executable, command.arguments...)
	process.Dir = command.directory
	process.Env = command.environment
	process.Stdout = &stdout
	process.Stderr = &stderr
	process.WaitDelay = goplsCommandWaitDelay
	if err := process.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return goplsCommandResult{}, fmt.Errorf("run %s with context: %w", command.executable, ctxErr)
		}
		return goplsCommandResult{stdout: stdout.String(), stderr: stderr.String()},
			fmt.Errorf("run %s: %w", command.executable, err)
	}
	return goplsCommandResult{stdout: stdout.String(), stderr: stderr.String()}, nil
}

func checkGopls(ctx context.Context, options GoplsOptions, runner goplsRunner) (GoplsReport, error) {
	normalized, err := normalizeGoplsOptions(options)
	if err != nil {
		return GoplsReport{}, err
	}
	report := GoplsReport{}
	for _, profile := range normalized.Profiles {
		environment := goplsEnvironment(normalized.Environment, normalized.GOOS, normalized.GOARCH, profile)
		packages, inputs, discoverErr := discoverGoplsInputs(ctx, normalized.Root, environment, runner)
		if discoverErr != nil {
			return report, fmt.Errorf("discover gopls inputs; tags=%s: %w", profile, discoverErr)
		}
		result, runErr := runner.run(ctx, goplsCommand{
			executable:  "gopls",
			arguments:   append([]string{"check"}, inputs...),
			directory:   normalized.Root,
			environment: environment,
		})
		if runErr != nil {
			return report, fmt.Errorf("gopls check; tags=%s; output=%s: %w", profile, commandOutput(result), runErr)
		}
		if output := commandOutput(result); output != "" {
			return report, fmt.Errorf("%w; tags=%s:\n%s", errGoplsDiagnostics, profile, output)
		}
		report.ProfileCount++
		report.PackageCount += packages
		report.InputCount += len(inputs)
	}
	return report, nil
}

func normalizeGoplsOptions(options GoplsOptions) (GoplsOptions, error) {
	if options.Root == "" {
		options.Root = "."
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return GoplsOptions{}, fmt.Errorf("resolve gopls root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return GoplsOptions{}, fmt.Errorf("stat gopls root: %w", err)
	}
	if !info.IsDir() {
		return GoplsOptions{}, fmt.Errorf("gopls root %s: %w", root, errGoplsRootNotDirectory)
	}
	options.Root = root
	if options.GOOS == "" {
		options.GOOS = "linux"
	}
	if options.GOARCH == "" {
		options.GOARCH = "amd64"
	}
	if len(options.Profiles) == 0 {
		options.Profiles = DefaultGoplsProfiles()
	}
	for _, profile := range options.Profiles {
		if strings.TrimSpace(profile) == "" || strings.ContainsAny(profile, " \t\r\n") {
			return GoplsOptions{}, fmt.Errorf("gopls profile %q: %w", profile, errInvalidGoplsProfile)
		}
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	return options, nil
}

func discoverGoplsInputs(
	ctx context.Context,
	root string,
	environment []string,
	runner goplsRunner,
) (int, []string, error) {
	result, err := runner.run(ctx, goplsCommand{
		executable:  "go",
		arguments:   []string{"list", "-json", "./..."},
		directory:   root,
		environment: environment,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("go list; output=%s: %w", commandOutput(result), err)
	}
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	inputs := make([]string, 0)
	packageCount := 0
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return 0, nil, fmt.Errorf("decode go list package: %w", err)
		}
		packageCount++
		for _, name := range slices.Concat(pkg.GoFiles, pkg.TestGoFiles, pkg.XTestGoFiles) {
			inputs = append(inputs, filepath.Join(pkg.Dir, name))
		}
	}
	if packageCount == 0 {
		return 0, nil, errNoGoPackages
	}
	if len(inputs) == 0 {
		return 0, nil, errNoGoInputs
	}
	slices.Sort(inputs)
	return packageCount, inputs, nil
}

func goplsEnvironment(base []string, goos, goarch, profile string) []string {
	environment := slices.DeleteFunc(slices.Clone(base), func(value string) bool {
		name, _, _ := strings.Cut(value, "=")
		return name == "GOOS" || name == "GOARCH" || name == "GOFLAGS"
	})
	goFlags := environmentValue(base, "GOFLAGS")
	if goFlags != "" {
		goFlags += " "
	}
	return append(environment,
		"GOOS="+goos,
		"GOARCH="+goarch,
		"GOFLAGS="+goFlags+"-tags="+profile,
	)
}

func environmentValue(environment []string, target string) string {
	for _, entry := range slices.Backward(environment) {
		name, value, found := strings.Cut(entry, "=")
		if found && name == target {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func commandOutput(result goplsCommandResult) string {
	return strings.TrimSpace(strings.TrimSpace(result.stdout) + "\n" + strings.TrimSpace(result.stderr))
}
