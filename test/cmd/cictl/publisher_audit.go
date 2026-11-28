package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
	"github.com/dantte-lp/gobfd/test/internal/dependencyinventory"
)

type publisherAuditWorkspace interface {
	dependencyinventory.PreparedPublisherWorkspace
	Build(ctx context.Context) (string, error)
	Close() error
}

const (
	publisherAuditTimeout   = 30 * time.Minute
	publisherAuditWaitDelay = 5 * time.Second
)

func runPublisherAudit(ctx context.Context, arguments []string, deps dependencies) error {
	if err := rejectArguments("publisher-audit", arguments); err != nil {
		return err
	}
	auditCtx, cancel := context.WithTimeout(ctx, publisherAuditTimeout)
	defer cancel()
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("publisher audit repository: %w", err)
	}
	prepare := deps.preparePublisher
	if prepare == nil {
		prepare = func(ctx context.Context, options cirunner.GoReleaserOptions) (publisherAuditWorkspace, error) {
			workspace, prepareErr := cirunner.PrepareGoReleaserWorkspace(ctx, options)
			if prepareErr != nil {
				return nil, fmt.Errorf("prepare managed publisher: %w", prepareErr)
			}
			return workspace, nil
		}
	}
	workspace, err := prepare(auditCtx, cirunner.GoReleaserOptions{
		Root: root, InstallDir: filepath.Join(os.TempDir(), ".gobfd-publisher-audit-"+rand.Text()),
		Environment: deps.environ(), Runner: deps.specRunner,
	})
	if err != nil {
		return fmt.Errorf("prepare publisher audit: %w", err)
	}
	auditErr := auditPreparedPublisher(auditCtx, root, workspace, deps)
	return errors.Join(auditErr, workspace.Close())
}

func auditPreparedPublisher(ctx context.Context, root string, workspace publisherAuditWorkspace,
	deps dependencies,
) error {
	if _, err := dependencyinventory.CollectGoReleaserGraph(ctx, workspace, deps.specRunner); err != nil {
		return fmt.Errorf("collect publisher audit graph: %w", err)
	}
	artifact, err := workspace.Build(ctx)
	if err != nil {
		return fmt.Errorf("build publisher audit artifact: %w", err)
	}
	directory, err := workspace.Directory()
	if err != nil {
		return fmt.Errorf("publisher audit directory: %w", err)
	}
	environment, err := workspace.Environment()
	if err != nil {
		return fmt.Errorf("publisher audit environment: %w", err)
	}
	if err := workspace.Verify(); err != nil {
		return fmt.Errorf("verify publisher audit workspace: %w", err)
	}
	runErr := deps.specRunner.RunCommand(ctx, cirunner.CommandSpec{
		Name: "go", Args: []string{
			"run", filepath.Join(root, "scripts", "vuln-audit.go"),
			"--publisher-directory", directory, "--publisher-artifact", artifact,
			"--report-dir", filepath.Join(root, "reports", "security", "publisher-audit"),
		},
		Dir: root, Env: environment, Stdout: deps.stdout, WaitDelay: publisherAuditWaitDelay,
	})
	return errors.Join(runErr, ctx.Err(), workspace.Verify())
}
