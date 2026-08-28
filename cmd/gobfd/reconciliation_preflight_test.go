package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestCompiledSourceInterfaceDependencies(t *testing.T) {
	t.Parallel()

	candidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{
			{config: bfd.SessionConfig{Interface: "base0"}},
			{config: bfd.SessionConfig{}},
		},
		echo: []echoSessionCandidate{
			{config: bfd.EchoSessionConfig{Interface: "echo0"}},
			{config: bfd.EchoSessionConfig{}},
		},
		microGroups: []bfd.MicroBFDReconcileConfig{
			{Config: bfd.MicroBFDConfig{LAGInterface: "bond0"}},
			{Config: bfd.MicroBFDConfig{}},
		},
		microMembers: []microBFDMemberCandidate{
			{member: "member0"},
			{},
		},
		overlays: [2]compiledOverlayCandidate{
			{desired: []bfd.ReconcileConfig{{Key: "vxlan"}}},
			{desired: []bfd.ReconcileConfig{{Key: "geneve"}}},
		},
	}

	tests := []struct {
		source reconciliationSource
		want   []string
	}{
		{source: sourceBase, want: []string{"base0"}},
		{source: sourceEcho, want: []string{"echo0"}},
		{source: sourceMicroGroup, want: []string{"bond0"}},
		{source: sourceMicroMember, want: []string{"member0"}},
		{source: sourceVXLAN},
		{source: sourceGeneve},
	}
	for _, test := range tests {
		got := compiledSourceInterfaceDependencies(test.source, candidate)
		if !slices.Equal(got, test.want) {
			t.Errorf("%s interface dependencies = %v, want %v", test.source, got, test.want)
		}
	}
}

func TestCompiledSourceInterfacePreflightClassifiesCompleteBatch(t *testing.T) {
	t.Parallel()

	candidate := compiledControlSessionCandidate{base: []baseSessionCandidate{
		{config: bfd.SessionConfig{Interface: "base0"}},
		{config: bfd.SessionConfig{Interface: "base1"}},
	}}
	typed0 := testUnavailableResourceError(t, "base0")
	typed1 := testUnavailableResourceError(t, "base1")
	permanent := errors.New("permanent inventory failure")
	tests := []struct {
		name         string
		pending      int
		err          error
		wantPending  int
		wantFailed   int
		wantInternal bool
	}{
		{
			name: "all unavailable", pending: 2, err: errors.Join(typed0, typed1),
			wantPending: 2,
		},
		{
			name: "permanent", err: permanent,
			wantFailed: 1,
		},
		{
			name: "mixed", pending: 1, err: errors.Join(typed0, permanent),
			wantFailed: 1,
		},
		{
			name: "positive count without error", pending: 1,
			wantFailed: 1, wantInternal: true,
		},
		{
			name: "negative count", pending: -1, err: typed0,
			wantFailed: 1, wantInternal: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			factory := newNthFailureDeclarativeSenderFactory(0)
			checkerCalls := 0
			result := applyCompiledSourceWithInterfaceChecker(
				context.Background(), sourceBase, candidate, mgr, factory,
				slog.New(slog.DiscardHandler),
				func(got []string) (int, error) {
					checkerCalls++
					if !slices.Equal(got, []string{"base0", "base1"}) {
						t.Fatalf("checked interfaces = %v, want complete base set", got)
					}
					return test.pending, test.err
				},
			)
			if checkerCalls != 1 {
				t.Fatalf("batch checker calls = %d, want 1", checkerCalls)
			}
			if result.Pending != test.wantPending || result.Failed != test.wantFailed {
				t.Fatalf("preflight result = %+v, want pending=%d failed=%d",
					result, test.wantPending, test.wantFailed)
			}
			if test.wantFailed != 0 && result.Errors.Count(bfd.ReconcileErrorCreate) != 1 {
				t.Errorf("preflight create failures = %d, want 1",
					result.Errors.Count(bfd.ReconcileErrorCreate))
			}
			if test.wantInternal && !errors.Is(result.Err, errInvalidInterfacePreflightResult) {
				t.Errorf("inconsistent preflight error = %v, want errInvalidInterfacePreflightResult", result.Err)
			}
			if factory.calls != 0 || len(mgr.Sessions()) != 0 {
				t.Errorf("preflight failure mutated source: sender calls=%d sessions=%d",
					factory.calls, len(mgr.Sessions()))
			}
		})
	}
}

func TestBaseSourcePendingPreflightPreservesOldClaimAndEmptyDesiredReleasesIt(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	logger := slog.New(slog.DiscardHandler)
	seedFactory := newNthFailureDeclarativeSenderFactory(0)
	seed := basePreflightTestCandidates(t, "192.0.2.230", "old0")
	seedResult := applyCompiledSourceWithInterfaceChecker(
		context.Background(), sourceBase,
		compiledControlSessionCandidate{base: seed}, mgr, seedFactory, logger,
		func(got []string) (int, error) {
			if !slices.Equal(got, []string{"old0"}) {
				t.Fatalf("seed interfaces = %v, want [old0]", got)
			}
			return 0, nil
		},
	)
	if seedResult.Err != nil || seedResult.Created != 1 {
		t.Fatalf("seed result = %+v, want one created claim", seedResult)
	}
	before := mgr.Sessions()
	if len(before) != 1 {
		t.Fatalf("seed sessions = %d, want 1", len(before))
	}

	newFactory := newNthFailureDeclarativeSenderFactory(0)
	replacement := basePreflightTestCandidates(t, "192.0.2.231", "missing0")
	pendingResult := applyCompiledSourceWithInterfaceChecker(
		context.Background(), sourceBase,
		compiledControlSessionCandidate{base: replacement}, mgr, newFactory, logger,
		func(got []string) (int, error) {
			if !slices.Equal(got, []string{"missing0"}) {
				t.Fatalf("replacement interfaces = %v, want [missing0]", got)
			}
			return 1, testUnavailableResourceError(t, "missing0")
		},
	)
	if pendingResult.Pending != 1 || pendingResult.Failed != 0 {
		t.Fatalf("pending replacement result = %+v, want pending=1", pendingResult)
	}
	if newFactory.calls != 0 {
		t.Fatalf("pending replacement sender calls = %d, want 0", newFactory.calls)
	}
	afterPending := mgr.Sessions()
	if len(afterPending) != 1 || afterPending[0].LocalDiscr != before[0].LocalDiscr ||
		afterPending[0].PeerAddr != before[0].PeerAddr {
		t.Fatalf("old claim changed during pending preflight: before=%+v after=%+v", before, afterPending)
	}

	emptyFactory := newNthFailureDeclarativeSenderFactory(0)
	emptyResult := applyCompiledSourceWithInterfaceChecker(
		context.Background(), sourceBase, compiledControlSessionCandidate{},
		mgr, emptyFactory, logger,
		func([]string) (int, error) {
			t.Fatal("empty desired source invoked interface checker")
			return 0, nil
		},
	)
	if emptyResult.Err != nil || emptyResult.Released != 1 {
		t.Fatalf("empty desired result = %+v, want one released claim", emptyResult)
	}
	if emptyFactory.calls != 0 || len(mgr.Sessions()) != 0 {
		t.Fatalf("empty desired sender calls/sessions = %d/%d, want 0/0",
			emptyFactory.calls, len(mgr.Sessions()))
	}
}

func basePreflightTestCandidates(t *testing.T, peer, ifName string) []baseSessionCandidate {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{
		Peer: peer, Local: "127.0.0.1", Interface: ifName,
	}}
	candidates, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		t.Fatalf("compile base preflight candidate: %v", err)
	}
	return candidates
}
