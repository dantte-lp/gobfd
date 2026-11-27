package e2erunner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

type ownedRuntime struct {
	Name         string `json:"name"`
	Run          string `json:"run"`
	Kind         string `json:"kind"`
	Project      string `json:"project"`
	ID           string `json:"id,omitempty"`
	attempted    bool
	checkpointed bool
}

func (r *runner) newRuntime(kind string) *ownedRuntime {
	run := strings.ToLower(rand.Text())
	return &ownedRuntime{Name: "e2e-" + kind + "-" + run, Run: run, Kind: "e2e-" + kind, Project: r.devProject}
}

func (r *runner) createRuntime(ctx context.Context, owned *ownedRuntime, args []string) error {
	if err := r.writeRuntimeReceipt("runtime-plan.json", owned); err != nil {
		return err
	}
	exists, err := r.runtimeExists(ctx, owned.Name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: planned runtime name already exists", errUsage)
	}
	args = append([]string{
		podmanCommand, "create", "--label", "io.gobfd.run=" + owned.Run,
		"--label", "io.gobfd.test=" + owned.Kind, "--label", "io.gobfd.project=" + owned.Project,
	}, args[2:]...)
	var output strings.Builder
	owned.attempted = true
	createErr := r.command(ctx, commandTimeout, &output, r.stderr, args...)
	id := strings.TrimSpace(output.String())
	if !imageIDPattern.MatchString(id) {
		if createErr != nil {
			return createErr
		}
		return fmt.Errorf("%w: create returned an invalid runtime container ID", errUsage)
	}
	bindCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
	defer cancel()
	return errors.Join(createErr, r.bindRuntime(bindCtx, owned, id))
}

func (r *runner) writeRuntimeReceipt(name string, owned *ownedRuntime) error {
	data, err := json.Marshal(owned)
	if err != nil {
		return fmt.Errorf("encode runtime receipt: %w", err)
	}
	path := filepath.Join(r.reportDir, name)
	if err := interopproject.CreatePrivateReceipt(path, data); err != nil {
		return fmt.Errorf("persist %s: %w", name, err)
	}
	return nil
}

func (r *runner) runtimeExists(ctx context.Context, reference string) (bool, error) {
	err := r.command(ctx, commandTimeout, io.Discard, r.stderr, podmanCommand, "container", "exists", reference)
	if err == nil {
		return true, nil
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok && exitErr.Code == 1 {
		return false, nil
	}
	return false, err
}

func (r *runner) bindRuntime(ctx context.Context, owned *ownedRuntime, reference string) error {
	output, err := r.commandText(
		ctx, podmanCommand, "inspect", "--type", "container", "--format",
		`{"id":{{json .ID}},"labels":{{json .Config.Labels}}}`, reference,
	)
	if err != nil {
		return err
	}
	if validateErr := strictjson.Validate([]byte(output)); validateErr != nil {
		return fmt.Errorf("validate runtime inspection: %w", validateErr)
	}
	var metadata struct {
		ID     string            `json:"id"`
		Labels map[string]string `json:"labels"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&metadata); decodeErr != nil {
		return fmt.Errorf("decode runtime inspection: %w", decodeErr)
	}
	if !matchesOwnedRuntime(owned, reference, metadata.ID, metadata.Labels) {
		return fmt.Errorf("%w: refuse foreign or changed runtime container", errUsage)
	}
	owned.ID = metadata.ID
	return r.checkpointBoundRuntime(owned)
}

func matchesOwnedRuntime(owned *ownedRuntime, reference, id string, labels map[string]string) bool {
	if !imageIDPattern.MatchString(id) {
		return false
	}
	if owned.ID != "" && id != owned.ID {
		return false
	}
	if imageIDPattern.MatchString(reference) && id != reference {
		return false
	}
	return labels["io.gobfd.run"] == owned.Run &&
		labels["io.gobfd.test"] == owned.Kind &&
		labels["io.gobfd.project"] == owned.Project
}

func (r *runner) checkpointBoundRuntime(owned *ownedRuntime) error {
	if owned.checkpointed {
		return nil
	}
	if err := r.writeRuntimeReceipt("runtime-container.json", owned); err != nil {
		return r.recoverPublishedRuntimeReceipt(owned, err)
	}
	owned.checkpointed = true
	return nil
}

func (r *runner) recoverPublishedRuntimeReceipt(owned *ownedRuntime, writeErr error) error {
	// An atomic create may publish before returning a sync/cleanup error.
	data, _, readErr := interopproject.ReadPrivateReceipt(filepath.Join(r.reportDir, "runtime-container.json"))
	expected, marshalErr := json.Marshal(owned)
	if readErr != nil || marshalErr != nil || !bytes.Equal(data, expected) {
		var wrappedMarshal error
		if marshalErr != nil {
			wrappedMarshal = fmt.Errorf("marshal expected runtime receipt: %w", marshalErr)
		}
		var wrappedRead error
		if readErr != nil {
			wrappedRead = fmt.Errorf("read published runtime receipt: %w", readErr)
		}
		return errors.Join(writeErr, wrappedRead, wrappedMarshal)
	}
	// The published identity is recoverable; retain the original failure for the caller.
	owned.checkpointed = true
	return writeErr
}

func (r *runner) cleanupRuntime(ctx context.Context, owned *ownedRuntime) error {
	if !owned.attempted {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
	defer cancel()
	reference := owned.ID
	if reference == "" {
		reference = owned.Name
	}
	exists, err := r.runtimeExists(cleanupCtx, reference)
	if err != nil || !exists {
		return err
	}
	if bindErr := r.bindRuntime(cleanupCtx, owned, reference); bindErr != nil {
		return bindErr
	}
	artifactErr := errors.Join(
		r.runtimeArtifact(cleanupCtx, containersJSONName, "inspect", "--type", "container", owned.ID),
		r.runtimeArtifact(cleanupCtx, containersLogName, "logs", owned.ID),
	)
	removeErr := r.command(cleanupCtx, commandTimeout, io.Discard, r.stderr, podmanCommand, "rm", "-f", "--", owned.ID)
	exists, err = r.runtimeExists(cleanupCtx, owned.ID)
	if err == nil && exists {
		err = fmt.Errorf("%w: runtime container %s remains after removal", errUsage, owned.ID)
	}
	return errors.Join(artifactErr, removeErr, err)
}

func (r *runner) runtimeArtifact(ctx context.Context, name string, args ...string) error {
	file, err := secureFile(filepath.Join(r.reportDir, name))
	if err != nil {
		return err
	}
	err = r.command(ctx, commandTimeout, file, r.stderr, append([]string{podmanCommand}, args...)...)
	return errors.Join(err, r.closeArtifact(file))
}
