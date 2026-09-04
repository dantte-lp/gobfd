package cirunner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseUPXUsesPinnedAssetAndAppendsVerifiedBinaryPath(t *testing.T) {
	t.Parallel()

	tarData := makeUPXTestTar(t, false)
	archiveData := []byte("test xz archive")
	digest := sha256.Sum256(archiveData)
	asset := testUPXAssetContract(int64(len(archiveData)), hex.EncodeToString(digest[:]), int64(len(tarData)))
	runnerTemp := t.TempDir()
	githubPath := filepath.Join(t.TempDir(), "github-path")
	if err := os.WriteFile(githubPath, []byte("/existing/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &upxTestRunner{archive: archiveData, tar: tarData, version: "upx 4.2.2\nextra\n"}
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin", "OTHER=value"}
	if err := ReleaseUPX(context.Background(), ReleaseUPXOptions{
		RunnerTemp: runnerTemp, GitHubPath: githubPath, Environment: environment, Runner: runner, asset: &asset,
	}); err != nil {
		t.Fatalf("ReleaseUPX() error = %v", err)
	}

	root := filepath.Join(runnerTemp, "gobfd-upx-4.2.2")
	wantCalls := []specInvocation{
		{name: "gh", args: []string{
			"release", "download", "v4.2.2", "--repo", "upx/upx", "--pattern",
			"upx-4.2.2-amd64_linux.tar.xz", "--output", "-",
		}, dir: root, env: environment},
		{name: "xz", args: []string{"-d", "-c", "-q"}, dir: root, env: []string{
			"PATH=/usr/bin", "OTHER=value",
		}},
		{name: "upx", args: []string{"--version"}, dir: root, env: []string{
			"OTHER=value", "PATH=" + filepath.Join(root, "bin") + ":/usr/bin",
		}, executable: true},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Errorf("UPX bootstrap calls = %#v, want %#v", runner.calls, wantCalls)
	}
	archivePath := filepath.Join(root, "download", "upx-4.2.2-amd64_linux.tar.xz")
	if data, err := os.ReadFile(archivePath); err != nil || !bytes.Equal(data, archiveData) {
		t.Errorf("downloaded archive = %q, %v; want exact fixture", data, err)
	}
	assertExactMode(t, archivePath, 0o644)
	upxPath := filepath.Join(root, "bin", "upx")
	if data, err := os.ReadFile(upxPath); err != nil || string(data) != "test upx" {
		t.Errorf("extracted UPX = %q, %v", data, err)
	}
	assertExactMode(t, upxPath, 0o755)
	if data, err := os.ReadFile(githubPath); err != nil || string(data) != "/existing/bin\n"+filepath.Join(root, "bin")+"\n" {
		t.Errorf("GITHUB_PATH = %q, %v", data, err)
	}
}

func TestAppendGitHubPathSeparatesRecords(t *testing.T) {
	t.Parallel()

	binDirectory := filepath.Join(string(os.PathSeparator), "new", "bin")
	for _, test := range []struct {
		name    string
		initial string
		want    string
	}{
		{name: "missing newline", initial: "/existing/bin", want: "/existing/bin\n" + binDirectory + "\n"},
		{name: "empty", want: binDirectory + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "github-path")
			if err := os.WriteFile(path, []byte(test.initial), 0o644); err != nil {
				t.Fatal(err)
			}
			published, err := appendGitHubPath(path, binDirectory)
			if err != nil || !published {
				t.Fatalf("appendGitHubPath() = %t, %v; want true, nil", published, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != test.want {
				t.Errorf("GITHUB_PATH = %q, %v; want %q", data, err, test.want)
			}
		})
	}
}

func TestWriteRootedModeArtifactReportsCommitState(t *testing.T) {
	t.Parallel()

	postRenameErr := errors.New("post-rename inspection failed")
	for _, test := range []struct {
		name          string
		limit         int
		inspect       rootedModeArtifactInspector
		wantCommitted bool
	}{
		{name: "pre-rename failure", inspect: inspectPublishedRootedModeArtifact},
		{
			name:          "post-rename failure",
			limit:         4,
			inspect:       func(*os.Root, string, string, os.FileMode, int64) error { return postRenameErr },
			wantCommitted: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.Close() })
			committed, err := writeRootedModeArtifactState(
				root, "artifact", []byte("data"), "test artifact", test.limit, 0o600, test.inspect,
			)
			if committed != test.wantCommitted || err == nil {
				t.Fatalf("writeRootedModeArtifactState() = %t, %v; want %t, error", committed, err, test.wantCommitted)
			}
			if test.wantCommitted {
				if _, statErr := root.Lstat("artifact"); statErr != nil || !errors.Is(err, postRenameErr) {
					t.Fatalf("post-rename result = %t, %v; artifact error = %v", committed, err, statErr)
				}
			}
		})
	}
}

func TestReleaseUPXProductionAssetContract(t *testing.T) {
	t.Parallel()

	asset := defaultUPXAssetContract()
	if asset.version != "4.2.2" || asset.archiveName != "upx-4.2.2-amd64_linux.tar.xz" ||
		asset.archiveSize != 590172 || asset.archiveSHA256 != "915c8e844f835de03b9cc311ff185aedec79d757aee9d7133a528b9e89c463bb" ||
		asset.tarSize != 747520 {
		t.Fatalf("production UPX contract = %#v", asset)
	}
	wantEntries := []upxTarEntry{
		{name: "upx-4.2.2-amd64_linux/", size: 0, mode: 0o755, kind: tar.TypeDir},
		{name: "upx-4.2.2-amd64_linux/COPYING", size: 18092, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/LICENSE", size: 5448, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/NEWS", size: 24953, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/README", size: 3728, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/THANKS.txt", size: 2230, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/upx", size: 562176, mode: 0o755, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/upx-doc.html", size: 38689, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/upx-doc.txt", size: 37296, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-4.2.2-amd64_linux/upx.1", size: 43267, mode: 0o644, kind: tar.TypeReg},
	}
	if !reflect.DeepEqual(asset.entries, wantEntries) {
		t.Errorf("production UPX entries = %#v, want %#v", asset.entries, wantEntries)
	}
}

func TestExecRunnerRequiresBoundAllowlistedUPX(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bin, "upx")
	if err := os.WriteFile(want, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = executable.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, spec := range []CommandSpec{
		{Name: "xz", Args: []string{"--version"}},
		{Name: "upx", Args: []string{"--version"}, Dir: root, Env: []string{"PATH=" + bin}, Executable: executable},
	} {
		if err := (ExecRunner{}).RunCommand(ctx, spec); errors.Is(err, errCommandNotAllowed) {
			t.Errorf("RunCommand(%s) error = %v, want deliberately allowlisted executable", spec.Name, err)
		}
	}
}

func TestReleaseUPXRejectsInvalidEvidenceAndUnsafePaths(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *ReleaseUPXOptions, *upxTestRunner, *upxAssetContract)
	}{
		{name: "checksum", mutate: func(_ *testing.T, _ *ReleaseUPXOptions, _ *upxTestRunner, asset *upxAssetContract) {
			asset.archiveSHA256 = strings.Repeat("0", 64)
		}},
		{name: "unexpected tar entry", mutate: func(t *testing.T, _ *ReleaseUPXOptions, runner *upxTestRunner, asset *upxAssetContract) {
			t.Helper()
			runner.tar = makeUPXTestTar(t, true)
			asset.tarSize = int64(len(runner.tar))
		}},
		{name: "directory collision", mutate: func(t *testing.T, options *ReleaseUPXOptions, _ *upxTestRunner, _ *upxAssetContract) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(options.RunnerTemp, "gobfd-upx-4.2.2"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong version", mutate: func(_ *testing.T, _ *ReleaseUPXOptions, runner *upxTestRunner, _ *upxAssetContract) {
			runner.version = "upx 4.2.1\n"
		}},
		{name: "archive replacement after verification", mutate: func(_ *testing.T, _ *ReleaseUPXOptions, runner *upxTestRunner, asset *upxAssetContract) {
			runner.beforeCommand = func(spec CommandSpec) error {
				if spec.Name != "xz" {
					return nil
				}
				archive := filepath.Join(spec.Dir, "download", asset.archiveName)
				if err := os.Rename(archive, archive+".owned"); err != nil {
					return err
				}
				return os.WriteFile(archive, bytes.Repeat([]byte("x"), int(asset.archiveSize)), 0o644)
			}
		}},
		{name: "executable replacement during verification", mutate: func(_ *testing.T, _ *ReleaseUPXOptions, runner *upxTestRunner, _ *upxAssetContract) {
			runner.beforeCommand = func(spec CommandSpec) error {
				if spec.Name != "upx" {
					return nil
				}
				executable := filepath.Join(spec.Dir, "bin", "upx")
				if err := os.Rename(executable, executable+".owned"); err != nil {
					return err
				}
				return os.WriteFile(executable, []byte("malicious"), 0o755)
			}
		}},
		{name: "root ownership replacement", mutate: func(_ *testing.T, _ *ReleaseUPXOptions, runner *upxTestRunner, _ *upxAssetContract) {
			runner.beforeCommand = func(spec CommandSpec) error {
				if spec.Name != "upx" {
					return nil
				}
				if err := os.Rename(spec.Dir, spec.Dir+".owned"); err != nil {
					return err
				}
				if err := os.Mkdir(spec.Dir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(spec.Dir, "sentinel"), []byte("preserve"), 0o644)
			}
		}},
		{name: "symlink GITHUB_PATH", mutate: func(t *testing.T, options *ReleaseUPXOptions, _ *upxTestRunner, _ *upxAssetContract) {
			t.Helper()
			external := filepath.Join(t.TempDir(), "external")
			if err := os.WriteFile(external, []byte("preserve\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			options.GitHubPath = filepath.Join(t.TempDir(), "github-path")
			if err := os.Symlink(external, options.GitHubPath); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			archiveData := []byte("test xz archive")
			tarData := makeUPXTestTar(t, false)
			digest := sha256.Sum256(archiveData)
			asset := testUPXAssetContract(int64(len(archiveData)), hex.EncodeToString(digest[:]), int64(len(tarData)))
			runner := &upxTestRunner{archive: archiveData, tar: tarData, version: "upx 4.2.2\n"}
			githubPath := filepath.Join(t.TempDir(), "github-path")
			if err := os.WriteFile(githubPath, []byte("preserve\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			options := ReleaseUPXOptions{
				RunnerTemp: t.TempDir(), GitHubPath: githubPath, Environment: []string{"PATH=/usr/bin"},
				Runner: runner, asset: &asset,
			}
			test.mutate(t, &options, runner, &asset)
			preservedPath := githubPath
			if test.name == "symlink GITHUB_PATH" {
				var err error
				preservedPath, err = filepath.EvalSymlinks(options.GitHubPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := ReleaseUPX(context.Background(), options); err == nil {
				t.Fatal("ReleaseUPX() error = nil, want fail-closed rejection")
			}
			if test.name == "root ownership replacement" {
				if data, readErr := os.ReadFile(filepath.Join(options.RunnerTemp, "gobfd-upx-4.2.2", "sentinel")); readErr != nil || string(data) != "preserve" {
					t.Errorf("replacement UPX root changed: %q, %v", data, readErr)
				}
			} else if test.name != "directory collision" {
				if _, err := os.Lstat(filepath.Join(options.RunnerTemp, "gobfd-upx-4.2.2")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("partial UPX root remains after failure: %v", err)
				}
			}
			if data, err := os.ReadFile(preservedPath); err != nil || string(data) != "preserve\n" {
				t.Errorf("GITHUB_PATH changed after failure: %q, %v", data, err)
			}
		})
	}
}

func testUPXAssetContract(archiveSize int64, archiveSHA string, tarSize int64) upxAssetContract {
	return upxAssetContract{
		version: "4.2.2", archiveName: "upx-4.2.2-amd64_linux.tar.xz",
		archiveSize: archiveSize, archiveSHA256: archiveSHA, tarSize: tarSize,
		entries: []upxTarEntry{
			{name: "upx-4.2.2-amd64_linux/", mode: 0o755, kind: tar.TypeDir},
			{name: "upx-4.2.2-amd64_linux/upx", size: 8, mode: 0o755, kind: tar.TypeReg},
		},
	}
}

func makeUPXTestTar(t *testing.T, unexpected bool) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	entries := []struct {
		header tar.Header
		data   string
	}{
		{header: tar.Header{Name: "upx-4.2.2-amd64_linux/", Mode: 0o755, Typeflag: tar.TypeDir}},
		{header: tar.Header{Name: "upx-4.2.2-amd64_linux/upx", Mode: 0o755, Size: 8, Typeflag: tar.TypeReg}, data: "test upx"},
	}
	if unexpected {
		entries = append(entries, struct {
			header tar.Header
			data   string
		}{header: tar.Header{Name: "upx-4.2.2-amd64_linux/extra", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, data: "x"})
	}
	for _, entry := range entries {
		if err := writer.WriteHeader(&entry.header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type upxTestRunner struct {
	archive       []byte
	tar           []byte
	version       string
	calls         []specInvocation
	beforeCommand func(CommandSpec) error
}

func (runner *upxTestRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir,
		env: append([]string(nil), spec.Env...), executable: spec.Executable != nil,
	})
	if runner.beforeCommand != nil {
		if err := runner.beforeCommand(spec); err != nil {
			return err
		}
	}
	switch spec.Name {
	case "gh":
		_, err := spec.Stdout.Write(runner.archive)
		return err
	case "xz":
		if spec.Stdin == nil {
			return errors.New("xz stdin is nil")
		}
		if _, err := io.ReadAll(spec.Stdin); err != nil {
			return err
		}
		_, err := spec.Stdout.Write(runner.tar)
		return err
	case "upx":
		if _, err := io.WriteString(spec.Stdout, runner.version); err != nil {
			return err
		}
		_, err := io.WriteString(spec.Stderr, "verified stderr\n")
		return err
	default:
		return errors.New("unexpected command")
	}
}
