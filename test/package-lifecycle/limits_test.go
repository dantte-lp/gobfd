//go:build package_lifecycle_testcontainers

package packagelifecycle_test

import (
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestSystemdBuildBudget(t *testing.T) {
	t.Parallel()
	request := systemdImageRequest("/fixture", "fixture", nil)
	var options client.ImageBuildOptions
	request.BuildOptionsModifier(&options)
	if options.CPUPeriod != 100000 || options.CPUQuota != 200000 ||
		options.Memory != 2<<30 || options.MemorySwap != options.Memory {
		t.Fatalf("unbounded outbound build: CPU %d/%d memory %d swap %d",
			options.CPUQuota, options.CPUPeriod, options.Memory, options.MemorySwap)
	}
	if options.NetworkMode != "host" {
		t.Fatal("package mirror resolver configuration lost")
	}
}

func TestSystemdRuntimeBudget(t *testing.T) {
	t.Parallel()
	request := systemdContainerRequest("fixture", "/packages")
	var config container.HostConfig
	request.HostConfigModifier(&config)
	if config.CPUPeriod != 100000 || config.CPUQuota != 100000 ||
		config.Memory != containerMemory || config.MemorySwap != config.Memory ||
		config.PidsLimit == nil || *config.PidsLimit != containerPIDs {
		t.Fatalf("unbounded outbound runtime: CPU %d/%d memory %d swap %d PIDs %v",
			config.CPUQuota, config.CPUPeriod, config.Memory, config.MemorySwap, config.PidsLimit)
	}
}

func TestSystemdRuntimeNamespace(t *testing.T) {
	t.Parallel()
	request := systemdContainerRequest("fixture", "/packages")
	var config container.HostConfig
	request.HostConfigModifier(&config)
	if config.CgroupnsMode != container.CgroupnsModePrivate {
		t.Fatalf("systemd fixture cgroup namespace = %q, want private", config.CgroupnsMode)
	}
}
