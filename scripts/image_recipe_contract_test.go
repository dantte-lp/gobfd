package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnosticImagesUseExecBuildSteps(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"../deployments/docker/Containerfile.debug",
		"../deployments/docker/Containerfile.dev",
		"../test/interop-clab/Containerfile.gobfd",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			data := strings.ReplaceAll(readContractFile(t, path), "\\\n", " ")
			steps := 0
			for line := range strings.Lines(data) {
				body, ok := strings.CutPrefix(strings.TrimSpace(line), "RUN ")
				if !ok {
					continue
				}
				steps++
				for strings.HasPrefix(body, "--mount=") {
					_, body, _ = strings.Cut(body, " ")
					body = strings.TrimSpace(body)
				}
				var arguments []string
				if err := json.Unmarshal([]byte(body), &arguments); err != nil || len(arguments) == 0 {
					t.Errorf("build step requires a shell: %s", line)
				}
			}
			if steps == 0 {
				t.Fatal("no build steps checked")
			}
		})
	}
}
