//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func TestPeersCommandRejectsInvalidInputsBeforeBinding(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"peers"},
		{"peers", "unknown"},
		{"peers", "fixture"},
		{"peers", "reference", "--sessions=2"},
		{"peers", "fixture", "--sessions=1", "--sessions=2"},
		{"peers", "fixture", "--detect-multiplier=256"},
		{"peers", "fixture", "--source-port-base=65536"},
		{"owner", "fixture", "--cpu-load-percent=101"},
		{"owner", "reference", "--cpu-load-percent=25"},
		{"peers", "fixture", "--session=not-an-address"},
		{"peers", "fixture", "--session=127.31.0.1:3784,127.31.0.1:3784"},
		{"peers", "fixture", "--unknown=true"},
		{"peers", "fixture", "extra"},
	} {
		var output bytes.Buffer
		if err := run(t.Context(), args, &output); !errors.Is(err, bfdload.ErrProfile) {
			t.Fatalf("arguments %v: got %v, want profile rejection", args, err)
		}
		if output.Len() != 0 {
			t.Fatalf("invalid inputs produced a workload report: %v", args)
		}
	}
}

func TestWorkloadCPUArgumentUsesProfileValidation(t *testing.T) {
	t.Parallel()
	cfg, err := parseWorkloadConfig([]string{
		"fixture", "--sessions=1", "--cpu-load-percent=25",
		"--session=127.0.0.1:3784,127.31.0.2:3784",
	})
	if err != nil || cfg.Profile.CPULoadPercent != 25 {
		t.Fatalf("requested CPU duty did not reach the shared profile: %+v: %v", cfg.Profile, err)
	}
}

func TestPeersCommandActualUDPAndPartialJSON(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 31, 0, 1), Port: 37971})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	args := []string{
		"peers", "fixture", "--sessions=1", "--duration=10s", "--source-port-base=55000",
		"--session=" + listener.LocalAddr().String() + ",127.31.0.2:3784",
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, args, &output) }()
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var packet [64]byte
	n, source, readErr := listener.ReadFromUDPAddrPort(packet[:])
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("command cancellation: %v", err)
	}
	if readErr != nil || n != 24 || source.Port() != 55000 {
		t.Fatalf("actual UDP n=%d source=%s: %v", n, source, readErr)
	}
	var report bfdload.PeerObservation
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Scope != "udp-peer-generator" || report.ReferenceQualified || !report.Stopped || report.Completed ||
		len(report.Peers) != 1 || report.Peers[0].Written == 0 || report.Error == "" {
		t.Fatalf("partial generator report: %+v", report)
	}
	writeErr := errors.New("report output unavailable")
	if err := run(ctx, args, failingOutput{writeErr}); !errors.Is(err, writeErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost report output or workload failure: %v", err)
	}
}

func TestOwnerCommandActualUDPAndJSON(t *testing.T) {
	listener, listenErr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 31, 0, 4), Port: 37974})
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	var output bytes.Buffer
	args := []string{
		"owner", "fixture", "--sessions=1", "--duration=1500ms",
		"--session=127.0.0.1:37973,127.31.0.4:37974",
	}
	if err := run(t.Context(), args, &output); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var packet [64]byte
	n, source, readErr := listener.ReadFromUDPAddrPort(packet[:])
	if readErr != nil || n != 24 || source.Port() < 49152 {
		t.Fatalf("owner command did not write real UDP: n=%d source=%s error=%v", n, source, readErr)
	}
	var report bfdload.OwnerWorkloadObservation
	//nolint:musttag // Owner snapshots retain core Observation and DeliveryCounter field names.
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Scope != "production-core-owner-workload" || report.ReferenceQualified || !report.Completed ||
		!report.Final.Stopped || len(report.Final.Sessions) != 1 || report.Final.TimingUncertaintyNS != nil ||
		report.Resources.PeakRSSBytes == 0 || report.Final.Sessions[0].Sent == 0 {
		t.Fatalf("owner JSON lost scope, unknown precision or actual data: %+v", report)
	}
}
