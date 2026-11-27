package cirunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type releaseBenchmarkRunner struct {
	runner      SpecRunner
	environment []string
}

func (r releaseBenchmarkRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	spec.Env = r.environment
	if err := r.runner.RunCommand(ctx, spec); err != nil {
		return fmt.Errorf("run release benchmark command %s: %w", spec.Name, err)
	}
	return nil
}

type releaseBenchmarkProvenance struct {
	BaselineTag       string                   `json:"baseline_tag"`
	BaselineTagObject string                   `json:"baseline_tag_object"`
	BaselineCommit    string                   `json:"baseline_commit"`
	CurrentCommit     string                   `json:"current_commit"`
	Compiler          string                   `json:"compiler"`
	Toolchain         string                   `json:"toolchain"`
	Samples           int                      `json:"samples"`
	SampleDuration    string                   `json:"sample_duration"`
	SampleTimeout     string                   `json:"sample_timeout"`
	CleanupTimeout    string                   `json:"cleanup_timeout"`
	RawSHA256         map[string]string        `json:"raw_sha256"`
	InputSHA256       map[string]string        `json:"input_sha256"`
	Fixture           receiveFixtureProvenance `json:"fixture"`
}

func collectReleaseBenchmarkPair(ctx context.Context, options ReleaseComparisonOptions) (string, SpecRunner, error) {
	if options.Runner == nil {
		return "", nil, fmt.Errorf("release benchmark command runner is required: %w", errInvalidConfig)
	}
	environment := append(withoutEnvironmentKeys(options.Environment, "GH_TOKEN", "GITHUB_TOKEN", "GOTOOLCHAIN"),
		"GOTOOLCHAIN=local")
	runner := releaseBenchmarkRunner{runner: options.Runner, environment: environment}
	if err := prepareReleaseBenchmarkPair(options.Root); err != nil {
		return "", nil, err
	}
	source, err := freezeReleaseBenchmarkSource(ctx, options, runner)
	if err != nil {
		return "", nil, err
	}
	if err := collectBenchmarkBase(ctx, BenchmarkBaseOptions{
		Root: options.Root, RunnerTemp: options.RunnerTemp, Ref: source.BaselineCommit, Regex: ".",
		Output: "reports/benchmarks/pair-baseline-raw.txt", Runner: runner,
	}, "reports/benchmarks/pair-current-raw.txt", source.BaselineCommit); err != nil {
		return "", nil, err
	}
	if err := verifyReleaseBenchmarkSource(ctx, options, runner, source); err != nil {
		return "", nil, err
	}
	if err := retainReleaseBenchmarkPair(options.Root, source); err != nil {
		return "", nil, err
	}
	return source.BaselineTag, runner, nil
}

func prepareReleaseBenchmarkPair(root string) error {
	for _, name := range []string{
		"pair-baseline-raw.txt", "pair-current-raw.txt", "pair-baseline.txt", "pair-current.txt",
		"pair-baseline-race.txt", "pair-current-race.txt",
		"comparison-provenance.json", "legacy-baseline.txt",
		"pair-fixture-provenance.json",
		"pair-baseline-fixture-original.go.txt", "pair-baseline-fixture-owned.go.txt",
		"pair-current-fixture-original.go.txt", "pair-current-fixture-owned.go.txt",
	} {
		path, err := validateRootFile(root, "reports/benchmarks/"+name, "paired release benchmark", false)
		if err != nil {
			return err
		}
		if err := resetArtifact(path, "paired release benchmark"); err != nil {
			return err
		}
	}
	return nil
}

// Unsafe receive fixtures cannot establish a performance baseline.
func checkReleaseBenchmarkRace(ctx context.Context, options BenchmarkBaseOptions, worktree string) error {
	base, head, err := prepareBenchmarkCollectionOutputs(options.Root,
		"reports/benchmarks/pair-baseline-race.txt", "reports/benchmarks/pair-current-race.txt")
	if err != nil {
		return err
	}
	for _, target := range []struct{ directory, output string }{{worktree, base}, {options.Root, head}} {
		artifact, err := openFreshArtifact(target.output, "receive benchmark race output")
		if err != nil {
			return err
		}
		writer := benchmarkOutputWriter{file: artifact}
		runErr := options.Runner.RunCommand(ctx, CommandSpec{
			Name: "go", Dir: target.directory, WaitDelay: benchmarkCleanupLimit,
			Stdout: writer, Stderr: writer,
			Args: []string{
				goTestSubcommand, buildVCSDisabledFlag, "-race", "-run=^$", singleTestRunFlag, "-benchmem",
				"-bench=^(BenchmarkManagerDemux1000Sessions|BenchmarkRecvDecodeLookupEnqueue|BenchmarkFullRecvPath)$",
				"-benchtime=100000x", "-timeout=120s", "./internal/bfd/",
			},
		})
		commandErr := errors.Join(runErr, wrapOptional("close receive benchmark race output", artifact.Close()))
		if commandErr != nil {
			return fmt.Errorf("receive benchmark race preflight failed; see %s: %w", target.output, commandErr)
		}
		if sampleErr := validateBenchmarkSamples(target.output, 1); sampleErr != nil {
			return sampleErr
		}
		data, err := readRegularFile(target.output, "receive benchmark race output")
		if err != nil {
			return err
		}
		for _, name := range []string{"ManagerDemux1000Sessions", "RecvDecodeLookupEnqueue"} {
			if !containsTopLevelBenchmark(normalizeBenchmarkData(string(data)), name) {
				return fmt.Errorf("receive race benchmark %s is absent from %s: %w", name, target.output, errInvalidConfig)
			}
		}
	}
	return nil
}

func freezeReleaseBenchmarkSource(
	ctx context.Context, options ReleaseComparisonOptions, runner SpecRunner,
) (releaseBenchmarkProvenance, error) {
	var source releaseBenchmarkProvenance
	owner, repository, err := parseGitHubRepository(options.Repository)
	if err != nil {
		return source, err
	}
	source.CurrentCommit, err = validateFullCommitSHA(options.SHA, "release benchmark current commit")
	if err != nil {
		return source, err
	}
	source.BaselineTag, err = previousPublishedRelease(ctx, options.Runner,
		options.Root, owner, repository, options.Version, options.Environment)
	if err != nil {
		return source, err
	}
	source.BaselineCommit, err = resolveReleaseBenchmarkCommit(ctx, runner,
		options.Root, "refs/tags/"+source.BaselineTag+"^{commit}")
	if err != nil {
		return source, err
	}
	source.BaselineTagObject, err = verifyAnnotatedReleaseTag(ctx, options.Runner, options.Root,
		owner, repository, source.BaselineTag, source.BaselineCommit, options.Environment)
	if err != nil {
		return source, err
	}
	if headErr := verifyReleaseBenchmarkHead(ctx, runner, options.Root, source.CurrentCommit); headErr != nil {
		return source, headErr
	}
	compiler, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "go", Args: []string{"env", "GOVERSION"}, Dir: options.Root,
	}, "read paired benchmark compiler")
	if err != nil {
		return source, err
	}
	source.Compiler = strings.TrimSuffix(string(compiler), "\n")
	if source.Compiler == "" || hasControl(source.Compiler) {
		return source, fmt.Errorf("paired benchmark compiler is invalid: %w", errInvalidConfig)
	}
	source.Toolchain, source.Samples = "local", benchmarkPairCount
	source.SampleDuration, source.SampleTimeout = "100ms", fmt.Sprintf("%ds", benchmarkSampleLimit/time.Second)
	source.CleanupTimeout = benchmarkCleanupLimit.String()
	return source, nil
}

func verifyReleaseBenchmarkSource(
	ctx context.Context, options ReleaseComparisonOptions, runner SpecRunner, source releaseBenchmarkProvenance,
) error {
	owner, repository, err := parseGitHubRepository(options.Repository)
	if err != nil {
		return err
	}
	selected, err := previousPublishedRelease(ctx, options.Runner,
		options.Root, owner, repository, options.Version, options.Environment)
	if err != nil {
		return err
	}
	tag, err := verifyAnnotatedReleaseTag(ctx, options.Runner, options.Root,
		owner, repository, source.BaselineTag, source.BaselineCommit, options.Environment)
	if err != nil {
		return err
	}
	if selected != source.BaselineTag || tag != source.BaselineTagObject {
		return fmt.Errorf("release benchmark stable identity changed during collection: %w", errInvalidConfig)
	}
	return verifyReleaseBenchmarkHead(ctx, runner, options.Root, source.CurrentCommit)
}

func verifyReleaseBenchmarkHead(ctx context.Context, runner SpecRunner, directory, expected string) error {
	actual, err := resolveReleaseBenchmarkCommit(ctx, runner, directory, "HEAD^{commit}")
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("release benchmark HEAD differs from frozen commit: %w", errInvalidConfig)
	}
	return nil
}

func retainReleaseBenchmarkPair(root string, source releaseBenchmarkProvenance) error {
	fixture, fixtureErr := verifyReceiveFixtureEvidence(root)
	if fixtureErr != nil {
		return fixtureErr
	}
	source.Fixture = fixture
	source.RawSHA256 = make(map[string]string)
	source.InputSHA256 = make(map[string]string)
	for _, side := range []string{"baseline", "current"} {
		rawName := "pair-" + side + "-raw.txt"
		data, err := readRegularFile(filepath.Join(root, "reports", "benchmarks", rawName), "raw release pair")
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		source.RawSHA256[rawName] = hex.EncodeToString(digest[:])
		raceName := "pair-" + side + "-race.txt"
		race, err := readRegularFile(filepath.Join(root, "reports", "benchmarks", raceName), "receive race evidence")
		if err != nil {
			return err
		}
		raceDigest := sha256.Sum256(race)
		source.RawSHA256[raceName] = hex.EncodeToString(raceDigest[:])
		if err := writeAtomicArtifact(filepath.Join(root, "reports", "benchmarks", "pair-"+side+".txt"),
			data, "paired benchmark copy"); err != nil {
			return err
		}
	}
	if err := retainHistoricalBenchmarkBaseline(root); err != nil {
		return err
	}
	names := []string{"reports/benchmarks/pair-baseline.txt", "reports/benchmarks/pair-current.txt"}
	if err := NormalizeBenchmarks(root, names); err != nil {
		return err
	}
	for _, name := range names {
		data, err := readRegularFile(filepath.Join(root, name), "normalized release pair")
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		source.InputSHA256[filepath.Base(name)] = hex.EncodeToString(digest[:])
	}
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return fmt.Errorf("encode release benchmark provenance: %w", err)
	}
	return writeAtomicArtifact(filepath.Join(root, "reports", "benchmarks", "comparison-provenance.json"),
		append(data, '\n'), "release benchmark provenance")
}

func retainHistoricalBenchmarkBaseline(root string) error {
	data, err := readRegularFile(filepath.Join(root, "testdata", "benchmarks", "baseline.txt"),
		"historical benchmark baseline")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return writeAtomicArtifact(filepath.Join(root, "reports", "benchmarks", "legacy-baseline.txt"),
		data, "historical benchmark evidence")
}

func resolveReleaseBenchmarkCommit(ctx context.Context, runner SpecRunner, directory, ref string) (string, error) {
	data, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "git", Args: []string{"rev-parse", "--verify", ref}, Dir: directory,
	}, "resolve release benchmark commit")
	if err != nil {
		return "", err
	}
	return parseCommandSHA(data, "release benchmark commit")
}
