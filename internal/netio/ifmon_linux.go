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

const netlinkReadBufferSize = 8192

var (
	errShortIfInfoMsg  = errors.New("short ifinfomsg")
	errIfIndexOverflow = errors.New("ifinfomsg index exceeds int32")
)

// LinuxInterfaceMonitor watches Linux rtnetlink link notifications.
type LinuxInterfaceMonitor struct {
	fd        int
	events    chan InterfaceEvent
	logger    *slog.Logger
	closeOnce sync.Once
}

// NewInterfaceMonitor creates the platform default interface monitor.
func NewInterfaceMonitor(logger *slog.Logger) (InterfaceMonitor, error) {
	return NewLinuxInterfaceMonitor(logger)
}

// NewLinuxInterfaceMonitor subscribes to NETLINK_ROUTE link notifications.
func NewLinuxInterfaceMonitor(logger *slog.Logger) (*LinuxInterfaceMonitor, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("open netlink route socket: %w", err)
	}

	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: unix.RTMGRP_LINK,
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

// Run reads RTM_NEWLINK / RTM_DELLINK notifications until ctx is cancelled.
func (m *LinuxInterfaceMonitor) Run(ctx context.Context) error {
	m.logger.Info("linux netlink interface monitor started")
	defer close(m.events)

	go func() {
		<-ctx.Done()
		if err := m.Close(); err != nil {
			m.logger.Warn("failed to close netlink socket on context cancellation",
				slog.String("error", err.Error()),
			)
		}
	}()

	buf := make([]byte, netlinkReadBufferSize)
	for {
		n, _, err := unix.Recvfrom(m.fd, buf, 0)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, unix.EBADF) {
				m.logger.Info("linux netlink interface monitor stopped")
				return nil
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ENOBUFS) {
				m.logger.Warn("netlink receive buffer overflow; interface state may need resync")
				continue
			}
			return fmt.Errorf("receive netlink link event: %w", err)
		}

		if err := m.handleNetlinkBuffer(buf[:n]); err != nil {
			m.logger.Warn("failed to parse netlink link event",
				slog.String("error", err.Error()),
			)
		}
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
	return err
}

func (m *LinuxInterfaceMonitor) handleNetlinkBuffer(buf []byte) error {
	msgs, err := syscall.ParseNetlinkMessage(buf)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
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
			m.logger.Warn("interface event channel full, dropping event",
				slog.String("interface", ev.IfName),
				slog.Bool("up", ev.Up),
			)
		}
	}
	return nil
}

func linkEventFromNetlinkMessage(msg syscall.NetlinkMessage) (InterfaceEvent, bool, error) {
	switch msg.Header.Type {
	case syscall.RTM_NEWLINK, syscall.RTM_DELLINK:
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
		return "", err
	}
	for _, attr := range attrs {
		if attr.Attr.Type == syscall.IFLA_IFNAME {
			return strings.TrimRight(string(attr.Value), "\x00"), nil
		}
	}
	return "", nil
}
