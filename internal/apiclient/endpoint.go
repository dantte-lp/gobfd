// Package apiclient resolves GoBFD management API endpoints for the CLI and
// the companion daemons: unix:///path, http://host:port, https://host:port,
// or a bare host:port meaning http://.
package apiclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
