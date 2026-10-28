//go:build linux

package bfdload

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestSessionChurnCanceledBeforeEpochDoesNotRelease(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.SessionChurn = true
	profile.Duration = 6 * time.Second
	owner, _ := ownedPeerFixture(t, profile, 38211)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	churn, err := startSessionChurn(ctx, owner, profile, time.Now().Add(time.Hour), cancel)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	observed, joinErr := churn.join()
	if !errors.Is(joinErr, context.Canceled) || !observed.Joined || observed.Completed ||
		observed.Created != 0 || observed.Released != 0 || len(observed.Events) != 0 {
		t.Fatalf("canceled future churn mutated membership: %+v: %v", observed, joinErr)
	}
	registered, err := owner.registrationSnapshot()
	if err != nil || registered.KnownPeers != 2 || registered.ActiveMetricRegistrations != 2 {
		t.Fatalf("canceled churn lost registrations: %+v: %v", registered, err)
	}
}

func TestSessionChurnCanceledReleaseAndCloseDoNotResurrect(t *testing.T) {
	owner, profile := churnOwnerWithPeers(t, 38221)
	canceled, cancelOperation := context.WithCancel(t.Context())
	cancelOperation()
	if _, released, releaseErr := owner.releaseForChurn(canceled, 0); released ||
		!errors.Is(releaseErr, context.Canceled) {
		t.Fatalf("pre-canceled release changed inventory: released=%t: %v", released, releaseErr)
	}
	previous, released, err := owner.releaseForChurn(t.Context(), 0)
	if err != nil || !released {
		t.Fatalf("actual final-claim release failed: released=%t: %v", released, err)
	}
	gap, err := owner.Snapshot(t.Context())
	if err != nil || gap.Registrations.KnownPeers != 1 || gap.Registrations.ActiveMetricRegistrations != 1 ||
		gap.Registrations.RetainedSessions != 2 || gap.Registrations.RetainedPacketRows != 8 ||
		gap.RetiredSessions != 1 || gap.RetiredReceived == 0 || len(gap.Sessions) != 1 {
		t.Fatalf("gap retained a stale session or conflated active/history: %+v: %v", gap, err)
	}
	if gap.Sessions[0].LocalDiscriminator == previous.session.LocalDiscriminator() || !gap.Sessions[0].Registered {
		t.Fatal("retired generation appeared as the current session")
	}
	if _, createErr := owner.createForChurn(canceled, 0, profile); !errors.Is(createErr, context.Canceled) {
		t.Fatalf("pre-canceled recreation accepted: %v", createErr)
	}
	start, created, closed := make(chan struct{}), make(chan error, 1), make(chan error, 1)
	type sample struct {
		observation OwnerObservation
		err         error
	}
	snapshots := make(chan sample, 1)
	go func() {
		<-start
		observation, snapshotErr := owner.Snapshot(t.Context())
		snapshots <- sample{observation, snapshotErr}
	}()
	go func() {
		<-start
		_, createErr := owner.createForChurn(t.Context(), 0, profile)
		created <- createErr
	}()
	go func() {
		<-start
		closed <- owner.Close()
	}()
	close(start)
	if createErr := <-created; createErr != nil && !errors.Is(createErr, errOwnerConfig) {
		t.Fatal(createErr)
	}
	if closeErr := <-closed; closeErr != nil {
		t.Fatal(closeErr)
	}
	concurrent := <-snapshots
	registered := 0
	for _, session := range concurrent.observation.Sessions {
		if session.Registered {
			registered++
		}
	}
	if concurrent.err != nil || registered != concurrent.observation.Registrations.KnownPeers {
		t.Fatalf("concurrent snapshot combined different generations: %+v: %v", concurrent.observation, concurrent.err)
	}
	if _, createErr := owner.createForChurn(t.Context(), 0, profile); !errors.Is(createErr, errOwnerConfig) {
		t.Fatalf("session resurrected after Close: %v", createErr)
	}
	final, err := owner.Snapshot(t.Context())
	if err != nil || !final.Stopped || final.Registrations.KnownPeers != 0 ||
		final.Registrations.ActiveMetricRegistrations != 0 ||
		final.Registrations.RetainedSessions != 2 || final.Registrations.RetainedPacketRows != 8 {
		t.Fatalf("Close leaked membership or lost retained series: %+v: %v", final, err)
	}
	for _, session := range final.Sessions {
		if session.Registered {
			t.Fatal("stopped observation claims a live registration")
		}
	}
}

func TestSessionChurnCancellationInsideDrainRetiresWithoutResurrection(t *testing.T) {
	owner, profile := churnOwnerWithPeers(t, 38251)
	owner.inventory.RLock()
	previous := owner.sessions[0]
	owner.inventory.RUnlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		released bool
		err      error
	}
	results := make(chan result, 1)
	go func() {
		_, released, releaseErr := owner.releaseForChurn(ctx, 0)
		results <- result{released, releaseErr}
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for previous.session.State() != bfd.StateAdminDown {
		select {
		case <-deadline.C:
			cancel()
			t.Fatal("real owner did not enter AdminDown during retirement")
		case <-ticker.C:
		}
	}
	cancel()
	retired := <-results
	var incomplete *bfd.DrainIncompleteError
	if !retired.released || !errors.Is(retired.err, context.Canceled) || !errors.As(retired.err, &incomplete) {
		t.Fatalf("interrupted drain lost retirement or its error: %+v", retired)
	}
	if _, createErr := owner.createForChurn(ctx, 0, profile); !errors.Is(createErr, context.Canceled) {
		t.Fatalf("canceled drain resurrected a session: %v", createErr)
	}
	observed, err := owner.Snapshot(t.Context())
	if err != nil || observed.RetiredSessions != 1 || observed.RetiredReceived == 0 ||
		observed.RetiredReceived != previous.session.PacketsReceived() || len(observed.Sessions) != 1 ||
		observed.Registrations.KnownPeers != 1 || observed.Registrations.ActiveMetricRegistrations != 1 {
		t.Fatalf("canceled drain lost frozen counters or membership: %+v: %v", observed, err)
	}
	binding := netip.AddrPortFrom(previous.endpoint.Local.Addr(), previous.sourcePort)
	probe, bindErr := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(binding))
	if bindErr != nil {
		t.Fatalf("canceled drain leaked its real sender socket: %v", bindErr)
	}
	if closeErr := probe.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func churnOwnerWithPeers(t *testing.T, port uint16) (*Owner, Profile) {
	t.Helper()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Duration = 10 * time.Second
	owner, cfg := ownedPeerFixture(t, profile, port)
	ctx, cancel := context.WithCancel(t.Context())
	peers := make(chan error, 1)
	go func() {
		_, runErr := RunPeers(ctx, cfg, slog.New(slog.DiscardHandler))
		peers <- runErr
	}()
	t.Cleanup(func() {
		cancel()
		if peerErr := <-peers; peerErr != nil && !errors.Is(peerErr, context.Canceled) {
			t.Error(peerErr)
		}
	})
	readyCtx, readyCancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer readyCancel()
	for index := range cfg.Sessions {
		if readyErr := awaitChurnRecovery(readyCtx, owner, index, 1); readyErr != nil {
			t.Fatal(readyErr)
		}
	}
	return owner, profile
}

func TestSessionChurnRecoveryDeadlineRetainsFailure(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := ownedPeerFixture(t, profile, 38231)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := awaitChurnRecovery(ctx, owner, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("absent peer falsely recovered: %v", err)
	}
	if ready, err := owner.churnRecovered(t.Context(), 0, 2); ready || !errors.Is(err, errOwnerConfig) {
		t.Fatalf("stale generation falsely recovered: ready=%t: %v", ready, err)
	}
}

func TestSessionChurnDisabledStartsNoWorker(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	churn, err := startSessionChurn(ctx, nil, profile, time.Now(), cancel)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := churn.join()
	if err != nil || observed.Enabled || observed.Joined || !observed.Completed || churn.result != nil {
		t.Fatalf("disabled churn started work: %+v: %v", observed, err)
	}
}

func TestSessionChurnExpiredWindowRejectsMissingActivity(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.SessionChurn, profile.Duration = true, 6*time.Second
	owner, _ := ownedPeerFixture(t, profile, 38241)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	churn, err := startSessionChurn(ctx, owner, profile, time.Now().Add(-profile.Duration), cancel)
	if err != nil {
		t.Fatal(err)
	}
	observed, joinErr := churn.join()
	if !errors.Is(joinErr, errOwnerConfig) || observed.Completed || !observed.Joined ||
		observed.Created != 0 || observed.Recovered != 0 {
		t.Fatalf("unexecuted churn was accepted: %+v: %v", observed, joinErr)
	}
}
