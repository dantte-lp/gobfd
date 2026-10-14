// Package apiclient resolves GoBFD management API endpoints for the CLI and
// the companion daemons: unix:///path, http://host:port, https://host:port,
// or a bare host:port meaning http://.
package apiclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// ErrInvalidEndpoint indicates an endpoint that is not a unix, http or https
// URL or a host:port.
var ErrInvalidEndpoint = errors.New("invalid GoBFD API endpoint")

// Endpoint is a resolved management API endpoint.
type Endpoint struct {
	// BaseURL is the URL prefix for ConnectRPC requests.
	BaseURL string
	// SocketPath is the Unix socket path; empty for TCP endpoints.
	SocketPath string
}

// ParseEndpoint resolves an endpoint string.
func ParseEndpoint(raw string) (Endpoint, error) {
	if raw == "" {
		return Endpoint{}, fmt.Errorf("empty endpoint: %w", ErrInvalidEndpoint)
	}
	if !strings.Contains(raw, "://") {
		return Endpoint{BaseURL: "http://" + raw}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("parse %q: %w: %w", raw, ErrInvalidEndpoint, err)
	}
	switch u.Scheme {
	case "http", "https":
		return Endpoint{BaseURL: raw}, nil
	case "unix":
		// unix:///abs/path: an empty host keeps the path absolute.
		if u.Host != "" || !path.IsAbs(u.Path) || u.Path == "/" {
			return Endpoint{}, fmt.Errorf("%q needs an absolute socket path (unix:///path): %w", raw, ErrInvalidEndpoint)
		}
		return Endpoint{BaseURL: "http://localhost", SocketPath: u.Path}, nil
	default:
		return Endpoint{}, fmt.Errorf("scheme %q: %w", u.Scheme, ErrInvalidEndpoint)
	}
}

// HTTPClient returns the HTTP client for the endpoint: one that dials the
// Unix socket for unix:// endpoints, http.DefaultClient otherwise.
func (e Endpoint) HTTPClient() *http.Client {
	if e.SocketPath == "" {
		return http.DefaultClient
	}
	socket := e.SocketPath
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}

// TLSFiles are the client-side PEM files for an https:// endpoint: the CA
// that signed the server certificate and the client certificate and key for
// mTLS.
type TLSFiles struct {
	CAFile   string
	CertFile string
	KeyFile  string
}

// HTTPClientTLS returns the HTTP client for the endpoint with the given
// client TLS files. Without files it is HTTPClient; files need an https://
// endpoint.
func (e Endpoint) HTTPClientTLS(files TLSFiles) (*http.Client, error) {
	if files == (TLSFiles{}) {
		return e.HTTPClient(), nil
	}
	if !strings.HasPrefix(e.BaseURL, "https://") {
		return nil, fmt.Errorf("TLS files need an https:// endpoint, got %s: %w", e.BaseURL, ErrInvalidEndpoint)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if files.CAFile != "" {
		pem, err := os.ReadFile(files.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file %s has no PEM certificate: %w", files.CAFile, ErrInvalidEndpoint)
		}
	}
	if files.CertFile != "" || files.KeyFile != "" {
		pair, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}}, nil
}

// TLSFilesFromEnv reads GOBFD_TLS_CA_FILE, GOBFD_TLS_CERT_FILE and
// GOBFD_TLS_KEY_FILE, the client TLS files of the companion daemons.
func TLSFilesFromEnv() TLSFiles {
	return TLSFiles{
		CAFile:   os.Getenv("GOBFD_TLS_CA_FILE"),
		CertFile: os.Getenv("GOBFD_TLS_CERT_FILE"),
		KeyFile:  os.Getenv("GOBFD_TLS_KEY_FILE"),
	}
}
