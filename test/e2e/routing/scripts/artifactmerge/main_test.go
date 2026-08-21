package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMergesFixedRoutingSuites(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	output := filepath.Join(root, "containers.json")
	base := filepath.Join(root, "base.json")
	bgp := filepath.Join(root, "bgp.json")
	writeCLIArtifactFixture(t, base, `[{"Id":"base"}]`+"\n")
	writeCLIArtifactFixture(t, bgp, `[{"Id":"bgp"}]`+"\n")

	if err := run([]string{"merge", output, base, bgp}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run artifact merge CLI: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read CLI output: %v", err)
	}
	var merged struct {
		Suites map[string][]json.RawMessage `json:"suites"`
	}
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("decode CLI output: %v", err)
	}
	if len(merged.Suites["interop"]) != 1 || len(merged.Suites["interop-bgp"]) != 1 {
		t.Fatalf("merged suite sizes = %#v, want one item each", merged.Suites)
	}
}

func TestRunRejectsWrongArgumentCount(t *testing.T) {
	t.Parallel()

	err := run([]string{"merge", "output", "base"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("argument error = %v, want usage diagnostic", err)
	}
}

func TestRunReadsAndWritesImageIDArtifact(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tshark-image-id")
	const imageID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := run([]string{"write-image-id", path, imageID}, &bytes.Buffer{}); err != nil {
		t.Fatalf("write image ID through CLI: %v", err)
	}
	var stdout bytes.Buffer
	if err := run([]string{"read-image-id", path}, &stdout); err != nil {
		t.Fatalf("read image ID through CLI: %v", err)
	}
	if stdout.String() != imageID+"\n" {
		t.Fatalf("image ID stdout = %q, want exact ID", stdout.String())
	}
}

func writeCLIArtifactFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write CLI fixture %s: %v", path, err)
	}
}
