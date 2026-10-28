package netio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// rtnetlinkTimeout bounds one request when the context has no earlier deadline.
	rtnetlinkTimeout = 5 * time.Second
	rtnetlinkSeq     = 1
	rtnetlinkBufSize = 32 << 10
	nlaTypeMask      = ^uint16(unix.NLA_F_NESTED | unix.NLA_F_NET_BYTEORDER)
)

var errMalformedRTNetlink = errors.New("malformed rtnetlink message")

// rtnetlinkBondLinks implements KernelBondLinks with one NETLINK_ROUTE
// socket per request in the caller's network namespace.
type rtnetlinkBondLinks struct{}

// Link returns the ifindex, IFLA_INFO_KIND and IFLA_MASTER of name.
func (rtnetlinkBondLinks) Link(ctx context.Context, name string) (KernelBondLink, error) {
	req := linkRequest(unix.RTM_GETLINK, 0, rtattr(unix.IFLA_IFNAME, append([]byte(name), 0)))
	var link KernelBondLink
	found := false
	err := rtnetlinkExchange(ctx, req, func(msg syscall.NetlinkMessage) error {
		if msg.Header.Type != unix.RTM_NEWLINK {
			return nil
		}
		parsed, err := parseKernelBondLink(msg.Data)
		link, found = parsed, err == nil
		return err
	})
	if err != nil {
		return KernelBondLink{}, fmt.Errorf("rtnetlink get link %q: %w", name, err)
	}
	if !found {
		return KernelBondLink{}, fmt.Errorf("rtnetlink get link %q: no link in reply: %w", name, errMalformedRTNetlink)
	}
	return link, nil
}

// SetMaster sets IFLA_MASTER of the link index to masterIndex; 0 releases it.
func (rtnetlinkBondLinks) SetMaster(ctx context.Context, index, masterIndex int) error {
	value := make([]byte, 4)
	binary.NativeEndian.PutUint32(value, uint32(masterIndex)) // #nosec G115 -- ifindex is a positive kernel int32.
	if err := rtnetlinkExchange(ctx, linkRequest(unix.RTM_NEWLINK, index, rtattr(unix.IFLA_MASTER, value)), nil); err != nil {
		return fmt.Errorf("rtnetlink set link %d master %d: %w", index, masterIndex, err)
	}
	return nil
}

// linkRequest builds an acknowledged rtnetlink link request for ifindex
// index (0 selects the link by an IFLA_IFNAME attribute).
func linkRequest(msgType uint16, index int, attrs []byte) []byte {
	msg := make([]byte, unix.NLMSG_HDRLEN+unix.SizeofIfInfomsg, unix.NLMSG_HDRLEN+unix.SizeofIfInfomsg+len(attrs))
	msg = append(msg, attrs...)
	binary.NativeEndian.PutUint32(msg[0:4], uint32(len(msg))) // #nosec G115 -- bounded request size.
	binary.NativeEndian.PutUint16(msg[4:6], msgType)
	binary.NativeEndian.PutUint16(msg[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	binary.NativeEndian.PutUint32(msg[8:12], rtnetlinkSeq)
	ifi := msg[unix.NLMSG_HDRLEN:]
	ifi[0] = unix.AF_UNSPEC
	binary.NativeEndian.PutUint32(ifi[4:8], uint32(index)) // #nosec G115 -- ifindex is a positive kernel int32.
	return msg
}

func rtattr(attrType uint16, value []byte) []byte {
	length := unix.SizeofRtAttr + len(value)
	buf := make([]byte, rtaAlign(length))
	binary.NativeEndian.PutUint16(buf[0:2], uint16(length)) // #nosec G115 -- attribute values are a few bytes.
	binary.NativeEndian.PutUint16(buf[2:4], attrType)
	copy(buf[unix.SizeofRtAttr:], value)
	return buf
}

func rtaAlign(n int) int { return (n + unix.NLA_ALIGNTO - 1) &^ (unix.NLA_ALIGNTO - 1) }

// rtnetlinkExchange sends req and reads replies until the kernel ACK
// (NLMSG_ERROR with error 0) or an error. The context deadline, capped at
// rtnetlinkTimeout, bounds the receive.
func rtnetlinkExchange(ctx context.Context, req []byte, handle func(syscall.NetlinkMessage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(rtnetlinkTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open NETLINK_ROUTE socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.NsecToTimeval(max(time.Until(deadline), time.Millisecond).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return fmt.Errorf("set NETLINK_ROUTE receive timeout: %w", err)
	}
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("send rtnetlink request: %w", err)
	}
	buf := make([]byte, rtnetlinkBufSize)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			return fmt.Errorf("rtnetlink reply: %w", context.DeadlineExceeded)
		case err != nil:
			return fmt.Errorf("receive rtnetlink reply: %w", err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return fmt.Errorf("parse rtnetlink reply: %w", err)
		}
		for _, msg := range msgs {
			if msg.Header.Seq != rtnetlinkSeq {
				continue
			}
			if msg.Header.Type == unix.NLMSG_ERROR {
				return rtnetlinkAckError(msg.Data)
			}
			if handle != nil {
				if err := handle(msg); err != nil {
					return err
				}
			}
		}
	}
}

func rtnetlinkAckError(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("short rtnetlink ACK: %w", errMalformedRTNetlink)
	}
	code := int32(binary.NativeEndian.Uint32(data[:4])) // #nosec G115 -- nlmsgerr.error is a signed int.
	if code == 0 {
		return nil
	}
	return unix.Errno(-code)
}

// parseKernelBondLink decodes an RTM_NEWLINK body: struct ifinfomsg, then
// IFLA_MASTER and the IFLA_INFO_KIND nested in IFLA_LINKINFO.
func parseKernelBondLink(data []byte) (KernelBondLink, error) {
	if len(data) < unix.SizeofIfInfomsg {
		return KernelBondLink{}, fmt.Errorf("short ifinfomsg: %w", errMalformedRTNetlink)
	}
	link := KernelBondLink{Index: int(int32(binary.NativeEndian.Uint32(data[4:8])))} // #nosec G115 -- ifi_index is int32.
	err := walkRTAttrs(data[unix.SizeofIfInfomsg:], func(attrType uint16, value []byte) error {
		switch attrType {
		case unix.IFLA_MASTER:
			if len(value) < 4 {
				return fmt.Errorf("short IFLA_MASTER: %w", errMalformedRTNetlink)
			}
			link.MasterIndex = int(binary.NativeEndian.Uint32(value[:4]))
		case unix.IFLA_LINKINFO:
			return walkRTAttrs(value, func(infoType uint16, info []byte) error {
				if infoType == unix.IFLA_INFO_KIND {
					link.Kind = string(trimNUL(info))
				}
				return nil
			})
		}
		return nil
	})
	return link, err
}

func walkRTAttrs(b []byte, visit func(attrType uint16, value []byte) error) error {
	for len(b) >= unix.SizeofRtAttr {
		length := int(binary.NativeEndian.Uint16(b[0:2]))
		if length < unix.SizeofRtAttr || length > len(b) {
			return fmt.Errorf("attribute length %d of %d bytes: %w", length, len(b), errMalformedRTNetlink)
		}
		if err := visit(binary.NativeEndian.Uint16(b[2:4])&nlaTypeMask, b[unix.SizeofRtAttr:length]); err != nil {
			return err
		}
		b = b[min(rtaAlign(length), len(b)):]
	}
	return nil
}

func trimNUL(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}
