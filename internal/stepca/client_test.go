package stepca

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRenewalEndpointRejectsNonOrigins(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"", "http://ca.test", "https://user:password@ca.test", "https://ca.test/path",
		"https://ca.test/?token=CA_BODY_SENTINEL", "https://ca.test/#fragment", "https://ca.test/?",
	} {
		if _, err := renewalEndpoint(origin); !errors.Is(err, ErrRenewal) {
			t.Fatalf("renewalEndpoint error = %v, want ErrRenewal", err)
		}
	}
}

func TestRenewCertificateCancellation(t *testing.T) {
	t.Parallel()
	fixture := newRenewalFixture(t, "valid")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := renewCertificate(ctx, fixture.server.URL, fixture.roots, fixture.current)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("renewal cancellation error = %v, want context.Canceled", err)
	}
}

func TestRenewCertificate(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"valid", "unchanged", "identity", "key", "usage", "ca", "expiry",
		"untrusted", "intermediate usage", "intermediate client usage", "duplicate", "invalid UTF8", "oversize",
		"redirect", "denied", "crt mismatch",
		"no EKU", "any EKU", "intermediate no EKU", "intermediate any EKU",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newRenewalFixture(t, name)
			got, err := renewCertificate(t.Context(), fixture.server.URL, fixture.roots, fixture.current)
			if !slices.Contains([]string{"valid", "unchanged", "no EKU", "any EKU"}, name) {
				if err == nil || strings.Contains(err.Error(), "CA_BODY_SENTINEL") {
					t.Fatalf("renewal error = %v, want sanitized rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "unchanged" && got != nil {
				t.Fatal("identical valid chain is not a no-op")
			}
			if name == "valid" && !bytes.Equal(got, fixture.renewed) {
				t.Fatal("renewed PEM differs from the verified chain")
			}
		})
	}
}

type renewalFixture struct {
	server  *httptest.Server
	roots   []byte
	current tls.Certificate
	renewed []byte
}

func newRenewalFixture(t *testing.T, mode string) renewalFixture {
	t.Helper()
	caKey := renewalKey(t)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "renewal fixture CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER := renewalDER(t, ca, ca, caKey, caKey)
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key := renewalKey(t)
	uri, err := url.Parse("spiffe://gobfd.test/ops")
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "node.test"},
		DNSNames: []string{"node.test"}, URIs: []*url.URL{uri},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if strings.Contains(mode, "no EKU") {
		leaf.ExtKeyUsage = nil
	} else if strings.Contains(mode, "any EKU") {
		leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
	}
	oldDER := renewalDER(t, leaf, ca, key, caKey)
	leaf.SerialNumber = big.NewInt(3)
	leaf.NotAfter = time.Now().Add(24 * time.Hour)
	mutateRenewalLeaf(leaf, mode)
	newKey := key
	if mode == "key" {
		newKey = renewalKey(t)
	}
	issuer := ca
	responseCA := renewalPEM(caDER)
	if strings.HasPrefix(mode, "intermediate ") {
		intermediate := *ca
		intermediate.SerialNumber = big.NewInt(4)
		intermediate.Subject = pkix.Name{CommonName: "restricted intermediate"}
		intermediate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		if mode == "intermediate client usage" {
			intermediate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		intermediateKey := renewalKey(t)
		der := renewalDER(t, &intermediate, ca, intermediateKey, caKey)
		issuer, err = x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		caKey = intermediateKey
		responseCA = renewalPEM(der)
	}
	if mode == "untrusted" {
		caKey = renewalKey(t)
		other := *ca
		other.PublicKey = &caKey.PublicKey
		issuer = &other
	}
	newDER := renewalDER(t, leaf, issuer, newKey, caKey)
	if mode == "unchanged" {
		newDER = oldDER
	}
	oldPEM, newPEM, caPEM := renewalPEM(oldDER), renewalPEM(newDER), renewalPEM(caDER)
	fixture := renewalFixture{
		current: tls.Certificate{Certificate: [][]byte{oldDER, caDER}, PrivateKey: key},
		renewed: append(bytes.Clone(newPEM), caPEM...),
	}
	fixture.server = renewalServer(t, ca, oldPEM, newPEM, responseCA, mode)
	fixture.roots = append(bytes.Clone(caPEM), renewalPEM(fixture.server.Certificate().Raw)...)
	return fixture
}

func mutateRenewalLeaf(leaf *x509.Certificate, mode string) {
	switch mode {
	case "identity":
		leaf.DNSNames = []string{"other.test"}
	case "usage":
		leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	case "ca":
		leaf.IsCA, leaf.BasicConstraintsValid = true, true
	case "expiry":
		leaf.NotAfter = time.Now().Add(30 * time.Minute)
	}
}

func renewalServer(t *testing.T, ca *x509.Certificate, oldPEM, newPEM, caPEM []byte, mode string) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"crt": string(newPEM), "ca": string(caPEM), "certChain": []string{string(newPEM), string(caPEM)},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/renew" || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("renewal did not use POST /renew with verified mTLS")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "redirect":
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		case "denied":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("CA_BODY_SENTINEL"))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(mutateRenewalJSON(data, oldPEM, mode))
	}))
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func mutateRenewalJSON(data, oldPEM []byte, mode string) []byte {
	switch mode {
	case "duplicate":
		return append([]byte(`{"crt":"CA_BODY_SENTINEL",`), data[1:]...)
	case "invalid UTF8":
		return bytes.Replace(data, []byte(`{"`), []byte{'{', '"', 0xff}, 1)
	case "oversize":
		return bytes.Repeat([]byte(" "), 131073)
	case "crt mismatch":
		var response map[string]any
		if err := json.Unmarshal(data, &response); err != nil {
			return nil
		}
		response["crt"] = string(oldPEM)
		encoded, err := json.Marshal(response)
		if err != nil {
			return nil
		}
		return encoded
	default:
		return data
	}
}

func renewalKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func renewalDER(t *testing.T, leaf, ca *x509.Certificate, key, signer *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func renewalPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestCertificateBlocksRejectsSkippedMalformedBlock(t *testing.T) {
	t.Parallel()
	valid := renewalPEM([]byte("DER validation occurs after PEM parsing"))
	for _, prefix := range []string{
		"-----BEGIN CERTIFICATE-----\ninvalid!\n-----END CERTIFICATE-----\n",
		"-----BEGIN CERTIFICATE-----\n",
	} {
		if _, err := certificateBlocks(append([]byte(prefix), valid...), maxChainLength); !errors.Is(err, ErrRenewal) {
			t.Fatalf("malformed first PEM block skipped: %v", err)
		}
	}
}
