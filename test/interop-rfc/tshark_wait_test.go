//go:build interop_rfc

package interop_rfc_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCleanTsharkOutputStripsStatusLines(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		"Running as user \"root\" and group \"root\". This could be dangerous.",
		"Capturing on 'any'",
		"tshark: Promiscuous mode not supported on the \"any\" device.",
		" ** (tshark:1) 12:59:00.000000 [Main MESSAGE] -- Capture started.",
		"172.22.0.10\t172.22.0.20\t100000",
		"1 packet captured",
		"",
	}, "\n")

	got := strings.Split(cleanTsharkOutput(output), "\n")
	want := []string{"172.22.0.10\t172.22.0.20\t100000", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanTsharkOutput() = %#v, want %#v", got, want)
	}
}

func TestWaitTsharkFieldsRetriesUntilRowsAppear(t *testing.T) {
	t.Parallel()

	calls := 0
	rows, err := waitTsharkFields(
		t.Context(),
		"bfd && bfd.sta == 0x03",
		[]string{"bfd.desired_min_tx_interval"},
		10,
		100*time.Millisecond,
		time.Millisecond,
		func(string, []string, int) ([][]string, error) {
			calls++
			if calls < 3 {
				return nil, nil
			}
			return [][]string{{"100000"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("waitTsharkFields returned error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("query calls = %d, want 3", calls)
	}
	if got := rows[0][0]; got != "100000" {
		t.Fatalf("rows[0][0] = %q, want 100000", got)
	}
}

func TestWaitTsharkFieldsReportsLastError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("pcap not ready")
	_, err := waitTsharkFields(
		t.Context(),
		"bfd && ip.src == 172.22.0.10",
		[]string{"frame.number"},
		0,
		5*time.Millisecond,
		time.Millisecond,
		func(string, []string, int) ([][]string, error) {
			return nil, wantErr
		},
	)
	if err == nil {
		t.Fatal("waitTsharkFields returned nil error, want timeout")
	}
	for _, want := range []string{
		"timed out waiting for tshark rows",
		"bfd && ip.src == 172.22.0.10",
		"pcap not ready",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestWaitTsharkCountRetriesUntilPacketsAppear(t *testing.T) {
	t.Parallel()

	calls := 0
	count, err := waitTsharkCount(
		t.Context(),
		"udp.dstport == 3785",
		100*time.Millisecond,
		time.Millisecond,
		func(string) (int, error) {
			calls++
			if calls < 4 {
				return 0, nil
			}
			return 7, nil
		},
	)
	if err != nil {
		t.Fatalf("waitTsharkCount returned error: %v", err)
	}
	if calls != 4 {
		t.Fatalf("query calls = %d, want 4", calls)
	}
	if count != 7 {
		t.Fatalf("count = %d, want 7", count)
	}
}
