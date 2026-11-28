//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/netio"
)

type echoReturnSender struct {
	conn *net.UDPConn
	port uint16
}

func newEchoReturnSender(ctx context.Context, endpoint OwnerEndpoint, sourcePort uint16) (*echoReturnSender, error) {
	network, level, option := "udp4", unix.IPPROTO_IP, unix.IP_TTL
	if endpoint.Peer.Addr().Is6() {
		network, level, option = "udp6", unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS
	}
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		controlErr := raw.Control(func(fd uintptr) {
			socketErr = unix.SetsockoptInt(int(fd), level, option, int(netio.TTLEchoLoopback))
			if socketErr == nil && endpoint.Interface != "" {
				socketErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, endpoint.Interface)
			}
		})
		return errors.Join(controlErr, socketErr)
	}}
	conn, err := config.ListenPacket(ctx, network, netip.AddrPortFrom(endpoint.Peer.Addr(), sourcePort).String())
	if err != nil {
		return nil, fmt.Errorf("bind Echo return sender: %w", err)
	}
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		return nil, errors.Join(fmt.Errorf("echo sender is not UDP: %w", ErrProfile), conn.Close())
	}
	return &echoReturnSender{conn: udp, port: endpoint.Local.Port()}, nil
}

func (s *echoReturnSender) SendPacket(ctx context.Context, wire []byte, addr netip.Addr) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("echo return canceled: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("bound Echo return write: %w", err)
	}
	written, err := s.conn.WriteToUDPAddrPort(wire, netip.AddrPortFrom(addr, s.port))
	if err != nil {
		return fmt.Errorf("write Echo return: %w", err)
	}
	if written != len(wire) {
		return fmt.Errorf("write Echo return: %w", io.ErrShortWrite)
	}
	return nil
}

func (s *echoReturnSender) Close() error {
	if err := s.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close Echo return sender: %w", err)
	}
	return nil
}

func (p *peerRuntime) echoLoop(ctx context.Context, started time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case input, open := <-p.input:
			if !open {
				return nil
			}
			if err := p.returnEcho(ctx, input, started); err != nil {
				return err
			}
		}
	}
}

func (p *peerRuntime) returnEcho(ctx context.Context, input peerInput, started time.Time) error {
	p.observeSocketDrops(input.meta)
	if !p.acceptEcho(input) {
		return nil
	}
	p.echoReadSequence = input.sequence
	now := time.Now()
	decision, err := p.decision(p.rx, now, started)
	if err != nil {
		return err
	}
	switch decision.disposition {
	case packetRandomLoss:
		p.observed.ReceiveRandomOmitted++
		return nil
	case packetBlackhole:
		p.observed.ReceiveBlackholeOmitted++
		return p.recordBlackhole(decision, true, input.packet)
	case packetTransmit:
	}
	p.observed.Accepted++
	decision, err = p.decision(p.tx, time.Now(), started)
	if err != nil {
		return err
	}
	p.observed.Scheduled++
	switch decision.disposition {
	case packetRandomLoss:
		p.observed.RandomOmitted++
		return nil
	case packetBlackhole:
		p.observed.BlackholeOmitted++
		return p.recordBlackhole(decision, false, input.packet)
	case packetTransmit:
	}
	p.observed.WriteAttempts++
	if err := p.sender.SendPacket(ctx, input.wire[:input.length], p.observed.Endpoint.Local.Addr()); err != nil {
		if ctx.Err() != nil {
			p.observed.WriteCanceled++
		} else {
			p.observed.WriteErrors++
		}
		return fmt.Errorf("return Echo datagram: %w", err)
	}
	p.observed.Written++
	p.up.Store(true) // Readiness means an actual return was written, not a remote Control Up.
	return nil
}

func (p *peerRuntime) acceptEcho(input peerInput) bool {
	if input.invalid || input.packet.AuthPresent || input.meta.SrcAddr != p.observed.Endpoint.Local.Addr() ||
		input.meta.DstAddr != p.observed.Endpoint.Peer.Addr() || netio.ValidateTTL(input.meta) != nil {
		p.observed.Rejected++
		return false
	}
	return true
}
