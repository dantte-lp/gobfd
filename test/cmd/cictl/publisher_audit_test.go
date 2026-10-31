package main

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

type publisherWorkspaceFixture struct {
	closed      bool
	built       bool
	verifyCalls int
	verifyErr   error
	closeErr    error
	buildErr    error
}

func (*publisherWorkspaceFixture) Directory() (string, error)     { return "/owned/stage", nil }
func (*publisherWorkspaceFixture) Environment() ([]string, error) { return []string{"GOWORK=off"}, nil }

func (workspace *publisherWorkspaceFixture) Verify() error {
	workspace.verifyCalls++
	return workspace.verifyErr
}

func (workspace *publisherWorkspaceFixture) Build(context.Context) (string, error) {
	workspace.built = true
	return "/owned/stage/goreleaser", workspace.buildErr
}

func (workspace *publisherWorkspaceFixture) Close() error {
	workspace.closed = true
	return workspace.closeErr
}

type publisherRunnerFunc func(context.Context, cirunner.CommandSpec) error

func (runner publisherRunnerFunc) RunCommand(ctx context.Context, spec cirunner.CommandSpec) error {
	return runner(ctx, spec)
}

const publisherSelectedFixture = `{"Path":"github.com/dantte-lp/gobfd/tools/goreleaser","Main":true}
{"Path":"github.com/goreleaser/goreleaser/v2","Version":"v2.19.0","Replace":{"Path":"../source"}}
{"Path":"github.com/polydawn/refmt","Version":"v0.90.0","Replace":{"Path":"../refmt-local"}}`

func TestPublisherAuditSharesWorkspaceAndRetainsReportsOutsideCleanup(t *testing.T) {
	t.Parallel()
	workspace := &publisherWorkspaceFixture{}
	calls := 0
	err := run(context.Background(), []string{"publisher-audit"}, dependencies{
		getwd:   func() (string, error) { return "/repository", nil },
		environ: func() []string { return []string{"GH_TOKEN=not-for-child"} },
		stdout:  io.Discard,
		preparePublisher: func(_ context.Context, options cirunner.GoReleaserOptions) (publisherAuditWorkspace, error) {
			if options.Root != "/repository" || options.Runner == nil {
				t.Fatalf("prepare options = %#v", options)
			}
			return workspace, nil
		},
		specRunner: publisherRunnerFunc(func(_ context.Context, spec cirunner.CommandSpec) error {
			calls++
			if !slices.Equal(spec.Env, []string{"GOWORK=off"}) || workspace.closed {
				t.Fatalf("unbound child environment/lifecycle: %#v", spec)
			}
			if calls == 1 {
				if spec.Dir != "/owned/stage" || workspace.built {
					t.Fatalf("graph command = %#v", spec)
				}
				_, err := io.WriteString(spec.Stdout, publisherSelectedFixture)
				return err
			}
			wantArgs := []string{
				"run", "/repository/scripts/vuln-audit.go", "--publisher-directory", "/owned/stage",
				"--publisher-artifact", "/owned/stage/goreleaser",
				"--report-dir", "/repository/reports/security/publisher-audit",
			}
			if !workspace.built || spec.Dir != "/repository" || !slices.Equal(spec.Args, wantArgs) {
				t.Fatalf("audit command = %#v", spec)
			}
			return nil
		}),
	})
	if err != nil || calls != 2 || !workspace.closed || workspace.verifyCalls != 4 {
		t.Fatalf("audit = %v; calls = %d; workspace = %#v", err, calls, workspace)
	}
}

func TestPublisherAuditJoinsScannerIntegrityAndCleanupErrors(t *testing.T) {
	t.Parallel()
	scanErr := errors.New("scanner rejected findings")
	identityErr := errors.New("workspace changed")
	closeErr := errors.New("foreign replacement prevents cleanup")
	workspace := &publisherWorkspaceFixture{closeErr: closeErr}
	calls := 0
	err := run(context.Background(), []string{"publisher-audit"}, dependencies{
		getwd:   func() (string, error) { return "/repository", nil },
		environ: func() []string { return nil },
		preparePublisher: func(context.Context, cirunner.GoReleaserOptions) (publisherAuditWorkspace, error) {
			return workspace, nil
		},
		specRunner: publisherRunnerFunc(func(_ context.Context, spec cirunner.CommandSpec) error {
			calls++
			if calls == 1 {
				_, err := io.WriteString(spec.Stdout, publisherSelectedFixture)
				return err
			}
			workspace.verifyErr = identityErr
			return scanErr
		}),
	})
	if !errors.Is(err, scanErr) || !errors.Is(err, identityErr) || !errors.Is(err, closeErr) || !workspace.closed {
		t.Fatalf("audit error = %v; workspace = %#v", err, workspace)
	}
}

func TestPublisherAuditRejectsArgumentsBeforePreparing(t *testing.T) {
	t.Parallel()
	err := run(context.Background(), []string{"publisher-audit", "--artifact", "/foreign/binary"}, dependencies{})
	if err == nil {
		t.Fatal("external artifact accepted by managed publisher audit")
	}
}
