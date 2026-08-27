package clabbootstrap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
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
	frrReference, err := loadFRRReference(options.ProjectRoot)
	if err != nil {
		return err
	}

	if err := runPreflight(ctx, options, runner); err != nil {
		return err
	}

	images := publicImages(options.Tags, frrReference)
	failures := pullImages(ctx, options, runner, images)
	failures = append(failures, runVendorPhases(ctx, options, runner, images)...)
	if !options.SkipBuild {
		if err := runCommand(ctx, runner, buildCommand(options)); err != nil {
			failures = append(failures, "gobfd-build")
		}
	}

	inspectInventory(ctx, options, runner, frrReference)
	if len(failures) != 0 {
		return fmt.Errorf("%w: %s", ErrBootstrapFailed, strings.Join(failures, ", "))
	}
	if err := runTopology(ctx, options, runner); err != nil {
		return fmt.Errorf("%w: deploy/test: %w", ErrBootstrapFailed, err)
	}
	return nil
}

func validateOptions(options Options, runner Runner) error {
	if runner == nil {
		return fmt.Errorf("validate bootstrap runner: %w", errInvalidBootstrapOptions)
	}
	if !filepath.IsAbs(options.ProjectRoot) || options.Jobs <= 0 {
		return fmt.Errorf(
			"validate bootstrap root %q and jobs %d: %w",
			options.ProjectRoot,
			options.Jobs,
			errInvalidBootstrapOptions,
		)
	}
	if options.Deploy && options.Test {
		return fmt.Errorf("validate mutually exclusive deploy and test flags: %w", errInvalidBootstrapOptions)
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
			Arguments:  []string{"version", "--format", "{{.Client.Version}}"},
			DryRun:     options.DryRun,
		},
	}
	if !options.SkipBuild {
		commands = append(commands, Command{Executable: "go", Arguments: []string{"version"}, DryRun: options.DryRun})
	}
	for _, command := range commands {
		if err := runCommand(ctx, runner, command); err != nil {
			return fmt.Errorf("run bootstrap preflight: %w", err)
		}
	}
	return nil
}

func publicImages(tags ImageTags, frrReference string) []imageReference {
	return []imageReference{
		{name: "nokia", reference: "ghcr.io/nokia/srlinux:" + tags.Nokia},
		{name: "sonic", reference: "docker.io/netreplica/docker-sonic-vs:" + tags.Sonic},
		{name: "vyos", reference: "docker.io/muruu1/vyos:" + tags.VyOS},
		{name: "frr", reference: frrReference},
		{
			name: "gobgp",
			reference: "docker.io/jauderho/gobgp:v3.37.0@sha256:" +
				"3bb7304d299c42383c738f5bde2464793e2def9c1ff7fa3f25707a5bb10aee37",
		},
		{
			name: "golang",
			reference: "docker.io/library/golang:1.27.0-trixie@sha256:" +
				"ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a",
		},
		{
			name: "debian",
			reference: "docker.io/library/debian:trixie-slim@sha256:" +
				"d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132",
		},
	}
}

func loadFRRReference(projectRoot string) (string, error) {
	topologyPath := filepath.Join(projectRoot, "test", "interop-clab", "gobfd-vendors.clab.yml")
	// #nosec G304 -- projectRoot is the validated absolute repository cwd and the joined suffix is constant.
	file, err := os.Open(topologyPath)
	if err != nil {
		return "", fmt.Errorf("open Containerlab topology %s: %w", topologyPath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat Containerlab topology %s: %w", topologyPath, err)
	}
	const maxTopologySize = 1 << 20
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxTopologySize {
		return "", fmt.Errorf("validate Containerlab topology size %d: %w", info.Size(), errInvalidBootstrapOptions)
	}

	type topologyDocument struct {
		Topology struct {
			Nodes map[string]struct {
				Image string `yaml:"image"`
			} `yaml:"nodes"`
		} `yaml:"topology"`
	}
	var document topologyDocument
	decoder := yaml.NewDecoder(io.LimitReader(file, maxTopologySize))
	if err := decoder.Decode(&document); err != nil {
		return "", fmt.Errorf("decode Containerlab topology %s: %w", topologyPath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", fmt.Errorf(
				"validate Containerlab topology %s: multiple YAML documents: %w",
				topologyPath,
				errInvalidBootstrapOptions,
			)
		}
		return "", fmt.Errorf("validate Containerlab topology %s: %w", topologyPath, err)
	}

	reference := document.Topology.Nodes["frr"].Image
	name, digest, found := strings.Cut(reference, "@sha256:")
	if !found || name == "" || !strings.HasPrefix(name, "quay.io/frrouting/frr:") || len(digest) != 64 {
		return "", fmt.Errorf("validate immutable FRR image %q: %w", reference, errInvalidBootstrapOptions)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("decode FRR image digest %q: %w", digest, err)
	}
	return reference, nil
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

func runVendorPhases(ctx context.Context, options Options, runner Runner, images []imageReference) []string {
	failures := make([]string, 0, 3)
	if err := prepareVyOS(ctx, options, runner, images[2].reference); err != nil {
		failures = append(failures, "vyos")
	}
	if options.Archives.Arista != "" {
		if err := importArista(ctx, options, runner); err != nil {
			failures = append(failures, "arista")
		}
	}
	if options.Archives.Cisco != "" {
		if err := importCisco(ctx, options, runner); err != nil {
			failures = append(failures, "cisco")
		}
	}
	return failures
}

func buildCommand(options Options) Command {
	return Command{
		Executable: executablePodman,
		Arguments: []string{
			"build", "-t", "gobfd-clab:latest", "-f",
			filepath.Join(options.ProjectRoot, "test", "interop-clab", "Containerfile.gobfd"),
			options.ProjectRoot,
		},
		DryRun: options.DryRun,
	}
}

func inspectInventory(ctx context.Context, options Options, runner Runner, frrReference string) {
	images := []imageReference{
		{name: "nokia", reference: "ghcr.io/nokia/srlinux:" + options.Tags.Nokia},
		{name: "sonic", reference: "docker.io/netreplica/docker-sonic-vs:" + options.Tags.Sonic},
		{name: "vyos", reference: "vyos:latest"},
		{name: "frr", reference: frrReference},
		{name: "gobfd", reference: "gobfd-clab:latest"},
		{name: "arista", reference: options.Tags.Arista},
		{name: "cisco", reference: options.Tags.Cisco},
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
}

func runTopology(ctx context.Context, options Options, runner Runner) error {
	if !options.Deploy && !options.Test {
		return nil
	}
	arguments := []string(nil)
	if options.Deploy {
		arguments = []string{"--up-only"}
	}
	return runCommand(ctx, runner, Command{
		Executable: filepath.Join(options.ProjectRoot, "test", "interop-clab", "run.sh"),
		Arguments:  arguments,
		Directory:  filepath.Join(options.ProjectRoot, "test", "interop-clab"),
		DryRun:     options.DryRun,
	})
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
