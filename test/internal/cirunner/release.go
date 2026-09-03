package cirunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/junitreport"
)

const releaseArtifactLimit = 64 << 20

// ReleaseBuildOptions supplies release tag metadata and build dependencies.
type ReleaseBuildOptions struct {
	RefName string
	SHA     string
	Output  string
	Now     func() time.Time
	Runner  CommandRunner
}

// ReleaseBuild compiles release binaries with the tag version.
func ReleaseBuild(ctx context.Context, options ReleaseBuildOptions) error {
	version, err := validateReleaseVersion(options.RefName)
	if err != nil {
		return err
	}
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return fmt.Errorf("release version is empty after tag prefix removal: %w", errInvalidConfig)
	}
	return Build(ctx, BuildOptions{
		Version: version, SHA: options.SHA, Output: options.Output, Now: options.Now, Runner: options.Runner,
	})
}

// ReleaseTestReport runs release tests and renders their JUnit HTML report.
func ReleaseTestReport(ctx context.Context, root string, runner SpecRunner) error {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("release test command runner is required: %w", errInvalidConfig)
	}
	paths := []string{
		"reports/tests/unit-report.xml",
		"reports/tests/unit-report.json",
		"reports/tests/unit-report.html",
	}
	for _, name := range paths {
		path, pathErr := validateRootFile(root, name, "release test report", true)
		if pathErr != nil {
			return pathErr
		}
		if err := resetArtifact(path, "release test report"); err != nil {
			return err
		}
	}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "go",
		Args: []string{
			"tool", "-modfile=tools/go.mod", "gotestsum",
			"--junitfile", "reports/tests/unit-report.xml",
			"--jsonfile", "reports/tests/unit-report.json",
			"--format", "short-verbose", "--", "-buildvcs=false", "./...", "-race", "-count=1",
		},
		Dir: root,
	}); err != nil {
		return fmt.Errorf("run release tests: %w", err)
	}
	if err := junitreport.Render(root, "reports/tests/unit-report.xml", "reports/tests/unit-report.html"); err != nil {
		return fmt.Errorf("render release JUnit report: %w", err)
	}
	return nil
}

// ReleaseBenchmarks writes per-package and combined release benchmark evidence.
func ReleaseBenchmarks(ctx context.Context, root, version string, output io.Writer, runner SpecRunner) error {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	version, err = validateReleaseVersion(version)
	if err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("release benchmark command runner is required: %w", errInvalidConfig)
	}
	if output == nil {
		output = io.Discard
	}
	versionDirectory := filepath.Join(root, "testdata", "benchmarks", version)
	if directoryErr := ensureDirectory(versionDirectory, "release benchmark", reportDirectoryMode); directoryErr != nil {
		return directoryErr
	}
	if directoryErr := ensureDirectory(
		filepath.Join(root, "reports", "benchmarks"), "release report", reportDirectoryMode,
	); directoryErr != nil {
		return directoryErr
	}
	type benchmarkTarget struct {
		file    string
		pkg     string
		content []byte
	}
	targets := []benchmarkTarget{
		{file: "benchmark-bfd.txt", pkg: "./internal/bfd/"},
		{file: "benchmark-netio.txt", pkg: "./internal/netio/"},
	}
	paths := make([]string, len(targets)+1)
	for index := range targets {
		paths[index], err = validateRootFile(root, filepath.Join("testdata", "benchmarks", version, targets[index].file), "release benchmark", false)
		if err != nil {
			return err
		}
	}
	paths[len(targets)], err = validateRootFile(root, filepath.Join("testdata", "benchmarks", version, "benchmark.txt"), "combined release benchmark", false)
	if err != nil {
		return err
	}
	for index, path := range paths {
		purpose := "release benchmark"
		if index == len(targets) {
			purpose = "combined release benchmark"
		}
		if err := resetArtifact(path, purpose); err != nil {
			return err
		}
	}
	for index := range targets {
		artifact, openErr := os.OpenFile(paths[index], os.O_WRONLY|os.O_TRUNC, benchmarkArtifactMode)
		if openErr != nil {
			return fmt.Errorf("open prepared release benchmark %s: %w", paths[index], openErr)
		}
		var captured bytes.Buffer
		runErr := runner.RunCommand(ctx, CommandSpec{
			Name: "go",
			Args: []string{
				"test", "-buildvcs=false", "-bench=.", "-benchmem", "-count=6",
				"-run=^$", "-timeout=120s", targets[index].pkg,
			},
			Dir: root, Stdout: io.MultiWriter(output, artifact, &captured),
		})
		closeErr := artifact.Close()
		if runErr != nil || closeErr != nil {
			return errors.Join(wrapOptional("run release benchmark", runErr), wrapOptional("close release benchmark", closeErr))
		}
		if captured.Len() == 0 {
			return fmt.Errorf("release benchmark output for %s is empty: %w", targets[index].pkg, errInvalidConfig)
		}
		targets[index].content = append([]byte(nil), captured.Bytes()...)
	}
	combined := append(append([]byte(nil), targets[0].content...), targets[1].content...)
	return writeAtomicArtifact(paths[len(targets)], combined, "combined release benchmark")
}

// ReleaseMetadataOptions supplies benchmark metadata inputs.
type ReleaseMetadataOptions struct {
	Root    string
	Version string
	SHA     string
	Now     func() time.Time
	Runner  SpecRunner
}

// ReleaseBenchmarkMetadata writes the release benchmark metadata JSON.
func ReleaseBenchmarkMetadata(ctx context.Context, options ReleaseMetadataOptions) error {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	version, err := validateReleaseVersion(options.Version)
	if err != nil {
		return err
	}
	commit, err := validateSHA(options.SHA)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("release metadata command runner is required: %w", errInvalidConfig)
	}
	var goVersion bytes.Buffer
	if commandErr := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go", Args: []string{"env", "GOVERSION"}, Dir: root, Stdout: &goVersion,
	}); commandErr != nil {
		return fmt.Errorf("read Go version: %w", commandErr)
	}
	goName := strings.TrimSuffix(goVersion.String(), "\n")
	if goName == "" || hasControl(goName) {
		return fmt.Errorf("go env GOVERSION returned an invalid value: %w", errInvalidConfig)
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	metadata := struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
		Go      string `json:"go"`
		Count   int    `json:"count"`
	}{version, commit, now().UTC().Format(time.RFC3339), goName, 6}
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode release benchmark metadata: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Join(root, "testdata", "benchmarks", version)
	if err := ensureDirectory(directory, "release benchmark metadata", reportDirectoryMode); err != nil {
		return err
	}
	return writeAtomicArtifact(filepath.Join(directory, "meta.json"), data, "release benchmark metadata")
}

// ReleaseComparisonOptions supplies release-versus-baseline report inputs.
type ReleaseComparisonOptions struct {
	Root    string
	Version string
	Now     func() time.Time
	Runner  SpecRunner
}

// ReleaseBenchmarkComparison writes text, HTML, and structured baseline comparisons when a baseline exists.
func ReleaseBenchmarkComparison(ctx context.Context, options ReleaseComparisonOptions) error {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	version, err := validateReleaseVersion(options.Version)
	if err != nil {
		return err
	}
	reportDirectory := filepath.Join(root, "reports", "benchmarks")
	if directoryErr := ensureDirectory(
		reportDirectory, "release benchmark comparison", reportDirectoryMode,
	); directoryErr != nil {
		return directoryErr
	}
	comparisonArtifacts := releaseComparisonArtifactNames()
	comparisonPaths := make([]string, len(comparisonArtifacts))
	for index, name := range comparisonArtifacts {
		comparisonPaths[index], err = validateRootFile(root, filepath.Join("reports", "benchmarks", name), "release benchmark comparison", false)
		if err != nil {
			return err
		}
	}
	for _, path := range comparisonPaths {
		if err := resetArtifact(path, "release benchmark comparison"); err != nil {
			return err
		}
	}
	baseline := filepath.Join(root, "testdata", "benchmarks", "baseline.txt")
	if _, err := os.Lstat(baseline); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect release benchmark baseline: %w", err)
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	return BenchmarkReport(ctx, BenchmarkReportOptions{
		Root: root, Old: "testdata/benchmarks/baseline.txt",
		New:             "testdata/benchmarks/" + version + "/benchmark.txt",
		Text:            "reports/benchmarks/comparison-vs-baseline.txt",
		Markdown:        "reports/benchmarks/comparison-vs-baseline.md",
		HTML:            "reports/benchmarks/comparison-vs-baseline.html",
		JSON:            "reports/benchmarks/comparison-vs-baseline.json",
		CSV:             "reports/benchmarks/comparison-vs-baseline.csv",
		Notes:           "reports/benchmarks/comparison-vs-baseline-notes.txt",
		SkipStepSummary: true,
		ReleaseContext: &BenchmarkReleaseContext{
			Baseline: "baseline", Version: version, GeneratedAt: now().UTC(),
		},
		Runner: options.Runner,
	})
}

func releaseComparisonArtifactNames() []string {
	return []string{
		"comparison-vs-baseline.txt",
		"comparison-vs-baseline.md",
		"comparison-vs-baseline.html",
		"comparison-vs-baseline.json",
		"comparison-vs-baseline.csv",
		"comparison-vs-baseline-notes.txt",
	}
}

// ReleaseReportsArchive copies benchmark evidence into reports and creates the release report archive.
func ReleaseReportsArchive(root, version string) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	version, err = validateReleaseVersion(version)
	if err != nil {
		return err
	}
	reports := filepath.Join(root, "reports")
	benchmarks := filepath.Join(reports, "benchmarks")
	if directoryErr := ensureDirectory(benchmarks, "release benchmark reports", reportDirectoryMode); directoryErr != nil {
		return directoryErr
	}
	repositoryRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository root for release reports: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close repository root for release reports", repositoryRoot.Close()))
	}()
	sourceDirectory := filepath.Join("testdata", "benchmarks", version)
	for _, name := range []string{"benchmark-bfd.txt", "benchmark-netio.txt", "benchmark.txt", "meta.json"} {
		data, readErr := readRootedRegularFile(repositoryRoot, filepath.Join(sourceDirectory, name), "release benchmark evidence", releaseArtifactLimit)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if writeErr := writeAtomicArtifact(
			filepath.Join(benchmarks, name), data, "release benchmark evidence",
		); writeErr != nil {
			return writeErr
		}
	}
	archive, err := validateRootFile(root, "gobfd-"+version+"-reports.tar.gz", "release reports archive", false)
	if err != nil {
		return err
	}
	reportsRoot, err := os.OpenRoot(reports)
	if err != nil {
		return fmt.Errorf("open reports root for release archive: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close reports root for release archive", reportsRoot.Close()))
	}()
	return writeTarGzip(archive, reports, reportsRoot)
}

func validateReleaseVersion(version string) (string, error) {
	if version == "" || version == "." || version == ".." || len(version) > 128 || filepath.Base(version) != version ||
		filepath.Clean(version) != version || hasControl(version) || strings.ContainsAny(version, " \\") {
		return "", fmt.Errorf("GITHUB_REF_NAME is not a safe release artifact version: %w", errInvalidConfig)
	}
	return version, nil
}

func writeTarGzip(path, reports string, reportsRoot *os.Root) (returnErr error) {
	if err := resetArtifact(path, "release reports archive"); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, benchmarkArtifactMode)
	if err != nil {
		return fmt.Errorf("open release reports archive: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release reports archive", file.Close()))
	}()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	walkErr := filepath.WalkDir(reports, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk release report %s: %w", name, walkErr)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect release report %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("release report %s has unsupported mode %s: %w", name, info.Mode(), errInvalidConfig)
		}
		relative, err := filepath.Rel(reports, name)
		if err != nil {
			return fmt.Errorf("resolve release report path %s: %w", name, err)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("create release report archive header for %s: %w", name, err)
		}
		header.Name = filepath.ToSlash(filepath.Join("reports", relative))
		if info.IsDir() {
			header.Name += "/"
		}
		if headerErr := tarWriter.WriteHeader(header); headerErr != nil {
			return fmt.Errorf("write release report archive header for %s: %w", name, headerErr)
		}
		if info.IsDir() {
			return nil
		}
		input, err := openReportArchiveFile(reportsRoot, relative, info)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, input)
		if copyErr == nil {
			copyErr = verifyOpenedRegularFile(input, info, "release report "+relative)
		}
		return errors.Join(
			wrapOptional("copy release report "+name, copyErr),
			wrapOptional("close release report "+name, input.Close()),
		)
	})
	closeErr := errors.Join(
		wrapOptional("close release report tar writer", tarWriter.Close()),
		wrapOptional("close release report gzip writer", gzipWriter.Close()),
	)
	if walkErr != nil || closeErr != nil {
		return errors.Join(walkErr, closeErr)
	}
	return nil
}

func openReportArchiveFile(reportsRoot *os.Root, name string, expected fs.FileInfo) (*os.File, error) {
	return openRootedRegularFile(reportsRoot, name, expected, "release report")
}

func readRootedRegularFile(root *os.Root, name, purpose string, limit int64) ([]byte, error) {
	expected, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect %s %s: %w", purpose, name, err)
	}
	if !expected.Mode().IsRegular() || expected.Size() == 0 {
		return nil, fmt.Errorf("%s %s is not a non-empty regular file: %w", purpose, name, errInvalidConfig)
	}
	if expected.Size() > limit {
		return nil, fmt.Errorf("%s %s exceeds %d bytes: %w", purpose, name, limit, errInvalidConfig)
	}
	file, err := openRootedRegularFile(root, name, expected, purpose)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	if readErr == nil {
		readErr = verifyOpenedRegularFile(file, expected, purpose+" "+name)
	}
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(
			wrapOptional("read "+purpose+" "+name, readErr),
			wrapOptional("close "+purpose+" "+name, closeErr),
		)
	}
	if int64(len(data)) != expected.Size() {
		return nil, fmt.Errorf("%s %s size changed while reading: %w", purpose, name, errInvalidConfig)
	}
	return data, nil
}

func openRootedRegularFile(root *os.Root, name string, expected fs.FileInfo, purpose string) (*os.File, error) {
	if root == nil || expected == nil || !expected.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s lacks expected regular file metadata: %w", purpose, name, errInvalidConfig)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open rooted %s %s: %w", purpose, name, err)
	}
	if err := verifyOpenedRegularFile(file, expected, purpose+" "+name); err != nil {
		return nil, errors.Join(err, wrapOptional("close rooted "+purpose+" "+name, file.Close()))
	}
	return file, nil
}

func verifyOpenedRegularFile(file *os.File, expected fs.FileInfo, purpose string) error {
	actual, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", purpose, err)
	}
	if !actual.Mode().IsRegular() || actual.Size() != expected.Size() || !os.SameFile(expected, actual) {
		return fmt.Errorf("opened %s does not match the expected regular file: %w", purpose, errInvalidConfig)
	}
	return nil
}
