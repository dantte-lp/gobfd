package cirunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ociReferenceTypeAnnotation   = "vnd.docker.reference.type"
	ociReferenceDigestAnnotation = "vnd.docker.reference.digest"
	ociAttestationManifestType   = "attestation-manifest"
)

type ociManifestIndex struct {
	Digest    string                  `json:"digest"`
	Manifests []ociManifestDescriptor `json:"manifests"`
}

type ociManifestDescriptor struct {
	Digest      string            `json:"digest"`
	Platform    ociPlatform       `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}

type ociPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type releaseOCIImageEvidence struct {
	Image        string
	Digest       string
	Runnable     map[string]string
	Attestations map[string]string
}

func inspectReleaseOCIManifests(
	ctx context.Context,
	runner SpecRunner,
	root string,
	refName string,
	environment []string,
) ([]releaseOCIImageEvidence, error) {
	repositoryRoot, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return nil, err
	}
	version, _, err := parseStableReleaseVersion(refName)
	if err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, fmt.Errorf("OCI manifest command runner is required: %w", errInvalidConfig)
	}
	images := []string{
		"ghcr.io/dantte-lp/gobfd:" + version,
		"ghcr.io/dantte-lp/gobfd:" + version + "-debian-trixie",
		"ghcr.io/dantte-lp/gobfd:" + version + "-oraclelinux10",
	}
	dockerEnvironment := withoutEnvironmentKeys(environment, "GH_TOKEN", "GITHUB_TOKEN")
	evidence := make([]releaseOCIImageEvidence, 0, len(images))
	for _, image := range images {
		data, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
			Name: "docker", Args: []string{
				"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}", image,
			}, Dir: repositoryRoot, Env: dockerEnvironment,
		}, "inspect versioned OCI manifest "+image)
		if err != nil {
			return nil, err
		}
		if err := validateStrictJSONDocument(data, "OCI manifest "+image); err != nil {
			return nil, err
		}
		if err := validateOCIManifestJSONFields(data, image); err != nil {
			return nil, err
		}
		index := ociManifestIndex{}
		if err := decodeJSONDocument(data, &index, "OCI manifest "+image); err != nil {
			return nil, err
		}
		item, err := validateOCIManifestIndex(image, index)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, item)
	}
	return evidence, nil
}

func validateOCIManifestIndex(image string, index ociManifestIndex) (releaseOCIImageEvidence, error) {
	if !canonicalOCIDigest(index.Digest) || len(index.Manifests) != 4 {
		return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s violates digest or descriptor count: %w", image, errInvalidConfig)
	}
	runnable := make(map[string]string, 2)
	attestations := make(map[string]string, 2)
	descriptorDigests := make(map[string]struct{}, len(index.Manifests))
	for descriptorIndex, descriptor := range index.Manifests {
		if !canonicalOCIDigest(descriptor.Digest) {
			return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s descriptor %d has invalid digest: %w", image, descriptorIndex, errInvalidConfig)
		}
		if _, duplicate := descriptorDigests[descriptor.Digest]; duplicate {
			return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s duplicates descriptor digest: %w", image, errInvalidConfig)
		}
		descriptorDigests[descriptor.Digest] = struct{}{}
		referenceType, hasReferenceType := descriptor.Annotations[ociReferenceTypeAnnotation]
		referenceDigest, hasReferenceDigest := descriptor.Annotations[ociReferenceDigestAnnotation]
		if !hasReferenceType && !hasReferenceDigest {
			platform := descriptor.Platform.OS + "/" + descriptor.Platform.Architecture
			if platform != "linux/amd64" && platform != "linux/arm64" {
				return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s has unexpected runnable platform %s: %w", image, platform, errInvalidConfig)
			}
			if _, duplicate := runnable[platform]; duplicate {
				return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s has duplicate or linked runnable platform %s: %w", image, platform, errInvalidConfig)
			}
			runnable[platform] = descriptor.Digest
			continue
		}
		if !hasReferenceType || !hasReferenceDigest || referenceType != ociAttestationManifestType ||
			descriptor.Platform.OS != "unknown" ||
			descriptor.Platform.Architecture != "unknown" {
			return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s has unexpected referenced descriptor: %w", image, errInvalidConfig)
		}
		subject := referenceDigest
		if !canonicalOCIDigest(subject) {
			return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s has invalid attestation subject: %w", image, errInvalidConfig)
		}
		if _, duplicate := attestations[subject]; duplicate {
			return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s duplicates attestation subject: %w", image, errInvalidConfig)
		}
		attestations[subject] = descriptor.Digest
	}
	if len(runnable) != 2 || len(attestations) != 2 {
		return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s lacks exact runnable and attestation pairs: %w", image, errInvalidConfig)
	}
	for _, digest := range runnable {
		if _, exists := attestations[digest]; !exists {
			return releaseOCIImageEvidence{}, fmt.Errorf("OCI index %s lacks attestation for runnable digest %s: %w", image, digest, errInvalidConfig)
		}
	}
	return releaseOCIImageEvidence{
		Image: image, Digest: index.Digest, Runnable: runnable, Attestations: attestations,
	}, nil
}

func canonicalOCIDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validateOCIManifestJSONFields(data []byte, image string) error {
	root := map[string]json.RawMessage{}
	if err := decodeJSONDocument(data, &root, "OCI manifest fields "+image); err != nil {
		return err
	}
	if err := rejectJSONFieldAliases(root, []string{"digest", "manifests"}); err != nil {
		return err
	}
	rawManifests, exists := root["manifests"]
	if !exists {
		return fmt.Errorf("OCI manifest %s lacks manifests field: %w", image, errInvalidConfig)
	}
	descriptors := []map[string]json.RawMessage{}
	if err := decodeJSONDocument(rawManifests, &descriptors, "OCI descriptors "+image); err != nil {
		return err
	}
	for index, descriptor := range descriptors {
		if descriptor == nil {
			return fmt.Errorf("OCI descriptor %s/%d is not an object: %w", image, index, errInvalidConfig)
		}
		if err := rejectJSONFieldAliases(descriptor, []string{"digest", "platform", "annotations"}); err != nil {
			return err
		}
		platformData, exists := descriptor["platform"]
		if !exists {
			return fmt.Errorf("OCI descriptor %s/%d lacks platform: %w", image, index, errInvalidConfig)
		}
		platform := map[string]json.RawMessage{}
		if err := decodeJSONDocument(platformData, &platform, "OCI descriptor platform"); err != nil {
			return err
		}
		if err := rejectJSONFieldAliases(platform, []string{"os", "architecture"}); err != nil {
			return err
		}
	}
	return nil
}
