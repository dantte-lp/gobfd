package interopproject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

const (
	maxImageStateBytes = 1 << 20
	maxComposeServices = 32
)

var imageRunPattern = regexp.MustCompile(`^[a-z2-7]{26,}$`)

type composeOwnedImage struct {
	Alias string `json:"alias"`
	ID    string `json:"id,omitempty"`
}

type composeOwnedService struct {
	Image      string `json:"image"`
	PullPolicy string `json:"pull_policy"`
}

type composeImageState struct {
	Services map[string]composeOwnedService `json:"services"`
	Owned    composeImageOwner              `json:"x-gobfd-owned-images"` //nolint:tagliatelle // Compose extension field.
}

type composeImageOwner struct {
	Schema  int                          `json:"schema"`
	Project string                       `json:"project"`
	Kind    string                       `json:"kind"`
	Root    string                       `json:"root"`
	Engine  string                       `json:"engine"`
	Run     string                       `json:"run"`
	Images  map[string]composeOwnedImage `json:"images"`
}

func ownedComposeAlias(project, service, run string) string {
	return "localhost/gobfd-interop-" + project + "-" + service + ":" + run
}

func (c *Controller) imageStatePath() (string, error) {
	if c.lock == nil || c.lock.file == nil {
		return "", fmt.Errorf("%w: image state requires the project lock", errControl)
	}
	return filepath.Join(filepath.Dir(c.lock.file.Name()), c.projectName+".images.json"), nil
}

func (c *Controller) imageEngine(ctx context.Context) (string, error) {
	const format = `{"hostname":{{json .Host.Hostname}},` +
		`"graph_root":{{json .Store.GraphRoot}},"run_root":{{json .Store.RunRoot}}}`
	output, err := c.podmanText(ctx, commandTimeout, "info", "--format", format)
	if err != nil {
		return "", err
	}
	var identity struct {
		Hostname  string `json:"hostname"`
		GraphRoot string `json:"graph_root"`
		RunRoot   string `json:"run_root"`
	}
	if decodeErr := decodeImageJSON([]byte(output), &identity); decodeErr != nil {
		return "", decodeErr
	}
	if identity.Hostname == "" || identity.GraphRoot == "" || identity.RunRoot == "" {
		return "", fmt.Errorf("%w: Podman engine identity is incomplete", errControl)
	}
	// Hash selectors rather than persisting potentially credential-bearing URLs.
	selectors := []string{identity.Hostname, identity.GraphRoot, identity.RunRoot, strconv.Itoa(os.Getuid())}
	envKeys := []string{
		"CONTAINER_HOST", "CONTAINER_CONNECTION", "DOCKER_HOST",
		"PODMAN_HOST", "HOME", "XDG_CONFIG_HOME",
	}
	for _, key := range envKeys {
		selectors = append(selectors, key, os.Getenv(key))
	}
	data, marshalErr := json.Marshal(selectors)
	if marshalErr != nil {
		return "", fmt.Errorf("encode image engine identity: %w", marshalErr)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func decodeImageJSON(data []byte, target any) error {
	if len(data) == 0 || len(data) > maxImageStateBytes {
		return fmt.Errorf("%w: image JSON exceeds its bounds or is empty", errControl)
	}
	if err := strictjson.Validate(data); err != nil {
		return fmt.Errorf("validate image JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode image JSON: %w", err)
	}
	return nil
}

func (state *composeImageState) validate(c *Controller, engine string) error {
	if !validOwnerHeader(state.Owned, c, engine, len(state.Services)) {
		return fmt.Errorf("%w: image receipt owner, engine or service set differs", errControl)
	}
	for name, image := range state.Owned.Images {
		if err := validateOwnedServiceEntry(state.Owned, state.Services, name, image); err != nil {
			return err
		}
	}
	return nil
}

func validOwnerHeader(owner composeImageOwner, c *Controller, engine string, serviceCount int) bool {
	if owner.Schema != 1 || owner.Project != c.projectName || owner.Kind != c.kind || owner.Root != c.root {
		return false
	}
	if owner.Engine == "" || owner.Engine != engine || !imageRunPattern.MatchString(owner.Run) {
		return false
	}
	return len(owner.Images) > 0 && len(owner.Images) <= maxComposeServices && len(owner.Images) == serviceCount
}

func validateOwnedServiceEntry(
	owner composeImageOwner,
	services map[string]composeOwnedService,
	name string,
	image composeOwnedImage,
) error {
	service, exists := services[name]
	if !exists || !projectNamePattern.MatchString(name) ||
		image.Alias != ownedComposeAlias(owner.Project, name, owner.Run) ||
		service.PullPolicy != composePullPolicyNever {
		return fmt.Errorf("%w: invalid owned image service %q", errControl, name)
	}
	expected := image.Alias
	if image.ID != "" {
		if !containertest.IsImageDigest(image.ID) {
			return fmt.Errorf("%w: invalid owned image ID for %s", errControl, name)
		}
		expected = image.ID
	}
	if service.Image != expected {
		return fmt.Errorf("%w: Compose image differs from its owner receipt for %s", errControl, name)
	}
	return nil
}

// ReadPrivateReceipt reads at most 1 MiB from a private file and checks its path identity before and after reading.
func ReadPrivateReceipt(path string) (_ []byte, _ os.FileInfo, returnErr error) {
	root, openRootErr := os.OpenRoot(filepath.Dir(path))
	if openRootErr != nil {
		return nil, nil, fmt.Errorf("open private receipt parent directory: %w", openRootErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close private receipt parent root: %w", closeErr))
		}
	}()
	directory, openDirErr := root.Open(".")
	if openDirErr != nil {
		return nil, nil, fmt.Errorf("open private receipt directory handle: %w", openDirErr)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close private receipt directory handle: %w", closeErr))
		}
	}()
	if valErr := ValidatePrivateReceiptDirectory(directory, filepath.Dir(path)); valErr != nil {
		return nil, nil, valErr
	}
	return readVerifiedReceiptFromRoot(root, filepath.Base(path))
}

func readVerifiedReceiptFromRoot(root *os.Root, baseName string) (_ []byte, _ os.FileInfo, returnErr error) {
	info, inspectErr := InspectPrivateReceipt(root, baseName)
	if inspectErr != nil {
		return nil, nil, inspectErr
	}
	if info.Size() <= 0 || info.Size() > maxImageStateBytes {
		return nil, nil, fmt.Errorf("%w: image receipt size is invalid", errControl)
	}
	file, openErr := root.Open(baseName)
	if openErr != nil {
		return nil, nil, fmt.Errorf("open private receipt file: %w", openErr)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close private receipt file: %w", closeErr))
		}
	}()
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) || !IsPrivateReceipt(opened) {
		return nil, nil, fmt.Errorf("image receipt changed before read: %w", errors.Join(statErr, ErrUnsafeReceipt))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxImageStateBytes+1))
	if readErr != nil || len(data) > maxImageStateBytes {
		return nil, nil, fmt.Errorf("read bounded image receipt: %w", errors.Join(readErr, errControl))
	}
	current, curErr := InspectPrivateReceipt(root, baseName)
	unchanged := curErr == nil &&
		os.SameFile(info, current) &&
		current.Size() == info.Size() &&
		current.ModTime().Equal(info.ModTime())
	if !unchanged {
		return nil, nil, fmt.Errorf("image receipt changed during read: %w", errors.Join(curErr, ErrUnsafeReceipt))
	}
	return data, info, nil
}

func (c *Controller) loadImageState(ctx context.Context) error {
	path, err := c.imageStatePath()
	if err != nil {
		return err
	}
	data, info, readErr := ReadPrivateReceipt(path)
	if errors.Is(readErr, os.ErrNotExist) && c.images == nil {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	engine, engErr := c.imageEngine(ctx)
	if engErr != nil {
		return engErr
	}
	var state composeImageState
	if decodeErr := decodeImageJSON(data, &state); decodeErr != nil {
		return decodeErr
	}
	if valErr := state.validate(c, engine); valErr != nil {
		return valErr
	}
	digest := sha256.Sum256(data)
	recovered := false
	if c.images != nil {
		if !c.imageStateUncertain && (!os.SameFile(c.imageStateInfo, info) || c.imageStateHash != digest) {
			return fmt.Errorf("%w: owned image receipt was replaced", ErrUnsafeReceipt)
		}
		var recErr error
		recovered, recErr = c.recoverImagePlan(&state)
		if recErr != nil {
			return recErr
		}
	}
	c.images, c.imageStateInfo, c.imageStateHash, c.imageStateUncertain = &state, info, digest, false
	if recovered {
		// Persist recovered IDs before cleanup can fail and outlive this process.
		return c.saveImageState(false)
	}
	return nil
}

func (c *Controller) recoverImagePlan(state *composeImageState) (bool, error) {
	if state.Owned.Run != c.images.Owned.Run || len(state.Owned.Images) != len(c.images.Owned.Images) {
		return false, fmt.Errorf("%w: image recovery plan differs", ErrUnsafeReceipt)
	}
	recovered := false
	for name, known := range c.images.Owned.Images {
		stored, exists := state.Owned.Images[name]
		if !exists || stored.Alias != known.Alias || (known.ID != "" && stored.ID != "" && known.ID != stored.ID) {
			return false, fmt.Errorf("%w: image recovery identity differs for %s", ErrUnsafeReceipt, name)
		}
		// A failed checkpoint must not discard an already validated live ID.
		if known.ID != "" && stored.ID == "" {
			stored.ID = known.ID
			state.Owned.Images[name] = stored
			state.Services[name] = composeOwnedService{Image: known.ID, PullPolicy: composePullPolicyNever}
			recovered = true
		}
	}
	return recovered, nil
}

func (c *Controller) saveImageState(create bool) error {
	path, err := c.imageStatePath()
	if err != nil {
		return err
	}
	if c.images == nil {
		return fmt.Errorf("%w: no owned image plan to persist", errControl)
	}
	if valErr := c.images.validate(c, c.images.Owned.Engine); valErr != nil {
		return valErr
	}
	data, marshalErr := json.Marshal(c.images)
	if marshalErr != nil {
		return fmt.Errorf("encode owned image receipt: %w", marshalErr)
	}
	if len(data) > maxImageStateBytes {
		return fmt.Errorf("%w: encoded image receipt exceeds its bound", errControl)
	}
	mutated, writeErr := c.persistImageReceiptBytes(path, data, create)
	if writeErr != nil {
		if mutated {
			c.imageStateUncertain = true
		}
		return writeErr
	}
	published, info, readBackErr := ReadPrivateReceipt(path)
	if readBackErr != nil {
		c.imageStateUncertain = true
		return readBackErr
	}
	if !bytes.Equal(data, published) {
		c.imageStateUncertain = true
		return fmt.Errorf("%w: published image receipt differs", ErrUnsafeReceipt)
	}
	c.imageStateInfo, c.imageStateHash, c.imageStateUncertain = info, sha256.Sum256(data), false
	return nil
}

func (c *Controller) persistImageReceiptBytes(path string, data []byte, create bool) (bool, error) {
	if create {
		return true, CreatePrivateReceipt(path, data)
	}
	current, info, readErr := ReadPrivateReceipt(path)
	if readErr != nil {
		return false, readErr
	}
	if !os.SameFile(c.imageStateInfo, info) || c.imageStateHash != sha256.Sum256(current) {
		return false, fmt.Errorf("%w: image receipt changed before checkpoint", ErrUnsafeReceipt)
	}
	return true, ReplacePrivateReceipt(path, data)
}
