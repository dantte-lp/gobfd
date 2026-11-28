package cirunner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/npmbootstrap"
)

type releasePleaseRunnerFunc func(context.Context, CommandSpec) error

func (f releasePleaseRunnerFunc) RunCommand(ctx context.Context, spec CommandSpec) error {
	return f(ctx, spec)
}

func TestReleasePleaseRejectsUntrustedContextBeforeCredentialsAndCommands(t *testing.T) {
	t.Parallel()

	for _, change := range []func(*ReleasePleaseOptions){
		func(options *ReleasePleaseOptions) { options.Repository = "foreign/gobfd" },
		func(options *ReleasePleaseOptions) { options.Ref = "refs/heads/dev" },
		func(options *ReleasePleaseOptions) { options.Event = "pull_request_target" },
		func(options *ReleasePleaseOptions) { options.Event = "pull_request" },
	} {
		options := ReleasePleaseOptions{
			Root: "/missing", Repository: releasePleaseRepository, Ref: releasePleaseRef, Event: "push",
			Environment: func() []string { t.Fatal("untrusted context read environment"); return nil },
			Token:       func() string { t.Fatal("untrusted context read token"); return "" },
			Runner: releasePleaseRunnerFunc(func(context.Context, CommandSpec) error {
				t.Fatal("untrusted context executed a command")
				return nil
			}),
		}
		change(&options)
		if err := PrepareReleasePleasePullRequest(t.Context(), options); !errors.Is(err, errInvalidConfig) {
			t.Fatalf("untrusted release automation error = %v, want invalid configuration", err)
		}
	}
}

func TestReleasePleaseAuditsBeforeTokenAndUsesPrivateNativeTokenFile(t *testing.T) {
	t.Parallel()

	for _, event := range []string{"push", "workflow_dispatch"} {
		t.Run(event, func(t *testing.T) {
			t.Parallel()

			options := releasePleaseFixture(t)
			options.Event = event
			const token = "private-release-fixture-value"
			reads, calls := 0, 0
			options.Token = func() string { reads++; return token }
			var directory string
			options.Runner = releasePleaseRunnerFunc(func(ctx context.Context, spec CommandSpec) error {
				calls++
				directory = spec.Dir
				assertReleasePleaseCommand(ctx, t, spec, options.Root, token)
				cache := filepath.Join(directory, "node-compile-cache")
				if err := os.MkdirAll(cache, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cache, "fixture.cache"), []byte("cached upstream code"), 0o600); err != nil {
					t.Fatal(err)
				}
				if calls < 3 {
					if reads != 0 {
						t.Fatal("npm received token authority before completing its audit")
					}
					if _, err := os.Lstat(filepath.Join(directory, "github-token")); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("npm could access a token file: %v", err)
					}
					want := "ci"
					if calls == 2 {
						want = "audit"
					}
					if spec.Name != "node" || len(spec.Args) < 2 || spec.Args[1] != want ||
						spec.Args[0] != filepath.Join(directory, filepath.FromSlash(npmbootstrap.CLIPath)) {
						t.Fatalf("stage %d command = %s %q", calls, spec.Name, spec.Args)
					}
					return nil
				}
				assertReleasePleaseToken(t, spec, token)
				return nil
			})
			if err := prepareReleasePleaseTest(t.Context(), options); err != nil {
				t.Fatalf("prepare release PR: %v", err)
			}
			if calls != 3 || reads != 1 {
				t.Fatalf("commands = %d, token reads = %d; want 3 and 1", calls, reads)
			}
			assertReleasePleaseCleanup(t, directory)
		})
	}
}

func TestReleasePleaseFailureStopsLaterStagesAndCleansPrivateFiles(t *testing.T) {
	t.Parallel()

	for _, failAt := range []int{1, 2, 3} {
		options := releasePleaseFixture(t)
		want := errors.New("injected child failure")
		calls, reads := 0, 0
		var directory string
		options.Token = func() string { reads++; return "release-fixture-value" }
		options.Runner = releasePleaseRunnerFunc(func(_ context.Context, spec CommandSpec) error {
			calls++
			directory = spec.Dir
			if calls == failAt {
				return want
			}
			return nil
		})
		if err := prepareReleasePleaseTest(t.Context(), options); !errors.Is(err, want) {
			t.Fatalf("stage %d error = %v, want original failure", failAt, err)
		}
		if calls != failAt || (reads != 0) != (failAt == 3) {
			t.Fatalf("stage %d commands = %d, token reads = %d", failAt, calls, reads)
		}
		assertReleasePleaseCleanup(t, directory)
	}
}

func TestReleasePleaseRejectsInputDriftBeforeToken(t *testing.T) {
	t.Parallel()

	for _, name := range releasePleaseInputNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			options := releasePleaseFixture(t)
			options.Token = func() string { t.Fatal("modified inputs received token authority"); return "" }
			calls := 0
			var directory string
			options.Runner = releasePleaseRunnerFunc(func(_ context.Context, spec CommandSpec) error {
				calls++
				directory = spec.Dir
				if calls == 2 {
					if err := os.WriteFile(filepath.Join(directory, name), []byte("modified"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			})
			if err := prepareReleasePleaseTest(t.Context(), options); !errors.Is(err, errInvalidConfig) || calls != 2 {
				t.Fatalf("modified input error = %v, commands = %d; want rejection before token access", err, calls)
			}
			assertReleasePleaseCleanup(t, directory)
		})
	}
}

func TestReleasePleaseCancellationStopsBeforeTokenAndCleansPrivateFiles(t *testing.T) {
	t.Parallel()

	options := releasePleaseFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	var directory string
	options.Token = func() string { t.Fatal("cancelled execution read token"); return "" }
	options.Runner = releasePleaseRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		calls++
		directory = spec.Dir
		cancel()
		return nil
	})
	if err := prepareReleasePleaseTest(ctx, options); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled release automation error = %v, calls = %d", err, calls)
	}
	assertReleasePleaseCleanup(t, directory)
}

func TestReleasePleaseRejectsLinkedManifestBeforeInstall(t *testing.T) {
	t.Parallel()

	options := releasePleaseFixture(t)
	manifest := filepath.Join(options.Root, "tools", "release-please", "package.json")
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(options.Root, "tools", "release-please", "package-lock.json")
	if err := os.Symlink(target, manifest); err != nil {
		t.Fatal(err)
	}
	options.Environment = func() []string { t.Fatal("linked manifest read environment"); return nil }
	options.Token = func() string { t.Fatal("linked manifest read token"); return "" }
	options.Runner = releasePleaseRunnerFunc(func(context.Context, CommandSpec) error {
		t.Fatal("linked manifest executed npm")
		return nil
	})
	if err := prepareReleasePleaseTest(t.Context(), options); !errors.Is(err, errInvalidConfig) {
		t.Fatalf("linked manifest error = %v, want invalid configuration", err)
	}
}

func TestReleasePleaseRejectsInvalidTokenAfterAudit(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"", "value\nwith-line-break", strings.Repeat("x", releasePleaseTokenLimit+1)} {
		options := releasePleaseFixture(t)
		calls := 0
		var directory string
		options.Token = func() string { return token }
		options.Runner = releasePleaseRunnerFunc(func(_ context.Context, spec CommandSpec) error {
			calls++
			directory = spec.Dir
			return nil
		})
		if err := prepareReleasePleaseTest(t.Context(), options); !errors.Is(err, errInvalidConfig) || calls != 2 {
			t.Fatalf("invalid token error = %v, calls = %d; want rejection after audit", err, calls)
		}
		assertReleasePleaseCleanup(t, directory)
	}
}

func releasePleaseFixture(t *testing.T) ReleasePleaseOptions {
	t.Helper()

	root := t.TempDir()
	directory := filepath.Join(root, "tools", "release-please")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range releasePleaseInputNames() {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "tools", "release-please", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return ReleasePleaseOptions{
		Root: root, Repository: releasePleaseRepository, Ref: releasePleaseRef, Event: "push",
		Environment: func() []string {
			return []string{
				"PATH=/usr/bin", "GH_TOKEN=private-release-fixture-value", "GITHUB_TOKEN=other-sensitive-value",
				"NODE_OPTIONS=--require=untrusted", "NODE_PATH=/untrusted", "NODE_COMPILE_CACHE=/untrusted/cache",
				"NPM_TOKEN=registry-sensitive-value",
				"npm_config_registry=https://untrusted.invalid", "NPM_CONFIG__AUTH=registry-sensitive-value",
				"ACTIONS_RUNTIME_TOKEN=runtime-sensitive-value", "HOME=/untrusted",
			}
		},
		Token: func() string { return "release-fixture-value" },
	}
}

func assertReleasePleaseCommand(ctx context.Context, t *testing.T, spec CommandSpec, root, token string) {
	t.Helper()

	deadline, bounded := ctx.Deadline()
	if !bounded || time.Until(deadline) > releasePleaseTimeout || spec.WaitDelay != time.Second {
		t.Fatal("release automation subprocess is not context and pipe bounded")
	}
	if strings.HasPrefix(spec.Dir, root+string(filepath.Separator)) || spec.Dir == root {
		t.Fatal("release automation used the checkout as its mutable directory")
	}
	for _, value := range append(slices.Clone(spec.Args), spec.Env...) {
		if strings.Contains(value, token) || strings.Contains(value, "sensitive-value") ||
			strings.Contains(value, "untrusted") {
			t.Fatal("child arguments or environment retained credential or injection authority")
		}
	}
	if !slices.Contains(spec.Env, "NPM_CONFIG_REGISTRY="+releasePleaseRegistry) ||
		!slices.Contains(spec.Env, "HOME="+spec.Dir) {
		t.Fatal("release automation did not isolate npm configuration")
	}
	cacheVariables := 0
	for _, entry := range spec.Env {
		if strings.HasPrefix(entry, "NODE_COMPILE_CACHE=") {
			cacheVariables++
			if entry != "NODE_COMPILE_CACHE="+filepath.Join(spec.Dir, "node-compile-cache") {
				t.Fatal("node compile cache escaped the private cleanup directory")
			}
		}
	}
	if cacheVariables != 1 {
		t.Fatal("node compile cache authority was missing or duplicated")
	}
	if spec.Name == "node" && strings.HasSuffix(spec.Args[0], filepath.FromSlash(npmbootstrap.CLIPath)) &&
		(!slices.Contains(spec.Args, "--registry="+releasePleaseRegistry) ||
			(spec.Args[1] == "ci" && !slices.Contains(spec.Args, "--ignore-scripts"))) {
		t.Fatal("npm did not enforce the public registry and disabled install scripts")
	}
}

func assertReleasePleaseToken(t *testing.T, spec CommandSpec, token string) {
	t.Helper()

	path := filepath.Join(spec.Dir, "github-token")
	want := []string{
		filepath.Join(spec.Dir, "node_modules", "release-please", "build", "src", "bin", "release-please.js"),
		"release-pr", "--repo-url=" + releasePleaseRepository, "--target-branch=release/v1.0",
		"--config-file=release-please-config.json", "--manifest-file=.release-please-manifest.json", "--token=" + path,
	}
	if spec.Name != "node" || !slices.Equal(spec.Args, want) || spec.Stdout != io.Discard || spec.Stderr != io.Discard {
		t.Fatal("upstream release command authority changed")
	}
	for name, mode := range map[string]os.FileMode{spec.Dir: 0o700, path: 0o600} {
		info, err := os.Lstat(name)
		if err != nil || info.Mode().Perm() != mode || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("private credential path %s lacks mode %04o: %v", name, mode, err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != token {
		t.Fatal("native token file was not populated before invoking the upstream CLI")
	}
}

func assertReleasePleaseCleanup(t *testing.T, directory string) {
	t.Helper()

	if directory == "" {
		t.Fatal("release automation never created a private directory")
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release automation retained its private token or module tree: %v", err)
	}
}

type releasePleaseTransport func(*http.Request) (*http.Response, error)

func (f releasePleaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestReleasePleaseBootstrapFailureStopsBeforeCredentialsAndCommands(t *testing.T) {
	t.Parallel()
	options := releasePleaseFixture(t)
	want := errors.New("injected registry failure")
	options.HTTPClient = &http.Client{Transport: releasePleaseTransport(func(*http.Request) (*http.Response, error) {
		return nil, want
	})}
	options.Token = func() string { t.Fatal("failed bootstrap read token"); return "" }
	options.Runner = releasePleaseRunnerFunc(func(context.Context, CommandSpec) error {
		t.Fatal("failed bootstrap executed child")
		return nil
	})
	if err := PrepareReleasePleasePullRequest(t.Context(), options); !errors.Is(err, want) {
		t.Fatalf("bootstrap failure=%v", err)
	}
}

func prepareReleasePleaseTest(ctx context.Context, options ReleasePleaseOptions) error {
	return prepareReleasePleasePullRequest(ctx, options, func(context.Context, string, *http.Client) error {
		return nil
	})
}
