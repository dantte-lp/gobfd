package server

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestCausalProtoConversion(t *testing.T) {
	t.Parallel()
	for _, known := range []bool{false, true} {
		snap := bfd.SessionSnapshot{
			State: bfd.StateDown, LocalDiag: bfd.DiagNeighborDown,
			RemoteKnown: known, RemoteState: bfd.StateAdminDown, RemoteDiag: bfd.DiagAdminDown,
			RemoteC: true, Cause: bfd.CauseRemoteAdminDown, ObservationVersion: 500,
		}
		sc := bfd.StateChange{
			NewState: snap.State, Diag: snap.LocalDiag,
			RemoteKnown: known, RemoteState: snap.RemoteState, RemoteDiag: snap.RemoteDiag,
			RemoteC: snap.RemoteC, Cause: snap.Cause, ObservationVersion: snap.ObservationVersion,
		}
		for _, pb := range []*bfdv1.BfdSession{snapshotToProto(snap), stateChangeToProto(sc).GetSession()} {
			wantState, wantDiag := bfdv1.SessionState_SESSION_STATE_UNSPECIFIED, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_UNSPECIFIED
			if known {
				wantState, wantDiag = bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ADMIN_DOWN
			}
			if pb.GetObservationVersion() != 500 || pb.GetRemoteKnown() != known || pb.GetRemoteState() != wantState ||
				pb.GetRemoteDiagnostic() != wantDiag || !pb.GetRemoteControlPlaneIndependent() ||
				pb.GetLocalControlPlaneIndependent() ||
				pb.GetCause() != bfdv1.ObservationCause_OBSERVATION_CAUSE_REMOTE_ADMIN_DOWN {
				t.Fatalf("causal conversion known=%t: %v", known, pb)
			}
		}
	}
}
