//go:build linux

package cirunner

import (
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
	"time"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

func TestSyftCompilerArgumentsUseActualIdentityAndClosedMounts(t *testing.T) {
	t.Parallel()
	for _, identity := range []struct{ uid, gid int }{{1007, 1023}, {65532, 65531}} {
		t.Run(stringIdentity(identity.uid, identity.gid), func(t *testing.T) {
			t.Parallel()
			args := syftCompilerArguments("/checkout", "/runner/private", "/runner/gobfd-cictl-20-2", identity.uid,
				identity.gid, "owned", "token")
			for _, required := range []string{
				"--user", stringIdentity(identity.uid, identity.gid), "--read-only", "--cap-drop=ALL",
				"--security-opt=no-new-privileges",
				"--cpus=2", "--memory=3221225472", "--memory-swap=3221225472", "--pids-limit=512",
				toolprofile.CompilerImage, "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "600s",
				"type=bind,src=/checkout,dst=/checkout,readonly", "type=bind,src=/runner/private,dst=/runner/private",
				"type=bind,src=/runner/gobfd-cictl-20-2,dst=/runner/gobfd-cictl-20-2,readonly",
				"GITHUB_PATH=/runner/private/github-path", "RUNNER_TEMP=/runner/private/install",
				"GOCACHE=/runner/private/cache/build", "GOMODCACHE=/runner/private/cache/mod",
				"GOPATH=/runner/private/cache/go", "GOSUMDB=sum.golang.org",
				"CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off",
			} {
				if !slices.Contains(args, required) {
					t.Errorf("missing compiler argument %q", required)
				}
			}
			for _, forbidden := range []string{
				"docker.sock", "podman.sock", "GH_TOKEN", "GITHUB_TOKEN", "--privileged",
				"--cap-add", "--foreground", "--compiler-container", "GOROOT=", "GOTOOLDIR=", "/out",
			} {
				for _, arg := range args {
					if strings.Contains(arg, forbidden) {
						t.Errorf("unexpected compiler argument %q", arg)
					}
				}
			}
			if slices.Contains(args, "sh") || slices.Contains(args, "bash") {
				t.Fatal("compiler worker uses a shell")
			}
			if !slices.Equal(args[len(args)-3:], []string{"600s", "/runner/gobfd-cictl-20-2", "release-syft"}) {
				t.Fatalf("worker does not invoke the existing native installer: %q", args)
			}
		})
	}
}

func stringIdentity(uid, gid int) string {
	return fmt.Sprintf("%d:%d", uid, gid)
}

type syftCompilerFixture struct {
	t                                               *testing.T
	root, temp, executable, path, work, name, token string
	fault                                           string
	id                                              string
	exists, removed                                 bool
	cleanupAfterCancel                              bool
	cancel                                          context.CancelFunc
	specs                                           []CommandSpec
	worker                                          *syftFixtureRunner
}

func newSyftCompilerFixture(t *testing.T, fault string) *syftCompilerFixture {
	t.Helper()
	fixture := &syftCompilerFixture{
		t: t, root: syftFixtureRoot(t), temp: t.TempDir(), fault: fault,
		id: strings.Repeat("a", 64), worker: &syftFixtureRunner{t: t},
	}
	fixture.executable = filepath.Join(fixture.temp, "gobfd-cictl-20-2")
	fixture.path = filepath.Join(fixture.temp, "github-path")
	if err := os.WriteFile(fixture.executable, []byte("prebuilt fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *syftCompilerFixture) options() ReleaseSyftOptions {
	return ReleaseSyftOptions{
		Root: fixture.root, RunnerTemp: fixture.temp, GitHubPath: fixture.path,
		Environment: []string{
			"PATH=/host/setup-go", "GH_TOKEN=never-forward", "GITHUB_TOKEN=never-forward",
			"GOROOT=/host/go", "DOCKER_HOST=tcp://foreign",
		}, Runner: fixture,
	}
}

func (fixture *syftCompilerFixture) buildInfo(io.ReaderAt) (*debug.BuildInfo, error) {
	info := &debug.BuildInfo{
		GoVersion: runtime.Version(), Path: toolprofile.SyftCommand,
		Main: debug.Module{Path: toolprofile.SyftModule, Version: syftFixtureVersion, Sum: toolprofile.SyftSourceSum},
	}
	if fixture.fault == "wrong source sum" {
		info.Main.Sum = "h1:foreign"
	}
	return info, nil
}

func (fixture *syftCompilerFixture) RunCommand(ctx context.Context, spec CommandSpec) error {
	fixture.t.Helper()
	fixture.specs = append(fixture.specs, spec)
	if spec.Name == "syft" {
		if fixture.fault == "host probe failure" {
			return syscall.EIO
		}
		if !fixture.removed {
			fixture.t.Fatal("host executable probed before container cleanup")
		}
		return fixture.worker.RunCommand(ctx, spec)
	}
	if spec.Name != "docker" || !slices.Equal(spec.Env, []string{"PATH=/usr/bin:/bin", "DOCKER_HOST=tcp://foreign"}) {
		fixture.t.Fatalf("unexpected host command/environment: %s %q", spec.Name, spec.Env)
	}
	if spec.Args[0] == "ps" || spec.Args[0] == "rm" {
		if ctx.Err() != nil {
			fixture.t.Fatal("cleanup inherited cancellation")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 31*time.Second {
			fixture.t.Fatal("cleanup has no finite independent deadline")
		}
		if fixture.fault == "cancel create" || fixture.fault == "cancel worker" {
			fixture.cleanupAfterCancel = true
		}
	}
	switch spec.Args[0] {
	case "create":
		fixture.exists = fixture.fault != "create absent failure"
		for index, arg := range spec.Args {
			if arg == "--name" {
				fixture.name = spec.Args[index+1]
			}
			if arg == "--label" {
				fixture.token = strings.TrimPrefix(spec.Args[index+1], syftCompilerOwnerLabel+"=")
			}
			if directory, found := strings.CutPrefix(arg, "RUNNER_TEMP="); found {
				fixture.work = filepath.Dir(directory)
			}
		}
		if fixture.fault == "cancel create" {
			fixture.cancel()
			return ctx.Err()
		}
		if fixture.fault == "create failure" || fixture.fault == "create absent failure" {
			return syscall.EIO
		}
		id := fixture.id
		if fixture.fault == "truncated create ID" {
			id = id[:12]
		}
		_, err := io.WriteString(spec.Stdout, id+"\n")
		return err
	case "ps":
		if fixture.exists {
			_, err := io.WriteString(spec.Stdout, fixture.id+"\n")
			return err
		}
		return nil
	case "inspect":
		identity := syftCompilerIdentity{
			ID: fixture.id, Name: "/" + fixture.name, Image: toolprofile.CompilerImage,
			Owner: fixture.token,
		}
		switch fixture.fault {
		case "foreign owner":
			identity.Owner = "foreign"
		case "foreign image":
			identity.Image = "foreign"
		case "foreign name":
			identity.Name = "/foreign"
		case "foreign ID":
			identity.ID = strings.Repeat("b", 64)
		case "nonzero worker":
			identity.ExitCode = 124
		}
		return json.NewEncoder(spec.Stdout).Encode(identity)
	case "start":
		if fixture.fault == "cancel worker" {
			fixture.cancel()
			return ctx.Err()
		}
		if fixture.fault == "worker failure" {
			return syscall.EIO
		}
		return ReleaseSyft(ctx, ReleaseSyftOptions{Root: fixture.root, RunnerTemp: filepath.Join(fixture.work,
			"install"), GitHubPath: filepath.Join(fixture.work, "github-path"), Runner: fixture.worker})
	case "rm":
		if fixture.fault == "cleanup failure" {
			return syscall.EIO
		}
		if !slices.Equal(spec.Args, []string{"rm", "--force", fixture.id}) {
			fixture.t.Fatalf("cleanup did not use the owned full ID: %q", spec.Args)
		}
		fixture.exists, fixture.removed = false, true
		return nil
	default:
		fixture.t.Fatalf("unexpected Docker operation %q", spec.Args)
		return nil
	}
}

func TestSyftDockerTransportEnvironmentIsHostOnly(t *testing.T) {
	t.Parallel()
	transport := []string{
		"DOCKER_HOST=unix:///run/user/1007/podman/podman.sock", "DOCKER_TLS=1", "DOCKER_TLS_VERIFY=1",
		"DOCKER_CERT_PATH=/private/certs", "DOCKER_CONFIG=/private/docker", "DOCKER_CONTEXT=private", "HOME=/home/runner",
	}
	environment := append(slices.Clone(transport), "GH_TOKEN=never-forward", "GITHUB_TOKEN=never-forward",
		"GOROOT=/host/go", "GOFLAGS=-host-only", "PATH=/host/bin", "UNRELATED=never-forward")
	want := append([]string{"PATH=/usr/bin:/bin"}, transport...)
	if got := syftDockerEnvironment(environment); !slices.Equal(got, want) {
		t.Fatalf("host Docker environment = %q, want %q", got, want)
	}
	args := syftCompilerArguments("/checkout", "/private/compiler", "/runner/gobfd-cictl-20-2", 1007, 1023,
		"owned", "token")
	for _, entry := range transport {
		if slices.Contains(args, entry) {
			t.Fatalf("host Docker transport environment reached compiler argv: %q", entry)
		}
	}
}

func TestReleaseSyftCompilerRetainsVerifiedHostPathAfterCleanup(t *testing.T) {
	t.Parallel()
	fixture := newSyftCompilerFixture(t, "")
	if err := releaseSyftCompiler(t.Context(), fixture.options(), fixture.executable, os.Getuid(), os.Getgid(),
		fixture.buildInfo); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture.path)
	want := filepath.Join(fixture.work, "install", "gobfd-syft", "bin")
	if err != nil || string(data) != want+"\n" {
		t.Fatalf("host PATH = %q, %v", data, err)
	}
	if !fixture.removed || fixture.exists {
		t.Fatal("owned compiler container retained after success")
	}
	for _, path := range []string{
		fixture.executable, filepath.Join(fixture.work, "cache"),
		filepath.Join(fixture.work, "github-path"),
	} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("transient path retained: %s: %v", path, statErr)
		}
	}
	info, err := os.Lstat(filepath.Join(want, "syft"))
	if err != nil || info.Mode().Perm() != 0o755 || !info.Mode().IsRegular() {
		t.Fatalf("verified executable not retained at host path: %v", err)
	}
	if relative, err := filepath.Rel(filepath.Join(fixture.root, "dist"), want); err != nil ||
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("installation is inside dist: %s: %v", relative, err)
	}
	for _, spec := range fixture.specs {
		if strings.Contains(strings.Join(append(slices.Clone(spec.Args), spec.Env...), "\n"), "never-forward") {
			t.Fatal("host secret reached a compiler/probe command")
		}
	}
}

func TestReleaseSyftCompilerFailureDoesNotPublishHostPath(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{
		"create failure", "create absent failure", "truncated create ID",
		"worker failure", "nonzero worker", "wrong source sum", "host probe failure", "cancel create",
		"cancel worker", "cleanup failure", "foreign owner", "foreign image", "foreign name", "foreign ID",
	} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture := newSyftCompilerFixture(t, fault)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.cancel = cancel
			err := releaseSyftCompiler(ctx, fixture.options(), fixture.executable, os.Getuid(), os.Getgid(), fixture.buildInfo)
			if err == nil {
				t.Fatal("fault accepted")
			}
			if strings.HasPrefix(fault, "cancel") && (!errors.Is(err, context.Canceled) || !fixture.cleanupAfterCancel) {
				t.Fatalf("cancellation or independent cleanup missing: %v", err)
			}
			data, readErr := os.ReadFile(fixture.path)
			if readErr != nil || len(data) != 0 {
				t.Fatalf("host PATH published on failure: %q: %v", data, readErr)
			}
			foreign := strings.HasPrefix(fault, "foreign") || fault == "cleanup failure"
			if foreign {
				if fixture.removed || !fixture.exists {
					t.Fatal("unconfirmed/foreign compiler container was removed")
				}
				if _, err := os.Stat(fixture.work); err != nil {
					t.Fatalf("bind files removed without confirmed container termination: %v", err)
				}
			} else {
				if fixture.exists {
					t.Fatal("owned compiler container remains after failure")
				}
				if _, err := os.Lstat(fixture.work); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("owned compiler files remain after failure: %v", err)
				}
			}
		})
	}
}

func TestSyftCompilerCleanupRemovesReadonlyCacheWithoutFollowingLinks(t *testing.T) {
	t.Parallel()
	files, prepareErr := prepareSyftCompilerFiles(t.TempDir())
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(files.directory, "cache", "mod", "module")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "source"), []byte("cache"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := files.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(files.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned readonly cache retained: %v", err)
	}
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("cleanup followed a cache symlink: %q %v", data, err)
	}
}

func TestSyftCompilerCleanupPreservesReplacedOwnedDirectories(t *testing.T) {
	t.Parallel()
	for _, selected := range []string{"root", "cache", "install"} {
		t.Run(selected, func(t *testing.T) {
			t.Parallel()
			files, prepareErr := prepareSyftCompilerFiles(t.TempDir())
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			path := files.directory
			if selected != "root" {
				path = filepath.Join(path, selected)
			}
			if err := os.Rename(path, path+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(path, "foreign")
			if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := files.close(); err == nil {
				t.Fatal("foreign directory replacement accepted for cleanup")
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "foreign" {
				t.Fatalf("foreign directory changed: %q %v", data, err)
			}
		})
	}
}

func TestReleaseSyftCompilerRejectsUnsafeHostPathsBeforeDocker(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{
		"relative checkout", "dist temporary parent", "PATH symlink",
		"controller symlink", "controller mode", "controller name", "mount comma",
	} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture := newSyftCompilerFixture(t, "")
			options := fixture.options()
			executable := fixture.executable
			switch fault {
			case "relative checkout":
				options.Root = "checkout"
			case "dist temporary parent":
				options.RunnerTemp = filepath.Join(options.Root, "dist")
				if err := os.Mkdir(options.RunnerTemp, 0o700); err != nil {
					t.Fatal(err)
				}
			case "PATH symlink":
				options.GitHubPath += "-link"
				if err := os.Symlink(fixture.path, options.GitHubPath); err != nil {
					t.Fatal(err)
				}
			case "controller symlink":
				if err := os.Rename(executable, executable+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(executable+"-original", executable); err != nil {
					t.Fatal(err)
				}
			case "controller mode":
				if err := os.Chmod(executable, 0o644); err != nil {
					t.Fatal(err)
				}
			case "controller name":
				executable = filepath.Join(fixture.temp, "cictl")
			case "mount comma":
				options.Root = filepath.Join(fixture.root, "comma,path")
				if err := os.Mkdir(options.Root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := releaseSyftCompiler(t.Context(), options, executable, os.Getuid(), os.Getgid(),
				fixture.buildInfo); err == nil {
				t.Fatal("unsafe host path accepted")
			}
			if len(fixture.specs) != 0 {
				t.Fatal("unsafe host path reached Docker")
			}
		})
	}
}
