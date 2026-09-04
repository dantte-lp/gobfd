package cirunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateReleaseAssetContents(t *testing.T) {
	t.Parallel()

	downloadRoot, artifactRoot, runnerTempRoot := openReleaseAssetContentFixture(t)
	if err := validateReleaseAssetContents(
		downloadRoot, artifactRoot, runnerTempRoot, "0.6.2", "v0.6.2",
	); err != nil {
		t.Fatalf("validateReleaseAssetContents() error = %v", err)
	}
}

func TestValidateReleaseAssetContentsRejectsInvalidEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, string, string)
		want   string
	}{
		{name: "main checksum mismatch", mutate: func(t *testing.T, download, _, _ string) {
			t.Helper()
			path := filepath.Join(download, "checksums.txt")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[0] = changedHexByte(data[0])
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "SHA-256 mismatch"},
		{name: "duplicate checksum name", mutate: func(t *testing.T, download, _, _ string) {
			t.Helper()
			path := filepath.Join(download, "checksums.txt")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(data, []byte{'\n'})
			lines[len(lines)-2] = append([]byte(nil), lines[0]...)
			if err := os.WriteFile(path, bytes.Join(lines, []byte{'\n'}), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "duplicate"},
		{name: "supplemental receipt differs from local", mutate: func(t *testing.T, download, _, _ string) {
			t.Helper()
			path := filepath.Join(download, "release-evidence-checksums.txt")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[0] = changedHexByte(data[0])
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "differs from local"},
		{name: "SBOM duplicate field", mutate: func(t *testing.T, download, _, _ string) {
			t.Helper()
			name := expectedChecksummedArtifactNames("0.6.2")[3]
			data := `{"bomFormat":"CycloneDX","bomFormat":"CycloneDX","specVersion":"1.6","metadata":{"component":{}},"components":[{"name":"gobfd"}]}`
			if err := os.WriteFile(filepath.Join(download, name), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			rewriteMainChecksums(t, download)
		}, want: "duplicate JSON"},
		{name: "SBOM trailing data", mutate: func(t *testing.T, download, _, _ string) {
			t.Helper()
			name := expectedChecksummedArtifactNames("0.6.2")[3]
			path := filepath.Join(download, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, []byte("{}")...), 0o600); err != nil {
				t.Fatal(err)
			}
			rewriteMainChecksums(t, download)
		}, want: "trailing JSON"},
		{name: "SBOM empty components", mutate: func(t *testing.T, download, _, _ string) {
			t.Helper()
			name := expectedChecksummedArtifactNames("0.6.2")[3]
			data := `{"bomFormat":"CycloneDX","specVersion":"1.6","metadata":{"component":{}},"components":[]}`
			if err := os.WriteFile(filepath.Join(download, name), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			rewriteMainChecksums(t, download)
		}, want: "components"},
		{name: "report archive link", mutate: func(t *testing.T, download, artifact, _ string) {
			t.Helper()
			archive := releaseReportsArchiveWithHeaders(t, []tar.Header{
				{Name: "reports/", Typeflag: tar.TypeDir, Mode: 0o755},
				{Name: "reports/link", Typeflag: tar.TypeSymlink, Linkname: "target", Mode: 0o777},
			})
			rewriteSupplementalEvidence(t, download, archive)
			syncSupplementalReceipt(t, download, artifact)
		}, want: "unsupported type"},
		{name: "malformed report archive", mutate: func(t *testing.T, download, artifact, _ string) {
			t.Helper()
			rewriteSupplementalEvidence(t, download, []byte("not a gzip stream"))
			syncSupplementalReceipt(t, download, artifact)
		}, want: "gzip stream"},
		{name: "report archive duplicate", mutate: func(t *testing.T, download, artifact, _ string) {
			t.Helper()
			archive := releaseReportsArchiveWithHeaders(t, []tar.Header{
				{Name: "reports/", Typeflag: tar.TypeDir, Mode: 0o755},
				{Name: "reports/report.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
				{Name: "reports/report.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
			})
			rewriteSupplementalEvidence(t, download, archive)
			syncSupplementalReceipt(t, download, artifact)
		}, want: "duplicate"},
		{name: "reports root is a regular file", mutate: func(t *testing.T, download, artifact, _ string) {
			t.Helper()
			archive := releaseReportsArchiveWithHeaders(t, []tar.Header{
				{Name: "reports", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
			})
			rewriteSupplementalEvidence(t, download, archive)
			syncSupplementalReceipt(t, download, artifact)
		}, want: "not a reports descendant"},
		{name: "report archive trailing gzip member", mutate: func(t *testing.T, download, artifact, _ string) {
			t.Helper()
			archive := append(validReleaseReportsArchive(t), validReleaseReportsArchive(t)...)
			rewriteSupplementalEvidence(t, download, archive)
			syncSupplementalReceipt(t, download, artifact)
		}, want: "trailing compressed data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			download := t.TempDir()
			artifact := t.TempDir()
			runnerTemp := t.TempDir()
			writeReleaseAssetContentFixture(t, download, artifact, runnerTemp)
			test.mutate(t, download, artifact, runnerTemp)
			downloadRoot := openTestRoot(t, download)
			artifactRoot := openTestRoot(t, artifact)
			runnerTempRoot := openTestRoot(t, runnerTemp)
			err := validateReleaseAssetContents(
				downloadRoot, artifactRoot, runnerTempRoot, "0.6.2", "v0.6.2",
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReleaseAssetContents() error = %v, want %q", err, test.want)
			}
		})
	}
}

func openReleaseAssetContentFixture(t *testing.T) (*os.Root, *os.Root, *os.Root) {
	t.Helper()
	download := t.TempDir()
	artifact := t.TempDir()
	runnerTemp := t.TempDir()
	writeReleaseAssetContentFixture(t, download, artifact, runnerTemp)
	return openTestRoot(t, download), openTestRoot(t, artifact), openTestRoot(t, runnerTemp)
}

func openTestRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close test root: %v", err)
		}
	})
	return root
}

func writeReleaseAssetContentFixture(t *testing.T, download, artifact, runnerTemp string) {
	t.Helper()
	writeValidDownloadedReleaseAssets(t, download)
	report := validReleaseReportsArchive(t)
	digests := validReleaseDigestReceipt("0.6.2")
	checksums := append(
		formatReleaseSHA256Line(report, "gobfd-v0.6.2-reports.tar.gz"),
		formatReleaseSHA256Line(digests, "release-image-digests.txt")...,
	)
	writeReleaseVerifyFile(t, artifact, "release-image-digests.txt", digests)
	writeReleaseVerifyFile(t, artifact, "release-evidence-checksums.txt", checksums)
	writeReleaseVerifyFile(
		t, runnerTemp, "expected-checksummed-assets.txt",
		renderArtifactNames(expectedChecksummedArtifactNames("0.6.2")),
	)
}

func writeValidDownloadedReleaseAssets(t *testing.T, directory string) {
	t.Helper()
	for name, data := range validReleaseAssetData(t) {
		writeReleaseVerifyFile(t, directory, name, data)
	}
}

func validReleaseAssetData(t *testing.T) map[string][]byte {
	t.Helper()
	assets := make(map[string][]byte)
	names := expectedChecksummedArtifactNames("0.6.2")
	mainChecksums := make([]byte, 0, releaseSHA256LinesCapacity(names))
	for _, name := range names {
		data := []byte("asset:" + name)
		if strings.HasSuffix(name, ".sbom.json") {
			data = []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","metadata":{"component":{"name":"gobfd"}},"components":[{"name":"gobfd"}]}`)
		}
		assets[name] = data
		mainChecksums = append(mainChecksums, formatReleaseSHA256Line(data, name)...)
	}
	report := validReleaseReportsArchive(t)
	digests := validReleaseDigestReceipt("0.6.2")
	assets["checksums.txt"] = mainChecksums
	assets["gobfd-v0.6.2-reports.tar.gz"] = report
	assets["release-image-digests.txt"] = digests
	assets["release-evidence-checksums.txt"] = append(
		formatReleaseSHA256Line(report, "gobfd-v0.6.2-reports.tar.gz"),
		formatReleaseSHA256Line(digests, "release-image-digests.txt")...,
	)
	return assets
}

func rewriteMainChecksums(t *testing.T, directory string) {
	t.Helper()
	names := expectedChecksummedArtifactNames("0.6.2")
	checksums := make([]byte, 0, releaseSHA256LinesCapacity(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		checksums = append(checksums, formatReleaseSHA256Line(data, name)...)
	}
	writeReleaseVerifyFile(t, directory, "checksums.txt", checksums)
}

func releaseSHA256LinesCapacity(names []string) int {
	capacity := len(names) * (hex.EncodedLen(sha256.Size) + len("  \n"))
	for _, name := range names {
		capacity += len(name)
	}
	return capacity
}

func rewriteSupplementalEvidence(t *testing.T, directory string, report []byte) {
	t.Helper()
	digests := validReleaseDigestReceipt("0.6.2")
	reportName := "gobfd-v0.6.2-reports.tar.gz"
	checksums := append(
		formatReleaseSHA256Line(report, reportName),
		formatReleaseSHA256Line(digests, "release-image-digests.txt")...,
	)
	writeReleaseVerifyFile(t, directory, reportName, report)
	writeReleaseVerifyFile(t, directory, "release-image-digests.txt", digests)
	writeReleaseVerifyFile(t, directory, "release-evidence-checksums.txt", checksums)
}

func syncSupplementalReceipt(t *testing.T, download, artifact string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(download, "release-evidence-checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseVerifyFile(t, artifact, "release-evidence-checksums.txt", data)
}

func changedHexByte(value byte) byte {
	if value == '0' {
		return '1'
	}
	return '0'
}

func validReleaseReportsArchive(t *testing.T) []byte {
	t.Helper()
	return releaseReportsArchiveWithHeaders(t, []tar.Header{
		{Name: "reports/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "reports/tests/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "reports/tests/unit.xml", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
	})
}

func releaseReportsArchiveWithHeaders(t *testing.T, headers []tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressor := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressor)
	for index := range headers {
		header := headers[index]
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size > 0 {
			if _, err := writer.Write(bytes.Repeat([]byte{'x'}, int(header.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(writer.Close(), compressor.Close()); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
