//go:build linux

package netio

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

type observedDropConn struct {
	PacketConn

	last     PacketMeta
	consumed uint64
}

func (c *observedDropConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	n, meta, err := c.PacketConn.ReadPacket(buf)
	if err == nil || errors.Is(err, ErrPacketTruncated) || errors.Is(err, ErrAncillaryTruncated) {
		c.last = meta
		c.consumed++
	}
	return n, meta, err
}

func newSocketDropUDPListener(
	t *testing.T, family loopbackFamily, fresh bool, reporter bfd.DeliveryReporter,
) (*Listener, *net.UDPConn) {
	t.Helper()
	if !fresh {
		conn := timestampTestConn(t, family, false, unix.SO_RXQ_OVFL)
		if err := conn.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
		return NewListenerFromConn(&LinuxPacketConn{conn: conn}, false, reporter), conn
	}
	reserve, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(family.addr, 0)))
	if err != nil {
		t.Fatal(err)
	}
	port := reserve.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	if closeErr := reserve.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	ln, err := NewListener(ListenerConfig{Addr: family.addr, Port: port, ReadBufferSize: 1024, Delivery: reporter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Error(err)
		}
	})
	return ln, ln.conn.(*LinuxPacketConn).conn
}

func socketDropMetric(t *testing.T, registry *prometheus.Registry, name, outcome string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			kind, event := "", ""
			for _, label := range metric.GetLabel() {
				if label.GetName() == "kind" {
					kind = label.GetValue()
				}
				if label.GetName() == "outcome" {
					event = label.GetValue()
				}
			}
			if kind == "control" && event == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("missing socket metric %s/%s", name, outcome)
	return 0
}

func TestSocketDropsRealUDP(t *testing.T) {
	t.Parallel()
	for _, family := range loopbackFamilies() {
		for _, fresh := range []bool{false, true} {
			t.Run(family.name+"/"+map[bool]string{false: "existing", true: "fresh"}[fresh], func(t *testing.T) {
				t.Parallel()
				registry := prometheus.NewRegistry()
				metrics := bfdmetrics.NewCollector(registry)
				logger := slog.New(slog.DiscardHandler)
				manager := bfd.NewManager(logger, bfd.WithManagerMetrics(metrics))
				t.Cleanup(manager.Close)
				ln, udp := newSocketDropUDPListener(t, family, fresh, manager)
				observer := &observedDropConn{PacketConn: ln.conn}
				ln.conn = observer
				receiver := NewReceiver(manager, logger)
				port := udp.LocalAddr().(*net.UDPAddr).AddrPort().Port()
				var initial, final uint32
				for burst := range 2 {
					for range 128 {
						sendLoopback(t, family, port, 255, make([]byte, 512))
					}
					if err := udp.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
						t.Fatal(err)
					}
					for {
						err := receiver.recvOne(t.Context(), ln)
						if err == nil {
							continue
						}
						var netErr net.Error
						if !errors.As(err, &netErr) || !netErr.Timeout() {
							t.Fatal(err)
						}
						break
					}
					if err := udp.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
						t.Fatal(err)
					}
					sendLoopback(t, family, port, 255, []byte{0})
					if err := receiver.recvOne(t.Context(), ln); err != nil {
						t.Fatal(err)
					}
					if !observer.last.SocketDropsKnown || observer.last.SocketDrops == 0 {
						t.Fatalf("missing post-load cumulative marker: %+v", observer.last)
					}
					if burst == 0 {
						initial = observer.last.SocketDrops
					} else {
						final = observer.last.SocketDrops
					}
				}
				sendLoopback(t, family, port, 255, []byte{0})
				if err := receiver.recvOne(t.Context(), ln); err != nil {
					t.Fatal(err)
				}
				if !observer.last.SocketDropsKnown || observer.last.SocketDrops != final || final <= initial {
					t.Fatalf("marker drift initial=%d final=%d last=%+v", initial, final, observer.last)
				}
				want := final - initial
				initialEvents := float64(1)
				if fresh {
					want = final
					initialEvents = 0
				}
				if got := socketDropMetric(t, registry, "gobfd_bfd_socket_drops_total", ""); got != float64(want) {
					t.Fatalf("observed drops=%g,want%d (initial=%d final=%d)", got, want, initial, final)
				}
				if got := socketDropMetric(t, registry, "gobfd_bfd_socket_drop_samples_total", "initial"); got != initialEvents {
					t.Fatalf("initial events=%g,want%g", got, initialEvents)
				}
				if observer.consumed+uint64(final) != 259 {
					t.Fatalf("UDP denominator: consumed=%d socket_drops=%d sent=259", observer.consumed, final)
				}
				t.Logf("sent=259 consumed=%d cumulative_initial=%d cumulative_final=%d metric_delta=%d fresh=%v",
					observer.consumed, initial, final, want, fresh)
			})
		}
	}
}

func BenchmarkListenerSocketDropObservation(b *testing.B) {
	metrics := bfdmetrics.NewCollector(prometheus.NewRegistry())
	ln := NewListenerFromConn(nil, false, metrics)
	meta := knownSocketDrops(10)
	ln.recordSocketDrops(meta, nil)
	if allocs := testing.AllocsPerRun(100, func() { ln.recordSocketDrops(meta, nil) }); allocs != 0 {
		b.Fatalf("listener drop observations=%gallocs/op,want0", allocs)
	}
	b.ReportAllocs()
	for b.Loop() {
		ln.recordSocketDrops(meta, nil)
	}
}
