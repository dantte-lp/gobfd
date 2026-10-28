//go:build linux

package netio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

var errInvalidAddressInventory = errors.New("invalid or interrupted local address inventory")

const localAddressFlagsSize = 4

type localAddressAssignment struct {
	index  int
	usable bool
}

// CheckLocalAddressesAvailable inspects bind dependencies in one Linux address
// dump. Tentative and DAD-failed addresses cannot satisfy a dependency.
func CheckLocalAddressesAvailable(claims []LocalAddress) (int, error) {
	needed := false
	for _, claim := range claims {
		if claim.Addr.IsValid() && !claim.Addr.IsUnspecified() {
			needed = true
			break
		}
	}
	if !needed {
		return 0, nil
	}
	assigned, err := readAssignedLocalAddresses()
	if err != nil {
		return 0, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return 0, fmt.Errorf("list local address interfaces: %w", err)
	}
	byName := localInterfaceIndices(interfaces)
	for _, claim := range claims {
		addr := claim.Addr.Unmap()
		if !addr.IsValid() || addr.IsUnspecified() {
			continue
		}
		if _, known := assigned[addr.WithZone("")]; known {
			continue
		}
		// A kernel-local route may permit binding an address absent from GETADDR.
		bindErr := probeLocalAddress(claim)
		if bindErr != nil {
			if errors.Is(bindErr, unix.EADDRNOTAVAIL) {
				continue
			}
			return 0, fmt.Errorf("probe local address bind: %w", bindErr)
		}
		index, _ := localAddressScopeIndex(claim, byName)
		assigned[addr.WithZone("")] = []localAddressAssignment{{index: index, usable: true}}
	}
	return checkLocalAddresses(claims, assigned, interfaces)
}

func probeLocalAddress(claim LocalAddress) error {
	addr := claim.Addr.Unmap()
	network := "udp4"
	if addr.Is6() {
		network = "udp6"
	}
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			optionErr = setSenderSockOpts(int(fd), addr.Is6(), false, claim.Interface, nil, nil)
			if optionErr == nil {
				// A probe must never share an existing session's port.
				optionErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 0)
			}
		}); err != nil {
			return fmt.Errorf("control local address probe: %w", err)
		}
		if optionErr != nil {
			return fmt.Errorf("configure local address probe: %w", optionErr)
		}
		return nil
	}}
	conn, err := lc.ListenPacket(context.Background(), network, netip.AddrPortFrom(addr, 0).String())
	if err != nil {
		return fmt.Errorf("bind local address probe: %w", err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close local address probe: %w", err)
	}
	return nil
}

func readAssignedLocalAddresses() (map[netip.Addr][]localAddressAssignment, error) {
	data, err := syscall.NetlinkRIB(unix.RTM_GETADDR, unix.AF_UNSPEC)
	if err != nil {
		return nil, fmt.Errorf("read local address inventory: %w", err)
	}
	messages, err := syscall.ParseNetlinkMessage(data)
	if err != nil {
		return nil, fmt.Errorf("parse local address inventory: %w", err)
	}
	return assignedLocalAddresses(messages)
}

func assignedLocalAddresses(messages []syscall.NetlinkMessage) (map[netip.Addr][]localAddressAssignment, error) {
	assigned := make(map[netip.Addr][]localAddressAssignment)
	for _, msg := range messages {
		if msg.Header.Flags&unix.NLM_F_DUMP_INTR != 0 {
			return nil, errInvalidAddressInventory
		}
		if msg.Header.Type == unix.NLMSG_DONE && len(msg.Data) > 0 {
			if len(msg.Data) < localAddressFlagsSize || binary.NativeEndian.Uint32(msg.Data[:localAddressFlagsSize]) != 0 {
				return nil, errInvalidAddressInventory
			}
		}
		if msg.Header.Type != unix.RTM_NEWADDR {
			continue
		}
		addr, assignment, err := localAddressFromMessage(msg)
		if err != nil {
			return nil, err
		}
		if addr.IsValid() {
			assigned[addr] = append(assigned[addr], assignment)
		}
	}
	return assigned, nil
}

func localAddressFromMessage(msg syscall.NetlinkMessage) (netip.Addr, localAddressAssignment, error) {
	var assignment localAddressAssignment
	if len(msg.Data) < unix.SizeofIfAddrmsg {
		return netip.Addr{}, assignment, errInvalidAddressInventory
	}
	family := msg.Data[0]
	if family != unix.AF_INET && family != unix.AF_INET6 {
		return netip.Addr{}, assignment, nil
	}
	index := binary.NativeEndian.Uint32(msg.Data[4:8])
	if index == 0 || index > math.MaxInt32 {
		return netip.Addr{}, assignment, errInvalidAddressInventory
	}
	local, flags, err := localAddressAttributes(msg)
	if err != nil {
		return netip.Addr{}, assignment, err
	}
	if len(local) == 0 {
		return netip.Addr{}, assignment, nil
	}
	if (family == unix.AF_INET && len(local) != net.IPv4len) || (family == unix.AF_INET6 && len(local) != net.IPv6len) {
		return netip.Addr{}, assignment, errInvalidAddressInventory
	}
	addr, ok := netip.AddrFromSlice(local)
	if !ok {
		return netip.Addr{}, assignment, errInvalidAddressInventory
	}
	assignment = localAddressAssignment{index: int(index), usable: flags&(unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED) == 0}
	return addr.Unmap(), assignment, nil
}

func localAddressAttributes(msg syscall.NetlinkMessage) ([]byte, uint32, error) {
	attrs, err := syscall.ParseNetlinkRouteAttr(&msg)
	if err != nil {
		return nil, 0, fmt.Errorf("parse assigned address attributes: %w", err)
	}
	flags := uint32(msg.Data[2])
	var local, peer []byte
	for _, attr := range attrs {
		switch attr.Attr.Type {
		case unix.IFA_LOCAL:
			local = attr.Value
		case unix.IFA_ADDRESS:
			peer = attr.Value
		case unix.IFA_FLAGS:
			if len(attr.Value) != localAddressFlagsSize {
				return nil, 0, errInvalidAddressInventory
			}
			flags = binary.NativeEndian.Uint32(attr.Value)
		}
	}
	if local == nil {
		local = peer
	} // Point-to-point IFA_ADDRESS is the remote address.
	return local, flags, nil
}

func checkLocalAddresses(
	claims []LocalAddress, assigned map[netip.Addr][]localAddressAssignment, interfaces []net.Interface,
) (int, error) {
	byName := localInterfaceIndices(interfaces)
	var unavailable []error
	for _, claim := range claims {
		addr := claim.Addr.Unmap()
		if !addr.IsValid() || addr.IsUnspecified() {
			continue
		}
		indices := assigned[addr.WithZone("")]
		if localAddressPresent(claim, indices, byName) {
			continue
		}
		unavailable = append(unavailable, bfd.NewResourceUnavailableError(bfd.ResourceRef{
			Kind: bfd.ResourceKindAddress, ID: addr.WithZone("").String(),
		}))
	}
	return len(unavailable), errors.Join(unavailable...)
}

func localInterfaceIndices(interfaces []net.Interface) map[string]int {
	byName := make(map[string]int, len(interfaces))
	for _, iface := range interfaces {
		byName[iface.Name] = iface.Index
	}
	return byName
}

func localAddressScopeIndex(claim LocalAddress, byName map[string]int) (int, bool) {
	if !claim.Addr.Is6() || !claim.Addr.IsLinkLocalUnicast() {
		return 0, false
	}
	zone := claim.Addr.Zone()
	if zone == "" {
		zone = claim.Interface
	}
	if zone == "" {
		return 0, false
	}
	if index, ok := byName[zone]; ok {
		return index, true
	}
	index, err := strconv.Atoi(zone)
	if err != nil {
		return 0, true
	}
	return index, true
}

func localAddressPresent(claim LocalAddress, assignments []localAddressAssignment, byName map[string]int) bool {
	index, scoped := localAddressScopeIndex(claim, byName)
	for _, assignment := range assignments {
		if !assignment.usable {
			continue
		}
		if !scoped || (index > 0 && assignment.index == index) {
			return true
		}
	}
	return false
}
