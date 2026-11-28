// Package junitreport renders bounded JUnit XML as a standalone HTML report.
package junitreport

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxReportBytes = 64 << 20

type reportError struct {
	message string
}

func (err reportError) Error() string {
	return err.message
}

type testSuites struct {
	XMLName  xml.Name    `xml:"testsuites"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Suites   []testSuite `xml:"testsuite"`
}

type testSuite struct {
	Name      string     `xml:"name,attr"`
	Package   string     `xml:"package,attr"`
	Tests     int        `xml:"tests,attr"`
	Failures  int        `xml:"failures,attr"`
	Errors    int        `xml:"errors,attr"`
	Skipped   int        `xml:"skipped,attr"`
	Time      string     `xml:"time,attr"`
	Timestamp string     `xml:"timestamp,attr"`
	Cases     []testCase `xml:"testcase"`
}

type testCase struct {
	Name      string       `xml:"name,attr"`
	ClassName string       `xml:"classname,attr"`
	Time      string       `xml:"time,attr"`
	Failure   *testOutcome `xml:"failure"`
	Error     *testOutcome `xml:"error"`
	Skipped   *testOutcome `xml:"skipped"`
	SystemOut string       `xml:"system-out"`
	SystemErr string       `xml:"system-err"`
}

type testOutcome struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

type reportView struct {
	Title  string
	Totals reportTotals
	Suites []suiteView
}

type reportTotals struct {
	Tests    int
	Failures int
	Errors   int
	Skipped  int
	Time     string
}

type suiteView struct {
	Name      string
	Package   string
	Timestamp string
	Totals    reportTotals
	Cases     []caseView
}

type caseView struct {
	Name      string
	ClassName string
	Time      string
	Status    string
	Details   string
}

// Render converts one repository-relative JUnit XML file into one bounded,
// standalone repository-relative HTML file.
func Render(rootPath, inputPath, outputPath string) (returnErr error) {
	if inputPath == "" || outputPath == "" {
		return reportError{message: "input and output paths are required"}
	}
	root, openErr := os.OpenRoot(rootPath)
	if openErr != nil {
		return fmt.Errorf("open report root: %w", openErr)
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()

	data, err := readInput(root, filepath.ToSlash(inputPath))
	if err != nil {
		return err
	}
	var suites testSuites
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&suites); err != nil {
		return fmt.Errorf("decode JUnit XML: %w", err)
	}
	if suites.XMLName.Local != "testsuites" || len(suites.Suites) == 0 {
		return reportError{message: "JUnit XML has no test suites"}
	}

	view := makeView(suites)
	var output bytes.Buffer
	reportTemplate, parseErr := template.New("junit").Parse(reportTemplateText)
	if parseErr != nil {
		return fmt.Errorf("parse JUnit HTML template: %w", parseErr)
	}
	if err := reportTemplate.Execute(&output, view); err != nil {
		return fmt.Errorf("render JUnit HTML: %w", err)
	}
	if output.Len() > maxReportBytes {
		return reportError{message: fmt.Sprintf("rendered JUnit HTML exceeds %d bytes", maxReportBytes)}
	}
	if err := writeOutput(root, filepath.ToSlash(outputPath), output.Bytes()); err != nil {
		return err
	}
	return nil
}

func readInput(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("lstat JUnit input: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxReportBytes {
		return nil, reportError{
			message: fmt.Sprintf("JUnit input must be a regular file no larger than %d bytes", maxReportBytes),
		}
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open JUnit input: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxReportBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read JUnit input: %w", err)
	}
	if len(data) > maxReportBytes {
		return nil, reportError{message: fmt.Sprintf("JUnit input exceeds %d bytes", maxReportBytes)}
	}
	return data, nil
}

func writeOutput(root *os.Root, name string, data []byte) error {
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return reportError{message: "JUnit HTML output exists and is not a regular file"}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat JUnit output: %w", err)
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create JUnit output: %w", err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write JUnit output: %w", err)
	}
	return nil
}

func makeView(suites testSuites) reportView {
	view := reportView{Title: strings.TrimSpace(suites.Name), Suites: make([]suiteView, 0, len(suites.Suites))}
	if view.Title == "" {
		view.Title = "GoBFD test report"
	}
	for _, suite := range suites.Suites {
		suiteOutput := suiteView{
			Name: suite.Name, Package: suite.Package, Timestamp: suite.Timestamp,
			Totals: reportTotals{
				Tests: suite.Tests, Failures: suite.Failures, Errors: suite.Errors,
				Skipped: suite.Skipped, Time: suite.Time,
			},
			Cases: make([]caseView, 0, len(suite.Cases)),
		}
		for _, test := range suite.Cases {
			status, details := outcome(test)
			suiteOutput.Cases = append(suiteOutput.Cases, caseView{
				Name: test.Name, ClassName: test.ClassName, Time: test.Time, Status: status, Details: details,
			})
		}
		view.Suites = append(view.Suites, suiteOutput)
	}
	view.Totals = reportTotals{
		Tests: suites.Tests, Failures: suites.Failures, Errors: suites.Errors, Skipped: suites.Skipped, Time: suites.Time,
	}
	if view.Totals.Tests == 0 {
		for _, suite := range view.Suites {
			view.Totals.Tests += suite.Totals.Tests
			view.Totals.Failures += suite.Totals.Failures
			view.Totals.Errors += suite.Totals.Errors
			view.Totals.Skipped += suite.Totals.Skipped
		}
	}
	return view
}

func outcome(test testCase) (string, string) {
	for _, candidate := range []struct {
		status  string
		outcome *testOutcome
	}{{"failed", test.Failure}, {"error", test.Error}, {"skipped", test.Skipped}} {
		if candidate.outcome != nil {
			return candidate.status, strings.TrimSpace(strings.Join(
				[]string{candidate.outcome.Type, candidate.outcome.Message, candidate.outcome.Body}, "\n",
			))
		}
	}
	return "passed", ""
}

const reportTemplateText = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title><style>
:root{color-scheme:light dark;font-family:system-ui,sans-serif}
body{margin:2rem;max-width:120rem}h1,h2{margin-bottom:.5rem}
.summary{display:flex;gap:1rem;flex-wrap:wrap;margin:1rem 0 2rem}
.metric{border:1px solid #7777;border-radius:.5rem;padding:.75rem 1rem}
table{border-collapse:collapse;width:100%;margin-bottom:2rem}
th,td{border-bottom:1px solid #7777;padding:.45rem;text-align:left;vertical-align:top}
.passed{color:#16803a}.failed,.error{color:#c83232}.skipped{color:#a46b00}
details pre{white-space:pre-wrap;overflow-wrap:anywhere}
</style></head><body><h1>{{.Title}}</h1><div class="summary">
<span class="metric">Tests: {{.Totals.Tests}}</span><span class="metric">Failures: {{.Totals.Failures}}</span>
<span class="metric">Errors: {{.Totals.Errors}}</span><span class="metric">Skipped: {{.Totals.Skipped}}</span>
<span class="metric">Time: {{.Totals.Time}}s</span></div>
{{range .Suites}}<section><h2>{{.Name}}</h2><p>{{.Package}} {{.Timestamp}}</p><table>
<thead><tr><th>Status</th><th>Test</th><th>Class</th><th>Time</th><th>Details</th></tr></thead><tbody>
{{range .Cases}}<tr><td class="{{.Status}}">{{.Status}}</td><td>{{.Name}}</td>
<td>{{.ClassName}}</td><td>{{.Time}}s</td><td>{{if .Details}}<details><summary>output</summary>
<pre>{{.Details}}</pre></details>{{end}}</td></tr>{{end}}
</tbody></table></section>{{end}}</body></html>
`
