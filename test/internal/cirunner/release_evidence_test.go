package cirunner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseEvidenceUploadsExactHashedAssets(t *testing.T) {
	t.Parallel()

	commandRoot := t.TempDir()
	artifactRoot := t.TempDir()
	reportName := "gobfd-v0.6.5-reports.tar.gz"
	report := []byte("structured release reports")
	digests := validReleaseDigestReceipt("0.6.5")
	if err := os.WriteFile(filepath.Join(artifactRoot, reportName), report, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactRoot, "release-image-digests.txt"), digests, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingSpecRunner{}
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin"}
	if err := ReleaseEvidence(context.Background(), ReleaseEvidenceOptions{
		Root: commandRoot, ArtifactRoot: artifactRoot, RefName: "v0.6.5",
		Environment: environment, Runner: runner,
	}); err != nil {
		t.Fatalf("ReleaseEvidence() error = %v", err)
	}
	checksumName := "release-evidence-checksums.txt"
	checksumPath := filepath.Join(artifactRoot, checksumName)
	checksums, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatal(err)
	}
	wantChecksums := checksumLine(report, reportName) + checksumLine(digests, "release-image-digests.txt")
	if string(checksums) != wantChecksums {
		t.Errorf("release evidence checksums = %q, want %q", checksums, wantChecksums)
	}
	info, err := os.Stat(checksumPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != 0o644 {
		t.Errorf("release evidence checksums mode = %s, want -rw-r--r--", info.Mode())
	}
	wantCall := specInvocation{
		name: "gh",
		args: []string{
			"release", "upload", "v0.6.5",
			filepath.Join(commandRoot, releaseEvidenceStageName, reportName),
			filepath.Join(commandRoot, releaseEvidenceStageName, "release-image-digests.txt"),
			filepath.Join(commandRoot, releaseEvidenceStageName, checksumName),
		},
		dir: commandRoot,
		env: environment,
	}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], wantCall) {
		t.Fatalf("release evidence upload calls = %#v, want %#v", runner.calls, wantCall)
	}
	if _, err := os.Lstat(filepath.Join(commandRoot, releaseEvidenceStageName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release evidence staging directory after upload error = %v, want not exist", err)
	}
}

func TestReleaseEvidenceUploadsOwnedSnapshotWhenSourceChanges(t *testing.T) {
	t.Parallel()

	commandRoot := t.TempDir()
	artifactRoot := t.TempDir()
	reportName := "gobfd-v0.6.5-reports.tar.gz"
	report := []byte("verified report")
	writeReleaseEvidenceFile(t, artifactRoot, reportName, report)
	writeReleaseDigestReceipt(t, artifactRoot, validReleaseDigestReceipt("0.6.5"))
	var uploadedReport []byte
	runner := &releaseEvidenceTestRunner{run: func(spec CommandSpec) error {
		var err error
		uploadedReport, err = os.ReadFile(spec.Args[3])
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(artifactRoot, reportName), []byte("changed report!"), 0o600)
	}}
	err := ReleaseEvidence(context.Background(), ReleaseEvidenceOptions{
		Root: commandRoot, ArtifactRoot: artifactRoot, RefName: "v0.6.5", Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), reportName+" changed after upload") {
		t.Fatalf("ReleaseEvidence() error = %v, want source mutation rejection", err)
	}
	if !reflect.DeepEqual(uploadedReport, report) {
		t.Errorf("uploaded report snapshot = %q, want %q", uploadedReport, report)
	}
}

func TestReleaseEvidenceRunsPostValidationAfterUploadFailure(t *testing.T) {
	t.Parallel()

	commandRoot := t.TempDir()
	artifactRoot := t.TempDir()
	reportName := "gobfd-v0.6.5-reports.tar.gz"
	writeReleaseEvidenceFile(t, artifactRoot, reportName, []byte("verified report"))
	writeReleaseDigestReceipt(t, artifactRoot, validReleaseDigestReceipt("0.6.5"))
	wantErr := errors.New("partial upload")
	runner := &releaseEvidenceTestRunner{run: func(_ CommandSpec) error {
		if err := os.WriteFile(filepath.Join(artifactRoot, reportName), []byte("changed report!"), 0o600); err != nil {
			return err
		}
		return wantErr
	}}
	err := ReleaseEvidence(context.Background(), ReleaseEvidenceOptions{
		Root: commandRoot, ArtifactRoot: artifactRoot, RefName: "v0.6.5", Runner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReleaseEvidence() error = %v, want upload failure %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), reportName+" changed after upload") {
		t.Fatalf("ReleaseEvidence() error = %v, want joined post-validation failure", err)
	}
}

func TestReleaseEvidenceCleanupLeavesReplacementDirectoryUntouched(t *testing.T) {
	t.Parallel()

	commandRoot := t.TempDir()
	artifactRoot := t.TempDir()
	reportName := "gobfd-v0.6.5-reports.tar.gz"
	writeReleaseEvidenceFile(t, artifactRoot, reportName, []byte("verified report"))
	writeReleaseDigestReceipt(t, artifactRoot, validReleaseDigestReceipt("0.6.5"))
	stagePath := filepath.Join(commandRoot, releaseEvidenceStageName)
	movedStagePath := stagePath + "-moved"
	sentinelPath := filepath.Join(stagePath, "nested", "sentinel")
	runner := &releaseEvidenceTestRunner{run: func(CommandSpec) error {
		if err := os.Rename(stagePath, movedStagePath); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(sentinelPath), 0o700); err != nil {
			return err
		}
		return os.WriteFile(sentinelPath, []byte("preserve"), 0o600)
	}}
	err := ReleaseEvidence(context.Background(), ReleaseEvidenceOptions{
		Root: commandRoot, ArtifactRoot: artifactRoot, RefName: "v0.6.5", Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), "staging directory ownership changed") {
		t.Fatalf("ReleaseEvidence() error = %v, want staging ownership rejection", err)
	}
	if data, readErr := os.ReadFile(sentinelPath); readErr != nil || string(data) != "preserve" {
		t.Fatalf("replacement sentinel = %q, error = %v; want preserved", data, readErr)
	}
	for _, name := range []string{reportName, "release-image-digests.txt", "release-evidence-checksums.txt"} {
		if _, statErr := os.Lstat(filepath.Join(movedStagePath, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("owned staged file %s cleanup error = %v, want not exist", name, statErr)
		}
	}
}

func TestReleaseEvidenceRejectsInvalidInputsWithoutUpload(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "missing report", setup: func(t *testing.T, root string) {
			t.Helper()
			writeReleaseDigestReceipt(t, root, validReleaseDigestReceipt("0.6.5"))
		}},
		{name: "empty report", setup: func(t *testing.T, root string) {
			t.Helper()
			writeReleaseEvidenceFile(t, root, "gobfd-v0.6.5-reports.tar.gz", nil)
			writeReleaseDigestReceipt(t, root, validReleaseDigestReceipt("0.6.5"))
		}},
		{name: "linked report", setup: func(t *testing.T, root string) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "report.tar.gz")
			writeReleaseEvidenceFile(t, filepath.Dir(target), filepath.Base(target), []byte("report"))
			if err := os.Symlink(target, filepath.Join(root, "gobfd-v0.6.5-reports.tar.gz")); err != nil {
				t.Fatal(err)
			}
			writeReleaseDigestReceipt(t, root, validReleaseDigestReceipt("0.6.5"))
		}},
		{name: "missing digest receipt", setup: func(t *testing.T, root string) {
			t.Helper()
			writeReleaseEvidenceFile(t, root, "gobfd-v0.6.5-reports.tar.gz", []byte("report"))
		}},
		{name: "malformed digest receipt", setup: func(t *testing.T, root string) {
			t.Helper()
			writeReleaseEvidenceFile(t, root, "gobfd-v0.6.5-reports.tar.gz", []byte("report"))
			writeReleaseDigestReceipt(t, root, []byte("not canonical\n"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			artifactRoot := t.TempDir()
			test.setup(t, artifactRoot)
			runner := &recordingSpecRunner{}
			if err := ReleaseEvidence(context.Background(), ReleaseEvidenceOptions{
				Root: t.TempDir(), ArtifactRoot: artifactRoot, RefName: "v0.6.5", Runner: runner,
			}); err == nil {
				t.Fatal("ReleaseEvidence() error = nil, want invalid input rejection")
			}
			if len(runner.calls) != 0 {
				t.Errorf("invalid release evidence upload calls = %d, want 0", len(runner.calls))
			}
		})
	}
}

func validReleaseDigestReceipt(version string) []byte {
	return []byte(
		"ghcr.io/dantte-lp/gobfd:" + version + " " + testOCIDigest("1") + "\n" +
			"ghcr.io/dantte-lp/gobfd:" + version + "-debian-trixie " + testOCIDigest("1") + "\n" +
			"ghcr.io/dantte-lp/gobfd:" + version + "-oraclelinux10 " + testOCIDigest("3") + "\n",
	)
}

func checksumLine(data []byte, name string) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x  %s\n", digest, name)
}

func writeReleaseDigestReceipt(t *testing.T, root string, data []byte) {
	t.Helper()
	writeReleaseEvidenceFile(t, root, "release-image-digests.txt", data)
}

func writeReleaseEvidenceFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type releaseEvidenceTestRunner struct {
	run func(CommandSpec) error
}

func (runner *releaseEvidenceTestRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	return runner.run(spec)
}

func TestValidateReleaseOCIDigestReceiptRejectsNoncanonicalRecords(t *testing.T) {
	t.Parallel()

	for _, data := range [][]byte{
		[]byte(strings.Replace(string(validReleaseDigestReceipt("0.6.5")), "sha256:", "SHA256:", 1)),
		[]byte(strings.Replace(string(validReleaseDigestReceipt("0.6.5")), "-debian-trixie", "-oraclelinux10", 1)),
		append(validReleaseDigestReceipt("0.6.5"), '\n'),
	} {
		if err := validateReleaseOCIDigestReceipt(data, "0.6.5"); err == nil {
			t.Errorf("validateReleaseOCIDigestReceipt(%q) error = nil", data)
		}
	}
	if err := validateReleaseOCIDigestReceipt(validReleaseDigestReceipt("0.6.5"), "0.6.5"); err != nil {
		t.Fatalf("validateReleaseOCIDigestReceipt(valid) error = %v", err)
	}
}

func TestReleaseEvidencePreservesExistingChecksumOnFailure(t *testing.T) {
	t.Parallel()

	artifactRoot := t.TempDir()
	checksumPath := filepath.Join(artifactRoot, "release-evidence-checksums.txt")
	if err := os.WriteFile(checksumPath, []byte("previous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingSpecRunner{}
	if err := ReleaseEvidence(context.Background(), ReleaseEvidenceOptions{
		Root: t.TempDir(), ArtifactRoot: artifactRoot, RefName: "v0.6.5", Runner: runner,
	}); err == nil {
		t.Fatal("ReleaseEvidence() error = nil, want missing input failure")
	}
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "previous\n" {
		t.Errorf("existing checksum after failure = %q", data)
	}
}
