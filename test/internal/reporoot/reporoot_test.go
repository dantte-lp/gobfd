package reporoot

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

const validGoMod = "module github.com/dantte-lp/gobfd\n\ngo 1.27\n"

func TestFromDirFindsVerifiedRepositoryRoot(t *testing.T) {
	t.Parallel()

	ancestor := t.TempDir()
	if err := os.WriteFile(filepath.Join(ancestor, "go.mod"), []byte(validGoMod), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	nested := filepath.Join(ancestor, "test", "cmd", "e2ectl")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}

	got, err := FromDir(nested)
	if err != nil {
		t.Fatalf("FromDir() error = %v", err)
	}
	if got != ancestor {
		t.Fatalf("FromDir() = %q, want %q", got, ancestor)
	}
}

func TestFromDirFailsClosedWithoutWalkingToAncestor(t *testing.T) {
	t.Parallel()

	t.Run("symlink go.mod rejected", func(t *testing.T) {
		t.Parallel()

		child := setupAncestorWithValidGoMod(t)
		target := filepath.Join(child, "real.mod")
		if err := os.WriteFile(target, []byte(validGoMod), 0o600); err != nil {
			t.Fatalf("write symlink target: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(child, "go.mod")); err != nil {
			t.Fatalf("create symlink go.mod: %v", err)
		}

		_, err := FromDir(child)
		if !errors.Is(err, ErrInvalidGoMod) {
			t.Fatalf("FromDir() error = %v, want ErrInvalidGoMod", err)
		}
	})

	t.Run("directory go.mod rejected", func(t *testing.T) {
		t.Parallel()

		child := setupAncestorWithValidGoMod(t)
		if err := os.Mkdir(filepath.Join(child, "go.mod"), 0o750); err != nil {
			t.Fatalf("create directory go.mod: %v", err)
		}

		_, err := FromDir(child)
		if !errors.Is(err, ErrInvalidGoMod) {
			t.Fatalf("FromDir() error = %v, want ErrInvalidGoMod", err)
		}
	})

	t.Run("wrong module rejected", func(t *testing.T) {
		t.Parallel()

		child := setupAncestorWithValidGoMod(t)
		wrong := "module example.com/other/project\n\ngo 1.27\n"
		if err := os.WriteFile(filepath.Join(child, "go.mod"), []byte(wrong), 0o600); err != nil {
			t.Fatalf("write wrong go.mod: %v", err)
		}

		_, err := FromDir(child)
		if !errors.Is(err, ErrUnexpectedModule) {
			t.Fatalf("FromDir() error = %v, want ErrUnexpectedModule", err)
		}
	})

	t.Run("symlink loop rejected", func(t *testing.T) {
		t.Parallel()

		child := setupAncestorWithValidGoMod(t)
		loop := filepath.Join(child, "loop")
		if err := os.Symlink(loop, loop); err != nil {
			t.Fatalf("create symlink loop: %v", err)
		}

		_, err := FromDir(filepath.Join(loop, "sub"))
		if !errors.Is(err, syscall.ELOOP) {
			t.Fatalf("FromDir() error = %v, want ELOOP", err)
		}
	})

	t.Run("lstat permission denied stops before ancestor", func(t *testing.T) {
		t.Parallel()

		child := setupAncestorWithValidGoMod(t)
		_, err := fromDirWith(child, func(dir string) (dirRoot, error) {
			if dir == child {
				return stubDirRoot{lstatErr: syscall.EACCES}, nil
			}
			return defaultOpenRoot(dir)
		})
		if !errors.Is(err, syscall.EACCES) {
			t.Fatalf("fromDirWith() error = %v, want EACCES", err)
		}
	})

	t.Run("open permission denied stops before ancestor", func(t *testing.T) {
		t.Parallel()

		child := setupAncestorWithValidGoMod(t)
		if err := os.WriteFile(filepath.Join(child, "go.mod"), []byte(validGoMod), 0o600); err != nil {
			t.Fatalf("write child go.mod: %v", err)
		}
		realRoot, openErr := os.OpenRoot(child)
		if openErr != nil {
			t.Fatalf("open child root: %v", openErr)
		}
		t.Cleanup(func() { _ = realRoot.Close() })

		_, err := fromDirWith(child, func(dir string) (dirRoot, error) {
			if dir == child {
				return stubDirRoot{delegate: realRoot, openErr: syscall.EACCES}, nil
			}
			return defaultOpenRoot(dir)
		})
		if !errors.Is(err, syscall.EACCES) {
			t.Fatalf("fromDirWith() error = %v, want EACCES", err)
		}
	})
}

func setupAncestorWithValidGoMod(t *testing.T) string {
	t.Helper()
	ancestor := t.TempDir()
	if err := os.WriteFile(filepath.Join(ancestor, "go.mod"), []byte(validGoMod), 0o600); err != nil {
		t.Fatalf("write ancestor go.mod: %v", err)
	}
	child := filepath.Join(ancestor, "nested")
	if err := os.MkdirAll(child, 0o750); err != nil {
		t.Fatalf("create child dir: %v", err)
	}
	return child
}

type stubDirRoot struct {
	delegate *os.Root
	lstatErr error
	openErr  error
}

func (s stubDirRoot) Lstat(name string) (os.FileInfo, error) {
	if s.lstatErr != nil {
		return nil, s.lstatErr
	}
	return s.delegate.Lstat(name)
}

func (s stubDirRoot) Open(name string) (*os.File, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return s.delegate.Open(name)
}

func (s stubDirRoot) Close() error {
	return nil
}
