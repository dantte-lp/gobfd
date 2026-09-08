package interopproject

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"
)

func (c *Controller) buildBase(ctx context.Context) error {
	if os.Getenv("COMPOSE_COMPATIBILITY") != "" {
		return fmt.Errorf("%w: COMPOSE_COMPATIBILITY is unsupported for base builds", errControl)
	}
	revision, buildDate, err := BuildMetadata(ctx, c.root, os.Getenv("GOBFD_BUILD_REVISION"))
	if err != nil {
		return err
	}
	output, err := c.podmanText(ctx, commandTimeout,
		"compose", "-p", c.projectName, "-f", c.composeFile, "config", "--format", "json")
	if err != nil {
		return fmt.Errorf("render base Compose builds: %w", err)
	}
	var rendered struct {
		Services map[string]struct {
			Image    string          `json:"image"`
			Platform string          `json:"platform"`
			Build    json.RawMessage `json:"build"`
		} `json:"services"`
	}
	if err := decodeSingleJSON([]byte(output), &rendered); err != nil {
		return fmt.Errorf("decode base Compose builds: %w", err)
	}
	var builds [][]string
	for _, name := range slices.Sorted(maps.Keys(rendered.Services)) {
		service := rendered.Services[name]
		if len(service.Build) == 0 {
			continue
		}
		if service.Platform != "" {
			return fmt.Errorf("%w: base Compose service %s build platform is unsupported", errControl, name)
		}
		image := service.Image
		if image == "" {
			// Compose v5.5.0 pkg/api.GetImageNameOrDefault uses project-service.
			image = c.projectName + "-" + name
		}
		args, err := baseBuildArgs(service.Build, image, revision, buildDate)
		if err != nil {
			return fmt.Errorf("validate base Compose service %s build: %w", name, err)
		}
		builds = append(builds, args)
	}
	if len(builds) == 0 {
		return fmt.Errorf("%w: base Compose project has no active builds", errControl)
	}
	// Validate every build before mutation; unsupported Compose options fail closed.
	c.mutation = true
	for _, args := range builds {
		if err := c.podmanStream(ctx, 10*time.Minute, args...); err != nil {
			return err
		}
	}
	return nil
}

func baseBuildArgs(raw json.RawMessage, image, revision, buildDate string) ([]string, error) {
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
	}
	if build.Args == nil {
		build.Args = make(map[string]*string)
	}
	build.Args["VCS_REF"] = &revision
	build.Args["BUILD_DATE"] = &buildDate
	for _, key := range slices.Sorted(maps.Keys(build.Args)) {
		value := build.Args[key]
		if value == nil {
			return nil, fmt.Errorf("%w: unresolved build argument %s", errControl, key)
		}
		args = append(args, "--build-arg", key+"="+*value)
	}
	return append(args, build.Context), nil
}
