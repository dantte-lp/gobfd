//go:build linux

package netio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	netlinkReadBufferSize    = 8192
	netlinkReceiveTimeoutSec = 1
)

var (
	errShortIfInfoMsg  = errors.New("short ifinfomsg")
	errIfIndexOverflow = errors.New("ifinfomsg index exceeds int32")
	errShortIfAddrMsg  = errors.New("short ifaddrmsg")
)

// LinuxInterfaceMonitor watches Linux rtnetlink link and address notifications.
type LinuxInterfaceMonitor struct {
	fd            int
	events        chan InterfaceEvent
	logger        *slog.Logger
	closeOnce     sync.Once
	resyncPending bool
}

// NewInterfaceMonitor creates the platform default interface monitor.
func NewInterfaceMonitor(logger *slog.Logger) (InterfaceMonitor, error) {
	return NewLinuxInterfaceMonitor(logger)
}

// NewLinuxInterfaceMonitor subscribes to NETLINK_ROUTE link and IPv4/IPv6
// address notifications. Receiving these groups needs no capability: the
// kernel creates NETLINK_ROUTE with NL_CFG_F_NONROOT_RECV.
func NewLinuxInterfaceMonitor(logger *slog.Logger) (*LinuxInterfaceMonitor, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("open netlink route socket: %w", err)
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{
		Sec: netlinkReceiveTimeoutSec,
	}); err != nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			logger.Warn("failed to close netlink socket after timeout setup failure",
				slog.String("error", closeErr.Error()),
			)
		}
		return nil, fmt.Errorf("set netlink receive timeout: %w", err)
	}

	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR,
	}
	if err := unix.Bind(fd, addr); err != nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			logger.Warn("failed to close netlink socket after bind failure",
				slog.String("error", closeErr.Error()),
			)
		}
		return nil, fmt.Errorf("bind netlink route socket: %w", err)
	}

	return &LinuxInterfaceMonitor{
		fd:     fd,
		events: make(chan InterfaceEvent, 64),
		logger: logger.With(slog.String("component", "ifmon.netlink")),
	}, nil
}

// Run reads link and address notifications until ctx is cancelled.
func (m *LinuxInterfaceMonitor) Run(ctx context.Context) error {
	m.logger.Info("linux netlink interface monitor started")
	defer close(m.events)

	runCtx, cancel := context.WithCancel(ctx)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		<-runCtx.Done()
		if err := m.Close(); err != nil {
			m.logger.Warn("failed to close netlink socket on context cancellation",
				slog.String("error", err.Error()),
			)
		}
	}()
	defer func() {
		cancel()
		<-closed
	}()

	buf := make([]byte, netlinkReadBufferSize)
	for {
		n, _, flags, _, err := unix.Recvmsg(m.fd, buf, nil, 0)
		if err != nil {
			continueReading, recvErr := m.handleReceiveError(ctx, err)
			if recvErr != nil {
				return recvErr
			}
			if !continueReading {
				return nil
			}

			continue
		}

		if flags&unix.MSG_TRUNC != 0 {
			m.requestResync()
			continue
		}
		if err := m.handleNetlinkBuffer(buf[:n]); err != nil {
			m.requestResync()
			m.logger.Warn("failed to parse netlink link event",
				slog.String("error", err.Error()),
			)
		}
	}
}

func (m *LinuxInterfaceMonitor) handleReceiveError(ctx context.Context, err error) (bool, error) {
	switch {
	case ctx.Err() != nil || errors.Is(err, unix.EBADF):
		m.logger.Info("linux netlink interface monitor stopped")

		return false, nil //nolint:nilerr // Cancellation and EBADF are normal monitor shutdown signals.
	case errors.Is(err, unix.EINTR), errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EWOULDBLOCK):
		m.flushResync()
		return true, nil
	case errors.Is(err, unix.ENOBUFS):
		m.logger.Warn("netlink receive buffer overflow; requesting interface resync")
		m.requestResync()

		return true, nil
	default:
		return false, fmt.Errorf("receive netlink link event: %w", err)
	}
}

// Events returns interface state changes.
func (m *LinuxInterfaceMonitor) Events() <-chan InterfaceEvent {
	return m.events
}

// Close closes the netlink socket.
func (m *LinuxInterfaceMonitor) Close() error {
	var err error
	m.closeOnce.Do(func() {
		err = unix.Close(m.fd)
	})
	if err != nil {
		return fmt.Errorf("close netlink route socket: %w", err)
	}

	return nil
}

func (m *LinuxInterfaceMonitor) handleNetlinkBuffer(buf []byte) error {
	msgs, err := syscall.ParseNetlinkMessage(buf)
	if err != nil {
		return fmt.Errorf("parse netlink messages: %w", err)
	}
	for _, msg := range msgs {
		if m.resyncPending {
			m.flushResync()
			continue
		}
		if msg.Header.Type == syscall.NLMSG_OVERRUN {
			m.requestResync()
			continue
		}
		ev, ok, err := linkEventFromNetlinkMessage(msg)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		select {
		case m.events <- ev:
		default:
			m.resyncPending = true
		}
	}
	m.flushResync()
	return nil
}

func (m *LinuxInterfaceMonitor) requestResync() {
	m.resyncPending = true
	m.flushResync()
}

func (m *LinuxInterfaceMonitor) flushResync() {
	if !m.resyncPending {
		return
	}
	select {
	case m.events <- InterfaceEvent{Resync: true}:
		m.resyncPending = false
	default:
	}
}

func linkEventFromNetlinkMessage(msg syscall.NetlinkMessage) (InterfaceEvent, bool, error) {
	switch msg.Header.Type {
	case syscall.RTM_NEWLINK, syscall.RTM_DELLINK:
	case syscall.RTM_NEWADDR, syscall.RTM_DELADDR:
		return addressEventFromNetlinkMessage(msg)
	default:
		return InterfaceEvent{}, false, nil
	}
	if len(msg.Data) < syscall.SizeofIfInfomsg {
		return InterfaceEvent{}, false, fmt.Errorf("%w: %d", errShortIfInfoMsg, len(msg.Data))
	}

	info, err := parseIfInfomsg(msg.Data[:syscall.SizeofIfInfomsg])
	if err != nil {
		return InterfaceEvent{}, false, err
	}

	ifName, err := linkNameFromAttrs(msg)
	if err != nil {
		return InterfaceEvent{}, false, err
	}
	if ifName == "" {
		ifName = fmt.Sprintf("if%d", info.Index)
	}

	up := msg.Header.Type == syscall.RTM_NEWLINK &&
		info.Flags&uint32(syscall.IFF_UP) != 0 &&
		info.Flags&uint32(syscall.IFF_RUNNING) != 0

	return InterfaceEvent{
		IfName:  ifName,
		IfIndex: int(info.Index),
		Up:      up,
	}, true, nil
}

func parseIfInfomsg(buf []byte) (syscall.IfInfomsg, error) {
	idx := binary.NativeEndian.Uint32(buf[4:8])
	if idx > math.MaxInt32 {
		return syscall.IfInfomsg{}, fmt.Errorf("%w: %d", errIfIndexOverflow, idx)
	}

	return syscall.IfInfomsg{
		Family: buf[0],
		Type:   binary.NativeEndian.Uint16(buf[2:4]),
		Index:  int32(idx),
		Flags:  binary.NativeEndian.Uint32(buf[8:12]),
		Change: binary.NativeEndian.Uint32(buf[12:16]),
	}, nil
}

func linkNameFromAttrs(msg syscall.NetlinkMessage) (string, error) {
	attrs, err := syscall.ParseNetlinkRouteAttr(&msg)
	if err != nil {
		return "", fmt.Errorf("parse netlink route attributes: %w", err)
	}
	for _, attr := range attrs {
		if attr.Attr.Type == syscall.IFLA_IFNAME {
			return strings.TrimRight(string(attr.Value), "\x00"), nil
		}
	}
	return "", nil
}

// addressEventFromNetlinkMessage reports the interface index of an
// RTM_NEWADDR or RTM_DELADDR notification (struct ifaddrmsg).
func addressEventFromNetlinkMessage(msg syscall.NetlinkMessage) (InterfaceEvent, bool, error) {
	if len(msg.Data) < syscall.SizeofIfAddrmsg {
		return InterfaceEvent{}, false, fmt.Errorf("%w: %d", errShortIfAddrMsg, len(msg.Data))
	}
	index := binary.NativeEndian.Uint32(msg.Data[4:8])
	if index > math.MaxInt32 {
		return InterfaceEvent{}, false, fmt.Errorf("%w: %d", errIfIndexOverflow, index)
	}
	return InterfaceEvent{IfIndex: int(index), Address: true}, true, nil
}
