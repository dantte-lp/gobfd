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
	OverlayBackendUserspaceUDP OverlayBackendType = "userspace-udp"
	OverlayBackendKernel       OverlayBackendType = "kernel"
	OverlayBackendOVS          OverlayBackendType = "ovs"
	OverlayBackendOVN          OverlayBackendType = "ovn"
	OverlayBackendCilium       OverlayBackendType = "cilium"
	OverlayBackendNSX          OverlayBackendType = "nsx"
)

var (
	ErrInvalidOverlayBackend      = errors.New("invalid overlay backend")
	ErrUnsupportedOverlayBackend  = errors.New("unsupported overlay backend")
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
