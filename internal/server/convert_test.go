package server_test

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// -------------------------------------------------------------------------
// stateToProto
// -------------------------------------------------------------------------

func TestStateToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state bfd.State
		want  bfdv1.SessionState
	}{
		{"AdminDown", bfd.StateAdminDown, bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN},
		{"Down", bfd.StateDown, bfdv1.SessionState_SESSION_STATE_DOWN},
		{"Init", bfd.StateInit, bfdv1.SessionState_SESSION_STATE_INIT},
		{"Up", bfd.StateUp, bfdv1.SessionState_SESSION_STATE_UP},
		{"Unknown", bfd.State(99), bfdv1.SessionState_SESSION_STATE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := server.StateToProto(tt.state)
			if got != tt.want {
				t.Errorf("StateToProto(%d) = %s, want %s", tt.state, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// diagToProto
// -------------------------------------------------------------------------

func TestDiagToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diag bfd.Diag
		want bfdv1.DiagnosticCode
	}{
		{"None", bfd.DiagNone, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE},
		{"ControlTimeExpired", bfd.DiagControlTimeExpired, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_CONTROL_TIME_EXPIRED},
		{"EchoFailed", bfd.DiagEchoFailed, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ECHO_FAILED},
		{"NeighborDown", bfd.DiagNeighborDown, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NEIGHBOR_DOWN},
		{"ForwardingPlaneReset", bfd.DiagForwardingPlaneReset, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_FORWARDING_RESET},
		{"PathDown", bfd.DiagPathDown, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_PATH_DOWN},
		{"ConcatPathDown", bfd.DiagConcatPathDown, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_CONCAT_PATH_DOWN},
		{"AdminDown", bfd.DiagAdminDown, bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ADMIN_DOWN},
		{
			"ReverseConcatPathDown",
			bfd.DiagReverseConcatPathDown,
			bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_REVERSE_CONCAT_PATH_DOWN,
		},
		{"Unknown", bfd.Diag(99), bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := server.DiagToProto(tt.diag)
			if got != tt.want {
				t.Errorf("DiagToProto(%d) = %s, want %s", tt.diag, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// sessionTypeToProto
// -------------------------------------------------------------------------

func TestSessionTypeToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		st   bfd.SessionType
		want bfdv1.SessionType
	}{
		{"SingleHop", bfd.SessionTypeSingleHop, bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP},
		{"MultiHop", bfd.SessionTypeMultiHop, bfdv1.SessionType_SESSION_TYPE_MULTI_HOP},
		{"Unknown", bfd.SessionType(99), bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := server.SessionTypeToProto(tt.st)
			if got != tt.want {
				t.Errorf("SessionTypeToProto(%d) = %s, want %s", tt.st, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// sessionTypeFromProto
// -------------------------------------------------------------------------

func TestSessionTypeFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pt      bfdv1.SessionType
		want    bfd.SessionType
		wantErr bool
	}{
		{"SingleHop", bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, bfd.SessionTypeSingleHop, false},
		{"MultiHop", bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, bfd.SessionTypeMultiHop, false},
		{"Unspecified", bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := server.SessionTypeFromPB(tt.pt)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionTypeFromPB(%s) error = %v, wantErr = %v", tt.pt, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("SessionTypeFromPB(%s) = %d, want %d", tt.pt, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// durationFromProto
// -------------------------------------------------------------------------

func TestDurationFromProto(t *testing.T) {
	t.Parallel()

	// nil returns 1 second default (RFC 5880 Section 6.8.1).
	got := server.DurationFromPB(nil)
	if got != time.Second {
		t.Errorf("DurationFromPB(nil) = %v, want 1s", got)
	}
}

// -------------------------------------------------------------------------
// stateChangeToProto
// -------------------------------------------------------------------------

func TestStateChangeToProto(t *testing.T) {
	t.Parallel()

	sc := bfd.StateChange{
		LocalDiscr: 42,
		OldState:   bfd.StateDown,
		NewState:   bfd.StateUp,
		Diag:       bfd.DiagNone,
		Timestamp:  time.Now(),
	}

	resp := server.StateChangeToProto(sc)
	if resp.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE {
		t.Errorf("Type = %s, want STATE_CHANGE", resp.GetType())
	}
	if resp.GetSession().GetLocalDiscriminator() != 42 {
		t.Errorf("LocalDiscriminator = %d, want 42", resp.GetSession().GetLocalDiscriminator())
	}
	if resp.GetSession().GetLocalState() != bfdv1.SessionState_SESSION_STATE_UP {
		t.Errorf("LocalState = %s, want UP", resp.GetSession().GetLocalState())
	}
	if resp.GetPreviousState() != bfdv1.SessionState_SESSION_STATE_DOWN {
		t.Errorf("PreviousState = %s, want DOWN", resp.GetPreviousState())
	}
	if resp.GetTimestamp() == nil {
		t.Error("Timestamp should not be nil")
	}
}

// -------------------------------------------------------------------------
// snapshotToProto
// -------------------------------------------------------------------------

func TestSnapshotToProto(t *testing.T) {
	t.Parallel()

	now := time.Now()
	snap := bfd.SessionSnapshot{
		LocalDiscr:           42,
		RemoteDiscr:          43,
		Type:                 bfd.SessionTypeMultiHop,
		State:                bfd.StateInit,
		RemoteState:          bfd.StateDown,
		LocalDiag:            bfd.DiagPathDown,
		DesiredMinTx:         100 * time.Millisecond,
		RequiredMinRx:        200 * time.Millisecond,
		DetectMultiplier:     5,
		NegotiatedTxInterval: 200 * time.Millisecond,
		DetectionTime:        1 * time.Second,
		LastStateChange:      now,
		LastPacketReceived:   now,
		Counters: bfd.SessionCounters{
			PacketsSent:      100,
			PacketsReceived:  99,
			StateTransitions: 3,
		},
	}

	pb := server.SnapshotToProto(snap)

	if pb.GetLocalDiscriminator() != 42 {
		t.Errorf("LocalDiscriminator = %d, want 42", pb.GetLocalDiscriminator())
	}
	if pb.GetRemoteDiscriminator() != 43 {
		t.Errorf("RemoteDiscriminator = %d, want 43", pb.GetRemoteDiscriminator())
	}
	if pb.GetType() != bfdv1.SessionType_SESSION_TYPE_MULTI_HOP {
		t.Errorf("Type = %s, want MULTI_HOP", pb.GetType())
	}
	if pb.GetLocalState() != bfdv1.SessionState_SESSION_STATE_INIT {
		t.Errorf("LocalState = %s, want INIT", pb.GetLocalState())
	}
	if pb.GetRemoteState() != bfdv1.SessionState_SESSION_STATE_DOWN {
		t.Errorf("RemoteState = %s, want DOWN", pb.GetRemoteState())
	}
	if pb.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_PATH_DOWN {
		t.Errorf("LocalDiagnostic = %s, want PATH_DOWN", pb.GetLocalDiagnostic())
	}
	if pb.GetDetectMultiplier() != 5 {
		t.Errorf("DetectMultiplier = %d, want 5", pb.GetDetectMultiplier())
	}
	if pb.GetLastStateChange() == nil {
		t.Error("LastStateChange should not be nil")
	}
	if pb.GetLastPacketReceived() == nil {
		t.Error("LastPacketReceived should not be nil")
	}
	if pb.GetCounters().GetPacketsSent() != 100 {
		t.Errorf("PacketsSent = %d, want 100", pb.GetCounters().GetPacketsSent())
	}
}

func TestSnapshotToProtoZeroTimestamps(t *testing.T) {
	t.Parallel()

	snap := bfd.SessionSnapshot{
		State: bfd.StateDown,
	}

	pb := server.SnapshotToProto(snap)
	if pb.GetLastStateChange() != nil {
		t.Error("LastStateChange should be nil for zero time")
	}
	if pb.GetLastPacketReceived() != nil {
		t.Error("LastPacketReceived should be nil for zero time")
	}
}

// -------------------------------------------------------------------------
// mapManagerError
// -------------------------------------------------------------------------

func TestMapManagerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode connect.Code
	}{
		{"DuplicateSession", bfd.ErrDuplicateSession, connect.CodeAlreadyExists},
		{"SessionNotFound", bfd.ErrSessionNotFound, connect.CodeNotFound},
		{"InvalidPeerAddr", bfd.ErrInvalidPeerAddr, connect.CodeInvalidArgument},
		{"InvalidDetectMult", bfd.ErrInvalidDetectMult, connect.CodeInvalidArgument},
		{"InvalidTxInterval", bfd.ErrInvalidTxInterval, connect.CodeInvalidArgument},
		{"InvalidSessionType", bfd.ErrInvalidSessionType, connect.CodeInvalidArgument},
		{"InvalidSessionRole", bfd.ErrInvalidSessionRole, connect.CodeInvalidArgument},
		{"Unknown", errors.New("unknown error"), connect.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cerr := server.MapManagerError(tt.err, "test-op")
			if cerr.Code() != tt.wantCode {
				t.Errorf("MapManagerError(%v) code = %s, want %s", tt.err, cerr.Code(), tt.wantCode)
			}
		})
	}
}

// -------------------------------------------------------------------------
// wrapError
// -------------------------------------------------------------------------

func TestWrapError(t *testing.T) {
	t.Parallel()

	base := errors.New("base error")
	wrapped := server.WrapError("context", base)

	if !errors.Is(wrapped, base) {
		t.Error("wrapped error should contain base error")
	}
	if wrapped.Error() != "context: base error" {
		t.Errorf("wrapped.Error() = %q, want %q", wrapped.Error(), "context: base error")
	}
}
