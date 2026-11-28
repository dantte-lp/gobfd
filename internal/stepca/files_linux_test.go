//go:build linux

package stepca

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCredentialFileInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		private bool
		mode    os.FileMode
		kind    string
		wantErr bool
	}{
		{name: "public", mode: 0o644},
		{name: "private", private: true, mode: 0o600},
		{name: "read-only private", private: true, mode: 0o400},
		{name: "executable key", private: true, mode: 0o700, wantErr: true},
		{name: "readable key", private: true, mode: 0o644, wantErr: true},
		{name: "writable certificate", mode: 0o666, wantErr: true},
		{name: "symlink", mode: 0o600, kind: "symlink", wantErr: true},
		{name: "hardlink", mode: 0o600, kind: "hardlink", wantErr: true},
		{name: "directory", kind: "directory", wantErr: true},
		{name: "fifo", kind: "fifo", wantErr: true},
		{name: "oversize", mode: 0o600, kind: "oversize", wantErr: true},
		{name: "writable parent", mode: 0o600, kind: "parent", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "credential.pem")
			contents := []byte("fixture credential bytes")
			switch tc.kind {
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				if tc.kind == "oversize" {
					contents = bytes.Repeat([]byte("x"), 1_048_577)
				}
				writeCredentialFixture(t, path, contents, tc.mode)
				if tc.kind == "symlink" || tc.kind == "hardlink" {
					linked := filepath.Join(dir, "linked.pem")
					var err error
					if tc.kind == "symlink" {
						err = os.Symlink(filepath.Base(path), linked)
					} else {
						err = os.Link(path, linked)
					}
					if err != nil {
						t.Fatal(err)
					}
					path = linked
				}
				if tc.kind == "parent" {
					if err := os.Chmod(dir, 0o777); err != nil {
						t.Fatal(err)
					}
				}
			}
			file, err := openCredential(path, tc.private)
			if tc.wantErr {
				if !errors.Is(err, ErrCredential) {
					t.Fatalf("openCredential error = %v, want ErrCredential", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := file.close(); closeErr != nil {
					t.Error(closeErr)
				}
			}()
			if !bytes.Equal(file.data, contents) {
				t.Fatal("credential content changed while reading")
			}
		})
	}
}

func TestCredentialFileDetectsChanges(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{
		"content", "replacement", "hardlink", "permissions", "directory replacement", "directory permissions",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			sandbox := t.TempDir()
			dir := filepath.Join(sandbox, "credentials")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "certificate.pem")
			writeCredentialFixture(t, path, []byte("original certificate"), 0o640)
			file, err := openCredential(path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := file.close(); closeErr != nil {
					t.Error(closeErr)
				}
			}()
			if err := file.unchanged(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "content":
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				writeCredentialFixture(t, path, []byte("modified certificate"), 0o640)
				if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				other := filepath.Join(dir, "other.pem")
				writeCredentialFixture(t, other, []byte("original certificate"), 0o640)
				if err := os.Rename(other, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(dir, "other.pem")); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory replacement":
				if err := os.Rename(dir, filepath.Join(sandbox, "detached")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				writeCredentialFixture(t, path, []byte("original certificate"), 0o640)
			case "directory permissions":
				if err := os.Chmod(dir, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.unchanged(); !errors.Is(err, ErrCredential) {
				t.Fatalf("unchanged error = %v, want ErrCredential", err)
			}
		})
	}
}

func TestCredentialFileRejectsForeignOwner(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("ownership regression requires root within the temporary fixture")
	}
	for _, target := range []string{"directory", "certificate", "private key"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "credential.pem")
			writeCredentialFixture(t, path, []byte("fixture credential bytes"), 0o600)
			foreignPath := path
			if target == "directory" {
				foreignPath = dir
			}
			before, err := os.Stat(foreignPath)
			if err != nil {
				t.Fatal(err)
			}
			original := credentialFixtureStat(t, before)
			if chownErr := os.Chown(foreignPath, 65_534, 65_534); chownErr != nil {
				if errors.Is(chownErr, syscall.EPERM) {
					t.Skip("ownership fixture needs CAP_CHOWN")
				}
				t.Fatal(chownErr)
			}
			t.Cleanup(func() {
				if cleanupErr := os.Chown(foreignPath, int(original.Uid), int(original.Gid)); cleanupErr != nil {
					t.Error(cleanupErr)
				}
			})
			file, err := openCredential(path, target == "private key")
			if file != nil {
				if closeErr := file.close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			if !errors.Is(err, ErrCredential) {
				t.Fatalf("foreign-owner credential error = %v, want ErrCredential", err)
			}
		})
	}
}

func TestCredentialOwnerTrust(t *testing.T) {
	t.Parallel()
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foreignUID := uint32(65_534)
	if foreignUID == uint32(os.Geteuid()) {
		foreignUID--
	}
	for _, tc := range []struct {
		name string
		uid  uint32
		want bool
	}{
		{name: "root", uid: 0, want: true},
		{name: "caller", uid: uint32(os.Geteuid()), want: true},
		{name: "foreign", uid: foreignUID, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stat := *credentialFixtureStat(t, info)
			stat.Uid = tc.uid
			got := trustedOwner(credentialOwnerInfo{FileInfo: info, stat: stat})
			if got != tc.want {
				t.Fatalf("trustedOwner UID %d = %v, want %v", tc.uid, got, tc.want)
			}
		})
	}
}

type credentialOwnerInfo struct {
	os.FileInfo

	stat syscall.Stat_t
}

func (f credentialOwnerInfo) Sys() any { return &f.stat }

func TestCredentialFileAtomicReplacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "certificate.pem")
	writeCredentialFixture(t, path, []byte("original certificate"), 0o640)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openCredential(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	changed, err := file.replace(t.Context(), []byte("renewed certificate"), file, file)
	if err != nil || !changed {
		t.Fatalf("replace = %v, %v; want true, nil", changed, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "renewed certificate" {
		t.Fatalf("replacement = %q", contents)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeStat := credentialFixtureStat(t, before)
	afterStat := credentialFixtureStat(t, after)
	if after.Mode().Perm() != 0o640 || beforeStat.Uid != afterStat.Uid || beforeStat.Gid != afterStat.Gid {
		t.Fatal("replacement changed permissions or ownership")
	}
	if os.SameFile(before, after) {
		t.Fatal("replacement overwrote the original inode")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files remain: %d directory entries", len(entries))
	}
}

func TestRenewFileRejectsInvalidLocalInputs(t *testing.T) {
	t.Parallel()
	changed, err := RenewFile(t.Context(), Options{})
	if changed || !errors.Is(err, ErrCredential) {
		t.Fatalf("RenewFile = %v, %v; want false, ErrCredential", changed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	changed, err = RenewFile(ctx, Options{})
	if changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled RenewFile = %v, %v; want false, context.Canceled", changed, err)
	}
}

func TestCredentialFilePreservesCertificateOnPrecommitFailure(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"changed key", "changed certificate", "changed roots", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			certPath := filepath.Join(dir, "certificate.pem")
			keyPath := filepath.Join(dir, "key.pem")
			trustPath := filepath.Join(dir, "roots.pem")
			writeCredentialFixture(t, certPath, []byte("original certificate"), 0o640)
			writeCredentialFixture(t, keyPath, []byte("original key"), 0o600)
			writeCredentialFixture(t, trustPath, []byte("original trust root"), 0o644)
			cert, err := openCredential(certPath, false)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := cert.close(); closeErr != nil {
					t.Error(closeErr)
				}
			}()
			key, err := openCredential(keyPath, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := key.close(); closeErr != nil {
					t.Error(closeErr)
				}
			}()
			trust, err := openCredential(trustPath, false)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := trust.close(); closeErr != nil {
					t.Error(closeErr)
				}
			}()
			// Parsed private-key PEM must be erasable before the network request.
			clear(key.data)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantErr := ErrCredential
			wantCertificate := "original certificate"
			switch kind {
			case "changed key":
				writeCredentialFixture(t, keyPath, []byte("modified key"), 0o600)
			case "changed certificate":
				wantCertificate = "externally renewed certificate"
				writeCredentialFixture(t, certPath, []byte(wantCertificate), 0o640)
			case "changed roots":
				writeCredentialFixture(t, trustPath, []byte("modified trust root"), 0o644)
			case "cancelled":
				wantErr = context.Canceled
				cancel()
			}
			changed, err := cert.replace(ctx, []byte("renewed certificate"), key, trust)
			if changed || !errors.Is(err, wantErr) {
				t.Fatalf("replace = %v, %v; want false, %v", changed, err, wantErr)
			}
			contents, err := os.ReadFile(certPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != wantCertificate {
				t.Fatal("failed replacement overwrote the certificate")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 3 {
				t.Fatalf("temporary files remain: %d directory entries", len(entries))
			}
		})
	}
}

func TestRenewFileRejectsConcurrentRenewal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "certificate.pem")
	writeCredentialFixture(t, path, []byte("original certificate"), 0o644)
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := held.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if lockErr := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr != nil {
		t.Fatal(lockErr)
	}
	changed, err := RenewFile(t.Context(), Options{CertFile: path})
	if changed || !errors.Is(err, ErrCredential) || !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("concurrent RenewFile = %v, %v; want false, ErrCredential and EWOULDBLOCK", changed, err)
	}
}

func TestRenewFileTLSReplacement(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"valid", "unchanged", "identity"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := newRenewalFixture(t, mode)
			dir := t.TempDir()
			opts, original, keyPEM := writeRenewalFiles(t, dir, fixture)
			before, err := os.Stat(opts.CertFile)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := RenewFile(t.Context(), opts)
			if mode == "identity" {
				if changed || err == nil {
					t.Fatalf("rejected renewal = %v, %v", changed, err)
				}
			} else if err != nil || changed != (mode == "valid") {
				t.Fatalf("RenewFile = %v, %v", changed, err)
			}
			got, err := os.ReadFile(opts.CertFile)
			if err != nil {
				t.Fatal(err)
			}
			want := original
			if mode == "valid" {
				want = fixture.renewed
			}
			if !bytes.Equal(got, want) {
				t.Fatal("certificate content differs from validated outcome")
			}
			after, err := os.Stat(opts.CertFile)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode().Perm() != 0o640 || (mode != "valid" && !os.SameFile(before, after)) {
				t.Fatal("renewal changed file mode or replaced an unchanged certificate")
			}
			gotKey, err := os.ReadFile(opts.KeyFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotKey, keyPEM) {
				t.Fatal("renewal rewrote the private key")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 3 {
				t.Fatalf("temporary files remain: %d directory entries", len(entries))
			}
		})
	}
}

func TestRenewFileRejectsAliasedInputs(t *testing.T) {
	t.Parallel()
	for _, pair := range []string{"certificate and key", "certificate and roots", "key and roots"} {
		t.Run(pair, func(t *testing.T) {
			t.Parallel()
			fixture := newRenewalFixture(t, "valid")
			opts, original, keyPEM := writeRenewalFiles(t, t.TempDir(), fixture)
			switch pair {
			case "certificate and key":
				writeCredentialFixture(t, opts.CertFile, append(bytes.Clone(original), keyPEM...), 0o600)
				opts.KeyFile = opts.CertFile
			case "certificate and roots":
				opts.RootFile = opts.CertFile
			case "key and roots":
				writeCredentialFixture(t, opts.KeyFile, append(bytes.Clone(fixture.roots), keyPEM...), 0o600)
				opts.RootFile = opts.KeyFile
			}
			before, err := os.ReadFile(opts.CertFile)
			if err != nil {
				t.Fatal(err)
			}
			beforeKey, err := os.ReadFile(opts.KeyFile)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := RenewFile(t.Context(), opts)
			if changed || !errors.Is(err, ErrCredential) {
				t.Fatalf("aliased RenewFile = %v, %v; want false, ErrCredential", changed, err)
			}
			after, err := os.ReadFile(opts.CertFile)
			if err != nil {
				t.Fatal(err)
			}
			afterKey, err := os.ReadFile(opts.KeyFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || !bytes.Equal(beforeKey, afterKey) {
				t.Fatal("alias rejection rewrote source credentials")
			}
		})
	}
}

func writeRenewalFiles(t *testing.T, dir string, fixture renewalFixture) (Options, []byte, []byte) {
	t.Helper()
	opts := Options{
		CAURL: fixture.server.URL, RootFile: filepath.Join(dir, "roots.pem"),
		CertFile: filepath.Join(dir, "certificate.pem"), KeyFile: filepath.Join(dir, "key.pem"),
	}
	certificatePEM := make([]byte, 0, len(fixture.renewed))
	for _, der := range fixture.current.Certificate {
		certificatePEM = append(certificatePEM, renewalPEM(der)...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(fixture.current.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	writeCredentialFixture(t, opts.CertFile, certificatePEM, 0o640)
	writeCredentialFixture(t, opts.KeyFile, keyPEM, 0o600)
	writeCredentialFixture(t, opts.RootFile, fixture.roots, 0o644)
	return opts, certificatePEM, keyPEM
}

func writeCredentialFixture(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func credentialFixtureStat(t *testing.T, info os.FileInfo) *syscall.Stat_t {
	t.Helper()
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("credential fixture lacks Linux stat metadata")
	}
	return stat
}
