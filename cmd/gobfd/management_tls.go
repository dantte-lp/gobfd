package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/dantte-lp/gobfd/internal/config"
)

// errManagementTLSNoCA reports a client CA file without a PEM certificate.
var errManagementTLSNoCA = errors.New("grpc.tls.client_ca_file has no PEM certificate")

// newManagementTLSListener wraps the TCP management listener in mTLS
// (gobfd-330.2). Every handshake re-reads the certificate, key and client CA,
// so rotated files take effect without a restart; the files are loaded once
// here so a bad configuration fails at startup. ALPN offers h2 for gRPC
// clients, which require it, and http/1.1.
func newManagementTLSListener(ln net.Listener, cfg config.GRPCTLSConfig) (net.Listener, error) {
	if _, err := loadManagementTLS(cfg); err != nil {
		return nil, err
	}
	return tls.NewListener(ln, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return loadManagementTLS(cfg)
		},
	}), nil
}

func loadManagementTLS(cfg config.GRPCTLSConfig) (*tls.Config, error) {
	pair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load grpc.tls certificate: %w", err)
	}
	pem, err := os.ReadFile(cfg.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read grpc.tls.client_ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s: %w", cfg.ClientCAFile, errManagementTLSNoCA)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
		Certificates: []tls.Certificate{pair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}, nil
}

// unauthenticatedGRPCAddr is the TCP API address when it accepts requests
// without client authentication, empty under mTLS.
func unauthenticatedGRPCAddr(cfg config.GRPCConfig) string {
	if cfg.TLS.Enabled() {
		return ""
	}
	return cfg.Addr
}

// serveGRPCTCP serves srv on the TCP management address, under mTLS when
// grpc.tls is set, until the server shuts down.
func serveGRPCTCP(
	ctx context.Context,
	lc *net.ListenConfig,
	srv *http.Server,
	cfg config.GRPCConfig,
	logger *slog.Logger,
) error {
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	if cfg.TLS.Enabled() {
		tlsLn, tlsErr := newManagementTLSListener(ln, cfg.TLS)
		if tlsErr != nil {
			return errors.Join(tlsErr, ln.Close())
		}
		ln = tlsLn
	}
	logger.Info("gRPC server listening", slog.String("addr", cfg.Addr), slog.Bool("mtls", cfg.TLS.Enabled()))
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve on %s: %w", cfg.Addr, err)
	}
	return nil
}
