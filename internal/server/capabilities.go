package server

import (
	"context"

	"connectrpc.com/connect/v2"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// CapabilityRuntime contains only coarse state from the runtime owner.
// Known=false makes Enabled and Ready unknown, regardless of their values.
type CapabilityRuntime struct {
	Known   bool
	Enabled bool
	Ready   bool
}

// CapabilitySnapshot reports the five preview profiles without runtime details.
type CapabilitySnapshot struct {
	Unsolicited CapabilityRuntime
	Echo        CapabilityRuntime
	MicroBFD    CapabilityRuntime
	VXLAN       CapabilityRuntime
	Geneve      CapabilityRuntime
}

// CapabilitySource supplies one current runtime snapshot per ListCapabilities RPC.
type CapabilitySource interface {
	CapabilitySnapshot() CapabilitySnapshot
}

type capabilityServer struct {
	source CapabilitySource
}

var _ bfdv1connect.CapabilityServiceHandler = (*capabilityServer)(nil)

// RegisterCapabilities registers read-only capability inspection on rpc.
// A nil source preserves every preview profile with unknown runtime state.
func RegisterCapabilities(rpc *connect.Server, source CapabilitySource) {
	bfdv1connect.RegisterCapabilityServiceHandler(rpc, &capabilityServer{source: source})
}

func (s *capabilityServer) ListCapabilities(
	_ context.Context,
	_ *bfdv1.ListCapabilitiesRequest,
) (*bfdv1.ListCapabilitiesResponse, error) {
	var snapshot CapabilitySnapshot
	if s.source != nil {
		snapshot = s.source.CapabilitySnapshot()
	}

	profiles := [...]struct {
		kind    bfdv1.CapabilityKind
		rfc     uint32
		runtime CapabilityRuntime
	}{
		{bfdv1.CapabilityKind_CAPABILITY_KIND_UNSOLICITED, 9468, snapshot.Unsolicited},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_ECHO, 9747, snapshot.Echo},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_MICRO_BFD, 7130, snapshot.MicroBFD},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_VXLAN, 8971, snapshot.VXLAN},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_GENEVE, 9521, snapshot.Geneve},
	}
	response := &bfdv1.ListCapabilitiesResponse{
		Capabilities: make([]*bfdv1.CapabilityStatus, 0, len(profiles)),
	}
	for _, profile := range profiles {
		status := &bfdv1.CapabilityStatus{
			Kind:     profile.kind,
			Rfc:      profile.rfc,
			Maturity: bfdv1.CapabilityMaturity_CAPABILITY_MATURITY_PREVIEW,
		}
		if profile.runtime.Known {
			status.Enablement = bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_DISABLED
			status.Readiness = bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY
			if profile.runtime.Enabled {
				status.Enablement = bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_ENABLED
				if profile.runtime.Ready {
					status.Readiness = bfdv1.CapabilityReadiness_CAPABILITY_READINESS_READY
				}
			}
		}
		response.Capabilities = append(response.Capabilities, status)
	}
	return response, nil
}
