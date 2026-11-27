package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	localBenchmarkCount = 6
	localReportDirMode  = 0o750
	localReportFileMode = 0o600
)

// DevEnsureOptions configures the local Podman Compose dev service preflight.
type DevEnsureOptions struct {
	Root        string
	Project     string
	ComposeFile string
	Environment []string
	Stdout      io.Writer
	Runner      SpecRunner
}

// DevEnsure ensures the Podman Compose dev container exists and mounts Root at /app.
func DevEnsure(ctx context.Context, options DevEnsureOptions) error {
	root, err := validateAbsoluteExistingDirectory(options.Root, "dev-ensure repository root")
	if err != nil {
		return err
	}
	if options.Project == "" || hasControl(options.Project) {
		return fmt.Errorf("dev-ensure project name is required: %w", errInvalidConfig)
	}
	if options.ComposeFile == "" || hasControl(options.ComposeFile) {
		return fmt.Errorf("dev-ensure compose file is required: %w", errInvalidConfig)
	}
	if options.Runner == nil {
		return fmt.Errorf("dev-ensure command runner is required: %w", errInvalidConfig)
	}
	env := append(
		withoutEnvironmentKeys(options.Environment, "COMPOSE_PROJECT_NAME"),
		"COMPOSE_PROJECT_NAME="+options.Project,
	)
	stdout := options.Stdout
	if stdout == nil {
		stdout = io.Discard
	}

	cid, err := queryDevContainerID(ctx, root, options.Project, options.ComposeFile, env, options.Runner)
	if err != nil {
		return err
	}
	if cid == "" {
		return createMissingDevContainer(ctx, root, options.Project, options.ComposeFile, env, stdout, options.Runner)
	}
	mountSrc, err := inspectDevAppMount(ctx, root, cid, env, options.Runner)
	if err != nil {
		return err
	}
	if mountSrc != root {
		if _, writeErr := fmt.Fprintf(
			stdout, "dev-ensure: mount source %s != %s, force recreating\n", mountSrc, root,
		); writeErr != nil {
			return fmt.Errorf("write dev-ensure recreate notice: %w", writeErr)
		}
		return runComposeDevUp(ctx, root, options.Project, options.ComposeFile, env, options.Runner,
			"--no-build", "--force-recreate")
	}
	return runComposeDevUp(ctx, root, options.Project, options.ComposeFile, env, options.Runner, "--no-build")
}

func queryDevContainerID(
	ctx context.Context,
	root, project, composeFile string,
	env []string,
	runner SpecRunner,
) (string, error) {
	var out bytes.Buffer
	if err := runner.RunCommand(ctx, CommandSpec{
		Name:   podmanName,
		Args:   []string{"compose", "-p", project, "-f", composeFile, "ps", "--all", "--quiet", "dev"},
		Dir:    root,
		Env:    env,
		Stdout: &out,
	}); err != nil {
		return "", fmt.Errorf("query dev container ID: %w", err)
	}
	fields := strings.Fields(out.String())
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

func createMissingDevContainer(
	ctx context.Context,
	root, project, composeFile string,
	env []string,
	stdout io.Writer,
	runner SpecRunner,
) error {
	if _, err := fmt.Fprintln(stdout, "dev-ensure: dev service container missing, creating"); err != nil {
		return fmt.Errorf("write dev-ensure create notice: %w", err)
	}
	existsErr := runner.RunCommand(ctx, CommandSpec{
		Name:   podmanName,
		Args:   []string{"image", "exists", project + "-dev:latest"},
		Dir:    root,
		Env:    env,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if existsErr == nil {
		return runComposeDevUp(ctx, root, project, composeFile, env, runner, "--no-build")
	}
	return runComposeDevUp(ctx, root, project, composeFile, env, runner, "--build")
}

func inspectDevAppMount(
	ctx context.Context,
	root, containerID string,
	env []string,
	runner SpecRunner,
) (string, error) {
	var out bytes.Buffer
	const mountTemplate = `{{range .Mounts}}{{if eq .Destination "/app"}}{{.Source}}{{end}}{{end}}`
	if err := runner.RunCommand(ctx, CommandSpec{
		Name:   podmanName,
		Args:   []string{"inspect", containerID, "--format", mountTemplate},
		Dir:    root,
		Env:    env,
		Stdout: &out,
	}); err != nil {
		return "", fmt.Errorf("inspect dev container mount: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}

func runComposeDevUp(
	ctx context.Context,
	root, project, composeFile string,
	env []string,
	runner SpecRunner,
	flags ...string,
) error {
	args := make([]string, 0, 7+len(flags))
	args = append(args, "compose", "-p", project, "-f", composeFile, "up", "-d")
	args = append(args, flags...)
	args = append(args, "dev")
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: podmanName, Args: args, Dir: root, Env: env,
	}); err != nil {
		return fmt.Errorf("start dev container: %w", err)
	}
	return nil
}

// LocalBenchmarkOptions configures a local benchmark command teed to a file.
type LocalBenchmarkOptions struct {
	Dir         string
	Output      string
	Package     string
	Timeout     string
	ExecPrefix  []string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
	Runner      PrefixRunner
}

// LocalBenchmark runs go test benchmarks and tees stdout to Output while preserving failures.
func LocalBenchmark(ctx context.Context, options LocalBenchmarkOptions) error {
	if options.Output == "" || hasControl(options.Output) {
		return fmt.Errorf("local benchmark output path is required: %w", errInvalidConfig)
	}
	if options.Package == "" || hasControl(options.Package) {
		return fmt.Errorf("local benchmark package is required: %w", errInvalidConfig)
	}
	if options.Runner == nil {
		return fmt.Errorf("local benchmark command runner is required: %w", errInvalidConfig)
	}
	args := []string{
		"go", "test", buildVCSDisabledFlag, "-bench=.", benchMemFlag, "-count=6", noUnitTestsFlag,
	}
	if options.Timeout != "" {
		args = append(args, "-timeout="+options.Timeout)
	}
	args = append(args, options.Package)
	return runTeedPrefixCommand(
		ctx, options.Dir, options.Output, options.Environment, options.ExecPrefix, args,
		options.Stdout, options.Stderr, options.Runner,
	)
}

// LocalBenchmarkSaveOptions configures local versioned benchmark saving.
type LocalBenchmarkSaveOptions struct {
	Dir         string
	BenchDir    string
	Version     string
	Commit      string
	BuildDate   string
	ExecPrefix  []string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
	Runner      PrefixRunner
}

// LocalBenchmarkSave runs BFD and netio benchmarks, concatenates them, and writes meta.json.
func LocalBenchmarkSave(ctx context.Context, options LocalBenchmarkSaveOptions) error {
	if options.BenchDir == "" || options.Version == "" {
		return fmt.Errorf("benchmark directory and version are required: %w", errInvalidConfig)
	}
	if options.Runner == nil {
		return fmt.Errorf("benchmark save runner is required: %w", errInvalidConfig)
	}
	targetDir := filepath.Join(options.Dir, options.BenchDir, options.Version)
	if err := os.MkdirAll(targetDir, localReportDirMode); err != nil {
		return fmt.Errorf("create benchmark save directory %s: %w", targetDir, err)
	}
	bfdPath := filepath.Join(targetDir, "benchmark-bfd.txt")
	if err := LocalBenchmark(ctx, LocalBenchmarkOptions{
		Dir: options.Dir, Output: bfdPath, Package: bfdBenchmarkPackage, Timeout: "120s",
		ExecPrefix: options.ExecPrefix, Environment: options.Environment,
		Stdout: options.Stdout, Stderr: options.Stderr, Runner: options.Runner,
	}); err != nil {
		return err
	}
	netioPath := filepath.Join(targetDir, "benchmark-netio.txt")
	if err := LocalBenchmark(ctx, LocalBenchmarkOptions{
		Dir: options.Dir, Output: netioPath, Package: "./internal/netio/", Timeout: "120s",
		ExecPrefix: options.ExecPrefix, Environment: options.Environment,
		Stdout: options.Stdout, Stderr: options.Stderr, Runner: options.Runner,
	}); err != nil {
		return err
	}
	if err := concatenateBenchmarkFiles(bfdPath, netioPath, filepath.Join(targetDir, "benchmark.txt")); err != nil {
		return err
	}
	goVersion, err := queryLocalGoVersion(
		ctx, options.Dir, options.Environment, options.ExecPrefix, options.Stderr, options.Runner,
	)
	if err != nil {
		return err
	}
	return writeLocalBenchmarkMeta(targetDir, options, goVersion)
}

func concatenateBenchmarkFiles(firstPath, secondPath, destination string) error {
	// #nosec G304 -- Paths are constructed beneath the caller-selected benchmark save directory.
	firstData, err := os.ReadFile(firstPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", firstPath, err)
	}
	// #nosec G304 -- Paths are constructed beneath the caller-selected benchmark save directory.
	secondData, err := os.ReadFile(secondPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", secondPath, err)
	}
	combined := make([]byte, 0, len(firstData)+len(secondData))
	combined = append(combined, firstData...)
	combined = append(combined, secondData...)
	if writeErr := writeCheckedOutputFile(destination, combined); writeErr != nil {
		return fmt.Errorf("write combined benchmark output: %w", writeErr)
	}
	return nil
}

func queryLocalGoVersion(
	ctx context.Context,
	dir string,
	baseEnv, execPrefix []string,
	stderr io.Writer,
	runner PrefixRunner,
) (string, error) {
	name, args, env, err := parsePrefixedCommand(baseEnv, execPrefix, []string{"go", "env", "GOVERSION"})
	if err != nil {
		return "", err
	}
	var stdout bytes.Buffer
	if err := runner.RunPrefix(ctx, CommandSpec{
		Name: name, Args: args, Dir: dir, Env: env, Stdout: &stdout, Stderr: stderr,
	}); err != nil {
		return "", fmt.Errorf("query Go version: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

type savedBenchmarkMetadata struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	Count   int    `json:"count"`
}

func writeLocalBenchmarkMeta(targetDir string, options LocalBenchmarkSaveOptions, goVersion string) error {
	payload, err := json.Marshal(savedBenchmarkMetadata{
		Version: options.Version,
		Commit:  options.Commit,
		Date:    options.BuildDate,
		Go:      goVersion,
		Count:   localBenchmarkCount,
	})
	if err != nil {
		return fmt.Errorf("encode benchmark metadata: %w", err)
	}
	metaPath := filepath.Join(targetDir, "meta.json")
	if writeErr := writeCheckedOutputFile(metaPath, append(payload, '\n')); writeErr != nil {
		return fmt.Errorf("write benchmark metadata: %w", writeErr)
	}
	if options.Stdout != nil {
		_, _ = fmt.Fprintf(options.Stdout, "=== Benchmarks saved to %s/%s/ ===\n", options.BenchDir, options.Version)
	}
	return nil
}

// LocalBenchmarkCompareOptions configures local benchstat comparison rendering.
type LocalBenchmarkCompareOptions struct {
	Dir         string
	BenchDir    string
	ReportDir   string
	Old         string
	New         string
	BuildDate   string
	ExecPrefix  []string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
	Runner      PrefixRunner
}

// LocalBenchmarkCompare runs benchstat and renders the standalone comparison HTML.
func LocalBenchmarkCompare(ctx context.Context, options LocalBenchmarkCompareOptions) error {
	if options.BenchDir == "" || options.ReportDir == "" || options.Old == "" || options.New == "" {
		return fmt.Errorf("benchmark comparison paths and versions are required: %w", errInvalidConfig)
	}
	if options.Runner == nil {
		return fmt.Errorf("benchmark comparison runner is required: %w", errInvalidConfig)
	}
	benchmarksDir := filepath.Join(options.Dir, options.ReportDir, "benchmarks")
	if err := os.MkdirAll(benchmarksDir, localReportDirMode); err != nil {
		return fmt.Errorf("create benchmark comparison directory %s: %w", benchmarksDir, err)
	}
	oldRel := filepath.ToSlash(filepath.Join(options.BenchDir, options.Old, "benchmark.txt"))
	if options.Old == "baseline" {
		oldRel = filepath.ToSlash(filepath.Join(options.BenchDir, "baseline.txt"))
	}
	newRel := filepath.ToSlash(filepath.Join(options.BenchDir, options.New, "benchmark.txt"))
	comparisonPath := filepath.Join(benchmarksDir, "comparison.txt")
	if err := runTeedPrefixCommand(
		ctx, options.Dir, comparisonPath, options.Environment, options.ExecPrefix,
		[]string{"benchstat", oldRel, newRel},
		options.Stdout, options.Stderr, options.Runner,
	); err != nil {
		return err
	}
	// #nosec G304 -- comparisonPath is created directly above inside benchmarksDir.
	comparisonText, err := os.ReadFile(comparisonPath)
	if err != nil {
		return fmt.Errorf("read benchmark comparison text: %w", err)
	}
	htmlPath := filepath.Join(benchmarksDir, "comparison.html")
	document := renderLocalComparisonHTML(options.Old, options.New, options.BuildDate, string(comparisonText))
	if err := writeCheckedOutputFile(htmlPath, []byte(document)); err != nil {
		return fmt.Errorf("write benchmark comparison HTML: %w", err)
	}
	if options.Stdout != nil {
		_, _ = fmt.Fprintf(options.Stdout, "=== Comparison: %s/benchmarks/comparison.html ===\n", options.ReportDir)
	}
	return nil
}

func renderLocalComparisonHTML(oldVer, newVer, buildDate, body string) string {
	safeOld := html.EscapeString(oldVer)
	safeNew := html.EscapeString(newVer)
	safeDate := html.EscapeString(buildDate)
	safeBody := html.EscapeString(body)
	return fmt.Sprintf(
		"<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\">\n"+
			"<title>GoBFD Benchmark Comparison: %s vs %s</title>\n"+
			"<style>\nbody{font-family:system-ui,sans-serif;margin:2em;background:#1a1a2e;color:#e0e0e0}\n"+
			"h1{color:#00d4ff}pre{background:#16213e;padding:1.5em;border-radius:8px;overflow-x:auto;"+
			"font-size:14px;line-height:1.6;border:1px solid #0f3460}\n"+
			".meta{color:#888;font-size:13px}\n</style></head><body>\n"+
			"<h1>GoBFD Benchmark Comparison</h1>\n"+
			"<p class=\"meta\">Old: %s &mdash; New: %s &mdash; Generated: %s</p>\n<pre>\n%s</pre></body></html>\n",
		safeOld, safeNew, safeOld, safeNew, safeDate, safeBody,
	)
}

func runTeedPrefixCommand(
	ctx context.Context,
	dir, outputPath string,
	baseEnv, execPrefix, commandArgs []string,
	stdout, stderr io.Writer,
	runner PrefixRunner,
) error {
	name, args, env, err := parsePrefixedCommand(baseEnv, execPrefix, commandArgs)
	if err != nil {
		return err
	}
	targetPath := outputPath
	if !filepath.IsAbs(targetPath) && dir != "" {
		targetPath = filepath.Join(dir, targetPath)
	}
	if validateErr := validateRegularOrMissingFile(targetPath, "local benchmark output"); validateErr != nil {
		return validateErr
	}
	// #nosec G304 -- validateRegularOrMissingFile rejects symlinks, directories, and special files before open.
	file, openErr := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, localReportFileMode)
	if openErr != nil {
		return fmt.Errorf("open benchmark output %s: %w", targetPath, openErr)
	}
	var writer io.Writer = file
	if stdout != nil {
		writer = io.MultiWriter(stdout, file)
	}
	runErr := runner.RunPrefix(ctx, CommandSpec{
		Name: name, Args: args, Dir: dir, Env: env, Stdout: writer, Stderr: stderr,
	})
	closeErr := file.Close()
	return errors.Join(runErr, wrapOptional("close benchmark output "+targetPath, closeErr))
}

func parsePrefixedCommand(
	baseEnv, execPrefix, commandArgs []string,
) (string, []string, []string, error) {
	if len(commandArgs) == 0 {
		return "", nil, nil, fmt.Errorf("prefix command arguments are required: %w", errInvalidConfig)
	}
	prefixIndex := 0
	var keys []string
	for prefixIndex < len(execPrefix) {
		key, ok := leadingEnvAssignmentKey(execPrefix[prefixIndex])
		if !ok {
			break
		}
		keys = append(keys, key)
		prefixIndex++
	}
	if len(execPrefix) > 0 && prefixIndex == len(execPrefix) {
		return "", nil, nil, fmt.Errorf(
			"exec prefix %q lacks an executable command after environment assignments: %w",
			strings.Join(execPrefix, " "), errInvalidConfig,
		)
	}
	env := baseEnv
	if prefixIndex > 0 {
		if env == nil {
			env = os.Environ()
		}
		env = append(withoutEnvironmentKeys(env, keys...), execPrefix[:prefixIndex]...)
	}
	name, args := buildPrefixedArgs(execPrefix[prefixIndex:], commandArgs)
	return name, args, env, nil
}

func leadingEnvAssignmentKey(token string) (string, bool) {
	if hasControl(token) {
		return "", false
	}
	key, _, found := strings.Cut(token, "=")
	if !found || !isValidEnvKey(key) {
		return "", false
	}
	return key, true
}

func isValidEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for index := range len(key) {
		ch := key[index]
		alpha := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
		digit := ch >= '0' && ch <= '9'
		if !alpha && (index == 0 || !digit) {
			return false
		}
	}
	return true
}

func buildPrefixedArgs(execPrefix, commandArgs []string) (string, []string) {
	if len(execPrefix) == 0 {
		return commandArgs[0], commandArgs[1:]
	}
	args := make([]string, 0, len(execPrefix)-1+len(commandArgs))
	args = append(args, execPrefix[1:]...)
	args = append(args, commandArgs...)
	return execPrefix[0], args
}

func writeCheckedOutputFile(path string, data []byte) (retErr error) {
	if err := validateRegularOrMissingFile(path, "benchmark artifact"); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open output directory for %s: %w", path, err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close output directory for %s: %w", path, closeErr))
		}
	}()
	file, openErr := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, localReportFileMode)
	if openErr != nil {
		return fmt.Errorf("open %s: %w", path, openErr)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("write %s: %w", path, errors.Join(writeErr, closeErr))
	}
	return nil
}
