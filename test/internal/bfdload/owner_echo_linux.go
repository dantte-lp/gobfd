//go:build linux

package bfdload

import (
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type workloadSession interface {
	LocalDiscriminator() uint32
	Type() bfd.SessionType
	Ref() bfd.SessionRef
	State() bfd.State
	Observation() bfd.Observation
	PacketsSent() uint64
	PacketsReceived() uint64
	NegotiatedTxInterval() time.Duration
	DetectionTime() time.Duration
}

type echoOwnerSession struct{ *bfd.EchoSession }

func (echoOwnerSession) Type() bfd.SessionType { return bfd.SessionTypeEcho }

func (s echoOwnerSession) PacketsSent() uint64 { return s.EchosSent() }

func (s echoOwnerSession) PacketsReceived() uint64 { return s.EchosReceived() }

func (s echoOwnerSession) NegotiatedTxInterval() time.Duration { return s.TxInterval() }

func (o *Owner) sessionRegistered(entry ownerSession) bool {
	if entry.session.Type() != bfd.SessionTypeEcho {
		registered, found := o.manager.LookupByDiscriminator(entry.session.LocalDiscriminator())
		return found && registered == entry.session
	}
	statuses, _ := o.manager.WatchSessionsWithRevision()
	for _, status := range statuses {
		if status.Ref == entry.session.Ref() && status.Type == bfd.SessionTypeEcho {
			return true
		}
	}
	return false
}
