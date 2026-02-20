// Package gobgp integrates GoBFD with GoBGP via its gRPC API.
//
// When a BFD session transitions to Down, the handler either disables the
// corresponding BGP peer or withdraws its routes through GoBGP. When the
// BFD session returns to Up, the peer is re-enabled or routes are restored.
//
// This package implements RFC 5882 Section 3.2 flap dampening to prevent
// rapid BFD state oscillations from causing BGP route churn.
package gobgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// -------------------------------------------------------------------------
// Client Interface
// -------------------------------------------------------------------------

// Client abstracts the GoBGP gRPC operations needed by the BFD handler.
// This interface enables testing without a running GoBGP instance.
type Client interface {
	// DisablePeer administratively disables a BGP peer by address.
	// The communication string is sent as the administrative shutdown reason.
	DisablePeer(ctx context.Context, addr string, communication string) error

	// EnablePeer administratively enables a previously disabled BGP peer.
	EnablePeer(ctx context.Context, addr string) error

	// Close releases the underlying gRPC connection.
	Close() error
}

// -------------------------------------------------------------------------
// Sentinel Errors
// -------------------------------------------------------------------------

var (
	// ErrClientClosed indicates the client has been closed.
	ErrClientClosed = errors.New("gobgp client is closed")

	// ErrDialFailed indicates the gRPC dial to GoBGP failed.
	ErrDialFailed = errors.New("gobgp gRPC dial failed")
)

// -------------------------------------------------------------------------
// GRPCClient — production GoBGP gRPC client
// -------------------------------------------------------------------------

// GRPCClient connects to GoBGP's gRPC API and implements the Client interface.
// It wraps the generated GobgpApiClient with reconnection-friendly patterns.
//
// The underlying gRPC connection uses insecure credentials (plaintext) because
// GoBGP's API is typically accessed on localhost in production deployments.
type GRPCClient struct {
	conn   *grpc.ClientConn
	api    apipb.GobgpApiClient
	logger *slog.Logger

	mu     sync.RWMutex
	closed bool
}

// GRPCClientConfig holds connection parameters for the GoBGP gRPC client.
type GRPCClientConfig struct {
	// Addr is the GoBGP gRPC listen address (e.g., "127.0.0.1:50051").
	Addr string

	// DialTimeout is the maximum time to wait for the initial connection.
	// Zero means no timeout (use context deadline instead).
	DialTimeout time.Duration
}

// NewGRPCClient creates a new GoBGP gRPC client and establishes a connection.
//
// The connection uses grpc.NewClient with insecure credentials. GoBGP's gRPC
// API is typically exposed on localhost without TLS. The client uses lazy
// connection establishment (grpc.NewClient does not block); actual connectivity
// is verified on the first RPC call.
func NewGRPCClient(cfg GRPCClientConfig, logger *slog.Logger) (*GRPCClient, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("create gobgp client: %w: empty address", ErrDialFailed)
	}

	conn, err := grpc.NewClient(
		cfg.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create gobgp client to %s: %w: %w", cfg.Addr, ErrDialFailed, err)
	}

	client := &GRPCClient{
		conn: conn,
		api:  apipb.NewGobgpApiClient(conn),
		logger: logger.With(
			slog.String("component", "gobgp.client"),
			slog.String("addr", cfg.Addr),
		),
	}

	client.logger.Info("gobgp gRPC client created",
		slog.String("target", cfg.Addr),
	)

	return client, nil
}

// DisablePeer disables a BGP peer by address with an administrative reason.
func (c *GRPCClient) DisablePeer(ctx context.Context, addr string, communication string) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("disable peer %s: %w", addr, ErrClientClosed)
	}
	c.mu.RUnlock()

	_, err := c.api.DisablePeer(ctx, &apipb.DisablePeerRequest{
		Address:       addr,
		Communication: communication,
	})
	if err != nil {
		return fmt.Errorf("disable peer %s: %w", addr, err)
	}

	c.logger.Info("disabled BGP peer",
		slog.String("peer", addr),
		slog.String("reason", communication),
	)

	return nil
}

// EnablePeer enables a previously disabled BGP peer by address.
func (c *GRPCClient) EnablePeer(ctx context.Context, addr string) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("enable peer %s: %w", addr, ErrClientClosed)
	}
	c.mu.RUnlock()

	_, err := c.api.EnablePeer(ctx, &apipb.EnablePeerRequest{
		Address: addr,
	})
	if err != nil {
		return fmt.Errorf("enable peer %s: %w", addr, err)
	}

	c.logger.Info("enabled BGP peer",
		slog.String("peer", addr),
	)

	return nil
}

// Close releases the underlying gRPC connection. After Close, all methods
// return ErrClientClosed.
func (c *GRPCClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close gobgp client: %w", err)
	}

	c.logger.Info("gobgp gRPC client closed")

	return nil
}
