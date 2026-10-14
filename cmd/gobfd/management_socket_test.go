package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// gobfd-330.1: the management socket gets the configured mode and group.
func TestListenManagementSocketModeAndGroup(t *testing.T) {
	t.Parallel()

	group, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Skipf("look up the current group: %v", err)
	}
	path := filepath.Join(t.TempDir(), "gobfd.sock")
	ln, err := listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: path, Mode: "0640", Group: group.Name})
	if err != nil {
		t.Fatalf("listenManagementSocket: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o640 {
		t.Fatalf("socket mode = %v, want socket 0640", info.Mode())
	}
}

// A socket file left by a crashed daemon is replaced; a socket that another
// server still answers on is refused.
func TestListenManagementSocketStaleAndLive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.sock")
	old, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", stale)
	if err != nil {
		t.Fatalf("listen stale: %v", err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close()
	ln, err := listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: stale, Mode: "0660"})
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	_ = ln.Close()

	live := filepath.Join(dir, "live.sock")
	other, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", live)
	if err != nil {
		t.Fatalf("listen live: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() })
	_, err = listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: live, Mode: "0660"})
	if !errors.Is(err, errManagementSocketInUse) {
		t.Fatalf("listen over a live socket error = %v, want errManagementSocketInUse", err)
	}

	regular := filepath.Join(dir, "regular")
	if writeErr := os.WriteFile(regular, nil, 0o600); writeErr != nil {
		t.Fatalf("write regular file: %v", writeErr)
	}
	_, err = listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: regular, Mode: "0660"})
	if !errors.Is(err, errManagementSocketNotSocket) {
		t.Fatalf("listen over a regular file error = %v, want errManagementSocketNotSocket", err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The API server answers grpc-go and connect clients over the management
// socket and logs the caller's peer UID as principal.
func TestGRPCServerOverManagementSocket(t *testing.T) {
	t.Parallel()

	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)

	path := filepath.Join(t.TempDir(), "gobfd.sock")
	srv := newGRPCServer(config.GRPCConfig{}, mgr, nil, newDaemonHealthChecker(), logger)
	ln, err := listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: path, Mode: "0660"})
	if err != nil {
		t.Fatalf("listenManagementSocket: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if serveErr := <-serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
			t.Errorf("serve: %v", serveErr)
		}
	})

	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	err = conn.Invoke(t.Context(), "/bfd.v1.BfdService/ListSessions",
		new(bfdv1.ListSessionsRequest), new(bfdv1.ListSessionsResponse))
	if err != nil {
		t.Fatalf("ListSessions over grpc-go unix://: %v", err)
	}

	ep, err := apiclient.ParseEndpoint("unix://" + path)
	if err != nil {
		t.Fatalf("ParseEndpoint: %v", err)
	}
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(ep.HTTPClient(), ep.BaseURL)))
	if _, err = client.ListSessions(context.Background(), new(bfdv1.ListSessionsRequest)); err != nil {
		t.Fatalf("ListSessions over connect unix://: %v", err)
	}

	if want := fmt.Sprintf("principal=uid:%d", os.Getuid()); strings.Count(logs.String(), want) < 2 {
		t.Fatalf("rpc logs missing %q:\n%s", want, logs.String())
	}
}
