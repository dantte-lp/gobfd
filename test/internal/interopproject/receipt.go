package interopproject

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const privateReceiptMode = 0o600

// ErrUnsafeReceipt identifies changed or non-private receipt storage.
var ErrUnsafeReceipt = errors.New("unsafe private receipt")

// ReplacePrivateReceipt atomically replaces an existing owned receipt under a caller-held lock.
func ReplacePrivateReceipt(path string, data []byte) error {
	return writePrivateReceipt(path, data, false)
}

// CreatePrivateReceipt publishes a complete first receipt without replacing an existing destination.
func CreatePrivateReceipt(path string, data []byte) error {
	return writePrivateReceipt(path, data, true)
}

func writePrivateReceipt(path string, data []byte, create bool) (retErr error) {
	parentDir := filepath.Dir(path)
	root, openRootErr := os.OpenRoot(parentDir)
	if openRootErr != nil {
		return fmt.Errorf("open private receipt directory: %w", openRootErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close private receipt root: %w", closeErr))
		}
	}()
	directory, openDirErr := root.Open(".")
	if openDirErr != nil {
		return fmt.Errorf("open rooted private receipt directory: %w", openDirErr)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close rooted private receipt directory: %w", closeErr))
		}
	}()
	if validateErr := ValidatePrivateReceiptDirectory(directory, parentDir); validateErr != nil {
		return validateErr
	}
	base := filepath.Base(path)
	var initial os.FileInfo
	if create {
		if preErr := requireDestinationAbsent(root, base); preErr != nil {
			return preErr
		}
	} else {
		var preErr error
		initial, preErr = InspectPrivateReceipt(root, base)
		if preErr != nil {
			return preErr
		}
	}
	return stageAndPublishPrivateReceipt(root, directory, parentDir, base, data, initial, create)
}

func requireDestinationAbsent(root *os.Root, base string) error {
	if _, statErr := root.Lstat(base); statErr == nil {
		return fmt.Errorf("private receipt already exists: %w", errors.Join(ErrUnsafeReceipt, os.ErrExist))
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect first private receipt: %w", statErr)
	}
	return nil
}

func stageAndPublishPrivateReceipt(
	root *os.Root,
	directory *os.File,
	parentDir, base string,
	data []byte,
	initial os.FileInfo,
	create bool,
) (retErr error) {
	temporary, tempErr := writeReceiptTemporary(root, data)
	if tempErr != nil {
		return tempErr
	}
	renamed := false
	defer func() {
		if !renamed {
			if removeErr := root.Remove(temporary); removeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("remove temporary private receipt: %w", removeErr))
			}
		}
	}()
	if validateErr := ValidatePrivateReceiptDirectory(directory, parentDir); validateErr != nil {
		return validateErr
	}
	if pubErr := publishStagedPrivateReceipt(root, temporary, base, initial, create); pubErr != nil {
		return pubErr
	}
	renamed = true
	if syncErr := directory.Sync(); syncErr != nil {
		return fmt.Errorf("private receipt replaced but directory sync failed: %w", syncErr)
	}
	return nil
}

func publishStagedPrivateReceipt(
	root *os.Root,
	temporary, base string,
	initial os.FileInfo,
	create bool,
) error {
	if create {
		if linkErr := root.Link(temporary, base); linkErr != nil {
			return fmt.Errorf("publish first private receipt: %w", linkErr)
		}
		if rmErr := root.Remove(temporary); rmErr != nil {
			return fmt.Errorf("unlink published private receipt temporary: %w", rmErr)
		}
		return nil
	}
	current, inspectErr := InspectPrivateReceipt(root, base)
	if inspectErr != nil {
		return inspectErr
	}
	if !os.SameFile(initial, current) {
		return fmt.Errorf("private receipt changed before replacement: %w", ErrUnsafeReceipt)
	}
	// The trusted parent and cooperative lifecycle lock bound writers; rename is not CAS.
	if renameErr := root.Rename(temporary, base); renameErr != nil {
		return fmt.Errorf("replace private lifecycle receipt: %w", renameErr)
	}
	return nil
}

// ValidatePrivateReceiptDirectory verifies the opened directory and its current path identity.
func ValidatePrivateReceiptDirectory(directory *os.File, path string) error {
	info, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("stat rooted private receipt directory: %w", err)
	}
	current, statErr := os.Lstat(path)
	if statErr != nil {
		return fmt.Errorf("inspect private receipt directory: %w", statErr)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || int(stat.Uid) != os.Geteuid() ||
		!current.IsDir() || !os.SameFile(info, current) {
		return fmt.Errorf("private receipt directory must remain owned and private: %w", ErrUnsafeReceipt)
	}
	return nil
}

// InspectPrivateReceipt rejects foreign, linked, or non-private receipt destinations.
func InspectPrivateReceipt(root *os.Root, name string) (os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect private lifecycle receipt: %w", err)
	}
	if !IsPrivateReceipt(info) {
		return nil, fmt.Errorf("private receipt must be an owned single-link 0600 regular file: %w", ErrUnsafeReceipt)
	}
	return info, nil
}

// IsPrivateReceipt accepts only owned, single-link, mode-0600 regular files.
func IsPrivateReceipt(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode() == privateReceiptMode && stat.Nlink == 1 && int(stat.Uid) == os.Geteuid()
}

func writeReceiptTemporary(root *os.Root, data []byte) (_ string, retErr error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate private receipt temporary name: %w", err)
	}
	name := fmt.Sprintf(".lifecycle-%x.tmp", nonce[:])
	file, openErr := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateReceiptMode)
	if openErr != nil {
		return "", fmt.Errorf("create temporary private receipt: %w", openErr)
	}
	defer func() {
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close temporary private receipt: %w", closeErr))
			}
		}
		if retErr != nil {
			if removeErr := root.Remove(name); removeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("remove incomplete private receipt: %w", removeErr))
			}
		}
	}()
	if chmodErr := file.Chmod(privateReceiptMode); chmodErr != nil {
		return "", fmt.Errorf("set temporary private receipt permissions: %w", chmodErr)
	}
	n, writeErr := file.Write(data)
	if writeErr != nil {
		return "", fmt.Errorf("write temporary private receipt: %w", writeErr)
	}
	if n != len(data) {
		return "", fmt.Errorf("write temporary private receipt: %w", io.ErrShortWrite)
	}
	if syncErr := file.Sync(); syncErr != nil {
		return "", fmt.Errorf("sync temporary private receipt: %w", syncErr)
	}
	closeErr := file.Close()
	file = nil
	if closeErr != nil {
		return "", fmt.Errorf("close temporary private receipt: %w", closeErr)
	}
	return name, nil
}
