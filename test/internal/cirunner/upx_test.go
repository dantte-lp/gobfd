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
	runner := &upxTestRunner{archive: archiveData, tar: tarData, version: "upx 5.3.0\nextra\n"}
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin", "OTHER=value"}
	if err := ReleaseUPX(context.Background(), ReleaseUPXOptions{
		RunnerTemp: runnerTemp, GitHubPath: githubPath, Environment: environment, Runner: runner, asset: &asset,
	}); err != nil {
		t.Fatalf("ReleaseUPX() error = %v", err)
	}

	root := filepath.Join(runnerTemp, "gobfd-upx-5.3.0")
	wantCalls := []specInvocation{
		{name: "gh", args: []string{
			"release", "download", "v5.3.0", "--repo", "upx/upx", "--pattern",
			"upx-5.3.0-amd64_linux.tar.xz", "--output", "-",
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
	archivePath := filepath.Join(root, "download", "upx-5.3.0-amd64_linux.tar.xz")
	if data, err := os.ReadFile(archivePath); err != nil || !bytes.Equal(data, archiveData) {
		t.Errorf("downloaded archive = %q, %v; want exact fixture", data, err)
	}
	assertExactMode(t, archivePath, 0o644)
	upxPath := filepath.Join(root, "bin", "upx")
	if data, err := os.ReadFile(upxPath); err != nil || string(data) != "test upx" {
		t.Errorf("extracted UPX = %q, %v", data, err)
	}
	assertExactMode(t, upxPath, 0o755)
	if data, err := os.ReadFile(githubPath); err != nil ||
		string(data) != "/existing/bin\n"+filepath.Join(root, "bin")+"\n" {
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

	asset := upxAssetContractForArchitecture("amd64")
	if asset.version != "5.3.0" || asset.archiveName != "upx-5.3.0-amd64_linux.tar.xz" ||
		asset.archiveSize != 670704 ||
		asset.archiveSHA256 != "79cea8a50b6fa102d10c7b79a419cd77dead3cc2d78661de2dc5e5815dc505c1" ||
		asset.tarSize != 839680 {
		t.Fatalf("production UPX contract = %#v", asset)
	}
	wantEntries := []upxTarEntry{
		{name: "upx-5.3.0-amd64_linux/", size: 0, mode: 0o755, kind: tar.TypeDir},
		{name: "upx-5.3.0-amd64_linux/COPYING", size: 18092, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/LICENSE", size: 5448, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/NEWS", size: 26509, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/README", size: 3718, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/THANKS.txt", size: 2566, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/upx", size: 645240, mode: 0o755, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/upx-doc.html", size: 40839, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/upx-doc.txt", size: 39224, mode: 0o644, kind: tar.TypeReg},
		{name: "upx-5.3.0-amd64_linux/upx.1", size: 42280, mode: 0o644, kind: tar.TypeReg},
	}
	if !reflect.DeepEqual(asset.entries, wantEntries) {
		t.Errorf("production UPX entries = %#v, want %#v", asset.entries, wantEntries)
	}
	if err := validateUPXAssetContract(asset); err != nil {
		t.Fatalf("verified amd64 contract rejected: %v", err)
	}
	arm := upxAssetContractForArchitecture("arm64")
	if arm.version != "5.3.0" || arm.architecture != "arm64" ||
		arm.archiveName != "upx-5.3.0-arm64_linux.tar.xz" || arm.archiveSize != 681612 ||
		arm.archiveSHA256 != "2a4afa860ac7aa6968f88df7841823746e5d00ba56affdf9342c2f5254fb776f" ||
		arm.tarSize != 849920 || arm.entriesUPXSize() != 658684 {
		t.Fatalf("production arm64 UPX contract = %#v", arm)
	}
	for index := range wantEntries {
		wantEntries[index].name = strings.Replace(wantEntries[index].name, "amd64_linux", "arm64_linux", 1)
		if strings.HasSuffix(wantEntries[index].name, "/upx") {
			wantEntries[index].size = 658684
		}
	}
	if !reflect.DeepEqual(arm.entries, wantEntries) {
		t.Errorf("production arm64 UPX entries = %#v, want %#v", arm.entries, wantEntries)
	}
	if err := validateUPXAssetContract(arm); err != nil {
		t.Fatalf("verified arm64 contract rejected: %v", err)
	}
}

func TestReleaseUPXAssetsExposeIndependentPinnedMetadata(t *testing.T) {
	t.Parallel()

	want := []ReleaseUPXAsset{
		{
			Version: "5.3.0", Architecture: "amd64", ArchiveName: "upx-5.3.0-amd64_linux.tar.xz",
			ArchiveSize: 670704, SHA256: "79cea8a50b6fa102d10c7b79a419cd77dead3cc2d78661de2dc5e5815dc505c1",
			SourceCommit:  "7c7bea44146c7ad6e17cb2d0bb9566805b44a91a",
			LicenseSHA256: "905d4a66d5324e154d976bbf88829d4d9263815a2b97cc40954f06d4a23c28eb",
			CopyingSHA256: "8177f97513213526df2cf6184d8ff986c675afb514d4e68a404010521b880643",
		},
		{
			Version: "5.3.0", Architecture: "arm64", ArchiveName: "upx-5.3.0-arm64_linux.tar.xz",
			ArchiveSize: 681612, SHA256: "2a4afa860ac7aa6968f88df7841823746e5d00ba56affdf9342c2f5254fb776f",
			SourceCommit:  "7c7bea44146c7ad6e17cb2d0bb9566805b44a91a",
			LicenseSHA256: "905d4a66d5324e154d976bbf88829d4d9263815a2b97cc40954f06d4a23c28eb",
			CopyingSHA256: "8177f97513213526df2cf6184d8ff986c675afb514d4e68a404010521b880643",
		},
	}
	assets := ReleaseUPXAssets()
	if !reflect.DeepEqual(assets, want) {
		t.Fatalf("release UPX inventory metadata = %#v, want %#v", assets, want)
	}
	assets[0].SHA256 = strings.Repeat("0", 64)
	if current := ReleaseUPXAssets(); !reflect.DeepEqual(current, want) {
		t.Fatalf("caller mutated authoritative UPX metadata: %#v", current)
	}
}

func TestReleaseUPXExtractsARM64TarWithoutExecutingIt(t *testing.T) {
	t.Parallel()

	tarData := makeUPXTestTarForArchitecture(t, "arm64", false)
	asset := testUPXAssetContract(1, strings.Repeat("0", 64), int64(len(tarData)))
	asset.architecture = "arm64"
	asset.archiveName = "upx-5.3.0-arm64_linux.tar.xz"
	for index := range asset.entries {
		asset.entries[index].name = strings.Replace(asset.entries[index].name, "amd64_linux", "arm64_linux", 1)
	}
	if err := validateUPXAssetContract(asset); err != nil {
		t.Fatalf("ARM64 fixture contract rejected: %v", err)
	}
	executable, err := validateUPXTar(tarData, asset)
	if err != nil || !bytes.Equal(executable, []byte("test upx")) {
		t.Fatalf("ARM64 archive extraction = %q, %v; want exact executable bytes", executable, err)
	}
}

func TestReleaseUPXRejectsUnsupportedAndMismatchedArchitectureContracts(t *testing.T) {
	t.Parallel()

	for _, change := range []func(*upxAssetContract){
		func(asset *upxAssetContract) { asset.architecture = "riscv64" },
		func(asset *upxAssetContract) { asset.architecture = "../amd64" },
		func(asset *upxAssetContract) { asset.architecture = "arm64" },
		func(asset *upxAssetContract) { asset.archiveName = "../" + asset.archiveName },
		func(asset *upxAssetContract) { asset.version = "4.2.2" },
		func(asset *upxAssetContract) { asset.entries = upxAssetContractForArchitecture("arm64").entries },
	} {
		asset := upxAssetContractForArchitecture("amd64")
		change(&asset)
		if err := validateUPXAssetContract(asset); !errors.Is(err, errInvalidConfig) {
			t.Errorf("invalid architecture/layout contract accepted: %#v; error = %v", asset, err)
		}
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
		{name: "unexpected tar entry", mutate: func(t *testing.T, _ *ReleaseUPXOptions,
			runner *upxTestRunner, asset *upxAssetContract,
		) {
			t.Helper()
			runner.tar = makeUPXTestTar(t, true)
			asset.tarSize = int64(len(runner.tar))
		}},
		{name: "directory collision", mutate: func(t *testing.T, options *ReleaseUPXOptions,
			_ *upxTestRunner, _ *upxAssetContract,
		) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(options.RunnerTemp, "gobfd-upx-5.3.0"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong version", mutate: func(_ *testing.T, _ *ReleaseUPXOptions, runner *upxTestRunner, _ *upxAssetContract) {
			runner.version = "upx 4.2.1\n"
		}},
		{name: "archive replacement after verification", mutate: func(_ *testing.T, _ *ReleaseUPXOptions,
			runner *upxTestRunner, asset *upxAssetContract,
		) {
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
		{name: "executable replacement during verification", mutate: func(_ *testing.T, _ *ReleaseUPXOptions,
			runner *upxTestRunner, _ *upxAssetContract,
		) {
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
		{name: "root ownership replacement", mutate: func(_ *testing.T, _ *ReleaseUPXOptions,
			runner *upxTestRunner, _ *upxAssetContract,
		) {
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
		{name: "symlink GITHUB_PATH", mutate: func(t *testing.T, options *ReleaseUPXOptions,
			_ *upxTestRunner, _ *upxAssetContract,
		) {
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
			runner := &upxTestRunner{archive: archiveData, tar: tarData, version: "upx 5.3.0\n"}
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
				if data, readErr := os.ReadFile(filepath.Join(options.RunnerTemp,
					"gobfd-upx-5.3.0", "sentinel")); readErr != nil || string(data) != "preserve" {
					t.Errorf("replacement UPX root changed: %q, %v", data, readErr)
				}
			} else if test.name != "directory collision" {
				if _, err := os.Lstat(filepath.Join(options.RunnerTemp, "gobfd-upx-5.3.0")); !errors.Is(err, os.ErrNotExist) {
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
		version: "5.3.0", architecture: "amd64", archiveName: "upx-5.3.0-amd64_linux.tar.xz",
		archiveSize: archiveSize, archiveSHA256: archiveSHA, tarSize: tarSize,
		entries: []upxTarEntry{
			{name: "upx-5.3.0-amd64_linux/", mode: 0o755, kind: tar.TypeDir},
			{name: "upx-5.3.0-amd64_linux/upx", size: 8, mode: 0o755, kind: tar.TypeReg},
		},
	}
}

func makeUPXTestTar(t *testing.T, unexpected bool) []byte {
	t.Helper()
	return makeUPXTestTarForArchitecture(t, "amd64", unexpected)
}

func makeUPXTestTarForArchitecture(t *testing.T, architecture string, unexpected bool) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	prefix := "upx-5.3.0-" + architecture + "_linux/"
	entries := []struct {
		header tar.Header
		data   string
	}{
		{header: tar.Header{Name: prefix, Mode: 0o755, Typeflag: tar.TypeDir}},
		{header: tar.Header{
			Name: prefix + "upx", Mode: 0o755, Size: 8,
			Typeflag: tar.TypeReg,
		}, data: "test upx"},
	}
	if unexpected {
		entries = append(entries, struct {
			header tar.Header
			data   string
		}{header: tar.Header{Name: prefix + "extra", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, data: "x"})
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
