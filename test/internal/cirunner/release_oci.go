package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	ociReferenceTypeAnnotation   = "vnd.docker.reference.type"
	ociReferenceDigestAnnotation = "vnd.docker.reference.digest"
	ociAttestationManifestType   = "attestation-manifest"
	ociSLSABuildTypeV1           = "https://github.com/moby/buildkit/blob/master/docs/attestations/slsa-definitions.md"
	releaseOCIImageCount         = 3
	releaseOCIDigestReceiptLimit = 1 << 10
)

// ReleaseOCIEvidenceOptions supplies the immutable OCI release identity and output root.
type ReleaseOCIEvidenceOptions struct {
	Root        string
	ReceiptRoot string
	RefName     string
	Environment []string
	Runner      SpecRunner
}

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

type ociAttestationManifest struct {
	Layers []ociAttestationLayer `json:"layers"`
}

type ociAttestationLayer struct {
	Annotations map[string]string `json:"annotations"`
}

type ociSPDXDocument struct {
	SPDXID            string            `json:"SPDXID"`
	DataLicense       string            `json:"dataLicense"`
	SPDXVersion       string            `json:"spdxVersion"`
	DocumentNamespace string            `json:"documentNamespace"`
	Packages          []json.RawMessage `json:"packages"`
}

type ociSLSAProvenance struct {
	BuildDefinition struct {
		BuildType            string            `json:"buildType"`
		ResolvedDependencies []json.RawMessage `json:"resolvedDependencies"`
	} `json:"buildDefinition"`
	RunDetails struct {
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
		Metadata struct {
			InvocationID string `json:"invocationId"`
			StartedOn    string `json:"startedOn"`
			FinishedOn   string `json:"finishedOn"`
		} `json:"metadata"`
	} `json:"runDetails"`
}

// ReleaseOCIEvidence validates all versioned OCI images and atomically records their index digests.
func ReleaseOCIEvidence(ctx context.Context, options ReleaseOCIEvidenceOptions) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	receiptRootPath, err := validateAbsoluteExistingDirectory(options.ReceiptRoot, "OCI digest receipt root")
	if err != nil {
		return err
	}
	receiptRoot, err := os.OpenRoot(receiptRootPath)
	if err != nil {
		return fmt.Errorf("open OCI digest receipt root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close OCI digest receipt root", receiptRoot.Close()))
	}()
	const receiptName = "release-image-digests.txt"
	if targetErr := validateRootedRegularTarget(receiptRoot, receiptName, "OCI digest receipt"); targetErr != nil {
		return targetErr
	}

	evidence, err := inspectReleaseOCIManifests(ctx, options.Runner, root, options.RefName, options.Environment)
	if err != nil {
		return err
	}
	if err := validateReleaseOCIAttestations(ctx, options.Runner, root, evidence, options.Environment); err != nil {
		return err
	}
	if evidence[0].Digest != evidence[1].Digest {
		return fmt.Errorf("primary and Debian versioned OCI tags differ: %w", errInvalidConfig)
	}
	receipt := renderReleaseOCIDigestReceipt(evidence)
	if err := validateRootPathIdentity(receiptRoot, receiptRootPath, "OCI digest receipt root before publication"); err != nil {
		return err
	}
	if err := writeRootedArtifact(
		receiptRoot, receiptName, receipt, "OCI digest receipt", releaseOCIDigestReceiptLimit,
	); err != nil {
		return err
	}
	if err := validateRootPathIdentity(receiptRoot, receiptRootPath, "OCI digest receipt root after publication"); err != nil {
		return err
	}
	return nil
}

func renderReleaseOCIDigestReceipt(evidence []releaseOCIImageEvidence) []byte {
	var receipt strings.Builder
	for _, image := range evidence {
		receipt.WriteString(image.Image)
		receipt.WriteByte(' ')
		receipt.WriteString(image.Digest)
		receipt.WriteByte('\n')
	}
	return []byte(receipt.String())
}

func validateReleaseOCIAttestations(
	ctx context.Context,
	runner SpecRunner,
	root string,
	evidence []releaseOCIImageEvidence,
	environment []string,
) error {
	repositoryRoot, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if runner == nil || len(evidence) != releaseOCIImageCount {
		return fmt.Errorf("OCI attestation evidence and command runner are required: %w", errInvalidConfig)
	}
	dockerEnvironment := withoutEnvironmentKeys(environment, "GH_TOKEN", "GITHUB_TOKEN")
	for _, image := range evidence {
		if !canonicalOCIDigest(image.Digest) {
			return fmt.Errorf("OCI image %s has invalid index digest: %w", image.Image, errInvalidConfig)
		}
		pinnedImage := image.Image + "@" + image.Digest
		for _, platform := range []string{"linux/amd64", "linux/arm64"} {
			runnableDigest, exists := image.Runnable[platform]
			if !exists {
				return fmt.Errorf("OCI image %s lacks runnable platform %s: %w", image.Image, platform, errInvalidConfig)
			}
			attestationDigest, exists := image.Attestations[runnableDigest]
			if !exists {
				return fmt.Errorf("OCI image %s lacks attestation for %s: %w", image.Image, platform, errInvalidConfig)
			}
			if err := validateOCIAttestationManifest(
				ctx, runner, repositoryRoot, image.Image, attestationDigest, dockerEnvironment,
			); err != nil {
				return err
			}
			if err := validateOCISBOM(ctx, runner, repositoryRoot, pinnedImage, platform, dockerEnvironment); err != nil {
				return err
			}
			if err := validateOCIProvenance(ctx, runner, repositoryRoot, pinnedImage, platform, dockerEnvironment); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOCIAttestationManifest(
	ctx context.Context,
	runner SpecRunner,
	root string,
	image string,
	digest string,
	environment []string,
) error {
	if !canonicalOCIDigest(digest) {
		return fmt.Errorf("OCI attestation for %s has invalid digest: %w", image, errInvalidConfig)
	}
	data, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "docker", Args: []string{"buildx", "imagetools", "inspect", "--raw", image + "@" + digest},
		Dir: root, Env: environment,
	}, "inspect OCI attestation manifest "+image)
	if err != nil {
		return err
	}
	if err := validateStrictJSONDocument(data, "OCI attestation manifest "+image); err != nil {
		return err
	}
	if err := validateOCIAttestationJSONFields(data, image); err != nil {
		return err
	}
	manifest := ociAttestationManifest{}
	if err := decodeJSONDocument(data, &manifest, "OCI attestation manifest "+image); err != nil {
		return err
	}
	hasSPDX := false
	hasSLSA := false
	for _, layer := range manifest.Layers {
		predicateType := layer.Annotations["in-toto.io/predicate-type"]
		hasSPDX = hasSPDX || predicateType == "https://spdx.dev/Document"
		hasSLSA = hasSLSA || strings.HasPrefix(predicateType, "https://slsa.dev/provenance/")
	}
	if len(manifest.Layers) == 0 || !hasSPDX || !hasSLSA {
		return fmt.Errorf("OCI attestation manifest %s lacks SPDX or SLSA layers: %w", image, errInvalidConfig)
	}
	return nil
}

func validateOCISBOM(
	ctx context.Context,
	runner SpecRunner,
	root string,
	image string,
	platform string,
	environment []string,
) error {
	format := `{{json (index .SBOM "` + platform + `").SPDX}}`
	data, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "docker", Args: []string{"buildx", "imagetools", "inspect", image, "--format", format},
		Dir: root, Env: environment,
	}, "inspect OCI SPDX SBOM "+image+" "+platform)
	if err != nil {
		return err
	}
	if err := validateStrictJSONDocument(data, "OCI SPDX SBOM "+image+" "+platform); err != nil {
		return err
	}
	if _, err := decodeRequiredJSONObject(
		data, "OCI SPDX SBOM fields", []string{"SPDXID", "dataLicense", "spdxVersion", "documentNamespace", "packages"},
	); err != nil {
		return err
	}
	document := ociSPDXDocument{}
	if err := decodeJSONDocument(data, &document, "OCI SPDX SBOM "+image+" "+platform); err != nil {
		return err
	}
	if document.SPDXID != "SPDXRef-DOCUMENT" || document.DataLicense != "CC0-1.0" ||
		!strings.HasPrefix(document.SPDXVersion, "SPDX-") || document.DocumentNamespace == "" || len(document.Packages) == 0 {
		return fmt.Errorf("OCI SPDX SBOM %s %s violates its document contract: %w", image, platform, errInvalidConfig)
	}
	return nil
}

func validateOCIProvenance(
	ctx context.Context,
	runner SpecRunner,
	root string,
	image string,
	platform string,
	environment []string,
) error {
	format := `{{json (index .Provenance "` + platform + `").SLSA}}`
	data, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "docker", Args: []string{"buildx", "imagetools", "inspect", image, "--format", format},
		Dir: root, Env: environment,
	}, "inspect OCI SLSA provenance "+image+" "+platform)
	if err != nil {
		return err
	}
	if err := validateStrictJSONDocument(data, "OCI SLSA provenance "+image+" "+platform); err != nil {
		return err
	}
	if err := validateOCIProvenanceJSONFields(data); err != nil {
		return err
	}
	provenance := ociSLSAProvenance{}
	if err := decodeJSONDocument(data, &provenance, "OCI SLSA provenance "+image+" "+platform); err != nil {
		return err
	}
	if provenance.BuildDefinition.BuildType != ociSLSABuildTypeV1 ||
		len(provenance.BuildDefinition.ResolvedDependencies) == 0 ||
		provenance.RunDetails.Metadata.InvocationID == "" || provenance.RunDetails.Metadata.StartedOn == "" ||
		provenance.RunDetails.Metadata.FinishedOn == "" {
		return fmt.Errorf("OCI SLSA provenance %s %s violates its payload contract: %w", image, platform, errInvalidConfig)
	}
	return nil
}

func validateOCIAttestationJSONFields(data []byte, image string) error {
	root, err := decodeRequiredJSONObject(data, "OCI attestation fields "+image, []string{"layers"})
	if err != nil {
		return err
	}
	layers := []map[string]json.RawMessage{}
	if err := decodeJSONDocument(root["layers"], &layers, "OCI attestation layer fields "+image); err != nil {
		return err
	}
	for index, layer := range layers {
		if layer == nil {
			return fmt.Errorf("OCI attestation layer %s/%d is not an object: %w", image, index, errInvalidConfig)
		}
		if err := rejectJSONFieldAliases(layer, []string{"annotations"}); err != nil {
			return err
		}
	}
	return nil
}

func validateOCIProvenanceJSONFields(data []byte) error {
	root, err := decodeRequiredJSONObject(data, "OCI provenance fields", []string{"buildDefinition", "runDetails"})
	if err != nil {
		return err
	}
	_, err = decodeRequiredJSONObject(
		root["buildDefinition"], "OCI provenance build definition fields", []string{"buildType", "resolvedDependencies"},
	)
	if err != nil {
		return err
	}
	runDetails, err := decodeRequiredJSONObject(
		root["runDetails"], "OCI provenance run details fields", []string{"builder", "metadata"},
	)
	if err != nil {
		return err
	}
	builder, err := decodeRequiredJSONObject(runDetails["builder"], "OCI provenance builder fields", []string{"id"})
	if err != nil {
		return err
	}
	var builderID any
	if err := decodeJSONDocument(builder["id"], &builderID, "OCI provenance builder id"); err != nil {
		return err
	}
	if _, valid := builderID.(string); !valid {
		return fmt.Errorf("OCI provenance builder id is not a string: %w", errInvalidConfig)
	}
	if _, err := decodeRequiredJSONObject(
		runDetails["metadata"], "OCI provenance metadata fields",
		[]string{"invocationId", "startedOn", "finishedOn"},
	); err != nil {
		return err
	}
	return nil
}

func decodeRequiredJSONObject(data []byte, purpose string, required []string) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := decodeJSONDocument(data, &fields, purpose); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("%s is not an object: %w", purpose, errInvalidConfig)
	}
	if err := rejectJSONFieldAliases(fields, required); err != nil {
		return nil, err
	}
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return nil, fmt.Errorf("%s lacks field %q: %w", purpose, name, errInvalidConfig)
		}
	}
	return fields, nil
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
		if validationErr := validateStrictJSONDocument(data, "OCI manifest "+image); validationErr != nil {
			return nil, validationErr
		}
		if fieldsErr := validateOCIManifestJSONFields(data, image); fieldsErr != nil {
			return nil, fieldsErr
		}
		index := ociManifestIndex{}
		if decodeErr := decodeJSONDocument(data, &index, "OCI manifest "+image); decodeErr != nil {
			return nil, decodeErr
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
