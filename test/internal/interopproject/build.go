package interopproject

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/toolbootstrap"
)

const composePullPolicyNever = "never"

type renderedComposeService struct {
	Image    string          `json:"image"`
	Platform string          `json:"platform"`
	Build    json.RawMessage `json:"build"`
}

type plannedComposeBuild struct {
	name string
	args []string
}

func (c *Controller) build(ctx context.Context) error {
	revision, buildDate, metaErr := c.resolveVerifiedComposeMetadata(ctx)
	if metaErr != nil {
		return metaErr
	}
	services, renderErr := c.renderComposeServices(ctx)
	if renderErr != nil {
		return renderErr
	}
	run := strings.ToLower(rand.Text())
	engine, engErr := c.imageEngine(ctx)
	if engErr != nil {
		return engErr
	}
	state, builds, planErr := c.planComposeBuilds(services, revision, buildDate, run, engine)
	if planErr != nil {
		return planErr
	}
	// Validate every build before mutation; unsupported Compose options fail closed.
	if valErr := state.validate(c, engine); valErr != nil {
		return valErr
	}
	if availErr := c.requirePlannedAliasesAbsent(ctx, state, builds); availErr != nil {
		return availErr
	}
	return c.executePlannedBuilds(ctx, state, builds)
}

func (c *Controller) resolveVerifiedComposeMetadata(ctx context.Context) (string, string, error) {
	if os.Getenv("COMPOSE_COMPATIBILITY") != "" {
		return "", "", fmt.Errorf("%w: COMPOSE_COMPATIBILITY is unsupported for interop builds", errControl)
	}
	version, err := c.podmanText(ctx, commandTimeout, "compose", "version", "--short")
	if err != nil {
		return "", "", fmt.Errorf("query Compose provider version: %w", err)
	}
	if strings.TrimSpace(version) != toolbootstrap.ComposeVersion {
		return "", "", fmt.Errorf(
			"%w: Compose provider version %q, want %q",
			errControl, strings.TrimSpace(version), toolbootstrap.ComposeVersion,
		)
	}
	return BuildMetadata(ctx, c.root, os.Getenv("GOBFD_BUILD_REVISION"))
}

func (c *Controller) renderComposeServices(ctx context.Context) (map[string]renderedComposeService, error) {
	output, err := c.podmanText(
		ctx, commandTimeout,
		"compose", "-p", c.projectName, "-f", c.composeFile, "config", "--format", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("render interop Compose builds: %w", err)
	}
	var rendered struct {
		Services map[string]renderedComposeService `json:"services"`
	}
	if decodeErr := decodeSingleJSON([]byte(output), &rendered); decodeErr != nil {
		return nil, fmt.Errorf("decode interop Compose builds: %w", decodeErr)
	}
	return rendered.Services, nil
}

func (c *Controller) planComposeBuilds(
	services map[string]renderedComposeService,
	revision, buildDate, run, engine string,
) (*composeImageState, []plannedComposeBuild, error) {
	state := &composeImageState{
		Services: make(map[string]composeOwnedService),
		Owned: composeImageOwner{
			Schema:  1,
			Project: c.projectName,
			Kind:    c.kind,
			Root:    c.root,
			Engine:  engine,
			Run:     run,
			Images:  make(map[string]composeOwnedImage),
		},
	}
	var builds []plannedComposeBuild
	for _, name := range slices.Sorted(maps.Keys(services)) {
		service := services[name]
		if len(service.Build) == 0 {
			continue
		}
		if service.Platform != "" {
			return nil, nil, fmt.Errorf(
				"%w: interop Compose service %s build platform is unsupported", errControl, name,
			)
		}
		image := ownedComposeAlias(c.projectName, name, run)
		args, argsErr := composeBuildArgs(service.Build, image, revision, buildDate, run)
		if argsErr != nil {
			return nil, nil, fmt.Errorf("validate interop Compose service %s build: %w", name, argsErr)
		}
		builds = append(builds, plannedComposeBuild{name: name, args: args})
		state.Owned.Images[name] = composeOwnedImage{Alias: image}
		state.Services[name] = composeOwnedService{Image: image, PullPolicy: composePullPolicyNever}
	}
	if len(builds) == 0 {
		return nil, nil, fmt.Errorf("%w: interop Compose project has no active builds", errControl)
	}
	return state, builds, nil
}

func (c *Controller) requirePlannedAliasesAbsent(
	ctx context.Context,
	state *composeImageState,
	builds []plannedComposeBuild,
) error {
	for _, build := range builds {
		exists, err := c.objectExists(ctx, "image", state.Owned.Images[build.name].Alias)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%w: planned image alias already exists for %s", errControl, build.name)
		}
	}
	return nil
}

func (c *Controller) executePlannedBuilds(
	ctx context.Context,
	state *composeImageState,
	builds []plannedComposeBuild,
) error {
	c.images = state
	if err := c.saveImageState(true); err != nil {
		return err
	}
	c.mutation = true
	if c.requiresCaptureDirectory() {
		if capErr := c.prepareCaptureDirectory(); capErr != nil {
			return capErr
		}
	}
	for _, build := range builds {
		buildErr := c.podmanStream(ctx, 10*time.Minute, build.args...)
		if joinErr := errors.Join(buildErr, c.checkpointAfterBuild(ctx, build.name)); joinErr != nil {
			return joinErr
		}
	}
	return nil
}

func composeBuildArgs(raw json.RawMessage, image, revision, buildDate, run string) ([]string, error) {
	var build struct {
		Context    string             `json:"context"`
		Dockerfile string             `json:"dockerfile"`
		Args       map[string]*string `json:"args"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&build); err != nil {
		return nil, fmt.Errorf("decode supported build options: %w", err)
	}
	if !filepath.IsAbs(build.Context) {
		return nil, fmt.Errorf("%w: build context must be a rendered absolute local path", errControl)
	}
	if build.Dockerfile == "" {
		build.Dockerfile = "Dockerfile"
	}
	if !filepath.IsAbs(build.Dockerfile) {
		build.Dockerfile = filepath.Join(build.Context, build.Dockerfile)
	}
	args := []string{
		"build", "--cpu-period", "100000", "--cpu-quota", "200000",
		"--memory", "2g", "--memory-swap", "2g", "--jobs", "1",
		"--tag", image, "--file", build.Dockerfile,
		"--label", "io.gobfd.image=" + image,
		"--label", "io.gobfd.run=" + run,
		"--label", "io.gobfd.test=" + composeImageKind,
	}
	if build.Args == nil {
		build.Args = make(map[string]*string)
	}
	stageKind := composeImageKind
	build.Args["VCS_REF"] = &revision
	build.Args["GIT_COMMIT"] = &revision
	build.Args["BUILD_DATE"] = &buildDate
	build.Args["GOBFD_IMAGE_RUN"] = &run
	build.Args["GOBFD_IMAGE_TEST"] = &stageKind
	for _, key := range slices.Sorted(maps.Keys(build.Args)) {
		value := build.Args[key]
		if value == nil {
			return nil, fmt.Errorf("%w: unresolved build argument %s", errControl, key)
		}
		args = append(args, "--build-arg", key+"="+*value)
	}
	return append(args, build.Context), nil
}
