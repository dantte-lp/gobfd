//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"golang.org/x/sys/unix"
)

// ErrTopology rejects a setup outside the isolated loopback workload boundary.
var ErrTopology = errors.New("invalid isolated workload topology")

const workloadAddressFamilies = 2

// LoopbackConfig describes unique native IPv4/IPv6 identities without changing networking.
func LoopbackConfig(profile Profile) (PeerConfig, error) {
	cfg := PeerConfig{Profile: profile, SourcePortBase: 49152}
	if err := profile.Validate(); err != nil {
		return cfg, err
	}
	ipv4 := (profile.Sessions + 1) / workloadAddressFamilies
	port := uint16(3784)
	if profile.Echo {
		port = 3785
	}
	local, peer := netip.MustParseAddr("10.253.0.1"), netip.MustParseAddr("10.253.64.1")
	for index := range profile.Sessions {
		if index == ipv4 {
			local, peer = netip.MustParseAddr("fd00:676f:6266::1"), netip.MustParseAddr("fd00:676f:6266:1::1")
		}
		cfg.Sessions = append(cfg.Sessions, OwnerEndpoint{
			Local: netip.AddrPortFrom(local, port), Peer: netip.AddrPortFrom(peer, port), Interface: "lo",
		})
		peer = peer.Next()
	}
	return cfg, cfg.Validate()
}

// NetworkNamespaceID reads the calling process's actual network namespace inode.
func NetworkNamespaceID() (uint64, error) {
	var stat unix.Stat_t
	if err := unix.Stat("/proc/self/ns/net", &stat); err != nil {
		return 0, fmt.Errorf("stat workload network namespace: %w", err)
	}
	return stat.Ino, nil
}

// TopologyObservation records only address setup in the disposable namespace.
type TopologyObservation struct {
	NamespaceID uint64 `json:"namespace_id"`
	HostID      uint64 `json:"host_id"`
	Assigned    uint64 `json:"assigned"`
	Existing    uint64 `json:"existing"`
	Completed   bool   `json:"completed"`
}

// PrepareLoopback adds addresses only in a verified private loopback namespace.
// The runner supplies its observed host inode and discards the whole owned
// namespace on partial failure; existing addresses are never deleted.
func PrepareLoopback(ctx context.Context, cfg PeerConfig, hostID uint64) (TopologyObservation, error) {
	result := TopologyObservation{HostID: hostID}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("prepare workload loopback: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	var err error
	result.NamespaceID, err = NetworkNamespaceID()
	if err != nil {
		return result, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result, fmt.Errorf("read workload interfaces: %w", err)
	}
	if namespaceErr := validateLoopbackNamespace(result.NamespaceID, hostID, interfaces); namespaceErr != nil {
		return result, namespaceErr
	}
	addresses, err := loopbackAddresses(cfg)
	if err != nil {
		return result, err
	}
	present, err := loopbackPrefixes(interfaces[0])
	if err != nil {
		return result, err
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return result, fmt.Errorf("open topology netlink socket: %w", err)
	}
	for index, address := range addresses {
		if present[netip.PrefixFrom(address, address.BitLen())] {
			result.Existing++
			continue
		}
		// Source-port capacity bounds the sequence well below uint32 exhaustion.
		wire, requestErr := addressRequest(uint32(index+1), interfaces[0].Index, address)
		if requestErr == nil {
			requestErr = sendAddressRequest(ctx, fd, wire, uint32(index+1))
		}
		if requestErr != nil {
			return result, errors.Join(fmt.Errorf("assign workload address %s: %w", address, requestErr), unix.Close(fd))
		}
		result.Assigned++
	}
	if err := unix.Close(fd); err != nil {
		return result, fmt.Errorf("close topology netlink socket: %w", err)
	}
	result.Completed = true
	return result, nil
}

func loopbackPrefixes(iface net.Interface) (map[netip.Prefix]bool, error) {
	existing, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("read existing loopback addresses: %w", err)
	}
	present := make(map[netip.Prefix]bool, len(existing))
	for _, address := range existing {
		prefix, parseErr := netip.ParsePrefix(address.String())
		if parseErr != nil {
			return nil, fmt.Errorf("parse loopback address: %w", parseErr)
		}
		present[prefix] = true
	}
	return present, nil
}

func validateLoopbackNamespace(current, host uint64, interfaces []net.Interface) error {
	if current == 0 || host == 0 || current == host || len(interfaces) != 1 {
		return fmt.Errorf("host identity or private network namespace unavailable: %w", ErrTopology)
	}
	iface := interfaces[0]
	if iface.Name != "lo" || iface.Index <= 0 || iface.Flags&net.FlagLoopback == 0 || iface.Flags&net.FlagUp == 0 {
		return fmt.Errorf("namespace is not loopback-only and up: %w", ErrTopology)
	}
	return nil
}

func loopbackAddresses(cfg PeerConfig) ([]netip.Addr, error) {
	seen := make(map[netip.Addr]bool)
	var addresses []netip.Addr
	for _, endpoint := range cfg.Sessions {
		if endpoint.Interface != "lo" {
			return nil, fmt.Errorf("loopback workload requires explicit lo binding: %w", ErrTopology)
		}
		for _, address := range []netip.Addr{endpoint.Local.Addr(), endpoint.Peer.Addr()} {
			if !seen[address] {
				seen[address] = true
				addresses = append(addresses, address)
			}
		}
	}
	return addresses, nil
}
