//go:build dependencyinventory_generate

// Command dependencyinventory renders the deterministic offline inventory.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/dependencyinventory"
)

func main() {
	root := flag.String("root", ".", "repository root")
	output := flag.String("output", "", "write inventory to this path instead of stdout")
	flag.Parse()

	inventory, err := dependencyinventory.Build(context.Background(), *root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build dependency inventory: %v\n", err)
		os.Exit(1)
	}
	if err := dependencyinventory.CollectLicenseEvidence(context.Background(), &inventory); err != nil {
		fmt.Fprintf(os.Stderr, "collect dependency license evidence: %v\n", err)
		os.Exit(1)
	}
	var rendered bytes.Buffer
	encoder := json.NewEncoder(&rendered)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(inventory); err != nil {
		fmt.Fprintf(os.Stderr, "encode dependency inventory: %v\n", err)
		os.Exit(1)
	}
	if *output != "" {
		//nolint:gosec // Generated tracked documentation is intentionally repository-readable.
		if err := os.WriteFile(*output, rendered.Bytes(), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write dependency inventory: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if _, err := os.Stdout.Write(rendered.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "write dependency inventory stdout: %v\n", err)
		os.Exit(1)
	}
}
