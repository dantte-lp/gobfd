// Package npmbootstrap installs three verified upstream archives without executing npm.
package npmbootstrap

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

const (
	// CLIPath identifies the unchanged upstream npm entrypoint under the private install root.
	CLIPath          = "npm-bootstrap/bin/npm-cli.js"
	contractLimit    = 16 << 10
	archiveLimit     = 8 << 20
	expandedLimit    = 32 << 20
	entryLimit       = 4096
	artifactCount    = 3
	versionPartCount = 3
)

// ErrInvalid marks a rejected bootstrap contract or archive.
var ErrInvalid = errors.New("invalid npm bootstrap")

// Contract pins the complete npm bundle and its two compatible dependency replacements.
type Contract struct {
	SchemaVersion int        `json:"schema_version"`
	Artifacts     []Artifact `json:"artifacts"`
}

// Artifact binds an official registry archive to exact integrity and resource bounds.
type Artifact struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Integrity     string `json:"integrity"`
	ArchiveBytes  int64  `json:"archive_bytes"`
	ExpandedBytes int64  `json:"expanded_bytes"`
	Entries       int    `json:"entries"`
}

// Parse accepts one bounded document with exact field spelling and no duplicates.
func Parse(data []byte) (Contract, error) {
	if len(data) > contractLimit {
		return Contract{}, fmt.Errorf("bootstrap document size: %w", ErrInvalid)
	}
	if err := strictjson.Validate(data); err != nil {
		return Contract{}, fmt.Errorf("validate bootstrap JSON: %w", errors.Join(err, ErrInvalid))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var contract Contract
	err := readObject(decoder, func(key string) error {
		switch key {
		case "schema_version":
			return decodeField(decoder, &contract.SchemaVersion)
		case "artifacts":
			var err error
			contract.Artifacts, err = readArtifacts(decoder)
			return err
		default:
			return fmt.Errorf("unknown bootstrap field %q: %w", key, ErrInvalid)
		}
	})
	if err != nil {
		return Contract{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Contract{}, fmt.Errorf("trailing bootstrap document data: %w", ErrInvalid)
	}
	if err := contract.validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

func readObject(decoder *json.Decoder, field func(string) error) error {
	if err := readDelimiter(decoder, '{'); err != nil {
		return err
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read bootstrap field: %w", errors.Join(err, ErrInvalid))
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("non-string bootstrap field: %w", ErrInvalid)
		}
		if err := field(key); err != nil {
			return err
		}
	}
	return readDelimiter(decoder, '}')
}

func readDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil || token != want {
		return fmt.Errorf("read bootstrap delimiter %q: %w", want, errors.Join(err, ErrInvalid))
	}
	return nil
}

func decodeField(decoder *json.Decoder, target any) error {
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode bootstrap field: %w", errors.Join(err, ErrInvalid))
	}
	return nil
}

func readArtifacts(decoder *json.Decoder) ([]Artifact, error) {
	if err := readDelimiter(decoder, '['); err != nil {
		return nil, err
	}
	artifacts := make([]Artifact, 0, artifactCount)
	for decoder.More() {
		if len(artifacts) == artifactCount {
			return nil, fmt.Errorf("bootstrap has too many artifacts: %w", ErrInvalid)
		}
		var artifact Artifact
		if err := readObject(decoder, func(key string) error {
			return readArtifactField(decoder, key, &artifact)
		}); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, readDelimiter(decoder, ']')
}

func readArtifactField(decoder *json.Decoder, key string, artifact *Artifact) error {
	switch key {
	case "name":
		return decodeField(decoder, &artifact.Name)
	case "version":
		return decodeField(decoder, &artifact.Version)
	case "integrity":
		return decodeField(decoder, &artifact.Integrity)
	case "archive_bytes":
		return decodeField(decoder, &artifact.ArchiveBytes)
	case "expanded_bytes":
		return decodeField(decoder, &artifact.ExpandedBytes)
	case "entries":
		return decodeField(decoder, &artifact.Entries)
	default:
		return fmt.Errorf("unknown bootstrap artifact field %q: %w", key, ErrInvalid)
	}
}

func (contract Contract) validate() error {
	if contract.SchemaVersion != 1 || len(contract.Artifacts) != artifactCount {
		return fmt.Errorf("bootstrap requires schema 1 and three archives: %w", ErrInvalid)
	}
	for index, name := range []string{"npm", "undici", "ip-address"} {
		if err := contract.Artifacts[index].validate(name); err != nil {
			return err
		}
	}
	return nil
}

func (artifact Artifact) validate(name string) error {
	digest, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(artifact.Integrity, "sha512-"))
	if artifact.Name != name || !canonicalVersion(artifact.Version) ||
		!strings.HasPrefix(artifact.Integrity, "sha512-") || err != nil || len(digest) != sha512.Size ||
		"sha512-"+base64.StdEncoding.EncodeToString(digest) != artifact.Integrity {
		return fmt.Errorf("bootstrap artifact %s identity or integrity: %w", name, ErrInvalid)
	}
	if artifact.ArchiveBytes <= 0 || artifact.ArchiveBytes > archiveLimit ||
		artifact.ExpandedBytes <= 0 || artifact.ExpandedBytes > expandedLimit ||
		artifact.Entries <= 0 || artifact.Entries > entryLimit {
		return fmt.Errorf("bootstrap artifact %s resource bounds: %w", name, ErrInvalid)
	}
	return nil
}

func canonicalVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != versionPartCount {
		return false
	}
	for _, part := range parts {
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(number, 10) != part {
			return false
		}
	}
	return true
}
