package cirunner

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

type goreleaserFixture struct {
	t                                *testing.T
	profile                          goreleaserProfile
	root, zipPath, installDir, fault string
	contents                         map[string][]byte
	specs                            []CommandSpec
	cancel                           context.CancelFunc
	refmtZIP                         string
	refmtContents                    map[string][]byte
}

func newGoReleaserFixture(t *testing.T) *goreleaserFixture {
	t.Helper()
	before := `"github.com/chrismellard/docker-credential-acr-env/pkg/credhelper"`
	after := `"github.com/osscontainertools/docker-credential-acr/pkg/credhelper"`
	ko := []byte("package ko\nimport " + before + "\n")
	fixture := &goreleaserFixture{
		t: t, root: t.TempDir(), installDir: filepath.Join(t.TempDir(), "publisher"),
		contents: map[string][]byte{
			"go.mod": []byte("module " + toolprofile.GoReleaserModule + "\n\ngo 1.27.3\n"),
			"go.sum": []byte("upstream fixture\n"), "main.go": []byte("package main\nfunc main() {}\n"),
			"internal/pipe/ko/ko.go": ko,
		},
	}
	fixture.profile = goreleaserProfile{
		Module: toolprofile.GoReleaserModule, Version: "v2.19.0",
		SourceCommit: "00139d15712716a3a2f21eb4d533f231ff19b9c5", Files: len(fixture.contents),
		Patch: goreleaserSourcePatch{
			Path: "internal/pipe/ko/ko.go", Before: before, After: after,
			BeforeSHA256: goreleaserSHA256(ko),
			AfterSHA256:  goreleaserSHA256([]byte(strings.Replace(string(ko), before, after, 1))),
		},
	}
	for _, data := range fixture.contents {
		fixture.profile.Bytes += int64(len(data))
	}
	fixture.profile.Sum = goReleaserContentSum(fixture.contents, fixture.profile.Module+"@"+fixture.profile.Version+"/")
	fixture.profile.GoModSum = goReleaserContentSum(map[string][]byte{"go.mod": fixture.contents["go.mod"]}, "")
	mod, sum := []byte(string(fixture.contents["go.mod"])+"// frozen test manifest\n"), []byte("qualified fixture\n")
	fixture.profile.SourceManifests = fixtureManifestHashes(mod, sum)
	directory := filepath.Join(fixture.root, "tools", "goreleaser")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"source.mod": mod, "source.sum": sum} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.prepareRefmtAndConsumer()
	fixture.writeProfile()
	fixture.zipPath = fixture.writeZIP(nil)
	return fixture
}

func (fixture *goreleaserFixture) writeProfile() {
	fixture.t.Helper()
	data, err := json.Marshal(fixture.profile)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, toolprofile.GoReleaserSource), data, 0o600); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *goreleaserFixture) writeZIP(mutate func(*zip.FileHeader)) string {
	fixture.t.Helper()
	return writePublisherFixtureZIP(fixture.t, fixture.profile, fixture.contents, mutate)
}

func writePublisherFixtureZIP(t *testing.T, profile goreleaserProfile,
	contents map[string][]byte, mutate func(*zip.FileHeader),
) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "source.zip")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, data := range contents {
		header := &zip.FileHeader{Name: profile.Module + "@" + profile.Version + "/" + name}
		header.SetMode(0o644)
		if mutate != nil {
			mutate(header)
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return filename
}

func (fixture *goreleaserFixture) buildInfo(io.ReaderAt) (*debug.BuildInfo, error) {
	info := &debug.BuildInfo{
		GoVersion: runtime.Version(), Path: fixture.profile.Module,
		Main: debug.Module{
			Path: fixture.profile.Module, Version: fixture.profile.Version,
			Replace: &debug.Module{Path: "../source", Version: "(devel)"},
		},
		Deps: []*debug.Module{{
			Path: toolprofile.RefmtModule, Version: fixture.profile.Refmt.Version,
			Replace: &debug.Module{Path: "../refmt-local", Version: "(devel)"},
		}},
	}
	for key, value := range map[string]string{
		"-trimpath":   "true",
		"CGO_ENABLED": "0", "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH,
	} {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: key, Value: value})
	}
	switch fixture.fault {
	case "forged main sum":
		info.Main.Sum = fixture.profile.Sum
	case "forged main version":
		info.Main.Version = "(devel)"
	case "missing replacement":
		info.Main.Replace = nil
	case "foreign replacement":
		info.Main.Replace.Path = "/foreign/source"
	case "forged replacement sum":
		info.Main.Replace.Sum = fixture.profile.Sum
	case "wrong refmt replacement":
		info.Deps[0].Replace.Path = "../foreign"
	case "missing compiler settings":
		info.Settings = nil
	}
	return info, nil
}

func (fixture *goreleaserFixture) version() string {
	return fmt.Sprintf("goreleaser: Release engineering, simplified.\nhttps://goreleaser.com\n\n"+
		"GitVersion:  %s\nGitCommit:  %s\nGitTreeState: dirty\nBuildDate: unknown\nBuiltBy: unknown\n"+
		"GoVersion: %s\nCompiler: %s\nModuleSum: unknown\nPlatform: %s/%s\n",
		strings.TrimPrefix(fixture.profile.Version, "v"), fixture.profile.SourceCommit,
		runtime.Version(), runtime.Compiler, runtime.GOOS, runtime.GOARCH)
}

func (fixture *goreleaserFixture) RunCommand(ctx context.Context, spec CommandSpec) error {
	fixture.t.Helper()
	fixture.specs = append(fixture.specs, spec)
	if strings.Contains(strings.Join(spec.Env, "\n"), "never-forward") {
		fixture.t.Fatal("credential reached publisher source build/probe")
	}
	if spec.Name == goreleaserName {
		if spec.Executable == nil || !slices.Equal(spec.Args, []string{"--version"}) {
			fixture.t.Fatal("publisher probe did not bind the executable")
		}
		version := fixture.version()
		if fixture.fault == "wrong probe" {
			version = strings.ReplaceAll(version, "dirty", "clean")
		}
		_, err := io.WriteString(spec.Stdout, version)
		return err
	}
	if spec.Name != "go" {
		fixture.t.Fatalf("unexpected source command: %s", spec.Name)
	}
	if spec.Args[0] == "mod" {
		if spec.Args[1] == "edit" {
			return fixture.writeManifestJSON(spec)
		}
		return fixture.writeDownloadJSON(spec)
	}
	return fixture.buildPublisher(ctx, spec)
}

func (fixture *goreleaserFixture) writeDownloadJSON(spec CommandSpec) error {
	fixture.t.Helper()
	relative, pathErr := filepath.Rel(fixture.root, spec.Dir)
	if pathErr != nil || !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		fixture.t.Fatal("source acquisition ran inside the readonly repository")
	}
	if err := os.WriteFile(filepath.Join(spec.Dir, "go.sum"), []byte("owned download output\n"), 0o600); err != nil {
		return err
	}
	profile, filename := fixture.profile, fixture.zipPath
	if spec.Args[3] == toolprofile.RefmtModule+"@"+fixture.profile.Refmt.Version {
		profile, filename = fixture.profile.Refmt.sourceProfile(), fixture.refmtZIP
	}
	download := goreleaserDownload{
		Path: profile.Module, Version: profile.Version,
		Zip: filename, Sum: profile.Sum, GoModSum: profile.GoModSum,
	}
	origin, err := publisherSourceOrigin(profile.Module)
	if err != nil {
		return err
	}
	download.Origin.VCS, download.Origin.URL = "git", origin
	download.Origin.Ref, download.Origin.Hash = "refs/tags/"+profile.Version, profile.SourceCommit
	if fixture.fault == "wrong download origin" {
		download.Origin.Hash = strings.Repeat("0", 40)
	}
	return json.NewEncoder(spec.Stdout).Encode(download)
}

func (fixture *goreleaserFixture) buildPublisher(ctx context.Context, spec CommandSpec) error {
	fixture.t.Helper()
	if spec.Args[0] != "build" || !slices.Contains(spec.Args, "-ldflags="+fixture.profile.ldflags()) ||
		!slices.Contains(spec.Env, "CGO_ENABLED=0") {
		fixture.t.Fatalf("wrong publisher build profile: %q", spec.Args)
	}
	if filepath.Base(spec.Dir) != "stage" || spec.Args[len(spec.Args)-1] != fixture.profile.Module {
		fixture.t.Fatal("publisher build escaped canonical consumer cwd/package")
	}
	if fixture.fault == "build failure" {
		return syscall.EIO
	}
	if fixture.fault == "cancel build" {
		fixture.cancel()
		return ctx.Err()
	}
	for index, arg := range spec.Args {
		if arg == "-o" {
			return os.WriteFile(spec.Args[index+1], []byte("publisher executable fixture"), 0o755)
		}
	}
	fixture.t.Fatal("publisher build has no output")
	return nil
}

func TestGoReleaserSourceH1UsesOwningGoSummary(t *testing.T) {
	t.Parallel()
	// Independently derived with sha256sum and openssl from the owning Go Hash1 summary.
	const expected = "h1:5Fn9UlLDraC1wY/JAI5JBHb+Ice9ZdUmjUVTW+ldYmc="
	if got := goReleaserContentSum(map[string][]byte{"a": []byte("hello\n")}, ""); got != expected {
		t.Fatalf("Go h1 = %q, want %q", got, expected)
	}
}

func TestInstallGoReleaserSharesOwnedInstallationAndPreservesZIP(t *testing.T) {
	t.Parallel()
	fixture := newGoReleaserFixture(t)
	original, err := os.ReadFile(fixture.zipPath)
	if err != nil {
		t.Fatal(err)
	}
	refmtOriginal, err := os.ReadFile(fixture.refmtZIP)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := installGoReleaser(t.Context(), GoReleaserOptions{
		Root:       fixture.root,
		InstallDir: fixture.installDir, Runner: fixture, Environment: []string{"GH_TOKEN=never-forward"},
	},
		fixture.profile, fixture.buildInfo)
	if err != nil {
		t.Fatal(err)
	}
	if installation.toolName() != goreleaserName || installation.executableAt != filepath.Join("bin", goreleaserName) {
		t.Fatal("publisher did not use the shared source installation")
	}
	fixture.verifyReceipt(installation)
	current, err := os.ReadFile(fixture.zipPath)
	if err != nil || !slices.Equal(current, original) {
		t.Fatal("source ZIP was modified")
	}
	refmtCurrent, err := os.ReadFile(fixture.refmtZIP)
	if err != nil || !slices.Equal(refmtCurrent, refmtOriginal) {
		t.Fatal("Refmt ZIP was modified")
	}
	for _, name := range []string{"source", refmtDirectoryName, "stage/go.mod", "stage/go.sum"} {
		if _, statErr := os.Stat(filepath.Join(fixture.installDir, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("private source/consumer retained after successful build: %v", statErr)
		}
	}
	if err := installation.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(fixture.installDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned publisher files remain after Close: %v", err)
	}
}

func (fixture *goreleaserFixture) verifyReceipt(installation *sourceInstallation) {
	fixture.t.Helper()
	t := fixture.t
	provenanceInfo, statErr := os.Lstat(filepath.Join(fixture.installDir, goreleaserProvenance))
	if statErr != nil || !provenanceInfo.Mode().IsRegular() || provenanceInfo.Mode().Perm() != 0o600 {
		t.Fatalf("publisher provenance is not a private regular bin artifact: %v", statErr)
	}
	data, err := readGoReleaserFile(installation.root, goreleaserProvenance)
	if err != nil {
		t.Fatal(err)
	}
	var receipt goreleaserReceipt
	if decodeErr := json.Unmarshal(data, &receipt); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if receipt.OriginalBytes != fixture.profile.Bytes || receipt.PatchedBytes == receipt.OriginalBytes ||
		receipt.OriginalFiles != receipt.PatchedFiles {
		t.Fatalf("original and overlaid trees conflated: %+v", receipt)
	}
	if receipt.Refmt.OriginalFiles != fixture.profile.Refmt.Files ||
		receipt.ConsumerContentSum != fixture.profile.consumerContentSum() {
		t.Fatal("component source and consumer manifest roles were conflated")
	}
}

func TestInstallGoReleaserRejectsUnqualifiedAndFailedInputs(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{
		"unqualified manifests", "wrong manifest", "wrong download origin",
		"wrong patch", "build failure", "cancel build", "forged main sum", "forged main version", "missing compiler settings",
		"wrong probe", "missing replacement", "foreign replacement", "forged replacement sum", "wrong refmt replacement",
		"wrong source manifest", "wrong refmt manifest", "unqualified source manifests", "unqualified refmt manifests",
		"source manifest role", "wrong consumer replacement", "consumer manifest drift",
		"source overlay role", "refmt overlay role",
		"wrong refmt source",
	} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture := newGoReleaserFixture(t)
			fixture.fault = fault
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.cancel = cancel
			fixture.configureManifestFault(fault)
			installation, err := installGoReleaser(ctx, GoReleaserOptions{
				Root:       fixture.root,
				InstallDir: fixture.installDir, Runner: fixture,
			}, fixture.profile, fixture.buildInfo)
			if err == nil || installation != nil {
				t.Fatal("invalid publisher accepted")
			}
			if fault == "cancel build" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if _, err := os.Lstat(fixture.installDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed publisher retained owned files: %v", err)
			}
			if strings.HasPrefix(fault, "unqualified ") && len(fixture.specs) != 0 {
				t.Fatal("unqualified profile reached a source command")
			}
		})
	}
}

func TestGoReleaserZIPRejectsUntrustedPathsAndChecksums(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{
		"source sum", "manifest sum", "file count", "byte count", "traversal", "symlink",
		"duplicate",
	} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture := newGoReleaserFixture(t)
			switch fault {
			case "source sum":
				fixture.profile.Sum = "h1:5Fn9UlLDraC1wY/JAI5JBHb+Ice9ZdUmjUVTW+ldYmc="
			case "manifest sum":
				fixture.profile.GoModSum = fixture.profile.Sum
			case "file count":
				fixture.profile.Files++
			case "byte count":
				fixture.profile.Bytes--
			default:
				fixture.zipPath = fixture.writeZIP(func(header *zip.FileHeader) {
					switch fault {
					case "traversal":
						header.Name = fixture.profile.Module + "@" + fixture.profile.Version + "/../escape"
					case "symlink":
						header.SetMode(os.ModeSymlink | 0o777)
					case "duplicate":
						header.Name = fixture.profile.Module + "@" + fixture.profile.Version + "/go.mod"
					}
				})
			}
			if _, err := readGoReleaserZIP(fixture.zipPath, fixture.profile); err == nil {
				t.Fatal("untrusted ZIP accepted")
			}
		})
	}
}

func TestGoReleaserVersionRejectsDuplicateMetadata(t *testing.T) {
	t.Parallel()
	fixture := newGoReleaserFixture(t)
	for _, key := range []string{
		"GitVersion", "GitCommit", "GitTreeState", "BuildDate", "BuiltBy", "GoVersion",
		"Compiler", "ModuleSum", "Platform",
	} {
		for line := range strings.SplitSeq(fixture.version(), "\n") {
			if strings.HasPrefix(line, key+":") {
				if err := fixture.profile.verifyVersion([]byte(fixture.version() + line + "\n")); err == nil {
					t.Fatalf("duplicate %s accepted", key)
				}
			}
		}
	}
}

func TestGoReleaserSourceCleanupPreservesForeignPaths(t *testing.T) {
	t.Parallel()
	for _, selected := range []string{"root", "directory", "file"} {
		t.Run(selected, func(t *testing.T) {
			t.Parallel()
			fixture := newGoReleaserFixture(t)
			installation, prepareErr := prepareSourceInstallation(fixture.installDir, goreleaserName, (*os.Root).Mkdir)
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			t.Cleanup(func() { _ = installation.closeHandles() })
			source, sourceErr := prepareGoReleaserSource(installation.root, fixture.installDir)
			if sourceErr != nil {
				t.Fatal(sourceErr)
			}
			if err := source.copy(t.Context(), fixture.contents); err != nil {
				t.Fatal(err)
			}
			name := source.directory
			switch selected {
			case "directory":
				name = filepath.Join(name, "internal")
			case "file":
				name = filepath.Join(name, "main.go")
			}
			if err := os.Rename(name, name+"-original"); err != nil {
				t.Fatal(err)
			}
			marker := name
			if selected != "file" {
				if err := os.Mkdir(name, 0o700); err != nil {
					t.Fatal(err)
				}
				marker = filepath.Join(name, "foreign")
			}
			if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err == nil {
				t.Fatal("foreign publisher source path accepted for cleanup")
			}
			data, readErr := os.ReadFile(marker)
			if readErr != nil || string(data) != "foreign" {
				t.Fatalf("foreign source path changed: %q %v", data, readErr)
			}
		})
	}
}

func TestExecRunnerPublisherRequiresBoundExecutableAndKeepsCancellation(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "publisher")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, spec := range []CommandSpec{{Name: goreleaserName}, {Name: "go", Executable: file}} {
		if err := (ExecRunner{}).RunCommand(ctx, spec); !errors.Is(err, errInvalidConfig) {
			t.Fatalf("invalid publisher descriptor pairing accepted: %v", err)
		}
	}
	if err := (ExecRunner{}).RunCommand(ctx, CommandSpec{
		Name: goreleaserName, Executable: file,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("bound publisher lost cancellation: %v", err)
	}
}
