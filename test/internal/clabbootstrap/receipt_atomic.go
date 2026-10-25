package clabbootstrap

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func replaceLifecycleReceipt(path string, data []byte) (retErr error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open vendor receipt directory: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open rooted vendor receipt directory: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, directory.Close()) }()
	if validateErr := validateReceiptDirectory(directory, filepath.Dir(path)); validateErr != nil {
		return validateErr
	}
	base := filepath.Base(path)
	initial, err := inspectReceiptDestination(root, base)
	if err != nil {
		return err
	}
	temporary, err := writeReceiptTemporary(root, data)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			if removeErr := root.Remove(temporary); removeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("remove temporary vendor receipt: %w", removeErr))
			}
		}
	}()
	if validateErr := validateReceiptDirectory(directory, filepath.Dir(path)); validateErr != nil {
		return validateErr
	}
	current, err := inspectReceiptDestination(root, base)
	if err != nil {
		return err
	}
	if !os.SameFile(initial, current) {
		return fmt.Errorf("vendor receipt changed before replacement: %w", errLifecycleState)
	}
	// The trusted parent and cooperative lifecycle lock bound writers; rename is not CAS.
	if err := root.Rename(temporary, base); err != nil {
		return fmt.Errorf("replace vendor lifecycle receipt: %w", err)
	}
	renamed = true
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("vendor receipt replaced but directory sync failed: %w", err)
	}
	return nil
}

func validateReceiptDirectory(directory *os.File, path string) error {
	info, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("stat rooted vendor receipt directory: %w", err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect vendor receipt directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || int(stat.Uid) != os.Geteuid() ||
		!current.IsDir() || !os.SameFile(info, current) {
		return fmt.Errorf("vendor receipt directory must remain owned and private: %w", errLifecycleState)
	}
	return nil
}

func inspectReceiptDestination(root *os.Root, name string) (os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect vendor lifecycle receipt: %w", err)
	}
	if !privateReceiptFile(info) {
		return nil, fmt.Errorf("vendor receipt must be an owned single-link 0600 regular file: %w", errLifecycleState)
	}
	return info, nil
}

func privateReceiptFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode() == secureFileMode && stat.Nlink == 1 && int(stat.Uid) == os.Geteuid()
}

//nolint:nonamedreturns // Deferred close and removal errors must accompany failed preparation.
func writeReceiptTemporary(root *os.Root, data []byte) (name string, retErr error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate vendor receipt temporary name: %w", err)
	}
	name = fmt.Sprintf(".lifecycle-%x.tmp", nonce[:])
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, secureFileMode)
	if err != nil {
		return "", fmt.Errorf("create temporary vendor receipt: %w", err)
	}
	defer func() {
		if file != nil {
			retErr = errors.Join(retErr, file.Close())
		}
		if retErr != nil {
			if removeErr := root.Remove(name); removeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("remove incomplete vendor receipt: %w", removeErr))
			}
		}
	}()
	if chmodErr := file.Chmod(secureFileMode); chmodErr != nil {
		return name, fmt.Errorf("set temporary vendor receipt permissions: %w", chmodErr)
	}
	if n, err := file.Write(data); err != nil {
		return name, fmt.Errorf("write temporary vendor receipt: %w", err)
	} else if n != len(data) {
		return name, fmt.Errorf("write temporary vendor receipt: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return name, fmt.Errorf("sync temporary vendor receipt: %w", err)
	}
	closeErr := file.Close()
	file = nil
	if closeErr != nil {
		return name, fmt.Errorf("close temporary vendor receipt: %w", closeErr)
	}
	return name, nil
}
