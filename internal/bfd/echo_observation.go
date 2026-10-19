package bfd

// Ref returns this Echo session's comparable identity.
func (es *EchoSession) Ref() SessionRef { return SessionRef{echo: es} }

func (es *EchoSession) observationSnapshot() Observation {
	es.observationMu.RLock()
	defer es.observationMu.RUnlock()
	return es.observation
}

// nextObservationVersionLocked is called only under observationMu, including detach.
func (es *EchoSession) nextObservationVersionLocked() uint64 {
	if es.observationClock != nil {
		return es.observationClock.Add(1)
	}
	return es.observation.ObservationVersion + 1
}

func (es *EchoSession) watchSnapshot() SessionSnapshot {
	es.observationMu.RLock()
	defer es.observationMu.RUnlock()
	return es.watchStatusForObservation(es.observation)
}

// watchStatusForObservation preserves the explicit causal tuple; telemetry is sampled here.
func (es *EchoSession) watchStatusForObservation(observation Observation) SessionSnapshot {
	return SessionSnapshot{
		LocalDiscr: es.localDiscr, PeerAddr: es.peerAddr, LocalAddr: es.localAddr,
		Interface: es.ifName, Type: SessionTypeEcho,
		State: observation.State, LocalDiag: observation.LocalDiag, Cause: observation.Cause,
		ObservationVersion: observation.ObservationVersion, Ref: es.Ref(),
		DesiredMinTx: es.txInterval, DetectMultiplier: es.detectMult, DetectionTime: es.DetectionTime(),
		LastStateChange: es.LastStateChange(), LastPacketReceived: es.LastEchoReceived(),
		Counters: SessionCounters{
			PacketsSent: es.EchosSent(), PacketsReceived: es.EchosReceived(), StateTransitions: es.StateTransitions(),
		},
	}
}
