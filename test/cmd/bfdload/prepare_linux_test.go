//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func TestPrepareCommandRefusesHostBeforeWrites(t *testing.T) {
	t.Parallel()
	namespace, err := bfdload.NetworkNamespaceID()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{
		"prepare", strconv.FormatUint(namespace, 10), "fixture", "--sessions=1", "--interface=lo",
		"--session=192.0.2.1:3784,192.0.2.2:3784",
	}
	if err := run(t.Context(), args, &output); !errors.Is(err, bfdload.ErrTopology) {
		t.Fatalf("host namespace mutation not rejected: %v", err)
	}
	var report bfdload.TopologyObservation
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Completed || report.Assigned != 0 || report.NamespaceID != namespace || report.HostID != namespace {
		t.Fatalf("host rejection lost measured namespace or claimed writes: %+v", report)
	}
	for _, args := range [][]string{{"prepare"}, {"prepare", "-1", "fixture"}, {"prepare", "not-an-inode", "fixture"}} {
		output.Reset()
		if err := run(t.Context(), args, &output); !errors.Is(err, bfdload.ErrTopology) {
			t.Fatalf("invalid topology identity accepted: args=%v error=%v", args, err)
		}
		if output.Len() != 0 {
			t.Fatal("malformed setup command emitted a runtime observation")
		}
	}
}
