//go:build linux

package bfdload

import (
	"math"
	"net/netip"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// OwnerDownIncarnation identifies one physical session within the fixed workload manifest.
type OwnerDownIncarnation struct {
	Known      bool          `json:"known"`
	Slot       int           `json:"slot"`
	Generation uint64        `json:"generation"`
	Endpoint   OwnerEndpoint `json:"endpoint"`
	Error      string        `json:"error,omitempty"`
}

type downEndpoint struct{ local, peer netip.Addr }

type downIncarnationSlot struct {
	endpoint      OwnerEndpoint
	generation    uint64
	ref           bfd.SessionRef
	discriminator uint32
}

type downIncarnations struct {
	slots      []downIncarnationSlot
	byEndpoint map[downEndpoint]int
	err        string
}

// The caller seeds under inventory before the workload can start churning.
func newDownIncarnations(entries []ownerSession) *downIncarnations {
	d := &downIncarnations{
		slots: make([]downIncarnationSlot, len(entries)), byEndpoint: make(map[downEndpoint]int, len(entries)),
	}
	if len(entries) == 0 {
		d.invalidate("owner incarnation seed is unavailable")
		return d
	}
	seen := make(map[uint32]struct{}, len(entries))
	for index, entry := range entries {
		session := entry.session
		if session == nil || entry.generation == 0 || !entry.endpoint.Local.IsValid() || !entry.endpoint.Peer.IsValid() ||
			session.LocalAddr() != entry.endpoint.Local.Addr() || session.PeerAddr() != entry.endpoint.Peer.Addr() ||
			session.Interface() != entry.endpoint.Interface || session.Type() != bfd.SessionTypeSingleHop ||
			session.LocalDiscriminator() == 0 {
			d.invalidate("invalid owner incarnation seed")
			return d
		}
		key := downEndpoint{session.LocalAddr(), session.PeerAddr()}
		_, duplicateEndpoint := d.byEndpoint[key]
		_, duplicateDiscriminator := seen[session.LocalDiscriminator()]
		if duplicateEndpoint || duplicateDiscriminator {
			d.invalidate("ambiguous owner incarnation seed")
			return d
		}
		d.byEndpoint[key] = index
		seen[session.LocalDiscriminator()] = struct{}{}
		d.slots[index] = downIncarnationSlot{entry.endpoint, entry.generation, session.Ref(), session.LocalDiscriminator()}
	}
	return d
}

func (d *downIncarnations) clear() {
	for index := range d.slots {
		d.slots[index].ref = bfd.SessionRef{}
	}
}

func (d *downIncarnations) invalidate(reason string) OwnerDownIncarnation {
	if d.err == "" {
		d.err = reason
	}
	d.clear()
	return OwnerDownIncarnation{Error: d.err}
}

func (d *downIncarnations) observe(change bfd.StateChange) OwnerDownIncarnation {
	if d.err != "" {
		return OwnerDownIncarnation{Error: d.err}
	}
	if change.Gap {
		return d.invalidate("owner incarnation notification gap")
	}
	index, found := d.byEndpoint[downEndpoint{change.LocalAddr, change.PeerAddr}]
	if !found || !coherentDownIncarnation(change) {
		return d.invalidate("inconsistent owner incarnation notification")
	}
	slot := &d.slots[index]
	if change.Interface != slot.endpoint.Interface {
		return d.invalidate("owner incarnation interface mismatch")
	}
	if change.Added && slot.ref == (bfd.SessionRef{}) {
		if slot.generation == math.MaxUint64 {
			return d.invalidate("owner incarnation generation overflow")
		}
		slot.generation++
		slot.ref, slot.discriminator = change.Ref, change.LocalDiscr
	}
	if change.Ref != slot.ref || change.LocalDiscr != slot.discriminator {
		return d.invalidate("owner incarnation physical source mismatch")
	}
	if change.Deleted {
		slot.ref = bfd.SessionRef{}
		return OwnerDownIncarnation{}
	}
	return OwnerDownIncarnation{Known: true, Slot: index, Generation: slot.generation, Endpoint: slot.endpoint}
}

func coherentDownIncarnation(change bfd.StateChange) bool {
	status := change.Status
	return change.Ref != (bfd.SessionRef{}) && change.Ref == status.Ref &&
		change.ObservationVersion != 0 && change.ObservationVersion == status.ObservationVersion &&
		change.LocalDiscr != 0 && change.LocalDiscr == status.LocalDiscr &&
		change.LocalAddr == status.LocalAddr && change.PeerAddr == status.PeerAddr &&
		change.Type == bfd.SessionTypeSingleHop && change.Type == status.Type && change.Interface == status.Interface &&
		(!change.Added || !change.Deleted)
}

func (d *downIncarnations) finish(result downJournalResult) downJournalResult {
	result.incarnationsComplete = result.closed && result.err == nil && d.err == ""
	result.incarnationError = d.err
	if !result.closed && result.incarnationError == "" {
		result.incarnationError = "owner incarnation subscription did not close normally"
	}
	d.clear()
	return result
}
