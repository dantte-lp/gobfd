package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPrepareOutputDirectory(t *testing.T) {
	tests := []struct {
		name        string
		output      func(root string) string
		wantOutside bool
	}{
		{
			name:   "relative path inside root",
			output: func(string) string { return filepath.Join("reports", "benchmarks", "report.html") },
		},
		{
			name:   "absolute path inside root",
			output: func(root string) string { return filepath.Join(root, "reports", "report.html") },
		},
		{
			name:        "path outside root",
			output:      func(root string) string { return filepath.Join(root, "..", "report.html") },
			wantOutside: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)

			err := prepareOutputDirectory(tt.output(root), "")
			if tt.wantOutside {
				if !errors.Is(err, errOutputOutsideRoot) {
					t.Fatalf("prepareOutputDirectory() error = %v, want %v", err, errOutputOutsideRoot)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepareOutputDirectory() error = %v", err)
			}
		})
	}
}
