package cirunner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/npmbootstrap"
)

const (
	releasePleaseRepository = "dantte-lp/gobfd"
	releasePleaseRef        = "refs/heads/release/v1.0"
	releasePleaseRegistry   = "https://registry.npmjs.org/"
	releasePleaseTimeout    = 4 * time.Minute
	releasePleaseFileLimit  = 1 << 20
	releasePleaseTokenLimit = 4 << 10
	releasePleaseInputCount = 4
)

// ReleasePleaseOptions supplies trusted workflow context and delayed credential access.
type ReleasePleaseOptions struct {
	Root        string
	Repository  string
	Ref         string
	Event       string
	Environment func() []string
	Token       func() string
	Runner      SpecRunner
	HTTPClient  *http.Client
}

// PrepareReleasePleasePullRequest audits the locked CLI before exposing its token.
// Only the upstream release-pr command runs; tagging and publication remain separate.
func PrepareReleasePleasePullRequest(ctx context.Context, options ReleasePleaseOptions) error {
	return prepareReleasePleasePullRequest(ctx, options, installReleasePleaseBootstrap)
}

func prepareReleasePleasePullRequest(
	ctx context.Context, options ReleasePleaseOptions,
	bootstrap func(context.Context, string, *http.Client) error,
) (returnErr error) {
	if err := validateReleasePleaseContext(options); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, releasePleaseTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare release pull request: %w", err)
	}
	root, err := validateAbsoluteExistingDirectory(options.Root, "release automation root")
	if err != nil {
		return err
	}
	directory, err := createReleasePleaseDirectory(root)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("remove private release automation files", os.RemoveAll(directory)))
	}()
	if err := copyReleasePleaseFiles(root, directory); err != nil {
		return err
	}
	environment := releasePleaseEnvironment(options.Environment(), directory)
	if err := bootstrap(ctx, directory, options.HTTPClient); err != nil {
		return err
	}
	if err := installAndVerifyReleasePlease(ctx, options.Runner, directory, environment); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare audited release pull request: %w", err)
	}
	if err := writeReleasePleaseToken(directory, options.Token()); err != nil {
		return err
	}
	return runReleasePlease(ctx, options.Runner, directory, environment)
}

func validateReleasePleaseContext(options ReleasePleaseOptions) error {
	if options.Repository != releasePleaseRepository || options.Ref != releasePleaseRef ||
		(options.Event != "push" && options.Event != "workflow_dispatch") {
		return fmt.Errorf("release automation requires the protected release branch and trusted event: %w", errInvalidConfig)
	}
	if options.Runner == nil || options.Environment == nil || options.Token == nil {
		return fmt.Errorf("release automation requires runner and delayed credential access: %w", errInvalidConfig)
	}
	return nil
}

func createReleasePleaseDirectory(root string) (string, error) {
	parent, err := validateAbsoluteExistingDirectory(os.TempDir(), "release automation temporary parent")
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, parent)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return "", fmt.Errorf("release automation temporary parent must be outside the checkout: %w", errInvalidConfig)
	}
	directory, err := os.MkdirTemp(parent, "gobfd-release-please-")
	if err != nil {
		return "", fmt.Errorf("create private release automation directory: %w", err)
	}
	return directory, nil
}

func copyReleasePleaseFiles(repository, directory string) (returnErr error) {
	source, err := os.OpenRoot(repository)
	if err != nil {
		return fmt.Errorf("open release automation source: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release automation source", source.Close()))
	}()
	target, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open private release automation directory: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close private release automation directory", target.Close()))
	}()
	for _, name := range releasePleaseInputNames() {
		data, err := readRootedRegularFile(source, filepath.Join("tools", "release-please", name),
			"release automation manifest", releasePleaseFileLimit)
		if err != nil {
			return err
		}
		if err := target.WriteFile(name, data, 0o600); err != nil {
			return fmt.Errorf("copy private release automation manifest: %w", err)
		}
	}
	if err := target.WriteFile("global.npmrc", nil, 0o600); err != nil {
		return fmt.Errorf("create isolated npm global configuration: %w", err)
	}
	return nil
}

func releasePleaseEnvironment(environment []string, directory string) []string {
	blocked := []string{"GH_TOKEN", "GITHUB_TOKEN", "HOME", "_AUTH", "_AUTH_TOKEN", "_AUTHTOKEN"}
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if found && (strings.HasPrefix(upper, "NPM_") || strings.HasPrefix(upper, "NODE_") ||
			strings.HasSuffix(upper, "_TOKEN") || upper == "_AUTH" || upper == "_AUTHTOKEN") {
			blocked = append(blocked, name)
		}
	}
	return append(withoutEnvironmentKeys(environment, blocked...),
		"HOME="+directory,
		"NODE_COMPILE_CACHE="+filepath.Join(directory, "node-compile-cache"),
		"NPM_CONFIG_REGISTRY="+releasePleaseRegistry,
		"NPM_CONFIG_CACHE="+filepath.Join(directory, "cache"),
		"NPM_CONFIG_USERCONFIG="+filepath.Join(directory, ".npmrc"),
		"NPM_CONFIG_GLOBALCONFIG="+filepath.Join(directory, "global.npmrc"),
		"NPM_CONFIG_IGNORE_SCRIPTS=true",
	)
}

func installReleasePleaseBootstrap(ctx context.Context, directory string, client *http.Client) (returnErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open private npm bootstrap contract: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	data, err := readRootedRegularFile(root, "bootstrap.json", "npm bootstrap contract", releasePleaseFileLimit)
	if err != nil {
		return err
	}
	contract, err := npmbootstrap.Parse(data)
	if err != nil {
		return fmt.Errorf("parse npm bootstrap contract: %w", err)
	}
	if err := npmbootstrap.Install(ctx, directory, contract, client); err != nil {
		return fmt.Errorf("install verified npm bootstrap: %w", err)
	}
	return nil
}

func releasePleaseInputNames() [releasePleaseInputCount]string {
	return [...]string{"package.json", "package-lock.json", ".npmrc", "bootstrap.json"}
}

func releasePleaseInputDigests(
	directory string,
) ([releasePleaseInputCount][sha256.Size]byte, error) {
	var digests [releasePleaseInputCount][sha256.Size]byte
	root, err := os.OpenRoot(directory)
	if err != nil {
		return digests, fmt.Errorf("open release automation inputs: %w", err)
	}
	for index, name := range releasePleaseInputNames() {
		data, err := readRootedRegularFile(root, name, "release automation input", releasePleaseFileLimit)
		if err != nil {
			return digests, errors.Join(err, wrapOptional("close release automation inputs", root.Close()))
		}
		digests[index] = sha256.Sum256(data)
	}
	return digests, wrapOptional("close release automation inputs", root.Close())
}

func installAndVerifyReleasePlease(
	ctx context.Context, runner SpecRunner, directory string, environment []string,
) error {
	before, err := releasePleaseInputDigests(directory)
	if err != nil {
		return err
	}
	if installErr := installAndAuditReleasePlease(ctx, runner, directory, environment); installErr != nil {
		return installErr
	}
	after, err := releasePleaseInputDigests(directory)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("release automation inputs changed during installation or audit: %w", errInvalidConfig)
	}
	return nil
}

func installAndAuditReleasePlease(
	ctx context.Context, runner SpecRunner, directory string, environment []string,
) error {
	commands := [][]string{
		{"ci", "--ignore-scripts", "--omit=dev", "--no-fund", "--registry=" + releasePleaseRegistry},
		{"audit", "--omit=dev", "--audit-level=low", "--json", "--registry=" + releasePleaseRegistry},
	}
	npmCLI := filepath.Join(directory, filepath.FromSlash(npmbootstrap.CLIPath))
	for _, arguments := range commands {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("prepare release automation dependency audit: %w", err)
		}
		if err := runner.RunCommand(ctx, CommandSpec{
			Name: "node", Args: append([]string{npmCLI}, arguments...),
			Dir: directory, Env: environment, WaitDelay: time.Second,
		}); err != nil {
			return fmt.Errorf("run release automation npm %s: %w", arguments[0], err)
		}
	}
	return nil
}

func writeReleasePleaseToken(directory, token string) error {
	if token == "" || len(token) > releasePleaseTokenLimit || strings.TrimSpace(token) != token ||
		strings.ContainsAny(token, "\r\n\x00") {
		return fmt.Errorf("release automation requires a valid parent token: %w", errInvalidConfig)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open private release automation token directory: %w", err)
	}
	file, err := root.OpenFile("github-token", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Join(fmt.Errorf("create private release automation token: %w", err), root.Close())
	}
	_, writeErr := file.WriteString(token)
	return errors.Join(
		wrapOptional("write private release automation token", writeErr),
		wrapOptional("close private release automation token", file.Close()),
		wrapOptional("close private release automation token directory", root.Close()),
	)
}

func runReleasePlease(ctx context.Context, runner SpecRunner, directory string, environment []string) error {
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "node", Dir: directory, Env: environment, WaitDelay: time.Second,
		Args: []string{
			filepath.Join(directory, "node_modules", "release-please", "build", "src", "bin", "release-please.js"),
			"release-pr", "--repo-url=" + releasePleaseRepository, "--target-branch=release/v1.0",
			"--config-file=release-please-config.json", "--manifest-file=.release-please-manifest.json",
			"--token=" + filepath.Join(directory, "github-token"),
		},
		// The token-bearing upstream process must not emit credential diagnostics.
		Stdout: io.Discard, Stderr: io.Discard,
	}); err != nil {
		return fmt.Errorf("prepare audited release pull request: %w", err)
	}
	return nil
}
