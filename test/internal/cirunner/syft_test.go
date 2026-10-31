package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const syftFixtureVersion = "v1.55.0"

type syftFixtureRunner struct {
	t       *testing.T
	scanner SpecRunner
	specs   []CommandSpec
	before  func(CommandSpec) error
	build   func(CommandSpec) error
	mutate  func(string, map[string]any)
}

func syftFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools", "syft"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPrepareSyftInstallationRemovesOnlyRecordedDirectoriesAfterMkdirFailure(t *testing.T) {
	t.Parallel()
	for _, failedName := range []string{"stage", "bin"} {
		t.Run(failedName, func(t *testing.T) {
			t.Parallel()
			directory := filepath.Join(t.TempDir(), "gobfd-syft")
			installation, err := prepareSyftInstallation(directory, func(root *os.Root, name string, mode os.FileMode) error {
				if name == failedName {
					return syscall.ENOSPC
				}
				return root.Mkdir(name, mode)
			})
			if installation != nil || !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("prepare error = %v, installation = %v", err, installation)
			}
			if _, statErr := os.Lstat(directory); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("partial owned installation retained: %v", statErr)
			}
			installation, err = prepareSyftInstallation(directory, (*os.Root).Mkdir)
			if err != nil {
				t.Fatalf("retry collided with partial installation: %v", err)
			}
			if err := installation.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareSyftInstallationMkdirFailurePreservesForeignDirectories(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{"unrecorded bin", "recorded stage", "root"} {
		t.Run(replacement, func(t *testing.T) {
			t.Parallel()
			directory := filepath.Join(t.TempDir(), "gobfd-syft")
			marker := ""
			installation, err := prepareSyftInstallation(directory, func(root *os.Root, name string, mode os.FileMode) error {
				if name != "bin" {
					return root.Mkdir(name, mode)
				}
				switch replacement {
				case "unrecorded bin":
					if err := root.Mkdir("bin", mode); err != nil {
						t.Fatal(err)
					}
					marker = filepath.Join(directory, "bin", "foreign")
				case "recorded stage":
					if err := root.Rename("stage", "stage-original"); err != nil {
						t.Fatal(err)
					}
					if err := root.Mkdir("stage", mode); err != nil {
						t.Fatal(err)
					}
					marker = filepath.Join(directory, "stage", "foreign")
				case "root":
					if err := os.Rename(directory, directory+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(directory, mode); err != nil {
						t.Fatal(err)
					}
					marker = filepath.Join(directory, "foreign")
				}
				if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
					t.Fatal(err)
				}
				return syscall.ENOSPC
			})
			if installation != nil || !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("prepare error = %v, installation = %v", err, installation)
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "foreign" {
				t.Fatalf("foreign marker changed: %q, %v", data, err)
			}
		})
	}
}

func unopenedSyftInstallationFixture(t *testing.T) *SyftInstallation {
	t.Helper()
	parentPath := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	parentInfo, err := parent.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	if mkdirErr := parent.Mkdir("gobfd-syft", 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	rootInfo, err := parent.Lstat("gobfd-syft")
	if err != nil {
		t.Fatal(err)
	}
	return &SyftInstallation{
		parent: parent, directory: filepath.Join(parentPath, "gobfd-syft"),
		name: "gobfd-syft", parentInfo: parentInfo, owned: map[string]os.FileInfo{".": rootInfo},
	}
}

func TestSyftInstallationCloseRemovesCapturedRootWithoutOpenHandle(t *testing.T) {
	t.Parallel()
	installation := unopenedSyftInstallationFixture(t)
	directory := installation.directory
	if err := installation.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unopened owned installation retained: %v", err)
	}
	retry, err := prepareSyftInstallation(directory, (*os.Root).Mkdir)
	if err != nil {
		t.Fatalf("retry collided after OpenRoot failure cleanup: %v", err)
	}
	if err := retry.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSyftInstallationCloseWithoutOpenRootPreservesForeignPaths(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{"root", "parent", "nonempty root"} {
		t.Run(replacement, func(t *testing.T) {
			t.Parallel()
			installation := unopenedSyftInstallationFixture(t)
			directory := installation.directory
			switch replacement {
			case "root":
				if err := os.Rename(directory, directory+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			case "parent":
				parent := filepath.Dir(directory)
				if err := os.Rename(parent, parent+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(directory, "foreign")
			if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := installation.Close(); err == nil {
				t.Fatal("unowned or nonempty directory accepted for cleanup")
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "foreign" {
				t.Fatalf("foreign marker changed: %q, %v", data, err)
			}
		})
	}
}

func (runner *syftFixtureRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	runner.t.Helper()
	runner.specs = append(runner.specs, spec)
	if runner.before != nil {
		if err := runner.before(spec); err != nil {
			return err
		}
	}
	var data map[string]any
	phase := spec.Name + " " + spec.Args[0]
	switch phase {
	case "go list":
		if spec.Args[len(spec.Args)-1] == toolprofile.SyftModule {
			data = map[string]any{
				"Path": toolprofile.SyftModule, "Version": syftFixtureVersion,
				"Sum": toolprofile.SyftSourceSum,
			}
		} else {
			data = map[string]any{
				"Path": "github.com/dantte-lp/gobfd/tools/syft", "Main": true,
				"Dir": spec.Dir, "GoMod": filepath.Join(spec.Dir, "go.mod"),
			}
		}
	case "go build":
		if runner.build != nil {
			return runner.build(spec)
		}
		for index, arg := range spec.Args {
			if arg == "-o" && index+1 < len(spec.Args) {
				return os.WriteFile(spec.Args[index+1], []byte("built executable fixture"), 0o755)
			}
		}
		return errors.New("missing build output")
	case "syft version":
		if spec.Executable == nil {
			return errors.New("version probe lacks bound file")
		}
		data = map[string]any{
			"application": "syft", "version": strings.TrimPrefix(syftFixtureVersion, "v"),
			"gitCommit": toolprofile.SyftSourceCommit, "gitDescription": syftFixtureVersion,
			"goVersion": runtime.Version(), "compiler": runtime.Compiler, "platform": runtime.GOOS + "/" + runtime.GOARCH,
		}
	case "syft scan":
		if runner.scanner == nil {
			return errors.New("unexpected scan")
		}
		return runner.scanner.RunCommand(ctx, spec)
	default:
		return fmt.Errorf("unexpected command %s", phase)
	}
	if runner.mutate != nil {
		runner.mutate(phase, data)
	}
	if err := json.NewEncoder(spec.Stdout).Encode(data); err != nil {
		return fmt.Errorf("encode fixture metadata: %w", err)
	}
	return nil
}

func TestInstallSyftBuildsFrozenProfileAndPublishesVerifiedInode(t *testing.T) {
	t.Parallel()
	root := syftFixtureRoot(t)
	installDir := filepath.Join(t.TempDir(), "syft")
	runner := &syftFixtureRunner{t: t}
	installation, err := InstallSyft(t.Context(), SyftOptions{
		Root: root, InstallDir: installDir,
		Environment: []string{
			"PATH=/sdk/bin", "GH_TOKEN=credential", "GITHUB_TOKEN=credential",
			"GOFLAGS=-modfile=foreign", "GOWORK=foreign", "GOOS=other",
		}, Runner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"build", "-buildvcs=false", "-mod=readonly", "-trimpath", "-tags=" + toolprofile.SyftBuildTags,
		"-ldflags=-X main.version=" + strings.TrimPrefix(syftFixtureVersion, "v") + " -X main.gitCommit=" +
			toolprofile.SyftSourceCommit + " -X main.gitDescription=" + syftFixtureVersion,
		"-o", filepath.Join(installDir, "stage", "syft"), toolprofile.SyftCommand,
	}
	if len(runner.specs) != 4 || !reflect.DeepEqual(runner.specs[2].Args, want) {
		t.Fatalf("build commands = %#v", runner.specs)
	}
	for _, spec := range runner.specs {
		for _, entry := range spec.Env {
			if strings.Contains(entry, "credential") || strings.Contains(entry, "foreign") || entry == "GOOS=other" {
				t.Fatalf("unsafe child environment retained: %q", entry)
			}
		}
		for _, entry := range []string{"CGO_ENABLED=0", "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off"} {
			if !strings.Contains(strings.Join(spec.Env, "\n"), entry) {
				t.Errorf("child environment lacks %s", entry)
			}
		}
	}
	if err := installation.verify(); err != nil {
		t.Fatal(err)
	}
	assertExactMode(t, installDir, 0o700)
	assertExactMode(t, filepath.Join(installDir, "bin", "syft"), 0o755)
	if _, err := os.Lstat(filepath.Join(installDir, "stage", "syft")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging executable remained: %v", err)
	}
	if err := installation.Close(); err != nil {
		t.Fatal(err)
	}
	if err := installation.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if err := (*SyftInstallation)(nil).Close(); err != nil {
		t.Fatalf("nil Close() = %v", err)
	}
	if _, err := os.Lstat(installDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned installation remained: %v", err)
	}
}

func TestReleaseSyftRejectsUnqualifiedMetadataWithoutPublishingPath(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(string, map[string]any)
	}{
		{"main module", func(phase string, data map[string]any) {
			if phase == "go list" {
				data["Path"] = "wrong"
			}
		}},
		{"replacement", func(phase string, data map[string]any) {
			if phase == "go list" {
				data["Replace"] = map[string]string{"Path": "foreign"}
			}
		}},
		{"checksum", func(phase string, data map[string]any) {
			if phase == "go list" {
				data["Sum"] = "wrong"
			}
		}},
		{"version", func(phase string, data map[string]any) {
			if phase == "syft version" {
				data["version"] = "wrong"
			}
		}},
		{"source", func(phase string, data map[string]any) {
			if phase == "syft version" {
				data["gitCommit"] = "wrong"
			}
		}},
		{"compiler", func(phase string, data map[string]any) {
			if phase == "syft version" {
				data["goVersion"] = "wrong"
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			temp := t.TempDir()
			path := filepath.Join(temp, "github-path")
			if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runner := &syftFixtureRunner{t: t, mutate: test.mutate}
			err := ReleaseSyft(t.Context(), ReleaseSyftOptions{
				Root: syftFixtureRoot(t), RunnerTemp: temp,
				GitHubPath: path, Runner: runner,
			})
			if err == nil {
				t.Fatal("unqualified metadata accepted")
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != "original\n" {
				t.Fatalf("GITHUB_PATH changed: %q %v", data, readErr)
			}
			if _, err := os.Lstat(filepath.Join(temp, "gobfd-syft")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial installation retained: %v", err)
			}
		})
	}
}

func TestReleaseSyftBuildFailureAndCancellationNeverPublish(t *testing.T) {
	t.Parallel()
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			t.Parallel()
			temp := t.TempDir()
			path := filepath.Join(temp, "github-path")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantErr := errors.New("build failed")
			runner := &syftFixtureRunner{t: t, build: func(spec CommandSpec) error {
				if err := os.WriteFile(spec.Args[len(spec.Args)-2], []byte("partial compiler output"), 0o755); err != nil {
					t.Fatal(err)
				}
				if cancelled {
					cancel()
					return context.Canceled
				}
				return wantErr
			}}
			err := ReleaseSyft(ctx, ReleaseSyftOptions{
				Root: syftFixtureRoot(t), RunnerTemp: temp, GitHubPath: path,
				Runner: runner,
			})
			if cancelled {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || len(data) != 0 {
				t.Fatalf("published path on failure: %q %v", data, readErr)
			}
			if _, err := os.Lstat(filepath.Join(temp, "gobfd-syft")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned partial installation retained: %v", err)
			}
		})
	}
}

func TestReleaseSyftRejectsCollisionsAndNonregularBuildOutputs(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{
		"installation collision", "symlink parent", "symlink output", "directory output",
		"cancel after build",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			temp := t.TempDir()
			path := filepath.Join(temp, "github-path")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(t.TempDir(), "foreign")
			if err := os.WriteFile(foreign, []byte("foreign"), 0o755); err != nil {
				t.Fatal(err)
			}
			runnerTemp := temp
			if kind == "installation collision" {
				if err := os.Mkdir(filepath.Join(temp, "gobfd-syft"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink parent" {
				runnerTemp = filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(temp, runnerTemp); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runner := &syftFixtureRunner{t: t}
			runner.build = func(spec CommandSpec) error {
				output := spec.Args[len(spec.Args)-2]
				if kind == "symlink output" {
					return os.Symlink(foreign, output)
				}
				if kind == "directory output" {
					return os.Mkdir(output, 0o700)
				}
				if kind == "cancel after build" {
					cancel()
				}
				return os.WriteFile(output, []byte("fixture"), 0o755)
			}
			err := ReleaseSyft(ctx, ReleaseSyftOptions{
				Root: syftFixtureRoot(t), RunnerTemp: runnerTemp,
				GitHubPath: path, Runner: runner,
			})
			if err == nil {
				t.Fatal("invalid installation accepted")
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || len(data) != 0 {
				t.Fatalf("published path on failure: %q %v", data, readErr)
			}
			data, readErr = os.ReadFile(foreign)
			if readErr != nil || string(data) != "foreign" {
				t.Fatalf("foreign executable changed: %q %v", data, readErr)
			}
		})
	}
}

func TestInstallSyftPreservesReplacedPathsAndForeignData(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{"file", "root", "symlink", "collision"} {
		t.Run(replacement, func(t *testing.T) {
			t.Parallel()
			temp := t.TempDir()
			installDir := filepath.Join(temp, "syft")
			foreign := filepath.Join(temp, "foreign")
			if err := os.Mkdir(foreign, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(foreign, "marker")
			if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			runner := &syftFixtureRunner{t: t, before: func(spec CommandSpec) error {
				if spec.Name != "syft" {
					return nil
				}
				switch replacement {
				case "file":
					name := filepath.Join(installDir, "stage", "syft")
					if err := os.Rename(name, name+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(name, []byte("foreign"), 0o755); err != nil {
						t.Fatal(err)
					}
				case "root", "symlink":
					if err := os.Rename(installDir, installDir+".original"); err != nil {
						t.Fatal(err)
					}
					if replacement == "root" {
						if err := os.Rename(foreign, installDir); err != nil {
							t.Fatal(err)
						}
						marker = filepath.Join(installDir, "marker")
					} else if err := os.Symlink(foreign, installDir); err != nil {
						t.Fatal(err)
					}
				case "collision":
					if err := os.WriteFile(filepath.Join(installDir, "bin", "syft"), []byte("foreign"), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}}
			if _, err := InstallSyft(t.Context(), SyftOptions{
				Root: syftFixtureRoot(t), InstallDir: installDir,
				Runner: runner,
			}); err == nil {
				t.Fatal("path replacement accepted")
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "foreign" {
				t.Fatalf("foreign data changed: %q %v", data, err)
			}
			if replacement == "file" || replacement == "collision" {
				name := filepath.Join(installDir, "stage", "syft")
				if replacement == "collision" {
					name = filepath.Join(installDir, "bin", "syft")
				}
				data, err := os.ReadFile(name)
				if err != nil || string(data) != "foreign" {
					t.Fatalf("replacement deleted: %q %v", data, err)
				}
			}
		})
	}
}

func TestExecRunnerRequiresBoundAllowlistedSyftAndPreservesCancellation(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "executable")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, spec := range []CommandSpec{{Name: "syft"}, {Name: "go", Executable: file}, {Name: "xz", Executable: file}} {
		if err := (ExecRunner{}).RunCommand(ctx, spec); !errors.Is(err, errInvalidConfig) {
			t.Fatalf("invalid descriptor pairing accepted: %v", err)
		}
	}
	if err := (ExecRunner{}).RunCommand(ctx, CommandSpec{Name: "syft", Executable: file}); !errors.Is(err,
		context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
