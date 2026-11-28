//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func TestAssessmentInputPreservesV1ProducerDTOs(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, time.October, 27, 1, 2, 3, 4, time.UTC)
	profile, err := bfdload.DefaultProfile("reference")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := bfdload.OwnerEndpoint{
		Local: netip.MustParseAddrPort("192.0.2.1:3784"),
		Peer:  netip.MustParseAddrPort("192.0.2.2:3784"),
	}
	metricName, metricValue := "bfd_test_total", 7.0
	owner := bfdload.OwnerWorkloadObservation{
		Scope: "production-core-owner-workload", StartedAtUTC: started,
		Config: bfdload.PeerConfig{Profile: profile, Sessions: []bfdload.OwnerEndpoint{endpoint}},
		Initial: bfdload.OwnerObservation{
			Sessions: []bfdload.OwnerSessionObservation{{
				Endpoint: endpoint, Observation: bfd.Observation{
					State: bfd.StateUp, RemoteKnown: true, ObservationVersion: 9007199254740993,
				},
			}},
			Delivery: []bfd.DeliveryCounter{{Source: bfd.DeliveryWireParse, Outcome: bfd.DeliveryAccepted, Count: 7}},
			Metrics: []*dto.MetricFamily{{
				Name: &metricName, Metric: []*dto.Metric{{Counter: &dto.Counter{Value: &metricValue}}},
			}},
		},
	}
	//nolint:musttag // Exercise the unchanged v1 producer's capitalized core observation fields.
	ownerJSON, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	var decodedOwner bfdload.OwnerWorkloadObservation
	err = decodeAssessmentReport(t.Context(), bytes.NewReader(ownerJSON), &decodedOwner, int64(len(ownerJSON)))
	if err != nil {
		t.Fatalf("decode actual v1 owner DTO: %v", err)
	}
	if decodedOwner.Config.Profile != profile || !decodedOwner.StartedAtUTC.Equal(started) ||
		decodedOwner.Initial.Sessions[0].Endpoint != endpoint ||
		decodedOwner.Initial.Sessions[0].Observation.ObservationVersion != 9007199254740993 ||
		decodedOwner.Initial.Sessions[0].Observation.State != bfd.StateUp ||
		decodedOwner.Initial.Delivery[0].Count != 7 ||
		decodedOwner.Initial.Metrics[0].GetMetric()[0].GetCounter().GetValue() != 7 {
		t.Fatal("v1 owner duration, time, core, address or protobuf evidence changed")
	}
	peers := bfdload.PeerObservation{
		Scope: "udp-peer-generator", Profile: profile, StartedAtUTC: started,
		Peers: []bfdload.PeerSessionObservation{{Endpoint: endpoint, Written: 9007199254740993}},
	}
	peerJSON, err := json.Marshal(peers)
	if err != nil {
		t.Fatal(err)
	}
	var decodedPeers bfdload.PeerObservation
	err = decodeAssessmentReport(t.Context(), bytes.NewReader(peerJSON), &decodedPeers, int64(len(peerJSON)))
	if err != nil {
		t.Fatalf("decode actual v1 peer DTO: %v", err)
	}
	if decodedPeers.Profile != profile || !decodedPeers.StartedAtUTC.Equal(started) ||
		decodedPeers.Peers[0].Endpoint != endpoint || decodedPeers.Peers[0].Written != 9007199254740993 {
		t.Fatal("v1 peer evidence changed")
	}
}

func TestAssessmentInputRejectsAmbiguousMalformedAndOversizedReports(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		limit int64
		valid bool
	}{
		{name: "one_object_and_whitespace", input: " \n{}\t", valid: true},
		{name: "empty"},
		{name: "whitespace_only", input: " \n\t"},
		{name: "truncated", input: `{"scope":`},
		{name: "duplicate", input: `{"scope":"a","scope":"b"}`},
		{name: "escaped_duplicate", input: `{"scope":"a","\u0073cope":"b"}`},
		{name: "nested_duplicate", input: `{"config":{"profile":{"kind":"a","kind":"b"}}}`},
		{name: "invalid_utf8", input: "{\"scope\":\"\xff\"}"},
		{name: "trailing_object", input: `{} {}`},
		{name: "trailing_garbage", input: `{}x`},
		{name: "unknown_member", input: `{"future_evidence":true}`},
		{name: "wrong_case", input: `{"Scope":"a"}`},
		{name: "nested_unknown", input: `{"config":{"profile":{"future_evidence":true}}}`},
		{name: "core_wrong_case", input: `{"initial":{"sessions":[{"observation":{"state":3}}]}}`},
		{name: "wrong_type", input: `{"completed":"true"}`},
		{name: "overflow", input: `{"elapsed_ns":9223372036854775808}`},
		{name: "exact_limit", input: `{}`, limit: 2, valid: true},
		{name: "oversized_value", input: `{"scope":"long"}`, limit: 4},
		{name: "oversized_whitespace", input: "{} ", limit: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limit := test.limit
			if limit == 0 {
				limit = 1024
			}
			var report bfdload.OwnerWorkloadObservation
			err := decodeAssessmentReport(t.Context(), strings.NewReader(test.input), &report, limit)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, decode error=%v", test.valid, err)
			}
		})
	}
}

func TestAssessmentInputRejectsNonregularAndOversizedFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	regular := filepath.Join(directory, "report.json")
	if err := os.WriteFile(regular, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "link.json")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(directory, "report.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(directory, "oversized.json")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(maxAssessmentReportBytes+1), file.Close()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, symlink, fifo, large, filepath.Join(directory, "missing")} {
		var report bfdload.OwnerWorkloadObservation
		if err := readAssessmentReport(t.Context(), path, &report); err == nil {
			t.Fatalf("accepted nonregular, linked, oversized or missing file %q", path)
		}
	}
	var report bfdload.OwnerWorkloadObservation
	if err := readAssessmentReport(t.Context(), regular, &report); err != nil {
		t.Fatalf("regular report rejected: %v", err)
	}
}

func TestAssessCommandInputFailureAlwaysEmitsIncompleteJSON(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	valid, invalid := filepath.Join(directory, "valid.json"), filepath.Join(directory, "invalid.json")
	if err := os.WriteFile(valid, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(`{"scope":"first","scope":"second"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	longInvalid := filepath.Join(directory, "long-invalid.json")
	if err := os.WriteFile(longInvalid, []byte(`{"`+strings.Repeat("\u754c", 2000)+`":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		nil, {valid}, {valid, valid, valid}, {invalid, valid}, {valid, invalid}, {valid, directory}, {longInvalid, valid},
	} {
		var output bytes.Buffer
		err := runAssess(t.Context(), args, &output)
		if !errors.Is(err, bfdload.ErrAssessment) {
			t.Fatalf("input failure did not return assessment rejection: %v", err)
		}
		var report bfdload.ProfileAssessment
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatalf("input failure omitted structured report: %v", err)
		}
		if report.Status != "incomplete" || !report.Conditional || report.ReferenceQualified ||
			report.TimingUncertaintyNS != nil || !strings.HasPrefix(report.Error, "assessment input:") ||
			len(report.Error) > 1024 {
			t.Fatalf("input failure produced invalid verdict: %+v", report)
		}
	}
}

func TestAssessCommandCancellationAndOutputFailureRemainErrors(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	err := runAssess(ctx, []string{"unused-owner", "unused-peer"}, &output)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, bfdload.ErrAssessment) || !json.Valid(output.Bytes()) {
		t.Fatalf("canceled assessment lost error or JSON report: %v, %s", err, output.Bytes())
	}
	file, err := os.CreateTemp(t.TempDir(), "closed-output")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runAssess(t.Context(), nil, file); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("output error lost: %v", err)
	}
	var report bfdload.OwnerWorkloadObservation
	if err := decodeAssessmentReport(t.Context(), file, &report, 1024); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("input read error lost: %v", err)
	}
}
