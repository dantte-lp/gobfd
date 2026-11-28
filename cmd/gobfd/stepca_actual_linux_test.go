package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/stepca"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

const actualManagementOrigin = "https://127.0.0.1:18552"

// This external-provider gate runs only with prebuilt binaries in a network-none namespace.
func TestActualSmallstepRenewalManagementLifecycle(t *testing.T) {
	caBinary := os.Getenv("GOBFD_TEST_STEP_CA_BINARY")
	if caBinary == "" {
		t.Skip("actual step-ca qualification requires GOBFD_TEST_STEP_CA_BINARY")
	}
	if !filepath.IsAbs(os.Getenv("GOBFD_TEST_STEP_CA_REPORT_DIR")) {
		t.Fatal("actual CA qualification requires an absolute report directory")
	}
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) != 1 || interfaces[0].Name != "lo" {
		t.Fatalf("actual CA qualification requires network-none isolation: %v", err)
	}
	pki, caConfig := newActualCA(t)
	var ca, daemon *qualificationChild
	var observations []map[string]any
	t.Cleanup(func() {
		if ca != nil && daemon != nil {
			writeActualCAEvidence(t, ca, daemon, observations)
		}
	})
	ca = startQualificationChild(t, caBinary, pki.dir, caConfig)
	awaitQualificationReady(t, ca, func(ctx context.Context) error {
		status, probeErr := actualCARequest(ctx, pki, nil, "/health", "")
		if probeErr == nil && status != http.StatusOK {
			probeErr = fmt.Errorf("CA health status %d", status)
		}
		return probeErr
	})
	server := actualCAIdentity(t, pki, "daemon", "allow",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth})
	ops := actualCAIdentity(t, pki, "ops", "allow", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	monitor := actualCAIdentity(t, pki, "monitor", "allow", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	daemonConfig := filepath.Join(pki.dir, "gobfd.yml")
	config := fmt.Sprintf(`grpc:
  addr: 127.0.0.1:18552
  socket:
    path: %s
  mutating_identities: ["spiffe://gobfd.test/ops"]
  tls:
    cert_file: %s
    key_file: %s
    client_ca_file: %s
metrics:
  addr: 127.0.0.1:0
log:
  level: warn
  format: json
`, filepath.Join(pki.dir, "gobfd.sock"), server.CertFile, server.KeyFile, server.RootFile)
	if writeErr := os.WriteFile(daemonConfig, []byte(config), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	daemon = startQualificationChild(t, os.Getenv("GOBFD_TEST_DAEMON_BINARY"), pki.dir, "-config", daemonConfig)
	client := actualManagementClient(t, ops)
	awaitQualificationReady(t, daemon, func(ctx context.Context) error {
		_, probeErr := client.ListSessions(ctx, new(bfdv1.ListSessionsRequest))
		return probeErr
	})
	created, err := client.AddSession(t.Context(), addSessionRequest("127.0.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetSession().GetLocalDiscriminator()
	observations = append(observations, renewActualIdentity(t, server))
	observations = append(observations, renewActualIdentity(t, ops))
	observations = append(observations, renewActualIdentity(t, monitor))
	rotated := actualManagementClient(t, ops)
	_, err = actualManagementClient(t, monitor).DeleteSession(t.Context(),
		&bfdv1.DeleteSessionRequest{LocalDiscriminator: id})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("renewed monitor acquired mutation rights: %v", err)
	}
	if _, err := rotated.DeleteSession(t.Context(), &bfdv1.DeleteSessionRequest{LocalDiscriminator: id}); err != nil {
		t.Fatal(err)
	}
	observations = append(observations, verifyActualManagementCertificate(t, pki, ops, server))
	for _, mode := range []string{"denied", "revoked", "expired", "server-only"} {
		t.Run(mode, func(t *testing.T) {
			observations = append(observations, rejectActualRenewal(t, pki, mode))
		})
	}
	daemon.stop(t)
	ca.stop(t)
}

func actualManagementClient(t *testing.T, opts stepca.Options) bfdv1connect.BfdServiceClient {
	t.Helper()
	endpoint, err := apiclient.ParseEndpoint(actualManagementOrigin)
	if err != nil {
		t.Fatal(err)
	}
	client, err := endpoint.HTTPClientTLS(apiclient.TLSFiles{
		CAFile: opts.RootFile, CertFile: opts.CertFile, KeyFile: opts.KeyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(client, endpoint.BaseURL)))
}

func actualCARequest(
	ctx context.Context, pki *testPKI, identity *stepca.Options, path, body string,
) (int, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pki.pool}
	if identity != nil {
		pair, err := tls.LoadX509KeyPair(identity.CertFile, identity.KeyFile)
		if err != nil {
			return 0, err
		}
		config.Certificates = []tls.Certificate{pair}
	}
	transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	method := http.MethodGet
	if body != "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, actualCAOrigin+path, bytes.NewBufferString(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(request)
	if err != nil {
		return 0, err
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, errors.Join(readErr, response.Body.Close())
}

func actualLeaf(t *testing.T, opts stepca.Options) *x509.Certificate {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func credentialHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(data)
	return sha256.Sum256(data)
}

func renewActualIdentity(t *testing.T, opts stepca.Options) map[string]any {
	t.Helper()
	before, key := actualLeaf(t, opts), credentialHash(t, opts.KeyFile)
	changed, err := stepca.RenewFile(t.Context(), opts)
	if err != nil || !changed {
		t.Fatalf("actual CA renewal: changed=%t error=%v", changed, err)
	}
	after := actualLeaf(t, opts)
	sameKey := bytes.Equal(before.RawSubjectPublicKeyInfo, after.RawSubjectPublicKeyInfo)
	if key != credentialHash(t, opts.KeyFile) || before.SerialNumber.Cmp(after.SerialNumber) == 0 ||
		!after.NotAfter.After(before.NotAfter) || !sameKey {
		t.Fatal("actual renewal lost key identity, serial change or advancing expiry")
	}
	return map[string]any{
		"identity": before.Subject.CommonName, "renewed": true,
		"old_serial": before.SerialNumber.String(), "new_serial": after.SerialNumber.String(), "key_unchanged": true,
	}
}

func verifyActualManagementCertificate(t *testing.T, pki *testPKI, client, server stepca.Options) map[string]any {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(client.CertFile, client.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: &tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: pki.pool, Certificates: []tls.Certificate{pair},
	}}).DialContext(t.Context(), "tcp", "127.0.0.1:18552")
	if err != nil {
		t.Fatal(err)
	}
	state := conn.(*tls.Conn).ConnectionState()
	closeErr := conn.Close()
	if closeErr != nil || len(state.PeerCertificates) == 0 ||
		state.PeerCertificates[0].SerialNumber.Cmp(actualLeaf(t, server).SerialNumber) != 0 {
		t.Fatalf("new handshake did not load renewed server identity: %v", closeErr)
	}
	return map[string]any{
		"management_handshake": "renewed server and client", "principal_claim_retained": true,
		"unlisted_identity_mutation": "denied",
	}
}

func rejectActualRenewal(t *testing.T, pki *testPKI, mode string) map[string]any {
	t.Helper()
	provisioner, usage := "allow", x509.ExtKeyUsageClientAuth
	if mode == "denied" {
		provisioner = "deny"
	}
	if mode == "server-only" {
		usage = x509.ExtKeyUsageServerAuth
	}
	opts := actualCAIdentity(t, pki, mode, provisioner, []x509.ExtKeyUsage{usage})
	if mode == "revoked" {
		body := fmt.Sprintf(`{"serial":%q,"passive":true}`, actualLeaf(t, opts).SerialNumber.String())
		status, err := actualCARequest(t.Context(), pki, &opts, "/revoke", body)
		if err != nil || status != http.StatusOK {
			t.Fatalf("actual CA self-revocation: status=%d error=%v", status, err)
		}
	}
	cert, key := credentialHash(t, opts.CertFile), credentialHash(t, opts.KeyFile)
	changed, err := stepca.RenewFile(t.Context(), opts)
	if err == nil || changed || cert != credentialHash(t, opts.CertFile) || key != credentialHash(t, opts.KeyFile) {
		t.Fatalf("rejected actual renewal changed credentials: mode=%s changed=%t error=%v", mode, changed, err)
	}
	return map[string]any{"case": mode, "rejected": true, "certificate_unchanged": true, "key_unchanged": true}
}

func writeActualCAEvidence(t *testing.T, ca, daemon *qualificationChild, observations []map[string]any) {
	t.Helper()
	caJoined, caExit := qualificationChildResult(ca)
	daemonJoined, daemonExit := qualificationChildResult(daemon)
	data, err := json.MarshalIndent(map[string]any{
		"compiler": runtime.Version(), "step_ca_sha256": ca.hash, "daemon_sha256": daemon.hash,
		"ca_pid": ca.process.Pid, "daemon_pid": daemon.process.Pid,
		"ca_joined": caJoined, "daemon_joined": daemonJoined,
		"ca_exit": caExit, "daemon_exit": daemonExit, "observations": observations,
	}, "", "  ")
	if err != nil {
		t.Error(err)
		return
	}
	path := filepath.Join(os.Getenv("GOBFD_TEST_STEP_CA_REPORT_DIR"), "actual-stepca.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Error(err)
	}
}

func qualificationChildResult(child *qualificationChild) (bool, int) {
	select {
	case <-child.done:
		if child.state != nil {
			return true, child.state.ExitCode()
		}
		return true, -1
	default:
		return false, -1
	}
}
