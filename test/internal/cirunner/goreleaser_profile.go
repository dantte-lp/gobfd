package cirunner

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

const (
	goreleaserName                = "goreleaser"
	goreleaserSourceDirectoryName = "source"
	goreleaserFileLimit           = 1 << 20
	goreleaserSourceFileLimit     = 1024
	goreleaserSourceByteLimit     = 4 << 20
	goreleaserZIPLimit            = 8 << 20
	goreleaserMetadataLimit       = 64 << 10
)

type goreleaserProfile struct {
	Module          string                `json:"module"`
	Version         string                `json:"version"`
	Sum             string                `json:"sum"`
	GoModSum        string                `json:"go_mod_sum"`
	SourceCommit    string                `json:"source_commit"`
	Files           int                   `json:"files"`
	Bytes           int64                 `json:"bytes"`
	Patch           goreleaserSourcePatch `json:"patch"`
	Manifests       *goreleaserManifests  `json:"qualified_manifests"`
	SourceManifests *goreleaserManifests  `json:"source_manifests"`
	Refmt           *refmtSourceProfile   `json:"refmt"`
}

type refmtSourceProfile struct {
	Module       string                `json:"module"`
	Version      string                `json:"version"`
	Sum          string                `json:"sum"`
	GoModSum     string                `json:"go_mod_sum"`
	SourceCommit string                `json:"source_commit"`
	Files        int                   `json:"files"`
	Bytes        int64                 `json:"bytes"`
	Patch        goreleaserSourcePatch `json:"patch"`
	Manifests    *goreleaserManifests  `json:"qualified_manifests"`
}

func (profile refmtSourceProfile) sourceProfile() goreleaserProfile {
	return goreleaserProfile{
		Module: profile.Module, Version: profile.Version, Sum: profile.Sum,
		GoModSum: profile.GoModSum, SourceCommit: profile.SourceCommit, Files: profile.Files,
		Bytes: profile.Bytes, Patch: profile.Patch, SourceManifests: profile.Manifests,
	}
}

type goreleaserSourcePatch struct {
	Path         string `json:"path"`
	Before       string `json:"before"`
	After        string `json:"after"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

type goreleaserManifests struct {
	ModSHA256 string `json:"go_mod_sha256"`
	SumSHA256 string `json:"go_sum_sha256"`
}

func loadGoReleaserProfile(repository string) (goreleaserProfile, error) {
	var profile goreleaserProfile
	if _, err := validateAbsoluteExistingDirectory(repository, "publisher repository"); err != nil {
		return profile, err
	}
	if _, err := validateAbsoluteExistingDirectory(goreleaserManifestDirectory(repository),
		"publisher manifest directory"); err != nil {
		return profile, err
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return profile, fmt.Errorf("open publisher repository: %w", err)
	}
	data, readErr := readGoReleaserFile(root, toolprofile.GoReleaserSource)
	closeErr := root.Close()
	if readErr != nil || closeErr != nil {
		return profile, errors.Join(readErr, closeErr)
	}
	if err := validateStrictJSONDocument(data, "publisher source profile"); err != nil {
		return profile, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return profile, fmt.Errorf("decode publisher source profile: %w", err)
	}
	return profile, profile.validate()
}

func (profile goreleaserProfile) validate() error {
	if profile.Module != toolprofile.GoReleaserModule || profile.Patch.Path != "internal/pipe/ko/ko.go" ||
		profile.Refmt == nil || profile.Refmt.Module != toolprofile.RefmtModule ||
		profile.Refmt.Patch.Path != "cmd/refmt/helper_yaml.go" {
		return fmt.Errorf("publisher component roles differ from closed source layout: %w", errInvalidConfig)
	}
	if err := profile.validateIdentity(); err != nil {
		return err
	}
	return errors.Join(profile.Patch.validate(), profile.Refmt.sourceProfile().validateIdentity(),
		profile.Refmt.Patch.validate())
}

func (profile goreleaserProfile) validateIdentity() error {
	if len(profile.Version) < 2 ||
		!strings.HasPrefix(profile.Version, "v") || hasControl(profile.Version) ||
		strings.ContainsAny(profile.Version, " \t\r\n@/\\") ||
		profile.Files <= 0 || profile.Files > goreleaserSourceFileLimit || profile.Bytes <= 0 ||
		profile.Bytes > goreleaserSourceByteLimit {
		return fmt.Errorf("invalid publisher source identity or bounds: %w", errInvalidConfig)
	}
	if _, err := validateFullCommitSHA(profile.SourceCommit, "publisher source"); err != nil {
		return err
	}
	if !validGoReleaserSum(profile.Sum) || !validGoReleaserSum(profile.GoModSum) {
		return fmt.Errorf("invalid publisher Go source checksums: %w", errInvalidConfig)
	}
	return nil
}

func (patch goreleaserSourcePatch) validate() error {
	if (patch.Path != "internal/pipe/ko/ko.go" && patch.Path != "cmd/refmt/helper_yaml.go") ||
		patch.Before == "" || patch.After == "" ||
		patch.Before == patch.After || !validGoReleaserHash(patch.BeforeSHA256) ||
		!validGoReleaserHash(patch.AfterSHA256) {
		return fmt.Errorf("invalid publisher single-import patch: %w", errInvalidConfig)
	}
	return nil
}

func (profile goreleaserProfile) requireManifests() error {
	if err := profile.validate(); err != nil {
		return err
	}
	if profile.Refmt == nil {
		return fmt.Errorf("publisher Refmt source profile is required: %w", errInvalidConfig)
	}
	for _, manifests := range []*goreleaserManifests{profile.Manifests, profile.SourceManifests, profile.Refmt.Manifests} {
		if manifests == nil || !validGoReleaserHash(manifests.ModSHA256) || !validGoReleaserHash(manifests.SumSHA256) {
			return fmt.Errorf("publisher frozen manifests await security qualification: %w", errInvalidConfig)
		}
	}
	return nil
}

func validGoReleaserSum(value string) bool {
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "h1:"))
	return strings.HasPrefix(value, "h1:") && err == nil && len(data) == sha256.Size
}

func validGoReleaserHash(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && value == strings.ToLower(value)
}

func readGoReleaserFile(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > goreleaserFileLimit {
		return nil, fmt.Errorf("publisher file %s is not bounded and regular: %w", name, errors.Join(err, errInvalidConfig))
	}
	file, err := openRootedRegularFile(root, name, info, "publisher source input")
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, goreleaserFileLimit+1))
	verifyErr := errors.Join(verifyOpenedRegularFile(file, info, "publisher source input"),
		verifyRootedPathIdentity(root, name, file, "publisher source input"))
	closeErr := file.Close()
	if readErr != nil || verifyErr != nil || closeErr != nil || len(data) > goreleaserFileLimit {
		return nil, errors.Join(wrapOptional("read publisher source input", readErr), verifyErr, closeErr, errInvalidConfig)
	}
	return data, nil
}

func goreleaserManifestDirectory(repository string) string {
	return filepath.Join(repository, filepath.Dir(toolprofile.GoReleaserSource))
}
