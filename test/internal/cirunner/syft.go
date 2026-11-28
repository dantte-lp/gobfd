package cirunner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const syftName = "syft"

// SyftOptions selects the repository consumer and an exclusive installation directory.
type SyftOptions struct {
	Root        string
	InstallDir  string
	Environment []string
	Runner      SpecRunner
}

// SyftInstallation owns a verified executable until Close releases its private files.
type SyftInstallation = sourceInstallation

// ReleaseSyftOptions supplies the runner-owned paths for the release prerequisite.
type ReleaseSyftOptions struct {
	Root        string
	RunnerTemp  string
	GitHubPath  string
	Environment []string
	Runner      SpecRunner
}

// ReleaseSyft installs the source-built prerequisite outside GoReleaser's dist directory.
func ReleaseSyft(ctx context.Context, options ReleaseSyftOptions) (returnErr error) {
	parent, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	if pathErr := validateGitHubPath(options.GitHubPath); pathErr != nil {
		return pathErr
	}
	root, err := validateAbsoluteExistingDirectory(options.Root, "Syft repository")
	if err != nil {
		return err
	}
	if pathErr := validateSyftOutsideDist(root, parent); pathErr != nil {
		return pathErr
	}
	installation, err := InstallSyft(ctx, SyftOptions{
		Root: root, InstallDir: filepath.Join(parent, "gobfd-syft"),
		Environment: options.Environment, Runner: options.Runner,
	})
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, installation.Close()) }()
	return installation.publishPath(ctx, options.GitHubPath)
}

func validateSyftOutsideDist(root, parent string) error {
	relative, err := filepath.Rel(filepath.Join(root, "dist"), parent)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("syft installation must be outside dist: %w", errors.Join(err, errInvalidConfig))
	}
	return nil
}

// InstallSyft builds from the frozen consumer and atomically publishes a verified file.
func InstallSyft(ctx context.Context, options SyftOptions) (_ *SyftInstallation, returnErr error) {
	if options.Runner == nil {
		return nil, fmt.Errorf("syft command runner is required: %w", errInvalidConfig)
	}
	root, err := validateAbsoluteExistingDirectory(options.Root, "Syft repository")
	if err != nil {
		return nil, err
	}
	moduleDir, err := validateAbsoluteExistingDirectory(
		filepath.Join(root, filepath.Dir(toolprofile.SyftManifest)), "Syft consumer",
	)
	if err != nil {
		return nil, err
	}
	installation, err := prepareSyftInstallation(options.InstallDir, (*os.Root).Mkdir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, installation.Close())
		}
	}()
	environment := syftEnvironment(options.Environment)
	version, err := selectedSyftVersion(ctx, options.Runner, moduleDir, environment)
	if err != nil {
		return nil, err
	}
	if err := installation.buildSyft(ctx, options.Runner, moduleDir, environment, version); err != nil {
		return nil, err
	}
	if err := installation.openExecutableAt("stage/syft"); err != nil {
		return nil, err
	}
	if err := verifySyftVersion(ctx, options.Runner, installation, environment, version); err != nil {
		return nil, err
	}
	if err := installation.publish(); err != nil {
		return nil, err
	}
	return installation, nil
}

func (installation *SyftInstallation) buildSyft(
	ctx context.Context, runner SpecRunner, moduleDir string, environment []string, version string,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start Syft build: %w", err)
	}
	if err := installation.verify(); err != nil {
		return err
	}
	output := filepath.Join(installation.directory, sourceStageDirectory, syftName)
	installation.buildStarted = true
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "go", Dir: moduleDir, Env: environment,
		Args: []string{
			goBuildSubcommand, "-buildvcs=false", "-mod=readonly", "-trimpath", "-tags=" + toolprofile.SyftBuildTags,
			"-ldflags=-X main.version=" + strings.TrimPrefix(version, "v") +
				" -X main.gitCommit=" + toolprofile.SyftSourceCommit + " -X main.gitDescription=" + version,
			"-o", output, toolprofile.SyftCommand,
		},
	}); err != nil {
		return fmt.Errorf("build frozen Syft consumer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("finish Syft build: %w", err)
	}
	return nil
}

func syftEnvironment(environment []string) []string {
	environment = withoutEnvironmentKeys(environment, "GH_TOKEN", "GITHUB_TOKEN", "GOFLAGS", "GOENV", "GOWORK",
		"GOTOOLCHAIN", "GOROOT", "GOTOOLDIR", "GOCACHEPROG", "GOAUTH", "GCCGO", "GO_EXTLINK_ENABLED",
		"GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOARM", "GO386", "GOMIPS",
		"GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM", "GOEXPERIMENT", "GODEBUG", "CGO_ENABLED",
		"CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS", "CC", "CXX", "AR", "PKG_CONFIG")
	return append(environment, "CGO_ENABLED=0", "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off")
}

//nolint:tagliatelle // The owning go list -m -json format uses these exact field names.
type syftSelectedModule struct {
	Path    string           `json:"Path"`
	Version string           `json:"Version"`
	Sum     string           `json:"Sum"`
	Dir     string           `json:"Dir"`
	GoMod   string           `json:"GoMod"`
	Main    bool             `json:"Main"`
	Replace *json.RawMessage `json:"Replace"`
	Error   *json.RawMessage `json:"Error"`
}

func selectedSyftVersion(
	ctx context.Context, runner SpecRunner, directory string, environment []string,
) (string, error) {
	mainModule, err := readSyftSelectedModule(ctx, runner, directory, environment, "")
	if err != nil {
		return "", err
	}
	if mainModule.Path != "github.com/dantte-lp/gobfd/tools/syft" || !mainModule.Main ||
		mainModule.Dir != directory || mainModule.GoMod != filepath.Join(directory, goModFilename) {
		return "", fmt.Errorf("unexpected Syft consumer main module: %w", errInvalidConfig)
	}
	module, err := readSyftSelectedModule(ctx, runner, directory, environment, toolprofile.SyftModule)
	if err != nil {
		return "", err
	}
	if err := module.verifySource(); err != nil {
		return "", err
	}
	return module.Version, nil
}

func readSyftSelectedModule(
	ctx context.Context, runner SpecRunner, directory string, environment []string, selected string,
) (syftSelectedModule, error) {
	var module syftSelectedModule
	output := &synchronizedBoundedBuffer{limit: 64 << 10}
	args := []string{"list", "-mod=readonly", "-m", "-json"}
	if selected != "" {
		args = append(args, selected)
	}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "go", Args: args, Dir: directory, Env: environment, Stdout: output,
	}); err != nil {
		return module, fmt.Errorf("resolve frozen Syft module: %w", err)
	}
	data := output.Bytes()
	if err := validateStrictJSONDocument(data, "Syft selected module"); err != nil {
		return module, err
	}
	if err := json.Unmarshal(data, &module); err != nil {
		return module, fmt.Errorf("decode Syft selected module: %w", err)
	}
	if module.Replace != nil || module.Error != nil {
		return module, fmt.Errorf("syft selected module is replaced or unresolved: %w", errInvalidConfig)
	}
	return module, nil
}

func (module syftSelectedModule) verifySource() error {
	if module.Path != toolprofile.SyftModule || module.Main || module.Sum != toolprofile.SyftSourceSum ||
		!strings.HasPrefix(module.Version, "v") || hasControl(module.Version) ||
		strings.ContainsAny(module.Version, " \t\r\n'\"") || len(module.Version) < 2 {
		return fmt.Errorf("syft module does not match the audited source checksum: %w", errInvalidConfig)
	}
	return nil
}

func verifySyftVersion(ctx context.Context, runner SpecRunner, installation *SyftInstallation,
	environment []string, version string,
) error {
	output := &synchronizedBoundedBuffer{limit: 64 << 10}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: syftName, Args: []string{"version", "--output", "json"},
		Dir: installation.directory, Env: environment, Executable: installation.executable, Stdout: output,
	}); err != nil {
		return fmt.Errorf("verify built Syft version: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify built Syft version: %w", err)
	}
	data := output.Bytes()
	if err := validateStrictJSONDocument(data, "Syft version"); err != nil {
		return err
	}
	//nolint:tagliatelle // Syft version JSON owns these exact camelCase field names.
	var actual struct {
		Application    string `json:"application"`
		Version        string `json:"version"`
		GitCommit      string `json:"gitCommit"`
		GitDescription string `json:"gitDescription"`
		GoVersion      string `json:"goVersion"`
		Compiler       string `json:"compiler"`
		Platform       string `json:"platform"`
	}
	if err := json.Unmarshal(data, &actual); err != nil {
		return fmt.Errorf("decode Syft version: %w", err)
	}
	if actual.Application != syftName || actual.Version != strings.TrimPrefix(version, "v") ||
		actual.GitCommit != toolprofile.SyftSourceCommit || actual.GitDescription != version ||
		actual.GoVersion != runtime.Version() || actual.Compiler != runtime.Compiler ||
		actual.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return fmt.Errorf("syft version/source/compiler metadata differs from the frozen profile: %w", errInvalidConfig)
	}
	return installation.verify()
}

func prepareSyftInstallation(
	directory string, mkdir func(*os.Root, string, os.FileMode) error,
) (*SyftInstallation, error) {
	return prepareSourceInstallation(directory, syftName, mkdir)
}

func temporarySyftDirectory(parent string) string {
	return filepath.Join(parent, ".gobfd-syft-"+rand.Text())
}
