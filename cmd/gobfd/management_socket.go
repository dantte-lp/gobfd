package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/dantte-lp/gobfd/internal/config"
)

var (
	// errManagementSocketInUse reports a socket path another server answers on.
	errManagementSocketInUse = errors.New("management socket is in use by another server")

	// errManagementSocketNotSocket reports a socket path held by another file.
	errManagementSocketNotSocket = errors.New("management socket path exists and is not a socket")
)

// staleSocketProbeTimeout bounds the connect that tells a live socket from a
// file left by a crashed daemon.
const staleSocketProbeTimeout = time.Second

// listenManagementSocket opens the management Unix socket (gobfd-330.1). A
// socket file left behind by a crashed daemon is replaced, a live one is
// refused. The socket starts with the umask-derived mode, which never grants
// group or other write access under the usual umask, and is then set to the
// configured mode and group before the server accepts on it.
func listenManagementSocket(ctx context.Context, cfg config.GRPCSocketConfig) (net.Listener, error) {
	mode, err := config.ParseSocketMode(cfg.Mode)
	if err != nil {
		return nil, fmt.Errorf("management socket: %w", err)
	}
	if staleErr := removeStaleSocket(ctx, cfg.Path); staleErr != nil {
		return nil, staleErr
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.Path, err)
	}
	if secureErr := secureSocket(cfg.Path, mode, cfg.Group); secureErr != nil {
		return nil, errors.Join(secureErr, ln.Close())
	}
	return ln, nil
}

func removeStaleSocket(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s: %w", path, errManagementSocketNotSocket)
	}
	probeCtx, cancel := context.WithTimeout(ctx, staleSocketProbeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(probeCtx, "unix", path)
	if err == nil {
		return errors.Join(fmt.Errorf("%s: %w", path, errManagementSocketInUse), conn.Close())
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("probe %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	return nil
}

func secureSocket(path string, mode os.FileMode, group string) error {
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return fmt.Errorf("socket group %q: %w", group, err)
		}
		gid, err := strconv.Atoi(g.Gid)
		if err != nil {
			return fmt.Errorf("socket group %q gid %q: %w", group, g.Gid, err)
		}
		if err := os.Chown(path, -1, gid); err != nil {
			return fmt.Errorf("chown %s to group %s: %w", path, group, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s to %v: %w", path, mode, err)
	}
	return nil
}

// serveManagementSocket serves srv on the management Unix socket until the
// server shuts down.
func serveManagementSocket(
	ctx context.Context,
	srv *http.Server,
	cfg config.GRPCSocketConfig,
	logger *slog.Logger,
) error {
	ln, err := listenManagementSocket(ctx, cfg)
	if err != nil {
		return err
	}
	logger.Info("gRPC server listening on management socket",
		slog.String("path", cfg.Path),
		slog.String("mode", cfg.Mode),
		slog.String("group", cfg.Group),
	)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve on %s: %w", cfg.Path, err)
	}
	return nil
}
