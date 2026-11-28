package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/stepca"
)

const actualCAOrigin = "https://127.0.0.1:18443"

func newActualCA(t *testing.T) (*testPKI, string) {
	t.Helper()
	root := newTestPKI(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	intermediate := &x509.Certificate{
		SerialNumber: testSerialNumber(t), Subject: pkix.Name{CommonName: "actual step-ca intermediate"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: root.ca.NotAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, intermediate, root.ca, &key.PublicKey, root.key)
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pki := &testPKI{dir: root.dir, ca: intermediate, key: key, pool: root.pool}
	cert := pki.writePEM(t, "intermediate.pem", "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := pki.writePEM(t, "intermediate.key", "EC PRIVATE KEY", keyDER)
	public := map[string]string{
		"kty": "EC", "crv": "P-256", "use": "sig", "kid": "qualifier",
		"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
		"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
	}
	config := map[string]any{
		"root": filepath.Join(pki.dir, "ca.pem"), "crt": cert, "key": keyFile,
		"address": "127.0.0.1:18443", "dnsNames": []string{"127.0.0.1"},
		"db":     map[string]string{"type": "badgerv2", "dataSource": filepath.Join(pki.dir, "db")},
		"logger": map[string]string{"format": "json"},
		"authority": map[string]any{"provisioners": []any{
			map[string]any{"type": "JWK", "name": "allow", "key": public},
			map[string]any{
				"type": "JWK", "name": "deny", "key": public,
				"claims": map[string]bool{"disableRenewal": true},
			},
		}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pki.dir, "ca.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return pki, path
}

func actualCAIdentity(t *testing.T, pki *testPKI, name, provisioner string, usages []x509.ExtKeyUsage) stepca.Options {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// The literal ASN.1 shape and OID come from step-ca v0.30.2 provisioner/extension.go.
	extension, err := asn1.Marshal(struct {
		Type       int
		Name       []byte
		Credential []byte
	}{1, []byte(provisioner), []byte("qualifier")})
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse("spiffe://gobfd.test/" + name)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: testSerialNumber(t), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-30 * time.Minute), NotAfter: time.Now().Add(15 * time.Minute),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages,
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, URIs: []*url.URL{uri},
		ExtraExtensions: []pkix.Extension{{
			Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 37476, 9000, 64, 1}, Value: extension,
		}},
	}
	if name == "expired" {
		leaf.NotBefore, leaf.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, pki.ca, &key.PublicKey, pki.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := pki.writePEM(t, name+".pem", "CERTIFICATE", der)
	file, err := os.OpenFile(certFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: pki.ca.Raw})
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write intermediate chain: %v %v", writeErr, closeErr)
	}
	return stepca.Options{
		CAURL: actualCAOrigin, RootFile: filepath.Join(pki.dir, "ca.pem"), CertFile: certFile,
		KeyFile: pki.writePEM(t, name+".key", "EC PRIVATE KEY", keyDER),
	}
}
