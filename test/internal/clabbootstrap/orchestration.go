package clabbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/dantte-lp/gobfd/test/internal/vendorprofiles"
)

var errInvalidBootstrapOptions = errors.New("invalid containerlab bootstrap options")

type imageReference struct {
	name      string
	reference string
}

type pullResult struct {
	name string
	err  error
}

// Run executes the Go-owned bootstrap phases through runner.
func Run(ctx context.Context, options Options, runner Runner) error {
	if err := validateOptions(options, runner); err != nil {
		return err
	}
	if !options.Down {
		if _, err := vendorprofiles.Load(options.ProjectRoot); err != nil {
			return fmt.Errorf("load vendor execution policy: %w", err)
		}
		if options.Archives != (VendorArchives{}) || options.VyOSISO != "" ||
			(options.VyOSVersion != "" && options.VyOSVersion != "latest") {
			return fmt.Errorf("NOS archive and ISO preparation remains unqualified: %w", vendorprofiles.ErrUnavailable)
		}
	}
	if options.DryRun {
		return runLocked(ctx, options, runner)
	}
	lock, err := acquireLifecycleLock(options.ProjectRoot)
	if err != nil {
		return err
	}
	options.lifecycleLockHeld = true
	return errors.Join(runLocked(ctx, options, runner), releaseLifecycleLock(lock))
}

func runLocked(ctx context.Context, options Options, runner Runner) error {
	if options.Down || options.TestOnly {
		lifecycleOptions := options
		lifecycleOptions.SkipBuild = true
		if err := runPreflight(ctx, lifecycleOptions, runner); err != nil {
			return err
		}
		return runTopology(ctx, options, runner, "")
	}
	if err := validateFRRTemplate(options.ProjectRoot); err != nil {
		return err
	}
	if err := runPreflight(ctx, options, runner); err != nil {
		return err
	}
	images := publicImages()
	failures := pullImages(ctx, options, runner, images)
	if len(failures) != 0 {
		return fmt.Errorf("%w: %s", ErrBootstrapFailed, strings.Join(failures, ", "))
	}
	receipt, err := prepareApplicationImages(ctx, options, runner)
	if err != nil {
		return fmt.Errorf("%w: prepare owned application images: %w", ErrBootstrapFailed, err)
	}
	if err := inspectInventory(ctx, options, runner, receipt.FRR.Reference, receipt.Image.Reference); err != nil {
		return err
	}
	if err := runTopology(ctx, options, runner, receipt.Image.Reference); err != nil {
		return fmt.Errorf("%w: deploy/test: %w", ErrBootstrapFailed, err)
	}
	return nil
}

func prepareApplicationImages(ctx context.Context, options Options, runner Runner) (lifecycleReceipt, error) {
	if options.DryRun {
		receipt := newImageReceipt("dry-run")
		if !options.SkipBuild {
			for _, command := range applicationBuildCommands(options, receipt,
				strings.Repeat("0", 40), "1970-01-01T00:00:00Z") {
				if err := runCommand(ctx, runner, command); err != nil {
					return lifecycleReceipt{}, err
				}
			}
		}
		return receipt, nil
	}
	return stageOwnedImages(ctx, options, runner)
}

func validateOptions(options Options, runner Runner) error {
	if runner == nil {
		return fmt.Errorf("validate bootstrap runner: %w", errInvalidBootstrapOptions)
	}
	if os.Getenv("CLAB_LABDIR_BASE") != "" {
		return fmt.Errorf("CLAB_LABDIR_BASE must be unset for owned topology discovery: %w", errInvalidBootstrapOptions)
	}
	if !filepath.IsAbs(options.ProjectRoot) || options.Jobs <= 0 {
		return fmt.Errorf(
			"validate bootstrap root %q and jobs %d: %w",
			options.ProjectRoot,
			options.Jobs,
			errInvalidBootstrapOptions,
		)
	}
	modes := 0
	for _, enabled := range []bool{options.Deploy, options.Test, options.TestOnly, options.Down} {
		if enabled {
			modes++
		}
	}
	if modes > 1 {
		return fmt.Errorf("validate mutually exclusive lifecycle flags: %w", errInvalidBootstrapOptions)
	}
	return nil
}

func runPreflight(ctx context.Context, options Options, runner Runner) error {
	versionResult, err := runner.Run(ctx, Command{
		Executable: executableContainerlab,
		Arguments:  []string{"version", "-j"},
		DryRun:     options.DryRun,
	})
	if err != nil {
		return fmt.Errorf("run containerlab version preflight: %w", err)
	}
	if !options.DryRun {
		if versionResult.ExitCode != 0 {
			return fmt.Errorf(
				"run containerlab version preflight: exit %d: %s: %w",
				versionResult.ExitCode,
				versionResult.Stderr,
				ErrBootstrapFailed,
			)
		}
		var version struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(versionResult.Stdout), &version); err != nil {
			return fmt.Errorf("decode containerlab version JSON: %w", err)
		}
		if version.Version != ContainerlabVersion {
			return fmt.Errorf(
				"containerlab version %q, require %q: %w",
				version.Version,
				ContainerlabVersion,
				errInvalidBootstrapOptions,
			)
		}
	}

	commands := []Command{
		{
			Executable: executablePodman,
			Arguments:  []string{"version", networkFormatOption, "{{.Client.Version}}"},
			DryRun:     options.DryRun,
		},
	}
	for _, command := range commands {
		if err := runCommand(ctx, runner, command); err != nil {
			return fmt.Errorf("run bootstrap preflight: %w", err)
		}
	}
	return nil
}

func publicImages() []imageReference {
	return []imageReference{
		{
			name:      "golang",
			reference: testCompilerImage,
		},
		{
			name:      "debian",
			reference: testRuntimeImage,
		},
	}
}

func validateFRRTemplate(projectRoot string) (returnErr error) {
	topologyPath := filepath.Join(projectRoot, "test", "interop-clab", "gobfd-vendors.clab.yml")
	file, err := os.Open(topologyPath)
	if err != nil {
		return fmt.Errorf("open Containerlab topology %s: %w", topologyPath, err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat Containerlab topology %s: %w", topologyPath, err)
	}
	const maxTopologySize = 1 << 20
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxTopologySize {
		return fmt.Errorf("validate Containerlab topology size %d: %w", info.Size(), errInvalidBootstrapOptions)
	}
	var document topologyDocument
	decoder := yaml.NewDecoder(io.LimitReader(file, maxTopologySize))
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode Containerlab topology %s: %w", topologyPath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("require one Containerlab YAML document: %w", errInvalidBootstrapOptions)
	}
	node := document.Topology.Nodes["frr"]
	if node["kind"] != "ext-container" || node["image"] != nil {
		return fmt.Errorf("FRR must use the owned external container: %w", errInvalidBootstrapOptions)
	}
	return nil
}

func pullImages(ctx context.Context, options Options, runner Runner, images []imageReference) []string {
	results := make([]pullResult, len(images))
	work := make(chan int)
	var workers sync.WaitGroup
	workerCount := min(options.Jobs, len(images))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range work {
				results[index] = pullImage(ctx, options, runner, images[index])
			}
		}()
	}
	for index := range images {
		work <- index
	}
	close(work)
	workers.Wait()

	failures := make([]string, 0)
	for _, result := range results {
		if result.err != nil {
			failures = append(failures, "pull:"+result.name)
		}
	}
	return failures
}

func pullImage(ctx context.Context, options Options, runner Runner, image imageReference) pullResult {
	exists, err := imageExists(ctx, options, runner, image.reference)
	if err != nil {
		return pullResult{name: image.name, err: err}
	}
	if exists {
		return pullResult{name: image.name}
	}
	command := Command{
		Executable: executablePodman,
		Arguments:  []string{"pull", "--quiet", image.reference},
		DryRun:     options.DryRun,
	}
	return pullResult{name: image.name, err: runCommand(ctx, runner, command)}
}

func imageExists(ctx context.Context, options Options, runner Runner, reference string) (bool, error) {
	if options.DryRun {
		return false, nil
	}
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"image", "exists", reference},
	})
	if err != nil {
		return false, fmt.Errorf("inspect image %s: %w", reference, err)
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("inspect image %s: exit %d: %w", reference, result.ExitCode, ErrBootstrapFailed)
	}
}

func inspectInventory(
	ctx context.Context,
	options Options,
	runner Runner,
	frrReference string,
	gobfdReference string,
) error {
	policy, err := vendorprofiles.Load(options.ProjectRoot)
	if err != nil {
		return fmt.Errorf("load vendor inventory policy: %w", err)
	}
	if options.Logger != nil {
		for _, profile := range policy.Profiles {
			if !policy.AllowsNode(profile.TopologyNode) {
				options.Logger.InfoContext(ctx, "vendor profile retained", "name", profile.ID,
					"status", profile.ExecutionPolicy, "qualification_tracking", profile.QualificationTracking)
			}
		}
	}
	images := []imageReference{
		{name: "frr", reference: frrReference},
	}
	if gobfdReference != "" {
		images = append(images, imageReference{name: "gobfd", reference: gobfdReference})
	}
	for _, image := range images {
		exists := options.DryRun
		var err error
		if !options.DryRun {
			exists, err = imageExists(ctx, options, runner, image.reference)
		}
		if options.Logger == nil {
			continue
		}
		if err != nil {
			options.Logger.WarnContext(
				ctx,
				"image inventory check failed",
				"name",
				image.name,
				"reference",
				image.reference,
				"error",
				err,
			)
			continue
		}
		status := "missing"
		if exists {
			status = "ready"
		}
		options.Logger.InfoContext(ctx, "image inventory", "name", image.name, "reference", image.reference, "status", status)
	}
	return nil
}

func runCommand(ctx context.Context, runner Runner, command Command) error {
	result, err := runner.Run(ctx, command)
	if err != nil {
		return fmt.Errorf("run %s: %w", command.Executable, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("run %s: exit %d: %s: %w", command.Executable, result.ExitCode, result.Stderr, ErrBootstrapFailed)
	}
	return nil
}
