package interopproject

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (c *Controller) captureParent() string {
	return filepath.Join("reports/e2e", c.kind+"-compose")
}

func (c *Controller) requiresCaptureDirectory() bool {
	return c.kind == "observability" || c.kind == "rfc"
}

func (c *Controller) captureDirectory() string {
	name := ".pending"
	if c.images != nil {
		name = "run." + c.images.Owned.Run
	}
	return filepath.Join(c.root, c.captureParent(), name)
}

func (c *Controller) commandEnvironment() []string {
	environment := append(os.Environ(), ComposeEnvironment()...)
	switch c.kind {
	case "observability":
		environment = append(environment, "GOBFD_OBSERVABILITY_CAPTURE_DIR="+c.captureDirectory())
	case "rfc":
		environment = append(environment, "GOBFD_RFC_CAPTURE_DIR="+c.captureDirectory())
	}
	return environment
}

func (c *Controller) prepareCaptureDirectory() (returnErr error) {
	if !c.requiresCaptureDirectory() || c.images == nil || !imageRunPattern.MatchString(c.images.Owned.Run) {
		return fmt.Errorf("%w: capture directory requires a persisted image run", errControl)
	}
	root, openErr := os.OpenRoot(c.root)
	if openErr != nil {
		return fmt.Errorf("open repository root for capture directory: %w", openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close capture root handle: %w", closeErr))
		}
	}()
	if mkdirAllErr := root.MkdirAll(c.captureParent(), 0o700); mkdirAllErr != nil {
		return fmt.Errorf("create capture parent: %w", mkdirAllErr)
	}
	if valParentErr := validateOwnedDirectory(filepath.Join(c.root, c.captureParent()), os.Getuid()); valParentErr != nil {
		return fmt.Errorf("validate private capture parent: %w", valParentErr)
	}
	name := filepath.Join(c.captureParent(), filepath.Base(c.captureDirectory()))
	if mkdirErr := root.Mkdir(name, 0o700); mkdirErr != nil {
		return fmt.Errorf("create exclusive capture directory: %w", mkdirErr)
	}
	info, statErr := root.Lstat(name)
	if statErr != nil {
		return fmt.Errorf("bind created capture directory identity: %w", statErr)
	}
	c.captureInfo = info
	if valDirErr := c.validateCaptureDirectory(); valDirErr != nil {
		return valDirErr
	}
	if _, printErr := fmt.Fprintf(c.stdout, "Capture directory: %s\n", c.captureDirectory()); printErr != nil {
		return fmt.Errorf("write capture directory: %w", printErr)
	}
	return nil
}

func (c *Controller) validateCaptureDirectory() error {
	if err := validateOwnedDirectory(filepath.Join(c.root, c.captureParent()), os.Getuid()); err != nil {
		return err
	}
	if err := validateOwnedDirectory(c.captureDirectory(), os.Getuid()); err != nil {
		return err
	}
	info, statErr := os.Lstat(c.captureDirectory())
	if statErr != nil {
		return fmt.Errorf("inspect capture directory identity: %w", statErr)
	}
	if c.captureInfo == nil || !os.SameFile(c.captureInfo, info) {
		return fmt.Errorf("%w: capture directory identity changed before launch", errControl)
	}
	return nil
}
