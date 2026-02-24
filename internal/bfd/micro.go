// RFC 7130 — Bidirectional Forwarding Detection (BFD) on
// Link Aggregation Group (LAG) Interfaces.
//
// Micro-BFD runs independent Asynchronous mode BFD sessions on every
// LAG member link. This verifies per-member-link continuity with faster
// detection than LACP, and can cover L3 bidirectional forwarding.
//
// Key requirements (RFC 7130):
//   - One BFD session per LAG member link (per address family)
//   - UDP destination port 6784 (distinct from single-hop 3784)
//   - Standard RFC 5880/5881 procedures (Asynchronous mode only)
//   - Aggregate state: member removed from LAG when micro-BFD goes Down
//   - Dedicated multicast MAC 01-00-5E-90-00-01 for initial packets

package bfd

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// -------------------------------------------------------------------------
// Micro-BFD Errors
// -------------------------------------------------------------------------

// Sentinel errors for Micro-BFD operations.
var (
	// ErrMicroBFDNoMembers indicates a Micro-BFD group was configured
	// with no member links.
	ErrMicroBFDNoMembers = errors.New("micro-BFD group requires at least one member link")

	// ErrMicroBFDDuplicateMember indicates a duplicate member link name.
	ErrMicroBFDDuplicateMember = errors.New("duplicate member link in micro-BFD group")

	// ErrMicroBFDInvalidMinActive indicates MinActiveLinks is invalid.
	ErrMicroBFDInvalidMinActive = errors.New("min_active_links must be >= 1 and <= number of members")

	// ErrMicroBFDMemberNotFound indicates the specified member link was
	// not found in the group.
	ErrMicroBFDMemberNotFound = errors.New("member link not found in micro-BFD group")
)

// -------------------------------------------------------------------------
// Micro-BFD Configuration — RFC 7130 Section 2
// -------------------------------------------------------------------------

// MicroBFDConfig configures a Micro-BFD group for a LAG interface.
type MicroBFDConfig struct {
	// LAGInterface is the logical LAG interface name (e.g., "bond0").
	LAGInterface string

	// MemberLinks lists the physical member link names (e.g., ["eth0", "eth1"]).
	// RFC 7130 Section 2: one micro-BFD session per member link.
	MemberLinks []string

	// PeerAddr is the remote system's IP address for all member sessions.
	PeerAddr netip.Addr

	// LocalAddr is the local system's IP address.
	LocalAddr netip.Addr

	// DesiredMinTxInterval is the BFD timer for member sessions.
	// RFC 7130 Section 2.2: timer values MAY differ per member but
	// are expected to be the same within a group.
	DesiredMinTxInterval time.Duration

	// RequiredMinRxInterval is the minimum acceptable RX interval.
	RequiredMinRxInterval time.Duration

	// DetectMultiplier is the detection time multiplier.
	DetectMultiplier uint8

	// MinActiveLinks is the minimum number of member links that must be
	// Up for the LAG to be considered operational. When the number of
	// Up members drops below this threshold, the group emits a Down
	// notification. Must be >= 1 and <= len(MemberLinks).
	MinActiveLinks int
}

// -------------------------------------------------------------------------
// Member State — per-member-link BFD session state
// -------------------------------------------------------------------------

// MemberLinkState represents the BFD state of a single LAG member link.
type MemberLinkState struct {
	// Interface is the member link name (e.g., "eth0").
	Interface string

	// State is the current BFD session state for this member.
	State State

	// LocalDiscr is the local discriminator for this member's BFD session.
	LocalDiscr uint32
}

// -------------------------------------------------------------------------
// MicroBFDGroup — RFC 7130 per-LAG aggregate
// -------------------------------------------------------------------------

// MicroBFDGroup manages per-member-link BFD sessions for a LAG interface.
//
// The group tracks the aggregate state of all member links. When the
// number of Up members drops below MinActiveLinks, the group is
// considered Down. State change notifications are emitted for the
// aggregate LAG state.
//
// RFC 7130 Section 3: "even when LACP considers the member link ready
// to forward traffic, the member link MUST NOT be used by the load
// balancer until all micro-BFD sessions of the member link are in Up state."
//
// RFC 7130 Section 5: "When a micro-BFD session goes down, this member
// link MUST be taken out of the LAG load-balancing table(s).".
type MicroBFDGroup struct {
	mu sync.RWMutex

	// lagInterface is the logical LAG interface name.
	lagInterface string

	// members maps member link names to their BFD state.
	members map[string]*memberEntry

	// minActive is the minimum Up members for the LAG to be operational.
	minActive int

	// aggregateUp tracks the current aggregate state.
	// true when upCount >= minActive.
	aggregateUp bool

	// upCount is the number of member links currently in Up state.
	upCount int

	// peerAddr is the remote system's address (for notifications).
	peerAddr netip.Addr

	// localAddr is the local system's address (for notifications).
	localAddr netip.Addr

	logger *slog.Logger
}

// memberEntry holds the state for a single LAG member link.
type memberEntry struct {
	ifName     string
	state      State
	localDiscr uint32
}

// NewMicroBFDGroup creates a new Micro-BFD group for the given configuration.
// The group starts with all members in Down state.
func NewMicroBFDGroup(cfg MicroBFDConfig, logger *slog.Logger) (*MicroBFDGroup, error) {
	if err := validateMicroBFDConfig(cfg); err != nil {
		return nil, err
	}

	members := make(map[string]*memberEntry, len(cfg.MemberLinks))
	for _, link := range cfg.MemberLinks {
		members[link] = &memberEntry{
			ifName: link,
			state:  StateDown,
		}
	}

	return &MicroBFDGroup{
		lagInterface: cfg.LAGInterface,
		members:      members,
		minActive:    cfg.MinActiveLinks,
		aggregateUp:  false,
		upCount:      0,
		peerAddr:     cfg.PeerAddr,
		localAddr:    cfg.LocalAddr,
		logger: logger.With(
			slog.String("component", "micro-bfd"),
			slog.String("lag", cfg.LAGInterface),
		),
	}, nil
}

// validateMicroBFDConfig checks the Micro-BFD configuration.
func validateMicroBFDConfig(cfg MicroBFDConfig) error {
	if len(cfg.MemberLinks) == 0 {
		return ErrMicroBFDNoMembers
	}

	seen := make(map[string]struct{}, len(cfg.MemberLinks))
	for _, link := range cfg.MemberLinks {
		if _, dup := seen[link]; dup {
			return fmt.Errorf("member link %q: %w", link, ErrMicroBFDDuplicateMember)
		}
		seen[link] = struct{}{}
	}

	if cfg.MinActiveLinks < 1 || cfg.MinActiveLinks > len(cfg.MemberLinks) {
		return fmt.Errorf(
			"min_active_links=%d, members=%d: %w",
			cfg.MinActiveLinks, len(cfg.MemberLinks), ErrMicroBFDInvalidMinActive,
		)
	}

	return nil
}

// -------------------------------------------------------------------------
// Dynamic Member Management — future Sprint 7 (netlink LAG events)
// -------------------------------------------------------------------------

// ErrMicroBFDMemberExists indicates the specified member link already
// exists in the group.
var ErrMicroBFDMemberExists = errors.New("member link already exists in micro-BFD group")

// AddMember adds a new member link to the group. The member starts in
// Down state. This enables dynamic LAG membership changes (e.g., via
// netlink events when a physical port is added to a LAG).
//
// Returns ErrMicroBFDMemberExists if the member already exists.
func (g *MicroBFDGroup) AddMember(ifName string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.members[ifName]; exists {
		return fmt.Errorf("micro-BFD group %q: member %q: %w",
			g.lagInterface, ifName, ErrMicroBFDMemberExists)
	}

	g.members[ifName] = &memberEntry{
		ifName: ifName,
		state:  StateDown,
	}

	g.logger.Info("member link added",
		slog.String("member", ifName),
		slog.Int("member_count", len(g.members)),
	)

	return nil
}

// RemoveMember removes a member link from the group. If the member was
// in Up state, the upCount is decremented and the aggregate state may
// change. Returns true if the aggregate state changed as a result.
//
// Returns ErrMicroBFDMemberNotFound if the member does not exist.
func (g *MicroBFDGroup) RemoveMember(ifName string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	entry, ok := g.members[ifName]
	if !ok {
		return false, fmt.Errorf("micro-BFD group %q: member %q: %w",
			g.lagInterface, ifName, ErrMicroBFDMemberNotFound)
	}

	// Adjust upCount if the member was Up.
	if entry.state == StateUp {
		g.upCount--
	}

	delete(g.members, ifName)

	g.logger.Info("member link removed",
		slog.String("member", ifName),
		slog.Int("up_count", g.upCount),
		slog.Int("member_count", len(g.members)),
	)

	// Re-evaluate aggregate state.
	oldAggUp := g.aggregateUp
	g.aggregateUp = g.upCount >= g.minActive

	if oldAggUp != g.aggregateUp {
		if g.aggregateUp {
			g.logger.Info("LAG aggregate state: Up (after member removal)",
				slog.Int("up_count", g.upCount),
			)
		} else {
			g.logger.Warn("LAG aggregate state: Down (after member removal)",
				slog.Int("up_count", g.upCount),
				slog.Int("min_active", g.minActive),
			)
		}
		return true, nil
	}

	return false, nil
}

// -------------------------------------------------------------------------
// State Management
// -------------------------------------------------------------------------

// UpdateMemberState updates the BFD state for a specific member link.
// Returns true if the aggregate LAG state changed as a result.
//
// This method is called by the Manager when a per-member BFD session
// transitions state (via StateChange notifications).
func (g *MicroBFDGroup) UpdateMemberState(ifName string, newState State, localDiscr uint32) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	entry, ok := g.members[ifName]
	if !ok {
		return false, fmt.Errorf("micro-BFD group %q: member %q: %w",
			g.lagInterface, ifName, ErrMicroBFDMemberNotFound)
	}

	oldState := entry.state
	if oldState == newState {
		return false, nil
	}

	entry.state = newState
	entry.localDiscr = localDiscr

	// Update the Up count.
	if oldState == StateUp && newState != StateUp {
		g.upCount--
	} else if oldState != StateUp && newState == StateUp {
		g.upCount++
	}

	g.logger.Info("member link state changed",
		slog.String("member", ifName),
		slog.String("old_state", oldState.String()),
		slog.String("new_state", newState.String()),
		slog.Int("up_count", g.upCount),
		slog.Int("min_active", g.minActive),
	)

	// Check aggregate state change.
	oldAggUp := g.aggregateUp
	g.aggregateUp = g.upCount >= g.minActive

	if oldAggUp != g.aggregateUp {
		if g.aggregateUp {
			g.logger.Info("LAG aggregate state: Up",
				slog.Int("up_count", g.upCount),
			)
		} else {
			g.logger.Warn("LAG aggregate state: Down",
				slog.Int("up_count", g.upCount),
				slog.Int("min_active", g.minActive),
			)
		}
		return true, nil
	}

	return false, nil
}

// -------------------------------------------------------------------------
// Public Accessors
// -------------------------------------------------------------------------

// LAGInterface returns the logical LAG interface name.
func (g *MicroBFDGroup) LAGInterface() string { return g.lagInterface }

// PeerAddr returns the remote system's IP address for the group.
func (g *MicroBFDGroup) PeerAddr() netip.Addr { return g.peerAddr }

// LocalAddr returns the local system's IP address for the group.
func (g *MicroBFDGroup) LocalAddr() netip.Addr { return g.localAddr }

// IsUp returns whether the LAG aggregate is considered Up.
// True when the number of Up member links >= MinActiveLinks.
func (g *MicroBFDGroup) IsUp() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.aggregateUp
}

// UpCount returns the number of member links currently in Up state.
func (g *MicroBFDGroup) UpCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.upCount
}

// MemberCount returns the total number of member links in the group.
func (g *MicroBFDGroup) MemberCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.members)
}

// MinActiveLinks returns the minimum active links threshold.
func (g *MicroBFDGroup) MinActiveLinks() int { return g.minActive }

// MemberNames returns the names of all member links in the group.
func (g *MicroBFDGroup) MemberNames() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, 0, len(g.members))
	for name := range g.members {
		names = append(names, name)
	}
	return names
}

// MemberStates returns a snapshot of all member link states.
func (g *MicroBFDGroup) MemberStates() []MemberLinkState {
	g.mu.RLock()
	defer g.mu.RUnlock()

	states := make([]MemberLinkState, 0, len(g.members))
	for _, entry := range g.members {
		states = append(states, MemberLinkState{
			Interface:  entry.ifName,
			State:      entry.state,
			LocalDiscr: entry.localDiscr,
		})
	}
	return states
}

// -------------------------------------------------------------------------
// Snapshot
// -------------------------------------------------------------------------

// MicroBFDGroupSnapshot is a read-only view of a Micro-BFD group.
type MicroBFDGroupSnapshot struct {
	// LAGInterface is the logical LAG interface name.
	LAGInterface string

	// PeerAddr is the remote system's IP address.
	PeerAddr netip.Addr

	// LocalAddr is the local system's IP address.
	LocalAddr netip.Addr

	// AggregateUp indicates the LAG aggregate state.
	AggregateUp bool

	// UpCount is the number of Up member links.
	UpCount int

	// MemberCount is the total number of member links.
	MemberCount int

	// MinActiveLinks is the threshold for aggregate Up.
	MinActiveLinks int

	// Members holds per-member link state snapshots.
	Members []MemberLinkState
}

// Snapshot returns a read-only view of the Micro-BFD group.
func (g *MicroBFDGroup) Snapshot() MicroBFDGroupSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	members := make([]MemberLinkState, 0, len(g.members))
	for _, entry := range g.members {
		members = append(members, MemberLinkState{
			Interface:  entry.ifName,
			State:      entry.state,
			LocalDiscr: entry.localDiscr,
		})
	}

	return MicroBFDGroupSnapshot{
		LAGInterface:   g.lagInterface,
		PeerAddr:       g.peerAddr,
		LocalAddr:      g.localAddr,
		AggregateUp:    g.aggregateUp,
		UpCount:        g.upCount,
		MemberCount:    len(g.members),
		MinActiveLinks: g.minActive,
		Members:        members,
	}
}
