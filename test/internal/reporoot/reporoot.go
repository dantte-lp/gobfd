// Package reporoot discovers and verifies the GoBFD module root fail-closed.
package reporoot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	goModName               = "go.mod"
	expectedModuleDirective = "module github.com/dantte-lp/gobfd\n"
	maxModuleHeaderBytes    = 4096
)

var (
	// ErrInvalidGoMod indicates go.mod is not a regular file.
	ErrInvalidGoMod = errors.New("go.mod is not a regular file")
	// ErrUnexpectedModule indicates go.mod does not declare the GoBFD module path.
	ErrUnexpectedModule = errors.New("go.mod declares an unexpected module")
)

type dirRoot interface {
	Lstat(name string) (os.FileInfo, error)
	Open(name string) (*os.File, error)
	Close() error
}

func defaultOpenRoot(dir string) (dirRoot, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open root %s: %w", dir, err)
	}
	return root, nil
}

// Find resolves the verified GoBFD repository root starting from the process working directory.
func Find() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return FromDir(wd)
}

// FromDir resolves the verified GoBFD repository root starting from start.
func FromDir(start string) (string, error) {
	return fromDirWith(start, defaultOpenRoot)
}

func fromDirWith(start string, openDir func(string) (dirRoot, error)) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve start directory %s: %w", start, err)
	}
	for {
		found, checkErr := hasVerifiedGoMod(current, openDir)
		if checkErr != nil {
			return "", checkErr
		}
		if found {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("find repository root from %s: %w", start, os.ErrNotExist)
		}
		current = parent
	}
}

func hasVerifiedGoMod(dir string, openDir func(string) (dirRoot, error)) (_ bool, retErr error) {
	root, err := openDir(dir)
	if err != nil {
		return false, fmt.Errorf("open candidate directory %s: %w", dir, err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close candidate directory %s: %w", dir, closeErr))
		}
	}()

	info, statErr := root.Lstat(goModName)
	if errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	}
	if statErr != nil {
		return false, fmt.Errorf("inspect %s in %s: %w", goModName, dir, statErr)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("inspect %s in %s: %w", goModName, dir, ErrInvalidGoMod)
	}

	header, readErr := readRegularGoModHeader(root, dir, info)
	if readErr != nil {
		return false, readErr
	}
	if !strings.HasPrefix(header, expectedModuleDirective) {
		return false, fmt.Errorf("verify %s in %s: %w", goModName, dir, ErrUnexpectedModule)
	}
	return true, nil
}

func readRegularGoModHeader(root dirRoot, dir string, expected os.FileInfo) (_ string, retErr error) {
	file, err := root.Open(goModName)
	if err != nil {
		return "", fmt.Errorf("open %s in %s: %w", goModName, dir, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close %s in %s: %w", goModName, dir, closeErr))
		}
	}()

	openedInfo, statErr := file.Stat()
	if statErr != nil {
		return "", fmt.Errorf("stat opened %s in %s: %w", goModName, dir, statErr)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(expected, openedInfo) {
		return "", fmt.Errorf("verify opened %s in %s: %w", goModName, dir, ErrInvalidGoMod)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxModuleHeaderBytes))
	if readErr != nil {
		return "", fmt.Errorf("read %s in %s: %w", goModName, dir, readErr)
	}
	return string(data), nil
}
