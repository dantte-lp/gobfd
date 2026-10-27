//go:build interop_testcontainers

package interop_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
)

func TestSameKeyBoundedEvidenceFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "evidence")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if raw, err := readSameKeyBoundedFile(path); err != nil || len(raw) != 64<<10 {
		t.Fatalf("bounded evidence length=%d error=%v", len(raw), err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, (64<<10)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSameKeyBoundedFile(path); !errors.Is(err, errTimerEvidence) {
		t.Fatalf("accepted oversized evidence: %v", err)
	}
	if _, err := readSameKeyBoundedFile(path + "-absent"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost missing-file error: %v", err)
	}
}

func sameKeyContractEvidence() ([]sameKeyFrame, sameKeyAPIReceipt) {
	owner := sameKeyFrame{
		Number: 1, Source: gobfdIP, Destination: frrIP, State: 3,
		Mine: 101, Yours: 202, TX: 300000, RX: 300000, Multiplier: 3,
		AuthPresent: 1, AuthType: 1, Key: 7, UDPLength: 136, SourcePort: 50000, DestinationPort: 3784,
	}
	peer := owner
	peer.Number, peer.Source, peer.Destination = 2, frrIP, gobfdIP
	peer.Mine, peer.Yours, peer.SourcePort = 202, 101, 60000
	admin := owner
	admin.Number, admin.State, admin.Diagnostic = 3, 0, 7
	down := peer
	down.Number, down.State, down.Diagnostic = 4, 1, 3
	next := owner
	next.Number, next.State, next.Mine, next.Yours, next.SourcePort = 5, 1, 303, 0, 50001
	init := peer
	init.Number, init.State, init.Yours = 6, 2, 303
	up := next
	up.Number, up.State, up.Yours = 7, 3, 202
	peerUp := init
	peerUp.Number, peerUp.State = 8, 3
	frames := []sameKeyFrame{owner, peer, admin, down, next, init, up, peerUp}
	for index := range frames {
		frames[index].Epoch = time.Unix(1700000000, int64(index)*100000000).UTC()
	}
	return frames, sameKeyAPIReceipt{
		OldLocal: 101, OldRemote: 202, NewLocal: 303, NewRemote: 202,
		OldPort: 50000, OldSocketInode: 12345, OldDetached: true,
		OldSocketBeforeConflict: true, OldSocketAfterConflict: true,
		DeleteComplete: true, ClosedBeforeRetry: true, DuplicateCode: connect.CodeAlreadyExists,
		OldUp: true, NewUp: true, FRRBefore: "up", FRRRetiring: "down", FRRAfter: "up",
	}
}

func TestSameKeyWireEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func([]sameKeyFrame, *sameKeyAPIReceipt) []sameKeyFrame
		valid  bool
	}{
		{name: "old AdminDown then distinct new bilateral Up", valid: true},
		{
			name: "FRR need not pad its replies", valid: true,
			change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
				for index := range frames {
					if frames[index].Source == frrIP {
						frames[index].UDPLength = 48
					}
				}
				return frames
			},
		},
		{
			name: "Down and AdminDown may advertise one second TX", valid: true,
			change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
				frames[2].TX, frames[4].TX = 1000000, 1000000
				return frames
			},
		},
		{
			name: "source port may be reused after old socket closes", valid: true,
			change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
				frames[4].SourcePort, frames[6].SourcePort = 50000, 50000
				return frames
			},
		},
		{
			name: "peer may choose a new discriminator", valid: true,
			change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
				receipt.NewRemote = 404
				frames[5].Mine, frames[7].Mine, frames[6].Yours = 404, 404, 404
				return frames
			},
		},
		{name: "empty capture", change: func(_ []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame { return nil }},
		{name: "missing old AdminDown", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			return slices.Delete(frames, 2, 3)
		}},
		{name: "old sender after new epoch", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			late := frames[2]
			late.Number, late.Epoch = frames[5].Number, frames[5].Epoch
			frames[5] = late
			return frames
		}},
		{name: "reused local discriminator", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.NewLocal = 101
			return frames
		}},
		{name: "epoch source port changes", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			frames[6].SourcePort = 50002
			return frames
		}},
		{name: "old socket port mismatch", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.OldPort = 50002
			return frames
		}},
		{name: "wrong peer tuple", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			frames[6].Destination = bird3IP
			return frames
		}},
		{name: "new peer names old epoch", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			frames[7].Yours = 101
			return frames
		}},
		{name: "missing new peer Up", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			return frames[:7]
		}},
		{name: "wrong AdminDown diagnostic", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			frames[2].Diagnostic = 1
			return frames
		}},
		{name: "authentication changed", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			frames[2].Key = 8
			return frames
		}},
		{name: "owner padding changed", change: func(frames []sameKeyFrame, _ *sameKeyAPIReceipt) []sameKeyFrame {
			frames[6].UDPLength = 137
			return frames
		}},
		{name: "old owner not detached", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.OldDetached = false
			return frames
		}},
		{name: "no socket before conflict", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.OldSocketBeforeConflict = false
			return frames
		}},
		{name: "no socket after conflict", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.OldSocketAfterConflict = false
			return frames
		}},
		{name: "old socket survives Delete", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.OldSocketAfterDelete = true
			return frames
		}},
		{name: "missing socket identity", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.OldSocketInode = 0
			return frames
		}},
		{name: "Delete never completed", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.DeleteComplete = false
			return frames
		}},
		{name: "retry before closure", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.ClosedBeforeRetry = false
			return frames
		}},
		{name: "wrong conflict code", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.DuplicateCode = connect.CodeUnavailable
			return frames
		}},
		{name: "new API owner never Up", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.NewUp = false
			return frames
		}},
		{name: "no FRR retirement state", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.FRRRetiring = ""
			return frames
		}},
		{name: "FRR failed to recover", change: func(frames []sameKeyFrame, receipt *sameKeyAPIReceipt) []sameKeyFrame {
			receipt.FRRAfter = "down"
			return frames
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, receipt := sameKeyContractEvidence()
			if test.change != nil {
				frames = test.change(frames, &receipt)
			}
			if err := validateSameKeyWire(frames, receipt); (err == nil) != test.valid {
				t.Fatalf("same-key evidence valid=%t, want %t: %v", err == nil, test.valid, err)
			}
		})
	}
}

func TestSameKeyFrameParsing(t *testing.T) {
	t.Parallel()
	const row = "1\t1700000000.100000000\t172.20.0.10\t172.20.0.20\t0\t0\t0\t101\t202\t" +
		"300000\t300000\t3\t1\t1\t7\t\t0\t136\t50000\t7\t3784"
	for _, test := range []struct {
		name string
		row  string
		ok   bool
	}{
		{name: "literal authenticated AdminDown", row: row, ok: true},
		{name: "missing port column", row: strings.TrimSuffix(row, "\t3784")},
		{name: "invalid diagnostic", row: strings.TrimSuffix(row, "7\t3784") + "oops\t3784"},
		{name: "wrong destination port", row: strings.TrimSuffix(row, "3784") + "4784"},
		{name: "source port outside ephemeral range", row: strings.Replace(row, "\t50000\t", "\t3784\t", 1)},
		{name: "invalid auth key", row: strings.Replace(row, "\t1\t1\t7\t", "\t1\t1\t256\t", 1)},
		{name: "invalid timer epoch", row: strings.Replace(row, "1700000000.100000000", "NaN", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, err := parseSameKeyFrames([][]string{strings.Split(test.row, "\t")})
			if (err == nil) != test.ok {
				t.Fatalf("parse valid=%t, want %t: %v", err == nil, test.ok, err)
			}
			if test.ok && (len(frames) != 1 || frames[0].Mine != 101 || frames[0].Diagnostic != 7 ||
				frames[0].SourcePort != 50000 || frames[0].DestinationPort != 3784 || frames[0].Key != 7) {
				t.Fatalf("lost literal frame identity: %+v", frames)
			}
		})
	}
}

func TestSameKeySenderSocketProof(t *testing.T) {
	t.Parallel()
	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"
	const row = "42: 0A0014AC:C350 00000000:0000 07 00000000:00000000 00:00000000 00000000 0 0 12345 2 0 0\n"
	for _, test := range []struct {
		name  string
		table string
		fds   []string
		port  uint16
		valid bool
	}{
		{name: "owned UDP sender", table: header + row, fds: []string{"socket:[12345]"}, port: 50000, valid: true},
		{name: "socket belongs to another process", table: header + row, fds: []string{"socket:[54321]"}, port: 50000},
		{name: "bound port differs", table: header + row, fds: []string{"socket:[12345]"}, port: 50001},
		{
			name: "local address differs", table: header + strings.Replace(row, "0A0014AC", "0B0014AC", 1),
			fds: []string{"socket:[12345]"}, port: 50000,
		},
		{name: "ambiguous bound sockets", table: header + row + row, fds: []string{"socket:[12345]"}, port: 50000},
		{name: "malformed bound row", table: header + "42: 0A0014AC:C350\n", fds: []string{"socket:[12345]"}, port: 50000},
		{name: "zero inode", table: header + strings.Replace(row, "12345", "0", 1), fds: []string{"socket:[0]"}, port: 50000},
		{
			name: "invalid inode", table: header + strings.Replace(row, "12345", "invalid", 1),
			fds: []string{"socket:[invalid]"}, port: 50000,
		},
		{name: "empty table", fds: []string{"socket:[12345]"}, port: 50000},
		{name: "invalid ephemeral port", table: header + row, fds: []string{"socket:[12345]"}, port: 3784},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			inode, err := sameKeySenderInode(test.table, test.fds, test.port)
			if (err == nil) != test.valid || (test.valid && inode != 12345) {
				t.Fatalf("sender inode=%d error=%v, want valid=%t inode12345", inode, err, test.valid)
			}
		})
	}
}
