package cirunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const directoryMode os.FileMode = 0o755

func validateSafeDirectory(path, purpose string, rejectRoot bool) (string, error) {
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", fmt.Errorf("%s directory %q: %w", purpose, path, errInvalidConfig)
	}
	clean := filepath.Clean(path)
	if clean != path || clean == "." {
		return "", fmt.Errorf("%s directory must be clean: %w", purpose, errInvalidConfig)
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("resolve %s directory: %w", purpose, err)
	}
	if rejectRoot && absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", fmt.Errorf("%s directory cannot be a filesystem root: %w", purpose, errInvalidConfig)
	}
	return absolute, nil
}

func inspectDirectoryTree(path, purpose string) error {
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(path, current)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect %s path component %s: %w", purpose, current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s path component %s has mode %s: %w",
				purpose, current, info.Mode(), errInvalidConfig)
		}
	}
	return nil
}

func ensureDirectory(path, purpose string) error {
	absolute, err := validateSafeDirectory(path, purpose, true)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(absolute, current)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, lstatErr := os.Lstat(current)
		switch {
		case lstatErr == nil:
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s path component %s has mode %s: %w",
					purpose, current, info.Mode(), errInvalidConfig)
			}
		case errors.Is(lstatErr, os.ErrNotExist):
			if mkdirErr := os.Mkdir(current, directoryMode); mkdirErr != nil {
				return fmt.Errorf("create %s directory %s: %w", purpose, current, mkdirErr)
			}
		default:
			return fmt.Errorf("inspect %s path component %s: %w", purpose, current, lstatErr)
		}
	}
	if err := os.Chmod(absolute, directoryMode); err != nil {
		return fmt.Errorf("set %s directory %s mode %#o: %w", purpose, absolute, directoryMode, err)
	}
	return nil
}
