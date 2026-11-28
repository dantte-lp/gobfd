package netio

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// ErrLocalAddrNotOwned indicates that no single local interface owns a
// single-hop session's local address.
var ErrLocalAddrNotOwned = errors.New("local address is not assigned to exactly one interface")

// ResolveSessionIfIndex returns the receive interface index of a single-hop
// session (RFC 5881 Section 3): the configured interface, else the zone of a
// link-local address, else the one interface that owns the local address. An
// invalid or unspecified local address without an interface returns zero.
func ResolveSessionIfIndex(local netip.Addr, ifName string) (int, error) {
	if ifName == "" {
		ifName = local.Zone()
	}
	if ifName != "" {
		iface, err := net.InterfaceByName(ifName)
		if err != nil {
			return 0, fmt.Errorf("resolve interface %q: %w", ifName, err)
		}
		return iface.Index, nil
	}
	local = local.Unmap()
	if !local.IsValid() || local.IsUnspecified() {
		return 0, nil
	}
	return interfaceOwning(local)
}

// interfaceOwning returns the index of the one interface that has local assigned.
func interfaceOwning(local netip.Addr) (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, fmt.Errorf("list interfaces: %w", err)
	}
	index := 0
	for _, iface := range ifaces {
		owns, err := interfaceHasAddr(iface, local)
		if err != nil {
			return 0, err
		}
		if !owns {
			continue
		}
		if index != 0 {
			return 0, fmt.Errorf("%s is assigned to several interfaces: %w", local, ErrLocalAddrNotOwned)
		}
		index = iface.Index
	}
	if index == 0 {
		return 0, fmt.Errorf("%s: %w", local, ErrLocalAddrNotOwned)
	}
	return index, nil
}

func interfaceHasAddr(iface net.Interface, local netip.Addr) (bool, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return false, fmt.Errorf("list addresses of %s: %w", iface.Name, err)
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(ipNet.IP); ok && ip.Unmap() == local {
				return true, nil
			}
		}
	}
	return false, nil
}
