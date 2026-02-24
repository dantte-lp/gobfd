package gobgp_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

func TestFormatBFDDownCommunication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		diag    bfd.Diag
		wantMsg string
	}{
		{
			name:    "control time expired",
			diag:    bfd.DiagControlTimeExpired,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Control Detection Time Expired",
		},
		{
			name:    "neighbor down",
			diag:    bfd.DiagNeighborDown,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Neighbor Signaled Session Down",
		},
		{
			name:    "path down",
			diag:    bfd.DiagPathDown,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Path Down",
		},
		{
			name:    "admin down",
			diag:    bfd.DiagAdminDown,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Administratively Down",
		},
		{
			name:    "no diagnostic",
			diag:    bfd.DiagNone,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=None",
		},
		{
			name:    "echo failed",
			diag:    bfd.DiagEchoFailed,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Echo Function Failed",
		},
		{
			name:    "forwarding plane reset",
			diag:    bfd.DiagForwardingPlaneReset,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Forwarding Plane Reset",
		},
		{
			name:    "concatenated path down",
			diag:    bfd.DiagConcatPathDown,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Concatenated Path Down",
		},
		{
			name:    "reverse concatenated path down",
			diag:    bfd.DiagReverseConcatPathDown,
			wantMsg: "BFD Down (RFC 9384 Cease/10): diag=Reverse Concatenated Path Down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := gobgp.FormatBFDDownCommunication(tt.diag)
			if got != tt.wantMsg {
				t.Errorf("FormatBFDDownCommunication(%v)\n  got:  %q\n  want: %q", tt.diag, got, tt.wantMsg)
			}
		})
	}
}

func TestParseBFDDownCommunication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantDiag string
		wantOK   bool
	}{
		{
			name:     "valid RFC 9384 message",
			input:    "BFD Down (RFC 9384 Cease/10): diag=Control Detection Time Expired",
			wantDiag: "Control Detection Time Expired",
			wantOK:   true,
		},
		{
			name:     "valid with path down",
			input:    "BFD Down (RFC 9384 Cease/10): diag=Path Down",
			wantDiag: "Path Down",
			wantOK:   true,
		},
		{
			name:   "legacy format without RFC 9384",
			input:  "BFD session down: Control Detection Time Expired",
			wantOK: false,
		},
		{
			name:   "empty string",
			input:  "",
			wantOK: false,
		},
		{
			name:   "unrelated message",
			input:  "Administrative shutdown",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			diag, ok := gobgp.ParseBFDDownCommunication(tt.input)

			if ok != tt.wantOK {
				t.Errorf("ParseBFDDownCommunication(%q): ok = %v, want %v", tt.input, ok, tt.wantOK)
			}

			if ok && diag != tt.wantDiag {
				t.Errorf("ParseBFDDownCommunication(%q): diag = %q, want %q", tt.input, diag, tt.wantDiag)
			}
		})
	}
}

func TestFormatAndParseRoundTrip(t *testing.T) {
	t.Parallel()

	diags := []bfd.Diag{
		bfd.DiagNone,
		bfd.DiagControlTimeExpired,
		bfd.DiagEchoFailed,
		bfd.DiagNeighborDown,
		bfd.DiagForwardingPlaneReset,
		bfd.DiagPathDown,
		bfd.DiagConcatPathDown,
		bfd.DiagAdminDown,
		bfd.DiagReverseConcatPathDown,
	}

	for _, diag := range diags {
		msg := gobgp.FormatBFDDownCommunication(diag)
		parsed, ok := gobgp.ParseBFDDownCommunication(msg)

		if !ok {
			t.Errorf("round-trip failed for diag %v: parse returned false", diag)
			continue
		}

		if parsed != diag.String() {
			t.Errorf("round-trip mismatch for diag %v: got %q, want %q", diag, parsed, diag.String())
		}
	}
}

func TestCeaseSubcodeBFDDown(t *testing.T) {
	t.Parallel()

	// RFC 9384 Section 3: IANA assigned value 10.
	if gobgp.CeaseSubcodeBFDDown != 10 {
		t.Errorf("CeaseSubcodeBFDDown = %d, want 10", gobgp.CeaseSubcodeBFDDown)
	}
}
