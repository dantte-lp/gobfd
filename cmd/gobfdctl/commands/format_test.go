package commands

import (
	"encoding/json"
	"strings"
	"testing"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

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
