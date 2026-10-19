package bfd

// Cause identifies the owner-committed reason for an observation. Unknown is
// retained for legacy callers; it never implies remote administrative intent.
type Cause uint8

// Causes recorded by the session owner.
const (
	CauseUnknown Cause = iota
	CauseRemoteAdminDown
	CauseRemoteDown
	CauseDetectionTimeout
	CauseLocalAdmin
	CausePathDown
	CauseConfiguration
)

// SessionRef distinguishes discriminator reuse without exposing a wire epoch.
// Constructors set exactly one private owner pointer.
type SessionRef struct {
	session *Session
	echo    *EchoSession
}

// Ref returns this session's comparable identity.
func (s *Session) Ref() SessionRef { return SessionRef{session: s} }

// Observation is the coherent causal tuple. Counters, configuration and timer
// telemetry are sampled separately and are outside ObservationVersion.
type Observation struct {
	State              State
	LocalDiag          Diag
	RemoteKnown        bool
	RemoteState        State
	RemoteDiag         Diag
	LocalC             bool
	RemoteC            bool
	Cause              Cause
	ObservationVersion uint64
}

// Observation returns a value copy of one committed causal tuple.
func (s *Session) Observation() Observation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.observation
}

const localDiagShift = 8

func packStateDiag(state State, diag Diag) uint32 {
	return uint32(state) | uint32(diag)<<localDiagShift
}

func (s *Session) pairedStateDiag() (State, Diag) {
	pair := s.localPair.Load()
	return State(pair & 3), Diag((pair >> localDiagShift) & 31) // #nosec G115 -- masked wire-sized fields.
}

// nextObservationVersionLocked is called only under mu, including detach.
func (s *Session) nextObservationVersionLocked() uint64 {
	if s.observationClock != nil {
		return s.observationClock.Add(1)
	}
	return s.observation.ObservationVersion + 1
}

func (s *Session) commitObservation(state State, diag Diag, cause Cause) (Observation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.observation
	if s.remoteStaged {
		next.RemoteKnown = true
		next.RemoteState = s.stagedRemote.RemoteState
		next.RemoteDiag = s.stagedRemote.RemoteDiag
		next.RemoteC = s.stagedRemote.RemoteC
		s.remoteStaged = false
	}
	next.State, next.LocalDiag, next.Cause = state, diag, cause
	if next == s.observation {
		return next, false
	}
	next.ObservationVersion = s.nextObservationVersionLocked()
	s.observation = next
	s.localPair.Store(packStateDiag(state, diag))
	return next, true
}
