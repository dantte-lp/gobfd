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
		peerReport.Phase.TimeNamespaceID != phase.TimeNamespaceID {
		t.Fatal("processes did not use the common phase")
	}
	if ownerReport.MeasurementFinal.Scope == "" ||
		len(ownerReport.MeasurementFinal.Sessions) != len(cfg.Sessions) || ownerReport.MeasurementFinal.Stopped {
		t.Fatal("owner measurement snapshot is missing or includes shutdown")
	}
	pressure := ownerReport.CPUPressure
	if !pressure.Completed || pressure.RequestedPercent != profile.CPULoadPercent || pressure.Workers != 1 ||
		pressure.CPUTimeNS <= 0 || pressure.WorkerWallTimeNS <= 0 || pressure.Windows == 0 ||
		!pressure.ScheduledAtUTC.Equal(ownerReport.StartedAtUTC) {
		t.Fatalf("owner process has no actual phase-coordinated CPU receipt: %+v", pressure)
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
		if observed.Written == 0 || observed.Accepted == 0 || observed.UpTransitions == 0 ||
			ownerReport.Final.Sessions[index].Received == 0 {
			t.Fatalf("no actual independent-process Up/commit for peer %d: %+v", index, observed)
		}
	}
	var received uint64
	for _, observed := range ownerReport.Final.Sessions {
		received += observed.Received
	}
	if received != written {
		t.Fatalf("actual process boundary incomplete: written=%d committed=%d", written, received)
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
	)
	if cfg.ControlDirectory != "" {
		args = append(args, "--control-dir="+cfg.ControlDirectory)
	}
	for _, endpoint := range cfg.Sessions {
		args = append(args, "--session="+endpoint.Local.String()+","+endpoint.Peer.String())
	}
	return args
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
