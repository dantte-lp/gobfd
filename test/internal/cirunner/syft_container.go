//go:build linux

package cirunner

import (
	"context"
	"crypto/rand"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const syftCompilerOwnerLabel = "io.gobfd.syft.owner"

var (
	syftCompilerID    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	syftBootstrapName = regexp.MustCompile(`^gobfd-cictl-[1-9][0-9]*-[1-9][0-9]*$`)
)

// ReleaseSyftCompiler runs the existing source installer with the actual host identity.
func ReleaseSyftCompiler(ctx context.Context, options ReleaseSyftOptions) error {
	executable, err := sourceControllerExecutable()
	if err != nil {
		return err
	}
	return releaseSyftCompiler(ctx, options, executable, os.Getuid(), os.Getgid(), buildinfo.Read)
}

func sourceControllerExecutable() (string, error) {
	if os.Getuid() <= 0 {
		return "", fmt.Errorf("source compiler requires the nonroot host runner identity: %w", errInvalidConfig)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve prebuilt source controller: %w", err)
	}
	running, runningErr := os.Stat("/proc/self/exe")
	current, currentErr := os.Lstat(executable)
	if runningErr != nil || currentErr != nil || !os.SameFile(running, current) {
		return "", fmt.Errorf("prebuilt source controller path differs from the running executable: %w",
			errors.Join(runningErr, currentErr, errInvalidConfig))
	}
	return executable, nil
}

func releaseSyftCompiler(
	ctx context.Context, options ReleaseSyftOptions, executable string, uid, gid int,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) (returnErr error) {
	if err := validateSyftCompilerHost(options, executable, uid, gid); err != nil {
		return err
	}
	bootstrap, err := openSyftBootstrap(executable)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, bootstrap.close()) }()
	files, err := prepareSyftCompilerFiles(options.RunnerTemp)
	if err != nil {
		return err
	}
	container := syftCompilerContainer{
		runner: options.Runner, token: rand.Text(), directory: options.Root,
		environment: syftDockerEnvironment(options.Environment),
	}
	container.name = "gobfd-syft-" + strings.ToLower(container.token)
	defer func() {
		cleanupErr := container.closeFiles(ctx, files)
		bootstrap.preserved = cleanupErr != nil
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	if verifyErr := files.verify(); verifyErr != nil {
		return verifyErr
	}
	if verifyErr := bootstrap.verify(); verifyErr != nil {
		return verifyErr
	}
	args := syftCompilerArguments(options.Root, files.directory, executable, uid, gid, container.name, container.token)
	if runErr := container.run(ctx, args); runErr != nil {
		return runErr
	}
	if cleanupErr := container.cleanupBounded(ctx); cleanupErr != nil {
		return cleanupErr
	}
	return files.publish(ctx, options, bootstrap, readBuildInfo)
}

func validateSyftCompilerHost(options ReleaseSyftOptions, executable string, uid, gid int) error {
	if options.Runner == nil || uid < 0 || gid < 0 {
		return fmt.Errorf("syft compiler requires a nonroot Linux host and command runner: %w", errInvalidConfig)
	}
	if err := validateSyftCompilerMounts(options.Root, options.RunnerTemp, filepath.Dir(executable)); err != nil {
		return err
	}
	if err := validateGitHubPath(options.GitHubPath); err != nil {
		return err
	}
	for _, path := range []string{options.RunnerTemp, filepath.Dir(options.GitHubPath), options.GitHubPath} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect runner-owned Syft path: %w", err)
		}
		if !syftRunnerOwns(info) {
			return fmt.Errorf("syft temporary/PATH location is not runner-owned: %w", errInvalidConfig)
		}
	}
	if err := validateSyftOutsideDist(options.Root, options.RunnerTemp); err != nil {
		return err
	}
	if filepath.Dir(executable) != options.RunnerTemp || !syftBootstrapName.MatchString(filepath.Base(executable)) {
		return fmt.Errorf("syft controller is not the run-scoped prebuilt executable: %w", errInvalidConfig)
	}
	return nil
}

func validateSyftCompilerMounts(paths ...string) error {
	for _, path := range paths {
		if _, err := validateAbsoluteExistingDirectory(path, "Syft compiler mount"); err != nil {
			return err
		}
		if hasControl(path) || strings.Contains(path, ",") {
			return fmt.Errorf("syft compiler mount cannot contain controls or commas: %w", errInvalidConfig)
		}
	}
	return nil
}

func syftRunnerOwns(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(owner.Uid) == int64(os.Getuid())
}

func (files *syftCompilerFiles) publish(
	ctx context.Context, options ReleaseSyftOptions, bootstrap *syftBootstrap,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish container-built Syft: %w", err)
	}
	installation, err := files.openInstallation()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, installation.Close()) }()
	if verifyErr := verifyContainerSyft(ctx, options.Runner, installation, readBuildInfo); verifyErr != nil {
		return verifyErr
	}
	if cleanupErr := files.removeTransient(); cleanupErr != nil {
		return cleanupErr
	}
	if cleanupErr := bootstrap.remove(); cleanupErr != nil {
		return cleanupErr
	}
	if verifyErr := installation.verify(); verifyErr != nil {
		return verifyErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("publish container-built Syft PATH: %w", contextErr)
	}
	published, err := appendGitHubPath(options.GitHubPath, filepath.Join(installation.directory, "bin"))
	installation.retained, files.retained = published, published
	return err
}

func verifyContainerSyft(
	ctx context.Context, runner SpecRunner, installation *SyftInstallation,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) error {
	info, err := readBuildInfo(installation.executable)
	if err != nil {
		return fmt.Errorf("read bound Syft Go build information: %w", err)
	}
	if info == nil || info.Path != toolprofile.SyftCommand || info.Main.Path != toolprofile.SyftModule ||
		info.Main.Sum != toolprofile.SyftSourceSum || info.Main.Replace != nil || info.GoVersion != runtime.Version() ||
		!strings.HasPrefix(info.Main.Version, "v") || len(info.Main.Version) < 2 || hasControl(info.Main.Version) {
		return fmt.Errorf("container-built Syft differs from the frozen source/compiler: %w", errInvalidConfig)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return verifySyftVersion(probeCtx, runner, installation, []string{"PATH=/usr/bin:/bin"}, info.Main.Version)
}

type syftBootstrap struct {
	root      *os.Root
	file      *os.File
	info      os.FileInfo
	name      string
	removed   bool
	preserved bool
}

func openSyftBootstrap(path string) (*syftBootstrap, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open Syft controller parent: %w", err)
	}
	info, err := root.Lstat(filepath.Base(path))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm() != 0o755 {
		return nil, errors.Join(
			fmt.Errorf("syft controller must be a regular0755 executable: %w", errors.Join(err, errInvalidConfig)),
			root.Close(),
		)
	}
	file, err := openRootedRegularFile(root, filepath.Base(path), info, "Syft controller")
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return &syftBootstrap{root: root, file: file, info: info, name: filepath.Base(path)}, nil
}

func (bootstrap *syftBootstrap) verify() error {
	if err := verifyOpenedRegularFile(bootstrap.file, bootstrap.info, "prebuilt Syft controller"); err != nil {
		return err
	}
	return verifyRootedPathIdentity(bootstrap.root, bootstrap.name, bootstrap.file, "Syft controller")
}

func (bootstrap *syftBootstrap) remove() error {
	if bootstrap.removed {
		return nil
	}
	if err := verifyRootedPathIdentity(bootstrap.root, bootstrap.name, bootstrap.file, "Syft controller"); err != nil {
		return err
	}
	if err := bootstrap.root.Remove(bootstrap.name); err != nil {
		return fmt.Errorf("remove run-scoped Syft controller: %w", err)
	}
	bootstrap.removed = true
	return nil
}

func (bootstrap *syftBootstrap) close() error {
	var removeErr error
	if !bootstrap.preserved {
		removeErr = bootstrap.remove()
	}
	return errors.Join(removeErr, bootstrap.file.Close(), bootstrap.root.Close())
}

func (container *syftCompilerContainer) cleanupBounded(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return container.cleanup(cleanupCtx)
}

func (container *syftCompilerContainer) closeFiles(ctx context.Context, files *syftCompilerFiles) error {
	if err := container.cleanupBounded(ctx); err != nil {
		// A container with unconfirmed termination may still write its bind mounts.
		return errors.Join(err, files.closeHandles())
	}
	return files.close()
}

func syftCompilerArguments(repository, directory, executable string, uid, gid int, name, token string) []string {
	args := []string{
		"create", "--name", name, "--label", syftCompilerOwnerLabel + "=" + token,
		"--user", strconv.Itoa(uid) + ":" + strconv.Itoa(gid),
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--cpus=2", "--memory=3221225472", "--memory-swap=3221225472", "--pids-limit=512",
		"--workdir", repository, "--entrypoint", "/usr/bin/timeout",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=1073741824,mode=1777",
		"--mount", "type=bind,src=" + repository + ",dst=" + repository + ",readonly",
		"--mount", "type=bind,src=" + directory + ",dst=" + directory,
		"--mount", "type=bind,src=" + executable + ",dst=" + executable + ",readonly",
	}
	for _, value := range []string{
		"PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOENV=off",
		"GOWORK=off",
		"GOMAXPROCS=2", "GOMEMLIMIT=2GiB", "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org",
		"GOCACHE=" + filepath.Join(directory, "cache", "build"), "GOMODCACHE=" + filepath.Join(directory, "cache", "mod"),
		"GOPATH=" + filepath.Join(directory, "cache", "go"),
		"HOME=" + filepath.Join(directory, "cache"),
		"GOTMPDIR=" + filepath.Join(directory, "cache", "tmp"), "TMPDIR=" + filepath.Join(directory, "cache", "tmp"),
		"RUNNER_TEMP=" + filepath.Join(directory, "install"), "GITHUB_PATH=" + filepath.Join(directory, "github-path"),
	} {
		args = append(args, "--env", value)
	}
	return append(args, toolprofile.CompilerImage, "--signal=TERM", "--kill-after=5s", "600s", executable, "release-syft")
}

type syftCompilerContainer struct {
	runner                     SpecRunner
	name, token, id, directory string
	attempted, removed         bool
	environment                []string
}

func syftDockerEnvironment(environment []string) []string {
	result := []string{"PATH=/usr/bin:/bin"}
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch name {
		case "DOCKER_HOST", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_CONFIG", "DOCKER_CONTEXT", "HOME":
			result = append(result, entry)
		}
	}
	return result
}

type syftCompilerIdentity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Image    string `json:"image"`
	Owner    string `json:"owner"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exit_code"`
}

func (container *syftCompilerContainer) command(ctx context.Context, args []string, output io.Writer) error {
	if err := container.runner.RunCommand(ctx, CommandSpec{
		Name: "docker", Args: args, Dir: container.directory,
		Env: container.environment, Stdout: output, WaitDelay: 2 * time.Second,
	}); err != nil {
		return fmt.Errorf("control Syft compiler container: %w", err)
	}
	return nil
}

func (container *syftCompilerContainer) run(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	output := &synchronizedBoundedBuffer{limit: 1024}
	container.attempted = true
	if err := container.command(ctx, args, output); err != nil {
		return fmt.Errorf("create Syft compiler container: %w", err)
	}
	container.id = strings.TrimSpace(string(output.Bytes()))
	if !syftCompilerID.MatchString(container.id) {
		container.id = ""
		return fmt.Errorf("syft compiler create returned no full container ID: %w", errInvalidConfig)
	}
	if _, err := container.inspect(ctx, container.id); err != nil {
		return err
	}
	if err := container.command(ctx, []string{"start", "--attach", container.id}, io.Discard); err != nil {
		return err
	}
	identity, err := container.inspect(ctx, container.id)
	if err != nil {
		return err
	}
	if identity.Running || identity.ExitCode != 0 {
		return fmt.Errorf("syft compiler did not exit successfully (running=%t, exit=%d): %w", identity.Running,
			identity.ExitCode, errInvalidConfig)
	}
	return wrapOptional("finish Syft compiler", ctx.Err())
}

func (container *syftCompilerContainer) inspect(ctx context.Context, id string) (syftCompilerIdentity, error) {
	output := &synchronizedBoundedBuffer{limit: 4096}
	format := `{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Config.Image}},"owner":{{json (inde` +
		`x .Config.Labels "` + syftCompilerOwnerLabel +
		`")}},"running":{{json .State.Running}},"exit_code":{{json .State.ExitCode}}}`
	if err := container.command(ctx, []string{"inspect", "--type=container", "--format", format, id}, output); err != nil {
		return syftCompilerIdentity{}, err
	}
	data := output.Bytes()
	if err := validateStrictJSONDocument(data, "Syft compiler identity"); err != nil {
		return syftCompilerIdentity{}, err
	}
	var identity syftCompilerIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return identity, fmt.Errorf("decode Syft compiler identity: %w", err)
	}
	if !syftCompilerID.MatchString(identity.ID) || identity.ID != id || identity.Name != "/"+container.name ||
		identity.Image != toolprofile.CompilerImage || identity.Owner != container.token {
		return identity, fmt.Errorf("refuse foreign Syft compiler container: %w", errInvalidConfig)
	}
	return identity, nil
}

func (container *syftCompilerContainer) cleanup(ctx context.Context) error {
	if !container.attempted || container.removed {
		return nil
	}
	output := &synchronizedBoundedBuffer{limit: 1024}
	filter := "name=^/" + container.name + "$"
	if container.id != "" {
		filter = "id=" + container.id
	}
	if err := container.command(ctx, []string{
		"ps", "--all", "--no-trunc", "--filter", filter, "--format",
		"{{.ID}}",
	}, output); err != nil {
		return err
	}
	id := strings.TrimSpace(string(output.Bytes()))
	if id == "" {
		container.removed = true
		return nil
	}
	if !syftCompilerID.MatchString(id) || (container.id != "" && id != container.id) {
		return fmt.Errorf("ambiguous Syft compiler cleanup identity: %w", errInvalidConfig)
	}
	if _, err := container.inspect(ctx, id); err != nil {
		return err
	}
	container.id = id
	if err := container.command(ctx, []string{"rm", "--force", id}, io.Discard); err != nil {
		return err
	}
	output = &synchronizedBoundedBuffer{limit: 1024}
	if err := container.command(ctx, []string{
		"ps", "--all", "--no-trunc", "--filter", "id=" + id, "--format",
		"{{.ID}}",
	}, output); err != nil {
		return err
	}
	if strings.TrimSpace(string(output.Bytes())) != "" {
		return fmt.Errorf("syft compiler still present after cleanup: %w", errInvalidConfig)
	}
	container.removed = true
	return nil
}

type syftCompilerFiles struct {
	directory    string
	parent, root *os.Root
	parentInfo   os.FileInfo
	owned        map[string]os.FileInfo
	retained     bool
}

func prepareSyftCompilerFiles(parentPath string) (_ *syftCompilerFiles, returnErr error) {
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open Syft compiler parent: %w", err)
	}
	files := &syftCompilerFiles{parent: parent, directory: filepath.Join(parentPath, ".gobfd-syft-compiler-"+
		rand.Text()), owned: make(map[string]os.FileInfo)}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, files.close())
		}
	}()
	files.parentInfo, err = parent.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("inspect Syft compiler parent: %w", err)
	}
	name := filepath.Base(files.directory)
	if mkdirErr := parent.Mkdir(name, 0o700); mkdirErr != nil {
		return nil, fmt.Errorf("create exclusive Syft compiler root: %w", mkdirErr)
	}
	files.owned["."], err = parent.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("capture Syft compiler root: %w", err)
	}
	files.root, err = parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open Syft compiler root: %w", err)
	}
	for _, name := range []string{"cache", filepath.Join("cache", "tmp"), "install"} {
		if mkdirErr := files.root.Mkdir(name, 0o700); mkdirErr != nil {
			return nil, fmt.Errorf("create private Syft compiler %s: %w", name, mkdirErr)
		}
		files.owned[name], err = files.root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("capture Syft compiler %s: %w", name, err)
		}
	}
	file, err := files.root.OpenFile("github-path", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create private Syft compiler PATH file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close private Syft compiler PATH file: %w", err)
	}
	return files, files.verify()
}

func (files *syftCompilerFiles) verify() error {
	parentInfo, err := os.Lstat(filepath.Dir(files.directory))
	if err != nil || !os.SameFile(parentInfo, files.parentInfo) {
		return fmt.Errorf("syft compiler parent identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	for name, expected := range files.owned {
		root, path := files.root, name
		if name == "." {
			root, path = files.parent, filepath.Base(files.directory)
		}
		current, err := root.Lstat(path)
		if err != nil || expected == nil || !current.IsDir() || current.Mode().Perm() != 0o700 ||
			!os.SameFile(current, expected) {
			return fmt.Errorf("syft compiler %s identity changed: %w", name, errors.Join(err, errInvalidConfig))
		}
	}
	return nil
}

func (files *syftCompilerFiles) openInstallation() (*SyftInstallation, error) {
	installation, err := files.openToolInstallation(syftName)
	if err != nil {
		return nil, err
	}
	if pathErr := files.verifyWorkerPath(installation.directory); pathErr != nil {
		installation.retained = true
		return nil, errors.Join(pathErr, installation.Close())
	}
	return installation, nil
}

func (files *syftCompilerFiles) openToolInstallation(command string) (_ *sourceInstallation, returnErr error) {
	directory := filepath.Join(files.directory, "install", "gobfd-"+command)
	if err := validateSourceInstallationPath(directory, command); err != nil {
		return nil, err
	}
	if err := files.verify(); err != nil {
		return nil, err
	}
	installation := &SyftInstallation{
		directory: filepath.Join(files.directory, "install", "gobfd-"+command),
		name:      "gobfd-" + command, command: command, owned: make(map[string]os.FileInfo),
	}
	defer func() {
		if returnErr != nil {
			// The private parent is cleaned by files after confirmed container termination.
			installation.retained = true
			returnErr = errors.Join(returnErr, installation.Close())
		}
	}()
	var err error
	installation.parent, err = files.root.OpenRoot("install")
	if err != nil {
		return nil, fmt.Errorf("open host Syft installation parent: %w", err)
	}
	installation.parentInfo = files.owned["install"]
	installation.owned["."], err = installation.parent.Lstat(installation.name)
	if err != nil {
		return nil, fmt.Errorf("capture container-built Syft root: %w", err)
	}
	installation.root, err = installation.parent.OpenRoot(installation.name)
	if err != nil {
		return nil, fmt.Errorf("open container-built Syft root: %w", err)
	}
	for _, name := range []string{sourceStageDirectory, "bin"} {
		installation.owned[name], err = installation.root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("capture container-built Syft %s: %w", name, err)
		}
	}
	if openErr := installation.openExecutableAt(filepath.Join("bin", command)); openErr != nil {
		return nil, openErr
	}
	for _, owned := range []os.FileInfo{
		installation.owned["."], installation.owned[sourceStageDirectory],
		installation.owned["bin"], installation.executableInfo,
	} {
		if !syftRunnerOwns(owned) {
			return nil, fmt.Errorf("container-built Syft is not owned by the host runner: %w", errInvalidConfig)
		}
	}
	if verifyErr := installation.verify(); verifyErr != nil {
		return nil, verifyErr
	}
	if artifactErr := installation.capturePublisherProvenance(); artifactErr != nil {
		return nil, artifactErr
	}
	return installation, nil
}

func (installation *sourceInstallation) capturePublisherProvenance() error {
	if installation.toolName() != goreleaserName {
		return nil
	}
	info, err := installation.root.Lstat(goreleaserProvenance)
	if err != nil || !info.Mode().IsRegular() || !syftRunnerOwns(info) {
		return fmt.Errorf("publisher provenance is not an owned regular file: %w", errors.Join(err, errInvalidConfig))
	}
	installation.artifacts = map[string]os.FileInfo{goreleaserProvenance: info}
	return nil
}

func (files *syftCompilerFiles) verifyWorkerPath(directory string) error {
	return files.verifyWorkerPaths([]string{filepath.Join(directory, "bin")})
}

func (files *syftCompilerFiles) verifyWorkerPaths(directories []string) error {
	pathInfo, err := files.root.Lstat("github-path")
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Size() > githubPathLimit {
		return fmt.Errorf("private worker PATH is not a bounded regular file: %w", errors.Join(err, errInvalidConfig))
	}
	pathFile, err := openRootedRegularFile(files.root, "github-path", pathInfo, "private Syft PATH")
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(pathFile, githubPathLimit+1))
	checkErr := verifyOpenedRegularFile(pathFile, pathInfo, "private Syft PATH")
	closeErr := pathFile.Close()
	if readErr != nil || checkErr != nil || closeErr != nil {
		return errors.Join(readErr, checkErr, closeErr)
	}
	if string(data) != strings.Join(directories, "\n")+"\n" {
		return fmt.Errorf("worker PATH differs from the absolute host installation: %w", errInvalidConfig)
	}
	return nil
}

func (files *syftCompilerFiles) removeTransient() error {
	if err := files.verify(); err != nil {
		return err
	}
	if _, exists := files.owned["cache"]; exists {
		if err := removeSyftCompilerTree(files.root, "cache"); err != nil {
			return err
		}
		delete(files.owned, "cache")
		delete(files.owned, filepath.Join("cache", "tmp"))
	}
	info, err := files.root.Lstat("github-path")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > githubPathLimit {
		return fmt.Errorf("private Syft PATH file changed type or exceeded bound: %w", errors.Join(err, errInvalidConfig))
	}
	return wrapOptional("remove private Syft compiler PATH file", files.root.Remove("github-path"))
}

func (files *syftCompilerFiles) close() error {
	return errors.Join(files.removeOwned(), files.closeHandles())
}

func (files *syftCompilerFiles) removeOwned() error {
	if files.root == nil {
		if files.owned["."] == nil {
			return nil
		}
		current, err := files.parent.Lstat(filepath.Base(files.directory))
		if err != nil || !os.SameFile(current, files.owned["."]) {
			return errors.Join(err, errInvalidConfig)
		}
		return wrapOptional("remove unopened Syft compiler root", files.parent.Remove(filepath.Base(files.directory)))
	}
	if err := files.removeTransient(); err != nil {
		return err
	}
	if files.retained {
		return nil
	}
	if _, exists := files.owned["install"]; exists {
		if err := removeSyftCompilerTree(files.root, "install"); err != nil {
			return err
		}
	}
	return wrapOptional("remove Syft compiler root", files.parent.Remove(filepath.Base(files.directory)))
}

func (files *syftCompilerFiles) closeHandles() error {
	var result error
	if files.root != nil {
		result = files.root.Close()
		files.root = nil
	}
	if files.parent != nil {
		result = errors.Join(result, files.parent.Close())
		files.parent = nil
	}
	return result
}

func removeSyftCompilerTree(root *os.Root, name string) error {
	// Module cache directories are read-only; never follow their symlinks during cleanup.
	if err := fs.WalkDir(root.FS(), name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			info, err := root.Lstat(path)
			if err != nil {
				return fmt.Errorf("inspect owned Syft compiler directory: %w", err)
			}
			if !syftRunnerOwns(info) {
				return fmt.Errorf("syft cache directory owner changed: %w", errInvalidConfig)
			}
			return wrapOptional("set owned Syft cache directory mode", root.Chmod(path, 0o700))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("make owned Syft compiler files removable: %w", err)
	}
	return wrapOptional("remove owned Syft compiler files", root.RemoveAll(name))
}
