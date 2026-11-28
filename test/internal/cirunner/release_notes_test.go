package cirunner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseNotesSelectsLatestPublishedSameLineAndRendersExactly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changelog := `# Changelog

## [0.6.5] - 2026-09-03

### Fixed

- Current fix.

## [0.6.4] - 2026-08-27

### Fixed

- Previous fix.
`
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}
	releases := `[[
  {"draft":false,"prerelease":false,"tag_name":"v0.6.2"},
  {"draft":false,"prerelease":false,"tag_name":"nightly"},
  {"draft":true,"prerelease":false,"tag_name":"v0.6.99"},
  {"draft":false,"prerelease":true,"tag_name":"v0.6.98"}
],[
  {"draft":false,"prerelease":false,"tag_name":"v1.0.0"},
  {"draft":false,"prerelease":false,"tag_name":"v0.6.4"},
  {"draft":false,"prerelease":false,"tag_name":"v0.6.5"}
]]`
	runner := &releaseNotesRunner{response: releases}
	var console bytes.Buffer
	if err := ReleaseNotes(context.Background(), ReleaseNotesOptions{
		Root: root, RefName: "v0.6.5", Repository: "dantte-lp/gobfd", Output: &console, Runner: runner,
	}); err != nil {
		t.Fatalf("ReleaseNotes() error = %v", err)
	}
	wantCall := []specInvocation{{
		name: "gh", args: []string{
			"api", "--paginate", "repos/dantte-lp/gobfd/releases?per_page=100", "--slurp",
		}, dir: root,
	}}
	if !reflect.DeepEqual(runner.calls, wantCall) {
		t.Errorf("release notes calls = %#v, want %#v", runner.calls, wantCall)
	}
	want := "## GoBFD v0.6.5\n\n" +
		"## [0.6.5] - 2026-09-03\n\n### Fixed\n\n- Current fix.\n\n\n" +
		"**Full changelog:** [CHANGELOG.md at v0.6.5](https://github.com/dantte-lp/gobfd/blob/v0.6.5/CHANGELOG.md)\n\n" +
		"**Changes since v0.6.4:** [compare v0.6.4...v0.6.5](https://github.com/dantte-lp/gobfd/compare/v0.6.4...v0.6.5)\n"
	path := filepath.Join(root, "release-notes.md")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Errorf("release-notes.md = %q, %v; want %q", data, err, want)
	}
	assertExactMode(t, path, 0o644)
	if got := console.String(); got != "--- Release notes for 0.6.5 ---\n"+want {
		t.Errorf("release notes console output = %q", got)
	}
}

func TestSelectPreviousReleaseFallsBackToHighestCanonicalPublishedVersion(t *testing.T) {
	t.Parallel()

	pages := `[[
  {"draft":false,"prerelease":false,"tag_name":"v1.9.9"},
  {"draft":false,"prerelease":false,"tag_name":"v0.10.0"},
  {"draft":false,"prerelease":false,"tag_name":"v0.9.99"}
]]`
	previous, err := selectPreviousRelease([]byte(pages), "v2.0.0")
	if err != nil {
		t.Fatalf("selectPreviousRelease() error = %v", err)
	}
	if previous != "v1.9.9" {
		t.Errorf("previous release = %q, want v1.9.9", previous)
	}
}

func TestReleaseNotesRejectsMalformedReleasesAndIncompleteChangelog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		releases  string
		changelog string
	}{
		{name: "null page", releases: `[null]`, changelog: validReleaseNotesChangelog},
		{
			name: "missing published tag", releases: `[[{"draft":false,"prerelease":false}]]`,
			changelog: validReleaseNotesChangelog,
		},
		{
			name: "impossible date", releases: validReleaseNotesResponse,
			changelog: strings.Replace(validReleaseNotesChangelog, "2026-09-03", "2026-99-99", 1),
		},
		{
			name: "missing category", releases: validReleaseNotesResponse,
			changelog: strings.Replace(validReleaseNotesChangelog, "### Fixed\n\n", "", 1),
		},
		{
			name: "missing entry", releases: validReleaseNotesResponse,
			changelog: strings.Replace(validReleaseNotesChangelog, "- Current fix.\n", "Current fix.\n", 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte(test.changelog), 0o644); err != nil {
				t.Fatal(err)
			}
			runner := &releaseNotesRunner{response: test.releases}
			if err := ReleaseNotes(context.Background(), ReleaseNotesOptions{
				Root: root, RefName: "v0.6.5", Repository: "dantte-lp/gobfd", Runner: runner,
			}); err == nil {
				t.Fatal("ReleaseNotes() error = nil, want fail-closed rejection")
			}
		})
	}
}

const validReleaseNotesResponse = `[[{"draft":false,"prerelease":false,"tag_name":"v0.6.4"}]]`

const validReleaseNotesChangelog = `# Changelog

## [0.6.5] - 2026-09-03

### Fixed

- Current fix.

## [0.6.4] - 2026-08-27

### Fixed

- Previous fix.
`

type releaseNotesRunner struct {
	response string
	calls    []specInvocation
	err      error
}

func (runner *releaseNotesRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir, env: append([]string(nil), spec.Env...),
	})
	if runner.err != nil {
		return runner.err
	}
	if spec.Stdout == nil {
		return errors.New("release notes command lacks captured stdout")
	}
	_, err := io.WriteString(spec.Stdout, runner.response)
	return err
}
