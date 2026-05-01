package commands

import (
	"testing"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestShortSessionType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sessType bfdv1.SessionType
		want     string
	}{
		{"single-hop", bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, "single-hop"},
		{"multi-hop", bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, "multi-hop"},
		{"echo", bfdv1.SessionType_SESSION_TYPE_ECHO, "echo"},
		{"micro-bfd", bfdv1.SessionType_SESSION_TYPE_MICRO_BFD, "micro-bfd"},
		{"vxlan", bfdv1.SessionType_SESSION_TYPE_VXLAN, "vxlan"},
		{"geneve", bfdv1.SessionType_SESSION_TYPE_GENEVE, "geneve"},
		{"unknown", bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := shortSessionType(tt.sessType)
			if got != tt.want {
				t.Errorf("shortSessionType(%s) = %q, want %q", tt.sessType, got, tt.want)
			}
		})
	}
}
