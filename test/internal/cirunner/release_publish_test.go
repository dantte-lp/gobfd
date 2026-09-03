package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPublishVerifiedReleaseChecksExactStateBeforeSingleMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	releaseView := writeReleasePublishFixture(t, artifactRoot, runnerTemp)
	runner := &releasePublishRunner{
		releaseView:  releaseView,
		aliasDigests: []string{testOCIDigest("1"), testOCIDigest("1"), testOCIDigest("3")},
	}
	environment := []string{
		"GH_TOKEN=secret", "GITHUB_TOKEN=other-secret", "PATH=/usr/bin", "DOCKER_CONFIG=/docker",
	}
	if err := PublishVerifiedRelease(context.Background(), PublishVerifiedReleaseOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: environment, Runner: runner,
	}); err != nil {
		t.Fatalf("PublishVerifiedRelease() error = %v", err)
	}
	if len(runner.calls) != 9 || runner.publications != 1 {
		t.Fatalf("publication calls/publications = %d/%d, want 9/1", len(runner.calls), runner.publications)
	}
	wantPublish := specInvocation{
		name: "gh", args: []string{
			"release", "edit", "v0.6.2", "--repo", "dantte-lp/gobfd", "--draft=false", "--latest",
		}, dir: root, env: environment,
	}
	if got := runner.calls[len(runner.calls)-1]; !reflect.DeepEqual(got, wantPublish) {
		t.Fatalf("final publication call = %#v, want %#v", got, wantPublish)
	}
	dockerEnvironment := []string{"PATH=/usr/bin", "DOCKER_CONFIG=/docker"}
	for _, call := range runner.calls[:3] {
		if call.name != "docker" || !reflect.DeepEqual(call.env, dockerEnvironment) {
			t.Errorf("alias verification call = %#v, want token-free Docker environment", call)
		}
	}
}

func TestPublishVerifiedReleaseRejectsDriftWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, string, string, *releasePublishRunner)
		want   string
	}{
		{name: "git receipt", mutate: func(t *testing.T, _ string, runnerTemp string, _ *releasePublishRunner) {
			t.Helper()
			writeReleaseVerifyFile(t, runnerTemp, "expected-release-branch.txt", []byte("release/v0.7\n"))
		}, want: "branch receipt"},
		{name: "draft body", mutate: func(_ *testing.T, _ string, _ string, runner *releasePublishRunner) {
			runner.releaseView = []byte(strings.Replace(string(runner.releaseView), "release notes", "changed", 1))
		}, want: "draft body"},
		{name: "draft changes during alias checks", mutate: func(_ *testing.T, _ string, _ string, runner *releasePublishRunner) {
			runner.afterAlias = func(int) {
				runner.releaseView = []byte(strings.Replace(string(runner.releaseView), "release notes", "changed", 1))
			}
		}, want: "draft body"},
		{name: "remote asset identity", mutate: func(t *testing.T, _ string, _ string, runner *releasePublishRunner) {
			t.Helper()
			proxy := &releaseVerifyRunner{releaseView: runner.releaseView}
			mutateReleaseDraftAssets(t, proxy, func(assets []map[string]any) {
				assets[0]["state"] = "new"
			})
			runner.releaseView = proxy.releaseView
		}, want: "uploaded"},
		{name: "asset receipt", mutate: func(t *testing.T, _ string, runnerTemp string, _ *releasePublishRunner) {
			t.Helper()
			path := filepath.Join(runnerTemp, releaseAssetIdentityReceiptName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), `"state": "uploaded"`, `"state": "new"`, 1))
			writeReleaseVerifyFile(t, runnerTemp, releaseAssetIdentityReceiptName, data)
		}, want: "receipt state"},
		{name: "OCI alias", mutate: func(_ *testing.T, _ string, _ string, runner *releasePublishRunner) {
			runner.aliasDigests[0] = testOCIDigest("9")
		}, want: "verified OCI index"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			artifactRoot := t.TempDir()
			runnerTemp := t.TempDir()
			runner := &releasePublishRunner{
				releaseView:  writeReleasePublishFixture(t, artifactRoot, runnerTemp),
				aliasDigests: []string{testOCIDigest("1"), testOCIDigest("1"), testOCIDigest("3")},
			}
			test.mutate(t, artifactRoot, runnerTemp, runner)
			err := PublishVerifiedRelease(context.Background(), PublishVerifiedReleaseOptions{
				Root: t.TempDir(), ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
				RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
			})
			if err == nil || !containsError(err, test.want) {
				t.Fatalf("PublishVerifiedRelease() error = %v, want %q", err, test.want)
			}
			if runner.publications != 0 {
				t.Fatalf("drift caused %d publication mutations", runner.publications)
			}
		})
	}
}

func TestParseReleaseAssetIdentityReceiptRejectsHostileJSON(t *testing.T) {
	t.Parallel()

	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	releaseView := writeReleasePublishFixture(t, artifactRoot, runnerTemp)
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	remote, err := validateExactReleaseDraft(
		releaseView, "v0.6.2", "dantte-lp/gobfd", []byte("release notes\n"), assets,
	)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(filepath.Join(runnerTemp, releaseAssetIdentityReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*testing.T, []byte) []byte
	}{
		{name: "duplicate member", mutate: func(_ *testing.T, data []byte) []byte {
			return bytes.Replace(data, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1)
		}},
		{name: "invalid UTF-8", mutate: func(_ *testing.T, data []byte) []byte {
			result := append([]byte(nil), data...)
			result[bytes.Index(result, []byte("uploaded"))] = 0xff
			return result
		}},
		{name: "case alias", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			record := firstReleaseIdentityRecord(root)
			record["Node_Id"] = record["node_id"]
			delete(record, "node_id")
		})},
		{name: "null assets", mutate: mutateReleaseIdentityJSON(func(root map[string]any) { root["assets"] = nil })},
		{name: "fractional REST ID", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			firstReleaseIdentityRecord(root)["database_id"] = 1.5
		})},
		{name: "overflow REST ID", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			firstReleaseIdentityRecord(root)["database_id"] = json.Number("18446744073709551616")
		})},
		{name: "negative size", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			firstReleaseIdentityRecord(root)["size"] = -1
		})},
		{name: "extra field", mutate: mutateReleaseIdentityJSON(func(root map[string]any) { root["extra"] = true })},
		{name: "missing field", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			delete(firstReleaseIdentityRecord(root), "state")
		})},
		{name: "record reorder", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			records := root["assets"].([]any)
			records[0], records[1] = records[1], records[0]
		})},
		{name: "duplicate REST ID", mutate: mutateReleaseIdentityJSON(func(root map[string]any) {
			records := root["assets"].([]any)
			records[1].(map[string]any)["database_id"] = records[0].(map[string]any)["database_id"]
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseReleaseAssetIdentityReceipt(test.mutate(t, valid), "v0.6.2", assets, remote); err == nil {
				t.Fatal("parseReleaseAssetIdentityReceipt() error = nil, want hostile JSON rejection")
			}
		})
	}
}

func mutateReleaseIdentityJSON(mutate func(map[string]any)) func(*testing.T, []byte) []byte {
	return func(t *testing.T, data []byte) []byte {
		t.Helper()
		root := map[string]any{}
		if err := json.Unmarshal(data, &root); err != nil {
			t.Fatal(err)
		}
		mutate(root)
		result, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
}

func firstReleaseIdentityRecord(root map[string]any) map[string]any {
	return root["assets"].([]any)[0].(map[string]any)
}

func writeReleasePublishFixture(t *testing.T, artifactRoot, runnerTemp string) []byte {
	t.Helper()
	assets := expectedReleaseAssetNames("0.6.2", "v0.6.2")
	writeReleaseVerifyFixture(t, artifactRoot, runnerTemp, assets)
	releaseView := marshalReleaseDraftView(t, "release notes", assets)
	remote, err := validateExactReleaseDraft(
		releaseView, "v0.6.2", "dantte-lp/gobfd", []byte("release notes\n"), assets,
	)
	if err != nil {
		t.Fatal(err)
	}
	dataByName := validReleaseAssetData(t)
	local := make(map[string]releaseAssetSnapshot, len(dataByName))
	for name, data := range dataByName {
		digest := sha256.Sum256(data)
		local[name] = releaseAssetSnapshot{Size: int64(len(data)), Digest: fmt.Sprintf("sha256:%x", digest)}
	}
	receipt, err := renderReleaseAssetIdentityReceipt("v0.6.2", remote, local)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseVerifyFile(t, runnerTemp, releaseAssetIdentityReceiptName, receipt)
	return releaseView
}

type releasePublishRunner struct {
	releaseView  []byte
	aliasDigests []string
	aliasIndex   int
	afterAlias   func(int)
	publications int
	calls        []specInvocation
}

func (runner *releasePublishRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir,
		env: append([]string(nil), spec.Env...),
	})
	var data []byte
	switch {
	case spec.Name == "git" && reflect.DeepEqual(spec.Args, []string{"rev-parse", "HEAD"}):
		data = []byte(preflightCommit + "\n")
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/tags/v0.6.2"}):
		data = fmt.Appendf(nil, `{"ref":"refs/tags/v0.6.2","object":{"type":"tag","sha":%q}}`, preflightTagObject)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/tags/" + preflightTagObject}):
		data = fmt.Appendf(nil,
			`{"sha":%q,"tag":"v0.6.2","object":{"type":"commit","sha":%q}}`, preflightTagObject, preflightCommit,
		)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/heads/release/v0.6"}):
		data = fmt.Appendf(nil, `{"ref":"refs/heads/release/v0.6","object":{"type":"commit","sha":%q}}`, preflightCommit)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{
		"release", "view", "v0.6.2", "--repo", "dantte-lp/gobfd", "--json", "isDraft,tagName,body,assets",
	}):
		data = runner.releaseView
	case spec.Name == "docker" && runner.aliasIndex < len(runner.aliasDigests):
		index := validOCIManifestIndex("alias")
		index.Digest = runner.aliasDigests[runner.aliasIndex]
		var err error
		data, err = json.Marshal(index)
		if err != nil {
			return err
		}
		runner.aliasIndex++
		if runner.afterAlias != nil {
			runner.afterAlias(runner.aliasIndex)
		}
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{
		"release", "edit", "v0.6.2", "--repo", "dantte-lp/gobfd", "--draft=false", "--latest",
	}):
		runner.publications++
		return nil
	default:
		return fmt.Errorf("unexpected release publication command: %s %q", spec.Name, spec.Args)
	}
	if spec.Stdout == nil {
		return fmt.Errorf("release publication command lacks captured stdout: %s %q", spec.Name, spec.Args)
	}
	_, err := io.Writer(spec.Stdout).Write(data)
	return err
}
