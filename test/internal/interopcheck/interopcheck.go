// Package interopcheck contains shared parsers for legacy interop runners.
package interopcheck

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/frrjson"
)

const maxEpochLineSize = 1 << 20

// GoBGP v3 PeerState_SessionState values from api/gobgp.proto.
const (
	goBGPStateUnknown     = 0
	goBGPStateIdle        = 1
	goBGPStateConnect     = 2
	goBGPStateActive      = 3
	goBGPStateOpenSent    = 4
	goBGPStateOpenConfirm = 5
	goBGPStateEstablished = 6
)

var (
	errInvalidEpoch = errors.New("invalid packet epoch")
	errInvalidLimit = errors.New("invalid detection gap limit")
	errPeerNotFound = errors.New("peer not found")
)

// Status is the result of a detection-gap check.
type Status string

const (
	// StatusPass means the measured gap is inside the accepted bound.
	StatusPass Status = "pass"
	// StatusFail means the measured gap exceeds the accepted bound.
	StatusFail Status = "fail"
	// StatusSkip means no packet preceded the Down transition.
	StatusSkip Status = "skip"
)

// GapResult describes the interval between the last received peer packet and
// the first local Down packet.
type GapResult struct {
	Status Status
	Gap    float64
}

// GoBGPNeighborState returns the lowercase GoBGP v3 session state for peer.
func GoBGPNeighborState(input []byte, peer string) (string, error) {
	var neighbors []struct {
		State struct {
			NeighborAddress string `json:"neighbor_address"`
			SessionState    int    `json:"session_state"`
		} `json:"state"`
	}
	if err := json.Unmarshal(input, &neighbors); err != nil {
		return "", fmt.Errorf("decode GoBGP neighbor JSON: %w", err)
	}

	for _, neighbor := range neighbors {
		if neighbor.State.NeighborAddress == peer {
			return goBGPStateName(neighbor.State.SessionState), nil
		}
	}
	return "", fmt.Errorf("%w in GoBGP neighbor JSON: %s", errPeerNotFound, peer)
}

// FRRBFDPeerStatus returns the lowercase FRR BFD status for peer.
func FRRBFDPeerStatus(input []byte, peer string) (string, error) {
	payload, err := frrjson.ExtractJSONArray(string(input))
	if err != nil {
		return "", fmt.Errorf("extract FRR BFD peer JSON: %w", err)
	}

	var peers []struct {
		Peer   string `json:"peer"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(payload), &peers); err != nil {
		return "", fmt.Errorf("decode FRR BFD peer JSON: %w", err)
	}
	for _, candidate := range peers {
		if candidate.Peer == peer {
			return strings.ToLower(candidate.Status), nil
		}
	}
	return "", fmt.Errorf("%w in FRR BFD peer JSON: %s", errPeerNotFound, peer)
}

// DetectionGap checks the last input epoch before downEpoch against maxGap.
func DetectionGap(input io.Reader, downEpoch, maxGap float64) (GapResult, error) {
	if !finite(downEpoch) || !finite(maxGap) || maxGap < 0 {
		return GapResult{}, fmt.Errorf("%w: down=%g max=%g", errInvalidLimit, downEpoch, maxGap)
	}

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxEpochLineSize)
	lastBefore := 0.0
	found := false
	for row := 1; scanner.Scan(); row++ {
		text := strings.TrimSpace(scanner.Text())
		epoch, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return GapResult{}, fmt.Errorf("parse packet epoch row %d %q: %w", row, text, err)
		}
		if !finite(epoch) {
			return GapResult{}, fmt.Errorf("parse packet epoch row %d %q: %w", row, text, errInvalidEpoch)
		}
		if epoch < downEpoch {
			lastBefore = epoch
			found = true
		}
	}
	if err := scanner.Err(); err != nil {
		return GapResult{}, fmt.Errorf("read packet epochs: %w", err)
	}
	if !found {
		return GapResult{Status: StatusSkip}, nil
	}

	gap := downEpoch - lastBefore
	status := StatusFail
	if gap >= 0 && gap <= maxGap {
		status = StatusPass
	}
	return GapResult{Status: status, Gap: gap}, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func goBGPStateName(state int) string {
	switch state {
	case goBGPStateUnknown:
		return "unspecified"
	case goBGPStateIdle:
		return "idle"
	case goBGPStateConnect:
		return "connect"
	case goBGPStateActive:
		return "active"
	case goBGPStateOpenSent:
		return "opensent"
	case goBGPStateOpenConfirm:
		return "openconfirm"
	case goBGPStateEstablished:
		return "established"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
