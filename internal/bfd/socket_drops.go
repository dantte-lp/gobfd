package bfd

// SocketDropOutcome describes sample validity independently of packet acceptance.
type SocketDropOutcome uint8

// Initial samples establish a baseline; only subsequent samples carry deltas.
const (
	SocketDropInitial SocketDropOutcome = iota
	SocketDropSample
	SocketDropUnavailable
	SocketDropInvalid
	SocketDropAmbiguous
	SocketDropOutcomeCount
)

func (o SocketDropOutcome) String() string {
	names := [...]string{"initial", "sample", "unavailable", "invalid", "ambiguous"}
	if int(o) >= len(names) {
		return unknownStr
	}
	return names[o]
}

// SocketDropReporter uses fixed Control/Echo dimensions, never peer identities.
type SocketDropReporter interface {
	ObserveSocketDrops(echo bool, outcome SocketDropOutcome, delta uint32)
}

// ObserveSocketDrops forwards transport telemetry without session projections.
func (m *Manager) ObserveSocketDrops(echo bool, outcome SocketDropOutcome, delta uint32) {
	if reporter, ok := m.metrics.(SocketDropReporter); ok {
		reporter.ObserveSocketDrops(echo, outcome, delta)
	}
}
