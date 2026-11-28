package overlay_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/overlay"
)

func TestParseBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		value      string
		want       overlay.Backend
		recognized bool
	}{
		{name: "empty uses default", want: overlay.BackendUserspaceUDP, recognized: true},
		{name: "whitespace uses default", value: "  ", want: overlay.BackendUserspaceUDP, recognized: true},
		{name: "surrounding whitespace", value: " userspace-udp ", want: overlay.BackendUserspaceUDP, recognized: true},
		{name: "reserved", value: overlay.BackendOVS, want: overlay.BackendOVS, recognized: true},
		{name: "case remains significant", value: "OVS"},
		{name: "unknown", value: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, recognized := overlay.ParseBackend(tt.value)
			if got != tt.want || recognized != tt.recognized {
				t.Fatalf("ParseBackend(%q) = (%q, %t), want (%q, %t)",
					tt.value, got, recognized, tt.want, tt.recognized)
			}
		})
	}
}
