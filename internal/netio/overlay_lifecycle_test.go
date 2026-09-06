package netio

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestOverlayContextCancellation(t *testing.T) {
	for _, backend := range []string{"vxlan", "geneve"} {
		t.Run(backend, func(t *testing.T) {
			conn, udp, _ := newLifecycleOverlay(t, backend)
			local := netip.MustParseAddr("127.0.0.1")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if sendErr := conn.SendEncapsulated(ctx, make([]byte, 24), local); !errors.Is(sendErr, context.Canceled) {
				t.Fatalf("canceled send = %v", sendErr)
			}
			ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, _, recvErr := conn.RecvDecapsulated(ctx)
				result <- recvErr
			}()
			select {
			case recvErr := <-result:
				if !errors.Is(recvErr, context.DeadlineExceeded) {
					t.Fatalf("receive deadline = %v", recvErr)
				}
			case <-time.After(time.Second):
				t.Fatal("receive ignored context deadline")
			}
			// A fresh operation must reach packet validation, not a stale deadline.
			if _, err := udp.WriteToUDP([]byte{0}, udp.LocalAddr().(*net.UDPAddr)); err != nil {
				t.Fatal(err)
			}
			fresh, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			_, _, err := conn.RecvDecapsulated(fresh)
			if !errors.Is(err, ErrInnerPacketTooShort) && !errors.Is(err, ErrGeneveVAPIdentityUnavailable) {
				t.Fatalf("fresh receive after cancellation = %v", err)
			}
		})
	}
}

func newLifecycleOverlay(t *testing.T, backend string) (OverlayConn, *net.UDPConn, chan struct{}) {
	t.Helper()
	local := netip.MustParseAddr("127.0.0.1")
	logger := slog.New(slog.DiscardHandler)
	var conn OverlayConn
	var udp *net.UDPConn
	var token chan struct{}
	if backend == "vxlan" {
		c, err := NewVXLANConn(local, 100, 49152, logger)
		if err != nil {
			t.Fatal(err)
		}
		conn, udp, token = c, c.conn, c.sendMu
	} else {
		c, err := NewGeneveConn(local, 100, 49152, logger)
		if err != nil {
			t.Fatal(err)
		}
		conn, udp, token = c, c.conn, c.sendMu
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn, udp, token
}

func TestOverlayQueuedSendAndReceiveShutdown(t *testing.T) {
	for _, backend := range []string{"vxlan", "geneve"} {
		t.Run(backend, func(t *testing.T) {
			conn, _, token := newLifecycleOverlay(t, backend)
			token <- struct{}{}
			defer func() { <-token }()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if err := conn.SendEncapsulated(ctx, nil, netip.Addr{}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("queued send deadline = %v", err)
			}
			result := make(chan error, 2)
			go func() { result <- conn.SendEncapsulated(t.Context(), nil, netip.Addr{}) }()
			go func() {
				_, _, err := conn.RecvDecapsulated(t.Context())
				result <- err
			}()
			closed := make(chan error, 1)
			go func() { closed <- conn.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("Close waited for send buffer ownership")
			}
			for range 2 {
				select {
				case err := <-result:
					if !errors.Is(err, ErrOverlayRecvClosed) {
						t.Fatalf("operation after Close = %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("Close did not unblock operation")
				}
			}
		})
	}
}

func TestOverlayIOBlockedWriteCancellation(t *testing.T) {
	t.Parallel()
	writer, reader := net.Pipe()
	defer writer.Close()
	defer reader.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- overlayIO(ctx, writer.SetWriteDeadline, writer.Close, func() error {
			close(started)
			_, err := writer.Write([]byte{1})
			return err
		})
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write ignored cancellation")
	}
	go func() {
		_, err := writer.Write([]byte{2})
		result <- err
	}()
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if _, err := reader.Read(buf[:]); err != nil {
		t.Fatalf("fresh write after cancellation: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("fresh write after cancellation: %v", err)
	}
}

func TestOverlayIOPreservesDeadlineErrors(t *testing.T) {
	t.Parallel()
	for _, failReset := range []bool{false, true} {
		t.Run(map[bool]string{false: "interrupt", true: "reset"}[failReset], func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			injected := errors.New("injected deadline error")
			interrupted := make(chan struct{})
			closed := false
			setDeadline := func(deadline time.Time) error {
				if !deadline.IsZero() {
					defer close(interrupted)
				}
				if deadline.IsZero() == failReset {
					return injected
				}
				return nil
			}
			err := overlayIO(ctx, setDeadline, func() error {
				closed = true
				return nil
			}, func() error {
				cancel()
				<-interrupted
				return nil
			})
			if !errors.Is(err, context.Canceled) || !errors.Is(err, injected) {
				t.Fatalf("lost cancellation or deadline error: %v", err)
			}
			if closed == failReset {
				t.Fatalf("fallback close = %v, want %v", closed, !failReset)
			}
		})
	}
}
