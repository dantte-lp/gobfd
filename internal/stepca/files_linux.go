//go:build linux

package stepca

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

const maxCredentialBytes = 1 << 20

type credentialFile struct {
	root    *os.Root
	dir     *os.File
	file    *os.File
	name    string
	parent  string
	info    fs.FileInfo
	data    []byte
	digest  [sha256.Size]byte
	private bool
}

// RenewFile renews existing PEM credentials and replaces only the public
// certificate. changed remains true on errors after the atomic replacement.
//
//nolint:nonamedreturns // Deferred cleanup must amend the returned error and commit status.
func RenewFile(ctx context.Context, opts Options) (changed bool, retErr error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("renew certificate: %w", err)
	}
	cert, err := openCredential(opts.CertFile, false)
	if err != nil {
		return false, err
	}
	defer func() { retErr = credentialClose(retErr, cert.close(), changed) }()
	if err = syscall.Flock(int(cert.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false, fmt.Errorf("lock certificate: %w: %w", ErrCredential, err)
	}
	if err = cert.unchanged(); err != nil {
		return false, err
	}
	key, err := openCredential(opts.KeyFile, true)
	if err != nil {
		return false, err
	}
	defer func() { retErr = credentialClose(retErr, key.close(), changed) }()
	root, err := openCredential(opts.RootFile, false)
	if err != nil {
		return false, err
	}
	defer func() { retErr = credentialClose(retErr, root.close(), changed) }()
	pair, err := parseCredentialIdentity(cert, key, root)
	if err != nil {
		return false, err
	}
	updated, err := renewCertificate(ctx, opts.CAURL, root.data, pair)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("commit renewed certificate: %w", err)
	}
	if err := key.unchanged(); err != nil {
		return false, err
	}
	if updated == nil {
		if err := root.unchanged(); err != nil {
			return false, err
		}
		return false, cert.unchanged()
	}
	return cert.replace(ctx, updated, key, root)
}

func parseCredentialIdentity(cert, key, root *credentialFile) (tls.Certificate, error) {
	if os.SameFile(cert.info, key.info) || os.SameFile(cert.info, root.info) || os.SameFile(key.info, root.info) {
		clear(cert.data)
		clear(key.data)
		clear(root.data)
		return tls.Certificate{}, fmt.Errorf("certificate, key and trust root need distinct files: %w", ErrCredential)
	}
	if _, err := certificateBlocks(cert.data, maxChainLength); err != nil {
		return tls.Certificate{}, fmt.Errorf("validate existing certificate PEM: %w: %w", ErrCredential, err)
	}
	if _, err := certificateBlocks(root.data, maxTrustRoots); err != nil {
		return tls.Certificate{}, fmt.Errorf("validate trust root PEM: %w: %w", ErrCredential, err)
	}
	pair, err := tls.X509KeyPair(cert.data, key.data)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse existing certificate and key: %w: %w", ErrCredential, err)
	}
	clear(key.data)
	return pair, nil
}

//nolint:nonamedreturns // Deferred cleanup must amend the returned error and commit status.
func openCredential(path string, private bool) (result *credentialFile, retErr error) {
	if path == "" {
		return nil, fmt.Errorf("empty credential path: %w", ErrCredential)
	}
	parent, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("resolve credential directory: %w: %w", ErrCredential, err)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, fmt.Errorf("open credential directory: %w: %w", ErrCredential, err)
	}
	current := &credentialFile{root: root, name: filepath.Base(path), parent: parent, private: private}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, current.close())
		}
	}()
	current.dir, err = root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open rooted credential directory: %w: %w", ErrCredential, err)
	}
	dirInfo, err := current.dir.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat credential directory: %w: %w", ErrCredential, err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o022 != 0 || !trustedOwner(dirInfo) {
		return nil, fmt.Errorf("credential directory is writable by an untrusted user: %w", ErrCredential)
	}
	current.file, err = current.open()
	if err != nil {
		return nil, fmt.Errorf("open credential without following links: %w: %w", ErrCredential, err)
	}
	current.info, err = current.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat credential: %w: %w", ErrCredential, err)
	}
	if err = current.validate(current.info); err != nil {
		return nil, err
	}
	current.data, err = boundedCredentialRead(current.file, current.info)
	if err != nil {
		return nil, err
	}
	current.digest = sha256.Sum256(current.data)
	return current, nil
}

func boundedCredentialRead(file *os.File, expected fs.FileInfo) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil {
		return data, fmt.Errorf("read credential: %w: %w", ErrCredential, err)
	}
	if len(data) > maxCredentialBytes {
		return data, fmt.Errorf("credential exceeds size limit: %w", ErrCredential)
	}
	after, err := file.Stat()
	if err != nil {
		return data, fmt.Errorf("stat credential after reading: %w: %w", ErrCredential, err)
	}
	if !sameCredential(expected, after) {
		return data, fmt.Errorf("credential changed while reading: %w", ErrCredential)
	}
	return data, nil
}

func (f *credentialFile) open() (*os.File, error) {
	// Root.OpenFile resolves in-root symlinks; openat must enforce final no-follow.
	fd, err := syscall.Openat(int(f.dir.Fd()), f.name,
		syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("openat credential: %w", err)
	}
	return os.NewFile(uintptr(fd), f.name), nil
}

func trustedOwner(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || int64(stat.Uid) == int64(os.Geteuid()))
}

func (f *credentialFile) validate(info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("credential lacks Linux stat metadata: %w", ErrCredential)
	}
	if !info.Mode().IsRegular() || stat.Nlink != 1 || !trustedOwner(info) || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("credential must be a protected, single-link regular file: %w", ErrCredential)
	}
	mode := info.Mode().Perm()
	if f.private && mode != 0o400 && mode != 0o600 {
		return fmt.Errorf("private key needs mode 0400 or 0600: %w", ErrCredential)
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() > maxCredentialBytes {
		return fmt.Errorf("credential has unsupported mode or size: %w", ErrCredential)
	}
	return nil
}

func sameCredential(a, b fs.FileInfo) bool {
	x, xOK := a.Sys().(*syscall.Stat_t)
	y, yOK := b.Sys().(*syscall.Stat_t)
	if !xOK || !yOK {
		return false
	}
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() &&
		a.ModTime().Equal(b.ModTime()) && x.Uid == y.Uid && x.Gid == y.Gid && x.Nlink == y.Nlink && x.Ctim == y.Ctim
}

func (f *credentialFile) unchanged() (err error) {
	dirInfo, err := f.dir.Stat()
	if err != nil {
		return fmt.Errorf("stat opened credential directory: %w: %w", ErrCredential, err)
	}
	parentInfo, err := os.Stat(f.parent)
	if err != nil {
		return fmt.Errorf("stat credential directory path: %w: %w", ErrCredential, err)
	}
	if !os.SameFile(dirInfo, parentInfo) || !dirInfo.IsDir() ||
		dirInfo.Mode().Perm()&0o022 != 0 || !trustedOwner(dirInfo) {
		return fmt.Errorf("credential directory changed or became untrusted: %w", ErrCredential)
	}
	current, err := f.open()
	if err != nil {
		return fmt.Errorf("reopen credential before commit: %w: %w", ErrCredential, err)
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	info, err := current.Stat()
	if err != nil {
		return fmt.Errorf("stat credential before commit: %w: %w", ErrCredential, err)
	}
	if !sameCredential(f.info, info) {
		return fmt.Errorf("credential changed before commit: %w", ErrCredential)
	}
	data, err := boundedCredentialRead(current, f.info)
	if f.private {
		defer clear(data)
	}
	if err != nil {
		return err
	}
	if sha256.Sum256(data) != f.digest {
		return fmt.Errorf("credential content changed before commit: %w", ErrCredential)
	}
	return nil
}

//nolint:nonamedreturns // Deferred cleanup must amend the returned error and commit status.
func (f *credentialFile) replace(
	ctx context.Context, data []byte, key, trust *credentialFile,
) (changed bool, retErr error) {
	name, err := f.writeTemporary(data)
	if err != nil {
		return false, err
	}
	defer func() {
		if !changed {
			retErr = errors.Join(retErr, f.root.Remove(name))
		}
	}()
	if err := key.unchanged(); err != nil {
		return false, err
	}
	if err := trust.unchanged(); err != nil {
		return false, err
	}
	if err := f.unchanged(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("commit renewed certificate: %w", err)
	}
	// Trusted parent and cooperative flock bound concurrency; rename is not CAS.
	if err := f.root.Rename(name, f.name); err != nil {
		return false, fmt.Errorf("replace certificate: %w", err)
	}
	if err := f.dir.Sync(); err != nil {
		return true, fmt.Errorf("sync certificate directory: %w: %w", ErrCommitted, err)
	}
	return true, nil
}

//nolint:nonamedreturns // Deferred cleanup must amend the returned error and remove an incomplete temporary file.
func (f *credentialFile) writeTemporary(data []byte) (name string, retErr error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("create certificate temporary name: %w", err)
	}
	name = fmt.Sprintf(".gobfd-cert-%x.tmp", nonce[:])
	tmp, err := f.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create certificate temporary file: %w", err)
	}
	defer func() {
		if tmp != nil {
			retErr = errors.Join(retErr, tmp.Close())
		}
		if retErr != nil {
			retErr = errors.Join(retErr, f.root.Remove(name))
		}
	}()
	stat, ok := f.info.Sys().(*syscall.Stat_t)
	if !ok {
		return name, fmt.Errorf("certificate lacks Linux stat metadata: %w", ErrCredential)
	}
	if err := tmp.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
		return name, fmt.Errorf("preserve certificate ownership: %w", err)
	}
	if err := tmp.Chmod(f.info.Mode().Perm()); err != nil {
		return name, fmt.Errorf("preserve certificate permissions: %w", err)
	}
	if n, err := tmp.Write(data); err != nil {
		return name, fmt.Errorf("write renewed certificate: %w", err)
	} else if n != len(data) {
		return name, fmt.Errorf("write renewed certificate: %w", io.ErrShortWrite)
	}
	if err := tmp.Sync(); err != nil {
		return name, fmt.Errorf("sync renewed certificate: %w", err)
	}
	closeErr := tmp.Close()
	tmp = nil
	if closeErr != nil {
		return name, fmt.Errorf("close renewed certificate: %w", closeErr)
	}
	return name, nil
}

func (f *credentialFile) close() error {
	if f.private {
		clear(f.data)
	}
	var err error
	if f.file != nil {
		err = errors.Join(err, f.file.Close())
	}
	if f.dir != nil {
		err = errors.Join(err, f.dir.Close())
	}
	if f.root != nil {
		err = errors.Join(err, f.root.Close())
	}
	if err != nil {
		return fmt.Errorf("close credential files: %w", err)
	}
	return nil
}

func credentialClose(previous, closeErr error, changed bool) error {
	if closeErr == nil {
		return previous
	}
	if changed {
		return errors.Join(previous, ErrCommitted, closeErr)
	}
	return errors.Join(previous, closeErr)
}
