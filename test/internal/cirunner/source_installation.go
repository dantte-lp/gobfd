package cirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	goModFilename        = "go.mod"
	sourceStageDirectory = "stage"
)

func (installation *sourceInstallation) publishPath(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish source tool prerequisite: %w", err)
	}
	if err := installation.verify(); err != nil {
		return err
	}
	retained, err := appendGitHubPath(path, filepath.Join(installation.directory, "bin"))
	installation.retained = retained
	return err
}

type sourceInstallation struct {
	command        string
	parent         *os.Root
	root           *os.Root
	directory      string
	name           string
	parentInfo     os.FileInfo
	owned          map[string]os.FileInfo
	artifacts      map[string]os.FileInfo
	executable     *os.File
	executableInfo os.FileInfo
	executableAt   string
	buildStarted   bool
	retained       bool
}

func (installation *sourceInstallation) toolName() string {
	if installation.command == "" {
		return syftName
	}
	return installation.command
}

func (installation *sourceInstallation) publish() error {
	if err := installation.verify(); err != nil {
		return err
	}
	if _, err := installation.root.Lstat(filepath.Join("bin", installation.toolName())); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("source tool executable publication collision: %w", errors.Join(err, errInvalidConfig))
	}
	if err := installation.root.Rename(filepath.Join(sourceStageDirectory, installation.toolName()),
		filepath.Join("bin", installation.toolName())); err != nil {
		return fmt.Errorf("publish verified source tool executable: %w", err)
	}
	installation.executableAt = filepath.Join("bin", installation.toolName())
	return installation.verify()
}

func prepareSourceInstallation(directory, command string, mkdir func(*os.Root, string,
	os.FileMode) error,
) (_ *sourceInstallation, returnErr error) {
	if err := validateSourceInstallationPath(directory, command); err != nil {
		return nil, err
	}
	parentPath, err := validateAbsoluteExistingDirectory(filepath.Dir(directory), "source tool installation parent")
	if err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open source tool installation parent: %w", err)
	}
	installation := &sourceInstallation{
		parent: parent, directory: directory, name: filepath.Base(directory), command: command,
		owned: make(map[string]os.FileInfo),
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, installation.Close())
		}
	}()
	installation.parentInfo, err = parent.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("inspect source tool installation parent: %w", err)
	}
	if mkdirErr := mkdir(parent, installation.name, 0o700); mkdirErr != nil {
		return nil, fmt.Errorf("create exclusive source tool installation: %w", mkdirErr)
	}
	installation.owned["."], err = parent.Lstat(installation.name)
	if err != nil {
		return nil, fmt.Errorf("inspect created source tool installation: %w", err)
	}
	installation.root, err = parent.OpenRoot(installation.name)
	if err != nil {
		return nil, fmt.Errorf("open source tool installation: %w", err)
	}
	for _, name := range []string{sourceStageDirectory, "bin"} {
		if mkdirErr := mkdir(installation.root, name, 0o700); mkdirErr != nil {
			return nil, fmt.Errorf("create source tool %s: %w", name, mkdirErr)
		}
		installation.owned[name], err = installation.root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("inspect source tool %s: %w", name, err)
		}
	}
	return installation, installation.verify()
}

func validateSourceInstallationPath(directory, command string) error {
	if command != syftName && command != goreleaserName {
		return fmt.Errorf("unsupported source tool: %w", errInvalidConfig)
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || hasControl(directory) {
		return fmt.Errorf("source tool installation requires an absolute clean path: %w", errInvalidConfig)
	}
	return nil
}

func (installation *sourceInstallation) verify() error {
	if err := installation.verifyRoot(); err != nil {
		return err
	}
	for _, name := range []string{".", sourceStageDirectory, "bin"} {
		if name != "." && installation.owned[name] == nil {
			continue
		}
		current, err := installation.root.Lstat(name)
		if err != nil || installation.owned[name] == nil || !current.IsDir() || current.Mode().Perm() != 0o700 ||
			!os.SameFile(current, installation.owned[name]) {
			return fmt.Errorf("source tool owned directory %s identity changed: %w", name, errors.Join(err, errInvalidConfig))
		}
	}
	if err := installation.verifyArtifacts(); err != nil {
		return err
	}
	if installation.executable != nil {
		if err := verifyOpenedRegularFile(installation.executable, installation.executableInfo,
			"built source tool executable"); err != nil {
			return err
		}
		return verifyRootedPathIdentity(installation.root, installation.executableAt, installation.executable,
			"source tool executable")
	}
	return nil
}

func (installation *sourceInstallation) verifyArtifacts() error {
	for name, expected := range installation.artifacts {
		current, err := installation.root.Lstat(name)
		if err != nil || expected == nil || !current.Mode().IsRegular() || !os.SameFile(current, expected) {
			return fmt.Errorf("source tool artifact identity changed: %w", errors.Join(err, errInvalidConfig))
		}
	}
	return nil
}

func (installation *sourceInstallation) verifyRoot() error {
	parent, err := os.Lstat(filepath.Dir(installation.directory))
	if err != nil || !parent.IsDir() || !os.SameFile(parent, installation.parentInfo) {
		return fmt.Errorf("source tool installation parent identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	current, err := installation.parent.Lstat(installation.name)
	if err != nil || installation.owned["."] == nil || !current.IsDir() || current.Mode().Perm() != 0o700 ||
		!os.SameFile(current, installation.owned["."]) {
		return fmt.Errorf("source tool installation root identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	return nil
}

func (installation *sourceInstallation) openExecutableAt(name string) error {
	if err := installation.verify(); err != nil {
		return err
	}
	info, err := installation.root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm() != 0o755 {
		return fmt.Errorf("built source tool is not a regular executable: %w", errors.Join(err, errInvalidConfig))
	}
	// go build -o replaces its output inode; bind the resulting file after it exits.
	installation.executable, err = openRootedRegularFile(installation.root, name, info, "source tool executable")
	installation.executableInfo = info
	installation.executableAt = name
	return err
}

// Close removes only identity-matched owned paths; unexpected files prevent directory removal.
func (installation *sourceInstallation) Close() error {
	if installation == nil {
		return nil
	}
	var result error
	if !installation.retained {
		result = installation.removeOwned()
	}
	return errors.Join(result, installation.closeHandles())
}

func (installation *sourceInstallation) removeOwned() error {
	if installation.root == nil {
		if installation.parent == nil || installation.owned["."] == nil {
			return nil
		}
		if err := installation.verifyRoot(); err != nil {
			return err
		}
		return wrapOptional("remove unopened owned source tool installation", installation.parent.Remove(installation.name))
	}
	if err := installation.verify(); err != nil {
		return err
	}
	result := installation.removeOutput()
	for name := range installation.artifacts {
		result = errors.Join(result, wrapOptional("remove owned source tool artifact", installation.root.Remove(name)))
	}
	for _, name := range []string{sourceStageDirectory, "bin"} {
		if installation.owned[name] != nil {
			result = errors.Join(result, wrapOptional("remove owned source tool "+name, installation.root.Remove(name)))
		}
	}
	if result == nil {
		result = wrapOptional("remove owned source tool installation", installation.parent.Remove(installation.name))
	}
	return result
}

func (installation *sourceInstallation) removeOutput() error {
	if installation.executable != nil {
		return wrapOptional("remove owned source tool executable", installation.root.Remove(installation.executableAt))
	}
	if !installation.buildStarted {
		return nil
	}
	info, err := installation.root.Lstat(filepath.Join(sourceStageDirectory, installation.toolName()))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect partial source tool compiler output: %w", err)
	case info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0:
		return wrapOptional("remove partial source tool compiler output",
			installation.root.Remove(filepath.Join(sourceStageDirectory, installation.toolName())))
	default:
		return fmt.Errorf("partial source tool compiler output has unexpected mode %s: %w", info.Mode(), errInvalidConfig)
	}
}

func (installation *sourceInstallation) closeHandles() error {
	var result error
	if installation.executable != nil {
		result = wrapOptional("close source tool executable", installation.executable.Close())
		installation.executable = nil
	}
	if installation.root != nil {
		result = errors.Join(result, wrapOptional("close source tool installation root", installation.root.Close()))
		installation.root = nil
	}
	if installation.parent != nil {
		result = errors.Join(result, wrapOptional("close source tool installation parent", installation.parent.Close()))
		installation.parent = nil
	}
	return result
}
