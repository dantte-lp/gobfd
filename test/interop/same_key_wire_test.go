//go:build interop_testcontainers

package interop_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect/v2"
)

type sameKeyFrame struct {
	detectFrame

	Diagnostic      uint64
	DestinationPort uint64
}

type sameKeyAPIReceipt struct {
	OldLocal                uint32       `json:"old_local"`
	OldRemote               uint32       `json:"old_remote"`
	NewLocal                uint32       `json:"new_local"`
	NewRemote               uint32       `json:"new_remote"`
	OldPort                 uint16       `json:"old_port"`
	OldSocketInode          uint64       `json:"old_socket_inode"`
	OldDetached             bool         `json:"old_detached"`
	OldSocketBeforeConflict bool         `json:"old_socket_before_conflict"`
	OldSocketAfterConflict  bool         `json:"old_socket_after_conflict"`
	OldSocketAfterDelete    bool         `json:"old_socket_after_delete"`
	DeleteComplete          bool         `json:"delete_complete"`
	ClosedBeforeRetry       bool         `json:"closed_before_retry"`
	DuplicateCode           connect.Code `json:"duplicate_code"`
	OldUp                   bool         `json:"old_up"`
	NewUp                   bool         `json:"new_up"`
	FRRBefore               string       `json:"frr_before"`
	FRRRetiring             string       `json:"frr_retiring"`
	FRRAfter                string       `json:"frr_after"`
}

func validateSameKeyWire(frames []sameKeyFrame, receipt sameKeyAPIReceipt) error {
	if len(frames) == 0 || receipt.OldLocal == 0 || receipt.NewLocal == 0 || receipt.OldRemote == 0 ||
		receipt.NewRemote == 0 || receipt.NewLocal == receipt.OldLocal || receipt.OldSocketInode == 0 ||
		!receipt.OldDetached || !receipt.OldSocketBeforeConflict || !receipt.OldSocketAfterConflict ||
		receipt.OldSocketAfterDelete || !receipt.DeleteComplete || !receipt.ClosedBeforeRetry ||
		receipt.DuplicateCode != connect.CodeAlreadyExists || !receipt.OldUp || !receipt.NewUp ||
		receipt.FRRBefore != "up" || receipt.FRRRetiring != "down" || receipt.FRRAfter != "up" {
		return fmt.Errorf("incomplete distinct-epoch API/socket/peer evidence: %w", errTimerEvidence)
	}
	var oldUp, oldPeerUp, admin, next, newUp, newPeerUp bool
	var newPort uint64
	for _, frame := range frames {
		owner := frame.Source == gobfdIP && frame.Destination == frrIP
		peer := frame.Source == frrIP && frame.Destination == gobfdIP
		if (!owner && !peer) || frame.DestinationPort != 3784 || frame.SourcePort < 49152 ||
			frame.SourcePort > 65535 || frame.AuthPresent != 1 || frame.AuthType != 1 || frame.Key != 7 {
			return fmt.Errorf("changed FRR transport/auth tuple at frame %d: %w", frame.Number, errTimerEvidence)
		}
		if peer {
			if frame.State == 3 && frame.Mine == uint64(receipt.OldRemote) && frame.Yours == uint64(receipt.OldLocal) && !admin {
				oldPeerUp = true
			}
			if next && frame.State == 3 && frame.Mine == uint64(receipt.NewRemote) && frame.Yours == uint64(receipt.NewLocal) {
				newPeerUp = true
			}
			continue
		}
		if frame.UDPLength != 136 {
			return fmt.Errorf("changed GoBFD padding at frame %d: %w", frame.Number, errTimerEvidence)
		}
		switch frame.Mine {
		case uint64(receipt.OldLocal):
			if next || frame.SourcePort != uint64(receipt.OldPort) {
				return fmt.Errorf("old sender overlaps new epoch or changes port: %w", errTimerEvidence)
			}
			if frame.State == 3 && frame.Yours == uint64(receipt.OldRemote) && !admin {
				oldUp = true
			}
			if frame.State == 0 && frame.Diagnostic == 7 && frame.Yours == uint64(receipt.OldRemote) && oldUp && oldPeerUp {
				admin = true
			}
		case uint64(receipt.NewLocal):
			if !admin || (next && frame.SourcePort != newPort) {
				return fmt.Errorf("new epoch precedes AdminDown or changes port: %w", errTimerEvidence)
			}
			next, newPort = true, frame.SourcePort
			if frame.State == 3 && frame.Yours == uint64(receipt.NewRemote) {
				newUp = true
			}
		default:
			return fmt.Errorf("unexpected GoBFD epoch %d: %w", frame.Mine, errTimerEvidence)
		}
	}
	if !oldUp || !oldPeerUp || !admin || !newUp || !newPeerUp {
		return fmt.Errorf("missing bilateral Up or retiring AdminDown: %w", errTimerEvidence)
	}
	return nil
}

func sameKeyFrameFields() []string { return append(detectFrameFields(), "bfd.diag", "udp.dstport") }

func parseSameKeyFrames(rows [][]string) ([]sameKeyFrame, error) {
	width := len(detectFrameFields())
	baseRows := make([][]string, len(rows))
	for index, row := range rows {
		if len(row) != width+2 {
			return nil, fmt.Errorf("same-key capture columns: %w", errTimerEvidence)
		}
		baseRows[index] = row[:width]
	}
	base, err := parseDetectFrames(baseRows)
	if err != nil {
		return nil, err
	}
	frames := make([]sameKeyFrame, len(base))
	for index, frame := range base {
		decoded := sameKeyFrame{detectFrame: frame}
		decoded.Diagnostic, err = parseTimerField(rows[index][width])
		if err != nil {
			return nil, err
		}
		decoded.DestinationPort, err = parseTimerField(rows[index][width+1])
		if err != nil {
			return nil, err
		}
		if decoded.Diagnostic > 8 || decoded.DestinationPort != 3784 || decoded.SourcePort < 49152 {
			return nil, fmt.Errorf("invalid same-key diagnostic/ports: %w", errTimerEvidence)
		}
		frames[index] = decoded
	}
	return frames, nil
}

func sameKeySenderInode(table string, fdTargets []string, port uint16) (uint64, error) {
	if port < 49152 || len(table) > 64<<10 || !strings.Contains(table, "local_address") {
		return 0, fmt.Errorf("invalid same-key UDP socket table/port: %w", errTimerEvidence)
	}
	// The fixed fixture address 172.20.0.10 uses this little-endian /proc encoding.
	bound := fmt.Sprintf("0A0014AC:%04X", port)
	var inode uint64
	for line := range strings.SplitSeq(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != bound {
			continue
		}
		if len(fields) < 10 || inode != 0 {
			return 0, fmt.Errorf("malformed/ambiguous same-key sender socket: %w", errTimerEvidence)
		}
		value, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil || value == 0 || !slices.Contains(fdTargets, fmt.Sprintf("socket:[%d]", value)) {
			return 0, fmt.Errorf("missing daemon-owned sender inode: %w", errTimerEvidence)
		}
		inode = value
	}
	if inode == 0 {
		return 0, fmt.Errorf("bound same-key sender not found: %w", errTimerEvidence)
	}
	return inode, nil
}
