package cirunner

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	benchmarkSchemaVersion     = "gobfd.benchmark-comparison.v1"
	comparisonCellLimit        = 4096
	criticalRegressionFloor    = 0.0000001
	reportedRegressionFloor    = 0.000000001
	regressionPercentThreshold = 10
)

// BenchmarkReportOptions configures benchstat execution and published artifacts.
type BenchmarkReportOptions struct {
	Root        string
	Old         string
	New         string
	Text        string
	Markdown    string
	HTML        string
	JSON        string
	CSV         string
	Notes       string
	StepSummary string
	// SkipStepSummary is reserved for release reports, which have no Actions step-summary contract.
	SkipStepSummary bool
	ReleaseContext  *BenchmarkReleaseContext
	Warning         io.Writer
	Runner          SpecRunner
}

// BenchmarkReleaseContext adds release-only context without changing generic PR reports.
type BenchmarkReleaseContext struct {
	Baseline    string
	Version     string
	GeneratedAt time.Time
}

type benchmarkComparison struct {
	SchemaVersion  string                   `json:"schema_version"`
	Baseline       string                   `json:"baseline,omitempty"`
	Version        string                   `json:"version,omitempty"`
	GeneratedAt    string                   `json:"generated_at,omitempty"`
	ComparisonRows []benchmarkComparisonRow `json:"comparison_rows"`
	Notes          []string                 `json:"notes"`
}

type benchmarkComparisonRow struct {
	Unit                     string  `json:"unit"`
	Name                     string  `json:"name"`
	Base                     float64 `json:"base"`
	Head                     float64 `json:"head"`
	Delta                    string  `json:"delta"`
	Significance             string  `json:"significance"`
	RegressionClassification string  `json:"regression_classification"`
}

// BenchmarkReport runs benchstat, classifies regressions, and publishes visualizable reports.
func BenchmarkReport(ctx context.Context, options BenchmarkReportOptions) error {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("benchmark report command runner is required: %w", errInvalidConfig)
	}
	if err := validateBenchmarkReleaseContext(options.ReleaseContext); err != nil {
		return err
	}
	oldPath, err := validateRootFile(root, options.Old, "old benchmark input", false)
	if err != nil {
		return err
	}
	newPath, err := validateRootFile(root, options.New, "new benchmark input", false)
	if err != nil {
		return err
	}
	if _, err := readRegularFile(oldPath, "old benchmark input", benchmarkInputLimit); err != nil {
		return err
	}
	if _, err := readRegularFile(newPath, "new benchmark input", benchmarkInputLimit); err != nil {
		return err
	}

	outputs := map[string]string{}
	for purpose, name := range map[string]string{
		"benchmark Markdown report": options.Markdown,
		"benchmark HTML report":     options.HTML,
		"benchmark JSON report":     options.JSON,
		"benchstat CSV":             options.CSV,
		"benchstat notes":           options.Notes,
	} {
		path, pathErr := validateRootFile(root, name, purpose, true)
		if pathErr != nil {
			return pathErr
		}
		outputs[purpose] = path
	}
	if options.Text != "" {
		path, pathErr := validateRootFile(root, options.Text, "benchmark text report", true)
		if pathErr != nil {
			return pathErr
		}
		outputs["benchmark text report"] = path
	}
	if !options.SkipStepSummary {
		if err := validateStepSummary(options.StepSummary); err != nil {
			return err
		}
	}
	for purpose, path := range outputs {
		if err := resetArtifact(path, purpose); err != nil {
			return err
		}
	}

	baseArguments := []string{"tool", "-modfile=tools/go.mod", "benchstat", options.Old, options.New}
	var textOutput bytes.Buffer
	textErr := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go", Args: append([]string(nil), baseArguments...), Dir: root,
		Stdout: &textOutput, Stderr: &textOutput,
	})
	if textOutput.Len() == 0 {
		if textErr != nil {
			textOutput.WriteString("benchstat comparison failed\n")
		} else {
			return fmt.Errorf("benchstat text output is empty: %w", errInvalidConfig)
		}
	}

	var csvOutput bytes.Buffer
	var notesOutput bytes.Buffer
	csvArguments := []string{"tool", "-modfile=tools/go.mod", "benchstat", "-format", "csv", options.Old, options.New}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go", Args: csvArguments, Dir: root, Stdout: &csvOutput, Stderr: &notesOutput,
	}); err != nil {
		return fmt.Errorf("generate benchstat CSV: %w", err)
	}
	if csvOutput.Len() == 0 {
		return fmt.Errorf("benchstat CSV output is empty: %w", errInvalidConfig)
	}
	comparison, err := parseBenchmarkCSV(csvOutput.Bytes(), notesOutput.String())
	if err != nil {
		return err
	}
	if release := options.ReleaseContext; release != nil {
		comparison.Baseline = release.Baseline
		comparison.Version = release.Version
		comparison.GeneratedAt = release.GeneratedAt.UTC().Format(time.RFC3339)
	}

	markdown := "## Benchmark comparison\n\n```text\n" + textOutput.String()
	if !strings.HasSuffix(markdown, "\n") {
		markdown += "\n"
	}
	markdown += "```\n"
	htmlReport := "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>Benchmark Comparison</title>" +
		"<style>body{font-family:system-ui;margin:2em;background:#1a1a2e;color:#e0e0e0}" +
		"pre{background:#16213e;padding:1.5em;border-radius:8px;font-size:14px;line-height:1.6;" +
		"border:1px solid #0f3460;overflow-x:auto}</style></head><body>" +
		"<h1 style=\"color:#00d4ff\">Benchmark Comparison</h1><pre>" +
		html.EscapeString(textOutput.String()) + "</pre></body></html>\n"
	if release := options.ReleaseContext; release != nil {
		title := "GoBFD Benchmark Comparison: " + html.EscapeString(release.Baseline) + " vs " + html.EscapeString(release.Version)
		metadata := html.EscapeString(release.Baseline) + " vs " + html.EscapeString(release.Version) +
			" &mdash; " + html.EscapeString(release.GeneratedAt.UTC().Format(time.RFC3339))
		htmlReport = "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>" + title + "</title>" +
			"<style>body{font-family:system-ui;margin:2em;background:#1a1a2e;color:#e0e0e0}" +
			"h1{color:#00d4ff}pre{background:#16213e;padding:1.5em;border-radius:8px;font-size:14px;" +
			"line-height:1.6;border:1px solid #0f3460;overflow-x:auto}.meta{color:#888;font-size:13px}</style>" +
			"</head><body><h1>GoBFD Benchmark Comparison</h1><p class=\"meta\">" + metadata + "</p><pre>" +
			html.EscapeString(textOutput.String()) + "</pre></body></html>\n"
	}
	jsonReport, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return fmt.Errorf("encode structured benchmark report: %w", err)
	}
	jsonReport = append(jsonReport, '\n')

	artifacts := []struct {
		path    string
		data    []byte
		purpose string
	}{
		{outputs["benchmark Markdown report"], []byte(markdown), "benchmark Markdown report"},
		{outputs["benchmark HTML report"], []byte(htmlReport), "benchmark HTML report"},
		{outputs["benchmark JSON report"], jsonReport, "benchmark JSON report"},
		{outputs["benchstat CSV"], csvOutput.Bytes(), "benchstat CSV"},
		{outputs["benchstat notes"], notesOutput.Bytes(), "benchstat notes"},
	}
	if path := outputs["benchmark text report"]; path != "" {
		artifacts = append(artifacts, struct {
			path    string
			data    []byte
			purpose string
		}{path, textOutput.Bytes(), "benchmark text report"})
	}
	for _, artifact := range artifacts {
		if err := writeAtomicArtifact(artifact.path, artifact.data, artifact.purpose); err != nil {
			return err
		}
	}
	if !options.SkipStepSummary {
		if err := appendBenchmarkSummary(options.StepSummary, comparison); err != nil {
			return err
		}
	}
	writeBenchmarkWarnings(options.Warning, comparison)
	return nil
}

func validateBenchmarkReleaseContext(release *BenchmarkReleaseContext) error {
	if release == nil {
		return nil
	}
	if release.Baseline == "" || release.Version == "" || release.GeneratedAt.IsZero() ||
		len(release.Baseline) > comparisonCellLimit || len(release.Version) > comparisonCellLimit ||
		hasControl(release.Baseline) || hasControl(release.Version) {
		return fmt.Errorf("release benchmark context is invalid: %w", errInvalidConfig)
	}
	return nil
}

func parseBenchmarkCSV(data []byte, notesText string) (benchmarkComparison, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return benchmarkComparison{}, fmt.Errorf("parse benchstat CSV: %w", err)
	}
	comparison := benchmarkComparison{
		SchemaVersion: benchmarkSchemaVersion,
		Notes:         nonemptyLines(notesText),
	}
	unit := ""
	for _, record := range records {
		if len(record) == 0 || allEmpty(record) {
			unit = ""
			continue
		}
		if record[0] == "" {
			if len(record) >= 7 && record[2] == "CI" && record[5] == "vs base" {
				unit = record[1]
			} else {
				unit = ""
			}
			continue
		}
		if unit == "" || len(record) < 7 || record[0] == "geomean" || record[1] == "" || record[3] == "" {
			continue
		}
		if err := validateComparisonCell(record[0], "benchmark name"); err != nil {
			return benchmarkComparison{}, err
		}
		for index, purpose := range map[int]string{5: "benchmark delta", 6: "benchmark significance"} {
			if err := validateComparisonCell(record[index], purpose); err != nil {
				return benchmarkComparison{}, err
			}
		}
		base, baseErr := strconv.ParseFloat(record[1], 64)
		if baseErr != nil {
			return benchmarkComparison{}, fmt.Errorf("parse base value for %s/%s: %w", record[0], unit, baseErr)
		}
		head, headErr := strconv.ParseFloat(record[3], 64)
		if headErr != nil {
			return benchmarkComparison{}, fmt.Errorf("parse head value for %s/%s: %w", record[0], unit, headErr)
		}
		row := benchmarkComparisonRow{
			Unit: unit, Name: record[0], Base: base, Head: head,
			Delta: record[5], Significance: record[6], RegressionClassification: "none",
		}
		row.RegressionClassification = classifyRegression(row)
		comparison.ComparisonRows = append(comparison.ComparisonRows, row)
	}
	if len(comparison.ComparisonRows) == 0 {
		return benchmarkComparison{}, fmt.Errorf("benchstat CSV contains no comparison rows: %w", errInvalidConfig)
	}
	return comparison, nil
}

func classifyRegression(row benchmarkComparisonRow) string {
	if row.Unit != "sec/op" || !strings.HasPrefix(row.Delta, "+") || !strings.HasSuffix(row.Delta, "%") {
		return "none"
	}
	percent, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(row.Delta, "+"), "%"), 64)
	if err != nil || percent < regressionPercentThreshold {
		return "none"
	}
	if isCriticalBenchmark(row.Name) &&
		(row.Base >= criticalRegressionFloor || row.Head >= criticalRegressionFloor) {
		return "critical"
	}
	if row.Base >= reportedRegressionFloor || row.Head >= reportedRegressionFloor {
		return "reported"
	}
	return "none"
}

func isCriticalBenchmark(name string) bool {
	for _, benchmark := range []string{
		"ManagerDemux1000Sessions",
		"ManagerLookup1000Sessions",
		"RecvDecodeLookupEnqueue",
		"RecvDecodeFSM",
		"TxMarshalJitter",
		"SessionRecvPacket",
		"ControlPacketMarshal",
		"ControlPacketUnmarshal",
		"ControlPacketRoundTrip",
		"BuildInnerPacket",
		"StripInnerPacket",
		"VXLANHeaderMarshal",
		"VXLANHeaderUnmarshal",
		"GeneveHeaderMarshal",
		"GeneveHeaderUnmarshal",
	} {
		if strings.HasPrefix(name, benchmark+"-") {
			return true
		}
	}
	return false
}

func validateComparisonCell(value, purpose string) error {
	if len(value) > comparisonCellLimit || hasControl(value) {
		return fmt.Errorf("%s contains control characters or is too long: %w", purpose, errInvalidConfig)
	}
	return nil
}

func allEmpty(record []string) bool {
	for _, field := range record {
		if field != "" {
			return false
		}
	}
	return true
}

func nonemptyLines(value string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func validateStepSummary(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || hasControl(path) {
		return fmt.Errorf(
			"GITHUB_STEP_SUMMARY must be a clean absolute path without control characters: %w",
			errInvalidConfig,
		)
	}
	if err := inspectDirectoryTree(filepath.Dir(path), "GITHUB_STEP_SUMMARY parent"); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect GITHUB_STEP_SUMMARY: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("GITHUB_STEP_SUMMARY has mode %s: %w", info.Mode(), errInvalidConfig)
	}
	return nil
}

func appendBenchmarkSummary(path string, comparison benchmarkComparison) error {
	var summary strings.Builder
	for _, row := range comparison.ComparisonRows {
		if row.RegressionClassification != "reported" {
			continue
		}
		if summary.Len() == 0 {
			summary.WriteString("### Report-only benchmark regressions\n\n")
			summary.WriteString("| Benchmark | Delta | Significance |\n| --- | ---: | --- |\n")
		}
		fmt.Fprintf(&summary, "| `%s` | %s | %s |\n",
			escapeMarkdownCell(row.Name), escapeMarkdownCell(row.Delta), escapeMarkdownCell(row.Significance))
	}
	if len(comparison.Notes) > 0 {
		if summary.Len() > 0 {
			summary.WriteByte('\n')
		}
		summary.WriteString("### benchstat notes\n\n")
		for _, note := range comparison.Notes {
			summary.WriteString("    ")
			summary.WriteString(note)
			summary.WriteByte('\n')
		}
	}
	if summary.Len() == 0 {
		return nil
	}
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open GITHUB_STEP_SUMMARY for append: %w", err)
	}
	written, writeErr := io.WriteString(output, summary.String())
	if writeErr == nil && written != summary.Len() {
		writeErr = io.ErrShortWrite
	}
	closeErr := output.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(
			wrapOptional("append benchmark step summary", writeErr),
			wrapOptional("close benchmark step summary", closeErr),
		)
	}
	return nil
}

func writeBenchmarkWarnings(output io.Writer, comparison benchmarkComparison) {
	if output == nil {
		return
	}
	wroteHeader := false
	for _, row := range comparison.ComparisonRows {
		if row.RegressionClassification != "critical" {
			continue
		}
		if !wroteHeader {
			_, _ = io.WriteString(
				output,
				"::warning::Critical benchmark regression detected (>=10%). Review bench-report.md.\n",
			)
			wroteHeader = true
		}
		_, _ = fmt.Fprintf(output, "%s,%s,%s\n", row.Name, row.Delta, row.Significance)
	}
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "`", "\\`")
}
