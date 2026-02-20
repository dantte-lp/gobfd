// Package commands implements the gobfdctl CLI commands.
package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

const (
	formatJSON  = "json"
	formatTable = "table"
	valueNA     = "N/A"
	valueUnknow = "Unknown"
)

// errUnsupportedFormat is returned when the requested output format is not supported.
var errUnsupportedFormat = errors.New("unsupported output format")

// formatSessions renders a slice of BFD sessions in the requested format.
func formatSessions(sessions []*bfdv1.BfdSession, format string) (string, error) {
	switch format {
	case formatJSON:
		return formatSessionsJSON(sessions)
	case formatTable:
		return formatSessionsTable(sessions)
	default:
		return "", fmt.Errorf("%w: %q", errUnsupportedFormat, format)
	}
}

// formatSession renders a single BFD session in the requested format.
func formatSession(session *bfdv1.BfdSession, format string) (string, error) {
	switch format {
	case formatJSON:
		return formatSessionJSON(session)
	case formatTable:
		return formatSessionDetail(session)
	default:
		return "", fmt.Errorf("%w: %q", errUnsupportedFormat, format)
	}
}

// formatEvent renders a session event in the requested format.
func formatEvent(event *bfdv1.WatchSessionEventsResponse, format string) (string, error) {
	switch format {
	case formatJSON:
		return formatEventJSON(event)
	case formatTable:
		return formatEventTable(event), nil
	default:
		return "", fmt.Errorf("%w: %q", errUnsupportedFormat, format)
	}
}

// --- Table formatters ---

func formatSessionsTable(sessions []*bfdv1.BfdSession) (string, error) {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DISCRIMINATOR\tPEER\tLOCAL\tTYPE\tSTATE\tREMOTE-STATE\tDIAG")

	for _, s := range sessions {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.GetLocalDiscriminator(),
			s.GetPeerAddress(),
			s.GetLocalAddress(),
			shortSessionType(s.GetType()),
			shortState(s.GetLocalState()),
			shortState(s.GetRemoteState()),
			shortDiag(s.GetLocalDiagnostic()),
		)
	}

	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("flush tabwriter: %w", err)
	}

	return buf.String(), nil
}

func formatSessionDetail(s *bfdv1.BfdSession) (string, error) {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	fmt.Fprintf(w, "Peer Address:\t%s\n", s.GetPeerAddress())
	fmt.Fprintf(w, "Local Address:\t%s\n", s.GetLocalAddress())
	fmt.Fprintf(w, "Interface:\t%s\n", s.GetInterfaceName())
	fmt.Fprintf(w, "Type:\t%s\n", shortSessionType(s.GetType()))
	fmt.Fprintf(w, "Local State:\t%s\n", shortState(s.GetLocalState()))
	fmt.Fprintf(w, "Remote State:\t%s\n", shortState(s.GetRemoteState()))
	fmt.Fprintf(w, "Local Diagnostic:\t%s\n", shortDiag(s.GetLocalDiagnostic()))
	fmt.Fprintf(w, "Local Discriminator:\t%d\n", s.GetLocalDiscriminator())
	fmt.Fprintf(w, "Remote Discriminator:\t%d\n", s.GetRemoteDiscriminator())
	fmt.Fprintf(w, "Detect Multiplier:\t%d\n", s.GetDetectMultiplier())

	if d := s.GetDesiredMinTxInterval(); d != nil {
		fmt.Fprintf(w, "Desired Min TX:\t%s\n", d.AsDuration())
	}
	if d := s.GetRequiredMinRxInterval(); d != nil {
		fmt.Fprintf(w, "Required Min RX:\t%s\n", d.AsDuration())
	}
	if d := s.GetRemoteMinRxInterval(); d != nil {
		fmt.Fprintf(w, "Remote Min RX:\t%s\n", d.AsDuration())
	}
	if d := s.GetNegotiatedTxInterval(); d != nil {
		fmt.Fprintf(w, "Negotiated TX:\t%s\n", d.AsDuration())
	}
	if d := s.GetDetectionTime(); d != nil {
		fmt.Fprintf(w, "Detection Time:\t%s\n", d.AsDuration())
	}

	fmt.Fprintf(w, "Auth Type:\t%s\n", shortAuthType(s.GetAuthType()))

	if ts := s.GetLastStateChange(); ts != nil {
		fmt.Fprintf(w, "Last State Change:\t%s\n", ts.AsTime().Format(time.RFC3339))
	}
	if ts := s.GetLastPacketReceived(); ts != nil {
		fmt.Fprintf(w, "Last Packet Received:\t%s\n", ts.AsTime().Format(time.RFC3339))
	}

	if c := s.GetCounters(); c != nil {
		fmt.Fprintf(w, "Packets Sent:\t%d\n", c.GetPacketsSent())
		fmt.Fprintf(w, "Packets Received:\t%d\n", c.GetPacketsReceived())
		fmt.Fprintf(w, "Packets Dropped:\t%d\n", c.GetPacketsDropped())
		fmt.Fprintf(w, "State Transitions:\t%d\n", c.GetStateTransitions())
		fmt.Fprintf(w, "Auth Failures:\t%d\n", c.GetAuthFailures())
	}

	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("flush tabwriter: %w", err)
	}

	return buf.String(), nil
}

func formatEventTable(event *bfdv1.WatchSessionEventsResponse) string {
	ts := valueNA
	if t := event.GetTimestamp(); t != nil {
		ts = t.AsTime().Format(time.RFC3339)
	}

	sess := event.GetSession()
	peer := valueNA
	state := valueNA

	var discr uint32

	if sess != nil {
		peer = sess.GetPeerAddress()
		state = shortState(sess.GetLocalState())
		discr = sess.GetLocalDiscriminator()
	}

	return fmt.Sprintf("[%s] %s  peer=%s  state=%s  prev=%s  discr=%d",
		ts,
		shortEventType(event.GetType()),
		peer,
		state,
		shortState(event.GetPreviousState()),
		discr,
	)
}

// --- JSON formatters ---

func formatSessionsJSON(sessions []*bfdv1.BfdSession) (string, error) {
	data, err := json.MarshalIndent(sessionsToView(sessions), "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sessions to JSON: %w", err)
	}

	return string(data), nil
}

func formatSessionJSON(session *bfdv1.BfdSession) (string, error) {
	data, err := json.MarshalIndent(sessionToView(session), "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal session to JSON: %w", err)
	}

	return string(data), nil
}

func formatEventJSON(event *bfdv1.WatchSessionEventsResponse) (string, error) {
	data, err := json.MarshalIndent(eventToView(event), "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal event to JSON: %w", err)
	}

	return string(data), nil
}

// --- View types for clean JSON output ---

type sessionView struct {
	PeerAddress         string `json:"peer_address"`
	LocalAddress        string `json:"local_address"`
	InterfaceName       string `json:"interface_name,omitempty"`
	Type                string `json:"type"`
	LocalState          string `json:"local_state"`
	RemoteState         string `json:"remote_state"`
	LocalDiagnostic     string `json:"local_diagnostic"`
	LocalDiscriminator  uint32 `json:"local_discriminator"`
	RemoteDiscriminator uint32 `json:"remote_discriminator"`
	DetectMultiplier    uint32 `json:"detect_multiplier"`
	DesiredMinTx        string `json:"desired_min_tx_interval,omitempty"`
	RequiredMinRx       string `json:"required_min_rx_interval,omitempty"`
	RemoteMinRx         string `json:"remote_min_rx_interval,omitempty"`
	NegotiatedTx        string `json:"negotiated_tx_interval,omitempty"`
	DetectionTime       string `json:"detection_time,omitempty"`
	AuthType            string `json:"auth_type"`
	LastStateChange     string `json:"last_state_change,omitempty"`
	LastPacketReceived  string `json:"last_packet_received,omitempty"`
}

type eventView struct {
	Timestamp     string       `json:"timestamp"`
	EventType     string       `json:"event_type"`
	PreviousState string       `json:"previous_state"`
	Session       *sessionView `json:"session,omitempty"`
}

func sessionToView(s *bfdv1.BfdSession) *sessionView {
	v := &sessionView{
		PeerAddress:         s.GetPeerAddress(),
		LocalAddress:        s.GetLocalAddress(),
		InterfaceName:       s.GetInterfaceName(),
		Type:                shortSessionType(s.GetType()),
		LocalState:          shortState(s.GetLocalState()),
		RemoteState:         shortState(s.GetRemoteState()),
		LocalDiagnostic:     shortDiag(s.GetLocalDiagnostic()),
		LocalDiscriminator:  s.GetLocalDiscriminator(),
		RemoteDiscriminator: s.GetRemoteDiscriminator(),
		DetectMultiplier:    s.GetDetectMultiplier(),
		AuthType:            shortAuthType(s.GetAuthType()),
	}

	if d := s.GetDesiredMinTxInterval(); d != nil {
		v.DesiredMinTx = d.AsDuration().String()
	}
	if d := s.GetRequiredMinRxInterval(); d != nil {
		v.RequiredMinRx = d.AsDuration().String()
	}
	if d := s.GetRemoteMinRxInterval(); d != nil {
		v.RemoteMinRx = d.AsDuration().String()
	}
	if d := s.GetNegotiatedTxInterval(); d != nil {
		v.NegotiatedTx = d.AsDuration().String()
	}
	if d := s.GetDetectionTime(); d != nil {
		v.DetectionTime = d.AsDuration().String()
	}
	if ts := s.GetLastStateChange(); ts != nil {
		v.LastStateChange = ts.AsTime().Format(time.RFC3339)
	}
	if ts := s.GetLastPacketReceived(); ts != nil {
		v.LastPacketReceived = ts.AsTime().Format(time.RFC3339)
	}

	return v
}

func sessionsToView(sessions []*bfdv1.BfdSession) []*sessionView {
	views := make([]*sessionView, 0, len(sessions))
	for _, s := range sessions {
		views = append(views, sessionToView(s))
	}

	return views
}

func eventToView(event *bfdv1.WatchSessionEventsResponse) *eventView {
	v := &eventView{
		EventType:     shortEventType(event.GetType()),
		PreviousState: shortState(event.GetPreviousState()),
	}

	if ts := event.GetTimestamp(); ts != nil {
		v.Timestamp = ts.AsTime().Format(time.RFC3339)
	}
	if s := event.GetSession(); s != nil {
		v.Session = sessionToView(s)
	}

	return v
}

// --- Enum short-name helpers ---

func shortState(s bfdv1.SessionState) string {
	switch s {
	case bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN:
		return "AdminDown"
	case bfdv1.SessionState_SESSION_STATE_DOWN:
		return "Down"
	case bfdv1.SessionState_SESSION_STATE_INIT:
		return "Init"
	case bfdv1.SessionState_SESSION_STATE_UP:
		return "Up"
	default:
		return valueUnknow
	}
}

func shortSessionType(t bfdv1.SessionType) string {
	switch t {
	case bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP:
		return "single-hop"
	case bfdv1.SessionType_SESSION_TYPE_MULTI_HOP:
		return "multi-hop"
	default:
		return "unknown"
	}
}

func shortDiag(d bfdv1.DiagnosticCode) string {
	switch d {
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE:
		return "None"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_CONTROL_TIME_EXPIRED:
		return "ControlTimeExpired"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ECHO_FAILED:
		return "EchoFailed"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NEIGHBOR_DOWN:
		return "NeighborDown"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_FORWARDING_RESET:
		return "ForwardingReset"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_PATH_DOWN:
		return "PathDown"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_CONCAT_PATH_DOWN:
		return "ConcatPathDown"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ADMIN_DOWN:
		return "AdminDown"
	case bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_REVERSE_CONCAT_PATH_DOWN:
		return "ReverseConcatPathDown"
	default:
		return valueUnknow
	}
}

func shortAuthType(a bfdv1.AuthenticationType) string {
	switch a {
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE:
		return "None"
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_SIMPLE_PASSWORD:
		return "SimplePassword"
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_MD5:
		return "KeyedMD5"
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_MD5:
		return "MeticulousKeyedMD5"
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1:
		return "KeyedSHA1"
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_SHA1:
		return "MeticulousKeyedSHA1"
	default:
		return valueUnknow
	}
}

func shortEventType(t bfdv1.WatchSessionEventsResponse_EventType) string {
	switch t {
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE:
		return "StateChange"
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED:
		return "SessionAdded"
	case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED:
		return "SessionDeleted"
	default:
		return valueUnknow
	}
}
