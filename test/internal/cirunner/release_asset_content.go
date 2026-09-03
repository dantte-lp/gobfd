package cirunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"slices"
	"sort"
	"strings"
)

const (
	releaseChecksumFileLimit    = 64 << 10
	releaseReportsExpandedLimit = 256 << 20
	releaseReportsEntryLimit    = 4096
)

func validateReleaseAssetContents(
	downloadRoot *os.Root,
	artifactRoot *os.Root,
	runnerTempRoot *os.Root,
	version string,
	refName string,
) error {
	expectedChecksummed := expectedChecksummedArtifactNames(version)
	expectedReceipt, err := readRootedRegularFile(
		runnerTempRoot, "expected-checksummed-assets.txt",
		"expected checksummed release asset manifest", releaseArtifactsManifestLimit,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(expectedReceipt, renderArtifactNames(expectedChecksummed)) {
		return fmt.Errorf("expected checksummed release asset manifest is not canonical: %w", errInvalidConfig)
	}
	sbomNames := make([]string, 0, 2)
	for _, name := range expectedChecksummed {
		if strings.HasSuffix(name, ".sbom.json") {
			sbomNames = append(sbomNames, name)
		}
	}
	if len(sbomNames) != 2 {
		return fmt.Errorf("canonical release matrix does not contain exactly two SBOMs: %w", errInvalidConfig)
	}
	mainChecksums, err := readRootedRegularFile(
		downloadRoot, "checksums.txt", "release checksum manifest", releaseChecksumFileLimit,
	)
	if err != nil {
		return err
	}
	mainRecords, err := parseReleaseChecksumRecords(mainChecksums, "release checksum manifest", expectedChecksummed)
	if err != nil {
		return err
	}
	mainSnapshots, err := validateReleaseChecksums(
		downloadRoot, mainRecords, expectedChecksummed, sbomNames,
	)
	if err != nil {
		return err
	}

	downloadedSupplemental, err := readRootedRegularFile(
		downloadRoot, "release-evidence-checksums.txt",
		"downloaded supplemental checksum receipt", releaseEvidenceChecksumLimit,
	)
	if err != nil {
		return err
	}
	localSupplemental, err := readRootedRegularFile(
		artifactRoot, "release-evidence-checksums.txt",
		"local supplemental checksum receipt", releaseEvidenceChecksumLimit,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(downloadedSupplemental, localSupplemental) {
		return fmt.Errorf("downloaded supplemental checksum receipt differs from local evidence: %w", errInvalidConfig)
	}
	reportName := "gobfd-" + refName + "-reports.tar.gz"
	supplementalNames := []string{reportName, "release-image-digests.txt"}
	sort.Strings(supplementalNames)
	supplementalRecords, err := parseReleaseChecksumRecords(
		downloadedSupplemental, "supplemental checksum receipt", supplementalNames,
	)
	if err != nil {
		return err
	}
	supplementalSnapshots, err := validateReleaseChecksums(
		downloadRoot, supplementalRecords, supplementalNames, supplementalNames,
	)
	if err != nil {
		return err
	}
	downloadedDigests := supplementalSnapshots["release-image-digests.txt"]
	localDigests, err := readRootedRegularFile(
		artifactRoot, "release-image-digests.txt", "local OCI digest receipt", releaseOCIDigestReceiptLimit,
	)
	if err != nil {
		return err
	}
	if err := validateReleaseOCIDigestReceipt(downloadedDigests, version); err != nil {
		return err
	}
	if !bytes.Equal(downloadedDigests, localDigests) {
		return fmt.Errorf("downloaded OCI digest receipt differs from local evidence: %w", errInvalidConfig)
	}
	if err := validateReleaseReportsArchive(supplementalSnapshots[reportName]); err != nil {
		return err
	}
	for _, name := range sbomNames {
		if err := validateReleaseCycloneDXSBOM(mainSnapshots[name], name); err != nil {
			return err
		}
	}
	return nil
}

func parseReleaseChecksumRecords(data []byte, purpose string, expectedNames []string) (map[string]string, error) {
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) != len(expectedNames)+1 || len(lines[len(lines)-1]) != 0 {
		return nil, fmt.Errorf("%s must contain exactly %d newline-terminated records: %w", purpose, len(expectedNames), errInvalidConfig)
	}
	records := make(map[string]string, len(expectedNames))
	actualNames := make([]string, 0, len(expectedNames))
	for index, line := range lines[:len(lines)-1] {
		if len(line) < sha256.Size*2+3 || line[sha256.Size*2] != ' ' || line[sha256.Size*2+1] != ' ' {
			return nil, fmt.Errorf("%s record %d is not canonical: %w", purpose, index, errInvalidConfig)
		}
		digestText := string(line[:sha256.Size*2])
		digest, err := hex.DecodeString(digestText)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != digestText {
			return nil, fmt.Errorf("%s record %d has a noncanonical SHA-256: %w", purpose, index, errors.Join(err, errInvalidConfig))
		}
		name := string(line[sha256.Size*2+2:])
		if !isCanonicalReleaseChecksumName(name) {
			return nil, fmt.Errorf("%s record %d has an unsafe asset name: %w", purpose, index, errInvalidConfig)
		}
		if _, exists := records[name]; exists {
			return nil, fmt.Errorf("%s contains duplicate asset %s: %w", purpose, name, errInvalidConfig)
		}
		records[name] = digestText
		actualNames = append(actualNames, name)
	}
	sort.Strings(actualNames)
	if !slices.Equal(actualNames, expectedNames) {
		return nil, fmt.Errorf("%s asset set differs from the exact manifest: %w", purpose, errInvalidConfig)
	}
	return records, nil
}

func isCanonicalReleaseChecksumName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, character := range []byte(name) {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._+-", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validateReleaseChecksums(
	root *os.Root,
	records map[string]string,
	names []string,
	retainNames []string,
) (map[string][]byte, error) {
	retain := make(map[string]struct{}, len(retainNames))
	for _, name := range retainNames {
		retain[name] = struct{}{}
	}
	snapshots := make(map[string][]byte, len(retain))
	for _, name := range names {
		data, err := readRootedRegularFile(root, name, "checksummed release asset", releaseArtifactLimit)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		if fmt.Sprintf("%x", digest) != records[name] {
			return nil, fmt.Errorf("release asset %s SHA-256 mismatch: %w", name, errInvalidConfig)
		}
		if _, keep := retain[name]; keep {
			snapshots[name] = data
		}
	}
	if len(snapshots) != len(retain) {
		return nil, fmt.Errorf("checksummed release snapshot set is incomplete: %w", errInvalidConfig)
	}
	return snapshots, nil
}

func validateReleaseCycloneDXSBOM(data []byte, name string) error {
	purpose := "release CycloneDX SBOM " + name
	if err := validateStrictJSONDocument(data, purpose); err != nil {
		return err
	}
	fields, err := decodeRequiredJSONObject(
		data, purpose, []string{"bomFormat", "specVersion", "metadata", "components"},
	)
	if err != nil {
		return err
	}
	bomFormat, err := decodeRequiredJSONString(fields["bomFormat"], purpose+" format")
	if err != nil {
		return err
	}
	if bomFormat != "CycloneDX" {
		return fmt.Errorf("%s has unexpected bomFormat: %w", purpose, errInvalidConfig)
	}
	if _, err := decodeRequiredJSONString(fields["specVersion"], purpose+" specVersion"); err != nil {
		return err
	}
	metadata := map[string]json.RawMessage{}
	if err := decodeJSONDocument(fields["metadata"], &metadata, purpose+" metadata"); err != nil {
		return err
	}
	if len(metadata) == 0 {
		return fmt.Errorf("%s metadata must be a nonempty object: %w", purpose, errInvalidConfig)
	}
	components := []json.RawMessage{}
	if err := decodeJSONDocument(fields["components"], &components, purpose+" components"); err != nil {
		return err
	}
	if len(components) == 0 {
		return fmt.Errorf("%s components must be a nonempty array: %w", purpose, errInvalidConfig)
	}
	for index, componentJSON := range components {
		component := map[string]json.RawMessage{}
		if err := decodeJSONDocument(componentJSON, &component, fmt.Sprintf("%s component %d", purpose, index)); err != nil {
			return err
		}
		if len(component) == 0 {
			return fmt.Errorf("%s component %d must be a nonempty object: %w", purpose, index, errInvalidConfig)
		}
	}
	return nil
}

func validateReleaseReportsArchive(data []byte) (returnErr error) {
	source := bytes.NewReader(data)
	decompressor, err := gzip.NewReader(source)
	if err != nil {
		return fmt.Errorf("open release reports gzip stream: %w", err)
	}
	decompressor.Multistream(false)
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release reports gzip stream", decompressor.Close()))
	}()
	limited := &io.LimitedReader{R: decompressor, N: releaseReportsExpandedLimit + 1}
	reader := tar.NewReader(limited)
	seen := make(map[string]struct{})
	entryCount := 0
	regularCount := 0
	foundReportsRoot := false
	var contentSize int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read release reports tar header: %w", err)
		}
		entryCount++
		if entryCount > releaseReportsEntryLimit {
			return fmt.Errorf("release reports archive exceeds %d entries: %w", releaseReportsEntryLimit, errInvalidConfig)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || pathpkg.IsAbs(name) || pathpkg.Clean(name) != name ||
			(name != "reports" && !strings.HasPrefix(name, "reports/")) || hasControl(name) {
			return fmt.Errorf("release reports archive path %q is unsafe: %w", header.Name, errInvalidConfig)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("release reports archive contains duplicate path %s: %w", name, errInvalidConfig)
		}
		seen[name] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 || header.Linkname != "" {
				return fmt.Errorf("release reports directory %s has invalid metadata: %w", name, errInvalidConfig)
			}
			if name == "reports" {
				foundReportsRoot = true
			}
		case tar.TypeReg, tar.TypeRegA:
			if !strings.HasPrefix(name, "reports/") {
				return fmt.Errorf("release reports regular file %s is not a reports descendant: %w", name, errInvalidConfig)
			}
			if header.Linkname != "" || header.Size < 0 || header.Size > releaseArtifactLimit ||
				header.Size > releaseReportsExpandedLimit-contentSize {
				return fmt.Errorf("release reports file %s exceeds content bounds: %w", name, errInvalidConfig)
			}
			contentSize += header.Size
			regularCount++
			if _, err := io.CopyN(io.Discard, reader, header.Size); err != nil {
				return fmt.Errorf("read release reports file %s: %w", name, err)
			}
		default:
			return fmt.Errorf("release reports archive path %s has unsupported type %d: %w", name, header.Typeflag, errInvalidConfig)
		}
	}
	if entryCount == 0 || regularCount == 0 || !foundReportsRoot {
		return fmt.Errorf("release reports archive lacks regular report files: %w", errInvalidConfig)
	}
	var trailing [1]byte
	count, trailingErr := limited.Read(trailing[:])
	if count != 0 || !errors.Is(trailingErr, io.EOF) {
		return fmt.Errorf("release reports tar stream has trailing or malformed data: %w", errors.Join(trailingErr, errInvalidConfig))
	}
	if limited.N == 0 {
		return fmt.Errorf("release reports archive exceeds expanded size limit: %w", errInvalidConfig)
	}
	if source.Len() != 0 {
		return fmt.Errorf("release reports archive has trailing compressed data: %w", errInvalidConfig)
	}
	return nil
}
