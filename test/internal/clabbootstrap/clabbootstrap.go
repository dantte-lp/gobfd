// Package clabbootstrap implements the repository-owned containerlab bootstrap
// orchestration while keeping vendor image preparation behind one narrow
// Python helper boundary.
package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
)

var (
	errUnknownVendorOperation = errors.New("unknown vendor image operation")
	// ErrBootstrapFailed identifies one or more failed bootstrap phases.
	ErrBootstrapFailed = errors.New("containerlab bootstrap failed")
)

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

// VendorOperation identifies one operation retained in Python vendor glue.
type VendorOperation string

const (
	// VendorVyOS prepares the runtime image from a public image or VyOS ISO.
	VendorVyOS VendorOperation = "vyos"
	// VendorArista imports an operator-supplied cEOS archive.
	VendorArista VendorOperation = "arista"
	// VendorCisco imports an operator-supplied XRd archive.
	VendorCisco VendorOperation = "cisco"
)

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

// VendorCommand constructs the sole frozen-Python boundary used by the Go
// bootstrap command.
func VendorCommand(projectRoot string, operation VendorOperation, arguments ...string) (Command, error) {
	switch operation {
	case VendorVyOS, VendorArista, VendorCisco:
	case "":
		return Command{}, fmt.Errorf("build vendor image command: %w", errUnknownVendorOperation)
	default:
		return Command{}, fmt.Errorf("build vendor image command for %q: %w", operation, errUnknownVendorOperation)
	}

	helper := filepath.Join(projectRoot, "test", "interop-clab", "vendor_images.py")
	commandArguments := make([]string, 0, 7+len(arguments))
	commandArguments = append(commandArguments,
		"run",
		"--frozen",
		"--no-default-groups",
		"--",
		"python",
		helper,
		string(operation),
	)
	commandArguments = append(commandArguments, arguments...)

	return Command{
		Executable: "uv",
		Arguments:  commandArguments,
		Directory:  projectRoot,
	}, nil
}
