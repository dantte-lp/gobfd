package cirunner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	upxDirectoryMode  = 0o755
	githubPathLimit   = 1 << 20
	upxVersionLimit   = 64 << 10
	upxRepository     = "upx/upx"
	upxRootNamePrefix = "gobfd-upx-"
)

// ReleaseUPXOptions supplies the runner-owned paths needed to install the pinned UPX prerequisite.
type ReleaseUPXOptions struct {
	RunnerTemp  string
	GitHubPath  string
	Environment []string
	Runner      SpecRunner
	asset       *upxAssetContract
}

type upxAssetContract struct {
	version       string
	archiveName   string
	archiveSize   int64
	archiveSHA256 string
	tarSize       int64
	entries       []upxTarEntry
}

type upxTarEntry struct {
	name string
	size int64
	mode int64
	kind byte
}

type upxRelease struct {
	runnerRoot      *os.Root
	root            *os.Root
	runnerTemp      string
	rootName        string
	createdInfo     os.FileInfo
	created         bool
	published       bool
	openedOwnedRoot bool
}

func defaultUPXAssetContract() upxAssetContract {
	return upxAssetContract{
		version:       "4.2.2",
		archiveName:   "upx-4.2.2-amd64_linux.tar.xz",
		archiveSize:   590172,
		archiveSHA256: "915c8e844f835de03b9cc311ff185aedec79d757aee9d7133a528b9e89c463bb",
		tarSize:       747520,
		entries: []upxTarEntry{
			{name: "upx-4.2.2-amd64_linux/", mode: 0o755, kind: tar.TypeDir},
			{name: "upx-4.2.2-amd64_linux/COPYING", size: 18092, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/LICENSE", size: 5448, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/NEWS", size: 24953, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/README", size: 3728, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/THANKS.txt", size: 2230, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/upx", size: 562176, mode: 0o755, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/upx-doc.html", size: 38689, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/upx-doc.txt", size: 37296, mode: 0o644, kind: tar.TypeReg},
			{name: "upx-4.2.2-amd64_linux/upx.1", size: 43267, mode: 0o644, kind: tar.TypeReg},
		},
	}
}

// ReleaseUPX downloads, verifies, and installs the immutable UPX release prerequisite.
func ReleaseUPX(ctx context.Context, options ReleaseUPXOptions) (returnErr error) {
	if options.Runner == nil {
		return fmt.Errorf("UPX command runner is required: %w", errInvalidConfig)
	}
	runnerTemp, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	if hasControl(runnerTemp) {
		return fmt.Errorf("RUNNER_TEMP contains control characters: %w", errInvalidConfig)
	}
	if pathErr := validateGitHubPath(options.GitHubPath); pathErr != nil {
		return pathErr
	}
	asset := defaultUPXAssetContract()
	if options.asset != nil {
		asset = *options.asset
	}
	if contractErr := validateUPXAssetContract(asset); contractErr != nil {
		return contractErr
	}
	runnerRoot, err := os.OpenRoot(runnerTemp)
	if err != nil {
		return fmt.Errorf("open RUNNER_TEMP for UPX prerequisite: %w", err)
	}
	release := &upxRelease{
		runnerRoot: runnerRoot,
		runnerTemp: runnerTemp,
		rootName:   upxRootNamePrefix + asset.version,
	}
	defer func() {
		returnErr = release.close(returnErr)
	}()
	if err := release.prepare(); err != nil {
		return err
	}
	return release.install(ctx, options, asset)
}

func (release *upxRelease) close(returnErr error) error {
	if returnErr != nil && !release.published && release.openedOwnedRoot {
		returnErr = errors.Join(
			returnErr,
			wrapOptional("clear partial UPX prerequisite", clearOwnedUPXRoot(release.root)),
		)
	}
	if release.root != nil {
		returnErr = errors.Join(returnErr, wrapOptional("close UPX prerequisite root", release.root.Close()))
	}
	if returnErr != nil && !release.published && release.created {
		returnErr = errors.Join(returnErr, wrapOptional(
			"remove partial UPX prerequisite",
			removeOwnedUPXRoot(release.runnerRoot, release.rootName, release.createdInfo),
		))
	}
	return errors.Join(
		returnErr,
		wrapOptional("close RUNNER_TEMP for UPX prerequisite", release.runnerRoot.Close()),
	)
}

func (release *upxRelease) prepare() error {
	if _, statErr := release.runnerRoot.Lstat(release.rootName); statErr == nil {
		return fmt.Errorf(
			"UPX prerequisite directory collision: %s: %w",
			filepath.Join(release.runnerTemp, release.rootName), errInvalidConfig,
		)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect UPX prerequisite directory: %w", statErr)
	}
	if err := release.runnerRoot.Mkdir(release.rootName, upxDirectoryMode); err != nil {
		return fmt.Errorf("create UPX prerequisite directory: %w", err)
	}
	release.created = true
	createdInfo, err := release.runnerRoot.Lstat(release.rootName)
	release.createdInfo = createdInfo
	if err != nil || !createdInfo.IsDir() {
		return fmt.Errorf("inspect created UPX prerequisite directory: %w", errors.Join(err, errInvalidConfig))
	}
	release.root, err = release.runnerRoot.OpenRoot(release.rootName)
	if err != nil {
		return fmt.Errorf("open UPX prerequisite directory: %w", err)
	}
	openedInfo, err := release.root.Stat(".")
	if err != nil || !os.SameFile(openedInfo, release.createdInfo) {
		return fmt.Errorf("opened UPX prerequisite root identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	release.openedOwnedRoot = true
	for _, name := range []string{"download", "bin"} {
		if err := release.root.Mkdir(name, upxDirectoryMode); err != nil {
			return fmt.Errorf("create UPX %s directory: %w", name, err)
		}
	}
	return nil
}

func (release *upxRelease) install(ctx context.Context, options ReleaseUPXOptions, asset upxAssetContract) error {
	directory := filepath.Join(release.runnerTemp, release.rootName)
	commandEnvironment := withoutEnvironmentKeys(options.Environment, "GH_TOKEN", "GITHUB_TOKEN")
	archive, err := downloadUPXArchive(
		ctx, options.Runner, release.root, directory, options.Environment, asset,
	)
	if err != nil {
		return err
	}
	tarData, err := decompressUPXArchive(
		ctx, options.Runner, archive, directory, commandEnvironment, asset,
	)
	if err == nil {
		err = verifyRootedPathIdentity(
			release.root, "download/"+asset.archiveName, archive, "UPX archive after decompression",
		)
	}
	closeErr := archive.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, wrapOptional("close verified UPX archive", closeErr))
	}
	upxData, err := validateUPXTar(tarData, asset)
	if err != nil {
		return err
	}
	if writeErr := writeUPXExecutable(release.root, upxData, asset.entriesUPXSize()); writeErr != nil {
		return writeErr
	}
	binDirectory := filepath.Join(directory, "bin")
	if verifyErr := verifyUPXVersion(
		ctx, options.Runner, release.root, directory, binDirectory, commandEnvironment, asset,
	); verifyErr != nil {
		return verifyErr
	}
	currentRootInfo, err := release.runnerRoot.Lstat(release.rootName)
	if err != nil || !os.SameFile(currentRootInfo, release.createdInfo) {
		return fmt.Errorf("verified UPX prerequisite root identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	release.published, err = appendGitHubPath(options.GitHubPath, binDirectory)
	if err != nil {
		return err
	}
	return nil
}

func removeOwnedUPXRoot(root *os.Root, name string, expected os.FileInfo) error {
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect partial UPX prerequisite ownership: %w", err)
	}
	if expected == nil || !os.SameFile(current, expected) {
		return fmt.Errorf("partial UPX prerequisite ownership changed: %w", errInvalidConfig)
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("remove partial UPX prerequisite root: %w", err)
	}
	return nil
}

func clearOwnedUPXRoot(root *os.Root) error {
	if root == nil {
		return nil
	}
	var result error
	for _, name := range []string{"download", "bin"} {
		result = errors.Join(result, root.RemoveAll(name))
	}
	return result
}

func verifyRootedPathIdentity(root *os.Root, name string, file *os.File, label string) error {
	expected, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", label, err)
	}
	current, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect rooted %s: %w", label, err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(current, expected) {
		return fmt.Errorf("rooted %s identity changed: %w", label, errInvalidConfig)
	}
	return nil
}

func writeUPXExecutable(root *os.Root, data []byte, expectedSize int64) error {
	binRoot, err := root.OpenRoot("bin")
	if err != nil {
		return fmt.Errorf("open UPX bin directory: %w", err)
	}
	writeErr := writeRootedModeArtifact(binRoot, "upx", data, "UPX executable", int(expectedSize), 0o755)
	return errors.Join(writeErr, wrapOptional("close UPX bin directory", binRoot.Close()))
}

func validateUPXAssetContract(asset upxAssetContract) error {
	if err := validateUPXAssetMetadata(asset); err != nil {
		return err
	}
	digest, err := hex.DecodeString(asset.archiveSHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("invalid UPX archive SHA-256: %w", errors.Join(err, errInvalidConfig))
	}
	seen := make(map[string]struct{}, len(asset.entries))
	rootPrefix := "upx-" + asset.version + "-amd64_linux/"
	for _, entry := range asset.entries {
		if err := validateUPXTarEntryContract(entry, rootPrefix, asset.tarSize); err != nil {
			return err
		}
		if _, exists := seen[entry.name]; exists {
			return fmt.Errorf("duplicate UPX tar entry contract %q: %w", entry.name, errInvalidConfig)
		}
		seen[entry.name] = struct{}{}
	}
	if asset.entriesUPXSize() <= 0 {
		return fmt.Errorf("UPX executable is absent from the asset contract: %w", errInvalidConfig)
	}
	return nil
}

func validateUPXAssetMetadata(asset upxAssetContract) error {
	if asset.version != "4.2.2" || asset.archiveName != "upx-"+asset.version+"-amd64_linux.tar.xz" ||
		filepath.Base(asset.archiveName) != asset.archiveName || hasControl(asset.archiveName) ||
		asset.archiveSize <= 0 || asset.tarSize <= 0 || len(asset.entries) == 0 {
		return fmt.Errorf("invalid UPX asset contract: %w", errInvalidConfig)
	}
	return nil
}

func validateUPXTarEntryContract(entry upxTarEntry, rootPrefix string, tarSize int64) error {
	if !strings.HasPrefix(entry.name, rootPrefix) || hasControl(entry.name) || entry.size < 0 ||
		entry.size > tarSize ||
		(entry.kind != tar.TypeDir && entry.kind != tar.TypeReg) ||
		(entry.kind == tar.TypeDir && (entry.name != rootPrefix || entry.size != 0)) {
		return fmt.Errorf("invalid UPX tar entry contract %q: %w", entry.name, errInvalidConfig)
	}
	return nil
}

func (asset upxAssetContract) entriesUPXSize() int64 {
	want := "upx-" + asset.version + "-amd64_linux/upx"
	for _, entry := range asset.entries {
		if entry.name == want && entry.kind == tar.TypeReg && entry.mode == 0o755 {
			return entry.size
		}
	}
	return 0
}

type upxDownloadWriter struct {
	file  *os.File
	hash  hash.Hash
	limit int64
	count int64
}

func (writer *upxDownloadWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.limit-writer.count {
		return 0, fmt.Errorf("UPX archive exceeds %d bytes: %w", writer.limit, errInvalidConfig)
	}
	written, err := writer.file.Write(data)
	if written > 0 {
		_, _ = writer.hash.Write(data[:written])
		writer.count += int64(written)
	}
	if err != nil {
		return written, fmt.Errorf("write UPX archive: %w", err)
	}
	return written, nil
}

func (writer *upxDownloadWriter) sum() string {
	return hex.EncodeToString(writer.hash.Sum(nil))
}

func downloadUPXArchive(
	ctx context.Context,
	runner SpecRunner,
	root *os.Root,
	directory string,
	environment []string,
	asset upxAssetContract,
) (*os.File, error) {
	name := "download/" + asset.archiveName
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create UPX archive: %w", err)
	}
	writer := &upxDownloadWriter{file: file, hash: sha256.New(), limit: asset.archiveSize}
	runErr := runner.RunCommand(ctx, CommandSpec{
		Name: "gh", Args: []string{
			"release", "download", "v" + asset.version, "--repo", upxRepository,
			"--pattern", asset.archiveName, "--output", "-",
		}, Dir: directory, Env: environment, Stdout: writer,
	})
	if runErr == nil && writer.count != asset.archiveSize {
		runErr = fmt.Errorf("UPX archive size is %d, want %d: %w", writer.count, asset.archiveSize, errInvalidConfig)
	}
	if runErr == nil && writer.sum() != asset.archiveSHA256 {
		runErr = fmt.Errorf("UPX archive SHA-256 does not match the immutable pin: %w", errInvalidConfig)
	}
	if runErr == nil {
		runErr = file.Chmod(0o644)
	}
	if runErr == nil {
		runErr = file.Sync()
	}
	if runErr != nil {
		return nil, errors.Join(wrapOptional("download and verify UPX archive", runErr), wrapOptional("close UPX archive", file.Close()))
	}
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 || info.Size() != asset.archiveSize {
		return nil, errors.Join(
			fmt.Errorf("downloaded UPX archive violates its file contract: %w", errors.Join(err, errInvalidConfig)),
			wrapOptional("close UPX archive", file.Close()),
		)
	}
	if err := verifyOpenedRegularFile(file, info, "UPX archive"); err != nil {
		return nil, errors.Join(err, wrapOptional("close UPX archive", file.Close()))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Join(fmt.Errorf("rewind verified UPX archive: %w", err), wrapOptional("close UPX archive", file.Close()))
	}
	return file, nil
}

func decompressUPXArchive(
	ctx context.Context,
	runner SpecRunner,
	archive *os.File,
	directory string,
	environment []string,
	asset upxAssetContract,
) ([]byte, error) {
	reader := &upxDownloadReader{reader: archive, hash: sha256.New(), limit: asset.archiveSize}
	output := &synchronizedBoundedBuffer{limit: int(asset.tarSize)}
	runErr := runner.RunCommand(ctx, CommandSpec{
		Name: "xz", Args: []string{"-d", "-c", "-q"}, Dir: directory,
		Env: environment, Stdin: reader, Stdout: output,
	})
	if runErr != nil {
		return nil, fmt.Errorf("decompress UPX archive: %w", runErr)
	}
	if reader.count != asset.archiveSize || reader.sum() != asset.archiveSHA256 {
		return nil, fmt.Errorf("decompressed UPX input does not match the verified archive: %w", errInvalidConfig)
	}
	data := output.Bytes()
	if int64(len(data)) != asset.tarSize {
		return nil, fmt.Errorf("decompressed UPX tar size is %d, want %d: %w", len(data), asset.tarSize, errInvalidConfig)
	}
	return data, nil
}

type upxDownloadReader struct {
	reader io.Reader
	hash   hash.Hash
	limit  int64
	count  int64
}

func (reader *upxDownloadReader) Read(data []byte) (int, error) {
	if reader.count >= reader.limit {
		var extra [1]byte
		count, err := reader.reader.Read(extra[:])
		if count > 0 {
			return 0, fmt.Errorf("UPX archive exceeds %d bytes: %w", reader.limit, errInvalidConfig)
		}
		if err == io.EOF {
			return 0, io.EOF
		}
		if err != nil {
			return 0, fmt.Errorf("probe UPX archive limit: %w", err)
		}
		return 0, nil
	}
	if int64(len(data)) > reader.limit-reader.count {
		data = data[:reader.limit-reader.count]
	}
	count, err := reader.reader.Read(data)
	if count > 0 {
		_, _ = reader.hash.Write(data[:count])
		reader.count += int64(count)
	}
	if err == io.EOF {
		return count, io.EOF
	}
	if err != nil {
		return count, fmt.Errorf("read UPX archive: %w", err)
	}
	return count, nil
}

func (reader *upxDownloadReader) sum() string {
	return hex.EncodeToString(reader.hash.Sum(nil))
}

func validateUPXTar(data []byte, asset upxAssetContract) ([]byte, error) {
	reader := tar.NewReader(bytes.NewReader(data))
	var executable []byte
	for index, expected := range asset.entries {
		header, err := reader.Next()
		if err != nil {
			return nil, fmt.Errorf("read UPX tar entry %d: %w", index, err)
		}
		if header.Name != expected.name || header.Typeflag != expected.kind || header.Size != expected.size ||
			header.Mode != expected.mode || header.Linkname != "" {
			return nil, fmt.Errorf("UPX tar entry %d violates the exact archive contract: %w", index, errInvalidConfig)
		}
		if expected.name == "upx-"+asset.version+"-amd64_linux/upx" {
			executable = make([]byte, expected.size)
			if _, err := io.ReadFull(reader, executable); err != nil {
				return nil, fmt.Errorf("read UPX executable entry: %w", err)
			}
		} else if copied, err := io.CopyN(io.Discard, reader, expected.size); err != nil || copied != expected.size {
			return nil, fmt.Errorf("read UPX tar entry %s: %w", expected.name, errors.Join(err, errInvalidConfig))
		}
	}
	if header, err := reader.Next(); err != io.EOF {
		return nil, fmt.Errorf("UPX tar contains an unexpected trailing entry %v: %w", header, errors.Join(err, errInvalidConfig))
	}
	if len(executable) == 0 {
		return nil, fmt.Errorf("UPX tar lacks its executable: %w", errInvalidConfig)
	}
	return executable, nil
}

type synchronizedBoundedBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

func (buffer *synchronizedBoundedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if len(data) > buffer.limit-buffer.data.Len() {
		return 0, fmt.Errorf("captured command output exceeds %d bytes: %w", buffer.limit, errInvalidConfig)
	}
	written, err := buffer.data.Write(data)
	if err != nil {
		return written, fmt.Errorf("buffer captured command output: %w", err)
	}
	return written, nil
}

func (buffer *synchronizedBoundedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Clone(buffer.data.Bytes())
}

func verifyUPXVersion(
	ctx context.Context,
	runner SpecRunner,
	root *os.Root,
	directory string,
	binDirectory string,
	environment []string,
	asset upxAssetContract,
) error {
	binRoot, err := root.OpenRoot("bin")
	if err != nil {
		return fmt.Errorf("open UPX bin directory for version verification: %w", err)
	}
	expected, err := binRoot.Lstat("upx")
	if err != nil {
		return errors.Join(fmt.Errorf("inspect UPX executable for version verification: %w", err), wrapOptional("close UPX bin directory", binRoot.Close()))
	}
	executable, err := openRootedRegularFile(binRoot, "upx", expected, "UPX executable")
	if err != nil {
		return errors.Join(err, wrapOptional("close UPX bin directory", binRoot.Close()))
	}
	output := &synchronizedBoundedBuffer{limit: upxVersionLimit}
	runErr := runner.RunCommand(ctx, CommandSpec{
		Name: "upx", Args: []string{"--version"}, Dir: directory,
		Env: prependPath(environment, binDirectory), Executable: executable, Stdout: output, Stderr: output,
	})
	verifyErr := verifyOpenedRegularFile(executable, expected, "UPX executable")
	if verifyErr == nil {
		verifyErr = verifyRootedPathIdentity(binRoot, "upx", executable, "UPX executable after version verification")
	}
	closeErr := errors.Join(executable.Close(), binRoot.Close())
	if runErr != nil || verifyErr != nil || closeErr != nil {
		return errors.Join(
			wrapOptional("verify UPX prerequisite version", runErr), verifyErr,
			wrapOptional("close UPX executable", closeErr),
		)
	}
	firstLine, _, _ := strings.Cut(string(output.Bytes()), "\n")
	if firstLine != "upx "+asset.version {
		return fmt.Errorf("UPX prerequisite first line is %q, want %q: %w", firstLine, "upx "+asset.version, errInvalidConfig)
	}
	return nil
}

func validateGitHubPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || hasControl(path) {
		return fmt.Errorf("GITHUB_PATH must be an absolute clean path without control characters: %w", errInvalidConfig)
	}
	parent, err := validateAbsoluteExistingDirectory(filepath.Dir(path), "GITHUB_PATH parent")
	if err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(parent, filepath.Base(path)))
	if err != nil {
		return fmt.Errorf("inspect GITHUB_PATH: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > githubPathLimit {
		return fmt.Errorf("GITHUB_PATH is not a bounded regular file: %w", errInvalidConfig)
	}
	return nil
}

func appendGitHubPath(path, binDirectory string) (published bool, returnErr error) {
	if err := validateGitHubPath(path); err != nil {
		return false, err
	}
	parentRoot, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false, fmt.Errorf("open GITHUB_PATH parent: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close GITHUB_PATH parent", parentRoot.Close()))
	}()
	name := filepath.Base(path)
	expected, err := parentRoot.Lstat(name)
	if err != nil {
		return false, fmt.Errorf("inspect rooted GITHUB_PATH: %w", err)
	}
	file, err := openRootedRegularFile(parentRoot, name, expected, "GITHUB_PATH")
	if err != nil {
		return false, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, githubPathLimit+1))
	if readErr == nil {
		readErr = verifyOpenedRegularFile(file, expected, "GITHUB_PATH")
	}
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > githubPathLimit {
		return false, errors.Join(
			wrapOptional("read GITHUB_PATH", readErr), wrapOptional("close GITHUB_PATH", closeErr),
			func() error {
				if len(data) > githubPathLimit {
					return fmt.Errorf("GITHUB_PATH exceeds %d bytes: %w", githubPathLimit, errInvalidConfig)
				}
				return nil
			}(),
		)
	}
	appended := append(data, binDirectory...)
	appended = append(appended, '\n')
	if len(appended) > githubPathLimit {
		return false, fmt.Errorf("updated GITHUB_PATH exceeds %d bytes: %w", githubPathLimit, errInvalidConfig)
	}
	if err := writeRootedModeArtifact(parentRoot, name, appended, "GITHUB_PATH", githubPathLimit, expected.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}
