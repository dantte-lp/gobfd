package bfdmetrics

import (
	"net/netip"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	sessionBitSingleHop uint8 = iota
	sessionBitMultiHop
	sessionBitMicroBFD
	sessionBitEcho
	sessionBitSingleHopTitle
	sessionBitMultiHopTitle
	sessionBitMicroBFDTitle
	sessionBitEchoTitle
	knownSessionTypeCount
)

const (
	stateIdxAdminDown uint8 = iota
	stateIdxDown
	stateIdxInit
	stateIdxUp
	knownBFDStateCount
)

const transitionBitCount = knownBFDStateCount * knownBFDStateCount

type packetMetricKey struct {
	peer, local netip.Addr
}

type stateTransitionPair struct {
	from, to string
}

type packetMetricEntry struct {
	key                                   packetMetricKey
	peerLabel, localLabel                 string
	sent, received, dropped, authFailures prometheus.Counter
	registrations                         int
	sessionTypeMask                       uint8
	extraSessionTypes                     []string
	transitionMask                        uint16
	extraTransitions                      []stateTransitionPair
	prev, next                            *packetMetricEntry
}

func sessionTypeBit(sessionType string) (uint8, bool) {
	switch sessionType {
	case "single_hop":
		return sessionBitSingleHop, true
	case "multi_hop":
		return sessionBitMultiHop, true
	case "micro_bfd":
		return sessionBitMicroBFD, true
	case "echo":
		return sessionBitEcho, true
	case "SingleHop":
		return sessionBitSingleHopTitle, true
	case "MultiHop":
		return sessionBitMultiHopTitle, true
	case "MicroBFD":
		return sessionBitMicroBFDTitle, true
	case "Echo":
		return sessionBitEchoTitle, true
	default:
		return 0, false
	}
}

func sessionTypeByBit(bit uint8) string {
	names := [knownSessionTypeCount]string{
		"single_hop", "multi_hop", "micro_bfd", "echo",
		"SingleHop", "MultiHop", "MicroBFD", "Echo",
	}
	return names[bit]
}

func bfdStateIndex(state string) (uint8, bool) {
	switch state {
	case "AdminDown":
		return stateIdxAdminDown, true
	case "Down":
		return stateIdxDown, true
	case "Init":
		return stateIdxInit, true
	case "Up":
		return stateIdxUp, true
	default:
		return 0, false
	}
}

func bfdStateByIndex(idx uint8) string {
	names := [knownBFDStateCount]string{"AdminDown", "Down", "Init", "Up"}
	return names[idx]
}

func (e *packetMetricEntry) recordSessionType(sessionType string) {
	if sessionType == "" {
		return
	}
	if bit, ok := sessionTypeBit(sessionType); ok {
		e.sessionTypeMask |= 1 << bit
		return
	}
	if !slices.Contains(e.extraSessionTypes, sessionType) {
		e.extraSessionTypes = append(e.extraSessionTypes, sessionType)
	}
}

func (e *packetMetricEntry) recordTransition(from, to string) {
	fromIdx, fromOK := bfdStateIndex(from)
	toIdx, toOK := bfdStateIndex(to)
	if fromOK && toOK {
		bit := fromIdx*knownBFDStateCount + toIdx
		e.transitionMask |= 1 << bit
		return
	}
	pair := stateTransitionPair{from: from, to: to}
	if !slices.Contains(e.extraTransitions, pair) {
		e.extraTransitions = append(e.extraTransitions, pair)
	}
}

func (c *Collector) newPacketMetricEntryLocked(key packetMetricKey) *packetMetricEntry {
	peerLabel, localLabel := key.peer.String(), key.local.String()
	return &packetMetricEntry{
		key:          key,
		peerLabel:    peerLabel,
		localLabel:   localLabel,
		sent:         c.PacketsSent.WithLabelValues(peerLabel, localLabel),
		received:     c.PacketsReceived.WithLabelValues(peerLabel, localLabel),
		dropped:      c.PacketsDropped.WithLabelValues(peerLabel, localLabel),
		authFailures: c.AuthFailures.WithLabelValues(peerLabel, localLabel),
	}
}

func (c *Collector) registerPacketMetricsLocked(
	peer, local netip.Addr,
	sessionType string,
) *packetMetricEntry {
	if c.activePackets == nil {
		c.activePackets = make(map[packetMetricKey]*packetMetricEntry)
	}
	key := packetMetricKey{peer: peer, local: local}
	entry := c.activePackets[key]
	if entry == nil {
		if inactive := c.inactivePeers[key]; inactive != nil {
			c.removeInactiveNodeLocked(inactive)
			entry = inactive
		} else {
			entry = c.newPacketMetricEntryLocked(key)
		}
		c.activePackets[key] = entry
	}
	entry.registrations++
	entry.recordSessionType(sessionType)
	return entry
}

func (c *Collector) unregisterPacketMetricsLocked(
	peer, local netip.Addr,
	sessionType string,
) *packetMetricEntry {
	key := packetMetricKey{peer: peer, local: local}
	entry := c.activePackets[key]
	if entry != nil {
		entry.recordSessionType(sessionType)
		entry.registrations--
		if entry.registrations > 0 {
			return entry
		}
		delete(c.activePackets, key)
		c.pushInactiveEntryLocked(entry)
		return entry
	}
	inactive := c.ensureInactiveEntryLocked(peer, local)
	inactive.recordSessionType(sessionType)
	return inactive
}

func (c *Collector) ensureInactiveEntryLocked(peer, local netip.Addr) *packetMetricEntry {
	key := packetMetricKey{peer: peer, local: local}
	if existing := c.inactivePeers[key]; existing != nil {
		c.removeInactiveNodeLocked(existing)
		c.pushInactiveEntryLocked(existing)
		return existing
	}
	entry := c.newPacketMetricEntryLocked(key)
	c.pushInactiveEntryLocked(entry)
	return entry
}

func (c *Collector) incUnregisteredPacketCounter(
	peer, local netip.Addr,
	inc func(*packetMetricEntry),
) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()
	key := packetMetricKey{peer: peer, local: local}
	if active := c.activePackets[key]; active != nil {
		inc(active)
		return
	}
	entry := c.ensureInactiveEntryLocked(peer, local)
	inc(entry)
	c.evictInactivePeersLocked()
}

func (c *Collector) recordStateTransition(peer, local netip.Addr, from, to string) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()
	key := packetMetricKey{peer: peer, local: local}
	if active := c.activePackets[key]; active != nil {
		active.recordTransition(from, to)
		c.StateTransitions.WithLabelValues(active.peerLabel, active.localLabel, from, to).Inc()
		return
	}
	entry := c.ensureInactiveEntryLocked(peer, local)
	entry.recordTransition(from, to)
	c.StateTransitions.WithLabelValues(entry.peerLabel, entry.localLabel, from, to).Inc()
	c.evictInactivePeersLocked()
}

func (c *Collector) pushInactiveEntryLocked(entry *packetMetricEntry) {
	if c.inactivePeers == nil {
		c.inactivePeers = make(map[packetMetricKey]*packetMetricEntry)
	}
	entry.prev = c.inactiveTail
	entry.next = nil
	if c.inactiveTail != nil {
		c.inactiveTail.next = entry
	} else {
		c.inactiveHead = entry
	}
	c.inactiveTail = entry
	c.inactivePeers[entry.key] = entry
}

func (c *Collector) removeInactiveNodeLocked(entry *packetMetricEntry) {
	if entry.prev != nil {
		entry.prev.next = entry.next
	} else {
		c.inactiveHead = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	} else {
		c.inactiveTail = entry.prev
	}
	entry.prev, entry.next = nil, nil
	delete(c.inactivePeers, entry.key)
}

func (c *Collector) evictInactivePeersLocked() {
	for len(c.inactivePeers) > c.maxInactivePeers && c.inactiveHead != nil {
		oldest := c.inactiveHead
		c.removeInactiveNodeLocked(oldest)
		c.deletePeerEntrySeriesLocked(oldest)
		c.EvictedPeerSeries.Inc()
	}
}

func (c *Collector) deletePeerEntrySeriesLocked(entry *packetMetricEntry) {
	c.PacketsSent.DeleteLabelValues(entry.peerLabel, entry.localLabel)
	c.PacketsReceived.DeleteLabelValues(entry.peerLabel, entry.localLabel)
	c.PacketsDropped.DeleteLabelValues(entry.peerLabel, entry.localLabel)
	c.AuthFailures.DeleteLabelValues(entry.peerLabel, entry.localLabel)

	for bit := range knownSessionTypeCount {
		if entry.sessionTypeMask&(1<<bit) != 0 {
			c.Sessions.DeleteLabelValues(entry.peerLabel, entry.localLabel, sessionTypeByBit(bit))
		}
	}
	for _, st := range entry.extraSessionTypes {
		c.Sessions.DeleteLabelValues(entry.peerLabel, entry.localLabel, st)
	}

	for bit := range transitionBitCount {
		if entry.transitionMask&(1<<bit) != 0 {
			from := bfdStateByIndex(bit / knownBFDStateCount)
			to := bfdStateByIndex(bit % knownBFDStateCount)
			c.StateTransitions.DeleteLabelValues(entry.peerLabel, entry.localLabel, from, to)
		}
	}
	for _, pair := range entry.extraTransitions {
		c.StateTransitions.DeleteLabelValues(entry.peerLabel, entry.localLabel, pair.from, pair.to)
	}
}

func (c *Collector) packetMetrics(peer, local netip.Addr) *packetMetricEntry {
	c.packetMu.RLock()
	entry := c.activePackets[packetMetricKey{peer: peer, local: local}]
	c.packetMu.RUnlock()
	return entry
}
