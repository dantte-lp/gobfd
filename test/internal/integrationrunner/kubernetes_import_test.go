package integrationrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func TestKubernetesImageIDOutputRejectsCopyOverflow(t *testing.T) {
	t.Parallel()
	output := kubernetesOutput{maxBytes: maxImageIDOutput}
	reader := struct{ io.Reader }{strings.NewReader(strings.Repeat("a", 1024))}
	n, err := io.Copy(&output, reader)
	if !errors.Is(err, errKubernetesOutputLimit) || n != 0 || output.String() != "" {
		t.Fatalf("unbounded image identity copy: bytes=%d stored=%d error=%v", n, len(output.String()), err)
	}
}

func TestKubernetesImporterRequiresExpectedImageID(t *testing.T) {
	t.Parallel()
	r := runner{}
	if _, err := r.importKubernetesImage(
		t.Context(), nil, "fixture:owned", "", "",
	); !errors.Is(err, errKubernetesImageIdentity) {
		t.Fatalf("missing expected image identity accepted: %v", err)
	}
}

func TestKubernetesImportArchiveLifecycle(t *testing.T) {
	expectedArchive, expectedID, fixtureErr := kubernetesArchiveTestBytes("fixture:owned", nil)
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	modes := []string{
		"healthy",
		"save failure",
		"import failure",
		"replaced archive",
		"cleanup failure",
		"import and cleanup failure",
		"tag replacement",
		"prefixed image ID",
		"inspect failure",
		"invalid image ID",
		"multiple image IDs",
		"oversized image ID",
		"replaced before import",
		"symlink before import",
		"replaced while importing",
		"foreign config export",
		"foreign layer export",
		"foreign tag export",
		"multiple exported images",
		"receipt collision",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			runImportArchiveLifecycleMode(t, mode, expectedArchive, expectedID)
		})
	}
}

func runImportArchiveLifecycleMode(t *testing.T, mode string, expectedArchive []byte, expectedID string) {
	t.Helper()
	root, bin, scratch := t.TempDir(), t.TempDir(), t.TempDir()
	receipts := filepath.Join(root, "receipts")
	if mkdirErr := os.Mkdir(receipts, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if mode == "receipt collision" {
		archiveReceipt := filepath.Join(receipts, "archive-"+strings.TrimPrefix(expectedID, "sha256:")+".json")
		if writeErr := os.WriteFile(archiveReceipt, []byte("foreign evidence"), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	executable, execErr := os.Executable()
	if execErr != nil {
		t.Fatal(execErr)
	}
	if symErr := os.Symlink(executable, filepath.Join(bin, "podman")); symErr != nil {
		t.Fatal(symErr)
	}
	for key, value := range map[string]string{
		"PATH":                     bin,
		"TMPDIR":                   scratch,
		"GOBFD_K8S_IMPORT_FIXTURE": root,
		"GOBFD_K8S_IMPORT_MODE":    mode,
		"GORACE":                   "atexit_sleep_ms=0",
	} {
		t.Setenv(key, value)
	}
	socket := filepath.Join(t.TempDir(), "containerd.sock")
	containerdFixture := newImportLifecycleContainerdFixture(root, receipts, mode, expectedID, expectedArchive)
	startKubernetesContainerdFixture(t, socket, containerdFixture)
	store, storeErr := openKubernetesContainerdStore(t.Context(), "unix://"+socket)
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	t.Cleanup(func() {
		if closeErr := store.close(); closeErr != nil {
			t.Errorf("close containerd store: %v", closeErr)
		}
	})
	r := runner{root: root, stdout: io.Discard, stderr: io.Discard}
	manifestDigest, importErr := r.importKubernetesImage(t.Context(), store, "fixture:owned", expectedID, receipts)
	verifyImportLifecycleOutcome(
		t, mode, root, receipts, scratch, expectedID, manifestDigest, importErr, containerdFixture,
	)
}

func newImportLifecycleContainerdFixture(
	root, receipts, mode, expectedID string,
	expectedArchive []byte,
) *kubernetesContainerdFixture {
	fixture := newKubernetesContainerdFixture()
	fixture.onImageCreate = func(_ string) error {
		if evErr := checkKubernetesArchiveTestEvidence(receipts, "fixture:owned", expectedID, expectedArchive); evErr != nil {
			return status.Error(codes.Internal, evErr.Error())
		}
		rawPath, readErr := os.ReadFile(filepath.Join(root, "archive-path"))
		if readErr != nil {
			return status.Error(codes.Internal, readErr.Error())
		}
		archivePath := string(rawPath)
		if mode == "replaced while importing" {
			if err := replaceArchiveOnDisk(archivePath, false); err != nil {
				return status.Error(codes.Internal, err.Error())
			}
		}
		if mode == "import failure" {
			return status.Error(codes.Unavailable, "injected containerd import failure")
		}
		if mode == "replaced archive" || mode == "cleanup failure" || mode == "import and cleanup failure" {
			if err := replaceArchiveOnDisk(archivePath, mode == "cleanup failure"); err != nil {
				return status.Error(codes.Internal, err.Error())
			}
		}
		if mode == "import and cleanup failure" {
			return status.Error(codes.Unavailable, "injected containerd import and cleanup failure")
		}
		return nil
	}
	return fixture
}

func replaceArchiveOnDisk(archivePath string, directoryReplacement bool) error {
	if err := os.Rename(archivePath, archivePath+".owned"); err != nil {
		return fmt.Errorf("rename owned archive: %w", err)
	}
	target := archivePath
	if directoryReplacement {
		if err := os.Mkdir(archivePath, 0o700); err != nil {
			return fmt.Errorf("mkdir replacement archive dir: %w", err)
		}
		target = filepath.Join(archivePath, "replacement")
	}
	if err := os.WriteFile(target, []byte("foreign replacement"), 0o600); err != nil {
		return fmt.Errorf("write replacement archive: %w", err)
	}
	return nil
}

func verifyImportLifecycleOutcome(
	t *testing.T,
	mode, root, receipts, scratch, expectedID, manifestDigest string,
	err error,
	fixture *kubernetesContainerdFixture,
) {
	t.Helper()
	wantSuccess := mode == "healthy" || mode == "tag replacement" || mode == "prefixed image ID"
	if (err != nil) != !wantSuccess {
		t.Fatalf("mode=%s error=%v", mode, err)
	}
	if verifyEarlyImportRejection(t, mode, root, receipts, expectedID, err, fixture) {
		return
	}
	assertImportFailureClassification(t, mode, err)
	assertArchiveScratchCustody(t, mode, root, scratch)
	assertContainerdPublicationState(t, mode, receipts, expectedID, manifestDigest, fixture)
}

func verifyEarlyImportRejection(
	t *testing.T,
	mode, root, receipts, expectedID string,
	err error,
	fixture *kubernetesContainerdFixture,
) bool {
	t.Helper()
	if mode == "receipt collision" {
		if !errors.Is(err, os.ErrExist) {
			t.Fatalf("receipt collision lost: %v", err)
		}
		assertNoPublishedContainerdImage(t, fixture)
		receiptPath := filepath.Join(receipts, "archive-"+strings.TrimPrefix(expectedID, "sha256:")+".json")
		data, readErr := os.ReadFile(receiptPath)
		if readErr != nil || string(data) != "foreign evidence" {
			t.Fatal("foreign receipt replaced")
		}
		return false
	}
	if mode == "foreign config export" || mode == "foreign layer export" ||
		mode == "foreign tag export" || mode == "multiple exported images" {
		if !errors.Is(err, errKubernetesArchiveContent) {
			t.Fatalf("archive content mismatch lost: %v", err)
		}
		assertNoPublishedContainerdImage(t, fixture)
		return false
	}
	if mode == "invalid image ID" || mode == "multiple image IDs" ||
		mode == "oversized image ID" || mode == "inspect failure" {
		if mode == "inspect failure" {
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 17 {
				t.Fatalf("original inspect exit lost: %v", err)
			}
		} else if !errors.Is(err, errKubernetesImageIdentity) {
			t.Fatalf("image identity failure lost: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(root, "archive-path")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("invalid image identity allowed archive export")
		}
		assertNoPublishedContainerdImage(t, fixture)
		return true
	}
	return false
}

func assertImportFailureClassification(t *testing.T, mode string, err error) {
	t.Helper()
	if mode == "save failure" {
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 17 {
			t.Fatalf("original save exit lost: %v", err)
		}
	}
	if mode == "import failure" || mode == "import and cleanup failure" {
		if status.Code(err) != codes.Unavailable && !strings.Contains(err.Error(), "Unavailable") {
			t.Fatalf("containerd failure status lost: %v", err)
		}
	}
	replacedModes := mode == "replaced archive" || mode == "cleanup failure" ||
		mode == "import and cleanup failure" || mode == "replaced before import" ||
		mode == "symlink before import" || mode == "replaced while importing"
	if replacedModes && !errors.Is(err, errKubernetesArchiveIdentity) {
		t.Fatalf("archive identity failure lost: %v", err)
	}
}

func assertArchiveScratchCustody(t *testing.T, mode, root, scratch string) {
	t.Helper()
	rawPath, readErr := os.ReadFile(filepath.Join(root, "archive-path"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if filepath.Dir(string(rawPath)) != scratch {
		t.Fatalf("archive escaped selected scratch directory: %s", rawPath)
	}
	_, statErr := os.Lstat(string(rawPath))
	replaced := mode == "replaced archive" || mode == "cleanup failure" ||
		mode == "import and cleanup failure" || mode == "replaced before import" ||
		mode == "symlink before import" || mode == "replaced while importing"
	if replaced {
		if statErr != nil {
			t.Fatalf("replacement archive was removed: %v", statErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("owned archive remains: %v", statErr)
	}
}

func assertContainerdPublicationState(
	t *testing.T,
	mode, receipts, expectedID, manifestDigest string,
	fixture *kubernetesContainerdFixture,
) {
	t.Helper()
	if mode == "save failure" || mode == "replaced before import" || mode == "symlink before import" {
		assertNoPublishedContainerdImage(t, fixture)
		return
	}
	if mode == "healthy" || mode == "tag replacement" ||
		mode == "prefixed image ID" || mode == "replaced while importing" {
		fixture.mu.Lock()
		published := fixture.images["fixture:owned"]
		configBlob := fixture.blobs[expectedID]
		fixture.mu.Unlock()
		if published == nil || configBlob == nil {
			t.Fatalf("expected published containerd image and verified config %s", expectedID)
		}
		if mode != "replaced while importing" {
			checkImportReceipt(t, receipts, expectedID, manifestDigest)
		}
	}
}

func assertNoPublishedContainerdImage(t *testing.T, fixture *kubernetesContainerdFixture) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.images) != 0 || len(fixture.blobs) != 0 {
		t.Fatalf("unexpected containerd writes: images=%d blobs=%d", len(fixture.images), len(fixture.blobs))
	}
}

func checkImportReceipt(t *testing.T, receipts, expectedID, manifestDigest string) {
	t.Helper()
	path := filepath.Join(receipts, "import-"+strings.TrimPrefix(expectedID, "sha256:")+".json")
	data, _, err := interopproject.ReadPrivateReceipt(path)
	if err != nil {
		t.Fatalf("read import receipt: %v", err)
	}
	var receipt kubernetesImportReceipt
	if unmarshalErr := json.Unmarshal(data, &receipt); unmarshalErr != nil {
		t.Fatalf("decode import receipt: %v", unmarshalErr)
	}
	if receipt.ManifestDigest != manifestDigest ||
		receipt.SourceID != expectedID ||
		receipt.ServerUUID != "containerd-server-uuid" {
		t.Fatalf("unexpected import receipt: %+v", receipt)
	}
}

func runKubernetesImportFixture() int {
	root, mode := os.Getenv("GOBFD_K8S_IMPORT_FIXTURE"), os.Getenv("GOBFD_K8S_IMPORT_MODE")
	args := os.Args[1:]
	expectedArchive, expectedID, fixtureErr := kubernetesArchiveTestBytes("fixture:owned", nil)
	if fixtureErr != nil || filepath.Base(os.Args[0]) != "podman" {
		return 125
	}
	if len(args) == 6 &&
		strings.Join(args[:5], " ") == "image inspect --format {{.ID}} --" &&
		args[5] == "fixture:owned" {
		return runPodmanInspectFixture(mode, expectedID)
	}
	return runPodmanSaveFixture(root, mode, args, expectedID, expectedArchive)
}

func runPodmanInspectFixture(mode, expectedID string) int {
	switch mode {
	case "inspect failure":
		return 17
	case "prefixed image ID":
		fmt.Fprintln(os.Stdout, expectedID)
	case "invalid image ID":
		fmt.Fprintln(os.Stdout, "invalid")
	case "oversized image ID":
		fmt.Fprintln(os.Stdout, strings.Repeat("a", 1024))
	case "multiple image IDs":
		fmt.Fprintln(os.Stdout, strings.Repeat("a", 64)+"\n"+strings.Repeat("b", 64))
	default:
		fmt.Fprintln(os.Stdout, strings.TrimPrefix(expectedID, "sha256:"))
	}
	return 0
}

func runPodmanSaveFixture(root, mode string, args []string, expectedID string, expectedArchive []byte) int {
	legacy := len(args) == 4 && args[0] == "save" && args[1] == "--output" && args[3] == "fixture:owned"
	owned := len(args) == 8 &&
		strings.Join(args[:5], " ") == "save --format docker-archive --multi-image-archive=false --output" &&
		args[6] == expectedID &&
		args[7] == "fixture:owned"
	if !legacy && !owned {
		return 125
	}
	archive := args[2]
	if owned {
		archive = args[5]
	}
	if err := os.WriteFile(filepath.Join(root, "archive-path"), []byte(archive), 0o600); err != nil {
		return 125
	}
	if mode == "save failure" {
		return 17
	}
	payload, payloadErr := mutateSaveFixturePayload(mode, legacy, expectedArchive)
	if payloadErr != nil || os.WriteFile(archive, payload, 0o600) != nil {
		return 125
	}
	if mode == "replaced before import" || mode == "symlink before import" {
		if os.Rename(archive, archive+".owned") != nil {
			return 125
		}
		if mode == "symlink before import" {
			if os.Symlink(archive+".owned", archive) != nil {
				return 125
			}
		} else if os.WriteFile(archive, []byte("foreign replacement"), 0o600) != nil {
			return 125
		}
	}
	return 0
}

func mutateSaveFixturePayload(mode string, legacy bool, expectedArchive []byte) ([]byte, error) {
	if legacy && mode == "tag replacement" {
		return []byte("foreign export"), nil
	}
	if mode != "foreign config export" && mode != "foreign layer export" &&
		mode != "foreign tag export" && mode != "multiple exported images" {
		return expectedArchive, nil
	}
	members, _, err := kubernetesArchiveTestMembers("fixture:owned", nil)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "foreign config export":
		members[1].data = append(members[1].data, ' ')
	case "foreign layer export":
		members[0].data[0] ^= 1
	case "foreign tag export":
		members[5].data = bytes.ReplaceAll(members[5].data, []byte("fixture:owned"), []byte("foreign:owned"))
	case "multiple exported images":
		raw := members[5].data
		dup := append([]byte{'['}, raw[1:len(raw)-1]...)
		dup = append(dup, ',')
		dup = append(dup, raw[1:]...)
		members[5].data = dup
	}
	return encodeKubernetesArchiveTestMembers(members)
}
