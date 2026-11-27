package interop_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type fakeComposeImage struct {
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

func fakeOwnedImageCommand(args []string) (int, bool) {
	if len(args) == 0 || (args[0] != "info" && args[0] != "image" && args[0] != "build") {
		return 0, false
	}
	if args[0] == "info" {
		fmt.Fprintln(os.Stdout, `{"hostname":"fake-engine","graph_root":"/fake/storage","run_root":"/fake/run"}`)
		return 0, true
	}
	statePath := filepath.Join(os.Getenv("INTEROP_FAKE_STATE_DIR"), "images.json")
	images, loadCode := loadFakeComposeImages(statePath)
	if loadCode != 0 {
		return loadCode, true
	}
	if args[0] == "build" {
		return handleFakeComposeBuild(statePath, args, images), true
	}
	return handleFakeComposeImageSubcommand(statePath, args, images), true
}

func loadFakeComposeImages(statePath string) (map[string]fakeComposeImage, int) {
	images := make(map[string]fakeComposeImage)
	data, err := os.ReadFile(statePath)
	if err == nil {
		if json.Unmarshal(data, &images) != nil {
			return nil, 9
		}
		return images, 0
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, 9
	}
	return images, 0
}

func saveFakeComposeImages(statePath string, images map[string]fakeComposeImage) int {
	data, err := json.Marshal(images)
	if err != nil || os.WriteFile(statePath, data, 0o600) != nil {
		return 9
	}
	return 0
}

func handleFakeComposeBuild(statePath string, args []string, images map[string]fakeComposeImage) int {
	index := slices.Index(args, "--tag")
	if index < 0 || index+1 >= len(args) {
		return 9
	}
	alias := args[index+1]
	if alias == os.Getenv("INTEROP_FAKE_PROTECTED_IMAGE") {
		if code := fakeStateMarker("protected-image-overwritten"); code != 0 {
			return code
		}
	}
	if os.Getenv("INTEROP_FAKE_BUILD_FAIL") == "true" {
		return 17
	}
	labels, buildArgs, ok := parseFakeBuildFlags(args)
	if !ok {
		return 9
	}
	imageCountBefore := len(images)
	images[alias] = fakeComposeImage{
		ID:     fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(alias))),
		Labels: labels,
	}
	if os.Getenv("INTEROP_FAKE_EMIT_STAGE_IMAGE") == "true" &&
		buildArgs["GOBFD_IMAGE_RUN"] != "" && buildArgs["GOBFD_IMAGE_TEST"] != "" {
		stageKey := "stage:" + alias
		images[stageKey] = fakeComposeImage{
			ID: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(stageKey))),
			Labels: map[string]string{
				"io.gobfd.stage-run":  buildArgs["GOBFD_IMAGE_RUN"],
				"io.gobfd.stage-test": buildArgs["GOBFD_IMAGE_TEST"],
			},
		}
	}
	if code := saveFakeComposeImages(statePath, images); code != 0 {
		return code
	}
	return postFakeComposeBuildHooks(imageCountBefore + 1)
}

func parseFakeBuildFlags(args []string) (map[string]string, map[string]string, bool) {
	labels := make(map[string]string)
	buildArgs := make(map[string]string)
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--label":
			key, value, ok := strings.Cut(args[i+1], "=")
			if !ok {
				return nil, nil, false
			}
			labels[key] = value
		case "--build-arg":
			key, value, ok := strings.Cut(args[i+1], "=")
			if !ok {
				return nil, nil, false
			}
			buildArgs[key] = value
		}
	}
	return labels, buildArgs, true
}

func postFakeComposeBuildHooks(builtTargetCount int) int {
	if replacePath := os.Getenv("INTEROP_FAKE_REPLACE_RECEIPT"); replacePath != "" {
		data, err := os.ReadFile(replacePath)
		if err != nil {
			return 9
		}
		data = append(data, '\n')
		if os.WriteFile(replacePath+".replacement", data, 0o600) != nil ||
			os.Rename(replacePath+".replacement", replacePath) != nil {
			return 9
		}
	}
	if os.Getenv("INTEROP_FAKE_BUILD_FAIL_AFTER_IMAGE") == "true" {
		return 17
	}
	if os.Getenv("INTEROP_FAKE_FAIL_SECOND_IMAGE") == "true" && builtTargetCount == 2 {
		return 17
	}
	return 0
}

func handleFakeComposeImageSubcommand(
	statePath string,
	args []string,
	images map[string]fakeComposeImage,
) int {
	if len(args) < 2 {
		return 9
	}
	if args[1] == "ls" {
		return handleFakeComposeImageList(args, images)
	}
	if len(args) < 3 {
		return 9
	}
	reference := args[len(args)-1]
	var key string
	var image fakeComposeImage
	for candidate, value := range images {
		if candidate == reference || value.ID == reference {
			key, image = candidate, value
			break
		}
	}
	switch args[1] {
	case "exists":
		if key == "" {
			return 1
		}
		return 0
	case "inspect":
		if key == "" || strings.Contains(strings.Join(args, " "), ".Id") {
			return 1
		}
		if encErr := json.NewEncoder(os.Stdout).Encode(image); encErr != nil {
			return 9
		}
		return 0
	case "rm":
		if os.Getenv("INTEROP_FAKE_IMAGE_REMOVE_FAIL") == "true" {
			return 19
		}
		if key == "" || reference != image.ID || !slices.Contains(args, "--no-prune") {
			return 9
		}
		delete(images, key)
		return saveFakeComposeImages(statePath, images)
	default:
		return 9
	}
}

func handleFakeComposeImageList(args []string, images map[string]fakeComposeImage) int {
	filters := make(map[string]string)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--filter" {
			kv, ok := strings.CutPrefix(args[i+1], "label=")
			if !ok {
				return 9
			}
			k, v, hasEq := strings.Cut(kv, "=")
			if !hasEq {
				return 9
			}
			filters[k] = v
		}
	}
	for _, image := range images {
		matches := true
		for k, want := range filters {
			if image.Labels[k] != want {
				matches = false
				break
			}
		}
		if matches {
			fmt.Fprintln(os.Stdout, image.ID)
		}
	}
	return 0
}

func fakeOwnedImageOverride(args []string) ([]string, error) {
	if len(args) < 2 || args[0] != "-f" {
		return args, nil
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return nil, err
	}
	var state struct {
		Services map[string]struct {
			Image      string `json:"image"`
			PullPolicy string `json:"pull_policy"`
		} `json:"services"`
	}
	if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
		return nil, unmarshalErr
	}
	if len(state.Services) == 0 {
		return nil, errors.New("empty image override")
	}
	for _, service := range state.Services {
		if !strings.HasPrefix(service.Image, "sha256:") || service.PullPolicy != "never" {
			return nil, errors.New("override is not bound to an image ID")
		}
		if code, _ := fakeOwnedImageCommand([]string{"image", "exists", service.Image}); code != 0 {
			return nil, errors.New("override image is absent")
		}
	}
	return args[2:], nil
}
