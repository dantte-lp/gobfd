// gobfd-haproxy-agent bridges BFD session state to HAProxy agent-check protocol.
//
// It watches BFD session events via GoBFD's gRPC streaming API and serves
// HAProxy agent-check responses over TCP. When a BFD session is Up, the agent
// responds with "up ready\n"; when Down, it responds with "down\n".
//
// HAProxy agent-check protocol:
//   - HAProxy connects TCP to agent-port, reads ASCII string until newline.
//   - Keywords: up, down, maint, ready, drain.
//   - Response format: "up ready\n" or "down\n".
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	appversion "github.com/dantte-lp/gobfd/internal/version"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func main() {
	os.Exit(run())
}

func parseFlags() (string, string) {
	addr := flag.String("gobfd-addr", envOrDefault("GOBFD_ADDR", "http://127.0.0.1:50052"), "GoBFD gRPC address")
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
	}

	if len(backends) == 0 {
		logger.Error("no backends configured; use --config with backends list")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// BFD state tracker: maps peer address to current session state.
	states := &stateMap{m: make(map[string]bfdv1.SessionState)}

	client := bfdv1connect.NewBfdServiceClient(http.DefaultClient, gobfdAddr)

	g, gCtx := errgroup.WithContext(ctx)

	// Watch BFD session events and update state map.
	g.Go(func() error {
		return watchEvents(gCtx, client, states, logger)
	})

	// Start TCP agent-check listeners for each backend.
	for _, b := range backends {
		g.Go(func() error {
			return serveAgentCheck(gCtx, b.Peer, b.AgentPort, states, logger)
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

// stateMap is a concurrent-safe map of peer addresses to BFD session states.
type stateMap struct {
	mu sync.RWMutex
	m  map[string]bfdv1.SessionState
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
	stream, err := client.WatchSessionEvents(ctx, &bfdv1.WatchSessionEventsRequest{
		IncludeCurrent: true,
	})
	if err != nil {
		return fmt.Errorf("watch session events: %w", err)
	}

	for stream.Receive() {
		msg := stream.Msg()
		if msg.GetSession() == nil {
			continue
		}

		peer := msg.GetSession().GetPeerAddress()
		state := msg.GetSession().GetLocalState()
		states.set(peer, state)

		logger.Info("BFD state updated",
			slog.String("peer", peer),
			slog.String("state", state.String()),
			slog.String("event_type", msg.GetType().String()),
		)
	}

	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("event stream error: %w", err)
	}
	return nil
}

// serveAgentCheck listens on the given TCP port and responds to HAProxy
// agent-check connections with the BFD state for the specified peer.
func serveAgentCheck(
	ctx context.Context,
	peer string,
	port int,
	states *stateMap,
	logger *slog.Logger,
) error {
	addr := fmt.Sprintf(":%d", port)
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

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			logger.Warn("accept error", slog.String("error", err.Error()))
			continue
		}

		handleAgentCheck(conn, peer, states, logger)
	}
}

// handleAgentCheck writes the HAProxy agent-check response for a single connection.
func handleAgentCheck(conn net.Conn, peer string, states *stateMap, logger *slog.Logger) {
	state := states.get(peer)

	response := "down\n"
	if state == bfdv1.SessionState_SESSION_STATE_UP {
		response = "up ready\n"
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
	AgentPort int    `yaml:"agent_port"`
}

type agentConfig struct {
	GoBFDAddr string          `yaml:"gobfd_addr"`
	Backends  []backendConfig `yaml:"backends"`
}

func loadConfig(path string) (*agentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &agentConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
