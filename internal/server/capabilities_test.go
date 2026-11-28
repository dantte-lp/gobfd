package server

import (
	"testing"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

type capabilityTestSource struct {
	snapshot CapabilitySnapshot
}

func (s *capabilityTestSource) CapabilitySnapshot() CapabilitySnapshot {
	return s.snapshot
}

func TestListCapabilitiesNormalizesRuntime(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		nilSource  bool
		runtime    CapabilityRuntime
		enablement bfdv1.CapabilityEnablement
		readiness  bfdv1.CapabilityReadiness
	}{
		{
			name:      "nil source",
			nilSource: true,
		},
		{
			name: "missing runtime",
		},
		{
			name:    "unknown ignores enabled and ready",
			runtime: CapabilityRuntime{Enabled: true, Ready: true},
		},
		{
			name:       "disabled",
			runtime:    CapabilityRuntime{Known: true},
			enablement: bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_DISABLED,
			readiness:  bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY,
		},
		{
			name:       "disabled ignores readiness",
			runtime:    CapabilityRuntime{Known: true, Ready: true},
			enablement: bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_DISABLED,
			readiness:  bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY,
		},
		{
			name:       "enabled awaiting readiness",
			runtime:    CapabilityRuntime{Known: true, Enabled: true},
			enablement: bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_ENABLED,
			readiness:  bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY,
		},
		{
			name:       "ready remains preview",
			runtime:    CapabilityRuntime{Known: true, Enabled: true, Ready: true},
			enablement: bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_ENABLED,
			readiness:  bfdv1.CapabilityReadiness_CAPABILITY_READINESS_READY,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var source CapabilitySource
			if !tc.nilSource {
				source = &capabilityTestSource{snapshot: CapabilitySnapshot{
					Unsolicited: tc.runtime,
					Echo:        tc.runtime,
					MicroBFD:    tc.runtime,
					VXLAN:       tc.runtime,
					Geneve:      tc.runtime,
				}}
			}
			srv := &capabilityServer{source: source}
			response, err := srv.ListCapabilities(t.Context(), new(bfdv1.ListCapabilitiesRequest))
			if err != nil {
				t.Fatalf("ListCapabilities: %v", err)
			}
			assertCapabilityCatalog(t, response)
			for _, status := range response.GetCapabilities() {
				if got := status.GetEnablement(); got != tc.enablement {
					t.Errorf("%s enablement = %s, want %s", status.GetKind(), got, tc.enablement)
				}
				if got := status.GetReadiness(); got != tc.readiness {
					t.Errorf("%s readiness = %s, want %s", status.GetKind(), got, tc.readiness)
				}
			}
		})
	}
}

func TestListCapabilitiesMapsIndependentProfiles(t *testing.T) {
	t.Parallel()

	srv := &capabilityServer{source: &capabilityTestSource{snapshot: CapabilitySnapshot{
		Unsolicited: CapabilityRuntime{},
		Echo:        CapabilityRuntime{Known: true},
		MicroBFD:    CapabilityRuntime{Known: true, Enabled: true},
		VXLAN:       CapabilityRuntime{Known: true, Enabled: true, Ready: true},
		Geneve:      CapabilityRuntime{Enabled: true, Ready: true},
	}}}
	response, err := srv.ListCapabilities(t.Context(), new(bfdv1.ListCapabilitiesRequest))
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	assertCapabilityCatalog(t, response)
	wantEnablement := []bfdv1.CapabilityEnablement{
		bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_UNSPECIFIED,
		bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_DISABLED,
		bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_ENABLED,
		bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_ENABLED,
		bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_UNSPECIFIED,
	}
	wantReadiness := []bfdv1.CapabilityReadiness{
		bfdv1.CapabilityReadiness_CAPABILITY_READINESS_UNSPECIFIED,
		bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY,
		bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY,
		bfdv1.CapabilityReadiness_CAPABILITY_READINESS_READY,
		bfdv1.CapabilityReadiness_CAPABILITY_READINESS_UNSPECIFIED,
	}
	for i, status := range response.GetCapabilities() {
		if got := status.GetEnablement(); got != wantEnablement[i] {
			t.Errorf("%s enablement = %s, want %s", status.GetKind(), got, wantEnablement[i])
		}
		if got := status.GetReadiness(); got != wantReadiness[i] {
			t.Errorf("%s readiness = %s, want %s", status.GetKind(), got, wantReadiness[i])
		}
	}
}

func TestListCapabilitiesRepliesAreIndependent(t *testing.T) {
	t.Parallel()

	source := &capabilityTestSource{}
	srv := &capabilityServer{source: source}
	first, err := srv.ListCapabilities(t.Context(), new(bfdv1.ListCapabilitiesRequest))
	if err != nil {
		t.Fatalf("first ListCapabilities: %v", err)
	}
	assertCapabilityCatalog(t, first)
	first.Capabilities[0].Kind = bfdv1.CapabilityKind_CAPABILITY_KIND_GENEVE
	first.Capabilities[0].Rfc = 1
	first.Capabilities[0].Maturity = bfdv1.CapabilityMaturity_CAPABILITY_MATURITY_UNSPECIFIED
	first.Capabilities[1] = nil
	source.snapshot.Echo = CapabilityRuntime{Known: true, Enabled: true, Ready: true}

	second, err := srv.ListCapabilities(t.Context(), new(bfdv1.ListCapabilitiesRequest))
	if err != nil {
		t.Fatalf("second ListCapabilities: %v", err)
	}
	assertCapabilityCatalog(t, second)
	gotEnablement := second.GetCapabilities()[0].GetEnablement()
	if gotEnablement != bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_UNSPECIFIED {
		t.Errorf("unsolicited enablement = %s, want unspecified", gotEnablement)
	}
	if got := second.GetCapabilities()[1].GetReadiness(); got != bfdv1.CapabilityReadiness_CAPABILITY_READINESS_READY {
		t.Errorf("echo readiness = %s, want ready from current snapshot", got)
	}
	second.Capabilities[0].Rfc = 2
	if got := first.GetCapabilities()[0].GetRfc(); got != 1 {
		t.Errorf("earlier reply changed RFC to %d", got)
	}
}

func assertCapabilityCatalog(t *testing.T, response *bfdv1.ListCapabilitiesResponse) {
	t.Helper()

	want := []struct {
		kind bfdv1.CapabilityKind
		rfc  uint32
	}{
		{bfdv1.CapabilityKind_CAPABILITY_KIND_UNSOLICITED, 9468},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_ECHO, 9747},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_MICRO_BFD, 7130},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_VXLAN, 8971},
		{bfdv1.CapabilityKind_CAPABILITY_KIND_GENEVE, 9521},
	}
	if got := len(response.GetCapabilities()); got != len(want) {
		t.Fatalf("capability count = %d, want %d", got, len(want))
	}
	for i, status := range response.GetCapabilities() {
		if status == nil {
			t.Fatalf("capability %d is nil", i)
		}
		if got := status.GetKind(); got != want[i].kind {
			t.Errorf("capability %d kind = %s, want %s", i, got, want[i].kind)
		}
		if got := status.GetRfc(); got != want[i].rfc {
			t.Errorf("capability %d RFC = %d, want %d", i, got, want[i].rfc)
		}
		if got := status.GetMaturity(); got != bfdv1.CapabilityMaturity_CAPABILITY_MATURITY_PREVIEW {
			t.Errorf("capability %d maturity = %s, want preview", i, got)
		}
	}
}
