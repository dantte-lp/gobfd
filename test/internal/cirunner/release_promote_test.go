package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestPromoteReleaseOCIAliasesUsesPinnedDigestsAndVerifiesAliases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	writeReleasePromotionFixture(t, artifactRoot, runnerTemp)
	primaryDigest := testOCIDigest("1")
	oracleDigest := testOCIDigest("3")
	runner := &releasePromotionRunner{aliasDigests: []string{
		testOCIDigest("4"), testOCIDigest("4"), testOCIDigest("5"),
		primaryDigest, primaryDigest, oracleDigest,
	}}
	environment := []string{
		"GH_TOKEN=secret", "GITHUB_TOKEN=other-secret", "PATH=/usr/bin", "DOCKER_CONFIG=/docker",
	}
	if err := PromoteReleaseOCIAliases(context.Background(), PromoteReleaseOCIAliasesOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd",
		Environment: environment, Runner: runner,
	}); err != nil {
		t.Fatalf("PromoteReleaseOCIAliases() error = %v", err)
	}

	dockerEnvironment := []string{"PATH=/usr/bin", "DOCKER_CONFIG=/docker"}
	wantDocker := []specInvocation{
		{name: "docker", args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}",
			"ghcr.io/dantte-lp/gobfd:latest",
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}",
			"ghcr.io/dantte-lp/gobfd:debian-trixie",
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}",
			"ghcr.io/dantte-lp/gobfd:oraclelinux10",
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "create",
			"--tag", "ghcr.io/dantte-lp/gobfd:latest",
			"--tag", "ghcr.io/dantte-lp/gobfd:debian-trixie",
			"ghcr.io/dantte-lp/gobfd@" + primaryDigest,
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "create",
			"--tag", "ghcr.io/dantte-lp/gobfd:oraclelinux10",
			"ghcr.io/dantte-lp/gobfd@" + oracleDigest,
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}",
			"ghcr.io/dantte-lp/gobfd:latest",
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}",
			"ghcr.io/dantte-lp/gobfd:debian-trixie",
		}, dir: root, env: dockerEnvironment},
		{name: "docker", args: []string{
			"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}",
			"ghcr.io/dantte-lp/gobfd:oraclelinux10",
		}, dir: root, env: dockerEnvironment},
	}
	if len(runner.calls) != 12 || !reflect.DeepEqual(runner.calls[4:], wantDocker) {
		t.Fatalf("promotion calls = %#v, want four identity calls then %#v", runner.calls, wantDocker)
	}
	if !reflect.DeepEqual(runner.calls[0].env, dockerEnvironment) {
		t.Errorf("git identity environment = %q, want %q", runner.calls[0].env, dockerEnvironment)
	}
	for _, call := range runner.calls[1:4] {
		if !reflect.DeepEqual(call.env, environment) {
			t.Errorf("GitHub identity environment = %q, want %q", call.env, environment)
		}
	}
}

func TestPromoteReleaseOCIAliasesRejectsAliasDigestDrift(t *testing.T) {
	t.Parallel()

	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	writeReleasePromotionFixture(t, artifactRoot, runnerTemp)
	runner := &releasePromotionRunner{aliasDigests: []string{
		testOCIDigest("4"), testOCIDigest("4"), testOCIDigest("5"),
		testOCIDigest("9"),
		testOCIDigest("4"), testOCIDigest("4"), testOCIDigest("5"),
	}}
	err := PromoteReleaseOCIAliases(context.Background(), PromoteReleaseOCIAliasesOptions{
		Root: t.TempDir(), ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || !containsError(err, "does not reference the verified OCI index") {
		t.Fatalf("PromoteReleaseOCIAliases() error = %v, want alias digest drift", err)
	}
	if len(runner.calls) != 16 {
		t.Fatalf("commands after first alias drift and rollback = %d, want 16", len(runner.calls))
	}
}

func TestPromoteReleaseOCIAliasesRollsBackAfterSecondMutationFailure(t *testing.T) {
	t.Parallel()

	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	writeReleasePromotionFixture(t, artifactRoot, runnerTemp)
	oldPrimary := testOCIDigest("4")
	oldOracle := testOCIDigest("5")
	runner := &releasePromotionRunner{
		aliasDigests: []string{oldPrimary, oldPrimary, oldOracle, oldPrimary, oldPrimary, oldOracle},
		failCreateAt: 2,
	}
	err := PromoteReleaseOCIAliases(context.Background(), PromoteReleaseOCIAliasesOptions{
		Root: t.TempDir(), ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || !containsError(err, "promote verified OCI alias") {
		t.Fatalf("PromoteReleaseOCIAliases() error = %v, want second mutation failure", err)
	}
	if len(runner.calls) != 15 {
		t.Fatalf("commands after second mutation failure and rollback = %d, want 15", len(runner.calls))
	}
	wantRollback := [][]string{
		{
			"buildx", "imagetools", "create", "--tag", "ghcr.io/dantte-lp/gobfd:latest",
			"ghcr.io/dantte-lp/gobfd@" + oldPrimary,
		},
		{
			"buildx", "imagetools", "create", "--tag", "ghcr.io/dantte-lp/gobfd:debian-trixie",
			"ghcr.io/dantte-lp/gobfd@" + oldPrimary,
		},
		{
			"buildx", "imagetools", "create", "--tag", "ghcr.io/dantte-lp/gobfd:oraclelinux10",
			"ghcr.io/dantte-lp/gobfd@" + oldOracle,
		},
	}
	for index, want := range wantRollback {
		if got := runner.calls[9+index].args; !reflect.DeepEqual(got, want) {
			t.Errorf("rollback call %d = %q, want %q", index, got, want)
		}
	}
}

func TestPromoteReleaseOCIAliasesRollsBackWithIndependentContext(t *testing.T) {
	t.Parallel()

	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	writeReleasePromotionFixture(t, artifactRoot, runnerTemp)
	oldPrimary := testOCIDigest("4")
	oldOracle := testOCIDigest("5")
	ctx, cancel := context.WithCancel(context.Background())
	runner := &releasePromotionRunner{
		aliasDigests:   []string{oldPrimary, oldPrimary, oldOracle, oldPrimary, oldPrimary, oldOracle},
		cancelCreateAt: 2,
		cancel:         cancel,
	}
	err := PromoteReleaseOCIAliases(ctx, PromoteReleaseOCIAliasesOptions{
		Root: t.TempDir(), ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || !containsError(err, context.Canceled.Error()) {
		t.Fatalf("PromoteReleaseOCIAliases() error = %v, want cancellation", err)
	}
	if len(runner.calls) != 15 {
		t.Fatalf("commands after cancellation and rollback = %d, want 15", len(runner.calls))
	}
}

func TestPromoteReleaseOCIAliasesRollsBackAfterFinalRootIdentityFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactRoot := t.TempDir()
	runnerParent := t.TempDir()
	runnerTemp := filepath.Join(runnerParent, "runner")
	if err := os.Mkdir(runnerTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	writeReleasePromotionFixture(t, artifactRoot, runnerTemp)
	oldPrimary := testOCIDigest("4")
	oldOracle := testOCIDigest("5")
	runner := &releasePromotionRunner{
		aliasDigests: []string{
			oldPrimary, oldPrimary, oldOracle,
			testOCIDigest("1"), testOCIDigest("1"), testOCIDigest("3"),
			oldPrimary, oldPrimary, oldOracle,
		},
	}
	runner.afterCall = func(call int) {
		if call != 12 {
			return
		}
		if err := os.Rename(runnerTemp, runnerTemp+"-owned"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(runnerTemp, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	err := PromoteReleaseOCIAliases(context.Background(), PromoteReleaseOCIAliasesOptions{
		Root: root, ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || !containsError(err, "RUNNER_TEMP after alias verification") {
		t.Fatalf("PromoteReleaseOCIAliases() error = %v, want final root identity failure", err)
	}
	if len(runner.calls) != 18 {
		t.Fatalf("commands after root replacement and rollback = %d, want 18", len(runner.calls))
	}
}

func TestPromoteReleaseOCIAliasesRejectsInvalidReceiptBeforeMutation(t *testing.T) {
	t.Parallel()

	artifactRoot := t.TempDir()
	runnerTemp := t.TempDir()
	writeReleasePromotionFixture(t, artifactRoot, runnerTemp)
	if err := os.WriteFile(
		filepath.Join(artifactRoot, "release-image-digests.txt"), []byte("invalid\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runner := &releasePromotionRunner{}
	err := PromoteReleaseOCIAliases(context.Background(), PromoteReleaseOCIAliasesOptions{
		Root: t.TempDir(), ArtifactRoot: artifactRoot, RunnerTemp: runnerTemp,
		RefName: "v0.6.2", SHA: preflightCommit, Repository: "dantte-lp/gobfd", Runner: runner,
	})
	if err == nil || !containsError(err, "exactly three") {
		t.Fatalf("PromoteReleaseOCIAliases() error = %v, want invalid receipt", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("invalid receipt command count = %d, want four identity checks", len(runner.calls))
	}
}

func writeReleasePromotionFixture(t *testing.T, artifactRoot, runnerTemp string) {
	t.Helper()
	for name, content := range map[string][]byte{
		"expected-release-commit.txt":     []byte(preflightCommit + "\n"),
		"expected-release-branch.txt":     []byte("release/v0.6\n"),
		"expected-release-tag-object.txt": []byte(preflightTagObject + "\n"),
	} {
		writeReleaseVerifyFile(t, runnerTemp, name, content)
	}
	writeReleaseVerifyFile(t, artifactRoot, "release-image-digests.txt", validReleaseDigestReceipt("0.6.2"))
}

type releasePromotionRunner struct {
	aliasDigests   []string
	aliasIndex     int
	createCount    int
	failCreateAt   int
	cancelCreateAt int
	cancel         context.CancelFunc
	afterCall      func(int)
	calls          []specInvocation
}

func (runner *releasePromotionRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir,
		env: append([]string(nil), spec.Env...),
	})
	call := len(runner.calls)
	if runner.afterCall != nil {
		defer runner.afterCall(call)
	}
	var data []byte
	switch {
	case spec.Name == "git" && reflect.DeepEqual(spec.Args, []string{"rev-parse", "HEAD"}):
		data = []byte(preflightCommit + "\n")
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/tags/v0.6.2"}):
		data = fmt.Appendf(nil, `{"ref":"refs/tags/v0.6.2","object":{"type":"tag","sha":%q}}`, preflightTagObject)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{
		"api", "repos/dantte-lp/gobfd/git/tags/" + preflightTagObject,
	}):
		data = fmt.Appendf(nil,
			`{"sha":%q,"tag":"v0.6.2","object":{"type":"commit","sha":%q}}`, preflightTagObject, preflightCommit,
		)
	case spec.Name == "gh" && slices.Equal(spec.Args, []string{"api", "repos/dantte-lp/gobfd/git/ref/heads/release/v0.6"}):
		data = fmt.Appendf(nil, `{"ref":"refs/heads/release/v0.6","object":{"type":"commit","sha":%q}}`, preflightCommit)
	case spec.Name == "docker" && len(spec.Args) >= 3 && slices.Equal(
		spec.Args[:3], []string{"buildx", "imagetools", "create"},
	):
		runner.createCount++
		if runner.createCount == runner.cancelCreateAt {
			runner.cancel()
			return ctx.Err()
		}
		if runner.createCount == runner.failCreateAt {
			return errors.New("injected create failure")
		}
		return nil
	case spec.Name == "docker" && runner.aliasIndex < len(runner.aliasDigests):
		index := validOCIManifestIndex("alias")
		index.Digest = runner.aliasDigests[runner.aliasIndex]
		var err error
		data, err = json.Marshal(index)
		if err != nil {
			return err
		}
		runner.aliasIndex++
	default:
		return fmt.Errorf("unexpected release promotion command: %s %q", spec.Name, spec.Args)
	}
	if spec.Stdout == nil {
		return fmt.Errorf("release promotion command lacks captured stdout: %s %q", spec.Name, spec.Args)
	}
	_, err := spec.Stdout.Write(data)
	return err
}
