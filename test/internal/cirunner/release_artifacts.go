package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const releaseArtifactsManifestLimit = 16 << 20

// ReleaseArtifactsOptions supplies the immutable release identity and rooted artifact paths.
type ReleaseArtifactsOptions struct {
	Root       string
	RunnerTemp string
	RefName    string
	SHA        string
	Runner     SpecRunner
}

type goReleaserArtifact struct {
	Type   string                  `json:"type"`
	Path   string                  `json:"path"`
	GoOS   string                  `json:"goos"`
	GoArch string                  `json:"goarch"`
	Extra  goReleaserArtifactExtra `json:"extra"`
}

type goReleaserArtifactExtra struct {
	Format string `json:"Format"`
}

// ReleaseArtifacts validates GoReleaser's release-asset matrix and writes its exact expected manifests.
func ReleaseArtifacts(ctx context.Context, options ReleaseArtifactsOptions) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	runnerTemp, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	version, _, err := parseStableReleaseVersion(options.RefName)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("release artifact command runner is required: %w", errInvalidConfig)
	}
	expectedWorkflowSHA, err := validateFullCommitSHA(options.SHA, "GITHUB_SHA")
	if err != nil {
		return err
	}

	repositoryRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository root for GoReleaser artifacts: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close repository artifact root", repositoryRoot.Close()))
	}()
	receiptRoot, err := os.OpenRoot(runnerTemp)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for release asset manifests: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release asset manifest root", receiptRoot.Close()))
	}()
	if verifyErr := verifyReleaseArtifactCommit(
		ctx, options.Runner, repositoryRoot, receiptRoot, root, expectedWorkflowSHA,
	); verifyErr != nil {
		return verifyErr
	}
	manifest, err := readRootedRegularFile(
		repositoryRoot, "dist/artifacts.json", "GoReleaser artifact manifest", releaseArtifactsManifestLimit,
	)
	if err != nil {
		return err
	}
	if validationErr := validateStrictJSONDocument(manifest, "GoReleaser artifact manifest"); validationErr != nil {
		return validationErr
	}
	artifacts := []goReleaserArtifact{}
	if decodeErr := decodeJSONDocument(manifest, &artifacts, "GoReleaser artifact manifest"); decodeErr != nil {
		return decodeErr
	}
	if validationErr := validateGoReleaserArtifactFields(manifest); validationErr != nil {
		return validationErr
	}
	checksummed, release, err := validateGoReleaserArtifactMatrix(artifacts, version, options.RefName)
	if err != nil {
		return err
	}
	outputs := []struct {
		name string
		data []byte
	}{
		{name: "expected-checksummed-assets.txt", data: renderArtifactNames(checksummed)},
		{name: "expected-release-assets.txt", data: renderArtifactNames(release)},
	}
	for _, output := range outputs {
		if err := validateRootedRegularTarget(receiptRoot, output.name, "release asset manifest"); err != nil {
			return err
		}
	}
	for _, output := range outputs {
		if err := writeRootedArtifact(
			receiptRoot, output.name, output.data, "release asset manifest", releaseArtifactsManifestLimit,
		); err != nil {
			return err
		}
	}
	return nil
}

func verifyReleaseArtifactCommit(
	ctx context.Context,
	runner SpecRunner,
	repositoryRoot *os.Root,
	receiptRoot *os.Root,
	repositoryDirectory string,
	expectedWorkflowSHA string,
) error {
	expectedData, err := readRootedRegularFile(
		receiptRoot, "expected-release-commit.txt", "release commit receipt", releaseReceiptLimit,
	)
	if err != nil {
		return err
	}
	expected, err := parseCommandSHA(expectedData, "expected release commit receipt")
	if err != nil {
		return err
	}
	if expected != expectedWorkflowSHA {
		return fmt.Errorf("release commit receipt differs from GITHUB_SHA: %w", errInvalidConfig)
	}
	actualData, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "git", Args: []string{"rev-parse", "HEAD"}, Dir: repositoryDirectory,
	}, "revalidate release artifact commit")
	if err != nil {
		return err
	}
	actual, err := parseCommandSHA(actualData, "git rev-parse HEAD")
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("checked-out commit changed before reading GoReleaser artifacts: %w", errInvalidConfig)
	}
	if err := validateRootPathIdentity(
		repositoryRoot, repositoryDirectory, "repository root before reading GoReleaser artifacts",
	); err != nil {
		return err
	}
	return nil
}

func validateRootPathIdentity(root *os.Root, path, purpose string) error {
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() {
		return fmt.Errorf("%s opened root is invalid: %w", purpose, errors.Join(err, errInvalidConfig))
	}
	current, err := os.Lstat(path)
	if err != nil || !current.IsDir() || !os.SameFile(opened, current) {
		return fmt.Errorf("%s pathname identity changed: %w", purpose, errors.Join(err, errInvalidConfig))
	}
	return nil
}

func validateStrictJSONDocument(data []byte, purpose string) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("%s contains invalid UTF-8: %w", purpose, errInvalidConfig)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateUniqueJSONValue(decoder); err != nil {
		return fmt.Errorf("validate %s structure: %w", purpose, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s contains trailing JSON data: %w", purpose, errInvalidConfig)
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string: %w", errInvalidConfig)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object member %q: %w", key, errInvalidConfig)
			}
			seen[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.Join(err, errInvalidConfig)
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.Join(err, errInvalidConfig)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q: %w", delimiter, errInvalidConfig)
	}
	return nil
}

func validateGoReleaserArtifactFields(data []byte) error {
	rawArtifacts := []map[string]json.RawMessage{}
	if err := decodeJSONDocument(data, &rawArtifacts, "GoReleaser artifact fields"); err != nil {
		return err
	}
	for index, artifact := range rawArtifacts {
		if artifact == nil {
			return fmt.Errorf("GoReleaser artifact %d is not an object: %w", index, errInvalidConfig)
		}
		if err := rejectJSONFieldAliases(artifact, []string{"type", "path", "goos", "goarch", "extra"}); err != nil {
			return fmt.Errorf("GoReleaser artifact %d: %w", index, err)
		}
		if extra, exists := artifact["extra"]; exists && !bytes.Equal(bytes.TrimSpace(extra), []byte("null")) {
			rawExtra := map[string]json.RawMessage{}
			if err := decodeJSONDocument(extra, &rawExtra, "GoReleaser artifact extra fields"); err != nil {
				return err
			}
			if err := rejectJSONFieldAliases(rawExtra, []string{"Format"}); err != nil {
				return fmt.Errorf("GoReleaser artifact %d extra fields: %w", index, err)
			}
		}
	}
	return nil
}

func rejectJSONFieldAliases(fields map[string]json.RawMessage, canonical []string) error {
	for name := range fields {
		for _, expected := range canonical {
			if strings.EqualFold(name, expected) && name != expected {
				return fmt.Errorf("noncanonical JSON field %q, want %q: %w", name, expected, errInvalidConfig)
			}
		}
	}
	return nil
}

func validateGoReleaserArtifactMatrix(
	artifacts []goReleaserArtifact,
	version string,
	refName string,
) ([]string, []string, error) {
	expectedChecksummed := expectedChecksummedArtifactNames(version)
	expectedGoReleaserRelease := append([]string{"checksums.txt"}, expectedChecksummed...)
	sort.Strings(expectedGoReleaserRelease)
	expectedRelease := expectedReleaseAssetNames(version, refName)

	checksummed := make([]string, 0, len(expectedChecksummed))
	release := make([]string, 0, len(expectedRelease))
	for index, artifact := range artifacts {
		if artifact.Type != "Archive" && artifact.Type != "Linux Package" &&
			artifact.Type != "SBOM" && artifact.Type != "Checksum" {
			continue
		}
		name, err := artifactFileName(artifact.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("GoReleaser artifact %d: %w", index, err)
		}
		switch artifact.Type {
		case "Archive":
			if artifact.GoOS != "linux" || !supportedReleaseArch(artifact.GoArch) ||
				name != "gobfd_"+version+"_linux_"+artifact.GoArch+".tar.gz" {
				return nil, nil, fmt.Errorf("GoReleaser archive %q violates the release matrix: %w", name, errInvalidConfig)
			}
			checksummed = append(checksummed, name)
			release = append(release, name)
		case "Linux Package":
			if artifact.GoOS != "linux" || !supportedReleaseArch(artifact.GoArch) ||
				(artifact.Extra.Format != "deb" && artifact.Extra.Format != "rpm") ||
				name != "gobfd_"+version+"_linux_"+artifact.GoArch+"."+artifact.Extra.Format {
				return nil, nil, fmt.Errorf("GoReleaser Linux package %q violates the release matrix: %w", name, errInvalidConfig)
			}
			checksummed = append(checksummed, name)
			release = append(release, name)
		case "SBOM":
			checksummed = append(checksummed, name)
			release = append(release, name)
		case "Checksum":
			if name != "checksums.txt" {
				return nil, nil, fmt.Errorf("GoReleaser checksum %q is not canonical: %w", name, errInvalidConfig)
			}
			release = append(release, name)
		}
	}
	sort.Strings(checksummed)
	sort.Strings(release)
	if !slices.Equal(checksummed, expectedChecksummed) {
		return nil, nil, fmt.Errorf("GoReleaser checksummed artifact matrix differs from the exact contract: %w", errInvalidConfig)
	}
	if !slices.Equal(release, expectedGoReleaserRelease) {
		return nil, nil, fmt.Errorf("GoReleaser release artifact matrix differs from the exact contract: %w", errInvalidConfig)
	}
	return checksummed, expectedRelease, nil
}

func expectedChecksummedArtifactNames(version string) []string {
	names := make([]string, 0, 8)
	for _, arch := range []string{"amd64", "arm64"} {
		prefix := "gobfd_" + version + "_linux_" + arch
		names = append(names, prefix+".deb", prefix+".rpm", prefix+".tar.gz", prefix+".tar.gz.sbom.json")
	}
	sort.Strings(names)
	return names
}

func expectedReleaseAssetNames(version, refName string) []string {
	names := append([]string{"checksums.txt"}, expectedChecksummedArtifactNames(version)...)
	names = append(
		names, "gobfd-"+refName+"-reports.tar.gz",
		"release-evidence-checksums.txt", "release-image-digests.txt",
	)
	sort.Strings(names)
	return names
}

func supportedReleaseArch(arch string) bool {
	return arch == "amd64" || arch == "arm64"
}

func artifactFileName(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || hasControl(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("artifact path %q is not clean: %w", path, errInvalidConfig)
	}
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || strings.ContainsAny(name, "/\\") ||
		path != filepath.Join("dist", name) {
		return "", fmt.Errorf("artifact path %q lacks a safe filename: %w", path, errInvalidConfig)
	}
	return name, nil
}

func renderArtifactNames(names []string) []byte {
	return []byte(strings.Join(names, "\n") + "\n")
}
