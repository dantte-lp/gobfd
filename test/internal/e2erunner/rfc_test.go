package e2erunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func TestRFCArtifactsRequireOwnedContainersAndNonemptyCapture(t *testing.T) {
	modes := []string{
		"rfc healthy", "rfc missing", "rfc foreign",
		"rfc empty capture", "rfc log failure", "rfc canceled",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			root, fixture := t.TempDir(), t.TempDir()
			bin := filepath.Dir(installFakeGo(t))
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if linkErr := os.Symlink(executable, filepath.Join(bin, "podman")); linkErr != nil {
				t.Fatal(linkErr)
			}
			for key, value := range map[string]string{
				"PATH": bin, runtimeFixtureEnv: fixture,
				"GOBFD_RUNTIME_MODE": mode, "GORACE": "atexit_sleep_ms=0",
			} {
				t.Setenv(key, value)
			}
			state := make([]runtimeFixtureContainer, 0, rfcContainerCount)
			for index, name := range []string{
				"gobfd-rfc-interop", "tshark-rfc-interop", "frr-rfc-interop",
				"gobfd-rfc9384-interop", "gobgp-rfc-interop", "frr-rfc-bgp-interop",
				"frr-rfc-unsolicited-interop", "echo-reflector-interop",
			} {
				state = append(state, runtimeFixtureContainer{
					ID:     fmt.Sprintf("%064x", index+1),
					Name:   name,
					Labels: map[string]string{"com.docker.compose.project": interopRFCProject},
				})
			}
			if mode == "rfc missing" {
				state = state[:len(state)-1]
			}
			if mode == "rfc foreign" {
				state[0].Labels["com.docker.compose.project"] = "foreign"
			}
			writeRuntimeFixture(t, fixture, state)
			controller, err := interopproject.NewProject(root, interopRFCProject, "rfc", io.Discard, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			r := runner{root: root, reportDir: root, stdout: io.Discard, stderr: io.Discard}
			if mode == "rfc canceled" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				err = r.runRFCSuite(ctx, controller)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			} else {
				err = r.collectRFCArtifacts(t.Context(), controller)
				if (err != nil) != (mode != "rfc healthy") {
					t.Fatalf("mode=%s error=%v", mode, err)
				}
			}
			pcap, readErr := os.ReadFile(filepath.Join(root, "packets.pcapng"))
			if mode == "rfc healthy" || mode == "rfc canceled" || mode == "rfc log failure" {
				if readErr != nil || !strings.Contains(string(pcap), "capture fixture") {
					t.Fatalf("packet collection did not survive cancellation/log failure: %q %v", pcap, readErr)
				}
			}
			if mode == "rfc foreign" || mode == "rfc missing" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("invalid required identity allowed capture collection")
				}
			}
		})
	}
}
