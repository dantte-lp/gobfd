//go:build e2e_haproxy_testcontainers

package haproxy_health_test

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
)

func TestHAProxyBoundedImageBuild(t *testing.T) {
	directory := t.TempDir()
	source := []byte("FROM " + containertest.DebianImage + "\n")
	if err := os.WriteFile(filepath.Join(directory, "Containerfile"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	topology := &haproxyHealthTopology{
		revision: strings.Repeat("a", 40), buildDate: "2026-10-28T00:00:00Z", runID: "fixture-run",
	}
	options, err := topology.imageBuildRequest(directory, "localhost/fixture", "bounded").BuildOptions()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := options.Context.(io.Closer); ok {
		t.Cleanup(func() {
			if err := closer.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := io.Copy(io.Discard, options.Context); err != nil {
		t.Fatal(err)
	}
	if options.CPUPeriod != 100000 || options.CPUQuota != 200000 ||
		options.Memory != 2147483648 || options.MemorySwap != 2147483648 {
		t.Fatalf("outbound build limits: CPU %d/%d, memory %d, memory+swap %d",
			options.CPUQuota, options.CPUPeriod, options.Memory, options.MemorySwap)
	}
	for name, want := range map[string]string{
		"VCS_REF": strings.Repeat("a", 40), "BUILD_DATE": "2026-10-28T00:00:00Z",
	} {
		if got := options.BuildArgs[name]; got == nil || *got != want {
			t.Errorf("build argument %s=%v, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"io.gobfd.test": "haproxy-health-testcontainers", "io.gobfd.run": "fixture-run",
		"io.gobfd.image": "localhost/fixture:bounded",
	} {
		if got := options.Labels[name]; got != want {
			t.Errorf("image ownership %s=%q, want %q", name, got, want)
		}
	}
}

func TestHAProxyBoundedContainerRequest(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		name := "without role modifier"
		if preserve {
			name = "preserves namespace and capabilities"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			request := testcontainers.ContainerRequest{}
			if preserve {
				request.HostConfigModifier = func(config *container.HostConfig) {
					calls++
					config.NetworkMode = "container:owned-monitor"
					config.CapAdd = []string{"NET_RAW", "NET_ADMIN"}
				}
			}
			request = haproxyContainerRequest(request)
			if request.HostConfigModifier == nil {
				t.Fatal("outbound runtime request has no resource modifier")
			}
			config := &container.HostConfig{}
			request.HostConfigModifier(config)
			if config.CPUPeriod != 100000 || config.CPUQuota != 100000 || config.Memory != 268435456 ||
				config.MemorySwap != 268435456 || config.PidsLimit == nil || *config.PidsLimit != 128 {
				t.Fatalf("outbound runtime limits: CPU %d/%d, memory %d, memory+swap %d, PIDs %v",
					config.CPUQuota, config.CPUPeriod, config.Memory, config.MemorySwap, config.PidsLimit)
			}
			if preserve && (calls != 1 || config.NetworkMode != "container:owned-monitor" ||
				!slices.Equal(config.CapAdd, []string{"NET_RAW", "NET_ADMIN"})) {
				t.Fatalf("role configuration lost: calls=%d config=%+v", calls, config)
			}
		})
	}
}

func TestHAProxyRuntimeResourceInspection(t *testing.T) {
	if err := validateHAProxyResources("fixture", nil); err == nil {
		t.Fatal("missing host configuration accepted")
	}
	for name, alter := range map[string]func(*container.HostConfig){
		"valid":        nil,
		"cpu period":   func(c *container.HostConfig) { c.CPUPeriod++ },
		"cpu quota":    func(c *container.HostConfig) { c.CPUQuota++ },
		"memory":       func(c *container.HostConfig) { c.Memory++ },
		"swap":         func(c *container.HostConfig) { c.MemorySwap++ },
		"pids":         func(c *container.HostConfig) { c.PidsLimit = new(int64(129)) },
		"missing pids": func(c *container.HostConfig) { c.PidsLimit = nil },
	} {
		t.Run(name, func(t *testing.T) {
			config := &container.HostConfig{
				CPUPeriod: 100000, CPUQuota: 100000, Memory: 268435456, MemorySwap: 268435456, PidsLimit: new(int64(128)),
			}
			if alter != nil {
				alter(config)
			}
			err := validateHAProxyResources("fixture", config)
			if (err != nil) != (alter != nil) {
				t.Fatalf("runtime inspection error=%v, want rejection=%t", err, alter != nil)
			}
		})
	}
}
