// Package stepca renews existing PEM credentials against an explicitly trusted
// external Smallstep CA. It does not enroll identities or run a CA.
package stepca

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	maxRenewalResponse = 128 << 10
	maxChainLength     = 8
	maxTrustRoots      = 32
	renewalTimeout     = 10 * time.Second
)

// ErrRenewal reports a rejected CA response or certificate.
var ErrRenewal = errors.New("certificate renewal rejected")

// Options selects the operator-owned CA origin, trust bundle and PEM identity.
// RootFile trusts both the CA HTTPS endpoint and the issued certificate chain.
type Options struct {
	CAURL    string
	RootFile string
	CertFile string
	KeyFile  string
}

// renewCertificate returns nil for a verified identical chain.
func renewCertificate(ctx context.Context, origin string, rootsPEM []byte, current tls.Certificate) ([]byte, error) {
	endpoint, err := renewalEndpoint(origin)
	if err != nil {
		return nil, err
	}
	roots, err := renewalRoots(rootsPEM)
	if err != nil {
		return nil, err
	}
	if len(current.Certificate) == 0 || current.PrivateKey == nil {
		return nil, fmt.Errorf("missing CA roots or current identity: %w", ErrRenewal)
	}
	old, err := verifyChain(current.Certificate, roots)
	if err != nil {
		return nil, fmt.Errorf("verify current certificate: %w", err)
	}
	data, err := requestRenewal(ctx, endpoint, roots, current)
	if err != nil {
		return nil, err
	}
	return decodeRenewal(data, old, current.Certificate, roots)
}

func renewalRoots(data []byte) (*x509.CertPool, error) {
	blocks, err := certificateBlocks(data, maxTrustRoots)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	for _, der := range blocks {
		cert, parseErr := x509.ParseCertificate(der)
		if parseErr != nil {
			return nil, fmt.Errorf("parse explicit CA root: %w", ErrRenewal)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func renewalEndpoint(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("parse CA origin: %w", ErrRenewal)
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("CA must be an HTTPS origin without credentials, path or query: %w", ErrRenewal)
	}
	u.Path = "/renew"
	return u.String(), nil
}

func requestRenewal(
	ctx context.Context, endpoint string, roots *x509.CertPool, current tls.Certificate,
) ([]byte, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{current},
		},
		DisableKeepAlives: true, MaxResponseHeaderBytes: maxRenewalResponse,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: renewalTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create CA renewal request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request certificate renewal: %w", err)
	}
	return readRenewalResponse(response)
}

func readRenewalResponse(response *http.Response) ([]byte, error) {
	if response.StatusCode != http.StatusCreated {
		return nil, errors.Join(
			fmt.Errorf("CA renewal HTTP status %d: %w", response.StatusCode, ErrRenewal), response.Body.Close(),
		)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRenewalResponse+1))
	if err = errors.Join(err, response.Body.Close()); err != nil {
		return nil, fmt.Errorf("read CA renewal response: %w", err)
	}
	if len(data) > maxRenewalResponse {
		return nil, fmt.Errorf("CA renewal response exceeds size limit: %w", ErrRenewal)
	}
	return data, nil
}
