package strategy_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/gobgp/strategy"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       string
		want        strategy.Strategy
		recognized  bool
		implemented bool
	}{
		{
			name:        "supported",
			value:       "disable-peer",
			want:        strategy.DisablePeer,
			recognized:  true,
			implemented: true,
		},
		{
			name:       "recognized but unsupported",
			value:      "withdraw-routes",
			want:       strategy.WithdrawRoutes,
			recognized: true,
		},
		{name: "unknown", value: "bogus"},
		{name: "different case", value: "Disable-Peer"},
		{name: "surrounding whitespace", value: " disable-peer "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, recognized := strategy.Parse(tt.value)
			if got != tt.want || recognized != tt.recognized {
				t.Fatalf("Parse(%q) = (%q, %t), want (%q, %t)",
					tt.value, got, recognized, tt.want, tt.recognized)
			}
			if implemented := got.Implemented(); implemented != tt.implemented {
				t.Errorf("Strategy(%q).Implemented() = %t, want %t",
					got, implemented, tt.implemented)
			}
		})
	}
}
