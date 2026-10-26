//go:build linux && testcontainers

package workload_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/test/internal/bfdload"
	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const (
	fixtureMemory      = 1 << 30
	peerMemory         = 512 << 20
	setupMemory        = 256 << 20
	fixturePIDs        = 512
	fixtureCPUUS       = 100000
	fixtureTimeout     = 90 * time.Second
	failureJoinTimeout = 5 * time.Second
	workloadPath       = "/usr/local/bin/bfdload"
	fixtureContainers  = 4
	baseArgumentCount  = 4
)

func TestUDPWorkloadOwnedTopology(t *testing.T) {
	testUDPWorkloadOwnedTopology(t, false)
}

func TestUDPWorkloadConditionalTXOnlyBlackhole(t *testing.T) {
	testUDPWorkloadOwnedTopology(t, true)
}

func testUDPWorkloadOwnedTopology(t *testing.T, txOnly bool) {
	t.Helper()
	inputs := loadWorkloadInputs(t)
	endpoint := containertest.RequirePodman(t)
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), fixtureTimeout)
	defer cancel()
	profile, err := bfdload.DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.CPULoadPercent = 25
	profile.GCPressure = true
	profile.SchedulerPressure = true
	profile.SessionChurn = true
	profile.LossPermille = 25
	profile.Duration = 13 * time.Second
	profile.BlackholeEvery, profile.BlackholeFor = 9*time.Second, 1200*time.Millisecond
	profile.TXOnlyBlackhole = txOnly
	if txOnly {
		profile.CPULoadPercent, profile.GCPressure, profile.SchedulerPressure, profile.SessionChurn = 0, false, false, false
		profile.Duration, profile.BlackholeEvery = 6*time.Second, 3*time.Second
		profile.LossPermille = 0
	}
	cfg, err := bfdload.LoopbackConfig(profile)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlDirectory = "/control"
	control, err := bfdload.OpenControl(inputs.controlDirectory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := control.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	hostID, err := bfdload.NetworkNamespaceID()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, fixtureContainers)
	t.Cleanup(func() {
		for _, id := range ids {
			containertest.AssertContainerRemoved(t, endpoint, id)
		}
		inputs.save(t, "removed.json", ids)
	})
	owner := startWorkloadContainer(ctx, t, inputs, "owner", "none", fixtureMemory,
		[]string{"NET_RAW"}, []string{"sleep", "infinity"}, wait.ForExec([]string{workloadPath, "environment"}))
	ids = append(ids, owner.GetContainerID())
	ownerInspection, err := owner.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inputs.save(t, "owner-inspect.json", ownerInspection)
	inputs.save(t, "owner-effective-caps.json",
		assertWorkloadContainer(ctx, t, client, ownerInspection, fixtureMemory, "NET_RAW", inputs.controlDirectory))
	if ownerInspection.HostConfig.NetworkMode != "none" || ownerInspection.HostConfig.Privileged ||
		ownerInspection.HostConfig.Memory != fixtureMemory || ownerInspection.HostConfig.MemorySwap != fixtureMemory {
		t.Fatal("owner isolation or resource limits differ from the bounded fixture")
	}
	mode := "container:" + owner.GetContainerID()
	args := workloadArguments(cfg)
	var namespace uint64
	for index := range 2 {
		command := append([]string{workloadPath, "prepare", strconv.FormatUint(hostID, 10)}, args...)
		setup := startWorkloadContainer(ctx, t, inputs, fmt.Sprintf("setup-%d", index), mode, setupMemory,
			[]string{"NET_ADMIN"}, command, wait.ForExit().WithExitTimeout(30*time.Second))
		ids = append(ids, setup.GetContainerID())
		logs, logErr := client.Logs(ctx, setup.GetContainerID(), 100)
		if logErr != nil {
			t.Fatal(logErr)
		}
		inputs.write(t, fmt.Sprintf("setup-%d.log", index), []byte(logs))
		inspection, inspectErr := setup.Inspect(ctx)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		inputs.save(t, fmt.Sprintf("setup-%d-effective-caps.json", index),
			assertWorkloadContainer(ctx, t, client, inspection, setupMemory, "NET_ADMIN", inputs.controlDirectory))
		if inspection.HostConfig.NetworkMode != container.NetworkMode(mode) {
			t.Fatal("initializer did not join the owned network namespace")
		}
		if inspection.State.ExitCode != 0 {
			t.Fatalf("isolated topology setup exit=%d: %s", inspection.State.ExitCode, logs)
		}
		var observed bfdload.TopologyObservation
		if decodeErr := json.Unmarshal([]byte(logs), &observed); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if !observed.Completed || observed.NamespaceID == hostID || observed.HostID != hostID {
			t.Fatalf("setup did not prove a private namespace: %+v", observed)
		}
		if index == 0 {
			namespace = observed.NamespaceID
			if observed.Assigned != 4 || observed.Existing != 0 {
				t.Fatalf("mixed topology was not assigned: %+v", observed)
			}
		} else if observed.NamespaceID != namespace || observed.Assigned != 0 || observed.Existing != 4 {
			t.Fatalf("repeated setup mutated or changed the namespace: %+v", observed)
		}
	}
	peer := startWorkloadContainer(ctx, t, inputs, "peers", mode, peerMemory,
		[]string{"NET_RAW"}, []string{"sleep", "infinity"}, wait.ForExec([]string{workloadPath, "environment"}))
	ids = append(ids, peer.GetContainerID())
	peerInspection, err := peer.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inputs.save(t, "peer-inspect.json", peerInspection)
	inputs.save(t, "peer-effective-caps.json",
		assertWorkloadContainer(ctx, t, client, peerInspection, peerMemory, "NET_RAW", inputs.controlDirectory))
	if peerInspection.HostConfig.NetworkMode != container.NetworkMode(mode) ||
		peerInspection.State.Pid == ownerInspection.State.Pid || peerInspection.HostConfig.Memory != peerMemory {
		t.Fatal("peer process does not have the requested namespace or independent bounded container")
	}
	type execution struct {
		result podmanapi.ExecResult
		err    error
	}
	done := make(chan execution, 1)
	go func() {
		result, executeErr := client.Exec(ctx, owner.GetContainerID(), append([]string{workloadPath, "owner"}, args...))
		done <- execution{result, executeErr}
	}()
	peerDone := make(chan execution, 1)
	go func() {
		result, executeErr := client.Exec(ctx, peer.GetContainerID(), append([]string{workloadPath, "peers"}, args...))
		peerDone <- execution{result, executeErr}
	}()
	phase, beginErr := control.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if waitErr := control.WaitMeasured(ctx); waitErr != nil {
		t.Fatal(waitErr)
	}
	if stopErr := control.StopPeers(); stopErr != nil {
		t.Fatal(stopErr)
	}
	peerExecution := <-peerDone
	peerResult, peerErr := peerExecution.result, peerExecution.err
	inputs.write(t, "peer.stdout.json", []byte(peerResult.Stdout))
	inputs.write(t, "peer.stderr.log", []byte(peerResult.Stderr))
	var peerReport bfdload.PeerObservation
	if decodeErr := json.Unmarshal([]byte(peerResult.Stdout), &peerReport); decodeErr != nil {
		cancel()
		select {
		case failedOwner := <-done:
			inputs.write(t, "owner.stdout.json", []byte(failedOwner.result.Stdout))
			inputs.write(t, "owner.stderr.log", []byte(failedOwner.result.Stderr))
			inputs.write(t, "owner-exec-error.txt", []byte(fmt.Sprint(failedOwner.err)))
		case <-time.After(failureJoinTimeout):
			inputs.write(t, "owner-exec-error.txt", []byte("owner execution did not return after bounded cancellation"))
		}
		t.Fatal(decodeErr)
	}
	var written uint64
	for _, observed := range peerReport.Peers {
		written += observed.Written
	}
	if stopErr := control.StopOwner(written); stopErr != nil {
		t.Fatal(stopErr)
	}
	ownerResult := <-done
	inputs.write(t, "owner.stdout.json", []byte(ownerResult.result.Stdout))
	inputs.write(t, "owner.stderr.log", []byte(ownerResult.result.Stderr))
	if peerErr != nil || ownerResult.err != nil || peerResult.ExitCode != 0 || ownerResult.result.ExitCode != 0 {
		t.Fatalf("actual process workload failed: owner=%v peer=%v", ownerResult.err, peerErr)
	}
	var ownerReport bfdload.OwnerWorkloadObservation
	//nolint:musttag // The core report deliberately retains Observation and DeliveryCounter names.
	if decodeErr := json.Unmarshal([]byte(ownerResult.result.Stdout), &ownerReport); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if ownerReport.Phase == nil || peerReport.Phase == nil ||
		ownerReport.Phase.NativeStartNS != phase.NativeStartNS || peerReport.Phase.NativeStartNS != phase.NativeStartNS ||
		ownerReport.Phase.TimeNamespaceID != phase.TimeNamespaceID ||
		peerReport.Phase.TimeNamespaceID != phase.TimeNamespaceID ||
		ownerReport.Phase.Run != phase.Run || peerReport.Phase.Run != phase.Run {
		t.Fatal("processes did not use the common phase")
	}
	if ownerReport.Config.Profile != profile || peerReport.Profile != profile {
		t.Fatal("actual processes did not retain the controller's exact requested profile")
	}
	if ownerReport.MeasurementFinal.Scope == "" ||
		len(ownerReport.MeasurementFinal.Sessions) != len(cfg.Sessions) || ownerReport.MeasurementFinal.Stopped {
		t.Fatal("owner measurement snapshot is missing or includes shutdown")
	}
	if !txOnly {
		pressure := ownerReport.CPUPressure
		if !pressure.Completed || pressure.RequestedPercent != profile.CPULoadPercent || pressure.Workers != 1 ||
			pressure.CPUTimeNS <= 0 || pressure.WorkerWallTimeNS <= 0 || pressure.Windows == 0 ||
			!pressure.ScheduledAtUTC.Equal(ownerReport.StartedAtUTC) {
			t.Fatalf("owner process has no actual phase-coordinated CPU receipt: %+v", pressure)
		}
		allocation := ownerReport.GCPressure
		if !allocation.Enabled || !allocation.Completed || allocation.Workers != 1 || allocation.AllocatedBytes == 0 ||
			allocation.Objects == 0 || allocation.ReleasedObjects == 0 || allocation.PeakRetainedObjects > 256 ||
			!allocation.ScheduledAtUTC.Equal(ownerReport.StartedAtUTC) || !peerReport.Profile.GCPressure ||
			ownerReport.Initial.Environment.Runtime.GCPercent != 100 ||
			ownerReport.MeasurementFinal.Environment.Runtime.GCCycles <= ownerReport.Initial.Environment.Runtime.GCCycles {
			t.Fatalf("owner process has no actual default-GC allocation receipt: %+v", allocation)
		}
		var gcPauses uint64
		for _, count := range ownerReport.GCPauseWindow.Counts {
			gcPauses += count
		}
		if gcPauses == 0 {
			t.Fatal("independent owner process observed no GC pauses")
		}
		t.Logf("GC allocation receipt: %+v; process_gc_pauses=%d", allocation, gcPauses)
		contention := ownerReport.SchedulerPressure
		if !contention.Enabled || !contention.Completed || contention.Workers != 4 || contention.JoinedWorkers != 4 ||
			contention.Rounds == 0 || contention.Yields != contention.Rounds || !peerReport.Profile.SchedulerPressure ||
			!contention.ScheduledAtUTC.Equal(ownerReport.StartedAtUTC) {
			t.Fatalf("owner process has no joined scheduler activity: %+v", contention)
		}
		var schedulerSamples uint64
		for _, count := range ownerReport.SchedulerWindow.Counts {
			schedulerSamples += count
		}
		if schedulerSamples == 0 {
			t.Fatal("process scheduler histogram has no window observations")
		}
		for _, name := range []string{"gobfd_bfd_timer_delay_seconds", "gobfd_bfd_control_rx_commit_seconds"} {
			initial := workloadHistogramCount(ownerReport.Initial, name)
			final := workloadHistogramCount(ownerReport.MeasurementFinal, name)
			if final <= initial {
				t.Fatalf("no actual %s window samples: before=%d after=%d", name, initial, final)
			}
			t.Logf("workload histogram %s: initial=%d final=%d", name, initial, final)
		}
		t.Logf("scheduler receipt: %+v; process_scheduler_samples=%d", contention, schedulerSamples)
		churn := ownerReport.SessionChurn
		if !churn.Enabled || !churn.Joined || !churn.Completed || churn.Released != 2 || churn.Created != 2 ||
			churn.Recovered != 2 || churn.EventsOmitted != 0 || len(churn.Events) != 2 || !peerReport.Profile.SessionChurn ||
			!churn.ScheduledAtUTC.Equal(ownerReport.StartedAtUTC) {
			t.Fatalf("mixed fixture has no joined actual session churn: %+v", churn)
		}
		for _, event := range churn.Events {
			if event.ReleaseStartedAtUTC.IsZero() || event.ReleasedAtUTC.Before(event.ReleaseStartedAtUTC) ||
				event.CreatedAtUTC.Before(event.ReleasedAtUTC) || event.RecoveredAtUTC.Before(event.CreatedAtUTC) {
				t.Fatalf("churn phase boundaries are missing or reversed: %+v", event)
			}
			if event.Before == nil || event.Absent == nil || event.Restored == nil ||
				event.Before.KnownPeers != 2 || event.Absent.KnownPeers != 1 || event.Restored.KnownPeers != 2 ||
				event.Before.ActiveMetricRegistrations != 2 || event.Absent.ActiveMetricRegistrations != 1 ||
				event.Restored.ActiveMetricRegistrations != 2 || event.Absent.RetainedSessions != 2 ||
				event.Absent.RetainedPacketRows != 8 || event.NewGeneration != event.OldGeneration+1 {
				t.Fatalf("churn conflated registry, active registrations or retained metrics: %+v", event)
			}
		}
		t.Logf("session churn receipt: %+v", churn)
	}
	journal := ownerReport.DownJournal
	if !journal.Settled || !journal.Complete || journal.SourceTransitions == 0 ||
		journal.ObservedBeforeCutoff == 0 || len(ownerReport.DownEvents) == 0 ||
		!journal.Closed || journal.FinalSourceTransitions == nil ||
		*journal.FinalSourceTransitions != journal.ObservedBeforeCutoff+journal.ObservedAfterCutoff ||
		journal.ObservedBeforeCutoff > journal.SourceTransitions ||
		uint64(len(ownerReport.DownEvents)) != journal.ObservedBeforeCutoff ||
		ownerReport.NotificationGaps != 0 || ownerReport.DownEventsOmitted != 0 {
		t.Fatalf("actual blackhole transitions lack a complete source-settled journal: %+v", journal)
	}
	t.Logf("Down journal receipt: %+v", journal)
	classification := bfdload.ClassifyDowns(ownerReport, peerReport)
	inputs.save(t, "down-classification.json", classification)
	if !classification.Complete || classification.Eligible == 0 || !classification.Conditional ||
		classification.ReferenceQualified || classification.TimingUncertaintyNS != nil ||
		classification.Eligible != classification.Intentional+classification.False+classification.Unresolved ||
		(classification.Passing && (classification.False != 0 || classification.Unresolved != 0)) {
		t.Fatalf("independent processes lost their complete conditional causal verdict: %+v", classification)
	}
	if txOnly && classification.Intentional == 0 {
		t.Fatalf("TX-only controlled execution produced no conditionally intentional Down: %+v", classification)
	}
	t.Logf("conditional Down classification: %+v", classification)
	var randomOmissions uint64
	for _, peer := range peerReport.Peers {
		randomOmissions += peer.RandomOmitted + peer.ReceiveRandomOmitted
	}
	if !txOnly && randomOmissions == 0 {
		t.Fatal("bidirectional stress/loss execution observed no actual random omissions")
	}
	if !ownerReport.Completed || !ownerReport.Final.Stopped || ownerReport.ReferenceQualified ||
		!peerReport.Completed || !peerReport.Stopped || peerReport.ReferenceQualified || len(peerReport.Peers) != 2 ||
		len(ownerReport.Final.Sessions) != 2 || ownerReport.Final.TimingUncertaintyNS != nil {
		t.Fatal("fixture lost cleanup, exact mixed session count or qualification boundaries")
	}
	ownerGroup, peerGroup := ownerReport.Initial.Environment.Cgroup, peerReport.Environment.Cgroup
	if ownerGroup == nil || peerGroup == nil || ownerGroup.MemberPath == peerGroup.MemberPath ||
		ownerGroup.MemoryMaxBytes.Value != fixtureMemory || peerGroup.MemoryMaxBytes.Value != peerMemory ||
		ownerGroup.CPUQuotaUS.Value != fixtureCPUUS || peerGroup.CPUQuotaUS.Value != fixtureCPUUS ||
		ownerReport.Initial.Environment.Runtime.GOMAXPROCS != 1 || peerReport.Environment.Runtime.GOMAXPROCS != 1 {
		t.Fatal("actual cgroup or runtime samples do not establish independent bounded processes")
	}
	for index, observed := range peerReport.Peers {
		if observed.Written == 0 || observed.Accepted == 0 || observed.UpTransitions < 2 ||
			ownerReport.Final.Sessions[index].Received == 0 || observed.BlackholeOmitted == 0 ||
			(txOnly && observed.ReceiveBlackholeOmitted != 0) {
			t.Fatalf("no actual independent-process Up/commit for peer %d: %+v", index, observed)
		}
	}
	received := ownerReport.Final.RetiredReceived
	for _, observed := range ownerReport.Final.Sessions {
		received += observed.Received
	}
	var committed uint64
	for _, row := range ownerReport.Final.Delivery {
		if row.Source == bfd.DeliveryPacketOwner && row.Outcome == bfd.DeliveryCommitted {
			committed += row.Count
		}
	}
	if ownerReport.Final.TerminalPackets != written || received != committed ||
		(profile.SessionChurn && ownerReport.Final.RetiredReceived == 0) {
		t.Fatalf("actual lifetime boundary incomplete: written=%d terminal=%d committed=%d sampled=%d",
			written, ownerReport.Final.TerminalPackets, committed, received)
	}
	postHostID, err := bfdload.NetworkNamespaceID()
	if err != nil || postHostID != hostID {
		t.Fatalf("host namespace changed: before=%d after=%d error=%v", hostID, postHostID, err)
	}
}

func assertWorkloadContainer(
	ctx context.Context, t *testing.T, client *podmanapi.Client, observed *container.InspectResponse,
	memory int64, capability, controlDirectory string,
) []string {
	t.Helper()
	config := observed.HostConfig
	if config.Privileged || config.Memory != memory || config.MemorySwap != memory ||
		config.CPUQuota != fixtureCPUUS || config.CPUPeriod != fixtureCPUUS || config.PidsLimit == nil ||
		*config.PidsLimit != fixturePIDs || config.CgroupnsMode != container.CgroupnsModeHost {
		t.Fatal("container resources or namespaces differ from the requested fixture")
	}
	if len(config.CapAdd) != 1 || strings.TrimPrefix(config.CapAdd[0], "CAP_") != capability {
		t.Fatal("container capabilities exceed the required setup or UDP scope")
	}
	if len(observed.Mounts) != 2 {
		t.Fatal("unexpected workload mounts")
	}
	for _, binding := range observed.Mounts {
		switch binding.Destination {
		case "/sys/fs/cgroup":
			if binding.RW {
				t.Fatal("cgroup observation mount is writable")
			}
		case "/control":
			if !binding.RW || binding.Source != controlDirectory {
				t.Fatal("control mount differs from the owned directory")
			}
		default:
			t.Fatal("unexpected workload mount destination")
		}
	}
	effective, err := client.EffectiveCapabilities(ctx, observed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 1 || strings.TrimPrefix(effective[0], "CAP_") != capability {
		t.Fatalf("Podman effective capabilities differ from the requested scope: %v", effective)
	}
	return effective
}

func workloadArguments(cfg bfdload.PeerConfig) []string {
	args := make([]string, 0, baseArgumentCount+len(cfg.Sessions))
	args = append(args,
		cfg.Profile.Kind, "--sessions="+strconv.Itoa(cfg.Profile.Sessions),
		"--duration="+cfg.Profile.Duration.String(), "--interface=lo",
		"--cpu-load-percent="+strconv.FormatUint(cfg.Profile.CPULoadPercent, 10),
		"--gc-pressure="+strconv.FormatBool(cfg.Profile.GCPressure),
		"--scheduler-pressure="+strconv.FormatBool(cfg.Profile.SchedulerPressure),
		"--session-churn="+strconv.FormatBool(cfg.Profile.SessionChurn),
		"--tx-only-blackhole="+strconv.FormatBool(cfg.Profile.TXOnlyBlackhole),
		"--loss-permille="+strconv.FormatUint(cfg.Profile.LossPermille, 10),
		"--blackhole-every="+cfg.Profile.BlackholeEvery.String(),
		"--blackhole-for="+cfg.Profile.BlackholeFor.String(),
	)
	if cfg.ControlDirectory != "" {
		args = append(args, "--control-dir="+cfg.ControlDirectory)
	}
	for _, endpoint := range cfg.Sessions {
		args = append(args, "--session="+endpoint.Local.String()+","+endpoint.Peer.String())
	}
	return args
}

func workloadHistogramCount(observed bfdload.OwnerObservation, name string) uint64 {
	var count uint64
	for _, family := range observed.Metrics {
		if family.GetName() == name {
			for _, metric := range family.GetMetric() {
				count += metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return count
}

func startWorkloadContainer(
	ctx context.Context, t *testing.T, inputs workloadInputs, name, mode string, memory int64,
	caps, command []string, waiting wait.Strategy,
) testcontainers.Container {
	t.Helper()
	created, err := containertest.Run(ctx, t, testcontainers.ContainerRequest{
		Image: containertest.DebianImage, Name: "gobfd-workload-" + inputs.runID + "-" + name,
		Cmd: command, WaitingFor: waiting,
		Env: map[string]string{"GOMAXPROCS": "1", "GOMEMLIMIT": "192MiB"},
		Labels: map[string]string{
			"io.gobfd.test": "udp-workload", "io.gobfd.run": inputs.runID,
			"io.gobfd.binary-sha256": inputs.binarySHA,
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      inputs.binary,
			ContainerFilePath: workloadPath, FileMode: 0o755,
		}},
		HostConfigModifier: func(config *container.HostConfig) {
			config.NetworkMode = container.NetworkMode(mode)
			config.CgroupnsMode = container.CgroupnsModeHost
			config.CgroupParent = "agents.slice"
			config.Memory, config.MemorySwap = memory, memory
			config.CPUPeriod, config.CPUQuota = fixtureCPUUS, fixtureCPUUS
			pids := int64(fixturePIDs)
			config.PidsLimit = &pids
			config.CapDrop, config.CapAdd = []string{"ALL"}, caps
			config.SecurityOpt = []string{"no-new-privileges"}
			config.Binds = []string{"/sys/fs/cgroup:/sys/fs/cgroup:ro"}
			config.Binds = append(config.Binds, inputs.controlDirectory+":/control:rw,z")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}
