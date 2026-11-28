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
	"runtime/debug"
	"strings"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

// ReleaseSourceToolsCompiler installs both closed release prerequisites before PATH publication.
func ReleaseSourceToolsCompiler(ctx context.Context, options ReleaseSyftOptions) error {
	executable, err := sourceControllerExecutable()
	if err != nil {
		return err
	}
	return releaseSourceToolsCompiler(ctx, options, executable, os.Getuid(), os.Getgid(), buildinfo.Read)
}

type sourceToolsBatch struct {
	options       ReleaseSyftOptions
	profile       goreleaserProfile
	bootstrap     *syftBootstrap
	files         *syftCompilerFiles
	containers    []*syftCompilerContainer
	installations []*sourceInstallation
	preserve      bool
}

func releaseSourceToolsCompiler(
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
	batch := &sourceToolsBatch{options: options, bootstrap: bootstrap}
	defer func() { returnErr = errors.Join(returnErr, batch.close(ctx)) }()
	batch.profile, err = loadGoReleaserProfile(options.Root)
	if err != nil {
		return err
	}
	if profileErr := batch.profile.requireManifests(); profileErr != nil {
		return profileErr
	}
	batch.files, err = prepareSyftCompilerFiles(options.RunnerTemp)
	if err != nil {
		return err
	}
	for _, tool := range []string{syftName, goreleaserName} {
		if runErr := batch.runWorker(ctx, executable, uid, gid, tool); runErr != nil {
			return runErr
		}
	}
	return batch.publish(ctx, readBuildInfo)
}

func (batch *sourceToolsBatch) runWorker(
	ctx context.Context, executable string, uid, gid int, tool string,
) error {
	if err := batch.files.verify(); err != nil {
		return err
	}
	if err := batch.bootstrap.verify(); err != nil {
		return err
	}
	container := &syftCompilerContainer{
		runner: batch.options.Runner, token: rand.Text(),
		directory: batch.options.Root, environment: syftDockerEnvironment(batch.options.Environment),
	}
	container.name = "gobfd-source-" + strings.ToLower(container.token)
	batch.containers = append(batch.containers, container)
	args, err := sourceCompilerArguments(batch.options.Root, batch.files.directory, executable,
		uid, gid, container.name, container.token, tool)
	if err != nil {
		return err
	}
	if err := container.run(ctx, args); err != nil {
		return err
	}
	if cleanupErr := container.cleanupBounded(ctx); cleanupErr != nil {
		return cleanupErr
	}
	installation, openErr := batch.files.openToolInstallation(tool)
	if openErr != nil {
		batch.preserve = true
		return openErr
	}
	batch.installations = append(batch.installations, installation)
	return nil
}

func sourceCompilerArguments(repository, directory, executable string, uid, gid int,
	name, token, tool string,
) ([]string, error) {
	args := syftCompilerArguments(repository, directory, executable, uid, gid, name, token)
	switch tool {
	case syftName:
		return args, nil
	case goreleaserName:
		args[len(args)-1] = "release-goreleaser"
		return args, nil
	default:
		return nil, fmt.Errorf("source compiler worker is not allowlisted: %w", errInvalidConfig)
	}
}

func (batch *sourceToolsBatch) publish(
	ctx context.Context, readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) error {
	bins := []string{
		filepath.Join(batch.files.directory, "install", "gobfd-syft", "bin"),
		filepath.Join(batch.files.directory, "install", "gobfd-goreleaser", "bin"),
	}
	if err := batch.files.verifyWorkerPaths(bins); err != nil {
		return err
	}
	for _, installation := range batch.installations {
		if err := batch.verifyTool(ctx, installation, readBuildInfo); err != nil {
			return err
		}
	}
	if err := batch.files.removeTransient(); err != nil {
		return err
	}
	for _, installation := range batch.installations {
		if err := installation.verify(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish source tool PATH transaction: %w", err)
	}
	if err := batch.bootstrap.remove(); err != nil {
		return err
	}
	published, err := appendGitHubPaths(batch.options.GitHubPath, bins)
	batch.files.retained = published
	for _, installation := range batch.installations {
		installation.retained = published
	}
	return err
}

func (batch *sourceToolsBatch) verifyTool(
	ctx context.Context, installation *sourceInstallation,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) error {
	if installation.toolName() == syftName {
		return verifyContainerSyft(ctx, batch.options.Runner, installation, readBuildInfo)
	}
	if err := verifyGoReleaserReceipt(installation, batch.profile); err != nil {
		return err
	}
	return verifyGoReleaserBinary(ctx, batch.options.Runner, installation, batch.profile, readBuildInfo)
}

func (batch *sourceToolsBatch) close(ctx context.Context) error {
	var result error
	for _, container := range batch.containers {
		result = errors.Join(result, container.cleanupBounded(ctx))
	}
	if batch.files != nil && !batch.preserve && result == nil {
		result = batch.verifyInstallSet()
	}
	for _, installation := range batch.installations {
		if batch.preserve || result != nil {
			installation.retained = true
		}
		result = errors.Join(result, installation.Close())
	}
	batch.preserve = batch.preserve || result != nil
	if batch.files != nil {
		if batch.preserve {
			result = errors.Join(result, batch.writeRecovery())
			result = errors.Join(result, batch.files.closeHandles())
		} else {
			result = errors.Join(result, batch.files.close())
		}
	}
	batch.bootstrap.preserved = batch.preserve || result != nil
	return errors.Join(result, batch.bootstrap.close())
}

func (batch *sourceToolsBatch) writeRecovery() error {
	if err := batch.files.verify(); err != nil {
		return err
	}
	type identity struct {
		Name  string `json:"name"`
		ID    string `json:"id"`
		Owner string `json:"owner"`
	}
	receipt := struct {
		Directory  string     `json:"directory"`
		Controller string     `json:"controller"`
		Image      string     `json:"image"`
		Containers []identity `json:"containers"`
	}{
		Directory:  batch.files.directory,
		Controller: filepath.Join(filepath.Dir(batch.files.directory), batch.bootstrap.name),
		Image:      toolprofile.CompilerImage,
	}
	for _, container := range batch.containers {
		if container.attempted {
			receipt.Containers = append(receipt.Containers,
				identity{Name: container.name, ID: container.id, Owner: container.token})
		}
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode source tool recovery ownership: %w", err)
	}
	const name = "recovery.json"
	if _, statErr := batch.files.root.Lstat(name); !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("source tool recovery receipt collision: %w", errors.Join(statErr, errInvalidConfig))
	}
	return writeRootedModeArtifact(batch.files.root, name, data, "source tool recovery ownership",
		goreleaserMetadataLimit, 0o600)
}

func (batch *sourceToolsBatch) verifyInstallSet() (returnErr error) {
	if err := batch.files.verify(); err != nil {
		return err
	}
	root, err := batch.files.root.OpenRoot("install")
	if err != nil {
		return fmt.Errorf("open source tool ownership set: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return fmt.Errorf("read source tool ownership set: %w", err)
	}
	if len(entries) != len(batch.installations) {
		return fmt.Errorf("source tool ownership set contains unconfirmed output: %w", errInvalidConfig)
	}
	for _, installation := range batch.installations {
		info, err := root.Lstat(installation.name)
		if err != nil || !os.SameFile(info, installation.owned["."]) {
			return fmt.Errorf("source tool ownership set changed: %w", errors.Join(err, errInvalidConfig))
		}
	}
	return nil
}
