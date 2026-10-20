//go:build interop_testcontainers

package interop_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func TestBIRDFlowSpecValidation(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	resources := new(fourPeerResources)
	t.Run("CVE-2026-112112", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		t.Cleanup(cancel)
		root := fourPeerRepositoryRoot(t)
		revision, buildDate, err := interopproject.BuildMetadata(ctx, root, os.Getenv("GOBFD_BUILD_REVISION"))
		if err != nil {
			t.Fatalf("resolve BIRD build metadata: %v", err)
		}
		image := buildFourPeerImage(ctx, t, endpoint, filepath.Join(root, "test/interop/bird3"),
			fmt.Sprintf("localhost/bird3-security-test:%d", time.Now().UnixNano()), resources,
			map[string]*string{"VCS_REF": &revision, "BUILD_DATE": &buildDate})
		peer := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
			Image:      image,
			Entrypoint: []string{"bird"},
			Cmd:        []string{"-f", "-c", "/etc/bird/validator.conf", "-s", "/run/bird/validator.ctl"},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: filepath.Join(root, "test/interop/bird3/security/validator.conf"),
					ContainerFilePath: "/etc/bird/validator.conf", FileMode: 0o644},
				{HostFilePath: filepath.Join(root, "test/interop/bird3/security/originator.conf"),
					ContainerFilePath: "/etc/bird/originator.conf", FileMode: 0o644},
			},
			HostConfigModifier: func(config *container.HostConfig) { config.NetworkMode = "none" },
			WaitingFor: wait.ForExec([]string{"birdc", "-s", "/run/bird/validator.ctl", "show", "status"}).
				WithStartupTimeout(15 * time.Second),
		})
		resources.containerIDs = append(resources.containerIDs, peer.GetContainerID())
		code, output, err := containertest.Exec(ctx, peer, []string{
			"bird", "-c", "/etc/bird/originator.conf", "-s", "/run/bird/originator.ctl", "-P", "/run/bird/originator.pid",
		})
		if err != nil || code != 0 {
			t.Fatalf("start FlowSpec originator: exit=%d error=%v\n%s", code, err, output)
		}
		pollCtx, pollCancel := context.WithTimeout(ctx, 30*time.Second)
		defer pollCancel()
		output, err = pollUntil(pollCtx, 100*time.Millisecond, func(ctx context.Context) (string, bool, error) {
			code, routes, execErr := containertest.Exec(ctx, peer, []string{
				"birdc", "-s", "/run/bird/validator.ctl", "show", "route", "table", "flows", "all",
			})
			return routes, code == 0 && strings.Count(routes, "\tbgp_path:") == 3, execErr
		})
		t.Logf("validator routes:\n%s", output)
		if err != nil {
			t.Fatalf("receive all three iBGP FlowSpec vectors: %v", err)
		}
		// RFC 8956 section 5 requires a destination prefix with offset zero.
		// BIRD prints the offset prefix without its first offset bits.
		for network, want := range map[string]string{
			"flow6 { dst 2001:db8:1::/64; }":       "",
			"flow6 { dst 1:db8:2::/64 offset 8; }": "invalid",
			"flow6 { src 2001:db8:3::/64; }":       "invalid",
		} {
			if got := flowSpecValidity(t, output, network); got != want {
				t.Errorf("%s validity = %q, want %q", network, got, want)
			}
		}
	})
	for _, id := range resources.containerIDs {
		containertest.AssertContainerRemoved(t, endpoint, id)
	}
	for _, image := range resources.imageNames {
		containertest.AssertImageRemoved(t, endpoint, image)
	}
}

// flowSpecValidity returns the BIRD validity column of the one route for network.
func flowSpecValidity(t *testing.T, routes, network string) string {
	t.Helper()

	var found []string
	for line := range strings.Lines(routes) {
		rest, ok := strings.CutPrefix(line, network+" ")
		if !ok {
			continue
		}
		validity, _, ok := strings.Cut(rest, " [originator ")
		if !ok {
			t.Fatalf("route line lacks originator: %q", line)
		}
		found = append(found, strings.TrimSpace(validity))
	}
	if len(found) != 1 {
		t.Fatalf("%s routes = %d, want 1", network, len(found))
	}
	return found[0]
}
