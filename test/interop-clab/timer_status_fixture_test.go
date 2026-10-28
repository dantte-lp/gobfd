//go:build interop_clab

package interop_clab_test

import (
	"strings"
	"testing"
)

const timerGoBFDIPv4Row = `{
"peer_address":"10.0.6.2",
"local_address":"10.0.6.1",
"type":"single-hop",
"local_state":"Up",
"remote_state":"Up",
"detect_multiplier":3,
"desired_min_tx_interval":"300ms",
"required_min_rx_interval":"300ms",
"remote_min_rx_interval":"300ms",
"negotiated_tx_interval":"300ms",
"detection_time":"900ms",
"note":"300 marker"
}`

const timerFRRIPv4Row = `{
"peer":"10.0.6.1",
"status":"up",
"multihop":false,
"receive-interval":300,
"transmit-interval":300,
"remote-receive-interval":300,
"remote-transmit-interval":300,
"detect-multiplier":3,
"remote-detect-multiplier":3,
"detection-timeout":900,
"transmit-interval-actual":257,
"note":"300 marker"
}`

const timerGoBFDIPv6Row = `{
"peer_address":"fd00:0000:0006:0000:0000:0000:0000:0001",
"local_address":"fd00:0000:0006:0000:0000:0000:0000:0000",
"type":"single-hop",
"local_state":"Up",
"remote_state":"Up",
"detect_multiplier":3,
"desired_min_tx_interval":"0.3s",
"required_min_rx_interval":"300000us",
"remote_min_rx_interval":"300ms",
"negotiated_tx_interval":"300000000ns",
"detection_time":"0.9s",
"note":"unrelated field"
}`

const timerFRRIPv6Row = `{
"peer":"fd00:0000:0006:0000:0000:0000:0000:0000",
"local":"fd00:0000:0006:0000:0000:0000:0000:0001",
"status":"up",
"multihop":false,
"receive-interval":300,
"transmit-interval":300,
"remote-receive-interval":300,
"remote-transmit-interval":300,
"detect-multiplier":3,
"remote-detect-multiplier":3,
"detection-timeout":900,
"transmit-interval-actual":229,
"note":"unrelated field"
}`

func TestValidateFRRTimersAcceptsExactNominalContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		gobfd string
		frr   string
		peer  vendorPeer
	}{
		{
			name:  "IPv4 with absent optional FRR local and jittered actual TX",
			gobfd: "[" + timerGoBFDIPv4Row + "]", frr: "[" + timerFRRIPv4Row + "]",
			peer: vendorPeer{name: "frr", baseName: "frr", peerIP: "10.0.6.2", localIP: "10.0.6.1"},
		},
		{
			name:  "IPv6 normalization and equivalent duration units",
			gobfd: "[" + timerGoBFDIPv6Row + "]", frr: "[" + timerFRRIPv6Row + "]",
			peer: vendorPeer{name: "frr-v6", baseName: "frr", peerIP: "fd00:0:6::1", localIP: "fd00:0:6::"},
		},
		{
			name:  "IPv4 with matching explicit FRR local",
			gobfd: "[" + timerGoBFDIPv4Row + "]",
			frr:   "[" + strings.Replace(timerFRRIPv4Row, `{`, `{"local":"10.0.6.2",`, 1) + "]",
			peer:  vendorPeer{name: "frr", baseName: "frr", peerIP: "10.0.6.2", localIP: "10.0.6.1"},
		},
		{
			name: "additional nonmatching rows",
			gobfd: "[" + timerGoBFDIPv4Row + "," +
				strings.Replace(timerGoBFDIPv4Row, `"local_address":"10.0.6.1"`,
					`"local_address":"10.0.6.99"`, 1) + "]",
			frr: "[" + timerFRRIPv4Row + "," +
				strings.Replace(timerFRRIPv4Row, `"peer":"10.0.6.1"`, `"peer":"10.0.6.99"`, 1) + "]",
			peer: vendorPeer{name: "frr", baseName: "frr", peerIP: "10.0.6.2", localIP: "10.0.6.1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateFRRTimers(test.gobfd, test.frr, test.peer); err != nil {
				t.Fatalf("valid independent timer fixture was rejected: %v", err)
			}
		})
	}
}

func TestValidateFRRTimersRequiresEveryTypedField(t *testing.T) {
	t.Parallel()
	for _, field := range []struct {
		side  string
		name  string
		value string
		wrong string
	}{
		{"gobfd", "peer_address", `"10.0.6.2"`, `"10.0.6.99"`},
		{"gobfd", "local_address", `"10.0.6.1"`, `"10.0.6.99"`},
		{"gobfd", "type", `"single-hop"`, `"multi-hop"`},
		{"gobfd", "local_state", `"Up"`, `"Down"`},
		{"gobfd", "remote_state", `"Up"`, `"Down"`},
		{"gobfd", "detect_multiplier", `3`, `4`},
		{"gobfd", "desired_min_tx_interval", `"300ms"`, `"301ms"`},
		{"gobfd", "required_min_rx_interval", `"300ms"`, `"301ms"`},
		{"gobfd", "remote_min_rx_interval", `"300ms"`, `"301ms"`},
		{"gobfd", "negotiated_tx_interval", `"300ms"`, `"301ms"`},
		{"gobfd", "detection_time", `"900ms"`, `"901ms"`},
		{"frr", "peer", `"10.0.6.1"`, `"10.0.6.99"`},
		{"frr", "status", `"up"`, `"down"`},
		{"frr", "multihop", `false`, `true`},
		{"frr", "receive-interval", `300`, `301`},
		{"frr", "transmit-interval", `300`, `301`},
		{"frr", "remote-receive-interval", `300`, `301`},
		{"frr", "remote-transmit-interval", `300`, `301`},
		{"frr", "detect-multiplier", `3`, `4`},
		{"frr", "remote-detect-multiplier", `3`, `4`},
		{"frr", "detection-timeout", `900`, `901`},
	} {
		t.Run(field.side+"/"+field.name, func(t *testing.T) {
			t.Parallel()
			row := timerGoBFDIPv4Row
			if field.side == "frr" {
				row = timerFRRIPv4Row
			}
			before := `"` + field.name + `":` + field.value + ","
			if strings.Count(row, before) != 1 {
				t.Fatal("independent fixture does not contain exactly one selected field")
			}
			for _, mutation := range []struct{ name, replacement string }{
				{"missing", ""},
				{"null", `"` + field.name + `":null,`},
				{"wrong type", `"` + field.name + `":[],`},
				{"wrong value", `"` + field.name + `":` + field.wrong + ","},
				{"duplicate key", before + before},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					t.Parallel()
					gobfd, frr := "["+timerGoBFDIPv4Row+"]", "["+timerFRRIPv4Row+"]"
					changed := "[" + strings.Replace(row, before, mutation.replacement, 1) + "]"
					if field.side == "frr" {
						frr = changed
					} else {
						gobfd = changed
					}
					peer := vendorPeer{name: "frr", baseName: "frr", peerIP: "10.0.6.2", localIP: "10.0.6.1"}
					if err := validateFRRTimers(gobfd, frr, peer); err == nil {
						t.Fatal("invalid required timer field was accepted")
					}
				})
			}
		})
	}
}

func TestValidateFRRTimersRejectsInvalidDocumentsAndPeerSelection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		gobfd string
		frr   string
	}{
		{"missing GoBFD peer", "[]", "[" + timerFRRIPv4Row + "]"},
		{"missing FRR peer", "[" + timerGoBFDIPv4Row + "]", "[]"},
		{"duplicate GoBFD tuple", "[" + timerGoBFDIPv4Row + "," + timerGoBFDIPv4Row + "]", "[" + timerFRRIPv4Row + "]"},
		{"duplicate FRR peer", "[" + timerGoBFDIPv4Row + "]", "[" + timerFRRIPv4Row + "," + timerFRRIPv4Row + "]"},
		{"GoBFD object not array", timerGoBFDIPv4Row, "[" + timerFRRIPv4Row + "]"},
		{"FRR object not array", "[" + timerGoBFDIPv4Row + "]", timerFRRIPv4Row},
		{"GoBFD null document", "null", "[" + timerFRRIPv4Row + "]"},
		{"FRR null document", "[" + timerGoBFDIPv4Row + "]", "null"},
		{"GoBFD malformed", "[", "[" + timerFRRIPv4Row + "]"},
		{"FRR malformed", "[" + timerGoBFDIPv4Row + "]", "["},
		{"GoBFD trailing document", "[" + timerGoBFDIPv4Row + "]{}", "[" + timerFRRIPv4Row + "]"},
		{"FRR trailing document", "[" + timerGoBFDIPv4Row + "]", "[" + timerFRRIPv4Row + "]{}"},
		{
			"GoBFD invalid UTF8", "[" + strings.Replace(timerGoBFDIPv4Row, "300 marker", "\xff", 1) + "]",
			"[" + timerFRRIPv4Row + "]",
		},
		{
			"FRR invalid UTF8", "[" + timerGoBFDIPv4Row + "]",
			"[" + strings.Replace(timerFRRIPv4Row, "300 marker", "\xff", 1) + "]",
		},
		{
			"FRR local mismatch", "[" + timerGoBFDIPv4Row + "]",
			"[" + strings.Replace(timerFRRIPv4Row, `{`, `{"local":"10.0.6.99",`, 1) + "]",
		},
		{
			"FRR local null", "[" + timerGoBFDIPv4Row + "]",
			"[" + strings.Replace(timerFRRIPv4Row, `{`, `{"local":null,`, 1) + "]",
		},
		{
			"FRR local wrong type", "[" + timerGoBFDIPv4Row + "]",
			"[" + strings.Replace(timerFRRIPv4Row, `{`, `{"local":[],`, 1) + "]",
		},
		{
			"GoBFD malformed duration", "[" + strings.Replace(timerGoBFDIPv4Row, `"300ms"`, `"300"`, 1) + "]",
			"[" + timerFRRIPv4Row + "]",
		},
		{
			"FRR interval numeric string", "[" + timerGoBFDIPv4Row + "]",
			"[" + strings.Replace(timerFRRIPv4Row, `"receive-interval":300`, `"receive-interval":"300"`, 1) + "]",
		},
		{
			"FRR interval fractional", "[" + timerGoBFDIPv4Row + "]",
			"[" + strings.Replace(timerFRRIPv4Row, `"receive-interval":300`, `"receive-interval":300.5`, 1) + "]",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			peer := vendorPeer{name: "frr", baseName: "frr", peerIP: "10.0.6.2", localIP: "10.0.6.1"}
			if err := validateFRRTimers(test.gobfd, test.frr, peer); err == nil {
				t.Fatal("invalid timer document or peer selection was accepted")
			}
		})
	}
}
