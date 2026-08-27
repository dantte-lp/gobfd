package clabbootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	vyosReleaseAPI    = "https://api.github.com/repos/vyos/vyos-rolling-nightly-builds/releases/latest"
	vyosDownloadBase  = "https://github.com/vyos/vyos-rolling-nightly-builds/releases/download"
	vyosTargetImage   = "vyos:latest"
	maxAPIResponse    = 1 << 20
	maxISOSize        = int64(16 << 30)
	maxNestedArchive  = int64(32 << 30)
	vendorHTTPTimeout = 10 * time.Minute
)

var (
	errVendorURL          = errors.New("vendor URL is not allowlisted")
	errVendorResponse     = errors.New("invalid vendor HTTP response")
	errVendorArchive      = errors.New("invalid vendor archive")
	errVendorImagePrepare = errors.New("vendor image preparation failed")
)

func prepareVyOS(ctx context.Context, options Options, runner Runner, sourceImage string) error {
	if options.SkipPull {
		exists, err := imageExists(ctx, options, runner, vyosTargetImage)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	exists, err := imageExists(ctx, options, runner, vyosTargetImage)
	if err != nil || exists {
		return err
	}
	if options.VyOSISO == "" {
		sourceExists, sourceErr := imageExists(ctx, options, runner, sourceImage)
		if sourceErr != nil {
			return sourceErr
		}
		if sourceExists || options.DryRun {
			return runCommand(ctx, runner, Command{
				Executable: executablePodman,
				Arguments:  []string{"tag", sourceImage, vyosTargetImage},
				DryRun:     options.DryRun,
			})
		}
	}
	if options.DryRun {
		return nil
	}
	return buildVyOSFromISO(ctx, options, runner)
}

func buildVyOSFromISO(ctx context.Context, options Options, runner Runner) error {
	work, err := os.MkdirTemp("", "gobfd-vyos-bootstrap-")
	if err != nil {
		return fmt.Errorf("create VyOS work directory: %w", err)
	}
	defer os.RemoveAll(work)

	isoPath, err := resolveVyOSISO(ctx, options, work)
	if err != nil {
		return err
	}

	squashfs, err := extractVyOSSquashFS(ctx, runner, isoPath, work)
	if err != nil {
		return err
	}
	rootfs := filepath.Join(work, "rootfs")
	if commandErr := runCommand(ctx, runner, Command{
		Executable: "unsquashfs",
		Arguments:  []string{"-f", "-d", rootfs, squashfs},
	}); commandErr != nil {
		return fmt.Errorf("extract VyOS rootfs: %w", commandErr)
	}
	containerfile := filepath.Join(work, "Containerfile.import")
	if writeErr := os.WriteFile(containerfile, []byte("FROM scratch\nADD rootfs/ /\n"), 0o600); writeErr != nil {
		return fmt.Errorf("write VyOS import Containerfile: %w", writeErr)
	}
	ignore := filepath.Join(work, ".containerignore")
	if writeErr := os.WriteFile(ignore, []byte("*\n!rootfs/**\n!Containerfile.import\n"), 0o600); writeErr != nil {
		return fmt.Errorf("write VyOS import ignore file: %w", writeErr)
	}
	if commandErr := runCommand(ctx, runner, Command{
		Executable: executablePodman,
		Arguments:  []string{"build", "--tag", vyosTargetImage, "--file", containerfile, work},
	}); commandErr != nil {
		return fmt.Errorf("build VyOS rootfs image: %w", commandErr)
	}
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"run", "--rm", vyosTargetImage, "cat", "/etc/vyos-release"},
	})
	if err != nil {
		return fmt.Errorf("verify VyOS image: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("verify VyOS image: exit %d: %w", result.ExitCode, errVendorImagePrepare)
	}
	return nil
}

func resolveVyOSISO(ctx context.Context, options Options, work string) (string, error) {
	if options.VyOSISO != "" {
		if err := validateRegularFile(options.VyOSISO); err != nil {
			return "", fmt.Errorf("validate VyOS ISO %s: %w", options.VyOSISO, err)
		}
		return options.VyOSISO, nil
	}
	version := options.VyOSVersion
	if version == "latest" {
		var err error
		version, err = latestVyOSVersion(ctx)
		if err != nil {
			return "", err
		}
	}
	return downloadVyOSISO(ctx, version, work)
}

func extractVyOSSquashFS(ctx context.Context, runner Runner, isoPath, work string) (string, error) {
	target := filepath.Join(work, "filesystem.squashfs")
	sevenZip := Command{
		Executable: "7z",
		Arguments:  []string{"x", "-y", "-o" + work, isoPath, "live/filesystem.squashfs"},
	}
	if result, err := runner.Run(ctx, sevenZip); err == nil && result.ExitCode == 0 {
		extracted := filepath.Join(work, "live", "filesystem.squashfs")
		if err := os.Rename(extracted, target); err != nil {
			return "", fmt.Errorf("move VyOS squashfs: %w", err)
		}
		return target, nil
	}
	if err := runCommand(ctx, runner, Command{
		Executable: "xorriso",
		Arguments: []string{
			"-osirrox", "on", "-indev", isoPath,
			"-extract", "/live/filesystem.squashfs", target,
		},
	}); err != nil {
		return "", fmt.Errorf("extract VyOS ISO with xorriso: %w", err)
	}
	return target, nil
}

func importArista(ctx context.Context, options Options, runner Runner) error {
	if err := validateRegularFile(options.Archives.Arista); err != nil {
		return fmt.Errorf("validate Arista archive: %w", err)
	}
	exists, err := imageExists(ctx, options, runner, options.Tags.Arista)
	if err != nil || exists {
		return err
	}
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"load", "--input", options.Archives.Arista},
		DryRun:     options.DryRun,
	})
	if err != nil {
		return fmt.Errorf("load Arista archive: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("load Arista archive: exit %d: %s: %w", result.ExitCode, result.Stderr, errVendorImagePrepare)
	}
	loaded := parseLoadedImage(result.Stdout)
	if loaded == "" || loaded == options.Tags.Arista {
		return nil
	}
	return runCommand(ctx, runner, Command{
		Executable: executablePodman,
		Arguments:  []string{"tag", loaded, options.Tags.Arista},
		DryRun:     options.DryRun,
	})
}

func importCisco(ctx context.Context, options Options, runner Runner) error {
	if err := validateRegularFile(options.Archives.Cisco); err != nil {
		return fmt.Errorf("validate Cisco archive: %w", err)
	}
	exists, err := imageExists(ctx, options, runner, options.Tags.Cisco)
	if err != nil || exists {
		return err
	}
	if options.DryRun {
		return nil
	}
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"load", "--input", options.Archives.Cisco},
	})
	if err != nil {
		return fmt.Errorf("load Cisco archive: %w", err)
	}
	if result.ExitCode == 0 {
		return nil
	}
	return importNestedCisco(ctx, runner, options.Archives.Cisco)
}

func importNestedCisco(ctx context.Context, runner Runner, archivePath string) error {
	work, err := os.MkdirTemp("", "gobfd-xrd-bootstrap-")
	if err != nil {
		return fmt.Errorf("create Cisco archive work directory: %w", err)
	}
	defer os.RemoveAll(work)
	inner, err := extractFirstNestedArchive(archivePath, work)
	if err != nil {
		return err
	}
	return runCommand(ctx, runner, Command{
		Executable: executablePodman,
		Arguments:  []string{"load", "--input", inner},
	})
}

func extractFirstNestedArchive(archivePath, destination string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open Cisco archive: %w", err)
	}
	defer file.Close()
	reader, closeReader, err := tarReader(file, archivePath)
	if err != nil {
		return "", err
	}
	target, extractErr := extractNestedArchive(reader, destination)
	return target, errors.Join(extractErr, closeReader())
}

func extractNestedArchive(reader *tar.Reader, destination string) (string, error) {
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			return "", fmt.Errorf("cisco archive contains no nested image archive: %w", errVendorArchive)
		}
		if nextErr != nil {
			return "", fmt.Errorf("read Cisco archive: %w", nextErr)
		}
		name := strings.ToLower(header.Name)
		if header.Typeflag != tar.TypeReg ||
			!strings.HasSuffix(name, ".tar") && !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".tgz") {
			continue
		}
		if header.Size <= 0 || header.Size > maxNestedArchive {
			return "", fmt.Errorf("nested Cisco archive size %d: %w", header.Size, errVendorArchive)
		}
		target := filepath.Join(destination, "nested"+nestedArchiveSuffix(name))
		output, createErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return "", fmt.Errorf("create nested Cisco archive: %w", createErr)
		}
		_, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return "", fmt.Errorf("extract nested Cisco archive: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close nested Cisco archive: %w", closeErr)
		}
		return target, nil
	}
}

func tarReader(file *os.File, name string) (*tar.Reader, func() error, error) {
	if strings.HasSuffix(strings.ToLower(name), ".gz") || strings.HasSuffix(strings.ToLower(name), ".tgz") {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, func() error { return nil }, fmt.Errorf("open compressed Cisco archive: %w", err)
		}
		return tar.NewReader(gzipReader), gzipReader.Close, nil
	}
	return tar.NewReader(file), func() error { return nil }, nil
}

func nestedArchiveSuffix(name string) string {
	if strings.HasSuffix(name, ".tar.gz") {
		return ".tar.gz"
	}
	if strings.HasSuffix(name, ".tgz") {
		return ".tgz"
	}
	return ".tar"
}

func parseLoadedImage(output string) string {
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)
		for _, marker := range []string{"Loaded image(s):", "Loaded image:"} {
			if _, value, found := strings.Cut(line, marker); found {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func latestVyOSVersion(ctx context.Context) (string, error) {
	body, err := vendorGET(ctx, vyosReleaseAPI, maxAPIResponse)
	if err != nil {
		return "", fmt.Errorf("query latest VyOS release: %w", err)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("decode latest VyOS release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return "", fmt.Errorf("latest VyOS release lacks tag_name: %w", errVendorResponse)
	}
	return release.TagName, nil
}

func downloadVyOSISO(ctx context.Context, version, destination string) (string, error) {
	filename := "vyos-" + version + "-generic-amd64.iso"
	endpoint := vyosDownloadBase + "/" + url.PathEscape(version) + "/" + url.PathEscape(filename)
	target := filepath.Join(destination, filename)
	request, err := vendorRequest(ctx, endpoint)
	if err != nil {
		return "", err
	}
	client := vendorHTTPClient()
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download VyOS ISO: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download VyOS ISO: status %s: %w", response.Status, errVendorResponse)
	}
	if response.ContentLength > maxISOSize {
		return "", fmt.Errorf("download VyOS ISO: content length %d: %w", response.ContentLength, errVendorResponse)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create VyOS ISO: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxISOSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("write VyOS ISO: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close VyOS ISO: %w", closeErr)
	}
	if written <= 0 || written > maxISOSize {
		return "", fmt.Errorf("download VyOS ISO: size %d: %w", written, errVendorResponse)
	}
	return target, nil
}

func vendorGET(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	request, err := vendorRequest(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	response, err := vendorHTTPClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("send GET %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %s: %w", endpoint, response.Status, errVendorResponse)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", endpoint, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s exceeds %d bytes: %w", endpoint, limit, errVendorResponse)
	}
	return body, nil
}

func vendorRequest(ctx context.Context, endpoint string) (*http.Request, error) {
	if err := validateVendorURL(endpoint); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create vendor request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "gobfd-bootstrap")
	return request, nil
}

func vendorHTTPClient() *http.Client {
	return &http.Client{
		Timeout: vendorHTTPTimeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return validateVendorURL(request.URL.String())
		},
	}
}

func validateVendorURL(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse vendor URL: %w", err)
	}
	allowedHost := parsed.Hostname() == "api.github.com" || parsed.Hostname() == "github.com" ||
		parsed.Hostname() == "release-assets.githubusercontent.com"
	if parsed.Scheme != "https" || !allowedHost || parsed.User != nil ||
		(parsed.Port() != "" && parsed.Port() != "443") {
		return fmt.Errorf("%w: %q", errVendorURL, endpoint)
	}
	return nil
}

func validateRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path %s is not a regular file: %w", path, errVendorArchive)
	}
	return nil
}
