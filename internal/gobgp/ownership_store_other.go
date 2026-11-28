//go:build !linux

package gobgp

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

//nolint:nonamedreturns // Deferred cleanup joins close failures onto returned errors.
func readOwnershipFile(dirPath, base string) (data []byte, exists bool, retErr error) {
	if _, statErr := os.Stat(dirPath); errors.Is(statErr, fs.ErrNotExist) {
		return nil, false, nil
	} else if statErr != nil {
		return nil, false, fmt.Errorf("stat ownership parent %s: %w", dirPath, statErr)
	}

	root, err := os.OpenRoot(dirPath)
	if err != nil {
		return nil, false, fmt.Errorf("open ownership root %s: %w", dirPath, err)
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()

	file, openErr := root.Open(base)
	if errors.Is(openErr, fs.ErrNotExist) {
		return nil, false, nil
	}
	if openErr != nil {
		return nil, false, fmt.Errorf("open ownership file %s: %w: %w", base, ErrInvalidOwnershipStore, openErr)
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()

	info, infoErr := file.Stat()
	if infoErr != nil {
		return nil, false, fmt.Errorf("stat ownership file %s: %w", base, infoErr)
	}
	if !info.Mode().IsRegular() || info.Size() > maxOwnershipStateBytes {
		return nil, false, fmt.Errorf("invalid ownership file %s: %w", base, ErrInvalidOwnershipStore)
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

func stagePortableTempOwnershipFile(root *os.Root, tmpName string, data []byte) error {
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary ownership file: %w", err)
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
	root, err := os.OpenRoot(dirPath)
	if err != nil {
		return fmt.Errorf("open ownership root %s: %w", dirPath, err)
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()

	tmpName, nameErr := randomTempOwnershipName(base)
	if nameErr != nil {
		return nameErr
	}
	if stageErr := stagePortableTempOwnershipFile(root, tmpName, data); stageErr != nil {
		return errors.Join(stageErr, removeIfPresent(root, tmpName))
	}
	if renameErr := root.Rename(tmpName, base); renameErr != nil {
		return errors.Join(fmt.Errorf("rename ownership file into place: %w", renameErr), removeIfPresent(root, tmpName))
	}
	return nil
}
