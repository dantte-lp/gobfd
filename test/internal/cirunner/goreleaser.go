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
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const goreleaserProvenance = "bin/source.json"

// GoReleaserOptions selects an isolated publisher installation from reviewed source inputs.
type GoReleaserOptions = SyftOptions

// GoReleaserInstallation shares the owned, inode-bound source installation lifecycle.
type GoReleaserInstallation = sourceInstallation

type goreleaserReceipt struct {
	Profile            goreleaserProfile         `json:"profile"`
	OriginalFiles      int                       `json:"original_files"`
	OriginalBytes      int64                     `json:"original_bytes"`
	PatchedFiles       int                       `json:"patched_files"`
	PatchedBytes       int64                     `json:"patched_bytes"`
	PatchedContentSum  string                    `json:"patched_content_sum"`
	Refmt              publisherComponentReceipt `json:"refmt"`
	ConsumerContentSum string                    `json:"consumer_content_sum"`
}

type publisherComponentReceipt struct {
	OriginalFiles     int    `json:"original_files"`
	OriginalBytes     int64  `json:"original_bytes"`
	PatchedFiles      int    `json:"patched_files"`
	PatchedBytes      int64  `json:"patched_bytes"`
	PatchedContentSum string `json:"patched_content_sum"`
}

// ReleaseGoReleaser runs the native source worker and publishes its private prerequisite PATH.
func ReleaseGoReleaser(ctx context.Context, options ReleaseSyftOptions) (returnErr error) {
	parent, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	if pathErr := validateGitHubPath(options.GitHubPath); pathErr != nil {
		return pathErr
	}
	installation, err := InstallGoReleaser(ctx, GoReleaserOptions{
		Root: options.Root, InstallDir: filepath.Join(parent, "gobfd-goreleaser"),
		Environment: options.Environment, Runner: options.Runner,
	})
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, installation.Close()) }()
	return installation.publishPath(ctx, options.GitHubPath)
}

// InstallGoReleaser builds only after the publisher's frozen manifests are qualified.
func InstallGoReleaser(ctx context.Context, options GoReleaserOptions) (*GoReleaserInstallation, error) {
	profile, err := loadGoReleaserProfile(options.Root)
	if err != nil {
		return nil, err
	}
	return installGoReleaser(ctx, options, profile, buildinfo.Read)
}

func installGoReleaser(
	ctx context.Context, options GoReleaserOptions, profile goreleaserProfile,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) (_ *GoReleaserInstallation, returnErr error) {
	if err := profile.requireManifests(); err != nil {
		return nil, err
	}
	if options.Runner == nil {
		return nil, fmt.Errorf("publisher source runner is required: %w", errInvalidConfig)
	}
	if err := validatePublisherInstallPath(options.Root, options.InstallDir); err != nil {
		return nil, err
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
	defer func() { returnErr = errors.Join(returnErr, workspace.Close()) }()
	if buildErr := workspace.build(ctx, readBuildInfo); buildErr != nil {
		return nil, buildErr
	}
	if cleanupErr := workspace.Close(); cleanupErr != nil {
		return nil, cleanupErr
	}
	if publishErr := installation.publish(); publishErr != nil {
		return nil, publishErr
	}
	return installation, nil
}

func validatePublisherInstallPath(repository, directory string) error {
	if _, err := validateAbsoluteExistingDirectory(repository, "publisher repository"); err != nil {
		return err
	}
	relative, err := filepath.Rel(repository, directory)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("publisher installation must be outside checkout: %w", errors.Join(err, errInvalidConfig))
	}
	return nil
}

func preparePublisherTree(
	ctx context.Context, options GoReleaserOptions, installation *sourceInstallation, profile goreleaserProfile,
) (*goreleaserSource, error) {
	return preparePublisherComponent(ctx, options, installation, profile, goreleaserSourceDirectoryName, "source")
}

func preparePublisherComponent(
	ctx context.Context, options GoReleaserOptions, installation *sourceInstallation, profile goreleaserProfile,
	name, manifestPrefix string,
) (_ *goreleaserSource, returnErr error) {
	source, err := preparePublisherSource(installation.root, installation.directory, name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, source.Close())
		}
	}()
	download, err := resolveGoReleaserDownload(ctx, options.Runner, source, profile, syftEnvironment(options.Environment))
	if err != nil {
		return nil, err
	}
	contents, err := readGoReleaserZIP(download.Zip, profile)
	if err != nil {
		return nil, err
	}
	if copyErr := source.copy(ctx, contents); copyErr != nil {
		return nil, copyErr
	}
	if patchErr := source.patch(profile); patchErr != nil {
		return nil, patchErr
	}
	if manifestErr := source.installManifests(options.Root, profile.SourceManifests,
		manifestPrefix, false); manifestErr != nil {
		return nil, manifestErr
	}
	return source, source.verifyContents()
}

func (source *goreleaserSource) installManifests(repository string, manifests *goreleaserManifests,
	prefix string, create bool,
) (returnErr error) {
	root, err := os.OpenRoot(goreleaserManifestDirectory(repository))
	if err != nil {
		return fmt.Errorf("open frozen publisher manifests: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	for name, digest := range map[string]string{
		goModFilename: manifests.ModSHA256, "go.sum": manifests.SumSHA256,
	} {
		data, err := readGoReleaserFile(root, prefix+filepath.Ext(name))
		if err != nil {
			return err
		}
		if goreleaserSHA256(data) != digest {
			return fmt.Errorf("publisher frozen manifest content differs from reviewed profile: %w", errInvalidConfig)
		}
		if err := source.write(name, data, create); err != nil {
			return err
		}
	}
	return nil
}

func compileGoReleaser(
	ctx context.Context, options GoReleaserOptions, installation *sourceInstallation,
	workspace *GoReleaserWorkspace, profile goreleaserProfile,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start publisher source build: %w", err)
	}
	if err := installation.verify(); err != nil {
		return err
	}
	if err := workspace.Verify(); err != nil {
		return err
	}
	installation.buildStarted = true
	if err := options.Runner.RunCommand(ctx, CommandSpec{
		Name: "go", Dir: workspace.consumer.directory, Env: syftEnvironment(options.Environment),
		Args: []string{
			goBuildSubcommand, "-trimpath", "-buildvcs=false", "-mod=readonly",
			"-ldflags=" + profile.ldflags(), "-o", filepath.Join(installation.directory, sourceStageDirectory, goreleaserName),
			profile.Module,
		},
	}); err != nil {
		return fmt.Errorf("build authenticated publisher: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("finish publisher source build: %w", err)
	}
	if err := workspace.Verify(); err != nil {
		return err
	}
	return installation.openExecutableAt(filepath.Join(sourceStageDirectory, goreleaserName))
}

func (profile goreleaserProfile) ldflags() string {
	return "-X main.version=" + strings.TrimPrefix(profile.Version, "v") +
		" -X main.commit=" + profile.SourceCommit + " -X main.treeState=dirty"
}

func verifyGoReleaserBinary(
	ctx context.Context, runner SpecRunner, installation *sourceInstallation, profile goreleaserProfile,
	readBuildInfo func(io.ReaderAt) (*debug.BuildInfo, error),
) error {
	info, err := readBuildInfo(installation.executable)
	if err != nil {
		return fmt.Errorf("read bound publisher build information: %w", err)
	}
	if err := profile.verifyBuildInfo(info); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output := &synchronizedBoundedBuffer{limit: goreleaserMetadataLimit}
	if err := runner.RunCommand(probeCtx, CommandSpec{
		Name: goreleaserName, Args: []string{"--version"},
		Dir: installation.directory, Env: []string{"PATH=/usr/bin:/bin"},
		Executable: installation.executable, Stdout: output,
	}); err != nil {
		return fmt.Errorf("verify publisher version: %w", err)
	}
	if err := probeCtx.Err(); err != nil {
		return fmt.Errorf("verify publisher version: %w", err)
	}
	if err := profile.verifyVersion(output.Bytes()); err != nil {
		return err
	}
	return installation.verify()
}

func (profile goreleaserProfile) verifyBuildInfo(info *debug.BuildInfo) error {
	if info == nil || info.Path != profile.Module || info.Main.Path != profile.Module ||
		info.Main.Version != profile.Version || info.Main.Sum != "" ||
		info.GoVersion != runtime.Version() {
		return fmt.Errorf("publisher tool-consumer build identity differs from reviewed source: %w", errInvalidConfig)
	}
	if err := verifyPublisherLocalReplacement(info.Main.Replace, "../source"); err != nil {
		return err
	}
	if err := profile.verifyRefmtBuildInfo(info.Deps); err != nil {
		return err
	}
	// Go omits linker flags with trimpath; verify their effect through the version probe.
	for name, value := range map[string]string{
		"-trimpath": "true", "CGO_ENABLED": "0",
		"GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH,
	} {
		if !slices.Contains(info.Settings, debug.BuildSetting{Key: name, Value: value}) {
			return fmt.Errorf("publisher compiler setting %s differs from reviewed profile: %w", name, errInvalidConfig)
		}
	}
	return nil
}

func (profile goreleaserProfile) verifyVersion(data []byte) error {
	want := map[string]string{
		"GitVersion": strings.TrimPrefix(profile.Version, "v"), "GitCommit": profile.SourceCommit,
		"GitTreeState": "dirty", "BuildDate": unknownMetadataValue, "BuiltBy": unknownMetadataValue,
		"GoVersion": runtime.Version(), "Compiler": runtime.Compiler, "ModuleSum": unknownMetadataValue,
		"Platform": runtime.GOOS + "/" + runtime.GOARCH,
	}
	seen := make(map[string]bool, len(want))
	for line := range strings.SplitSeq(string(data), "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if expected, present := want[name]; present {
			if seen[name] || strings.TrimSpace(value) != expected {
				return fmt.Errorf("publisher version field %s differs from source profile: %w", name, errInvalidConfig)
			}
			seen[name] = true
		} else if strings.HasPrefix(name, "Git") || name == "ModuleSum" {
			return fmt.Errorf("publisher version repeats identity fields: %w", errInvalidConfig)
		}
	}
	if len(seen) != len(want) {
		return fmt.Errorf("publisher version metadata is incomplete: %w", errInvalidConfig)
	}
	return nil
}

func (workspace *GoReleaserWorkspace) record() error {
	if err := workspace.Verify(); err != nil {
		return err
	}
	profile := workspace.profile
	source := workspace.source
	receipt := goreleaserReceipt{
		Profile: profile, OriginalFiles: profile.Files, OriginalBytes: profile.Bytes,
		PatchedFiles: len(source.contents),
	}
	for name := range source.contents {
		receipt.PatchedBytes += source.owned[name].Size()
	}
	receipt.PatchedContentSum = source.contentSum()
	receipt.Refmt = workspace.refmt.componentReceipt(profile.Refmt.sourceProfile())
	receipt.ConsumerContentSum = workspace.consumer.contentSum()
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode publisher source provenance: %w", err)
	}
	return workspace.installation.publishPublisherProvenance(data)
}

func (source *goreleaserSource) componentReceipt(profile goreleaserProfile) publisherComponentReceipt {
	receipt := publisherComponentReceipt{
		OriginalFiles: profile.Files, OriginalBytes: profile.Bytes,
		PatchedFiles: len(source.contents), PatchedContentSum: source.contentSum(),
	}
	for name := range source.contents {
		receipt.PatchedBytes += source.owned[name].Size()
	}
	return receipt
}

func verifyPublisherLocalReplacement(replacement *debug.Module, directory string) error {
	if replacement == nil || replacement.Path != directory || replacement.Version != "(devel)" ||
		replacement.Sum != "" || replacement.Replace != nil {
		return fmt.Errorf("publisher local replacement differs from authenticated prepared source: %w", errInvalidConfig)
	}
	return nil
}

func (profile goreleaserProfile) verifyRefmtBuildInfo(dependencies []*debug.Module) error {
	if profile.Refmt == nil {
		return fmt.Errorf("publisher Refmt profile is missing: %w", errInvalidConfig)
	}
	var selected *debug.Module
	for _, dependency := range dependencies {
		if dependency != nil && dependency.Path == toolprofile.RefmtModule {
			if selected != nil {
				return fmt.Errorf("publisher repeats Refmt module identity: %w", errInvalidConfig)
			}
			selected = dependency
		}
	}
	if selected == nil || selected.Version != profile.Refmt.Version || selected.Sum != "" {
		return fmt.Errorf("publisher Refmt build identity differs from authenticated source: %w", errInvalidConfig)
	}
	return verifyPublisherLocalReplacement(selected.Replace, "../refmt-local")
}

func (profile goreleaserProfile) consumerContentSum() string {
	return goreleaserSHA256([]byte(fmt.Sprintf("%s  go.mod\n%s  go.sum\n",
		profile.Manifests.ModSHA256, profile.Manifests.SumSHA256)))
}

func (receipt publisherComponentReceipt) validate(profile goreleaserProfile) error {
	if receipt.OriginalFiles != profile.Files || receipt.OriginalBytes != profile.Bytes ||
		receipt.PatchedFiles != profile.Files || receipt.PatchedBytes <= 0 ||
		receipt.PatchedBytes > profile.Bytes+2*goreleaserFileLimit || !validGoReleaserHash(receipt.PatchedContentSum) {
		return fmt.Errorf("publisher component provenance differs from authenticated source/manifests: %w", errInvalidConfig)
	}
	return nil
}

func (installation *sourceInstallation) publishPublisherProvenance(data []byte) (returnErr error) {
	if err := installation.verify(); err != nil {
		return err
	}
	if _, statErr := installation.root.Lstat(goreleaserProvenance); !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("publisher provenance publication collision: %w", errors.Join(statErr, errInvalidConfig))
	}
	binRoot, err := installation.root.OpenRoot("bin")
	if err != nil {
		return fmt.Errorf("open owned publisher provenance directory: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close publisher provenance directory", binRoot.Close()))
	}()
	binInfo, err := binRoot.Stat(".")
	if err != nil || !os.SameFile(binInfo, installation.owned["bin"]) {
		return fmt.Errorf("publisher provenance directory identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	if writeErr := writeRootedModeArtifact(binRoot, filepath.Base(goreleaserProvenance), data,
		"publisher source provenance", goreleaserMetadataLimit, 0o600); writeErr != nil {
		return writeErr
	}
	if verifyErr := installation.verify(); verifyErr != nil {
		return verifyErr
	}
	info, err := installation.root.Lstat(goreleaserProvenance)
	if err != nil {
		return fmt.Errorf("capture publisher provenance: %w", err)
	}
	installation.artifacts = map[string]os.FileInfo{goreleaserProvenance: info}
	return installation.verify()
}

func (source *goreleaserSource) contentSum() string {
	names := make([]string, 0, len(source.contents))
	for name := range source.contents {
		names = append(names, name)
	}
	slices.Sort(names)
	var summary strings.Builder
	for _, name := range names {
		fmt.Fprintf(&summary, "%s  %s\n", source.contents[name], name)
	}
	return goreleaserSHA256([]byte(summary.String()))
}
