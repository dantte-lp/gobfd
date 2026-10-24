//go:build linux

package bfdload

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	netlinkAckTimeout             = 5 * time.Second
	netlinkPollMS                 = 100
	netlinkErrnoSize              = 4
	ipv4AddressMessageSize uint32 = 40
	ipv6AddressMessageSize uint32 = 44
)

func addressRequest(sequence uint32, index int, address netip.Addr) ([]byte, error) {
	if sequence == 0 || index <= 0 || index > math.MaxInt32 || !ownerUnicast(address) {
		return nil, fmt.Errorf("netlink address identity: %w", ErrTopology)
	}
	raw := address.AsSlice()
	attributeSize := unix.SizeofRtAttr + len(raw)
	length := ipv6AddressMessageSize
	if address.Is4() {
		length = ipv4AddressMessageSize
	}
	wire := make([]byte, length)
	binary.NativeEndian.PutUint32(wire[:4], length)
	binary.NativeEndian.PutUint16(wire[4:6], unix.RTM_NEWADDR)
	binary.NativeEndian.PutUint16(wire[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL)
	binary.NativeEndian.PutUint32(wire[8:12], sequence)
	body := wire[unix.NLMSG_HDRLEN:]
	body[0], body[1] = unix.AF_INET6, 128
	body[2] = unix.IFA_F_NODAD
	if address.Is4() {
		body[0], body[1], body[2] = unix.AF_INET, 32, 0
	}
	binary.NativeEndian.PutUint32(body[4:8], uint32(index))
	attributes := body[unix.SizeofIfAddrmsg:]
	if address.Is4() {
		writeAddressAttribute(attributes[:attributeSize], unix.IFA_LOCAL, raw)
		attributes = attributes[attributeSize:]
	}
	writeAddressAttribute(attributes, unix.IFA_ADDRESS, raw)
	return wire, nil
}

func writeAddressAttribute(destination []byte, kind uint16, address []byte) {
	length := uint16(unix.SizeofRtAttr + net.IPv4len)
	if len(address) == net.IPv6len {
		length = unix.SizeofRtAttr + net.IPv6len
	}
	binary.NativeEndian.PutUint16(destination[:2], length)
	binary.NativeEndian.PutUint16(destination[2:4], kind)
	copy(destination[unix.SizeofRtAttr:], address)
}

func sendAddressRequest(ctx context.Context, fd int, wire []byte, sequence uint32) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("send topology address request: %w", err)
	}
	if err := unix.Sendto(fd, wire, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("send topology netlink message: %w", err)
	}
	return readAddressAck(ctx, fd, sequence)
}

func readAddressAck(ctx context.Context, fd int, sequence uint32) error {
	if fd < 0 || fd > math.MaxInt32 {
		return fmt.Errorf("invalid topology netlink descriptor: %w", ErrTopology)
	}
	ackCtx, cancel := context.WithTimeout(ctx, netlinkAckTimeout)
	defer cancel()
	var buffer [8192]byte
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		if err := ackCtx.Err(); err != nil {
			return fmt.Errorf("wait for topology address ACK: %w", err)
		}
		if _, err := unix.Poll(poll, netlinkPollMS); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("poll topology address ACK: %w", err)
		}
		n, _, flags, from, err := unix.Recvmsg(fd, buffer[:], nil, unix.MSG_DONTWAIT)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("receive topology address ACK: %w", err)
		}
		if flags&unix.MSG_TRUNC != 0 {
			return fmt.Errorf("truncated topology address ACK: %w", ErrTopology)
		}
		matched, err := addressAck(buffer[:n], from, sequence)
		if err != nil || matched {
			return err
		}
	}
}

func addressAck(wire []byte, from unix.Sockaddr, sequence uint32) (bool, error) {
	source, ok := from.(*unix.SockaddrNetlink)
	if !ok || source.Pid != 0 || source.Groups != 0 {
		return false, fmt.Errorf("non-kernel topology ACK: %w", ErrTopology)
	}
	messages, err := parseAddressMessages(wire)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message.Header.Seq != sequence || message.Header.Type != unix.NLMSG_ERROR {
			continue
		}
		if len(message.Data) < netlinkErrnoSize {
			return false, fmt.Errorf("short topology ACK: %w", ErrTopology)
		}
		code := binary.NativeEndian.Uint32(message.Data[:4])
		if code == 0 {
			return true, nil
		}
		if code <= math.MaxInt32 {
			return true, fmt.Errorf("positive topology ACK errno: %w", ErrTopology)
		}
		// Kernel errors are negative int32 encoded in the native-endian uint32 word.
		return true, fmt.Errorf("topology address ACK: %w", unix.Errno(-code))
	}
	return false, nil
}

func parseAddressMessages(wire []byte) ([]syscall.NetlinkMessage, error) {
	messages, err := syscall.ParseNetlinkMessage(wire)
	if err != nil {
		return nil, fmt.Errorf("parse topology ACK: %w", err)
	}
	var consumed uint64
	for _, message := range messages {
		consumed += (uint64(message.Header.Len) + unix.NLMSG_ALIGNTO - 1) &^ (unix.NLMSG_ALIGNTO - 1)
	}
	if len(messages) == 0 || consumed != uint64(len(wire)) {
		return nil, fmt.Errorf("incomplete topology ACK datagram: %w", ErrTopology)
	}
	return messages, nil
}
