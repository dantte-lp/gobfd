package cirunner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReleaseNotesAcceptsExplicitAlphaAndRetainsPullRequestLinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changelog := strings.Replace(validReleaseNotesChangelog, "[0.6.5]", "[1.0.0-alpha.1]", 1)
	changelog = strings.Replace(
		changelog, "- Current fix.", "- Current fix. ([#123](https://github.com/dantte-lp/gobfd/pull/123))", 1,
	)
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte(changelog), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ReleaseNotes(context.Background(), ReleaseNotesOptions{
		Root: root, RefName: "v1.0.0-alpha.1", Repository: "dantte-lp/gobfd",
		Runner: &releaseNotesRunner{response: validReleaseNotesResponse},
	})
	if err != nil {
		t.Fatalf("ReleaseNotes(alpha) error = %v", err)
	}
	notes, err := os.ReadFile(filepath.Join(root, "release-notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"https://github.com/dantte-lp/gobfd/pull/123",
		"https://github.com/dantte-lp/gobfd/blob/v1.0.0-alpha.1/CHANGELOG.md",
		"https://github.com/dantte-lp/gobfd/compare/v0.6.4...v1.0.0-alpha.1",
	} {
		if !strings.Contains(string(notes), fragment) {
			t.Errorf("alpha release notes lack %q", fragment)
		}
	}
}

func TestReleaseDraftRequiresExactPrereleaseState(t *testing.T) {
	t.Parallel()

	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	valid := marshalReleaseDraftView(t, "release notes", assets)
	for _, test := range []struct {
		name       string
		tag        string
		prerelease any
		omit       bool
		wantError  bool
	}{
		{name: "stable", tag: "v0.6.2", prerelease: false},
		{name: "alpha", tag: "v1.0.0-alpha.1", prerelease: true},
		{name: "stable flagged prerelease", tag: "v0.6.2", prerelease: true, wantError: true},
		{name: "alpha flagged stable", tag: "v1.0.0-alpha.1", prerelease: false, wantError: true},
		{name: "missing state", tag: "v1.0.0-alpha.1", omit: true, wantError: true},
		{name: "null state", tag: "v1.0.0-alpha.1", prerelease: nil, wantError: true},
		{name: "string state", tag: "v1.0.0-alpha.1", prerelease: "true", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var document map[string]any
			if err := json.Unmarshal(valid, &document); err != nil {
				t.Fatal(err)
			}
			document["tagName"] = test.tag
			if test.omit {
				delete(document, "isPrerelease")
			} else {
				document["isPrerelease"] = test.prerelease
			}
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = validateExactReleaseDraft(data, test.tag, "dantte-lp/gobfd", []byte("release notes"), assets)
			if (err != nil) != test.wantError {
				t.Errorf("validateExactReleaseDraft() error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestStableReleaseParserAndAliasPromotionRejectAlpha(t *testing.T) {
	t.Parallel()

	if _, _, err := parseStableReleaseVersion("v1.0.0-alpha.1"); err == nil {
		t.Fatal("stable release parser accepted alpha")
	}
	runner := &releasePublishRunner{}
	err := PromoteReleaseOCIAliases(context.Background(), PromoteReleaseOCIAliasesOptions{
		Root: t.TempDir(), ArtifactRoot: t.TempDir(), RunnerTemp: t.TempDir(),
		RefName: "v1.0.0-alpha.1", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || len(runner.calls) != 0 {
		t.Fatalf(
			"alpha alias promotion error = %v, remote calls = %d; want rejection before remote calls",
			err, len(runner.calls),
		)
	}
}

func TestReleaseVersionAcceptsOnlyStableOrApprovedAlpha(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"v0.6.2", "v1.0.0", "v1.0.0-alpha.1", "v1.0.0-alpha.20"} {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			version, branch, err := parseReleaseVersion(tag)
			wantBranch := "release/v1.0"
			if tag == "v0.6.2" {
				wantBranch = "release/v0.6"
			}
			if err != nil || version != strings.TrimPrefix(tag, "v") || branch != wantBranch {
				t.Fatalf("parseReleaseVersion(%q) = %q, %q, %v", tag, version, branch, err)
			}
		})
	}
	for _, tag := range []string{
		"v1.0.0-alpha.0", "v1.0.0-alpha.01", "v1.0.0-alpha.", "v1.0.0-alpha.-1",
		"v1.0.0-alpha.1+build", "v1.0.0-alpha.1.1", "v1.0.0-alpha.1\n",
		"v1.0.0-alpha.1/child", "v1.0.0-beta.1", "v1.0.0-rc.1", "v1.1.0-alpha.1",
		"v0.6.5-alpha.1", "1.0.0-alpha.1", "v01.0.0", "v1.0.0-alpha." + strings.Repeat("1", 128),
	} {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			if _, _, err := parseReleaseVersion(tag); err == nil {
				t.Fatalf("parseReleaseVersion(%q) accepted an unauthorized or noncanonical tag", tag)
			}
		})
	}
}

func TestAlphaReleaseNotesExcludeFutureStableBaseline(t *testing.T) {
	t.Parallel()

	pages := `[[
  {"draft":false,"prerelease":false,"tag_name":"v1.0.0"},
  {"draft":false,"prerelease":false,"tag_name":"v0.6.4"}
]]`
	tag, err := selectPreviousRelease([]byte(pages), "v1.0.0-alpha.1")
	if err != nil || tag != "v0.6.4" {
		t.Fatalf("alpha baseline = %q, %v; want previous stable v0.6.4", tag, err)
	}
}

func TestPublishVerifiedAlphaPreservesStableAliasesAndLatest(t *testing.T) {
	t.Parallel()

	root, artifactRoot, runnerTemp := t.TempDir(), t.TempDir(), t.TempDir()
	runner := &alphaPublishRunner{releaseView: writeAlphaPublishFixture(t, artifactRoot, runnerTemp)}
	err := PublishVerifiedRelease(context.Background(), PublishVerifiedReleaseOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v1.0.0-alpha.1", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err != nil {
		t.Fatalf("PublishVerifiedRelease(alpha) error = %v", err)
	}
	if runner.publications != 1 || runner.manifests != releaseOCIImageCount {
		t.Fatalf(
			"publications/manifests = %d/%d, want 1/%d", runner.publications, runner.manifests, releaseOCIImageCount,
		)
	}
	want := []string{
		"release", "edit", "v1.0.0-alpha.1", "--repo", "dantte-lp/gobfd",
		"--draft=false", "--prerelease", "--latest=false",
	}
	if !slices.Equal(runner.lastMutation, want) {
		t.Errorf("alpha publication = %q, want %q", runner.lastMutation, want)
	}
}

func TestPublishVerifiedAlphaRejectsVersionedOCIDrift(t *testing.T) {
	t.Parallel()

	artifactRoot, runnerTemp := t.TempDir(), t.TempDir()
	runner := &alphaPublishRunner{
		releaseView: writeAlphaPublishFixture(t, artifactRoot, runnerTemp), manifestDrift: true,
	}
	err := PublishVerifiedRelease(context.Background(), PublishVerifiedReleaseOptions{
		Root: t.TempDir(), ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v1.0.0-alpha.1", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || runner.publications != 0 || runner.manifests == 0 {
		t.Fatalf(
			"OCI drift error = %v, publications/manifests = %d/%d; want checked drift without publication",
			err, runner.publications, runner.manifests,
		)
	}
}

func writeAlphaPublishFixture(t *testing.T, artifactRoot, runnerTemp string) []byte {
	t.Helper()
	const tag = "v1.0.0-alpha.1"
	const version = "1.0.0-alpha.1"
	assets := expectedReleaseAssetNames(version, tag)
	for name, data := range map[string][]byte{
		"expected-release-commit.txt":     []byte(preflightCommit + "\n"),
		"expected-release-branch.txt":     []byte("release/v1.0\n"),
		"expected-release-tag-object.txt": []byte(preflightTagObject + "\n"),
		"expected-release-assets.txt":     renderArtifactNames(assets),
	} {
		writeReleaseVerifyFile(t, runnerTemp, name, data)
	}
	digests := validReleaseDigestReceipt(version)
	writeReleaseVerifyFile(t, artifactRoot, "release-image-digests.txt", digests)
	writeReleaseVerifyFile(t, artifactRoot, "release-notes.md", []byte("alpha release notes\n"))
	local := make(map[string]releaseAssetSnapshot, len(assets))
	remote := make([]map[string]any, 0, len(assets))
	for index, name := range assets {
		data := []byte("verified fixture asset: " + name)
		if name == "release-image-digests.txt" {
			data = digests
		}
		digest := sha256.Sum256(data)
		local[name] = releaseAssetSnapshot{Size: int64(len(data)), Digest: fmt.Sprintf("sha256:%x", digest)}
		remote = append(remote, map[string]any{
			"apiUrl": fmt.Sprintf("https://api.github.com/repos/dantte-lp/gobfd/releases/assets/%d", 1000+index),
			"id":     fmt.Sprintf("RA_test_%02d", index), "name": name, "size": len(data),
			"digest": local[name].Digest, "state": "uploaded",
		})
	}
	view, err := json.Marshal(map[string]any{
		"isDraft": true, "isPrerelease": true, "tagName": tag, "body": "alpha release notes", "assets": remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := validateExactReleaseDraft(view, tag, "dantte-lp/gobfd", []byte("alpha release notes"), assets)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := renderReleaseAssetIdentityReceipt(tag, identities, local)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseVerifyFile(t, runnerTemp, releaseAssetIdentityReceiptName, receipt)
	return view
}

type alphaPublishRunner struct {
	releaseView   []byte
	manifestDrift bool
	manifests     int
	publications  int
	lastMutation  []string
}

func (runner *alphaPublishRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	var data []byte
	switch {
	case spec.Name == "git" && slices.Equal(spec.Args, []string{"rev-parse", "HEAD"}):
		data = []byte(preflightCommit + "\n")
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{
		"api", "repos/dantte-lp/gobfd/git/ref/tags/v1.0.0-alpha.1",
	}):
		data = fmt.Appendf(
			nil, `{"ref":"refs/tags/v1.0.0-alpha.1","object":{"type":"tag","sha":%q}}`, preflightTagObject,
		)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{
		"api", "repos/dantte-lp/gobfd/git/tags/" + preflightTagObject,
	}):
		data = fmt.Appendf(
			nil, `{"sha":%q,"tag":"v1.0.0-alpha.1","object":{"type":"commit","sha":%q}}`,
			preflightTagObject, preflightCommit,
		)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{
		"api", "repos/dantte-lp/gobfd/git/ref/heads/release/v1.0",
	}):
		data = fmt.Appendf(
			nil, `{"ref":"refs/heads/release/v1.0","object":{"type":"commit","sha":%q}}`, preflightCommit,
		)
	case spec.Name == "gh" && len(spec.Args) > 1 && spec.Args[0] == "release" && spec.Args[1] == "view":
		data = runner.releaseView
	case spec.Name == "docker" && len(spec.Args) > 0:
		image := spec.Args[len(spec.Args)-1]
		const imagePrefix = "ghcr.io/dantte-lp/gobfd:1.0.0-alpha.1"
		if image != imagePrefix && image != imagePrefix+"-debian-trixie" {
			return fmt.Errorf("alpha release accessed stable alias: %q", spec.Args)
		}
		runner.manifests++
		index := validOCIManifestIndex("1")
		if runner.manifestDrift {
			index.Digest = testOCIDigest("9")
		}
		var err error
		data, err = json.Marshal(index)
		if err != nil {
			return err
		}
	case spec.Name == "gh" && len(spec.Args) > 1 && spec.Args[0] == "release" && spec.Args[1] == "edit":
		runner.publications++
		runner.lastMutation = append([]string(nil), spec.Args...)
		return nil
	default:
		return fmt.Errorf("unexpected alpha publication command: %s %q", spec.Name, spec.Args)
	}
	if spec.Stdout == nil {
		return fmt.Errorf("alpha publication command lacks captured stdout: %s %q", spec.Name, spec.Args)
	}
	_, err := spec.Stdout.Write(data)
	return err
}
