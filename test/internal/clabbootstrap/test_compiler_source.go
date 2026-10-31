package clabbootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
	"github.com/dantte-lp/gobfd/test/internal/vendorprofiles"
)

const (
	testCompilerImage = toolprofile.CompilerImage
	testRuntimeImage  = "docker.io/library/debian:trixie-slim@sha256:" +
		"62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad"
	testPackagePath         = "github.com/dantte-lp/gobfd/test/interop-clab"
	maxCompilerSourceSize   = 2 << 20
	maxCompiledArtifactSize = 128 << 20
)

func prepareTestRun(projectRoot string) (testRunReceipt, error) {
	if err := ensureRuntimeDirectory(projectRoot); err != nil {
		return testRunReceipt{}, err
	}
	if _, err := os.Lstat(testRunPath(projectRoot)); !errors.Is(err, os.ErrNotExist) {
		return testRunReceipt{}, fmt.Errorf("previous test run needs --down recovery: %w", errLifecycleState)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return testRunReceipt{}, fmt.Errorf("create compiler run identity: %w", err)
	}
	receipt := testRunReceipt{
		SchemaVersion: 1, RunID: hex.EncodeToString(nonce[:]), Containers: make(map[string]string),
	}
	for _, name := range testRunNames(receipt.RunID) {
		receipt.Containers[name] = ""
	}
	directory := testRunDirectory(projectRoot, receipt.RunID)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return testRunReceipt{}, fmt.Errorf("create private compiler snapshot: %w", err)
	}
	digest, err := snapshotTestSources(projectRoot, directory)
	if err != nil {
		return testRunReceipt{}, errors.Join(err, os.RemoveAll(directory))
	}
	receipt.SourceSHA = digest
	if err := createPrivateReceipt(testRunPath(projectRoot), receipt); err != nil {
		return testRunReceipt{}, errors.Join(err, os.RemoveAll(directory))
	}
	return receipt, nil
}

func snapshotTestSources(projectRoot, directory string) (string, error) {
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return "", fmt.Errorf("open compiler source root: %w", err)
	}
	paths, listErr := compilerSourcePaths(root)
	if listErr != nil {
		return "", errors.Join(listErr, root.Close())
	}
	for _, path := range paths {
		data, readErr := readCompilerSource(root, path)
		if readErr != nil {
			return "", errors.Join(readErr, root.Close())
		}
		destination := filepath.Join(directory, "src", path)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return "", errors.Join(fmt.Errorf("create compiler source directory: %w", err), root.Close())
		}
		if err := os.WriteFile(destination, data, 0o400); err != nil {
			return "", errors.Join(fmt.Errorf("snapshot compiler input %s: %w", path, err), root.Close())
		}
	}
	if err := root.Close(); err != nil {
		return "", fmt.Errorf("close compiler source root: %w", err)
	}
	if err := os.Mkdir(filepath.Join(directory, "out"), 0o700); err != nil {
		return "", fmt.Errorf("create compiler output directory: %w", err)
	}
	return testSourceSnapshotDigest(filepath.Join(directory, "src"))
}

// verifyTestSourceSnapshot binds the frozen compiler inputs, not a later host checkout.
func verifyTestSourceSnapshot(projectRoot string, receipt testRunReceipt) error {
	if !validTestRun(receipt) {
		return fmt.Errorf("validate compiler snapshot receipt: %w", errLifecycleState)
	}
	sourceRoot := filepath.Join(testRunDirectory(projectRoot, receipt.RunID), "src")
	digest, err := testSourceSnapshotDigest(sourceRoot)
	if err != nil {
		return err
	}
	if digest != receipt.SourceSHA {
		return fmt.Errorf("compiler source snapshot changed: %w", errLifecycleState)
	}
	return nil
}

func testSourceSnapshotDigest(sourceRoot string) (string, error) {
	root, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("open frozen compiler source root: %w", err)
	}
	records, recordsErr := compilerSnapshotRecords(root, sourceRoot)
	if err := errors.Join(recordsErr, root.Close()); err != nil {
		return "", err
	}
	if _, err := vendorprofiles.Load(sourceRoot); err != nil {
		return "", fmt.Errorf("validate frozen vendor execution policy: %w", errors.Join(errLifecycleState, err))
	}
	digest := sha256.Sum256([]byte(strings.Join(records, "\x00")))
	return hex.EncodeToString(digest[:]), nil
}

func compilerSnapshotRecords(root *os.Root, sourceRoot string) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open private compiler source directory: %w", err)
	}
	validationErr := validateReceiptDirectory(directory, sourceRoot)
	if closeErr := errors.Join(validationErr, directory.Close()); closeErr != nil {
		return nil, closeErr
	}
	paths, err := compilerSourcePaths(root)
	if err != nil {
		return nil, err
	}
	if err := validateCompilerSnapshotFiles(root, paths); err != nil {
		return nil, err
	}
	records := []string{
		testCompilerImage, testRuntimeImage, runtime.GOARCH, "GOOS=linux",
		"CGO_ENABLED=1", strings.Join(suiteCompileArguments(), "\x00"),
		"CGO_ENABLED=0", strings.Join(converterCompileArguments(), "\x00"),
	}
	for _, path := range paths {
		data, err := readCompilerSource(root, path)
		if err != nil {
			return nil, err
		}
		records = append(records, path, fmt.Sprintf("%x", sha256.Sum256(data)))
	}
	return records, nil
}

func validateCompilerSnapshotFiles(root *os.Root, paths []string) error {
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk frozen compiler inputs: %w", walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		_, allowed := slices.BinarySearch(paths, path)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect frozen compiler input %s: %w", path, err)
		}
		if !allowed || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
			return fmt.Errorf("unexpected or writable frozen compiler input %s: %w", path, errLifecycleState)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("validate frozen compiler source tree: %w", err)
	}
	return nil
}

func compilerSourcePaths(root *os.Root) ([]string, error) {
	paths := []string{"go.mod", "go.sum", "test/e2e/vendor/profiles.json"}
	for _, directory := range []string{"test/interop-clab", "test/internal/podmanapi", "test/internal/vendorprofiles"} {
		file, err := root.Open(directory)
		if err != nil {
			return nil, fmt.Errorf("open compiler input directory %s: %w", directory, err)
		}
		entries, readErr := file.ReadDir(-1)
		if err := errors.Join(readErr, file.Close()); err != nil {
			return nil, fmt.Errorf("read compiler input directory %s: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				(directory != "test/interop-clab" && strings.HasSuffix(entry.Name(), "_test.go")) {
				continue
			}
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	slices.Sort(paths)
	return paths, nil
}

func readCompilerSource(root *os.Root, path string) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open compiler input %s: %w", path, err)
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxCompilerSourceSize {
		return nil, errors.Join(
			fmt.Errorf("validate compiler input %s: %w", path, errLifecycleState), statErr, file.Close(),
		)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxCompilerSourceSize+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, fmt.Errorf("read compiler input %s: %w", path, err)
	}
	if len(data) > maxCompilerSourceSize {
		return nil, fmt.Errorf("compiler input %s exceeds limit: %w", path, errLifecycleState)
	}
	return data, nil
}

func verifyCompiledArtifact(path, wantPath string, race bool) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCompiledArtifactSize {
		return "", fmt.Errorf("compiled artifact is not a bounded regular file: %w",
			errors.Join(errLifecycleState, err))
	}
	if identityErr := verifyCompiledArtifactIdentity(path, wantPath, race); identityErr != nil {
		return "", identityErr
	}
	// #nosec G304 -- A bounded regular artifact and its package/toolchain/race identity are checked before hashing.
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open compiled artifact: %w", err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, io.LimitReader(file, maxCompiledArtifactSize+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", fmt.Errorf("hash compiled artifact: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func verifyCompiledArtifactIdentity(path, wantPath string, race bool) error {
	build, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read compiled artifact identity: %w", err)
	}
	version, _ := strings.CutPrefix(testCompilerImage, "docker.io/library/golang:")
	version, _, _ = strings.Cut(version, "-trixie@")
	settings := make(map[string]string, len(build.Settings))
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	if build.Path != wantPath || build.GoVersion != "go"+version || settings["GOOS"] != "linux" ||
		settings["GOARCH"] != runtime.GOARCH ||
		(race && (settings["-race"] != "true" || settings["CGO_ENABLED"] != "1")) ||
		(!race && (settings["-race"] == "true" || settings["CGO_ENABLED"] != "0")) {
		return fmt.Errorf("compiled artifact does not match pinned package/toolchain/race contract: %w",
			errLifecycleState)
	}
	return nil
}
