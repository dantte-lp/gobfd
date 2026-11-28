//go:build linux

package bfdload

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"
)

type startupRecordHandler struct {
	slog.Handler

	onCreated func()
	message   string
}

func (startupRecordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h startupRecordHandler) Handle(ctx context.Context, record slog.Record) error {
	message := h.message
	if message == "" {
		message = "session created"
	}
	if record.Message == message {
		h.onCreated()
	}
	if err := h.Handler.Handle(ctx, record); err != nil {
		return err
	}
	return nil
}

func (h startupRecordHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.Handler = h.Handler.WithAttrs(attrs)
	return h
}

func (h startupRecordHandler) WithGroup(name string) slog.Handler {
	h.Handler = h.Handler.WithGroup(name)
	return h
}

func TestOwnerLateStartupCancellationReleasesActualSenderAndListeners(t *testing.T) {
	runLateStartupCancellation(t, false)
}

func TestEchoOwnerLateStartupCancellationReleasesActualSenderAndListeners(t *testing.T) {
	runLateStartupCancellation(t, true)
}

func runLateStartupCancellation(t *testing.T, echo bool) {
	t.Helper()
	first := OwnerEndpoint{
		Local: netip.MustParseAddrPort("127.0.0.1:37975"), Peer: netip.MustParseAddrPort("127.32.0.2:37977"),
	}
	second := OwnerEndpoint{
		Local: netip.MustParseAddrPort("127.0.0.1:37976"), Peer: netip.MustParseAddrPort("127.32.0.3:37978"),
	}
	message := "session created"
	if echo {
		first.Local = netip.MustParseAddrPort("127.0.0.1:3785")
		second.Local = netip.MustParseAddrPort("127.0.0.4:3785")
		message = "echo session created"
	}
	peer, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(first.Peer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := peer.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var source netip.AddrPort
	var readErr error
	var read int
	created := 0
	handler := startupRecordHandler{Handler: slog.DiscardHandler, message: message, onCreated: func() {
		created++
		var wire [64]byte
		read, source, readErr = peer.ReadFromUDPAddrPort(wire[:])
		cancel()
	}}
	owner, startErr := StartOwner(ctx, OwnerConfig{
		Echo:     echo,
		Sessions: []OwnerEndpoint{first, second}, Interval: 100 * time.Millisecond, DetectMultiplier: 3,
	}, slog.New(handler))
	if owner != nil {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("canceled startup returned an owner")
	}
	if !errors.Is(startErr, context.Canceled) || created != 1 || readErr != nil || read != 24 || source.Port() < 49152 {
		t.Fatalf("failure did not follow a real sender/session: created=%d read=%d source=%s readErr=%v startErr=%v",
			created, read, source, readErr, startErr)
	}
	for _, binding := range []netip.AddrPort{first.Local, second.Local, source} {
		rebound, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(binding))
		if err != nil {
			t.Fatalf("late startup leaked %s: %v", binding, err)
		}
		if err := rebound.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
