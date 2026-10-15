package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.yaml.in/yaml/v3"
	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	appversion "github.com/dantte-lp/gobfd/internal/version"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

const (
	agentCheckMaxConcurrent = 64
	agentCheckWriteTimeout  = 100 * time.Millisecond
)

func main() {
	os.Exit(run())
}

func parseFlags() (string, string) {
	addr := flag.String("gobfd-addr", envOrDefault("GOBFD_ADDR", "unix:///run/gobfd/gobfd.sock"), "GoBFD gRPC address")
	cfg := flag.String("config", "", "path to YAML config file")
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(appversion.Full("gobfd-haproxy-agent"))
		os.Exit(0)
	}

	return *addr, *cfg
}

func run() int {
	gobfdAddr, configPath := parseFlags()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var backends []backendConfig
	var listenAddr string
	if configPath != "" {
		cfg, err := loadConfig(configPath)
		if err != nil {
			logger.Error("failed to load config", slog.String("error", err.Error()))
			return 1
		}
		if cfg.GoBFDAddr != "" {
			gobfdAddr = cfg.GoBFDAddr
		}
		backends = cfg.Backends
		listenAddr = cfg.ListenAddr
	}

	if len(backends) == 0 {
		logger.Error("no backends configured; use --config with backends list")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	states := newStateMap(backends)

	client, err := newBFDClient(gobfdAddr)
	if err != nil {
		logger.Error("invalid GoBFD endpoint", slog.String("error", err.Error()))
		return 1
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Watch BFD session events and update state map.
	g.Go(func() error {
		return watchEvents(gCtx, client, states, logger)
	})

	// Start TCP agent-check listeners for each backend.
	for _, b := range backends {
		g.Go(func() error {
			return serveAgentCheck(gCtx, strconv.Itoa(b.AgentPort),
				agentCheckListenAddr(listenAddr, b.AgentPort), states, logger)
		})
	}

	logger.Info("gobfd-haproxy-agent started",
		slog.String("gobfd_addr", gobfdAddr),
		slog.Int("backends", len(backends)),
	)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("agent exited with error", slog.String("error", err.Error()))
		return 1
	}

	logger.Info("gobfd-haproxy-agent stopped")
	return 0
}

// stateMap publishes selected backend states atomically to agent-check readers.
type stateMap struct {
	mu        sync.RWMutex
	m         map[string]bfdv1.SessionState
	selectors map[string]apiclient.SessionSelector
}

func newStateMap(backends []backendConfig) *stateMap {
	states := &stateMap{
		m:         make(map[string]bfdv1.SessionState),
		selectors: make(map[string]apiclient.SessionSelector, len(backends)),
	}
	for _, backend := range backends {
		states.selectors[strconv.Itoa(backend.AgentPort)] = backend.selector
	}
	return states
}

func (s *stateMap) set(peer string, state bfdv1.SessionState) {
	s.mu.Lock()
	s.m[peer] = state
	s.mu.Unlock()
}

func (s *stateMap) get(peer string) bfdv1.SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[peer]
}

// watchEvents streams BFD session events from GoBFD and updates the state map.
func watchEvents(
	ctx context.Context,
	client bfdv1connect.BfdServiceClient,
	states *stateMap,
	logger *slog.Logger,
) error {
	err := apiclient.WatchSessions(ctx, client, func(sessions []*bfdv1.BfdSession) error {
		current := make(map[string]bfdv1.SessionState, len(states.selectors))
		for backend, selector := range states.selectors {
			session, err := selector.Select(sessions)
			if err != nil {
				logger.Warn("backend session is ambiguous", slog.String("backend", backend), slog.Any("error", err))
				continue
			}
			current[backend] = session.GetLocalState()
		}
		states.mu.Lock()
		states.m = current
		states.mu.Unlock()
		return nil
	}, logger)
	if err != nil {
		return fmt.Errorf("watch HAProxy backend sessions: %w", err)
	}
	return nil
}

// newBFDClient builds the GoBFD client for a unix://, http://, https:// or
// host:port endpoint.
func newBFDClient(addr string) (bfdv1connect.BfdServiceClient, error) {
	ep, err := apiclient.ParseEndpoint(addr)
	if err != nil {
		return nil, fmt.Errorf("gobfd endpoint: %w", err)
	}
	httpClient, err := ep.HTTPClientTLS(apiclient.TLSFilesFromEnv())
	if err != nil {
		return nil, fmt.Errorf("gobfd endpoint TLS: %w", err)
	}
	return bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, ep.BaseURL))), nil
}

// agentCheckListenAddr returns the agent-check listen address for host and
// port; an empty host binds to loopback because the listener is
// unauthenticated.
func agentCheckListenAddr(host string, port int) string {
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// serveAgentCheck listens on the given TCP port and responds to HAProxy
// agent-check connections with the BFD state for the specified peer.
func serveAgentCheck(
	ctx context.Context,
	peer string,
	addr string,
	states *stateMap,
	logger *slog.Logger,
) error {
	lc := net.ListenConfig{}

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s for peer %s: %w", addr, peer, err)
	}
	defer ln.Close()

	logger.Info("agent-check listener started",
		slog.String("peer", peer),
		slog.String("addr", addr),
	)

	go func() {
		<-ctx.Done()
		if cErr := ln.Close(); cErr != nil {
			logger.Debug("listener close error", slog.String("error", cErr.Error()))
		}
	}()

	sem := make(chan struct{}, agentCheckMaxConcurrent)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			logger.Warn("accept error", slog.String("error", err.Error()))
			continue
		}

		select {
		case sem <- struct{}{}:
			go func() {
				defer func() { <-sem }()
				handleAgentCheck(conn, peer, states, logger)
			}()
		default:
			logger.Warn("agent-check concurrency limit reached",
				slog.String("peer", peer),
				slog.String("remote", conn.RemoteAddr().String()),
			)
			if cErr := conn.Close(); cErr != nil {
				logger.Debug("conn close error", slog.String("error", cErr.Error()))
			}
		}
	}
}

// handleAgentCheck writes the HAProxy agent-check response for a single connection.
func handleAgentCheck(conn net.Conn, peer string, states *stateMap, logger *slog.Logger) {
	state := states.get(peer)

	response := "down\n"
	if state == bfdv1.SessionState_SESSION_STATE_UP {
		response = "up ready\n"
	}

	if dErr := conn.SetWriteDeadline(time.Now().Add(agentCheckWriteTimeout)); dErr != nil {
		logger.Debug("set write deadline error",
			slog.String("peer", peer),
			slog.String("error", dErr.Error()),
		)
	}

	if _, wErr := conn.Write([]byte(response)); wErr != nil {
		logger.Warn("write error",
			slog.String("peer", peer),
			slog.String("error", wErr.Error()),
		)
	}
	if cErr := conn.Close(); cErr != nil {
		logger.Debug("conn close error", slog.String("error", cErr.Error()))
	}

	logger.Debug("agent-check served",
		slog.String("peer", peer),
		slog.String("response", response[:len(response)-1]),
		slog.String("remote", conn.RemoteAddr().String()),
	)
}

// backendConfig maps a BFD peer to an HAProxy agent-check port.
type backendConfig struct {
	Peer      string `yaml:"peer"`
	Local     string `yaml:"local"`
	Interface string `yaml:"interface"`
	Type      string `yaml:"type"`
	AgentPort int    `yaml:"agent_port"`
	selector  apiclient.SessionSelector
}

type agentConfig struct {
	GoBFDAddr string `yaml:"gobfd_addr"`
	// ListenAddr is the host the agent-check listeners bind to; empty means
	// loopback. HAProxy agent-addr may point at a remote agent, which then
	// needs an explicit address.
	ListenAddr string          `yaml:"listen_addr"`
	Backends   []backendConfig `yaml:"backends"`
}

func loadConfig(path string) (*agentConfig, error) {
	// os.Root provides sandboxed file access to prevent path traversal.
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open config root %s: %w", dir, err)
	}
	defer root.Close()

	f, err := root.Open(base)
	if err != nil {
		return nil, fmt.Errorf("open config file %s: %w", base, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &agentConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	ports := make(map[int]struct{}, len(cfg.Backends))
	for i := range cfg.Backends {
		backend := &cfg.Backends[i]
		if _, duplicate := ports[backend.AgentPort]; duplicate || backend.AgentPort < 1 || backend.AgentPort > 65535 {
			return nil, fmt.Errorf("backend %d agent_port %d: %w", i, backend.AgentPort, errors.ErrUnsupported)
		}
		ports[backend.AgentPort] = struct{}{}
		selector, err := apiclient.ParseSessionSelector(backend.Peer, backend.Local, backend.Interface, backend.Type)
		if err != nil {
			return nil, fmt.Errorf("backend %d: %w", i, err)
		}
		backend.selector = selector
	}

	return cfg, nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
