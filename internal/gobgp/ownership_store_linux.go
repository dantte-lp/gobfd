//go:build linux

package gobgp

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func trustedOwnershipStat(info fs.FileInfo) (*syscall.Stat_t, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, false
	}
	if stat.Uid != 0 && int64(stat.Uid) != int64(os.Geteuid()) {
		return nil, false
	}
	return stat, true
}

func validateOwnershipDir(dir *os.File) error {
	info, err := dir.Stat()
	if err != nil {
		return fmt.Errorf("stat ownership directory: %w", err)
	}
	if _, ok := trustedOwnershipStat(info); !ok || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("ownership directory must be a private trusted directory: %w", ErrInvalidOwnershipStore)
	}
	return nil
}

func openValidatedOwnershipDir(dirPath string) (*os.Root, *os.File, error) {
	root, err := os.OpenRoot(dirPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open ownership root %s: %w", dirPath, err)
	}
	dir, err := root.Open(".")
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("open ownership directory %s: %w", dirPath, err), root.Close())
	}
	if valErr := validateOwnershipDir(dir); valErr != nil {
		return nil, nil, errors.Join(valErr, dir.Close(), root.Close())
	}
	return root, dir, nil
}

func validateRegularOwnershipFile(base string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat ownership file %s: %w", base, err)
	}
	stat, ok := trustedOwnershipStat(info)
	mode := info.Mode().Perm()
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 ||
		(mode != 0o600 && mode != 0o400) ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 ||
		info.Size() > maxOwnershipStateBytes {
		return fmt.Errorf(
			"ownership file %s must be a single-link regular 0600/0400 file: %w",
			base, ErrInvalidOwnershipStore,
		)
	}
	return nil
}

//nolint:nonamedreturns // Deferred cleanup joins close failures onto returned errors.
func readOwnershipFile(dirPath, base string) (data []byte, exists bool, retErr error) {
	if _, statErr := os.Stat(dirPath); errors.Is(statErr, fs.ErrNotExist) {
		return nil, false, nil
	} else if statErr != nil {
		return nil, false, fmt.Errorf("stat ownership parent %s: %w", dirPath, statErr)
	}

	root, dir, err := openValidatedOwnershipDir(dirPath)
	if err != nil {
		return nil, false, err
	}
	defer func() { retErr = errors.Join(retErr, dir.Close(), root.Close()) }()

	fd, openErr := syscall.Openat(
		int(dir.Fd()),
		base,
		syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC,
		0,
	)
	if errors.Is(openErr, syscall.ENOENT) {
		return nil, false, nil
	}
	if openErr != nil {
		return nil, false, fmt.Errorf("open ownership file %s: %w: %w", base, ErrInvalidOwnershipStore, openErr)
	}
	file := os.NewFile(uintptr(fd), base)
	defer func() { retErr = errors.Join(retErr, file.Close()) }()

	if valErr := validateRegularOwnershipFile(base, file); valErr != nil {
		return nil, false, valErr
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxOwnershipStateBytes+1))
	if readErr != nil {
		return nil, false, fmt.Errorf("read ownership file %s: %w", base, readErr)
	}
	if len(raw) > maxOwnershipStateBytes {
		return nil, false, fmt.Errorf("ownership file %s exceeds maximum size: %w", base, ErrInvalidOwnershipStore)
	}
	return raw, true, nil
}

func rejectSymlinkTarget(dir *os.File, base string) error {
	var existing unix.Stat_t
	lstatErr := unix.Fstatat(int(dir.Fd()), base, &existing, unix.AT_SYMLINK_NOFOLLOW)
	if lstatErr == nil {
		if existing.Mode&unix.S_IFMT == unix.S_IFLNK {
			return fmt.Errorf("refusing to replace symlink %s: %w", base, ErrInvalidOwnershipStore)
		}
		return nil
	}
	if errors.Is(lstatErr, unix.ENOENT) {
		return nil
	}
	return fmt.Errorf("stat existing ownership file %s: %w", base, lstatErr)
}

func stageTempOwnershipFile(root *os.Root, tmpName string, data []byte) error {
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary ownership file: %w", err)
	}
	if chmodErr := tmp.Chmod(0o600); chmodErr != nil {
		return errors.Join(fmt.Errorf("chmod temporary ownership file: %w", chmodErr), tmp.Close())
	}
	if n, writeErr := tmp.Write(data); writeErr != nil {
		return errors.Join(fmt.Errorf("write temporary ownership file: %w", writeErr), tmp.Close())
	} else if n != len(data) {
		return errors.Join(fmt.Errorf("write temporary ownership file: %w", io.ErrShortWrite), tmp.Close())
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		return errors.Join(fmt.Errorf("sync temporary ownership file: %w", syncErr), tmp.Close())
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("close temporary ownership file: %w", closeErr)
	}
	return nil
}

func writeOwnershipFileAtomic(dirPath, base string, data []byte) (retErr error) {
	if mkdirErr := os.MkdirAll(dirPath, 0o700); mkdirErr != nil {
		return fmt.Errorf("create ownership directory %s: %w", dirPath, mkdirErr)
	}
	root, dir, err := openValidatedOwnershipDir(dirPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, dir.Close(), root.Close()) }()

	if linkErr := rejectSymlinkTarget(dir, base); linkErr != nil {
		return linkErr
	}
	tmpName, nameErr := randomTempOwnershipName(base)
	if nameErr != nil {
		return nameErr
	}
	if stageErr := stageTempOwnershipFile(root, tmpName, data); stageErr != nil {
		return errors.Join(stageErr, removeIfPresent(root, tmpName))
	}
	if renameErr := root.Rename(tmpName, base); renameErr != nil {
		return errors.Join(fmt.Errorf("rename ownership file into place: %w", renameErr), removeIfPresent(root, tmpName))
	}
	if syncErr := dir.Sync(); syncErr != nil {
		return fmt.Errorf("sync ownership directory: %w", syncErr)
	}
	return nil
}
