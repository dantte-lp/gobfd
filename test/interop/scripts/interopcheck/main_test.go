package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args  []string
		input string
		want  string
	}{
		"gobgp": {
			args:  []string{"gobgp-neighbor-state", "172.21.0.20"},
			input: `[{"state":{"neighbor_address":"172.21.0.20","session_state":6}}]`,
			want:  "established\n",
		},
		"frr": {
			args:  []string{"frr-bfd-peer-status", "172.20.0.10"},
			input: `[{"peer":"172.20.0.10","status":"up"}]`,
			want:  "up\n",
		},
		"detection gap": {
			args:  []string{"detection-gap", "12", "3"},
			input: "10\n11.25\n12.5\n",
			want:  "pass\t0.750\n",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			var errorOutput bytes.Buffer
			if status := run(tt.args, strings.NewReader(tt.input), &output, &errorOutput); status != 0 {
				t.Fatalf("run() status=%d stderr=%q", status, errorOutput.String())
			}
			if output.String() != tt.want {
				t.Fatalf("run() output=%q, want %q", output.String(), tt.want)
			}
		})
	}
}
