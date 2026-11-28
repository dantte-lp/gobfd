package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

// RFC 7130 Section 2: a member link belongs to one LAG. A candidate that
// assigns one member to two LAGs is ambiguous and is rejected as a whole.
func TestCompileControlSessionCandidateRejectsMicroMemberReusedAcrossLAGs(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MicroBFD.Enabled = true
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{
		{
			LAGInterface: "bond0", MemberLinks: []string{"eth0", "eth1"},
			PeerAddr: "192.0.2.1", LocalAddr: "192.0.2.2", MinActiveLinks: 1,
		},
		{
			LAGInterface: "bond1", MemberLinks: []string{"eth1", "eth2"},
			PeerAddr: "192.0.2.5", LocalAddr: "192.0.2.6", MinActiveLinks: 1,
		},
	}
	if _, err := compileControlSessionCandidate(cfg); err == nil {
		t.Fatal("candidate with eth1 in bond0 and bond1 compiled")
	}
}

// The group carries the effective member session timers, including the
// bfd.default_* values that its member sessions use.
func TestCompileControlSessionCandidateAppliesMicroTimerDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MicroBFD.Enabled = true
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface: "bond0", MemberLinks: []string{"eth0"},
		PeerAddr: "192.0.2.1", LocalAddr: "192.0.2.2", MinActiveLinks: 1,
	}}
	candidate, err := compileControlSessionCandidate(cfg)
	if err != nil {
		t.Fatalf("compileControlSessionCandidate: %v", err)
	}
	got := candidate.microGroups[0].Config
	if got.DesiredMinTxInterval != cfg.BFD.DefaultDesiredMinTx ||
		got.RequiredMinRxInterval != cfg.BFD.DefaultRequiredMinRx ||
		uint32(got.DetectMultiplier) != cfg.BFD.DefaultDetectMultiplier {
		t.Fatalf("group timers = %v/%v/%d, want defaults %v/%v/%d",
			got.DesiredMinTxInterval, got.RequiredMinRxInterval, got.DetectMultiplier,
			cfg.BFD.DefaultDesiredMinTx, cfg.BFD.DefaultRequiredMinRx, cfg.BFD.DefaultDetectMultiplier)
	}
}

// H-24: a failed member session claim leaves neither the group nor any
// member session behind.
func TestReconcileMicroBFDMemberFailureRollsBackGroup(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	sf := newNthFailureDeclarativeSenderFactory(2)
	cfg := config.DefaultConfig()
	cfg.MicroBFD.Enabled = true
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface: "bond0", MemberLinks: []string{"eth0", "eth1"},
		PeerAddr: "192.0.2.243", LocalAddr: "127.0.0.1", MinActiveLinks: 1,
	}}

	reconcileMicroBFDGroups(context.Background(), cfg, mgr, sf, slog.New(slog.DiscardHandler))

	if got := len(mgr.MicroBFDGroups()); got != 0 {
		t.Errorf("groups after member failure = %d, want 0", got)
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Errorf("member sessions after member failure = %d, want 0", got)
	}
	if len(sf.open) != 0 {
		t.Errorf("senders left open = %v", sf.open)
	}
}
