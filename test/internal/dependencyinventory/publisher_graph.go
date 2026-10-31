package dependencyinventory

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

var errPublisherGraph = errors.New("invalid publisher selected graph")

const (
	publisherGraphTimeout   = 10 * time.Minute
	publisherGraphWaitDelay = 5 * time.Second
)

//nolint:tagliatelle // These field names are the owning Go command's JSON protocol.
type selectedModule struct {
	Path     string          `json:"Path"`
	Version  string          `json:"Version"`
	Main     bool            `json:"Main"`
	Indirect bool            `json:"Indirect"`
	Replace  *selectedModule `json:"Replace"`
	Error    *struct {
		Err string `json:"Err"`
	} `json:"Error"`
}

func validPublisherMain(item selectedModule) bool {
	return item.Path == toolprofile.GoReleaserConsumer && item.Version == "" && item.Replace == nil && item.Error == nil
}

func publisherLocalSource(path string) string {
	switch path {
	case toolprofile.GoReleaserModule:
		return "../source"
	case toolprofile.RefmtModule:
		return "../refmt-local"
	default:
		return ""
	}
}

func normalizeSelectedModule(item selectedModule, graphID string) (moduleIdentity, bool, error) {
	if item.Path == "" || item.Version == "" || item.Error != nil {
		return moduleIdentity{}, false, fmt.Errorf("selected module in %s %q: %w",
			graphID, item.Path, errPublisherGraph)
	}
	local := false
	if replacement := item.Replace; replacement != nil {
		if replacement.Path == "" || replacement.Replace != nil || replacement.Error != nil || replacement.Main {
			return moduleIdentity{}, false, fmt.Errorf("incomplete replacement in %s for %s: %w",
				graphID, item.Path, errPublisherGraph)
		}
		switch {
		case replacement.Version != "":
			item.Path, item.Version = replacement.Path, replacement.Version
		case graphID == goreleaserGraphID && publisherLocalSource(item.Path) != "" &&
			replacement.Path == publisherLocalSource(item.Path):
			local = true
		default:
			return moduleIdentity{}, false, fmt.Errorf("unsupported local replacement in %s: %s => %s: %w",
				graphID, item.Path, replacement.Path, errPublisherGraph)
		}
	}
	return moduleIdentity{Path: item.Path, Version: item.Version, Indirect: item.Indirect}, local, nil
}

func publisherSourcePin(root string, graph ModuleGraph, path string) ([]SourceLocation, ImmutablePin, error) {
	// #nosec G304 -- the repository root is selected by the caller and the profile path is fixed.
	data, err := os.ReadFile(filepath.Join(root, toolprofile.GoReleaserSource))
	if err != nil {
		return nil, ImmutablePin{}, fmt.Errorf("read publisher source profile: %w", err)
	}
	sources := []SourceLocation{
		{Path: toolprofile.GoReleaserSource, Match: path},
		{Path: graph.Manifest, Match: publisherLocalSource(path)},
	}
	// A modified local tree is bound by the authenticated preparation profile;
	// its ancestry version is not an assertion of the upstream archive's h1.
	pin := ImmutablePin{
		Status: PinVerified, Kind: "prepared-source-profile-sha256",
		Value: fmt.Sprintf("%x", sha256.Sum256(data)),
	}
	return sources, pin, nil
}

// PreparedPublisherWorkspace binds graph collection to the managed source owner.
type PreparedPublisherWorkspace interface {
	Directory() (string, error)
	Environment() ([]string, error)
	Verify() error
}

// CollectGoReleaserGraph reads the actual selected graph from a prepared consumer.
// Local source versions denote authenticated upstream ancestry; their modified
// bytes remain bound by workspace verification, not by an upstream module h1.
func CollectGoReleaserGraph(ctx context.Context, workspace PreparedPublisherWorkspace,
	runner cirunner.SpecRunner,
) (ModuleGraph, error) {
	identities, err := collectPublisherModules(ctx, workspace, runner)
	if err != nil {
		return ModuleGraph{}, err
	}
	graph, ok := moduleGraphFiles(goreleaserGraphID)
	if !ok {
		return ModuleGraph{}, fmt.Errorf("publisher graph registration is missing: %w", errPublisherGraph)
	}
	for _, identity := range identities {
		graph.Modules = append(graph.Modules, Module{
			Path: identity.Path, Version: identity.Version, Indirect: identity.Indirect,
		})
	}
	return graph, nil
}

func selectedGoReleaserGraph(ctx context.Context, root string) ([]moduleIdentity, error) {
	runner := cirunner.ExecRunner{Stderr: os.Stderr}
	workspace, err := cirunner.PrepareGoReleaserWorkspace(ctx, cirunner.GoReleaserOptions{
		Root: root, InstallDir: filepath.Join(os.TempDir(), ".gobfd-inventory-"+rand.Text()),
		Environment: os.Environ(), Runner: runner,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare publisher inventory workspace: %w", err)
	}
	modules, collectErr := collectPublisherModules(ctx, workspace, runner)
	if err := errors.Join(collectErr, workspace.Close()); err != nil {
		return nil, err
	}
	return modules, nil
}

func collectPublisherModules(ctx context.Context, workspace PreparedPublisherWorkspace,
	runner cirunner.SpecRunner,
) ([]moduleIdentity, error) {
	if runner == nil {
		return nil, fmt.Errorf("publisher inventory requires a command runner: %w", errPublisherGraph)
	}
	directory, err := workspace.Directory()
	if err != nil {
		return nil, fmt.Errorf("publisher graph directory: %w", err)
	}
	environment, err := workspace.Environment()
	if err != nil {
		return nil, fmt.Errorf("publisher graph environment: %w", err)
	}
	if err := workspace.Verify(); err != nil {
		return nil, fmt.Errorf("verify publisher graph workspace: %w", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, publisherGraphTimeout)
	defer cancel()
	var output, diagnostics boundedGraphOutput
	runErr := runner.RunCommand(commandCtx, cirunner.CommandSpec{
		Name: "go", Args: []string{"list", "-mod=readonly", "-m", "-json", "all"},
		Dir: directory, Env: environment, Stdout: &output, Stderr: &diagnostics, WaitDelay: publisherGraphWaitDelay,
	})
	if err := errors.Join(runErr, commandCtx.Err(), workspace.Verify()); err != nil {
		return nil, fmt.Errorf("collect publisher selected graph: %w", err)
	}
	return parseSelectedModuleGraph(output.Bytes(), goreleaserGraphID)
}

type boundedGraphOutput struct{ buffer bytes.Buffer }

func (output *boundedGraphOutput) Bytes() []byte { return output.buffer.Bytes() }
func (output *boundedGraphOutput) Len() int      { return output.buffer.Len() }

func (output *boundedGraphOutput) Write(data []byte) (int, error) {
	if len(data) > maxJSONFileBytes-output.Len() {
		return 0, fmt.Errorf("publisher graph output exceeds %d bytes: %w", maxJSONFileBytes, errPublisherGraph)
	}
	n, err := output.buffer.Write(data)
	if err != nil {
		return n, fmt.Errorf("buffer publisher graph output: %w", err)
	}
	return n, nil
}
