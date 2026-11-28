package netio

import (
	"errors"
	"sync"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type socketDropObserver struct {
	mu          sync.Mutex
	reporter    bfd.SocketDropReporter
	last        uint32
	initialized bool
}

func newSocketDropObserver(delivery bfd.DeliveryReporter, fresh bool) socketDropObserver {
	reporter, _ := delivery.(bfd.SocketDropReporter)
	return socketDropObserver{reporter: reporter, initialized: fresh}
}

func (l *Listener) recordSocketDrops(meta PacketMeta, err error) {
	o := &l.socketDrops
	if o.reporter == nil || (err != nil && !errors.Is(err, ErrPacketTruncated) && !errors.Is(err, ErrAncillaryTruncated)) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	outcome, delta := o.sample(meta)
	o.reporter.ObserveSocketDrops(l.echo, outcome, delta)
}

func (o *socketDropObserver) sample(meta PacketMeta) (bfd.SocketDropOutcome, uint32) {
	if meta.SocketDropsInvalid {
		return bfd.SocketDropInvalid, 0
	}
	if !meta.SocketDropsKnown {
		return bfd.SocketDropUnavailable, 0
	}
	if !o.initialized {
		o.last, o.initialized = meta.SocketDrops, true
		return bfd.SocketDropInitial, 0
	}
	// Same socket epoch and a gap below half the uint32 space are required.
	delta := meta.SocketDrops - o.last
	if delta >= 1<<31 {
		return bfd.SocketDropAmbiguous, 0
	}
	o.last = meta.SocketDrops
	return bfd.SocketDropSample, delta
}
