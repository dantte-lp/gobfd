//go:build interop_clab

package interop_clab_test

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

const (
	timerStatusIntervalMS  = 300
	timerStatusDetectionMS = 900
	timerStatusMultiplier  = 3
)

type gobfdTimerStatus struct {
	PeerAddress      string `json:"peer_address"`
	LocalAddress     string `json:"local_address"`
	Type             string `json:"type"`
	LocalState       string `json:"local_state"`
	RemoteState      string `json:"remote_state"`
	DetectMultiplier uint32 `json:"detect_multiplier"`
	DesiredMinTx     string `json:"desired_min_tx_interval"`
	RequiredMinRx    string `json:"required_min_rx_interval"`
	RemoteMinRx      string `json:"remote_min_rx_interval"`
	NegotiatedTx     string `json:"negotiated_tx_interval"`
	DetectionTime    string `json:"detection_time"`
}

type frrTimerStatus struct {
	Peer                   string          `json:"peer"`
	Local                  json.RawMessage `json:"local"`
	Status                 string          `json:"status"`
	Multihop               *bool           `json:"multihop"`
	ReceiveInterval        uint32          `json:"receive-interval"`         //nolint:tagliatelle // FRR wire key.
	TransmitInterval       uint32          `json:"transmit-interval"`        //nolint:tagliatelle // FRR wire key.
	RemoteReceiveInterval  uint32          `json:"remote-receive-interval"`  //nolint:tagliatelle // FRR wire key.
	RemoteTransmitInterval uint32          `json:"remote-transmit-interval"` //nolint:tagliatelle // FRR wire key.
	DetectMultiplier       uint32          `json:"detect-multiplier"`        //nolint:tagliatelle // FRR wire key.
	RemoteDetectMultiplier uint32          `json:"remote-detect-multiplier"` //nolint:tagliatelle // FRR wire key.
	DetectionTimeout       uint32          `json:"detection-timeout"`        //nolint:tagliatelle // FRR wire key.
}

func validateFRRTimers(gobfdJSON, frrJSON string, peer vendorPeer) error {
	if !jsontext.Value(gobfdJSON).IsValid() || !jsontext.Value(frrJSON).IsValid() {
		return errors.New("timer status requires strict GoBFD and FRR JSON documents")
	}
	var gobfdRows []gobfdTimerStatus
	if err := json.Unmarshal([]byte(gobfdJSON), &gobfdRows); err != nil {
		return fmt.Errorf("decode GoBFD timer rows: %w", err)
	}
	var frrRows []frrTimerStatus
	if err := json.Unmarshal([]byte(frrJSON), &frrRows); err != nil {
		return fmt.Errorf("decode FRR timer rows: %w", err)
	}
	local, err := netip.ParseAddr(peer.localIP)
	if err != nil {
		return fmt.Errorf("parse expected GoBFD local address: %w", err)
	}
	remote, err := netip.ParseAddr(peer.peerIP)
	if err != nil {
		return fmt.Errorf("parse expected FRR peer address: %w", err)
	}
	gobfd, err := selectGoBFDTimerStatus(gobfdRows, local, remote)
	if err != nil {
		return err
	}
	if validationErr := validateGoBFDTimerStatus(gobfd); validationErr != nil {
		return validationErr
	}
	frr, err := selectFRRTimerStatus(frrRows, local)
	if err != nil {
		return err
	}
	return validateFRRTimerStatus(frr, remote)
}

func selectGoBFDTimerStatus(rows []gobfdTimerStatus, local, remote netip.Addr) (*gobfdTimerStatus, error) {
	var selected *gobfdTimerStatus
	for index := range rows {
		row := &rows[index]
		rowPeer, err := netip.ParseAddr(row.PeerAddress)
		if err != nil {
			return nil, fmt.Errorf("parse GoBFD timer peer: %w", err)
		}
		rowLocal, err := netip.ParseAddr(row.LocalAddress)
		if err != nil {
			return nil, fmt.Errorf("parse GoBFD timer local address: %w", err)
		}
		if rowPeer != remote || rowLocal != local {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("duplicate GoBFD timer tuple %s -> %s", local, remote)
		}
		selected = row
	}
	if selected == nil {
		return nil, fmt.Errorf("GoBFD timer tuple %s -> %s is absent", local, remote)
	}
	return selected, nil
}

func selectFRRTimerStatus(rows []frrTimerStatus, local netip.Addr) (*frrTimerStatus, error) {
	var selected *frrTimerStatus
	for index := range rows {
		row := &rows[index]
		rowPeer, err := netip.ParseAddr(row.Peer)
		if err != nil {
			return nil, fmt.Errorf("parse FRR timer peer: %w", err)
		}
		if rowPeer != local {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("duplicate FRR timer peer %s", local)
		}
		selected = row
	}
	if selected == nil {
		return nil, fmt.Errorf("FRR timer peer %s is absent", local)
	}
	return selected, nil
}

func validateGoBFDTimerStatus(row *gobfdTimerStatus) error {
	if row.Type != "single-hop" || row.LocalState != "Up" || row.RemoteState != "Up" ||
		row.DetectMultiplier != timerStatusMultiplier {
		return errors.New("GoBFD timer row must be single-hop Up/Up with detect multiplier 3")
	}
	for _, timer := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"desired_min_tx_interval", row.DesiredMinTx, timerStatusIntervalMS * time.Millisecond},
		{"required_min_rx_interval", row.RequiredMinRx, timerStatusIntervalMS * time.Millisecond},
		{"remote_min_rx_interval", row.RemoteMinRx, timerStatusIntervalMS * time.Millisecond},
		{"negotiated_tx_interval", row.NegotiatedTx, timerStatusIntervalMS * time.Millisecond},
		{"detection_time", row.DetectionTime, timerStatusDetectionMS * time.Millisecond},
	} {
		value, err := time.ParseDuration(timer.value)
		if err != nil {
			return fmt.Errorf("parse GoBFD %s: %w", timer.name, err)
		}
		if value != timer.want {
			return fmt.Errorf("GoBFD %s is %s, want %s", timer.name, value, timer.want)
		}
	}
	return nil
}

func validateFRRTimerStatus(row *frrTimerStatus, remote netip.Addr) error {
	if row.Status != "up" || row.Multihop == nil || *row.Multihop {
		return errors.New("FRR timer row must be up with explicit multihop false")
	}
	if row.ReceiveInterval != timerStatusIntervalMS || row.TransmitInterval != timerStatusIntervalMS ||
		row.RemoteReceiveInterval != timerStatusIntervalMS || row.RemoteTransmitInterval != timerStatusIntervalMS ||
		row.DetectMultiplier != timerStatusMultiplier || row.RemoteDetectMultiplier != timerStatusMultiplier ||
		row.DetectionTimeout != timerStatusDetectionMS {
		return errors.New("FRR nominal timers must be 300ms with multipliers 3 and detection timeout 900ms")
	}
	if len(row.Local) == 0 {
		return nil
	}
	var local string
	if err := json.Unmarshal(row.Local, &local); err != nil {
		return fmt.Errorf("decode optional FRR local address: %w", err)
	}
	address, err := netip.ParseAddr(local)
	if err != nil {
		return fmt.Errorf("parse optional FRR local address: %w", err)
	}
	if address != remote {
		return fmt.Errorf("FRR local address %s does not match peer %s", address, remote)
	}
	return nil
}
