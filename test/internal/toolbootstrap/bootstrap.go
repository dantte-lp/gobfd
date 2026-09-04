// Package toolbootstrap installs repository-pinned development tools without a
// shell runtime boundary.
package toolbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	// ComposeVersion is the exact Docker Compose Go provider used by Podman.
	ComposeVersion = "5.5.0"
	composeBaseURL = "https://github.com/docker/compose/releases/download"
	httpTimeout    = 5 * time.Minute
	otherWriteBits = os.FileMode(0o022)
)

var (
	errUnsupportedArchitecture = errors.New("unsupported Docker Compose architecture")
	errUnsupportedVersion      = errors.New("unsupported Docker Compose version")
	errInvalidDownload         = errors.New("invalid Docker Compose download")
	errUnexpectedVersion       = errors.New("unexpected Docker Compose provider version")
	errInvalidGitHubFile       = errors.New("invalid GitHub workflow environment file")
	errUnsafeInstallDirectory  = errors.New("unsafe Compose install directory")
	errTemporaryFileReplaced   = errors.New("temporary Compose provider replaced")
)

type composeAsset struct {
	name   string
	sha256 string
	size   int64
}

// ComposeOptions configures one verified provider installation.
type ComposeOptions struct {
	InstallDir string
	Version    string
}

// ComposeReport describes the verified installed provider.
type ComposeReport struct {
	Path    string
	Version string
}

// RuntimeOptions configures Podman and its exact Compose provider for CI.
type RuntimeOptions struct {
	InstallDir string
	GitHubEnv  string
	GitHubPath string
}

// RuntimeReport describes the verified Podman/Compose runtime.
type RuntimeReport struct {
	Compose ComposeReport
	Podman  string
}

// DefaultInstallDir returns the user-local executable directory used by CI.
func DefaultInstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// InstallCompose downloads, verifies, and atomically installs the pinned Go
// Compose provider.
func InstallCompose(ctx context.Context, options ComposeOptions) (ComposeReport, error) {
	version, asset, validationErr := validateComposeOptions(options)
	if validationErr != nil {
		return ComposeReport{}, validationErr
	}
	installDir, directoryErr := prepareComposeInstallDirectory(options.InstallDir)
	if directoryErr != nil {
		return ComposeReport{}, directoryErr
	}
	installRoot, rootErr := os.OpenRoot(installDir)
	if rootErr != nil {
		return ComposeReport{}, fmt.Errorf("open Compose install directory root: %w", rootErr)
	}
	if identityErr := validateComposeInstallRoot(installRoot, installDir); identityErr != nil {
		return ComposeReport{}, errors.Join(identityErr, wrapComposeCleanupError("close install root", installRoot.Close()))
	}
	temporary, createErr := os.CreateTemp(installDir, ".docker-compose-*")
	if createErr != nil {
		return ComposeReport{}, errors.Join(
			fmt.Errorf("create temporary Compose provider: %w", createErr),
			wrapComposeCleanupError("close install root", installRoot.Close()),
		)
	}
	temporaryName := filepath.Base(temporary.Name())
	temporaryInfo, statErr := temporary.Stat()
	if statErr != nil {
		return ComposeReport{}, abortComposeInstall(installRoot, temporary, temporaryName, nil,
			fmt.Errorf("stat temporary Compose provider: %w", statErr))
	}
	if downloadErr := downloadCompose(ctx, version, asset, temporary); downloadErr != nil {
		return ComposeReport{}, abortComposeInstall(installRoot, temporary, temporaryName, temporaryInfo, downloadErr)
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		return ComposeReport{}, abortComposeInstall(installRoot, temporary, temporaryName, temporaryInfo,
			fmt.Errorf("sync temporary Compose provider: %w", syncErr))
	}
	// #nosec G302 -- this descriptor identifies the checksum-verified provider and must become executable.
	if chmodErr := temporary.Chmod(0o755); chmodErr != nil {
		return ComposeReport{}, abortComposeInstall(installRoot, temporary, temporaryName, temporaryInfo,
			fmt.Errorf("set Compose provider mode: %w", chmodErr))
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return ComposeReport{}, failClosedComposeInstall(installRoot, temporaryName, temporaryInfo,
			fmt.Errorf("close temporary Compose provider: %w", closeErr),
		)
	}
	return publishCompose(ctx, version, installDir, installRoot, temporaryName, temporaryInfo)
}

func validateComposeOptions(options ComposeOptions) (string, composeAsset, error) {
	version := options.Version
	if version == "" {
		version = ComposeVersion
	}
	if version != ComposeVersion {
		return "", composeAsset{}, fmt.Errorf("docker Compose %s: %w", version, errUnsupportedVersion)
	}
	if options.InstallDir == "" || strings.ContainsAny(options.InstallDir, "\r\n") {
		return "", composeAsset{}, fmt.Errorf("docker Compose install directory %q: %w", options.InstallDir, os.ErrInvalid)
	}
	if !filepath.IsAbs(options.InstallDir) {
		return "", composeAsset{}, fmt.Errorf("docker Compose install directory %q must be absolute: %w",
			options.InstallDir, errUnsafeInstallDirectory)
	}
	asset, err := composeAssetFor(runtime.GOARCH)
	return version, asset, err
}

func publishCompose(
	ctx context.Context,
	version, installDir string,
	installRoot *os.Root,
	temporaryName string,
	temporaryInfo os.FileInfo,
) (ComposeReport, error) {
	verified, openErr := installRoot.Open(temporaryName)
	if openErr != nil {
		return ComposeReport{}, failClosedComposeInstall(installRoot, temporaryName, temporaryInfo,
			fmt.Errorf("open verified Compose provider: %w", openErr))
	}
	verifiedInfo, verifiedStatErr := verified.Stat()
	if verifiedStatErr != nil || !os.SameFile(temporaryInfo, verifiedInfo) {
		identityErr := errors.Join(verifiedStatErr, errTemporaryFileReplaced)
		return ComposeReport{}, failOpenComposeInstall(installRoot, verified, temporaryName, temporaryInfo,
			fmt.Errorf("bind verified Compose provider inode: %w", identityErr))
	}
	actual, commandErr := commandFileOutput(ctx, verified, []string{"version", "--short"}, nil)
	if commandErr != nil {
		return ComposeReport{}, failOpenComposeInstall(installRoot, verified, temporaryName, temporaryInfo,
			fmt.Errorf("verify temporary Compose provider: %w", commandErr),
		)
	}
	if actual != version {
		return ComposeReport{}, failOpenComposeInstall(installRoot, verified, temporaryName, temporaryInfo,
			fmt.Errorf("docker Compose provider version %s, want %s: %w",
				actual, version, errUnexpectedVersion),
		)
	}
	const targetName = "docker-compose"
	if renameErr := installRoot.Rename(temporaryName, targetName); renameErr != nil {
		return ComposeReport{}, failOpenComposeInstall(installRoot, verified, temporaryName, temporaryInfo,
			fmt.Errorf("install Compose provider: %w", renameErr),
		)
	}
	publishedInfo, publishedErr := installRoot.Stat(targetName)
	if publishedErr != nil || !os.SameFile(temporaryInfo, publishedInfo) {
		return ComposeReport{}, errors.Join(
			fmt.Errorf("verify published Compose provider inode: %w",
				errors.Join(publishedErr, errTemporaryFileReplaced)),
			wrapComposeCleanupError("close verified Compose provider", verified.Close()),
			wrapComposeCleanupError("close install root", installRoot.Close()),
		)
	}
	if closeErr := verified.Close(); closeErr != nil {
		return ComposeReport{}, errors.Join(
			fmt.Errorf("close verified Compose provider: %w", closeErr),
			wrapComposeCleanupError("close install root", installRoot.Close()),
		)
	}
	if closeErr := installRoot.Close(); closeErr != nil {
		return ComposeReport{}, fmt.Errorf("close install root: %w", closeErr)
	}
	target := filepath.Join(installDir, targetName)
	return ComposeReport{Path: target, Version: actual}, nil
}

func prepareComposeInstallDirectory(path string) (string, error) {
	cleaned := filepath.Clean(path)
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(cleaned, current), current)
	for index, component := range components {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := prepareComposeDirectoryComponent(current)
		if err != nil {
			return "", err
		}
		validationErr := validateComposeDirectoryComponent(current, info, index == len(components)-1)
		if validationErr != nil {
			return "", validationErr
		}
	}
	return cleaned, nil
}

func prepareComposeDirectoryComponent(path string) (os.FileInfo, error) {
	// #nosec G703 -- the component is derived from the absolute install directory and inspected before use.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		// #nosec G703 -- all existing ancestors were validated before creating this single component.
		if mkdirErr := os.Mkdir(path, 0o750); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			return nil, fmt.Errorf("create Compose install directory component %s: %w", path, mkdirErr)
		}
		// #nosec G703 -- a concurrent creator is accepted only after ownership and mode validation by the caller.
		info, err = os.Lstat(path)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Compose install directory component %s: %w", path, err)
	}
	return info, nil
}

func validateComposeDirectoryComponent(path string, info os.FileInfo, final bool) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("compose install directory component %s is not a real directory: %w",
			path, errUnsafeInstallDirectory)
	}
	trustedOwner, currentOwner := composeDirectoryOwnership(info)
	if !trustedOwner {
		return fmt.Errorf("compose install directory component %s has an untrusted owner: %w",
			path, errUnsafeInstallDirectory)
	}
	writableByOthers := info.Mode().Perm()&otherWriteBits != 0
	if final && !currentOwner {
		return fmt.Errorf("compose install directory %s is not owned by the current user: %w",
			path, errUnsafeInstallDirectory)
	}
	if writableByOthers && (final || info.Mode()&os.ModeSticky == 0) {
		return fmt.Errorf("compose install directory component %s has unsafe mode %o: %w",
			path, info.Mode().Perm(), errUnsafeInstallDirectory)
	}
	return nil
}

func validateComposeInstallRoot(root *os.Root, path string) error {
	rootInfo, rootErr := root.Stat(".")
	// #nosec G703 -- the absolute path was prepared without symlink traversal and is compared with the opened root.
	pathInfo, pathErr := os.Lstat(path)
	if err := errors.Join(rootErr, pathErr); err != nil {
		return fmt.Errorf("bind Compose install directory root: %w", err)
	}
	if !os.SameFile(rootInfo, pathInfo) {
		return fmt.Errorf("compose install directory changed identity: %w", errUnsafeInstallDirectory)
	}
	return nil
}

func abortComposeInstall(
	root *os.Root,
	temporary *os.File,
	name string,
	expected os.FileInfo,
	cause error,
) error {
	return errors.Join(
		cause,
		wrapComposeCleanupError("close temporary Compose provider", temporary.Close()),
		removeComposeTemporary(root, name, expected),
		wrapComposeCleanupError("close install root", root.Close()),
	)
}

func failOpenComposeInstall(
	root *os.Root,
	verified *os.File,
	name string,
	expected os.FileInfo,
	cause error,
) error {
	return errors.Join(
		cause,
		wrapComposeCleanupError("close verified Compose provider", verified.Close()),
		removeComposeTemporary(root, name, expected),
		wrapComposeCleanupError("close install root", root.Close()),
	)
}

func failClosedComposeInstall(root *os.Root, name string, expected os.FileInfo, cause error) error {
	return errors.Join(
		cause,
		removeComposeTemporary(root, name, expected),
		wrapComposeCleanupError("close install root", root.Close()),
	)
}

func removeComposeTemporary(root *os.Root, name string, expected os.FileInfo) error {
	if expected == nil {
		return fmt.Errorf("refuse to remove unverified temporary Compose provider %s: %w",
			name, errTemporaryFileReplaced)
	}
	info, statErr := root.Lstat(name)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect temporary Compose provider: %w", statErr)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, info) {
		return fmt.Errorf("temporary Compose provider %s changed identity: %w", name, errTemporaryFileReplaced)
	}
	if removeErr := root.Remove(name); removeErr != nil {
		return fmt.Errorf("remove temporary Compose provider: %w", removeErr)
	}
	return nil
}

func wrapComposeCleanupError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

// SetupPodmanRuntime ensures Podman is present, installs the verified Compose
// provider, persists GitHub Actions environment variables, and verifies the
// delegated provider version.
func SetupPodmanRuntime(ctx context.Context, options RuntimeOptions) (RuntimeReport, error) {
	if err := ensurePodman(ctx); err != nil {
		return RuntimeReport{}, err
	}
	if err := verifyJQ(ctx, execOutputRunner{}); err != nil {
		return RuntimeReport{}, err
	}
	compose, err := InstallCompose(ctx, ComposeOptions{InstallDir: options.InstallDir})
	if err != nil {
		return RuntimeReport{}, err
	}
	environment := providerEnvironment(os.Environ(), compose.Path)
	actual, err := commandOutput(ctx, "podman", []string{"compose", "version", "--short"}, environment)
	if err != nil {
		return RuntimeReport{}, fmt.Errorf("verify Podman Compose provider: %w", err)
	}
	if actual != ComposeVersion {
		return RuntimeReport{}, fmt.Errorf("podman Compose provider version %s, want %s: %w",
			actual, ComposeVersion, errUnexpectedVersion)
	}
	podmanVersion, err := commandOutput(ctx, "podman", []string{"--version"}, environment)
	if err != nil {
		return RuntimeReport{}, fmt.Errorf("verify Podman runtime: %w", err)
	}
	if err := writeGitHubEnvironment(options, compose.Path); err != nil {
		return RuntimeReport{}, err
	}
	return RuntimeReport{Compose: compose, Podman: podmanVersion}, nil
}

func composeAssetFor(goarch string) (composeAsset, error) {
	switch goarch {
	case "amd64":
		return composeAsset{
			name:   "docker-compose-linux-x86_64",
			sha256: "c57ab918abd5b05ca7e7d0f275875dd1330a695074f309dc9eab1b49efafcd4b",
			size:   49_441_177,
		}, nil
	case "arm64":
		return composeAsset{
			name:   "docker-compose-linux-aarch64",
			sha256: "ff42489f5a9b879d5d117c5ffea6defc27390b3286da8ad52cbc9c6ab5df590e",
			size:   46_470_638,
		}, nil
	default:
		return composeAsset{}, fmt.Errorf("docker Compose architecture %s: %w", goarch, errUnsupportedArchitecture)
	}
}

func downloadCompose(ctx context.Context, version string, asset composeAsset, destination *os.File) error {
	downloadURL := composeBaseURL + "/v" + version + "/" + asset.name
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create Compose download request: %w", err)
	}
	client := &http.Client{Timeout: httpTimeout, CheckRedirect: validateComposeRedirect}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download Compose provider: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return errors.Join(fmt.Errorf("download Compose provider: status %s: %w", response.Status, errInvalidDownload),
			response.Body.Close())
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, asset.size+1))
	closeErr := response.Body.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("read Compose provider: %w", err)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if written != asset.size || actualHash != asset.sha256 {
		return fmt.Errorf("compose asset %s size=%d sha256=%s: %w", asset.name, written, actualHash, errInvalidDownload)
	}
	return nil
}

func validateComposeRedirect(request *http.Request, _ []*http.Request) error {
	parsed := request.URL
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" && parsed.Port() != "443" {
		return fmt.Errorf("redirect URL %s: %w", parsed.Redacted(), errInvalidDownload)
	}
	switch parsed.Hostname() {
	case "github.com", "release-assets.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("redirect host %s: %w", parsed.Hostname(), errInvalidDownload)
	}
}

func ensurePodman(ctx context.Context) error {
	if _, err := commandPath("podman"); err == nil {
		return nil
	}
	for _, arguments := range [][]string{
		{"apt-get", "-o", "Acquire::Retries=3", "update"},
		{"apt-get", "-o", "Acquire::Retries=3", "install", "-y", "--no-install-recommends", "podman"},
	} {
		if err := runStreaming(ctx, "sudo", arguments, nil); err != nil {
			return fmt.Errorf("install Podman runtime: %w", err)
		}
	}
	return nil
}

func providerEnvironment(base []string, provider string) []string {
	path := filepath.Dir(provider) + string(os.PathListSeparator) + environmentValue(base, "PATH")
	return replaceEnvironment(base, map[string]string{
		"PATH":                        path,
		"PODMAN_COMPOSE_PROVIDER":     provider,
		"PODMAN_COMPOSE_WARNING_LOGS": "false",
		"DOCKER_BUILDKIT":             "0",
	})
}

func writeGitHubEnvironment(options RuntimeOptions, provider string) error {
	if options.GitHubPath != "" {
		if err := appendWorkflowFile(options.GitHubPath, filepath.Dir(provider)+"\n"); err != nil {
			return fmt.Errorf("write GITHUB_PATH: %w", err)
		}
	}
	if options.GitHubEnv != "" {
		content := "PODMAN_COMPOSE_PROVIDER=" + provider + "\n" +
			"PODMAN_COMPOSE_WARNING_LOGS=false\n" +
			"DOCKER_BUILDKIT=0\n"
		if err := appendWorkflowFile(options.GitHubEnv, content); err != nil {
			return fmt.Errorf("write GITHUB_ENV: %w", err)
		}
	}
	return nil
}

func appendWorkflowFile(name, content string) error {
	if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(content, "\r") {
		return errInvalidGitHubFile
	}
	if info, err := os.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("workflow file %s has mode %s: %w", name, info.Mode(), errInvalidGitHubFile)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workflow file %s: %w", name, err)
	}
	file, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open workflow file %s: %w", name, err)
	}
	_, writeErr := io.WriteString(file, content)
	return errors.Join(writeErr, file.Close())
}

func replaceEnvironment(base []string, replacements map[string]string) []string {
	environment := make([]string, 0, len(base)+len(replacements))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := replacements[name]; !replaced {
			environment = append(environment, entry)
		}
	}
	for name, value := range replacements {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func environmentValue(environment []string, target string) string {
	for _, entry := range slices.Backward(environment) {
		name, value, found := strings.Cut(entry, "=")
		if found && name == target {
			return value
		}
	}
	return ""
}
