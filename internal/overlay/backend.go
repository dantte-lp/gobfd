// Package overlay owns the shared overlay dataplane vocabulary.
package overlay

import "strings"

// Backend identifies an overlay dataplane integration.
type Backend string

const (
	// BackendUserspaceUDP selects the implemented userspace UDP backend.
	BackendUserspaceUDP = "userspace-udp"
	// BackendKernel selects the reserved Linux kernel backend.
	BackendKernel = "kernel"
	// BackendOVS selects the reserved Open vSwitch backend.
	BackendOVS = "ovs"
	// BackendOVN selects the reserved OVN backend.
	BackendOVN = "ovn"
	// BackendCilium selects the reserved Cilium backend.
	BackendCilium = "cilium"
	// BackendCalico selects the reserved Calico backend.
	BackendCalico = "calico"
	// BackendNSX selects the reserved NSX backend.
	BackendNSX = "nsx"
)

// ParseBackend normalizes and classifies an overlay backend value.
// Empty values select the default userspace UDP backend.
func ParseBackend(value string) (Backend, bool) {
	backend := Backend(strings.TrimSpace(value))
	if backend == "" {
		return BackendUserspaceUDP, true
	}

	switch backend {
	case BackendUserspaceUDP,
		BackendKernel,
		BackendOVS,
		BackendOVN,
		BackendCilium,
		BackendCalico,
		BackendNSX:
		return backend, true
	default:
		return "", false
	}
}

// Implemented reports whether the backend has a production implementation.
func (b Backend) Implemented() bool {
	return b == BackendUserspaceUDP
}
