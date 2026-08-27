// Command e2ectl owns repository E2E execution and artifact collection.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dantte-lp/gobfd/test/internal/e2erunner"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root, err := repositoryRoot()
	if err == nil {
		err = e2erunner.Run(ctx, root, os.Args[1:], os.Stdout, os.Stderr)
	}
	if err == nil {
		return 0
	}

	fmt.Fprintln(os.Stderr, err)
	if exitErr, ok := errors.AsType[*e2erunner.ExitError](err); ok {
		return exitErr.Code
	}
	return 1
}

func repositoryRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(wd, "go.mod")); statErr == nil {
			return wd, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", fmt.Errorf("find repository root from %s: %w", wd, os.ErrNotExist)
		}
		wd = parent
	}
}
