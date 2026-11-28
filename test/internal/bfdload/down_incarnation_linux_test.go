//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestOwnerDownWitnessRetainsWorkloadIncarnation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		churn bool
	}{{"initial", false}, {"after_churn", true}} {
		t.Run(tc.name, func(t *testing.T) {
			testOwnerDownIncarnation(t, tc.churn)
		})
	}
}

func testOwnerDownIncarnation(t *testing.T, churn bool) {
	t.Helper()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, 4*time.Second
	profile.BlackholeEvery, profile.BlackholeFor = 2*time.Second, 1200*time.Millisecond
	wantGeneration := uint64(1)
	if churn {
		profile.Duration, profile.BlackholeEvery, profile.SessionChurn = 9*time.Second, 6*time.Second, true
		wantGeneration = 2
	}
	owner, peers := runControlledWorkload(t, profile, 0)
	if len(peers.Peers) != 1 || peers.Peers[0].DownTransitions == 0 || peers.Peers[0].UpTransitions < 2 ||
		peers.Peers[0].BlackholeOmitted == 0 || len(owner.DownWitnesses) == 0 ||
		!owner.Final.Stopped || !owner.DownJournal.Complete || !owner.DownJournal.IncarnationsComplete ||
		owner.DownJournal.IncarnationError != "" {
		t.Fatalf("fixture did not retain actual joined UDP Down/recovery: owner=%+v peers=%+v",
			owner.DownJournal, peers.Peers)
	}
	if len(owner.Initial.Sessions) != 1 || owner.Initial.Sessions[0].Generation != 1 ||
		len(owner.MeasurementFinal.Sessions) != 1 || owner.MeasurementFinal.Sessions[0].Generation != wantGeneration {
		t.Fatal("initial owner generation is unavailable")
	}
	encoded, err := json.Marshal(owner.DownWitnesses)
	if err != nil {
		t.Fatal(err)
	}
	var witnesses []downWitnessJSON
	if err := json.Unmarshal(encoded, &witnesses); err != nil {
		t.Fatal(err)
	}
	for _, witness := range witnesses {
		identity := witness.Incarnation
		if !identity.Known || identity.Slot != 0 || identity.Generation != wantGeneration ||
			identity.Endpoint != owner.Config.Sessions[0] {
			t.Fatalf("actual owner Down has no exact workload incarnation: discriminator=%d identity=%+v want generation=%d",
				witness.LocalDiscriminator, identity, wantGeneration)
		}
	}
}

func incarnationEntry(t *testing.T, discr uint32) ownerSession {
	t.Helper()
	endpoint := OwnerEndpoint{
		Local: netip.MustParseAddrPort("192.0.2.1:3784"), Peer: netip.MustParseAddrPort("192.0.2.2:3784"),
	}
	session, err := bfd.NewSession(bfd.SessionConfig{
		LocalAddr: endpoint.Local.Addr(), PeerAddr: endpoint.Peer.Addr(),
		Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
		DesiredMinTxInterval: 100 * time.Millisecond, RequiredMinRxInterval: 100 * time.Millisecond, DetectMultiplier: 3,
	}, discr, journalSender{}, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return ownerSession{endpoint: endpoint, session: session, generation: 1}
}

func incarnationChange(entry ownerSession) bfd.StateChange {
	ref := entry.session.Ref()
	return bfd.StateChange{
		LocalDiscr: entry.session.LocalDiscriminator(),
		LocalAddr:  entry.endpoint.Local.Addr(), PeerAddr: entry.endpoint.Peer.Addr(),
		Interface: entry.endpoint.Interface, Type: bfd.SessionTypeSingleHop, Ref: ref, ObservationVersion: 7,
		OldState: bfd.StateUp, NewState: bfd.StateDown,
		Status: bfd.SessionSnapshot{
			LocalDiscr: entry.session.LocalDiscriminator(),
			LocalAddr:  entry.endpoint.Local.Addr(), PeerAddr: entry.endpoint.Peer.Addr(),
			Interface: entry.endpoint.Interface, Type: bfd.SessionTypeSingleHop,
			Ref: ref, ObservationVersion: 7, State: bfd.StateDown,
		},
	}
}

func TestDownIncarnationDiscriminatorReuseRetainsFrozenGeneration(t *testing.T) {
	t.Parallel()
	old, replacement := incarnationEntry(t, 42), incarnationEntry(t, 42)
	tracker := newDownIncarnations([]ownerSession{old})
	change := incarnationChange(old)
	// A repeated Added is the same physical registration, not a new generation.
	added := change
	added.Added = true
	tracker.observe(added)
	first := tracker.observe(change)
	if !first.Known || first.Slot != 0 || first.Generation != 1 || first.Endpoint != old.endpoint {
		t.Fatalf("delayed frozen Down lost original generation: %+v", first)
	}
	deleted := change
	deleted.Deleted = true
	tracker.observe(deleted)
	added = incarnationChange(replacement)
	added.Added = true
	tracker.observe(added)
	second := tracker.observe(incarnationChange(replacement))
	if !second.Known || second.Generation != 2 || second.Endpoint != first.Endpoint || first.Generation != 1 {
		t.Fatalf("same discriminator borrowed retired identity: old=%+v new=%+v", first, second)
	}
	stale := tracker.observe(change)
	if stale.Known || stale.Error == "" || tracker.err == "" {
		t.Fatalf("retired physical source was accepted: %+v", stale)
	}
	if current := tracker.observe(incarnationChange(replacement)); current.Known {
		t.Fatalf("invalid history silently repaired from later observation: %+v", current)
	}
}

func TestDownIncarnationRejectsUnprovenHistory(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{
		"missing_seed", "zero_generation", "missing_ref", "status_ref", "version", "zero_version", "discriminator",
		"tuple", "status_tuple", "interface", "type", "gap", "unmatched_delete", "replacement_without_delete", "overflow",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			entry := incarnationEntry(t, 42)
			entries := []ownerSession{entry}
			if mode == "missing_seed" {
				entries = nil
			}
			if mode == "zero_generation" {
				entries[0].generation = 0
			}
			if mode == "overflow" {
				entries[0].generation = math.MaxUint64
			}
			tracker := newDownIncarnations(entries)
			change := incarnationChange(entry)
			switch mode {
			case "missing_seed", "zero_generation":
			case "missing_ref":
				change.Ref = bfd.SessionRef{}
			case "status_ref":
				change.Status.Ref = incarnationEntry(t, 43).session.Ref()
			case "version":
				change.Status.ObservationVersion++
			case "zero_version":
				change.ObservationVersion, change.Status.ObservationVersion = 0, 0
			case "discriminator":
				change.LocalDiscr++
			case "tuple":
				change.PeerAddr = netip.MustParseAddr("192.0.2.3")
			case "status_tuple":
				change.Status.LocalAddr = netip.MustParseAddr("192.0.2.3")
			case "interface":
				change.Interface = "eth1"
			case "type":
				change.Type = bfd.SessionTypeMultiHop
			case "gap":
				change = bfd.StateChange{Gap: true}
			case "unmatched_delete":
				change = incarnationChange(incarnationEntry(t, 42))
				change.Deleted = true
			case "replacement_without_delete":
				change = incarnationChange(incarnationEntry(t, 42))
				change.Added = true
			case "overflow":
				deleted := change
				deleted.Deleted = true
				tracker.observe(deleted)
				change = incarnationChange(incarnationEntry(t, 42))
				change.Added = true
			}
			got := tracker.observe(change)
			if got.Known || got.Error == "" || tracker.err == "" {
				t.Fatalf("unproven history accepted (%s): %+v", mode, got)
			}
			if got = tracker.observe(incarnationChange(entry)); got.Known || got.Error == "" {
				t.Fatalf("later sample repaired invalid history (%s): %+v", mode, got)
			}
		})
	}
}

func TestDownIncarnationReleasesRetiredAndJoinedReferences(t *testing.T) {
	t.Parallel()
	entry := incarnationEntry(t, 42)
	tracker := newDownIncarnations([]ownerSession{entry})
	for generation := uint64(1); generation <= 128; generation++ {
		change := incarnationChange(entry)
		got := tracker.observe(change)
		if !got.Known || got.Generation != generation {
			t.Fatalf("lost generation %d: %+v", generation, got)
		}
		change.Deleted = true
		tracker.observe(change)
		if len(tracker.slots) != 1 || len(tracker.byEndpoint) != 1 || tracker.slots[0].ref != (bfd.SessionRef{}) {
			t.Fatal("retired physical source remains retained or manifest grows")
		}
		entry = incarnationEntry(t, 42)
		change = incarnationChange(entry)
		change.Added = true
		tracker.observe(change)
	}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		changes := make(chan bfd.StateChange, 1)
		changes <- incarnationChange(entry)
		close(changes)
		if canceled {
			tracker = newDownIncarnations([]ownerSession{entry})
			cancel()
		}
		result := collectDownJournal(ctx, changes, tracker)
		cancel()
		if tracker.slots[0].ref != (bfd.SessionRef{}) {
			t.Fatal("joined journal retained physical source")
		}
		if !canceled && (!result.incarnationsComplete || len(result.events) != 1 ||
			result.events[0].witness.Incarnation.Generation != 129) {
			t.Fatalf("normal close lost scalar incarnation: %+v", result)
		}
		if canceled && (result.incarnationsComplete || !errors.Is(result.err, context.Canceled) ||
			result.incarnationError == "") {
			t.Fatalf("canceled journal claimed complete identities: %+v", result)
		}
	}
}
