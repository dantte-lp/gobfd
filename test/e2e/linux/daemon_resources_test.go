//go:build e2e_linux

package linux_test

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func observeDaemonResources(pid int) ([]string, int, int, error) {
	root := fmt.Sprintf("/proc/%d", pid)
	entries, err := os.ReadDir(filepath.Join(root, "fd"))
	if err != nil {
		return nil, 0, 0, err
	}
	owned := make(map[string]bool)
	for _, entry := range entries {
		target, linkErr := os.Readlink(filepath.Join(root, "fd", entry.Name()))
		if errors.Is(linkErr, os.ErrNotExist) {
			continue
		}
		if linkErr != nil {
			return nil, 0, 0, linkErr
		}
		if inode, found := strings.CutPrefix(target, "socket:["); found {
			owned[strings.TrimSuffix(inode, "]")] = true
		}
	}
	var sockets []string
	for _, table := range []string{"udp", "udp6"} {
		data, readErr := os.ReadFile(filepath.Join(root, "net", table))
		if readErr != nil {
			return nil, 0, 0, readErr
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 9 && owned[fields[9]] {
				sockets = append(sockets, fields[1]+"@"+fields[9])
			}
		}
	}
	slices.Sort(sockets)
	status, err := os.ReadFile(filepath.Join(root, "status"))
	if err != nil {
		return nil, 0, 0, err
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if count, found := strings.CutPrefix(line, "Threads:"); found {
			threads, parseErr := strconv.Atoi(strings.TrimSpace(count))
			return sockets, len(entries), threads, parseErr
		}
	}
	return nil, 0, 0, errors.New("child status has no thread count")
}

func observeDaemonAddresses() (map[string][]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, iface := range interfaces {
		if iface.Name != "gobfdp0" && iface.Name != pendingLink {
			continue
		}
		addresses, addrErr := iface.Addrs()
		if addrErr != nil {
			return nil, addrErr
		}
		for _, addr := range addresses {
			result[iface.Name] = append(result[iface.Name], addr.String())
		}
		slices.Sort(result[iface.Name])
	}
	return result, nil
}

func daemonAddressPresent(index int, address netip.Addr) (bool, error) {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return false, err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return false, err
	}
	for _, addr := range addresses {
		prefix, parseErr := netip.ParsePrefix(addr.String())
		if parseErr != nil {
			return false, parseErr
		}
		if prefix.Addr() == address {
			return true, nil
		}
	}
	return false, nil
}
