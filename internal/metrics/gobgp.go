package bfdmetrics

import (
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// GoBGPPeerState is the scrape-time GoBGP state supplied by the daemon.
type GoBGPPeerState struct {
	Penalty             float64
	Suppressed          bool
	SuppressionDeadline time.Time
	Action              string
	NextAttempt         time.Time
}

type goBGPPeerCollector struct {
	mu     sync.RWMutex
	peers  []string // immutable after publication
	source func(string) GoBGPPeerState

	penalty             *prometheus.Desc
	suppressed          *prometheus.Desc
	suppressionDeadline *prometheus.Desc
	pending             *prometheus.Desc
	pendingDeadline     *prometheus.Desc
}

func newGoBGPPeerCollector() *goBGPPeerCollector {
	return &goBGPPeerCollector{
		penalty: prometheus.NewDesc(namespace+"_gobgp_dampening_penalty",
			"Effective decayed flap penalty at observation time.", []string{labelPeerAddr}, nil),
		suppressed: prometheus.NewDesc(namespace+"_gobgp_dampening_suppressed",
			"1 when BFD recovery remains suppressed at observation time.", []string{labelPeerAddr}, nil),
		suppressionDeadline: prometheus.NewDesc(namespace+"_gobgp_dampening_suppression_deadline_seconds",
			"Unix timestamp of the maximum suppression bound, or 0 when not suppressed.", []string{labelPeerAddr}, nil),
		pending: prometheus.NewDesc(namespace+"_gobgp_pending_action",
			"1 for the unapplied desired peer action after the last completed handler step.",
			[]string{labelPeerAddr, "action"}, nil),
		pendingDeadline: prometheus.NewDesc(namespace+"_gobgp_pending_action_deadline_seconds",
			"Unix timestamp of the next action retry or suppression recheck, or 0 when none.",
			[]string{labelPeerAddr, "action"}, nil),
	}
}

// SetGoBGPConfiguredPeers replaces the configured-peer metric allowlist.
func (c *Collector) SetGoBGPConfiguredPeers(peers []netip.Addr) {
	unique := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		if peer.IsValid() {
			unique[peer.Unmap().String()] = struct{}{}
		}
	}
	labels := make([]string, 0, len(unique))
	for peer := range unique {
		labels = append(labels, peer)
	}
	slices.Sort(labels)
	c.goBGPPeers.mu.Lock()
	c.goBGPPeers.peers = labels
	c.goBGPPeers.mu.Unlock()
}

// SetGoBGPPeerSource installs a concurrent-safe observation source.
func (c *Collector) SetGoBGPPeerSource(source func(string) GoBGPPeerState) {
	c.goBGPPeers.mu.Lock()
	c.goBGPPeers.source = source
	c.goBGPPeers.mu.Unlock()
}

func (c *goBGPPeerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.penalty
	ch <- c.suppressed
	ch <- c.suppressionDeadline
	ch <- c.pending
	ch <- c.pendingDeadline
}

func (c *goBGPPeerCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	peers, source := c.peers, c.source
	c.mu.RUnlock()
	if source == nil {
		return
	}
	for _, peer := range peers {
		state := source(peer)
		suppressed := 0.0
		if state.Suppressed {
			suppressed = 1
		}
		ch <- prometheus.MustNewConstMetric(c.penalty, prometheus.GaugeValue, state.Penalty, peer)
		ch <- prometheus.MustNewConstMetric(c.suppressed, prometheus.GaugeValue, suppressed, peer)
		ch <- prometheus.MustNewConstMetric(c.suppressionDeadline, prometheus.GaugeValue,
			metricTimestamp(state.SuppressionDeadline), peer)
		for _, action := range [...]string{"up", "down"} {
			pending, deadline := 0.0, 0.0
			if state.Action == action {
				pending = 1
				deadline = metricTimestamp(state.NextAttempt)
			}
			ch <- prometheus.MustNewConstMetric(c.pending, prometheus.GaugeValue, pending, peer, action)
			ch <- prometheus.MustNewConstMetric(c.pendingDeadline, prometheus.GaugeValue, deadline, peer, action)
		}
	}
}

func metricTimestamp(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix()) + float64(t.Nanosecond())/float64(time.Second)
}
