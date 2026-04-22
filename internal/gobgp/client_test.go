package gobgp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildTransportCredentialsUsesTLSWhenEnabled(t *testing.T) {
	t.Parallel()

	creds, err := buildTransportCredentials(GRPCClientTLSConfig{
		Enabled:    true,
		ServerName: "gobgp.example.net",
	})
	if err != nil {
		t.Fatalf("buildTransportCredentials: %v", err)
	}

	info := creds.Info()
	if info.SecurityProtocol != "tls" {
		t.Fatalf("SecurityProtocol = %q, want tls", info.SecurityProtocol)
	}
}

func TestBuildTransportCredentialsLoadsCAFile(t *testing.T) {
	t.Parallel()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, testCACertPEM(t), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	creds, err := buildTransportCredentials(GRPCClientTLSConfig{
		Enabled:    true,
		CAFile:     caPath,
		ServerName: "gobgp.example.net",
	})
	if err != nil {
		t.Fatalf("buildTransportCredentials: %v", err)
	}

	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Fatalf("SecurityProtocol = %q, want tls", got)
	}
}

func TestBuildTransportCredentialsRejectsInvalidCAFile(t *testing.T) {
	t.Parallel()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	_, err := buildTransportCredentials(GRPCClientTLSConfig{
		Enabled: true,
		CAFile:  caPath,
	})
	if err == nil {
		t.Fatal("expected invalid CA file error")
	}
}

func testCACertPEM(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "gobfd-test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
}
