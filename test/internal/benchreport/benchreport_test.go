package benchreport

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPublishesCompatibleReportAtomically(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	goInput := writeTestFile(t, directory, "bench-go.txt", strings.Join([]string{
		"goos: linux",
		"goarch: amd64",
		"cpu: test CPU",
		"BenchmarkControlPacketMarshal-8 1000 5.5 ns/op 0 B/op 0 allocs/op",
		"PASS",
		"",
	}, "\n"))
	frrInput := writeTestFile(t, directory, "bench-c-frr.txt", "BENCH\tfrr\tMarshal\t8.0\t1000\n")
	birdInput := writeTestFile(t, directory, "bench-c-bird.txt", "BENCH\tbird\tMarshal\t7.0\t1000\n")
	metadata := writeTestFile(t, directory, "meta.json", `{"version":"v0.6.2","go":"go1.27.0"}`+"\n")
	template := writeTestFile(t, directory, "template.html", "const REPORT_DATA = __BENCHMARK_DATA__;\n</html>\n")
	output := writeTestFile(t, directory, "report.html", "previous complete report\n")

	err := Render(context.Background(), Options{
		GoInput:    goInput,
		FRRInput:   frrInput,
		BIRDInput:  birdInput,
		Metadata:   metadata,
		Template:   template,
		Output:     output,
		Generated:  "2026-08-26T00:00:00Z",
		GCCVersion: "gcc (Debian trixie)",
		GoMaxProcs: "8",
	})
	if err != nil {
		t.Fatalf("render benchmark report: %v", err)
	}

	rendered, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read rendered report: %v", err)
	}
	if bytes.Contains(rendered, []byte("__BENCHMARK_DATA__")) {
		t.Fatal("rendered report retains the data marker")
	}
	const prefix = "const REPORT_DATA = "
	start := bytes.Index(rendered, []byte(prefix))
	if start < 0 {
		t.Fatalf("rendered report does not contain JSON prefix: %q", rendered)
	}
	end := bytes.IndexByte(rendered[start+len(prefix):], ';')
	if end < 0 {
		t.Fatalf("rendered report does not contain complete JSON: %q", rendered)
	}
	var report struct {
		Meta struct {
			Version  string `json:"gobfd_version"`
			Go       string `json:"go_version"`
			Platform string `json:"platform"`
		} `json:"meta"`
		Implementations []struct {
			ID string `json:"id"`
		} `json:"implementations"`
	}
	dataStart := start + len(prefix)
	if decodeErr := json.Unmarshal(rendered[dataStart:dataStart+end], &report); decodeErr != nil {
		t.Fatalf("decode rendered report JSON: %v", decodeErr)
	}
	if report.Meta.Version != "v0.6.2" || report.Meta.Go != "go1.27.0" {
		t.Fatalf("report metadata = %+v, want v0.6.2/go1.27.0", report.Meta)
	}
	if report.Meta.Platform != "test CPU, linux/amd64" {
		t.Fatalf("report platform = %q, want test CPU, linux/amd64", report.Meta.Platform)
	}
	gotIDs := make([]string, 0, len(report.Implementations))
	for _, implementation := range report.Implementations {
		gotIDs = append(gotIDs, implementation.ID)
	}
	if strings.Join(gotIDs, ",") != "go,frr,bird" {
		t.Fatalf("implementation IDs = %v, want go/frr/bird", gotIDs)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat rendered report: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("rendered report mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestRenderRejectsUnsafeInputWithoutReplacingReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata string
		goInput  string
		want     string
	}{
		{
			name:     "multiple metadata documents",
			metadata: "{}\n{}\n",
			goInput:  "BenchmarkControlPacketMarshal-8 1000 5.5 ns/op 0 B/op 0 allocs/op\n",
			want:     "more than one JSON document",
		},
		{
			name:     "oversized benchmark input",
			metadata: "{}\n",
			goInput:  strings.Repeat("#", maxInputSize+1),
			want:     "limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			output := writeTestFile(t, directory, "report.html", "previous complete report\n")
			err := Render(context.Background(), Options{
				GoInput:    writeTestFile(t, directory, "bench-go.txt", test.goInput),
				FRRInput:   writeTestFile(t, directory, "bench-c-frr.txt", "BENCH\tfrr\tMarshal\t8.0\t1000\n"),
				BIRDInput:  writeTestFile(t, directory, "bench-c-bird.txt", "BENCH\tbird\tMarshal\t7.0\t1000\n"),
				Metadata:   writeTestFile(t, directory, "meta.json", test.metadata),
				Template:   writeTestFile(t, directory, "template.html", "__BENCHMARK_DATA__\n"),
				Output:     output,
				Generated:  "2026-08-26T00:00:00Z",
				GCCVersion: "gcc (Debian trixie)",
				GoMaxProcs: "8",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Render error = %v, want diagnostic containing %q", err, test.want)
			}
			preserved, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatalf("read preserved report: %v", readErr)
			}
			if string(preserved) != "previous complete report\n" {
				t.Fatalf("invalid input changed report: %q", preserved)
			}
		})
	}
}

func TestParseCInputPreservesFirstSeenOrder(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, t.TempDir(), "bench-c-frr.txt", strings.Join([]string{
		"BENCH\tfrr\tMarshal\t8.0\t1000",
		"BENCH\tfrr\tUnmarshal\t9.0\t1000",
		"BENCH\tfrr\tMarshal\t10.0\t1000",
		"",
	}, "\n"))
	parsed, err := parseCInput(path, "frr")
	if err != nil {
		t.Fatalf("parse C benchmark input: %v", err)
	}
	if strings.Join(parsed.order, ",") != "Marshal,Unmarshal" {
		t.Fatalf("C benchmark order = %v, want first-seen order", parsed.order)
	}
	if parsed.averages["Marshal"] != 9 {
		t.Fatalf("Marshal average = %v, want 9", parsed.averages["Marshal"])
	}
}

func writeTestFile(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
	return path
}
