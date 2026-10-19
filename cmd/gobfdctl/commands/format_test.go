package commands

import (
	"encoding/json"
	"strings"
	"testing"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestCausalSessionFormats(t *testing.T) {
	t.Parallel()
	s := &bfdv1.BfdSession{
		PaddedPduSize:      128,
		ObservationVersion: 500, RemoteKnown: true,
		RemoteState:                   bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN,
		RemoteDiagnostic:              bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ADMIN_DOWN,
		RemoteControlPlaneIndependent: true, Cause: bfdv1.ObservationCause_OBSERVATION_CAUSE_REMOTE_ADMIN_DOWN,
	}
	for _, format := range []string{formatJSON, formatYAML, formatTable} {
		out, err := formatSession(s, format)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"500", "true", "AdminDown", "REMOTE_ADMIN_DOWN", "128"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s lacks %q: %s", format, want, out)
			}
		}
	}
}

func TestEchoWatchFormatsFrozenLifecycleRows(t *testing.T) {
	t.Parallel()
	for _, kind := range []bfdv1.WatchSessionEventsResponse_EventType{
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED,
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE,
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED,
	} {
		row := &bfdv1.BfdSession{
			PeerAddress: "192.0.2.61", LocalDiscriminator: 42,
			Type: bfdv1.SessionType_SESSION_TYPE_ECHO, LocalState: bfdv1.SessionState_SESSION_STATE_DOWN,
			ObservationVersion: 17, Counters: &bfdv1.SessionCounters{PacketsReceived: 3},
		}
		event := &bfdv1.WatchSessionEventsResponse{Type: kind, Session: row, Revision: 19}
		for _, format := range []string{formatJSON, formatYAML, formatTable} {
			out, err := formatEvent(event, format)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"192.0.2.61", "42", "19", "Down"} {
				if !strings.Contains(out, want) {
					t.Fatalf("Echo lifecycle %s lacks %q: %s", format, want, out)
				}
			}
			if format != formatTable && (!strings.Contains(out, "echo") || !strings.Contains(out, "17")) {
				t.Fatalf("Echo structured row lost type/version: %s", out)
			}
		}
	}
}

func TestMarshalJSONReplacesInvalidUTF8(t *testing.T) {
	t.Parallel()

	output, err := marshalJSON(map[string]string{"peer": string([]byte{'b', 'a', 'd', 0xff})})
	if err != nil {
		t.Fatalf("marshalJSON: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode marshalJSON output: %v", err)
	}
	if decoded["peer"] != "bad\ufffd" {
		t.Fatalf("decoded peer = %q, want replacement character", decoded["peer"])
	}
}

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

// The monitor shows stream gaps, snapshot ends and event revisions.
func TestFormatEventShowsGapAndRevision(t *testing.T) {
	t.Parallel()

	gap := &bfdv1.WatchSessionEventsResponse{
		Type:     bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP,
		Revision: 7,
	}
	if got := formatEventTable(gap); !strings.Contains(got, "Gap") || !strings.Contains(got, "rev=7") {
		t.Fatalf("table = %q, want Gap and rev=7", got)
	}
	sync := &bfdv1.WatchSessionEventsResponse{
		Type:     bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE,
		Revision: 9,
	}
	out, err := formatEvent(sync, "json")
	if err != nil {
		t.Fatalf("formatEvent: %v", err)
	}
	if !strings.Contains(out, `"event_type": "SyncComplete"`) || !strings.Contains(out, `"revision": 9`) {
		t.Fatalf("json = %s, want SyncComplete and revision 9", out)
	}
}
