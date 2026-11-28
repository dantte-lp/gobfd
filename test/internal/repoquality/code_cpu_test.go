package repoquality

import (
	"errors"
	"testing"
)

func TestCodeCPUAcceptsPartialInlineMarker(t *testing.T) {
	t.Parallel()
	const profile = "Type: cpu\nflat flat% sum% cum cum%\n" +
		"0 0% 100% 120000000ns 2.54% sync.(*RWMutex).RUnlock (partial-inline)\n"
	costs, err := parseCodeCPU([]byte(profile))
	if err != nil {
		t.Fatal(err)
	}
	cost, found := costs["sync.(*RWMutex).RUnlock"]
	if !found || len(costs) != 1 || cost.FlatNS != 0 || cost.CumulativeNS != 120_000_000 {
		t.Fatalf("partial inline CPU row: %+v", costs)
	}
}

func TestCodeCPURequiresMatchingSourceAndPreservesUnknownFunctions(t *testing.T) {
	t.Parallel()
	report := CodeReport{
		SourceSHA256: "source", Files: []CodeFile{{Path: "internal/a/a.go", Package: "internal/a:a"}},
		Functions: []CodeFunction{
			{Path: "internal/a/a.go", Name: "Run", Kind: "declaration", Runtime: "not_observed"},
			{Path: "internal/a/a.go", Name: "Other", Kind: "declaration", Runtime: "not_observed"},
		},
	}
	const profile = "File: example\nType: cpu\n      flat flat% sum% cum cum%\n" +
		"10ns 100% 100% 20ns 100% github.com/dantte-lp/gobfd/internal/a.Run\n"
	if err := AttachCodeCPU(&report, []byte(profile), "wrong"); err == nil {
		t.Fatal("profile from another source was accepted")
	}
	if err := AttachCodeCPU(&report, []byte(profile), "source"); err != nil {
		t.Fatal(err)
	}
	if report.Functions[0].CPU == nil || report.Functions[0].CPU.FlatNS != 10 ||
		report.Functions[1].CPU != nil || report.Functions[1].Runtime != "not_observed" {
		t.Fatalf("profile invented a zero duration or lost observed CPU: %+v", report.Functions)
	}
}

func TestCodeCPURejectsInvalidTables(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"", "Type: cpu\n", "Type: cpu\nflat flat% sum% cum cum%\nNaN 0% 0% 0 0% bad\n",
		"Type: cpu\nflat flat% sum% cum cum%\n10ns BAD WRONG 20ns BAD github.com/dantte-lp/gobfd/internal/a.Run\n",
		"Type: cpu\nflat flat% sum% cum cum%\n10ns NaN% 10% 20ns 10% github.com/dantte-lp/gobfd/internal/a.Run\n",
		"Type: cpu\nflat flat% sum% cum cum%\n0 0% 100% 20ns 10% sync.(*RWMutex).RUnlock (unknown)\n",
		"Type: cpu\nflat flat% sum% cum cum%\n0 0% 100% 20ns 10% sync.(*RWMutex).RUnlock (inline) (partial-inline)\n",
		"Type: cpu\nflat flat% sum% cum cum%\n0 0% 100% 20ns 10% pkg.F[go.shape.int] (unknown)\n",
		"Type: cpu\nflat flat% sum% cum cum%\n0 0% 100% 20ns 10% pkg.F[go.shape.int] (inline) (partial-inline)\n",
		"Type: cpu\nflat flat% sum% cum cum%\n0 0% 100% 20ns 10% pkg.F[go.shape.int\n",
		"Type: cpu\nflat flat% sum% cum cum%\n0 0% 100% 20ns 10% pkg.F]go.shape.int[\n",
	} {
		if _, err := parseCodeCPU([]byte(input)); err == nil {
			t.Fatalf("invalid CPU table accepted: %q", input)
		}
	}
}

func TestCodeCPUAcceptsGenericSymbolWithSpaces(t *testing.T) {
	t.Parallel()
	const symbol = "pkg.F[go.shape.struct { X []int }]"
	const profile = "Type: cpu\nflat flat% sum% cum cum%\n" +
		"10ns 100% 100% 20ns 100% " + symbol + " (partial-inline)\n"
	costs, err := parseCodeCPU([]byte(profile))
	if err != nil {
		t.Fatal(err)
	}
	if cost, found := costs[symbol]; !found || len(costs) != 1 || cost.FlatNS != 10 || cost.CumulativeNS != 20 {
		t.Fatalf("generic CPU row: %+v", costs)
	}
}

func TestCodeCPURejectsAmbiguousBuildVariants(t *testing.T) {
	t.Parallel()
	report := CodeReport{
		SourceSHA256: "source",
		Files: []CodeFile{
			{Path: "internal/a/a_linux.go", Package: "internal/a:a"},
			{Path: "internal/a/a_other.go", Package: "internal/a:a"},
		},
		Functions: []CodeFunction{
			{Path: "internal/a/a_linux.go", Name: "Run", Kind: "declaration"},
			{Path: "internal/a/a_other.go", Name: "Run", Kind: "declaration"},
		},
	}
	const profile = "Type: cpu\nflat flat% sum% cum cum%\n" +
		"10ns 50% 50% 20ns 100% github.com/dantte-lp/gobfd/internal/a.Run\n"
	if err := AttachCodeCPU(&report, []byte(profile), "source"); !errors.Is(err, errCodeCPUAmbiguous) {
		t.Fatalf("one build symbol was attributed to incompatible source variants: %v", err)
	}
	for _, fn := range report.Functions {
		if fn.CPU != nil {
			t.Fatal("failed CPU attachment modified a function")
		}
	}
}
