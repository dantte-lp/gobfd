package gobgp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/internal/gobgp/endpoint"
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

	// PeerAdminDown reports whether the BGP peer is administratively down,
	// for example disabled by an operator.
	PeerAdminDown(ctx context.Context, addr string) (bool, error)

	// Close releases the underlying gRPC connection.
	Close() error
}

// PeerAdminState represents the operational GoBGP administrative state of a neighbor.
type PeerAdminState uint8

const (
	// PeerAdminStateUnspecified indicates GoBGP reported no recognized admin state.
	PeerAdminStateUnspecified PeerAdminState = iota
	// PeerAdminStateUp indicates the neighbor is administratively enabled.
	PeerAdminStateUp
	// PeerAdminStateDown indicates the neighbor is administratively disabled.
	PeerAdminStateDown
	// PeerAdminStatePrefixLimit indicates the neighbor was shut down by prefix limit.
	PeerAdminStatePrefixLimit
)

// PeerInspection captures a neighbor's administrative configuration, operational
// state, and connected GoBGP speaker epoch identity.
type PeerInspection struct {
	AdminState    PeerAdminState
	ConfAdminDown bool
	Speaker       SpeakerIdentity
}

// OwnershipClient is an optional Client extension implemented by GRPCClient to
// verify speaker-epoch disable ownership across restarts and observe external
// peer administrative recoveries.
type OwnershipClient interface {
	InspectPeer(ctx context.Context, addr string) (PeerInspection, error)
	WatchPeerAdminUp(ctx context.Context, onAdminUp func(addr string)) error
}

// -------------------------------------------------------------------------
// Sentinel Errors
// -------------------------------------------------------------------------

var (
	// ErrClientClosed indicates the client has been closed.
	ErrClientClosed = errors.New("gobgp client is closed")

	// ErrDialFailed indicates the gRPC dial to GoBGP failed.
	ErrDialFailed = errors.New("gobgp gRPC dial failed")

	// ErrNoRootCAPEM indicates the configured CA file contained no PEM certificates.
	ErrNoRootCAPEM = errors.New("no PEM certificates found")

	// ErrIncompatibleServer indicates the endpoint does not serve the GoBGP v4
	// api.GoBgpService, for example a GoBGP v3 daemon (apipb.GobgpApi).
	ErrIncompatibleServer = errors.New("gobgp endpoint does not serve the GoBGP v4 API")
)

// -------------------------------------------------------------------------
// GRPCClient — production GoBGP gRPC client
// -------------------------------------------------------------------------

// GRPCClient connects to GoBGP's gRPC API and implements the Client interface.
// It wraps the generated GoBgpServiceClient with reconnection-friendly patterns.
//
// The underlying gRPC connection can use TLS for remote GoBGP APIs. Plaintext
// remains supported for local deployments where GoBGP listens only on loopback
// or another trusted management network.
type GRPCClient struct {
	target string
	conn   *grpc.ClientConn
	api    gobgpapi.GoBgpServiceClient
	logger *slog.Logger

	mu     sync.RWMutex
	closed bool

	peerMu   sync.RWMutex
	unixPeer unixTransportIdentity
	hasUnix  bool
}

// GRPCClientConfig holds connection parameters for the GoBGP gRPC client.
type GRPCClientConfig struct {
	// Addr is the GoBGP gRPC listen address (e.g., "127.0.0.1:50051").
	Addr string

	// TLS configures transport security for the GoBGP gRPC connection.
	TLS GRPCClientTLSConfig

	// DialTimeout is the maximum time to wait for the initial connection.
	// Zero means no timeout (use context deadline instead).
	DialTimeout time.Duration
}

// GRPCClientTLSConfig holds TLS settings for the GoBGP gRPC client.
type GRPCClientTLSConfig struct {
	// Enabled switches the GoBGP gRPC client from plaintext to TLS.
	Enabled bool

	// CAFile optionally points to a PEM bundle used as the root CA pool.
	// Empty means use the host root CA pool.
	CAFile string

	// ServerName overrides the TLS server name used for certificate
	// verification. Empty means derive it from the target address.
	ServerName string
}

// NewGRPCClient creates a new GoBGP gRPC client and establishes a connection.
//
// The client uses lazy connection establishment (grpc.NewClient does not
// block); actual connectivity is verified on the first RPC call.
func NewGRPCClient(cfg GRPCClientConfig, logger *slog.Logger) (*GRPCClient, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("create gobgp client: %w: empty address", ErrDialFailed)
	}

	transportCreds, err := buildTransportCredentials(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("create gobgp client to %s: %w: %w", cfg.Addr, ErrDialFailed, err)
	}

	client := &GRPCClient{
		target: cfg.Addr,
		logger: logger.With(
			slog.String("component", "gobgp.client"),
			slog.String("addr", cfg.Addr),
		),
	}

	opts := []grpc.DialOption{
		// transportCreds is TLS when gobgp.tls.enabled is true; plaintext fallback is limited to trusted deployments.
		// nosemgrep: go.grpc.tls.grpc-client-new-insecure-connection.grpc-client-new-insecure-connection
		grpc.WithTransportCredentials(transportCreds),
	}
	if socketPath, ok := endpoint.LocalUnixSocketPath(cfg.Addr); ok && !cfg.TLS.Enabled {
		opts = append(opts, grpc.WithContextDialer(client.unixContextDialer(socketPath)))
	}

	conn, err := grpc.NewClient(cfg.Addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("create gobgp client to %s: %w: %w", cfg.Addr, ErrDialFailed, err)
	}
	client.conn = conn
	client.api = gobgpapi.NewGoBgpServiceClient(conn)

	client.logger.Info("gobgp gRPC client created",
		slog.String("target", cfg.Addr),
		slog.Bool("tls", cfg.TLS.Enabled),
	)

	return client, nil
}

func buildTransportCredentials(cfg GRPCClientTLSConfig) (credentials.TransportCredentials, error) {
	if !cfg.Enabled {
		// Plaintext is supported for loopback or trusted GoBGP deployments; enable gobgp.tls for remote endpoints.
		// nosemgrep: go.grpc.tls.grpc-client-new-insecure-connection.grpc-client-new-insecure-connection
		return insecure.NewCredentials(), nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.ServerName,
	}

	if cfg.CAFile != "" {
		rootCAs, err := loadRootCAs(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = rootCAs
	}

	return credentials.NewTLS(tlsCfg), nil
}

func loadRootCAs(path string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(path) // #nosec G304 -- CAFile is an intentional operator-selected local PEM bundle.
	if err != nil {
		return nil, fmt.Errorf("read CA file %s: %w", path, err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("read CA file %s: %w", path, ErrNoRootCAPEM)
	}

	return pool, nil
}

// CheckCompatibility probes the endpoint with the side-effect-free GetBgp RPC.
// It returns ErrIncompatibleServer when the endpoint does not implement the
// GoBGP v4 service; other errors (unreachable, TLS, deadline) are returned
// wrapped so callers can treat them as transient.
func (c *GRPCClient) CheckCompatibility(ctx context.Context) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("check gobgp compatibility: %w", ErrClientClosed)
	}
	c.mu.RUnlock()

	_, err := c.api.GetBgp(ctx, &gobgpapi.GetBgpRequest{})
	if status.Code(err) == codes.Unimplemented {
		return fmt.Errorf("check gobgp compatibility: %w: %w", ErrIncompatibleServer, err)
	}
	if err != nil {
		return fmt.Errorf("check gobgp compatibility: %w", err)
	}
	return nil
}

// NativeBFDPeers returns the neighbor addresses for which GoBGP v4 native BFD
// is enabled, in ListPeer order. GoBFD must not own BFD for those peers.
func (c *GRPCClient) NativeBFDPeers(ctx context.Context) ([]netip.Addr, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, fmt.Errorf("list gobgp native BFD peers: %w", ErrClientClosed)
	}
	c.mu.RUnlock()

	stream, err := c.api.ListPeer(ctx, &gobgpapi.ListPeerRequest{})
	if err != nil {
		return nil, fmt.Errorf("list gobgp native BFD peers: %w", err)
	}
	var peers []netip.Addr
	for {
		resp, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return peers, nil
		}
		if recvErr != nil {
			return nil, fmt.Errorf("list gobgp native BFD peers: %w", recvErr)
		}
		if !resp.GetPeer().GetBfd().GetEnabled() {
			continue
		}
		address := resp.GetPeer().GetConf().GetNeighborAddress()
		peer, parseErr := netip.ParseAddr(address)
		if parseErr != nil {
			return nil, fmt.Errorf("parse gobgp neighbor address %q: %w", address, parseErr)
		}
		peers = append(peers, peer.Unmap())
	}
}

// errPeerNotFound reports a neighbor address unknown to GoBGP.
var errPeerNotFound = errors.New("gobgp neighbor not found")

// PeerAdminDown reports whether the neighbor's GoBGP admin state is DOWN.
func (c *GRPCClient) PeerAdminDown(ctx context.Context, addr string) (bool, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return false, fmt.Errorf("read gobgp peer %s admin state: %w", addr, ErrClientClosed)
	}
	c.mu.RUnlock()

	peer, err := c.readPeer(ctx, addr)
	if err != nil {
		return false, fmt.Errorf("read gobgp peer %s admin state: %w", addr, err)
	}
	return peer.GetState().GetAdminState() == gobgpapi.PeerState_ADMIN_STATE_DOWN, nil
}

func (c *GRPCClient) unixContextDialer(socketPath string) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		if err != nil {
			c.clearUnixIdentity()
			return nil, fmt.Errorf("dial gobgp unix socket %s: %w", socketPath, err)
		}
		id, ok := captureUnixTransportIdentity(conn, socketPath)
		c.peerMu.Lock()
		c.unixPeer = id
		c.hasUnix = ok
		c.peerMu.Unlock()
		return conn, nil
	}
}

func (c *GRPCClient) clearUnixIdentity() {
	c.peerMu.Lock()
	c.unixPeer = unixTransportIdentity{}
	c.hasUnix = false
	c.peerMu.Unlock()
}

func (c *GRPCClient) readPeer(ctx context.Context, addr string) (*gobgpapi.Peer, error) {
	stream, err := c.api.ListPeer(ctx, &gobgpapi.ListPeerRequest{Address: addr})
	if err != nil {
		return nil, fmt.Errorf("list gobgp peer %s: %w", addr, err)
	}
	for {
		resp, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return nil, errPeerNotFound
		}
		if recvErr != nil {
			return nil, fmt.Errorf("receive gobgp peer %s: %w", addr, recvErr)
		}
		if resp.GetPeer().GetConf().GetNeighborAddress() == addr {
			return resp.GetPeer(), nil
		}
	}
}

// InspectPeer reads a neighbor's operational and configured admin state along
// with the live speaker identity on the active transport.
func (c *GRPCClient) InspectPeer(ctx context.Context, addr string) (PeerInspection, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return PeerInspection{}, fmt.Errorf("inspect gobgp peer %s: %w", addr, ErrClientClosed)
	}
	c.mu.RUnlock()

	bgpResp, err := c.api.GetBgp(ctx, &gobgpapi.GetBgpRequest{})
	if err != nil {
		return PeerInspection{}, fmt.Errorf("inspect gobgp global state for %s: %w", addr, err)
	}
	peer, err := c.readPeer(ctx, addr)
	if err != nil {
		return PeerInspection{}, fmt.Errorf("inspect gobgp peer %s: %w", addr, err)
	}

	var admin PeerAdminState
	switch peer.GetState().GetAdminState() {
	case gobgpapi.PeerState_ADMIN_STATE_UP:
		admin = PeerAdminStateUp
	case gobgpapi.PeerState_ADMIN_STATE_DOWN:
		admin = PeerAdminStateDown
	case gobgpapi.PeerState_ADMIN_STATE_PFX_CT:
		admin = PeerAdminStatePrefixLimit
	default:
		admin = PeerAdminStateUnspecified
	}

	speaker := SpeakerIdentity{
		Target:   c.target,
		ASN:      bgpResp.GetGlobal().GetAsn(),
		RouterID: bgpResp.GetGlobal().GetRouterId(),
	}
	c.peerMu.RLock()
	if c.hasUnix {
		speaker.PeerPID = c.unixPeer.peerPID
		speaker.PeerUID = c.unixPeer.peerUID
		speaker.SocketDev = c.unixPeer.socketDev
		speaker.SocketIno = c.unixPeer.socketIno
		speaker.BootID = c.unixPeer.bootID
		speaker.ProcStartTime = c.unixPeer.procStartTime
	}
	c.peerMu.RUnlock()

	return PeerInspection{
		AdminState:    admin,
		ConfAdminDown: peer.GetConf().GetAdminDown(),
		Speaker:       speaker,
	}, nil
}

const (
	watchRetryInitial = 100 * time.Millisecond
	watchRetryMax     = 5 * time.Second
)

// WatchPeerAdminUp subscribes to GoBGP peer events and invokes onAdminUp whenever
// a neighbor is observed in ADMIN_STATE_UP. It reconnects on transient stream
// errors until ctx is cancelled or the client is closed.
func (c *GRPCClient) WatchPeerAdminUp(ctx context.Context, onAdminUp func(addr string)) error {
	backoff := watchRetryInitial
	timer := time.NewTimer(backoff)
	timer.Stop()
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			return nil
		}
		c.mu.RLock()
		closed := c.closed
		c.mu.RUnlock()
		if closed {
			return nil
		}

		err := c.consumePeerEventsOnce(ctx, onAdminUp)
		if err == nil || ctx.Err() != nil {
			return nil
		}
		timer.Reset(backoff)
		backoff = min(2*backoff, watchRetryMax)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (c *GRPCClient) consumePeerEventsOnce(ctx context.Context, onAdminUp func(addr string)) error {
	stream, err := c.api.WatchEvent(ctx, &gobgpapi.WatchEventRequest{
		Peer: &gobgpapi.WatchEventRequest_Peer{},
	})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return nil
		}
		return fmt.Errorf("open gobgp peer watch stream: %w", err)
	}
	for {
		resp, recvErr := stream.Recv()
		if recvErr != nil {
			if status.Code(recvErr) == codes.Unimplemented {
				return nil
			}
			return fmt.Errorf("receive gobgp peer watch event: %w", recvErr)
		}
		peerEv := resp.GetPeer().GetPeer()
		if peerEv == nil || peerEv.GetState().GetAdminState() != gobgpapi.PeerState_ADMIN_STATE_UP {
			continue
		}
		rawAddr := peerEv.GetState().GetNeighborAddress()
		if rawAddr == "" {
			rawAddr = peerEv.GetConf().GetNeighborAddress()
		}
		if parsed, parseErr := netip.ParseAddr(rawAddr); parseErr == nil {
			onAdminUp(parsed.Unmap().String())
		}
	}
}

// DisablePeer disables a BGP peer by address with an administrative reason.
func (c *GRPCClient) DisablePeer(ctx context.Context, addr string, communication string) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("disable peer %s: %w", addr, ErrClientClosed)
	}
	c.mu.RUnlock()

	_, err := c.api.DisablePeer(ctx, &gobgpapi.DisablePeerRequest{
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

	_, err := c.api.EnablePeer(ctx, &gobgpapi.EnablePeerRequest{
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
