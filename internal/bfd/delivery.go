package bfd

import (
	"errors"
	"sync/atomic"
)

// DeliverySource identifies one processing stage, each with its own denominator.
type DeliverySource uint8

// Delivery stages have independent denominators.
const (
	DeliverySourceUnspecified DeliverySource = iota
	DeliveryWireParse
	DeliveryBaseTTL
	DeliveryOverlayDecap
	DeliveryDemux
	DeliveryUnsolicitedAdmission
	DeliveryPacketAdmission
	DeliveryPacketOwner
	DeliveryPathDownAdmission
	DeliveryAdminDownAdmission
	DeliveryControlOwner
	DeliveryEchoRead
	DeliveryEchoParse
	DeliveryEchoTTL
	DeliveryEchoDemux
	DeliveryEchoAdmission
	DeliveryEchoOwner
	DeliveryControlNotification
	DeliveryUnsolicitedNotification
	DeliveryEchoNotification
	DeliveryNotificationDispatch
	DeliveryWatchRecipient
	DeliveryLegacyNotification
	deliverySourceCount
)

// DeliveryOutcome distinguishes queue admission from owner processing.
type DeliveryOutcome uint8

// Delivery outcomes retain distinct admission and owner results.
const (
	DeliveryOutcomeUnspecified DeliveryOutcome = iota
	DeliveryAccepted
	DeliveryParserRejected
	DeliveryTTLRejected
	DeliveryPolicyRejected
	DeliveryReadError
	DeliveryFound
	DeliveryUnknownSession
	DeliveryScopeMismatch
	DeliveryQueued
	DeliveryQueueFull
	DeliveryClosed
	DeliveryCapacityRejected
	DeliveryRateRejected
	DeliveryAuthUnexpected
	DeliveryAuthMissing
	DeliveryAuthFailed
	DeliverySenderUnavailable
	DeliveryCreateFailed
	DeliveryCommitted
	DeliveryZeroTX
	DeliveryAbandoned
	DeliveryCanceled
	DeliveryDeadline
	DeliveryIgnored
	DeliveryStale
	DeliveryGapped
	deliveryOutcomeCount
)

// Admission errors preserve errors.Is classification at demux boundaries.
var (
	// ErrDeliveryQueueFull indicates that the receive queue could not admit a packet.
	ErrDeliveryQueueFull = errors.New("session receive queue is full")
	// ErrDeliveryClosed indicates that terminal stop has closed receive admission.
	ErrDeliveryClosed = errors.New("session receive admission is closed")
)

// DeliveryReporter records fixed source/outcome pairs without peer labels.
type DeliveryReporter interface {
	RecordDelivery(source DeliverySource, outcome DeliveryOutcome)
}

// DeliveryCounter is sampled telemetry; no lifecycle revision covers its value.
type DeliveryCounter struct {
	Source  DeliverySource
	Outcome DeliveryOutcome
	Count   uint64
}

//nolint:gochecknoglobals // Immutable stage/outcome table shared by ledger and metric initialization.
var deliveryPairs = [...]DeliveryCounter{
	{Source: DeliveryWireParse, Outcome: DeliveryAccepted},
	{Source: DeliveryWireParse, Outcome: DeliveryParserRejected},
	{Source: DeliveryBaseTTL, Outcome: DeliveryAccepted},
	{Source: DeliveryBaseTTL, Outcome: DeliveryTTLRejected},
	{Source: DeliveryOverlayDecap, Outcome: DeliveryAccepted},
	{Source: DeliveryOverlayDecap, Outcome: DeliveryParserRejected},
	{Source: DeliveryOverlayDecap, Outcome: DeliveryTTLRejected},
	{Source: DeliveryOverlayDecap, Outcome: DeliveryPolicyRejected},
	{Source: DeliveryOverlayDecap, Outcome: DeliveryReadError},
	{Source: DeliveryDemux, Outcome: DeliveryFound},
	{Source: DeliveryDemux, Outcome: DeliveryUnknownSession},
	{Source: DeliveryDemux, Outcome: DeliveryScopeMismatch},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryAccepted},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryClosed},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryPolicyRejected},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryCapacityRejected},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryRateRejected},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryAuthUnexpected},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryAuthMissing},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryAuthFailed},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliverySenderUnavailable},
	{Source: DeliveryUnsolicitedAdmission, Outcome: DeliveryCreateFailed},
	{Source: DeliveryPacketAdmission, Outcome: DeliveryQueued},
	{Source: DeliveryPacketAdmission, Outcome: DeliveryQueueFull},
	{Source: DeliveryPacketAdmission, Outcome: DeliveryClosed},
	{Source: DeliveryPacketOwner, Outcome: DeliveryCommitted},
	{Source: DeliveryPacketOwner, Outcome: DeliveryZeroTX},
	{Source: DeliveryPacketOwner, Outcome: DeliveryAuthUnexpected},
	{Source: DeliveryPacketOwner, Outcome: DeliveryAuthMissing},
	{Source: DeliveryPacketOwner, Outcome: DeliveryAuthFailed},
	{Source: DeliveryPacketOwner, Outcome: DeliveryAbandoned},
	{Source: DeliveryPathDownAdmission, Outcome: DeliveryQueued},
	{Source: DeliveryPathDownAdmission, Outcome: DeliveryQueueFull},
	{Source: DeliveryPathDownAdmission, Outcome: DeliveryClosed},
	{Source: DeliveryAdminDownAdmission, Outcome: DeliveryQueued},
	{Source: DeliveryAdminDownAdmission, Outcome: DeliveryClosed},
	{Source: DeliveryAdminDownAdmission, Outcome: DeliveryCanceled},
	{Source: DeliveryAdminDownAdmission, Outcome: DeliveryDeadline},
	{Source: DeliveryControlOwner, Outcome: DeliveryCommitted},
	{Source: DeliveryControlOwner, Outcome: DeliveryIgnored},
	{Source: DeliveryControlOwner, Outcome: DeliveryAbandoned},
	{Source: DeliveryEchoRead, Outcome: DeliveryAccepted},
	{Source: DeliveryEchoRead, Outcome: DeliveryReadError},
	{Source: DeliveryEchoParse, Outcome: DeliveryAccepted},
	{Source: DeliveryEchoParse, Outcome: DeliveryParserRejected},
	{Source: DeliveryEchoTTL, Outcome: DeliveryAccepted},
	{Source: DeliveryEchoTTL, Outcome: DeliveryTTLRejected},
	{Source: DeliveryEchoDemux, Outcome: DeliveryFound},
	{Source: DeliveryEchoDemux, Outcome: DeliveryUnknownSession},
	{Source: DeliveryEchoAdmission, Outcome: DeliveryQueued},
	{Source: DeliveryEchoAdmission, Outcome: DeliveryQueueFull},
	{Source: DeliveryEchoAdmission, Outcome: DeliveryClosed},
	{Source: DeliveryEchoOwner, Outcome: DeliveryCommitted},
	{Source: DeliveryEchoOwner, Outcome: DeliveryAbandoned},
	{Source: DeliveryControlNotification, Outcome: DeliveryQueued},
	{Source: DeliveryControlNotification, Outcome: DeliveryQueueFull},
	{Source: DeliveryUnsolicitedNotification, Outcome: DeliveryQueued},
	{Source: DeliveryUnsolicitedNotification, Outcome: DeliveryQueueFull},
	{Source: DeliveryEchoNotification, Outcome: DeliveryQueued},
	{Source: DeliveryEchoNotification, Outcome: DeliveryQueueFull},
	{Source: DeliveryNotificationDispatch, Outcome: DeliveryAccepted},
	{Source: DeliveryNotificationDispatch, Outcome: DeliveryStale},
	{Source: DeliveryNotificationDispatch, Outcome: DeliveryAbandoned},
	{Source: DeliveryWatchRecipient, Outcome: DeliveryQueued},
	{Source: DeliveryWatchRecipient, Outcome: DeliveryGapped},
	{Source: DeliveryLegacyNotification, Outcome: DeliveryQueued},
	{Source: DeliveryLegacyNotification, Outcome: DeliveryQueueFull},
}

//nolint:gochecknoglobals // Immutable masks keep hot-path validation constant-time without per-session maps.
var deliveryMasks = func() [deliverySourceCount]uint32 {
	var masks [deliverySourceCount]uint32
	for _, pair := range deliveryPairs {
		masks[pair.Source] |= 1 << pair.Outcome
	}
	return masks
}()

func validDeliveryPair(source DeliverySource, outcome DeliveryOutcome) bool {
	return source < deliverySourceCount && outcome < deliveryOutcomeCount && deliveryMasks[source]&(1<<outcome) != 0
}

// DeliveryCounterPairs returns the finite supported pairs in stable order, including zero rows.
func DeliveryCounterPairs() []DeliveryCounter {
	return append([]DeliveryCounter(nil), deliveryPairs[:]...)
}

func (source DeliverySource) String() string {
	names := [...]string{
		"unspecified", "wire_parse", "base_ttl", "overlay_decap", "demux", "unsolicited_admission",
		"packet_admission", "packet_owner", "path_down_admission", "admin_down_admission", "control_owner",
		"echo_read", "echo_parse", "echo_ttl", "echo_demux", "echo_admission", "echo_owner",
		"control_notification", "unsolicited_notification", "echo_notification", "notification_dispatch",
		"watch_recipient", "legacy_notification",
	}
	index := int(source)
	if index >= len(names) {
		return names[0]
	}
	return names[index] // #nosec G602 -- uint8 enum is nonnegative; index < len(names) was checked above.
}

func (outcome DeliveryOutcome) String() string {
	names := [...]string{
		"unspecified", "accepted", "parser_rejected", "ttl_rejected", "policy_rejected", "read_error", "found",
		"unknown_session", "scope_mismatch", "queued", "queue_full", "closed", "capacity_rejected", "rate_rejected",
		"auth_unexpected", "auth_missing", "auth_failed", "sender_unavailable", "create_failed", "committed", "zero_tx",
		"abandoned", "canceled", "deadline", "ignored",
		"stale", "gapped",
	}
	index := int(outcome)
	if index >= len(names) {
		return names[0]
	}
	return names[index] // #nosec G602 -- uint8 enum is nonnegative; index < len(names) was checked above.
}

type deliveryLedger [deliverySourceCount][deliveryOutcomeCount]atomic.Uint64

func withManagerDelivery(m *Manager) SessionOption {
	return func(s *Session) {
		s.delivery = m
		s.cleanupWake = m.cleanupWake
	}
}

func withEchoDelivery(reporter DeliveryReporter) EchoSessionOption {
	return func(es *EchoSession) { es.delivery = reporter }
}

func (es *EchoSession) recordDelivery(source DeliverySource, outcome DeliveryOutcome) {
	if es.delivery != nil {
		es.delivery.RecordDelivery(source, outcome)
	} else {
		es.metrics.RecordDelivery(source, outcome)
	}
}

// RecordDelivery records a stage decision once; session projections never feed it back.
func (m *Manager) RecordDelivery(source DeliverySource, outcome DeliveryOutcome) {
	if !validDeliveryPair(source, outcome) {
		return
	}
	m.delivery[source][outcome].Add(1)
	m.metrics.RecordDelivery(source, outcome)
}

// DeliveryCounters samples each supported bucket once without an atomic cross-stage snapshot.
func (m *Manager) DeliveryCounters() []DeliveryCounter {
	rows := DeliveryCounterPairs()
	for i := range rows {
		rows[i].Count = m.delivery[rows[i].Source][rows[i].Outcome].Load()
	}
	return rows
}

func (s *Session) recordDelivery(source DeliverySource, outcome DeliveryOutcome) {
	if s.delivery != nil {
		s.delivery.RecordDelivery(source, outcome)
	} else {
		s.metrics.RecordDelivery(source, outcome)
	}
}

func (s *Session) recordPacketDrop(outcome DeliveryOutcome) {
	s.packetDrops[outcome].Add(1)
	s.metrics.IncPacketsDropped(s.peerAddr, s.localAddr)
	if outcome == DeliveryAuthUnexpected || outcome == DeliveryAuthMissing || outcome == DeliveryAuthFailed {
		s.metrics.IncAuthFailures(s.peerAddr, s.localAddr)
	}
}

func (s *Session) counters() SessionCounters {
	counts := SessionCounters{
		PacketsSent: s.PacketsSent(), PacketsReceived: s.PacketsReceived(), StateTransitions: s.StateTransitions(),
	}
	for _, outcome := range [...]DeliveryOutcome{
		DeliveryQueueFull, DeliveryClosed, DeliveryZeroTX,
		DeliveryAuthUnexpected, DeliveryAuthMissing, DeliveryAuthFailed, DeliveryAbandoned,
	} {
		count := s.packetDrops[outcome].Load()
		counts.PacketsDropped += count
		if outcome == DeliveryAuthUnexpected || outcome == DeliveryAuthMissing || outcome == DeliveryAuthFailed {
			counts.AuthFailures += count
		}
	}
	return counts
}
