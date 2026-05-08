package commands

import (
	"fmt"
	"strings"
	"text/tabwriter"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// formatEchoSessions renders a slice of echo sessions in the requested
// format. Mirrors formatSessions for the RFC 9747 EchoSession message.
func formatEchoSessions(sessions []*bfdv1.EchoSession, format string) (string, error) {
	return formatOutput(echoSessionsToView(sessions), format, func() (string, error) {
		return formatEchoSessionsTable(sessions)
	})
}

func formatEchoSessionsTable(sessions []*bfdv1.EchoSession) (string, error) {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DISCRIMINATOR\tPEER\tLOCAL\tINTERFACE\tSTATE\tTX-INTERVAL\tDETECT-MULT\tSENT\tRCVD")
	for _, s := range sessions {
		var tx string
		if d := s.GetTxInterval(); d != nil {
			tx = d.AsDuration().String()
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\n",
			s.GetLocalDiscriminator(),
			s.GetPeerAddress(),
			s.GetLocalAddress(),
			s.GetInterfaceName(),
			shortState(s.GetLocalState()),
			tx,
			s.GetDetectMultiplier(),
			s.GetPacketsSent(),
			s.GetPacketsReceived(),
		)
	}
	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("flush tabwriter: %w", err)
	}
	return buf.String(), nil
}

// echoSessionView is a JSON/YAML-friendly snapshot of EchoSession.
type echoSessionView struct {
	Discriminator   uint32 `json:"discriminator"         yaml:"discriminator"`
	PeerAddress     string `json:"peer_address"          yaml:"peer_address"`
	LocalAddress    string `json:"local_address"         yaml:"local_address"`
	Interface       string `json:"interface,omitempty"   yaml:"interface,omitempty"`
	State           string `json:"state"                 yaml:"state"`
	Diagnostic      string `json:"diagnostic"            yaml:"diagnostic"`
	TxInterval      string `json:"tx_interval,omitempty" yaml:"tx_interval,omitempty"`
	DetectMult      uint32 `json:"detect_multiplier"     yaml:"detect_multiplier"`
	PacketsSent     uint64 `json:"packets_sent"          yaml:"packets_sent"`
	PacketsReceived uint64 `json:"packets_received"      yaml:"packets_received"`
}

func echoSessionsToView(sessions []*bfdv1.EchoSession) []echoSessionView {
	out := make([]echoSessionView, 0, len(sessions))
	for _, s := range sessions {
		var tx string
		if d := s.GetTxInterval(); d != nil {
			tx = d.AsDuration().String()
		}
		out = append(out, echoSessionView{
			Discriminator:   s.GetLocalDiscriminator(),
			PeerAddress:     s.GetPeerAddress(),
			LocalAddress:    s.GetLocalAddress(),
			Interface:       s.GetInterfaceName(),
			State:           shortState(s.GetLocalState()),
			Diagnostic:      shortDiag(s.GetLocalDiagnostic()),
			TxInterval:      tx,
			DetectMult:      s.GetDetectMultiplier(),
			PacketsSent:     s.GetPacketsSent(),
			PacketsReceived: s.GetPacketsReceived(),
		})
	}
	return out
}

// formatMicroBFDGroups renders a slice of micro-BFD groups in the
// requested format.
func formatMicroBFDGroups(groups []*bfdv1.MicroBFDGroup, format string) (string, error) {
	return formatOutput(microBFDGroupsToView(groups), format, func() (string, error) {
		return formatMicroBFDGroupsTable(groups)
	})
}

func formatMicroBFDGroupsTable(groups []*bfdv1.MicroBFDGroup) (string, error) {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LAG\tPEER\tLOCAL\tMEMBERS\tUP\tMIN-ACTIVE\tAGGREGATE")
	for _, g := range groups {
		agg := "Down"
		if g.GetAggregateUp() {
			agg = "Up"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			g.GetLagInterface(),
			g.GetPeerAddress(),
			g.GetLocalAddress(),
			strings.Join(g.GetMemberLinks(), ","),
			g.GetUpMemberCount(),
			g.GetMinActiveLinks(),
			agg,
		)
	}
	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("flush tabwriter: %w", err)
	}
	return buf.String(), nil
}

// microBFDGroupView is a JSON/YAML-friendly snapshot of MicroBFDGroup.
type microBFDGroupView struct {
	LagInterface   string   `json:"lag_interface"     yaml:"lag_interface"`
	PeerAddress    string   `json:"peer_address"      yaml:"peer_address"`
	LocalAddress   string   `json:"local_address"     yaml:"local_address"`
	MemberLinks    []string `json:"member_links"      yaml:"member_links"`
	DetectMult     uint32   `json:"detect_multiplier" yaml:"detect_multiplier"`
	MinActiveLinks uint32   `json:"min_active_links"  yaml:"min_active_links"`
	UpMemberCount  uint32   `json:"up_member_count"   yaml:"up_member_count"`
	AggregateUp    bool     `json:"aggregate_up"      yaml:"aggregate_up"`
}

func microBFDGroupsToView(groups []*bfdv1.MicroBFDGroup) []microBFDGroupView {
	out := make([]microBFDGroupView, 0, len(groups))
	for _, g := range groups {
		out = append(out, microBFDGroupView{
			LagInterface:   g.GetLagInterface(),
			PeerAddress:    g.GetPeerAddress(),
			LocalAddress:   g.GetLocalAddress(),
			MemberLinks:    g.GetMemberLinks(),
			DetectMult:     g.GetDetectMultiplier(),
			MinActiveLinks: g.GetMinActiveLinks(),
			UpMemberCount:  g.GetUpMemberCount(),
			AggregateUp:    g.GetAggregateUp(),
		})
	}
	return out
}
