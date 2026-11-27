package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

func TestRunReleaseComparisonMeasuresPublishedStablePair(t *testing.T) {
	t.Parallel()
	root, temporary := t.TempDir(), t.TempDir()
	const records = "goos: linux\ngoarch: amd64\npkg: github.com/dantte-lp/gobfd/internal/bfd\n" +
		"BenchmarkManagerDemux1000Sessions-4 64 200 ns/op 0 B/op 0 allocs/op\n" +
		"BenchmarkRecvDecodeLookupEnqueue-4 64 200 ns/op 0 B/op 0 allocs/op\n" +
		"BenchmarkRecvDecodeFSM-4 64 200 ns/op 0 B/op 0 allocs/op\n" +
		"BenchmarkTxMarshalJitter-4 64 200 ns/op 0 B/op 0 allocs/op\nPASS\n"
	legacy := strings.ReplaceAll(strings.TrimPrefix(records,
		"goos: linux\ngoarch: amd64\npkg: github.com/dantte-lp/gobfd/internal/bfd\n"), "-4 ", "-8 ")
	for name, data := range map[string]string{
		"testdata/benchmarks/baseline.txt":                 legacy,
		"testdata/benchmarks/v1.0.0-alpha.2/benchmark.txt": strings.Repeat(records, 6),
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	baseSHA, headSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	tagSHA := strings.Repeat("c", 40)
	var tagDrift bool
	var sampleOrder []string
	runner := publisherRunnerFunc(func(_ context.Context, spec cirunner.CommandSpec) error {
		if spec.Name == "gh" {
			if !slices.Contains(spec.Env, "GH_TOKEN=fixture-read-token") {
				t.Error("published stable read lacks its explicit read-token environment")
			}
		} else {
			if spec.Env == nil || !slices.Contains(spec.Env, "GOTOOLCHAIN=local") {
				t.Error("benchmark child lacks an explicit same-compiler environment")
			}
			for _, entry := range spec.Env {
				if strings.HasPrefix(entry, "GH_TOKEN=") || strings.HasPrefix(entry, "GITHUB_TOKEN=") {
					t.Errorf("%s child received a GitHub token", spec.Name)
				}
			}
		}
		var output string
		switch spec.Name {
		case "gh":
			switch {
			case slices.Contains(spec.Args, "--paginate"):
				output = `[[{"draft":false,"prerelease":false,"tag_name":"v0.6.4"},` +
					`{"draft":true,"prerelease":true,"tag_name":"v1.0.0-alpha.1"}]]`
			case slices.Contains(spec.Args, "repos/dantte-lp/gobfd/git/ref/tags/v0.6.4"):
				output = `{"ref":"refs/tags/v0.6.4","object":{"type":"tag","sha":"` + tagSHA + `"}}`
			default:
				target := baseSHA
				if tagDrift {
					target = strings.Repeat("d", 40)
				}
				output = `{"sha":"` + tagSHA + `","tag":"v0.6.4","object":{"type":"commit","sha":"` + target + `"}}`
			}
		case "git":
			if slices.Contains(spec.Args, "add") {
				for _, directory := range []string{root, spec.Args[len(spec.Args)-2]} {
					writeReceiveSource(t, directory)
				}
			}
			if slices.Contains(spec.Args, "remove") {
				if err := os.RemoveAll(spec.Args[len(spec.Args)-1]); err != nil {
					t.Fatal(err)
				}
			}
			if slices.Contains(spec.Args, "show") {
				output = cliReceiveSource
			}
			if slices.Contains(spec.Args, "rev-parse") {
				output = baseSHA + "\n"
				if spec.Dir == root && slices.Contains(spec.Args, "HEAD^{commit}") {
					output = headSHA + "\n"
				}
			}
		case "go":
			switch {
			case slices.Contains(spec.Args, "GOVERSION"):
				output = "go1.27.4\n"
			case slices.Contains(spec.Args, "-race"):
				output = records
			case slices.Contains(spec.Args, "-benchtime=100ms"):
				sampleOrder = append(sampleOrder, spec.Dir)
				output = records
			case slices.Contains(spec.Args, "-format"):
				output = ",pair-baseline.txt,,pair-current.txt,,,\n,sec/op,CI,sec/op,CI,vs base,P\n" +
					"RecvDecodeFSM-4,2e-7,1%,2e-7,1%,~,p=1 n=10\n"
			default:
				output = "paired comparison\n"
			}
		}
		if spec.Stdout != nil {
			_, err := io.WriteString(spec.Stdout, output)
			return err
		}
		return nil
	})
	values := map[string]string{
		"GITHUB_REF_NAME": "v1.0.0-alpha.2", "GITHUB_REPOSITORY": "dantte-lp/gobfd", "RUNNER_TEMP": temporary,
		"GITHUB_SHA": headSHA,
	}
	if err := run(t.Context(), []string{"release-benchmark-comparison"}, dependencies{
		getwd:  func() (string, error) { return root, nil },
		getenv: func(name string) string { return values[name] },
		environ: func() []string {
			return []string{
				"PATH=/fixture", "GOTOOLCHAIN=local", "GH_TOKEN=fixture-read-token", "GITHUB_TOKEN=fixture-other-token",
			}
		},
		specRunner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	if len(sampleOrder) != 20 {
		t.Fatalf("release comparison collected %d samples, want ten balanced base/head pairs", len(sampleOrder))
	}
	base := filepath.Join(temporary, "gobfd-benchmark-base")
	for pair := range 10 {
		want := []string{base, root}
		if pair%2 != 0 {
			slices.Reverse(want)
		}
		if !slices.Equal(sampleOrder[pair*2:pair*2+2], want) {
			t.Fatalf("pair %d order=%q, want %q", pair, sampleOrder[pair*2:pair*2+2], want)
		}
	}
	for _, name := range []string{
		"pair-baseline-raw.txt", "pair-current-raw.txt", "pair-baseline.txt", "pair-current.txt",
	} {
		data, err := os.ReadFile(filepath.Join(root, "reports/benchmarks", name))
		if err != nil || strings.Count(string(data), "BenchmarkRecvDecodeFSM-4") != 10 {
			t.Fatalf("missing ten-sample release evidence %s: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "testdata/benchmarks/baseline.txt"))
	if err != nil || string(data) != legacy {
		t.Fatalf("legacy baseline was modified: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(root, "reports/benchmarks/comparison-provenance.json"))
	var provenance struct {
		BaselineTag    string `json:"baseline_tag"`
		BaselineCommit string `json:"baseline_commit"`
		CurrentCommit  string `json:"current_commit"`
		Samples        int    `json:"samples"`
	}
	if err != nil || json.Unmarshal(data, &provenance) != nil || provenance.BaselineTag != "v0.6.4" ||
		provenance.BaselineCommit != baseSHA || provenance.CurrentCommit != headSHA || provenance.Samples != 10 {
		t.Fatalf("comparison provenance is absent or unbound: %+v: %v", provenance, err)
	}
	tagDrift = true
	if err := run(t.Context(), []string{"release-benchmark-comparison"}, dependencies{
		getwd:      func() (string, error) { return root, nil },
		getenv:     func(name string) string { return values[name] },
		environ:    func() []string { return []string{"GOTOOLCHAIN=local", "GH_TOKEN=fixture-read-token"} },
		specRunner: runner,
	}); err == nil || len(sampleOrder) != 20 {
		t.Fatalf("changed stable tag was not rejected before collection: samples=%d: %v", len(sampleOrder), err)
	}
}

const cliReceiveSource = `package fixture
func BenchmarkRecvDecodeLookupEnqueue(b *testing.B) {
	var pkt bfd.ControlPacket

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		consume(&pkt)
	}
}
`

func writeReceiveSource(t *testing.T, directory string) {
	t.Helper()
	path := filepath.Join(directory, "internal/bfd/bench_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cliReceiveSource), 0o600); err != nil {
		t.Fatal(err)
	}
}
