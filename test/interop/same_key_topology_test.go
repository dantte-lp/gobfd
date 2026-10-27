//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
)

func TestSameKeyFRRTestcontainers(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	resources := new(fourPeerResources)
	t.Run("test-owned FRR topology", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
		t.Cleanup(cancel)
		root := fourPeerRepositoryRoot(t)
		buildID := time.Now().UnixNano()
		project := fmt.Sprintf("gobfd-same-key-tc-%d", buildID)
		images := []string{
			fmt.Sprintf("localhost/gobfd-same-key:%d", buildID),
			fmt.Sprintf("localhost/frr-same-key:%d", buildID),
			fmt.Sprintf("localhost/tshark-same-key:%d", buildID),
		}
		ensureFourPeerArtifactDirectory(t)
		ownership, err := json.Marshal(map[string]any{
			"project_name": project, "build_id": buildID, "image_names": images,
			"network_name":    interopNetworkName(project),
			"container_names": []string{"gobfd-interop", "frr-interop", "tshark-interop"},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeTimerArtifact(t, "same-key-ownership", "json", ownership)
		assertFourPeerNamesAvailable(ctx, t, endpoint)
		config := writeTimerArtifact(t, "same-key-startup", "yml", []byte(
			"grpc:\n  addr: 127.0.0.1:50051\nlog:\n  level: debug\n  format: json\nsessions: []\n"))
		gobfdImage := buildFourPeerImage(ctx, t, endpoint, prepareFourPeerGoContext(t, root), images[0], resources)
		frrImage := buildFourPeerImage(ctx, t, endpoint, filepath.Join(root, "test/interop/frr"), images[1], resources)
		tsharkImage := buildFourPeerImage(ctx, t, endpoint, filepath.Join(root, "test/interop/tshark"), images[2], resources)
		networkName, labels := startFourPeerNetwork(ctx, t, project, "same-key-frr", resources)
		gobfd := startFourPeerGoBFD(ctx, t, gobfdImage, config, networkName, labels, resources)
		resources.tshark = startFourPeerTshark(ctx, t, tsharkImage, gobfd, labels, resources)
		startFourPeerFRR(ctx, t, root, frrImage, networkName, labels, resources)
		t.Setenv("INTEROP_PROJECT_NAME", project)
		registerFourPeerArtifactCleanup(ctx, t, func(artifactCtx context.Context) {
			logs, logErr := projectContainerLogs(artifactCtx, "gobfd-interop")
			if logErr != nil {
				t.Errorf("retain same-key daemon logs: %v", logErr)
				return
			}
			writeTimerArtifact(t, "same-key-daemon", "jsonl", []byte(logs))
		}, func(artifactCtx context.Context) {
			captureFourPeerPCAP(artifactCtx, t, resources.tshark)
		})
		runSameKeyFRRScenario(ctx, t)
	})
	for _, id := range resources.containerIDs {
		containertest.AssertContainerRemoved(t, endpoint, id)
	}
	if resources.networkName != "" {
		containertest.AssertNetworkRemoved(t, resources.networkName)
	}
	for _, name := range resources.imageNames {
		containertest.AssertImageRemoved(t, endpoint, name)
	}
}

func runSameKeyFRRScenario(ctx context.Context, t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	requireCaptureFields(ctx, t, sameKeyFrameFields())
	output, err := projectContainerCommand(ctx, "gobfd-interop", "exec", "/bin/gobfdctl",
		"--addr", "unix:///run/gobfd/gobfd.sock", "session", "add", "--peer", frrIP, "--local", gobfdIP,
		"--type", "single-hop", "--tx-interval", "300ms", "--rx-interval", "300ms", "--detect-mult", "3",
		"--auth-type", "simple-password", "--auth-key-id", "7", "--auth-secret", "gobfd-interop",
		"--padded-pdu-size", "128", "--format", "json")
	writeTimerArtifact(t, "same-key-add-old", "txt", []byte(output))
	if err != nil {
		t.Fatalf("add API-only FRR owner: %v", err)
	}
	old, err := waitForSessionState(ctx, frrIP, 15*time.Second, func(state sessionState) bool {
		return state.LocalState == "Up" && state.RemoteState == "Up" && state.RemoteDiscriminator != 0
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFRRUp(ctx, t, 15*time.Second)
	filter := fmt.Sprintf("%s && ip.src == %s && bfd.sta == 3 && bfd.my_discriminator == %d",
		timerTraffic(frrIP), gobfdIP, old.LocalDiscriminator)
	if _, err = waitForFrame(ctx, filter, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	rows, err := tsharkFields(ctx, filter, sameKeyFrameFields(), 0)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := parseSameKeyFrames(rows)
	if err != nil {
		t.Fatal(err)
	}
	input := sameKeyAPIReceipt{
		OldLocal: old.LocalDiscriminator, OldRemote: old.RemoteDiscriminator,
		OldPort: uint16(frames[0].SourcePort), OldUp: true, FRRBefore: "up",
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTimerArtifact(t, "same-key-api-input", "json", raw)
	if err = copyTimerConfig(ctx, "gobfd-interop", path, "/tmp/same-key-api-input.json"); err != nil {
		t.Fatal(err)
	}
	receipt := runSameKeyAPIWithPeerObservation(ctx, t)
	waitFRRUp(ctx, t, 15*time.Second)
	receipt.FRRAfter = "up"
	peer, err := frrVtysh(ctx, "show bfd peers json")
	writeTimerArtifact(t, "same-key-frr-final", "json", []byte(peer))
	if err != nil {
		t.Fatal(err)
	}
	finalFilter := fmt.Sprintf("%s && ip.src == %s && bfd.sta == 3 && bfd.your_discriminator == %d",
		timerTraffic(frrIP), frrIP, receipt.NewLocal)
	if _, err = waitForFrame(ctx, finalFilter, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	wire, err := tsharkFieldStream(ctx, timerTraffic(frrIP), sameKeyFrameFields())
	if err != nil {
		t.Fatal(err)
	}
	writeTimerArtifact(t, "same-key-wire-fields", "tsv", []byte(wire))
	writeTimerArtifact(t, "same-key-wire-columns", "txt", []byte(strings.Join(sameKeyFrameFields(), "\t")+"\n"))
	frames, err = parseSameKeyFrames(parseTsharkFieldRows(wire, 0))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	writeTimerArtifact(t, "same-key-api-final", "json", raw)
	if err = validateSameKeyWire(frames, receipt); err != nil {
		t.Fatal(err)
	}
}

func runSameKeyAPIWithPeerObservation(ctx context.Context, t *testing.T) sameKeyAPIReceipt {
	t.Helper()
	probeCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var output string
	var probeErr error
	var samples []string
	go func() {
		defer close(done)
		output, probeErr = projectContainerCommand(probeCtx, "gobfd-interop", "exec", "/tmp/gobfd-interop.test",
			"-test.v", "-test.count=1", "-test.run=^TestSameKeyAPIProcess$")
	}()
	defer func() {
		cancel()
		<-done
		writeTimerArtifact(t, "same-key-api-process", "txt", []byte(output))
		writeTimerArtifact(t, "same-key-frr-samples", "txt", []byte(strings.Join(samples, "\n")))
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	down := false
	for {
		select {
		case <-done:
			if probeErr != nil {
				t.Fatalf("same-key API child: %v", probeErr)
			}
			for line := range strings.SplitSeq(output, "\n") {
				if _, raw, ok := strings.Cut(line, "SAME_KEY_API="); ok {
					var receipt sameKeyAPIReceipt
					if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
						t.Fatal(err)
					}
					if down {
						receipt.FRRRetiring = "down"
					}
					return receipt
				}
			}
			t.Fatal("same-key API child omitted its receipt")
		case <-ticker.C:
			status, err := frrBFDPeerStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			down = down || status == "down"
			if len(samples) == 256 {
				t.Fatal("same-key FRR observation exceeded 256 samples")
			}
			samples = append(samples, time.Now().UTC().Format(time.RFC3339Nano)+" "+status)
		case <-ctx.Done():
			t.Fatalf("same-key API/peer observation: %v", ctx.Err())
		}
	}
}
