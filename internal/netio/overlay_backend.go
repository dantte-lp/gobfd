package netio

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
)

// OverlayBackendType selects the owner-specific overlay dataplane integration.
type OverlayBackendType string

const (
	// OverlayBackendUserspaceUDP selects the implemented userspace UDP backend.
	OverlayBackendUserspaceUDP OverlayBackendType = "userspace-udp"
	// OverlayBackendKernel selects the reserved Linux kernel backend.
	OverlayBackendKernel OverlayBackendType = "kernel"
	// OverlayBackendOVS selects the reserved Open vSwitch backend.
	OverlayBackendOVS OverlayBackendType = "ovs"
	// OverlayBackendOVN selects the reserved OVN backend.
	OverlayBackendOVN OverlayBackendType = "ovn"
	// OverlayBackendCilium selects the reserved Cilium backend.
	OverlayBackendCilium OverlayBackendType = "cilium"
	// OverlayBackendCalico selects the reserved Calico backend.
	OverlayBackendCalico OverlayBackendType = "calico"
	// OverlayBackendNSX selects the reserved NSX backend.
	OverlayBackendNSX OverlayBackendType = "nsx"
)

var (
	// ErrInvalidOverlayBackend indicates an unrecognized overlay backend.
	ErrInvalidOverlayBackend = errors.New("invalid overlay backend")
	// ErrUnsupportedOverlayBackend indicates a recognized backend that is not implemented.
	ErrUnsupportedOverlayBackend = errors.New("unsupported overlay backend")
	// ErrInvalidOverlayBackendInput indicates invalid input for an overlay backend.
	ErrInvalidOverlayBackendInput = errors.New("invalid overlay backend input")
)

// VXLANOverlayBackendConfig configures a VXLAN BFD overlay backend.
type VXLANOverlayBackendConfig struct {
	Backend       OverlayBackendType
	LocalAddr     netip.Addr
	ManagementVNI uint32
	SourcePort    uint16
	Logger        *slog.Logger
}

// GeneveOverlayBackendConfig configures a Geneve BFD overlay backend.
type GeneveOverlayBackendConfig struct {
	Backend    OverlayBackendType
	LocalAddr  netip.Addr
	VNI        uint32
	SourcePort uint16
	Logger     *slog.Logger
}

// NewVXLANOverlayBackend creates the configured VXLAN BFD overlay backend.
func NewVXLANOverlayBackend(cfg VXLANOverlayBackendConfig) (OverlayConn, error) {
	backend, err := normalizeOverlayBackend(cfg.Backend)
	if err != nil {
		return nil, fmt.Errorf("vxlan overlay backend %q: %w", cfg.Backend, err)
	}
	if !cfg.LocalAddr.IsValid() {
		return nil, fmt.Errorf("vxlan local address: %w", ErrInvalidOverlayBackendInput)
	}

	switch backend {
	case OverlayBackendUserspaceUDP:
		return NewVXLANConn(cfg.LocalAddr, cfg.ManagementVNI, cfg.SourcePort, overlayBackendLogger(cfg.Logger))
	case OverlayBackendKernel,
		OverlayBackendOVS,
		OverlayBackendOVN,
		OverlayBackendCilium,
		OverlayBackendCalico,
		OverlayBackendNSX:
		return nil, fmt.Errorf("vxlan overlay backend %q: %w", backend, ErrUnsupportedOverlayBackend)
	default:
		return nil, fmt.Errorf("vxlan overlay backend %q: %w", backend, ErrInvalidOverlayBackend)
	}
}

// NewGeneveOverlayBackend creates the configured Geneve BFD overlay backend.
func NewGeneveOverlayBackend(cfg GeneveOverlayBackendConfig) (OverlayConn, error) {
	backend, err := normalizeOverlayBackend(cfg.Backend)
	if err != nil {
		return nil, fmt.Errorf("geneve overlay backend %q: %w", cfg.Backend, err)
	}
	if !cfg.LocalAddr.IsValid() {
		return nil, fmt.Errorf("geneve local address: %w", ErrInvalidOverlayBackendInput)
	}

	switch backend {
	case OverlayBackendUserspaceUDP:
		return NewGeneveConn(cfg.LocalAddr, cfg.VNI, cfg.SourcePort, overlayBackendLogger(cfg.Logger))
	case OverlayBackendKernel,
		OverlayBackendOVS,
		OverlayBackendOVN,
		OverlayBackendCilium,
		OverlayBackendCalico,
		OverlayBackendNSX:
		return nil, fmt.Errorf("geneve overlay backend %q: %w", backend, ErrUnsupportedOverlayBackend)
	default:
		return nil, fmt.Errorf("geneve overlay backend %q: %w", backend, ErrInvalidOverlayBackend)
	}
}

func normalizeOverlayBackend(backend OverlayBackendType) (OverlayBackendType, error) {
	if backend == "" {
		return OverlayBackendUserspaceUDP, nil
	}
	switch backend {
	case OverlayBackendUserspaceUDP,
		OverlayBackendKernel,
		OverlayBackendOVS,
		OverlayBackendOVN,
		OverlayBackendCilium,
		OverlayBackendCalico,
		OverlayBackendNSX:
		return backend, nil
	default:
		return "", ErrInvalidOverlayBackend
	}
}

func overlayBackendLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
