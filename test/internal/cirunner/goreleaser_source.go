package cirunner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

//nolint:tagliatelle // go mod download -json owns these exported wire field names.
type goreleaserDownload struct {
	Path     string           `json:"Path"`
	Version  string           `json:"Version"`
	Zip      string           `json:"Zip"`
	Sum      string           `json:"Sum"`
	GoModSum string           `json:"GoModSum"`
	Error    *json.RawMessage `json:"Error"`
	Origin   struct {
		VCS  string `json:"VCS"`
		URL  string `json:"URL"`
		Hash string `json:"Hash"`
		Ref  string `json:"Ref"`
	} `json:"Origin"`
}

type goreleaserSource struct {
	parent, root  *os.Root
	info          os.FileInfo
	directory     string
	owned         map[string]os.FileInfo
	contents      map[string]string
	closed        bool
	name          string
	keepDirectory bool
	outputOwner   *sourceInstallation
}

func prepareGoReleaserSource(parent *os.Root, directory string) (*goreleaserSource, error) {
	return preparePublisherSource(parent, directory, goreleaserSourceDirectoryName)
}

func preparePublisherSource(parent *os.Root, directory, name string) (_ *goreleaserSource, returnErr error) {
	if name != goreleaserSourceDirectoryName && name != refmtDirectoryName {
		return nil, fmt.Errorf("publisher source directory is not allowlisted: %w", errInvalidConfig)
	}
	source := &goreleaserSource{
		parent: parent, directory: filepath.Join(directory, name), name: name,
		owned: make(map[string]os.FileInfo), contents: make(map[string]string),
	}
	if err := parent.Mkdir(name, 0o700); err != nil {
		return nil, fmt.Errorf("create private publisher source: %w", err)
	}
	source.info, returnErr = parent.Lstat(name)
	if returnErr != nil {
		return nil, fmt.Errorf("capture private publisher source: %w", returnErr)
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, source.Close())
		}
	}()
	source.root, returnErr = parent.OpenRoot(name)
	if returnErr != nil {
		return nil, fmt.Errorf("open private publisher source: %w", returnErr)
	}
	return source, nil
}

func resolveGoReleaserDownload(
	ctx context.Context, runner SpecRunner, source *goreleaserSource, profile goreleaserProfile, environment []string,
) (goreleaserDownload, error) {
	var download goreleaserDownload
	origin, err := publisherSourceOrigin(profile.Module)
	if err != nil {
		return download, err
	}
	stub := []byte("module gobfd.local/source-download\n\ngo " + strings.TrimPrefix(runtime.Version(), "go") + "\n")
	if err := source.write(goModFilename, stub, true); err != nil {
		return download, err
	}
	output := &synchronizedBoundedBuffer{limit: goreleaserMetadataLimit}
	commandErr := runner.RunCommand(ctx, CommandSpec{
		Name: "go", Dir: source.directory, Env: environment, Stdout: output,
		Args: []string{"mod", downloadSubcommand, "-json", profile.Module + "@" + profile.Version},
	})
	cleanupErr := source.clearDownloadFiles()
	if commandErr != nil || cleanupErr != nil {
		return download, errors.Join(wrapOptional("download authenticated publisher source", commandErr), cleanupErr)
	}
	data := output.Bytes()
	if err := validateStrictJSONDocument(data, "publisher source download"); err != nil {
		return download, err
	}
	if err := json.Unmarshal(data, &download); err != nil {
		return download, fmt.Errorf("decode publisher source download: %w", err)
	}
	return download, download.verifySource(profile, origin)
}

func (download goreleaserDownload) verifySource(profile goreleaserProfile, origin string) error {
	if download.Error != nil || download.Path != profile.Module || download.Version != profile.Version ||
		download.Sum != profile.Sum || download.GoModSum != profile.GoModSum {
		return fmt.Errorf("publisher module download differs from reviewed source: %w", errInvalidConfig)
	}
	if download.Origin.VCS != "git" || download.Origin.Hash != profile.SourceCommit ||
		download.Origin.Ref != "refs/tags/"+profile.Version ||
		download.Origin.URL != origin {
		return fmt.Errorf("publisher source origin differs from reviewed commit: %w", errInvalidConfig)
	}
	return nil
}

func publisherSourceOrigin(module string) (string, error) {
	switch module {
	case toolprofile.RefmtModule:
		return "https://github.com/polydawn/refmt", nil
	case toolprofile.GoReleaserModule:
		return "https://github.com/goreleaser/goreleaser", nil
	default:
		return "", fmt.Errorf("publisher source module is not allowlisted: %w", errInvalidConfig)
	}
}

func (source *goreleaserSource) clearDownloadFiles() error {
	if err := source.verifyRoot(); err != nil {
		return err
	}
	entries, err := fs.ReadDir(source.root.FS(), ".")
	if err != nil {
		return fmt.Errorf("read owned source acquisition outputs: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != goModFilename && entry.Name() != "go.sum" {
			return fmt.Errorf("source acquisition wrote an unexpected file: %w", errInvalidConfig)
		}
		// The fixed Go download command owns these two outputs in this exclusive temporary module.
		info, err := source.root.Lstat(entry.Name())
		if err != nil || !info.Mode().IsRegular() || info.Size() > goreleaserFileLimit {
			return fmt.Errorf("source acquisition output is not bounded and regular: %w", errors.Join(err, errInvalidConfig))
		}
		if previous := source.owned[entry.Name()]; previous != nil && !os.SameFile(previous, info) {
			return fmt.Errorf("source acquisition replaced an owned output: %w", errInvalidConfig)
		}
		source.owned[entry.Name()] = info
	}
	for _, entry := range entries {
		info, statErr := source.root.Lstat(entry.Name())
		if statErr != nil || !os.SameFile(info, source.owned[entry.Name()]) {
			return fmt.Errorf("source acquisition output identity changed: %w", errors.Join(statErr, errInvalidConfig))
		}
		if err := source.root.Remove(entry.Name()); err != nil {
			return fmt.Errorf("remove owned source acquisition output: %w", err)
		}
		delete(source.owned, entry.Name())
		delete(source.contents, entry.Name())
	}
	return nil
}

func readGoReleaserZIP(filename string, profile goreleaserProfile) (_ map[string][]byte, returnErr error) {
	parentPath, err := validateAbsoluteExistingDirectory(filepath.Dir(filename), "publisher ZIP parent")
	if err != nil || filepath.Clean(filename) != filename || hasControl(filename) {
		return nil, fmt.Errorf("invalid publisher ZIP path: %w", errors.Join(err, errInvalidConfig))
	}
	root, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open publisher ZIP parent: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	info, err := root.Lstat(filepath.Base(filename))
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > goreleaserZIPLimit {
		return nil, fmt.Errorf("publisher ZIP is not a bounded regular file: %w", errors.Join(err, errInvalidConfig))
	}
	file, err := openRootedRegularFile(root, filepath.Base(filename), info, "publisher source ZIP")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("read publisher source ZIP: %w", err)
	}
	contents, err := authenticateGoReleaserZIP(archive, profile)
	if err != nil {
		return nil, err
	}
	return contents, errors.Join(verifyOpenedRegularFile(file, info, "publisher source ZIP"),
		verifyRootedPathIdentity(root, filepath.Base(filename), file, "publisher source ZIP"))
}

func authenticateGoReleaserZIP(archive *zip.Reader, profile goreleaserProfile) (map[string][]byte, error) {
	if len(archive.File) != profile.Files {
		return nil, fmt.Errorf("publisher ZIP file count differs from source contract: %w", errInvalidConfig)
	}
	prefix := profile.Module + "@" + profile.Version + "/"
	contents := make(map[string][]byte, len(archive.File))
	var total int64
	for _, entry := range archive.File {
		name, pathErr := goreleaserZIPName(entry, prefix)
		if pathErr != nil {
			return nil, pathErr
		}
		if _, duplicate := contents[name]; duplicate {
			return nil, fmt.Errorf("publisher ZIP repeats a file: %w", errInvalidConfig)
		}
		data, err := readGoReleaserZIPEntry(entry)
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		if total > profile.Bytes {
			return nil, fmt.Errorf("publisher ZIP exceeds original source bytes: %w", errInvalidConfig)
		}
		contents[name] = data
	}
	if _, found := contents[goModFilename]; !found {
		return nil, fmt.Errorf("publisher ZIP has no main manifest: %w", errInvalidConfig)
	}
	if total != profile.Bytes || goReleaserContentSum(contents, prefix) != profile.Sum ||
		goReleaserContentSum(map[string][]byte{goModFilename: contents[goModFilename]}, "") != profile.GoModSum {
		return nil, fmt.Errorf("publisher ZIP content/checksums differ from original source: %w", errInvalidConfig)
	}
	return contents, nil
}

func goreleaserZIPName(entry *zip.File, prefix string) (string, error) {
	name, found := strings.CutPrefix(entry.Name, prefix)
	if !found || name == "" || name == "." || name == ".." || path.Clean(name) != name || hasControl(name) ||
		strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "../") || path.IsAbs(name) ||
		!entry.Mode().IsRegular() {
		return "", fmt.Errorf("publisher ZIP has an invalid path or entry type: %w", errInvalidConfig)
	}
	return name, nil
}

func readGoReleaserZIPEntry(entry *zip.File) ([]byte, error) {
	if entry.UncompressedSize64 > goreleaserFileLimit {
		return nil, fmt.Errorf("publisher ZIP entry exceeds file limit: %w", errInvalidConfig)
	}
	file, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("open publisher ZIP entry: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, goreleaserFileLimit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > goreleaserFileLimit ||
		int64(len(data)) != entry.FileInfo().Size() {
		return nil, errors.Join(wrapOptional("read publisher ZIP entry", readErr), closeErr, errInvalidConfig)
	}
	return data, nil
}

func goReleaserContentSum(contents map[string][]byte, prefix string) string {
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	slices.Sort(names)
	summary := sha256.New()
	for _, name := range names {
		fmt.Fprintf(summary, "%x  %s\n", sha256.Sum256(contents[name]), prefix+name)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(summary.Sum(nil))
}

func (source *goreleaserSource) copy(ctx context.Context, contents map[string][]byte) error {
	for name, data := range contents {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("copy publisher source: %w", err)
		}
		if err := source.ensureParents(name); err != nil {
			return err
		}
		if err := source.write(name, data, true); err != nil {
			return err
		}
	}
	return source.verify()
}

func (source *goreleaserSource) ensureParents(name string) error {
	components := strings.Split(path.Dir(name), "/")
	current := ""
	for _, component := range components {
		if component == "." {
			continue
		}
		current = path.Join(current, component)
		if _, exists := source.owned[current]; exists {
			continue
		}
		if err := source.root.Mkdir(current, 0o700); err != nil {
			return fmt.Errorf("create private publisher source directory: %w", err)
		}
		info, err := source.root.Lstat(current)
		if err != nil {
			return fmt.Errorf("capture publisher source directory: %w", err)
		}
		source.owned[current] = info
	}
	return nil
}

func (source *goreleaserSource) write(name string, data []byte, create bool) error {
	if err := source.verifyRoot(); err != nil {
		return err
	}
	flags := os.O_WRONLY
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := source.root.OpenFile(name, flags, 0o600)
	if err != nil {
		return fmt.Errorf("open owned publisher source file: %w", err)
	}
	if create {
		source.owned[name], err = file.Stat()
	}
	if err == nil {
		err = verifyOpenedRegularFile(file, source.owned[name], "private publisher source")
	}
	if err == nil {
		err = file.Truncate(0)
	}
	if err == nil {
		var written int
		written, err = file.Write(data)
		if err == nil && written != len(data) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = verifyRootedPathIdentity(source.root, name, file, "private publisher source")
	}
	var updated os.FileInfo
	if err == nil {
		updated, err = file.Stat()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.Join(wrapOptional("write private publisher source", err), closeErr)
	}
	source.owned[name] = updated
	source.contents[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	return nil
}

func (source *goreleaserSource) patch(profile goreleaserProfile) error {
	data, err := readGoReleaserFile(source.root, profile.Patch.Path)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != profile.Patch.BeforeSHA256 ||
		bytes.Count(data, []byte(profile.Patch.Before)) != 1 {
		return fmt.Errorf("publisher patch preimage differs from reviewed single import: %w", errInvalidConfig)
	}
	updated := bytes.Replace(data, []byte(profile.Patch.Before), []byte(profile.Patch.After), 1)
	if fmt.Sprintf("%x", sha256.Sum256(updated)) != profile.Patch.AfterSHA256 {
		return fmt.Errorf("publisher patch postimage differs from reviewed source: %w", errInvalidConfig)
	}
	return source.write(profile.Patch.Path, updated, false)
}

func (source *goreleaserSource) verifyRoot() error {
	info, err := source.parent.Lstat(source.name)
	if err != nil || source.info == nil || !info.IsDir() || info.Mode().Perm() != 0o700 ||
		!os.SameFile(info, source.info) {
		return fmt.Errorf("private publisher source root identity changed: %w", errors.Join(err, errInvalidConfig))
	}
	return nil
}

func (source *goreleaserSource) verify() error {
	if err := source.verifyRoot(); err != nil {
		return err
	}
	return wrapOptional("verify private publisher source", fs.WalkDir(source.root.FS(), ".",
		func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || name == "." {
				return walkErr
			}
			// The fixed compiler output is owned and verified by sourceInstallation.
			if source.keepDirectory && name == goreleaserName {
				if source.outputOwner == nil || !source.outputOwner.buildStarted {
					return fmt.Errorf("publisher consumer contains unowned compiler output: %w", errInvalidConfig)
				}
				return nil
			}
			info, err := source.root.Lstat(name)
			if err != nil || source.owned[name] == nil || !os.SameFile(info, source.owned[name]) ||
				info.Mode() != source.owned[name].Mode() {
				return errors.Join(err, errInvalidConfig)
			}
			if !entry.IsDir() && !info.Mode().IsRegular() {
				return errInvalidConfig
			}
			return nil
		}))
}

func (source *goreleaserSource) verifyContents() error {
	if err := source.verify(); err != nil {
		return err
	}
	for name, digest := range source.contents {
		data, err := readGoReleaserFile(source.root, name)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
			return fmt.Errorf("private publisher source content changed: %w", errInvalidConfig)
		}
	}
	return nil
}

func (source *goreleaserSource) Close() error {
	if source.closed {
		return nil
	}
	source.closed = true
	if source.root == nil {
		if err := source.verifyRoot(); err != nil {
			return err
		}
		return wrapOptional("remove unopened publisher source", source.parent.Remove(source.name))
	}
	result := source.remove()
	return errors.Join(result, source.root.Close())
}

func (source *goreleaserSource) remove() error {
	if err := source.verify(); err != nil {
		return err
	}
	names := make([]string, 0, len(source.owned))
	for name := range source.owned {
		names = append(names, name)
	}
	slices.SortFunc(names, func(left, right string) int { return strings.Count(right, "/") - strings.Count(left, "/") })
	var result error
	for _, name := range names {
		info, err := source.root.Lstat(name)
		if err != nil || !os.SameFile(info, source.owned[name]) {
			return errors.Join(result, err, errInvalidConfig)
		}
		result = errors.Join(result, wrapOptional("remove owned publisher source entry", source.root.Remove(name)))
	}
	if result == nil && !source.keepDirectory {
		result = wrapOptional("remove private publisher source", source.parent.Remove(source.name))
	}
	return result
}

func goreleaserSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
