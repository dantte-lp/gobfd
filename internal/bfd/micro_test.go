package bfd_test

import (
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// Micro-BFD Configuration Validation Tests
// -------------------------------------------------------------------------

func TestMicroBFDGroupValid(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:          "bond0",
		MemberLinks:           []string{"eth0", "eth1", "eth2"},
		PeerAddr:              netip.MustParseAddr("10.0.0.1"),
		LocalAddr:             netip.MustParseAddr("10.0.0.2"),
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
		MinActiveLinks:        2,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if g.LAGInterface() != "bond0" {
		t.Errorf("LAGInterface() = %q, want %q", g.LAGInterface(), "bond0")
	}
	if g.MemberCount() != 3 {
		t.Errorf("MemberCount() = %d, want 3", g.MemberCount())
	}
	if g.MinActiveLinks() != 2 {
		t.Errorf("MinActiveLinks() = %d, want 2", g.MinActiveLinks())
	}
	if g.IsUp() {
		t.Error("expected aggregate Down initially")
	}
	if g.UpCount() != 0 {
		t.Errorf("UpCount() = %d, want 0", g.UpCount())
	}
}

func TestMicroBFDGroupNoMembers(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{},
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err == nil {
		t.Fatal("expected error for no member links")
	}
}

func TestMicroBFDGroupDuplicateMember(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1", "eth0"},
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err == nil {
		t.Fatal("expected error for duplicate member link")
	}
}

func TestMicroBFDGroupInvalidMinActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		members        []string
		minActiveLinks int
	}{
		{"zero", []string{"eth0"}, 0},
		{"negative_effectively", []string{"eth0"}, -1},
		{"exceeds_members", []string{"eth0", "eth1"}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := bfd.MicroBFDConfig{
				LAGInterface:   "bond0",
				MemberLinks:    tt.members,
				MinActiveLinks: tt.minActiveLinks,
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			_, err := bfd.NewMicroBFDGroup(cfg, logger)
			if err == nil {
				t.Fatal("expected error for invalid min_active_links")
			}
		})
	}
}

// -------------------------------------------------------------------------
// Aggregate State Transition Tests
// -------------------------------------------------------------------------

func TestMicroBFDGroupAggregateTransitions(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1", "eth2"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 2,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All Down — aggregate Down.
	if g.IsUp() {
		t.Error("expected aggregate Down with 0 Up members")
	}

	// eth0 → Up. UpCount=1, still below threshold of 2.
	changed, err := g.UpdateMemberState("eth0", bfd.StateUp, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no aggregate change with 1 Up member")
	}
	if g.IsUp() {
		t.Error("expected aggregate Down with 1 Up member")
	}

	// eth1 → Up. UpCount=2, meets threshold — aggregate Up.
	changed, err = g.UpdateMemberState("eth1", bfd.StateUp, 101)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected aggregate change when crossing threshold")
	}
	if !g.IsUp() {
		t.Error("expected aggregate Up with 2 Up members")
	}

	// eth2 → Up. UpCount=3, no aggregate change.
	changed, err = g.UpdateMemberState("eth2", bfd.StateUp, 102)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no aggregate change, already Up")
	}

	// eth2 → Down. UpCount=2, still at threshold — no change.
	changed, err = g.UpdateMemberState("eth2", bfd.StateDown, 102)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no aggregate change, still at threshold")
	}
	if !g.IsUp() {
		t.Error("expected aggregate Up with 2 Up members")
	}

	// eth1 → Down. UpCount=1, below threshold — aggregate Down.
	changed, err = g.UpdateMemberState("eth1", bfd.StateDown, 101)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected aggregate change when dropping below threshold")
	}
	if g.IsUp() {
		t.Error("expected aggregate Down with 1 Up member")
	}
}

func TestMicroBFDGroupSingleMemberThreshold(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Single member Up → aggregate Up.
	changed, err := g.UpdateMemberState("eth0", bfd.StateUp, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected aggregate change")
	}
	if !g.IsUp() {
		t.Error("expected aggregate Up")
	}

	// Single member Down → aggregate Down.
	changed, err = g.UpdateMemberState("eth0", bfd.StateDown, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected aggregate change")
	}
	if g.IsUp() {
		t.Error("expected aggregate Down")
	}
}

func TestMicroBFDGroupUnknownMember(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = g.UpdateMemberState("eth99", bfd.StateUp, 1)
	if err == nil {
		t.Fatal("expected error for unknown member link")
	}
}

func TestMicroBFDGroupNoChangeOnSameState(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Setting Down → Down should return no change.
	changed, err := g.UpdateMemberState("eth0", bfd.StateDown, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change when setting same state")
	}
}

func TestMicroBFDGroupMemberStates(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Update eth0 to Up.
	if _, err := g.UpdateMemberState("eth0", bfd.StateUp, 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	states := g.MemberStates()
	if len(states) != 2 {
		t.Fatalf("MemberStates() returned %d entries, want 2", len(states))
	}

	found := false
	for _, s := range states {
		if s.Interface == "eth0" {
			found = true
			if s.State != bfd.StateUp {
				t.Errorf("eth0 state = %v, want Up", s.State)
			}
			if s.LocalDiscr != 42 {
				t.Errorf("eth0 local_discr = %d, want 42", s.LocalDiscr)
			}
		}
	}
	if !found {
		t.Error("eth0 not found in MemberStates()")
	}
}

func TestMicroBFDGroupSnapshot(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1", "eth2"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		LocalAddr:      netip.MustParseAddr("10.0.0.2"),
		MinActiveLinks: 2,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap := g.Snapshot()
	if snap.LAGInterface != "bond0" {
		t.Errorf("snapshot LAGInterface = %q, want %q", snap.LAGInterface, "bond0")
	}
	if snap.MemberCount != 3 {
		t.Errorf("snapshot MemberCount = %d, want 3", snap.MemberCount)
	}
	if snap.MinActiveLinks != 2 {
		t.Errorf("snapshot MinActiveLinks = %d, want 2", snap.MinActiveLinks)
	}
	if snap.AggregateUp {
		t.Error("snapshot should show aggregate Down")
	}
	if snap.PeerAddr != cfg.PeerAddr {
		t.Errorf("snapshot PeerAddr = %v, want %v", snap.PeerAddr, cfg.PeerAddr)
	}
	if len(snap.Members) != 3 {
		t.Errorf("snapshot Members has %d entries, want 3", len(snap.Members))
	}
}

func TestMicroBFDGroupInitTransitionsThenRecover(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// eth0: Down → Init (not Up, so no aggregate change).
	changed, err := g.UpdateMemberState("eth0", bfd.StateInit, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("Init should not trigger aggregate Up")
	}

	// eth0: Init → Up.
	changed, err = g.UpdateMemberState("eth0", bfd.StateUp, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected aggregate change on first Up")
	}
	if !g.IsUp() {
		t.Error("expected aggregate Up")
	}
}

// -------------------------------------------------------------------------
// Dynamic Member Add/Remove Tests
// -------------------------------------------------------------------------

func TestMicroBFDGroupAddMember(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Add a new member.
	if err := g.AddMember("eth1"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	if g.MemberCount() != 2 {
		t.Errorf("MemberCount() = %d, want 2", g.MemberCount())
	}

	// The new member should start in Down state.
	states := g.MemberStates()
	for _, s := range states {
		if s.Interface == "eth1" {
			if s.State != bfd.StateDown {
				t.Errorf("new member state = %v, want Down", s.State)
			}
			return
		}
	}
	t.Error("eth1 not found in MemberStates()")
}

func TestMicroBFDGroupAddMemberDuplicate(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Adding an existing member should fail.
	err = g.AddMember("eth0")
	if err == nil {
		t.Fatal("expected error for duplicate member")
	}
	if !errors.Is(err, bfd.ErrMicroBFDMemberExists) {
		t.Errorf("error = %v, want ErrMicroBFDMemberExists", err)
	}
}

func TestMicroBFDGroupRemoveMemberDown(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Remove a Down member. No aggregate change expected.
	changed, err := g.RemoveMember("eth1")
	if err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if changed {
		t.Error("expected no aggregate change when removing Down member")
	}

	if g.MemberCount() != 1 {
		t.Errorf("MemberCount() = %d, want 1", g.MemberCount())
	}
}

func TestMicroBFDGroupRemoveMemberUp(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 2,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Bring both members Up — aggregate Up (upCount=2 >= minActive=2).
	if _, uErr := g.UpdateMemberState("eth0", bfd.StateUp, 1); uErr != nil {
		t.Fatalf("unexpected error: %v", uErr)
	}
	if _, uErr := g.UpdateMemberState("eth1", bfd.StateUp, 2); uErr != nil {
		t.Fatalf("unexpected error: %v", uErr)
	}
	if !g.IsUp() {
		t.Fatal("expected aggregate Up with 2 Up members")
	}

	// Remove an Up member — aggregate should go Down (upCount=1 < minActive=2).
	changed, err := g.RemoveMember("eth1")
	if err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if !changed {
		t.Error("expected aggregate change when removing Up member below threshold")
	}
	if g.IsUp() {
		t.Error("expected aggregate Down after removing Up member")
	}
	if g.UpCount() != 1 {
		t.Errorf("UpCount() = %d, want 1", g.UpCount())
	}
}

func TestMicroBFDGroupRemoveMemberNotFound(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = g.RemoveMember("eth99")
	if err == nil {
		t.Fatal("expected error for removing nonexistent member")
	}
	if !errors.Is(err, bfd.ErrMicroBFDMemberNotFound) {
		t.Errorf("error = %v, want ErrMicroBFDMemberNotFound", err)
	}
}

func TestMicroBFDGroupPeerAndLocalAddr(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		LocalAddr:      netip.MustParseAddr("10.0.0.2"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if g.PeerAddr() != cfg.PeerAddr {
		t.Errorf("PeerAddr() = %v, want %v", g.PeerAddr(), cfg.PeerAddr)
	}
	if g.LocalAddr() != cfg.LocalAddr {
		t.Errorf("LocalAddr() = %v, want %v", g.LocalAddr(), cfg.LocalAddr)
	}
}

func TestMicroBFDGroupMemberNames(t *testing.T) {
	t.Parallel()

	cfg := bfd.MicroBFDConfig{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1", "eth2"},
		PeerAddr:       netip.MustParseAddr("10.0.0.1"),
		MinActiveLinks: 1,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	g, err := bfd.NewMicroBFDGroup(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := g.MemberNames()
	if len(names) != 3 {
		t.Fatalf("MemberNames() returned %d entries, want 3", len(names))
	}

	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	for _, want := range []string{"eth0", "eth1", "eth2"} {
		if _, ok := nameSet[want]; !ok {
			t.Errorf("MemberNames() missing %q", want)
		}
	}
}
