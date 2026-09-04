package netio

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/overlay"
)

// OverlayBackendType selects the owner-specific overlay dataplane integration.
type OverlayBackendType = overlay.Backend

const (
	// OverlayBackendUserspaceUDP selects the implemented userspace UDP backend.
	OverlayBackendUserspaceUDP OverlayBackendType = overlay.BackendUserspaceUDP
	// OverlayBackendKernel selects the reserved Linux kernel backend.
	OverlayBackendKernel OverlayBackendType = overlay.BackendKernel
	// OverlayBackendOVS selects the reserved Open vSwitch backend.
	OverlayBackendOVS OverlayBackendType = overlay.BackendOVS
	// OverlayBackendOVN selects the reserved OVN backend.
	OverlayBackendOVN OverlayBackendType = overlay.BackendOVN
	// OverlayBackendCilium selects the reserved Cilium backend.
	OverlayBackendCilium OverlayBackendType = overlay.BackendCilium
	// OverlayBackendCalico selects the reserved Calico backend.
	OverlayBackendCalico OverlayBackendType = overlay.BackendCalico
	// OverlayBackendNSX selects the reserved NSX backend.
	OverlayBackendNSX OverlayBackendType = overlay.BackendNSX
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
	Scopes        []bfd.TransportScope
}

// GeneveOverlayBackendConfig configures a Geneve BFD overlay backend.
type GeneveOverlayBackendConfig struct {
	Backend    OverlayBackendType
	LocalAddr  netip.Addr
	VNI        uint32
	SourcePort uint16
	Logger     *slog.Logger
	Scopes     []bfd.TransportScope
}

// NewVXLANOverlayBackend creates the configured VXLAN BFD overlay backend.
func NewVXLANOverlayBackend(cfg VXLANOverlayBackendConfig) (OverlayConn, error) {
	backend, recognized := overlay.ParseBackend(string(cfg.Backend))
	if !recognized {
		return nil, fmt.Errorf("vxlan overlay backend %q: %w", cfg.Backend, ErrInvalidOverlayBackend)
	}
	if !cfg.LocalAddr.IsValid() {
		return nil, fmt.Errorf("vxlan local address: %w", ErrInvalidOverlayBackendInput)
	}

	if !backend.Implemented() {
		return nil, fmt.Errorf("vxlan overlay backend %q: %w", backend, ErrUnsupportedOverlayBackend)
	}
	if len(cfg.Scopes) > 0 {
		return NewVXLANConnForScopes(cfg.LocalAddr, cfg.SourcePort, cfg.Scopes, overlayBackendLogger(cfg.Logger))
	}
	return NewVXLANConn(cfg.LocalAddr, cfg.ManagementVNI, cfg.SourcePort, overlayBackendLogger(cfg.Logger))
}

// NewGeneveOverlayBackend creates the configured Geneve BFD overlay backend.
func NewGeneveOverlayBackend(cfg GeneveOverlayBackendConfig) (OverlayConn, error) {
	backend, recognized := overlay.ParseBackend(string(cfg.Backend))
	if !recognized {
		return nil, fmt.Errorf("geneve overlay backend %q: %w", cfg.Backend, ErrInvalidOverlayBackend)
	}
	if !cfg.LocalAddr.IsValid() {
		return nil, fmt.Errorf("geneve local address: %w", ErrInvalidOverlayBackendInput)
	}

	if !backend.Implemented() {
		return nil, fmt.Errorf("geneve overlay backend %q: %w", backend, ErrUnsupportedOverlayBackend)
	}
	if len(cfg.Scopes) > 0 {
		return NewGeneveConnForScopes(cfg.LocalAddr, cfg.SourcePort, cfg.Scopes, overlayBackendLogger(cfg.Logger))
	}
	return NewGeneveConn(cfg.LocalAddr, cfg.VNI, cfg.SourcePort, overlayBackendLogger(cfg.Logger))
}

func overlayBackendLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
