package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// testSerialNumber returns a random certificate serial number.
func testSerialNumber(t *testing.T) *big.Int {
	t.Helper()

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("serial number: %v", err)
	}
	return serial
}

// testPKI is a throwaway CA with helpers that write PEM files.
type testPKI struct {
	dir  string
	ca   *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          testSerialNumber(t),
		Subject:               pkix.Name{CommonName: "gobfd-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	p := &testPKI{dir: t.TempDir(), ca: ca, key: key, pool: x509.NewCertPool()}
	p.pool.AddCert(ca)
	p.writePEM(t, "ca.pem", "CERTIFICATE", der)
	return p
}

func (p *testPKI) writePEM(t *testing.T, name, kind string, der []byte) string {
	t.Helper()

	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// issue writes a leaf certificate and key and returns their paths.
func (p *testPKI) issue(t *testing.T, name string, usage x509.ExtKeyUsage, uri string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate %s key: %v", name, err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: testSerialNumber(t),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	if uri != "" {
		u, parseErr := url.Parse(uri)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", uri, parseErr)
		}
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.key)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal %s key: %v", name, err)
	}
	return p.writePEM(t, name+".pem", "CERTIFICATE", der), p.writePEM(t, name+".key", "EC PRIVATE KEY", keyDER)
}

// startTLSManagementServer serves the API on 127.0.0.1 with mTLS.
func startTLSManagementServer(t *testing.T, tlsCfg config.GRPCTLSConfig, logs *syncBuffer) string {
	t.Helper()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	logger := slog.New(slog.NewTextHandler(logs, nil))
	srv := newGRPCServer(config.GRPCConfig{}, false, mgr, nil, nil, newDaemonHealthChecker(), nil, logger, nil)
	tcp, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln, err := newManagementTLSListener(tcp, tlsCfg)
	if err != nil {
		t.Fatalf("newManagementTLSListener: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if serveErr := <-serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
			t.Errorf("serve: %v", serveErr)
		}
	})
	return tcp.Addr().String()
}

// gobfd-330.2: the TCP listener requires a client certificate from the
// configured CA, serves gRPC (ALPN h2) and Connect clients, and logs the
// client URI SAN as principal.
func TestGRPCServerMutualTLS(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	serverCert, serverKey := pki.issue(t, "server", x509.ExtKeyUsageServerAuth, "")
	clientCert, clientKey := pki.issue(t, "client", x509.ExtKeyUsageClientAuth, "spiffe://gobfd.test/ops")
	var logs syncBuffer
	addr := startTLSManagementServer(t, config.GRPCTLSConfig{
		CertFile: serverCert, KeyFile: serverKey, ClientCAFile: filepath.Join(pki.dir, "ca.pem"),
	}, &logs)

	pair, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatalf("load client pair: %v", err)
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: pki.pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	err = conn.Invoke(t.Context(), "/bfd.v1.BfdService/ListSessions",
		new(bfdv1.ListSessionsRequest), new(bfdv1.ListSessionsResponse))
	if err != nil {
		t.Fatalf("ListSessions over grpc-go mTLS: %v", err)
	}

	ep, err := apiclient.ParseEndpoint("https://" + addr)
	if err != nil {
		t.Fatalf("ParseEndpoint: %v", err)
	}
	httpClient, err := ep.HTTPClientTLS(apiclient.TLSFiles{
		CAFile: filepath.Join(pki.dir, "ca.pem"), CertFile: clientCert, KeyFile: clientKey,
	})
	if err != nil {
		t.Fatalf("HTTPClientTLS: %v", err)
	}
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, ep.BaseURL)))
	if _, err = client.ListSessions(context.Background(), new(bfdv1.ListSessionsRequest)); err != nil {
		t.Fatalf("ListSessions over connect mTLS: %v", err)
	}
	if want := "principal=spiffe://gobfd.test/ops"; strings.Count(logs.String(), want) < 2 {
		t.Fatalf("rpc logs missing %q:\n%s", want, logs.String())
	}

	noCert, err := ep.HTTPClientTLS(apiclient.TLSFiles{CAFile: filepath.Join(pki.dir, "ca.pem")})
	if err != nil {
		t.Fatalf("HTTPClientTLS without a client certificate: %v", err)
	}
	anonymous := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(noCert, ep.BaseURL)))
	if _, err = anonymous.ListSessions(context.Background(), new(bfdv1.ListSessionsRequest)); err == nil {
		t.Fatal("ListSessions without a client certificate succeeded")
	}
}

// Replacing the certificate files takes effect on the next handshake
// without a restart.
func TestManagementTLSReloadsServerCertificate(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	serverCert, serverKey := pki.issue(t, "server", x509.ExtKeyUsageServerAuth, "")
	clientCert, clientKey := pki.issue(t, "client", x509.ExtKeyUsageClientAuth, "spiffe://gobfd.test/ops")
	var logs syncBuffer
	addr := startTLSManagementServer(t, config.GRPCTLSConfig{
		CertFile: serverCert, KeyFile: serverKey, ClientCAFile: filepath.Join(pki.dir, "ca.pem"),
	}, &logs)
	pair, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatalf("load client pair: %v", err)
	}
	serial := func() *big.Int {
		t.Helper()
		conn, err := (&tls.Dialer{Config: &tls.Config{
			RootCAs: pki.pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12,
		}}).DialContext(t.Context(), "tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		return conn.(*tls.Conn).ConnectionState().PeerCertificates[0].SerialNumber
	}

	before := serial()
	newCert, newKey := pki.issue(t, "server-rotated", x509.ExtKeyUsageServerAuth, "")
	for _, f := range [][2]string{{newCert, serverCert}, {newKey, serverKey}} {
		data, err := os.ReadFile(f[0])
		if err != nil {
			t.Fatalf("read %s: %v", f[0], err)
		}
		if err := os.WriteFile(f[1], data, 0o600); err != nil {
			t.Fatalf("write %s: %v", f[1], err)
		}
	}
	if after := serial(); after.Cmp(before) == 0 {
		t.Fatalf("server certificate serial %v unchanged after rotation", after)
	}
}
