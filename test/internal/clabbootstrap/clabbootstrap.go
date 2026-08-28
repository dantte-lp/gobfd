// Package clabbootstrap implements repository-owned containerlab bootstrap and
// vendor image preparation without a shell or Python runtime boundary.
package clabbootstrap

import (
	"context"
	"errors"
	"log/slog"
)

// ErrBootstrapFailed identifies one or more failed bootstrap phases.
var ErrBootstrapFailed = errors.New("containerlab bootstrap failed")

const executablePodman = "podman"

const (
	executableContainerlab = "containerlab"
	// ContainerlabVersion is the exact CLI version required by the vendor interoperability harness.
	ContainerlabVersion = "0.79.0"
)

// ImageTags contains the operator-visible vendor image tags.
type ImageTags struct {
	Nokia  string
	Sonic  string
	VyOS   string
	Arista string
	Cisco  string
}

// Options contains the bootstrap inputs owned by the Go command.
type Options struct {
	ProjectRoot string
	Jobs        int
	Tags        ImageTags
	Archives    VendorArchives
	VyOSISO     string
	VyOSVersion string
	SkipBuild   bool
	SkipPull    bool
	Deploy      bool
	Test        bool
	TestOnly    bool
	Down        bool
	DryRun      bool
	Logger      *slog.Logger
}

// DefaultOptions returns the compatibility defaults of the existing bootstrap.
func DefaultOptions(projectRoot string) Options {
	return Options{
		ProjectRoot: projectRoot,
		Jobs:        3,
		VyOSVersion: "latest",
		Tags: ImageTags{
			Nokia:  "25.10.2",
			Sonic:  "latest",
			VyOS:   "latest",
			Arista: "ceos:4.36.0.1F",
			Cisco:  "ios-xr/xrd-control-plane:25.4.1",
		},
	}
}

// VendorArchives contains operator-supplied commercial image archives.
type VendorArchives struct {
	Arista string
	Cisco  string
}

// Command is an external command with an explicit working directory.
type Command struct {
	Executable string
	Arguments  []string
	Directory  string
	DryRun     bool
}

// Result is the bounded result of an external command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner executes a fixed-argv command.
type Runner interface {
	Run(ctx context.Context, command Command) (Result, error)
}
