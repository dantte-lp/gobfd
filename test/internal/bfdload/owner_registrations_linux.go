//go:build linux

package bfdload

import (
	"fmt"

	dto "github.com/prometheus/client_model/go"
)

// OwnerRegistrations separates real registry membership from retained series.
// Known peers are the Manager's one-to-one session/demux registrations, not hosts.
type OwnerRegistrations struct {
	KnownPeers                int    `json:"known_peers"`
	ActiveMetricRegistrations uint64 `json:"active_metric_registrations"`
	RetainedSessions          uint64 `json:"retained_session_series"`
	RetainedPacketRows        uint64 `json:"retained_packet_series"`
}

func observeOwnerRegistrations(families []*dto.MetricFamily, knownPeers int) (OwnerRegistrations, error) {
	observed := OwnerRegistrations{KnownPeers: knownPeers}
	for _, family := range families {
		switch family.GetName() {
		case "gobfd_bfd_sessions":
			observed.RetainedSessions = uint64(len(family.GetMetric()))
			for _, metric := range family.GetMetric() {
				gauge := metric.GetGauge()
				value := gauge.GetValue()
				if gauge == nil || (value != 0 && value != 1) {
					return observed, fmt.Errorf("owner session gauge is not a physical registration: %w", errOwnerConfig)
				}
				observed.ActiveMetricRegistrations += uint64(value)
			}
		case "gobfd_bfd_packets_sent_total", "gobfd_bfd_packets_received_total",
			"gobfd_bfd_packets_dropped_total", "gobfd_bfd_auth_failures_total":
			observed.RetainedPacketRows += uint64(len(family.GetMetric()))
		}
	}
	if observed.ActiveMetricRegistrations != uint64(knownPeers) { //nolint:gosec // Manager registry length is nonnegative.
		return observed, fmt.Errorf("owner registry and active collector registrations disagree: %w", errOwnerConfig)
	}
	return observed, nil
}
