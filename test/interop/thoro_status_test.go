//go:build interop

package interop_test

import "testing"

func TestIsThoroUnsupportedPollSequenceCrash(t *testing.T) {
	t.Parallel()

	logs := `panic: Not implemented
github.com/Thoro/bfd/pkg/server.(*Peer).SetDesiredMinTxInterval(0x1, 0xf4240)
	github.com/Thoro/bfd/pkg/server/peer.go:177 +0xb5`
	if !isThoroUnsupportedPollSequenceCrash(logs) {
		t.Fatal("isThoroUnsupportedPollSequenceCrash returned false for known Thoro panic")
	}
}

func TestIsThoroUnsupportedPollSequenceCrashRejectsOtherLogs(t *testing.T) {
	t.Parallel()

	for _, logs := range []string{
		"",
		"panic: different failure",
		"github.com/Thoro/bfd/pkg/server.(*Peer).SetDesiredMinTxInterval",
	} {
		if isThoroUnsupportedPollSequenceCrash(logs) {
			t.Fatalf("isThoroUnsupportedPollSequenceCrash returned true for %q", logs)
		}
	}
}
