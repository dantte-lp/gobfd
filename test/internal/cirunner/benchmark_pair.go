package cirunner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/benchreport"
)

const benchmarkPairCount = 10

// BenchmarkPair collects ten base/head samples with alternating first-run order.
func BenchmarkPair(ctx context.Context, options BenchmarkBaseOptions, headOutput string) error {
	if headOutput == "" {
		return fmt.Errorf("paired head benchmark output is required: %w", errInvalidConfig)
	}
	return benchmarkBase(ctx, options, headOutput)
}

func runBenchmarkCollection(
	ctx context.Context, options BenchmarkBaseOptions, worktree, basePath, headPath string,
) error {
	if headPath == "" {
		return runBenchmarkCommand(ctx, worktree, basePath, options.Regex, options.Runner)
	}
	return runBenchmarkPair(ctx, options, worktree, basePath, headPath)
}

func prepareBenchmarkCollectionOutputs(root, baseName, headName string) (string, string, error) {
	basePath, err := validateRootFile(root, baseName, "base benchmark output", true)
	if err != nil {
		return "", "", err
	}
	var headPath string
	if headName != "" {
		headPath, err = validateBenchmarkHeadOutput(root, basePath, headName)
		if err != nil {
			return "", "", err
		}
	}
	for _, target := range []struct{ path, purpose string }{
		{basePath, "base benchmark output"}, {headPath, "head benchmark output"},
	} {
		if target.path != "" {
			if err := resetArtifact(target.path, target.purpose); err != nil {
				return "", "", err
			}
		}
	}
	return basePath, headPath, nil
}

func validateBenchmarkHeadOutput(root, basePath, headName string) (string, error) {
	headPath, err := validateRootFile(root, headName, "head benchmark output", true)
	if err != nil {
		return "", err
	}
	if headPath == basePath {
		return "", fmt.Errorf("paired benchmark outputs must differ: %w", errInvalidConfig)
	}
	baseInfo, baseErr := os.Lstat(basePath)
	headInfo, headErr := os.Lstat(headPath)
	if baseErr != nil && !errors.Is(baseErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect base benchmark output: %w", baseErr)
	}
	if headErr != nil && !errors.Is(headErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect head benchmark output: %w", headErr)
	}
	if baseErr == nil && headErr == nil && os.SameFile(baseInfo, headInfo) {
		return "", fmt.Errorf("paired benchmark outputs share a file: %w", errInvalidConfig)
	}
	return headPath, nil
}

func runBenchmarkPair(
	ctx context.Context, options BenchmarkBaseOptions, worktree, basePath, headPath string,
) error {
	base, err := openFreshArtifact(basePath, "base benchmark output")
	if err != nil {
		return err
	}
	head, err := openFreshArtifact(headPath, "head benchmark output")
	if err != nil {
		return errors.Join(err, wrapOptional("close base benchmark output", base.Close()))
	}
	runErr := runInterleavedSamples(ctx, options, worktree, base, head)
	if err := errors.Join(runErr,
		wrapOptional("close base benchmark output", base.Close()),
		wrapOptional("close head benchmark output", head.Close()),
	); err != nil {
		return err
	}
	for _, path := range []string{basePath, headPath} {
		if err := validateBenchmarkPairSamples(path); err != nil {
			return err
		}
	}
	return nil
}

func runInterleavedSamples(
	ctx context.Context, options BenchmarkBaseOptions, worktree string, base, head *os.File,
) error {
	targets := [...]struct {
		dir    string
		output *os.File
	}{{worktree, base}, {options.Root, head}}
	for pair := range benchmarkPairCount {
		for side := range targets {
			target := targets[(pair+side)%len(targets)]
			before, err := target.output.Stat()
			if err != nil {
				return fmt.Errorf("inspect benchmark output before sample: %w", err)
			}
			if runErr := options.Runner.RunCommand(ctx, CommandSpec{
				Name: "go", Dir: target.dir,
				WaitDelay: benchmarkCleanupLimit,
				// Keep stdout behind io.Writer so os/exec must copy through the budget check.
				Stdout: benchmarkOutputWriter{file: target.output},
				Args: []string{
					"test", buildVCSDisabledFlag, "-bench=" + options.Regex, "-benchmem", "-count=1",
					"-benchtime=100ms", "-run=^$", "-timeout=120s", "./internal/bfd/", "./internal/netio/",
				},
			}); runErr != nil {
				return fmt.Errorf("execute benchmark sample in %s: %w", target.dir, runErr)
			}
			after, err := target.output.Stat()
			if err != nil {
				return fmt.Errorf("inspect benchmark output after sample: %w", err)
			}
			if after.Size() <= before.Size() || after.Size() > benchmarkInputLimit {
				return fmt.Errorf("benchmark sample is empty or output exceeds its limit: %w", errInvalidConfig)
			}
		}
	}
	return nil
}

type benchmarkOutputWriter struct {
	file *os.File
}

func (w benchmarkOutputWriter) Write(data []byte) (int, error) {
	info, err := w.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("inspect benchmark stdout budget: %w", err)
	}
	if info.Size() > benchmarkInputLimit || int64(len(data)) > benchmarkInputLimit-info.Size() {
		return 0, fmt.Errorf("benchmark stdout exceeds %d bytes: %w", benchmarkInputLimit, errInvalidConfig)
	}
	written, writeErr := w.file.Write(data)
	if writeErr != nil {
		return written, fmt.Errorf("write benchmark stdout: %w", writeErr)
	}
	return written, nil
}

func validateBenchmarkPairSamples(path string) error {
	data, err := readRegularFile(path, "paired benchmark output")
	if err != nil {
		return err
	}
	counts := make(map[string]int)
	var pkg string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "goos:") {
			pkg = ""
		}
		if value, found := strings.CutPrefix(line, "pkg:"); found {
			pkg = strings.TrimSpace(value)
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		if err := benchreport.ValidateGoRecord(line); err != nil {
			return fmt.Errorf("validate paired benchmark output %s: %w", path, err)
		}
		if pkg == "" {
			return fmt.Errorf("paired benchmark output has no package: %w", errInvalidConfig)
		}
		counts[pkg+" "+strings.Fields(line)[0]]++
	}
	if len(counts) == 0 {
		return fmt.Errorf("paired benchmark output has no records: %w", errInvalidConfig)
	}
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		count := counts[name]
		if count != benchmarkPairCount {
			return fmt.Errorf("benchmark %s has %d samples, want %d: %w", name, count, benchmarkPairCount, errInvalidConfig)
		}
	}
	return nil
}
