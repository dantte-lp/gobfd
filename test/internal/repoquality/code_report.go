package repoquality

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
)

// WriteCodeReport preserves output errors instead of publishing a partial success.
func WriteCodeReport(writer io.Writer, report CodeReport) error {
	if err := json.NewEncoder(writer).Encode(report); err != nil {
		return fmt.Errorf("write code inventory JSON: %w", err)
	}
	return nil
}

// CodeDependencyGraph renders the union of first-party source import edges.
// Build-incompatible variants are included; this is not a selected-build cycle check.
func CodeDependencyGraph(report CodeReport) string {
	const module = "github.com/dantte-lp/gobfd/"
	packages := make(map[string]bool)
	destinations := make(map[string]string)
	for _, file := range report.Files {
		packages[file.Package] = true
		if destinations[path.Dir(file.Path)] == "" || !file.Test {
			destinations[path.Dir(file.Path)] = file.Package
		}
	}
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	slices.Sort(names)
	nodes := make(map[string]int)
	var graph strings.Builder
	graph.WriteString("flowchart LR\n")
	for index, name := range names {
		nodes[name] = index
		fmt.Fprintf(&graph, "    n%d[%s]\n", index, strconv.Quote(name))
	}
	edges := make(map[string]bool)
	for _, dependency := range report.Imports {
		if local, ok := strings.CutPrefix(dependency.To, module); ok && destinations[local] != "" {
			edges[fmt.Sprintf("    n%d --> n%d\n", nodes[dependency.From], nodes[destinations[local]])] = true
		}
	}
	ordered := make([]string, 0, len(edges))
	for edge := range edges {
		ordered = append(ordered, edge)
	}
	slices.Sort(ordered)
	for _, edge := range ordered {
		graph.WriteString(edge)
	}
	return graph.String()
}
