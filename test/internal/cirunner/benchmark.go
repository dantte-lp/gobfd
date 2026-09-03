package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	benchmarkArtifactMode = 0o644
	benchmarkCleanupLimit = 30 * time.Second
	benchmarkInputLimit   = 64 << 20
	benchmarkRefLimit     = 1024
	benchmarkRegexLimit   = 16 << 10
)

// BenchmarkRunOptions configures one head benchmark run.
type BenchmarkRunOptions struct {
	Root   string
	Output string
	Regex  string
	Runner SpecRunner
}

// BenchmarkRun runs the fixed CI benchmark set and writes a fresh raw result.
func BenchmarkRun(ctx context.Context, options BenchmarkRunOptions) error {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	if err := validateBenchmarkRegex(options.Regex); err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("benchmark command runner is required: %w", errInvalidConfig)
	}
	output, err := validateRootFile(root, options.Output, "benchmark output", true)
	if err != nil {
		return err
	}
	return runBenchmarkCommand(ctx, root, output, options.Regex, options.Runner)
}

// BenchmarkBaseOptions configures an isolated benchmark run at a base ref.
type BenchmarkBaseOptions struct {
	Root       string
	RunnerTemp string
	Ref        string
	Output     string
	Regex      string
	Runner     SpecRunner
}

// BenchmarkBase benchmarks a base ref in a temporary worktree and always removes it.
func BenchmarkBase(ctx context.Context, options BenchmarkBaseOptions) error {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	runnerTemp, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	if err := validateBenchmarkRef(options.Ref); err != nil {
		return err
	}
	if err := validateBenchmarkRegex(options.Regex); err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("benchmark command runner is required: %w", errInvalidConfig)
	}
	output, err := validateRootFile(root, options.Output, "base benchmark output", true)
	if err != nil {
		return err
	}
	if err := resetArtifact(output, "base benchmark output"); err != nil {
		return err
	}

	worktree := filepath.Join(runnerTemp, "gobfd-benchmark-base")
	if _, statErr := os.Lstat(worktree); !errors.Is(statErr, os.ErrNotExist) {
		if statErr != nil {
			return fmt.Errorf("inspect benchmark worktree %s: %w", worktree, statErr)
		}
		return fmt.Errorf("benchmark worktree already exists at %s: %w", worktree, errInvalidConfig)
	}
	gitPrefix := []string{"-c", "safe.directory=" + root}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "git", Dir: root,
		Args: append(append([]string(nil), gitPrefix...), "check-ref-format", "--branch", options.Ref),
	}); err != nil {
		return fmt.Errorf("validate benchmark base ref: %w", err)
	}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "git", Dir: root,
		Args: append(append([]string(nil), gitPrefix...), "worktree", "add", "--detach", worktree, options.Ref),
	}); err != nil {
		return fmt.Errorf("create benchmark base worktree: %w", err)
	}

	runErr := runBenchmarkCommand(ctx, worktree, output, options.Regex, options.Runner)
	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), benchmarkCleanupLimit)
	defer cancelCleanup()
	cleanupErr := options.Runner.RunCommand(cleanupCtx, CommandSpec{
		Name: "git", Dir: root,
		Args: append(append([]string(nil), gitPrefix...), "worktree", "remove", worktree),
	})
	if runErr != nil || cleanupErr != nil {
		return errors.Join(
			wrapOptional("run base benchmarks", runErr),
			wrapOptional("remove benchmark base worktree", cleanupErr),
		)
	}
	return nil
}

func runBenchmarkCommand(ctx context.Context, workDir, output, regex string, runner SpecRunner) error {
	artifact, err := openFreshArtifact(output, "benchmark output")
	if err != nil {
		return err
	}
	spec := CommandSpec{
		Name: "go",
		Args: []string{
			"test", "-buildvcs=false", "-bench=" + regex, "-benchmem", "-count=6",
			"-run=^$", "-timeout=120s", "./internal/bfd/", "./internal/netio/",
		},
		Dir:    workDir,
		Stdout: artifact,
	}
	runErr := runner.RunCommand(ctx, spec)
	closeErr := artifact.Close()
	if runErr != nil || closeErr != nil {
		return errors.Join(
			wrapOptional("execute benchmark command", runErr),
			wrapOptional("close benchmark output", closeErr),
		)
	}
	if err := validateNonemptyRegularFile(output, "benchmark output"); err != nil {
		return err
	}
	return nil
}

func validateBenchmarkRegex(value string) error {
	if value == "" || len(value) > benchmarkRegexLimit || hasControl(value) {
		return fmt.Errorf("BENCH_REGEX must be non-empty and contain no control characters: %w", errInvalidConfig)
	}
	return nil
}

func validateBenchmarkRef(value string) error {
	if !strings.HasPrefix(value, "origin/") ||
		len(value) == len("origin/") ||
		len(value) > benchmarkRefLimit ||
		hasControl(value) {
		return fmt.Errorf("benchmark base ref must be an origin branch without control characters: %w", errInvalidConfig)
	}
	if strings.Contains(value, "..") || strings.Contains(value, "//") || strings.Contains(value, "@{") ||
		strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") {
		return fmt.Errorf("benchmark base ref %q is unsafe: %w", value, errInvalidConfig)
	}
	return nil
}

func hasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func wrapOptional(context string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

// NormalizeBenchmarks rewrites exact historical aliases and validates the stable comparison set.
func NormalizeBenchmarks(root string, names []string) error {
	validatedRoot, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("benchmark input files are required: %w", errInvalidConfig)
	}
	type normalizedFile struct {
		path string
		data []byte
	}
	files := make([]normalizedFile, 0, len(names))
	for _, name := range names {
		path, pathErr := validateRootFile(validatedRoot, name, "benchmark input", false)
		if pathErr != nil {
			return pathErr
		}
		data, readErr := readRegularFile(path, "benchmark input", benchmarkInputLimit)
		if readErr != nil {
			return readErr
		}
		normalized := normalizeBenchmarkData(string(data))
		for _, mandatory := range []string{"RecvDecodeLookupEnqueue", "RecvDecodeFSM", "TxMarshalJitter"} {
			if !containsTopLevelBenchmark(normalized, mandatory) {
				return fmt.Errorf("mandatory benchmark %s is absent from %s: %w", mandatory, name, errInvalidConfig)
			}
		}
		files = append(files, normalizedFile{path: path, data: []byte(normalized)})
	}
	for _, file := range files {
		if err := writeAtomicArtifact(file.path, file.data, "normalized benchmark input"); err != nil {
			return err
		}
	}
	return nil
}

func normalizeBenchmarkData(data string) string {
	aliases := []struct {
		old string
		new string
	}{
		{old: "BenchmarkFullRecvPathCodec", new: "BenchmarkRecvDecodeFSM"},
		{old: "BenchmarkFullRecvPath", new: "BenchmarkRecvDecodeLookupEnqueue"},
		{old: "BenchmarkFullTxPath", new: "BenchmarkTxMarshalJitter"},
	}
	lines := strings.SplitAfter(data, "\n")
	for index, line := range lines {
		for _, alias := range aliases {
			if !strings.HasPrefix(line, alias.old) || len(line) == len(alias.old) {
				continue
			}
			delimiter := line[len(alias.old)]
			if delimiter == '-' || delimiter == '/' {
				lines[index] = alias.new + line[len(alias.old):]
			}
			break
		}
	}
	return strings.Join(lines, "")
}

func containsTopLevelBenchmark(data, name string) bool {
	want := "Benchmark" + name
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		token := fields[0]
		if token == want {
			return true
		}
		if !strings.HasPrefix(token, want+"-") {
			continue
		}
		suffix := strings.TrimPrefix(token, want+"-")
		if suffix != "" && strings.IndexFunc(suffix, func(character rune) bool {
			return character < '0' || character > '9'
		}) < 0 {
			return true
		}
	}
	return false
}

func validateRootFile(root, name, purpose string, createParent bool) (string, error) {
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." || hasControl(name) {
		return "", fmt.Errorf("%s path %q must be a clean repository-relative file: %w", purpose, name, errInvalidConfig)
	}
	path := filepath.Join(root, name)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes the repository: %w", purpose, name, errInvalidConfig)
	}
	parent := filepath.Dir(path)
	if createParent && parent != root {
		if err := ensureDirectory(parent, purpose+" parent", reportDirectoryMode); err != nil {
			return "", err
		}
	} else if err := inspectDirectoryTree(parent, purpose+" parent"); err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s %s has mode %s: %w", purpose, path, info.Mode(), errInvalidConfig)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect %s %s: %w", purpose, path, statErr)
	}
	return path, nil
}

func openFreshArtifact(path, purpose string) (*os.File, error) {
	if err := resetArtifact(path, purpose); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, benchmarkArtifactMode)
	if err != nil {
		return nil, fmt.Errorf("open %s %s: %w", purpose, path, err)
	}
	return file, nil
}

func resetArtifact(path, purpose string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s %s has mode %s: %w", purpose, path, info.Mode(), errInvalidConfig)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s %s: %w", purpose, path, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, benchmarkArtifactMode)
	if err != nil {
		return fmt.Errorf("prepare %s %s: %w", purpose, path, err)
	}
	if err := errors.Join(file.Chmod(benchmarkArtifactMode), file.Close()); err != nil {
		return fmt.Errorf("secure %s %s: %w", purpose, path, err)
	}
	return nil
}

func validateNonemptyRegularFile(path, purpose string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %s: %w", purpose, path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s %s is not a non-empty regular file: %w", purpose, path, errInvalidConfig)
	}
	if err := os.Chmod(path, benchmarkArtifactMode); err != nil {
		return fmt.Errorf("set %s %s mode: %w", purpose, path, err)
	}
	return nil
}

func readRegularFile(path, purpose string, limit int64) ([]byte, error) {
	if err := validateNonemptyRegularFile(path, purpose); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s %s: %w", purpose, path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(wrapOptional("read "+purpose, readErr), wrapOptional("close "+purpose, closeErr))
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s %s exceeds %d bytes: %w", purpose, path, limit, errInvalidConfig)
	}
	return data, nil
}

func writeAtomicArtifact(path string, data []byte, purpose string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s target %s has mode %s: %w", purpose, path, info.Mode(), errInvalidConfig)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s target %s: %w", purpose, path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gobfd-benchmark-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", purpose, err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(benchmarkArtifactMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary %s mode: %w", purpose, err)
	}
	written, writeErr := temporary.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(
			wrapOptional("write temporary "+purpose, writeErr),
			wrapOptional("close temporary "+purpose, closeErr),
		)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish %s %s: %w", purpose, path, err)
	}
	if err := os.Chmod(path, benchmarkArtifactMode); err != nil {
		return fmt.Errorf("set %s %s mode: %w", purpose, path, err)
	}
	return nil
}
