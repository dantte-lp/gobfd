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
)

var (
	errUnsupportedArchitecture = errors.New("unsupported Docker Compose architecture")
	errUnsupportedVersion      = errors.New("unsupported Docker Compose version")
	errInvalidDownload         = errors.New("invalid Docker Compose download")
	errUnexpectedVersion       = errors.New("unexpected Docker Compose provider version")
	errInvalidGitHubFile       = errors.New("invalid GitHub workflow environment file")
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
	if options.Version == "" {
		options.Version = ComposeVersion
	}
	if options.Version != ComposeVersion {
		return ComposeReport{}, fmt.Errorf("docker Compose %s: %w", options.Version, errUnsupportedVersion)
	}
	if options.InstallDir == "" || strings.ContainsAny(options.InstallDir, "\r\n") {
		return ComposeReport{}, fmt.Errorf("docker Compose install directory %q: %w", options.InstallDir, os.ErrInvalid)
	}
	asset, err := composeAssetFor(runtime.GOARCH)
	if err != nil {
		return ComposeReport{}, err
	}
	//nolint:gosec // Executables require a traversable user-local or system bin directory.
	if mkdirErr := os.MkdirAll(options.InstallDir, 0o755); mkdirErr != nil {
		return ComposeReport{}, fmt.Errorf("create Compose install directory: %w", mkdirErr)
	}
	temporary, err := os.CreateTemp(options.InstallDir, ".docker-compose-*")
	if err != nil {
		return ComposeReport{}, fmt.Errorf("create temporary Compose provider: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if downloadErr := downloadCompose(ctx, options.Version, asset, temporary); downloadErr != nil {
		return ComposeReport{}, errors.Join(downloadErr, temporary.Close())
	}
	if finalizeErr := errors.Join(temporary.Sync(), temporary.Close()); finalizeErr != nil {
		return ComposeReport{}, fmt.Errorf("finalize temporary Compose provider: %w", finalizeErr)
	}
	//nolint:gosec // The checksum-verified provider must be executable by CI and development users.
	if chmodErr := os.Chmod(temporaryPath, 0o755); chmodErr != nil {
		return ComposeReport{}, fmt.Errorf("set Compose provider mode: %w", chmodErr)
	}
	actual, err := commandOutput(ctx, temporaryPath, []string{"version", "--short"}, nil)
	if err != nil {
		return ComposeReport{}, fmt.Errorf("verify temporary Compose provider: %w", err)
	}
	if actual != options.Version {
		return ComposeReport{}, fmt.Errorf("docker Compose provider version %s, want %s: %w",
			actual, options.Version, errUnexpectedVersion)
	}
	target := filepath.Join(options.InstallDir, "docker-compose")
	if err := os.Rename(temporaryPath, target); err != nil {
		return ComposeReport{}, fmt.Errorf("install Compose provider: %w", err)
	}
	return ComposeReport{Path: target, Version: actual}, nil
}

// SetupPodmanRuntime ensures Podman is present, installs the verified Compose
// provider, persists GitHub Actions environment variables, and verifies the
// delegated provider version.
func SetupPodmanRuntime(ctx context.Context, options RuntimeOptions) (RuntimeReport, error) {
	if err := ensurePodman(ctx); err != nil {
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
