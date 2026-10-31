package cirunner

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const refmtDirectoryName = "refmt-local"

// GoReleaserWorkspace owns the exact source and consumer layout used by the publisher.
// Inventory and security callers must use Directory, verify after their Go command, and Close.
type GoReleaserWorkspace struct {
	installation            *sourceInstallation
	profile                 goreleaserProfile
	options                 GoReleaserOptions
	source, refmt, consumer *goreleaserSource
	ownsInstallation        bool
	closed                  bool
}

// PrepareGoReleaserWorkspace authenticates both components before exposing a consumer directory.
func PrepareGoReleaserWorkspace(ctx context.Context, options GoReleaserOptions) (
	_ *GoReleaserWorkspace, returnErr error,
) {
	profile, err := loadGoReleaserProfile(options.Root)
	if err != nil {
		return nil, err
	}
	if profileErr := profile.requireManifests(); profileErr != nil {
		return nil, profileErr
	}
	if options.Runner == nil {
		return nil, fmt.Errorf("publisher workspace runner is required: %w", errInvalidConfig)
	}
	if pathErr := validatePublisherInstallPath(options.Root, options.InstallDir); pathErr != nil {
		return nil, pathErr
	}
	installation, err := prepareSourceInstallation(options.InstallDir, goreleaserName, (*os.Root).Mkdir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, installation.Close())
		}
	}()
	workspace, err := prepareGoReleaserWorkspace(ctx, options, installation, profile)
	if err != nil {
		return nil, err
	}
	workspace.ownsInstallation = true
	return workspace, nil
}

func prepareGoReleaserWorkspace(ctx context.Context, options GoReleaserOptions,
	installation *sourceInstallation, profile goreleaserProfile,
) (_ *GoReleaserWorkspace, returnErr error) {
	options.Environment = syftEnvironment(options.Environment)
	workspace := &GoReleaserWorkspace{installation: installation, profile: profile, options: options}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, workspace.Close())
		}
	}()
	var err error
	workspace.source, err = preparePublisherTree(ctx, options, installation, profile)
	if err != nil {
		return nil, err
	}
	workspace.refmt, err = preparePublisherComponent(ctx, options, installation,
		profile.Refmt.sourceProfile(), refmtDirectoryName, "refmt")
	if err != nil {
		return nil, err
	}
	workspace.consumer, err = preparePublisherConsumer(options.Root, installation, profile.Manifests)
	if err != nil {
		return nil, err
	}
	if err := workspace.validateSourceModules(ctx, options); err != nil {
		return nil, err
	}
	if err := workspace.validateConsumer(ctx, options); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("finish publisher workspace preparation: %w", err)
	}
	return workspace, workspace.Verify()
}

// Directory returns the sole verified Go working directory, never a checkout module.
func (workspace *GoReleaserWorkspace) Directory() (string, error) {
	if err := workspace.Verify(); err != nil {
		return "", err
	}
	return workspace.consumer.directory, nil
}

// Environment returns a copy of the closed compiler environment for the same prepared graph.
func (workspace *GoReleaserWorkspace) Environment() ([]string, error) {
	if err := workspace.Verify(); err != nil {
		return nil, err
	}
	return slices.Clone(workspace.options.Environment), nil
}

// Build verifies the bound artifact and keeps its prepared graph alive until Close.
func (workspace *GoReleaserWorkspace) Build(ctx context.Context) (string, error) {
	if err := workspace.build(ctx, buildinfo.Read); err != nil {
		return "", err
	}
	return filepath.Join(workspace.installation.directory, workspace.installation.executableAt), nil
}

func (workspace *GoReleaserWorkspace) build(ctx context.Context,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) error {
	if err := workspace.Verify(); err != nil {
		return err
	}
	if workspace.installation.buildStarted {
		return fmt.Errorf("publisher workspace build already started: %w", errInvalidConfig)
	}
	if err := compileGoReleaser(ctx, workspace.options, workspace.installation, workspace, workspace.profile); err != nil {
		return err
	}
	if err := verifyGoReleaserBinary(ctx, workspace.options.Runner, workspace.installation,
		workspace.profile, readBuildInfo); err != nil {
		return err
	}
	return workspace.record()
}

// Verify rejects modified files or path identities before or after a graph/build command.
func (workspace *GoReleaserWorkspace) Verify() error {
	if workspace == nil || workspace.closed {
		return fmt.Errorf("publisher workspace is closed: %w", errInvalidConfig)
	}
	if err := workspace.installation.verify(); err != nil {
		return err
	}
	for _, source := range []*goreleaserSource{workspace.source, workspace.refmt, workspace.consumer} {
		if source == nil {
			return fmt.Errorf("publisher workspace is incomplete: %w", errInvalidConfig)
		}
		if err := source.verifyContents(); err != nil {
			return err
		}
	}
	return nil
}

// Close removes only captured source/consumer paths and preserves foreign replacements.
func (workspace *GoReleaserWorkspace) Close() error {
	if workspace == nil || workspace.closed {
		return nil
	}
	workspace.closed = true
	var result error
	for _, source := range []*goreleaserSource{workspace.consumer, workspace.refmt, workspace.source} {
		if source != nil {
			result = errors.Join(result, source.Close())
		}
	}
	if workspace.ownsInstallation {
		result = errors.Join(result, workspace.installation.Close())
	}
	return result
}

func preparePublisherConsumer(repository string, installation *sourceInstallation,
	manifests *goreleaserManifests,
) (_ *goreleaserSource, returnErr error) {
	root, err := installation.root.OpenRoot(sourceStageDirectory)
	if err != nil {
		return nil, fmt.Errorf("open publisher consumer stage: %w", err)
	}
	consumer := &goreleaserSource{
		parent: installation.root, root: root,
		name: sourceStageDirectory, directory: filepath.Join(installation.directory, sourceStageDirectory),
		info: installation.owned[sourceStageDirectory], owned: make(map[string]os.FileInfo),
		contents: make(map[string]string), keepDirectory: true, outputOwner: installation,
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, consumer.Close())
		}
	}()
	if err := consumer.installManifests(repository, manifests, "qualified", true); err != nil {
		return nil, err
	}
	return consumer, consumer.verifyContents()
}

func (workspace *GoReleaserWorkspace) validateConsumer(ctx context.Context, options GoReleaserOptions) error {
	manifest, err := workspace.readManifest(ctx, options, workspace.consumer)
	if err != nil {
		return err
	}
	return manifest.validate(workspace.profile)
}

func (workspace *GoReleaserWorkspace) validateSourceModules(ctx context.Context, options GoReleaserOptions) error {
	for source, module := range map[*goreleaserSource]string{
		workspace.source: workspace.profile.Module, workspace.refmt: toolprofile.RefmtModule,
	} {
		manifest, err := workspace.readManifest(ctx, options, source)
		if err != nil {
			return err
		}
		if manifest.Module.Path != module {
			return fmt.Errorf("publisher source overlay has the wrong module role: %w", errInvalidConfig)
		}
	}
	return nil
}

func (workspace *GoReleaserWorkspace) readManifest(ctx context.Context, options GoReleaserOptions,
	source *goreleaserSource,
) (publisherConsumerManifest, error) {
	var manifest publisherConsumerManifest
	output := &synchronizedBoundedBuffer{limit: goreleaserFileLimit}
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go", Dir: source.directory,
		Env: syftEnvironment(options.Environment), Args: []string{"mod", "edit", "-json"}, Stdout: output,
	}); err != nil {
		return manifest, fmt.Errorf("parse publisher manifest with owning Go parser: %w", err)
	}
	if err := workspace.Verify(); err != nil {
		return manifest, err
	}
	if err := ctx.Err(); err != nil {
		return manifest, fmt.Errorf("finish publisher manifest parsing: %w", err)
	}
	if err := validateStrictJSONDocument(output.Bytes(), "publisher consumer manifest"); err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(output.Bytes(), &manifest); err != nil {
		return manifest, fmt.Errorf("decode publisher consumer manifest: %w", err)
	}
	return manifest, nil
}

//nolint:tagliatelle // go mod edit -json owns these exported wire field names.
type publisherConsumerManifest struct {
	Module  publisherModuleVersion         `json:"Module"`
	Tool    []publisherModuleVersion       `json:"Tool"`
	Require []publisherModuleVersion       `json:"Require"`
	Replace []publisherConsumerReplacement `json:"Replace"`
}

//nolint:tagliatelle // go mod edit -json owns these exported wire field names.
type publisherModuleVersion struct {
	Path    string `json:"Path"`
	Version string `json:"Version,omitempty"`
}

//nolint:tagliatelle // go mod edit -json owns these exported wire field names.
type publisherConsumerReplacement struct {
	Old publisherModuleVersion `json:"Old"`
	New publisherModuleVersion `json:"New"`
}

func (manifest publisherConsumerManifest) validate(profile goreleaserProfile) error {
	if manifest.Module.Path != toolprofile.GoReleaserConsumer || len(manifest.Tool) != 1 ||
		manifest.Tool[0].Path != profile.Module {
		return fmt.Errorf("publisher manifest is not the canonical tool consumer: %w", errInvalidConfig)
	}
	requirements := make(map[string]string, len(manifest.Require))
	for _, requirement := range manifest.Require {
		if _, exists := requirements[requirement.Path]; exists {
			return fmt.Errorf("publisher consumer repeats a requirement: %w", errInvalidConfig)
		}
		requirements[requirement.Path] = requirement.Version
	}
	if requirements[profile.Module] != profile.Version || requirements[toolprofile.RefmtModule] != profile.Refmt.Version {
		return fmt.Errorf("publisher consumer component requirements differ from source ancestry: %w", errInvalidConfig)
	}
	return manifest.validateReplacements(profile)
}

func (manifest publisherConsumerManifest) validateReplacements(profile goreleaserProfile) error {
	required := map[string]string{
		profile.Module: "../source", toolprofile.RefmtModule: "../refmt-local",
		"github.com/cilium/ebpf": "", "github.com/google/cel-go": "",
		"github.com/labstack/echo/v4": "", "github.com/prometheus/prometheus": "",
		"github.com/yuin/goldmark": "", "go.etcd.io/etcd/client/pkg/v3": "",
		"go.etcd.io/etcd/server/v3": "", "go.etcd.io/etcd/v3": "",
	}
	if len(manifest.Replace) != len(required) {
		return fmt.Errorf("publisher consumer replacement set differs from reviewed layout: %w", errInvalidConfig)
	}
	for _, replacement := range manifest.Replace {
		target, present := required[replacement.Old.Path]
		if !present {
			return fmt.Errorf("publisher consumer replacement is foreign or repeated: %w", errInvalidConfig)
		}
		delete(required, replacement.Old.Path)
		version := profile.Version
		if replacement.Old.Path == toolprofile.RefmtModule {
			version = profile.Refmt.Version
		}
		if err := replacement.validateBinding(version, target); err != nil {
			return err
		}
	}
	return nil
}

func (replacement publisherConsumerReplacement) validateBinding(version, target string) error {
	if target == "" {
		if replacement.Old.Version != "" || replacement.New.Path != replacement.Old.Path || replacement.New.Version == "" {
			return fmt.Errorf("publisher consumer security replacement changes module identity: %w", errInvalidConfig)
		}
		return nil
	}
	if replacement.Old.Version != version || replacement.New.Path != target || replacement.New.Version != "" {
		return fmt.Errorf("publisher consumer local replacement differs from prepared component: %w", errInvalidConfig)
	}
	return nil
}
