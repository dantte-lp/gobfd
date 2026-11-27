// Command e2ectl owns repository E2E execution and artifact collection.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dantte-lp/gobfd/test/internal/e2erunner"
	"github.com/dantte-lp/gobfd/test/internal/reporoot"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root, err := reporoot.Find()
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
