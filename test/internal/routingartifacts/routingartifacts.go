// Package routingartifacts validates and merges routing E2E JSON artifacts.
package routingartifacts

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxArtifactInputSize = 2 << 20
	imageIDLength        = 64
	imageIDArtifactSize  = imageIDLength + 1
	maximumTempAttempts  = 100
)

var (
	errInvalidArgument = errors.New("invalid routing artifact argument")
	errInvalidJSON     = errors.New("invalid routing artifact JSON")
	errUnsafeInput     = errors.New("unsafe routing artifact input")
	errUnsafeOutput    = errors.New("unsafe routing artifact output")
	errTempAttempts    = errors.New("exhausted rooted temporary output name attempts")
)

// Input identifies one routing suite's container inventory relative to the report root.
type Input struct {
	Name string
	Path string
}

type document struct {
	Suites map[string][]json.RawMessage `json:"suites"`
}

// Merge validates rooted JSON-array inventories and atomically writes their suite map.
func Merge(reportDirectory, output string, inputs []Input) (returnErr error) {
	root, err := openTrustedReportRoot(reportDirectory)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeRoot(root, "trusted report root"))
	}()

	if err := validateLocalArtifactPath(output); err != nil {
		return fmt.Errorf("validate output path: %w", err)
	}
	if len(inputs) == 0 {
		return fmt.Errorf("%w: inputs are empty", errInvalidArgument)
	}

	suites := make(map[string][]json.RawMessage, len(inputs))
	for _, input := range inputs {
		if input.Name == "" {
			return fmt.Errorf("%w: suite name is empty", errInvalidArgument)
		}
		if _, duplicate := suites[input.Name]; duplicate {
			return fmt.Errorf("%w: suite %q is duplicated", errInvalidArgument, input.Name)
		}
		items, readErr := readJSONArray(root, input.Path)
		if readErr != nil {
			return fmt.Errorf("read routing artifact suite %s: %w", input.Name, readErr)
		}
		suites[input.Name] = items
	}

	if err := writeAtomicJSON(root, output, document{Suites: suites}); err != nil {
		return fmt.Errorf("write merged routing artifact: %w", err)
	}
	return nil
}

func openTrustedReportRoot(reportDirectory string) (*os.Root, error) {
	if reportDirectory == "" || !filepath.IsAbs(reportDirectory) {
		return nil, fmt.Errorf("%w: trusted report root must be absolute", errInvalidArgument)
	}
	root, err := os.OpenRoot(reportDirectory)
	if err != nil {
		return nil, fmt.Errorf("open trusted report root %s: %w", reportDirectory, err)
	}
	return root, nil
}

func validateLocalArtifactPath(path string) error {
	if path == "" || path == "." || !filepath.IsLocal(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: artifact path %q must be clean and local to the report root", errInvalidArgument, path)
	}
	return nil
}

type pinnedParent struct {
	roots      []*os.Root
	components []string
	base       string
}

func openPinnedParent(root *os.Root, path string) (*pinnedParent, error) {
	if err := validateLocalArtifactPath(path); err != nil {
		return nil, err
	}
	directory, base := filepath.Split(path)
	directory = strings.TrimSuffix(directory, string(filepath.Separator))
	pinned := &pinnedParent{roots: []*os.Root{root}, base: base}
	if directory == "" || directory == "." {
		return pinned, nil
	}

	for component := range strings.SplitSeq(directory, string(filepath.Separator)) {
		parent := pinned.roots[len(pinned.roots)-1]
		initial, err := parent.Lstat(component)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("lstat artifact ancestor %s: %w", component, err),
				pinned.close(),
			)
		}
		if initial.Mode()&os.ModeSymlink != 0 || !initial.IsDir() {
			return nil, errors.Join(
				fmt.Errorf("%w: artifact ancestor %s is a symlink or non-directory", errUnsafeInput, component),
				pinned.close(),
			)
		}
		child, err := parent.OpenRoot(component)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("open artifact ancestor %s: %w", component, err),
				pinned.close(),
			)
		}
		pinned.roots = append(pinned.roots, child)
		pinned.components = append(pinned.components, component)
		opened, err := child.Lstat(".")
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("stat opened artifact ancestor %s: %w", component, err),
				pinned.close(),
			)
		}
		current, err := parent.Lstat(component)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("lstat artifact ancestor %s after open: %w", component, err),
				pinned.close(),
			)
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() ||
			!os.SameFile(initial, opened) || !os.SameFile(current, opened) {
			return nil, errors.Join(
				fmt.Errorf("%w: artifact ancestor %s changed while opening", errUnsafeInput, component),
				pinned.close(),
			)
		}
	}
	return pinned, nil
}

func (p *pinnedParent) parent() *os.Root {
	return p.roots[len(p.roots)-1]
}

func (p *pinnedParent) validateAncestors() error {
	for index, component := range p.components {
		current, err := p.roots[index].Lstat(component)
		if err != nil {
			return fmt.Errorf("lstat pinned artifact ancestor %s: %w", component, err)
		}
		opened, err := p.roots[index+1].Lstat(".")
		if err != nil {
			return fmt.Errorf("stat pinned artifact ancestor %s: %w", component, err)
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !opened.IsDir() ||
			!os.SameFile(current, opened) {
			return fmt.Errorf("%w: artifact ancestor %s changed or became unsafe", errUnsafeInput, component)
		}
	}
	return nil
}

func (p *pinnedParent) close() error {
	var closeErr error
	for index := len(p.roots) - 1; index >= 1; index-- {
		closeErr = errors.Join(closeErr, closeRoot(p.roots[index], "artifact ancestor"))
	}
	return closeErr
}

func readJSONArray(root *os.Root, path string) ([]json.RawMessage, error) {
	data, err := readBoundedInput(root, path, maxArtifactInputSize, nil)
	if err != nil {
		return nil, err
	}
	items, err := decodeJSONArray(path, data)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func readBoundedInput(
	root *os.Root,
	path string,
	limit int,
	afterOpen func() error,
) (_ []byte, returnErr error) {
	pinned, err := openPinnedParent(root, path)
	if err != nil {
		return nil, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, pinned.close())
	}()
	if ancestorErr := pinned.validateAncestors(); ancestorErr != nil {
		return nil, ancestorErr
	}
	initial, err := pinned.parent().Lstat(pinned.base)
	if err != nil {
		return nil, fmt.Errorf("lstat input %s: %w", path, err)
	}
	if initial.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: input %s is a symlink", errUnsafeInput, path)
	}
	if !initial.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: input %s is not a regular file", errUnsafeInput, path)
	}
	if initial.Size() > int64(limit) {
		return nil, fmt.Errorf(
			"%w: input %s is %d bytes, limit is %d",
			errUnsafeInput,
			path,
			initial.Size(),
			limit,
		)
	}
	return readOpenedInput(pinned, initial, path, limit, afterOpen)
}

func readBoundedInputWithHook(
	reportDirectory string,
	path string,
	afterOpen func() error,
) ([]byte, error) {
	root, err := openTrustedReportRoot(reportDirectory)
	if err != nil {
		return nil, err
	}
	data, readErr := readBoundedInput(root, path, maxArtifactInputSize, afterOpen)
	return data, errors.Join(readErr, closeRoot(root, "trusted report root"))
}

func readOpenedInput(
	pinned *pinnedParent,
	initial os.FileInfo,
	path string,
	limit int,
	afterOpen func() error,
) ([]byte, error) {
	file, err := pinned.parent().Open(pinned.base)
	if err != nil {
		return nil, fmt.Errorf("open input %s: %w", path, err)
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		return nil, errors.Join(
			fmt.Errorf("stat opened input %s: %w", path, statErr),
			closeFile(file, "input after stat failure"),
		)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return nil, errors.Join(
			fmt.Errorf("%w: input %s changed before open or is not regular", errUnsafeInput, path),
			closeFile(file, "unsafe input"),
		)
	}
	if opened.Size() > int64(limit) {
		return nil, errors.Join(
			fmt.Errorf(
				"%w: opened input %s is %d bytes, limit is %d",
				errUnsafeInput,
				path,
				opened.Size(),
				limit,
			),
			closeFile(file, "oversized input"),
		)
	}
	if afterOpen != nil {
		if hookErr := afterOpen(); hookErr != nil {
			return nil, errors.Join(
				fmt.Errorf("run post-open input hook: %w", hookErr),
				closeFile(file, "input after hook failure"),
			)
		}
	}
	current, lstatErr := pinned.parent().Lstat(pinned.base)
	if lstatErr != nil {
		return nil, errors.Join(
			fmt.Errorf("lstat input %s after open: %w", path, lstatErr),
			closeFile(file, "input after second lstat failure"),
		)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(current, opened) {
		return nil, errors.Join(
			fmt.Errorf("%w: input %s changed after open or is not regular", errUnsafeInput, path),
			closeFile(file, "replaced input"),
		)
	}
	if err := pinned.validateAncestors(); err != nil {
		return nil, errors.Join(err, closeFile(file, "input after ancestor validation failure"))
	}
	return readLimitedFile(file, path, limit)
}

func readLimitedFile(file *os.File, path string, limit int) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read bounded input %s: %w", path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close input %s: %w", path, closeErr)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%w: input %s grew beyond %d bytes", errUnsafeInput, path, limit)
	}
	return data, nil
}

func decodeJSONArray(path string, data []byte) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode JSON document from %s: %w", path, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: input %s contains more than one JSON document", errInvalidJSON, path)
		}
		return nil, fmt.Errorf("decode trailing JSON from %s: %w", path, err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return nil, fmt.Errorf("%w: input %s is not exactly one JSON array", errInvalidJSON, path)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("decode JSON array from %s: %w", path, err)
	}
	return items, nil
}

// ReadImageID returns one exact lowercase 64-hex image ID from a rooted artifact.
func ReadImageID(reportDirectory, path string) (_ string, returnErr error) {
	root, err := openTrustedReportRoot(reportDirectory)
	if err != nil {
		return "", err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeRoot(root, "trusted report root"))
	}()
	data, err := readBoundedInput(root, path, imageIDArtifactSize, nil)
	if err != nil {
		return "", fmt.Errorf("read image ID artifact: %w", err)
	}
	if len(data) != imageIDArtifactSize || data[imageIDLength] != '\n' || !validImageID(string(data[:imageIDLength])) {
		return "", fmt.Errorf("%w: image ID artifact must be exactly lowercase 64-hex plus newline", errInvalidArgument)
	}
	return string(data[:imageIDLength]), nil
}

// WriteImageID atomically persists one exact lowercase 64-hex image ID beneath the report root.
func WriteImageID(reportDirectory, path, imageID string) (returnErr error) {
	if !validImageID(imageID) {
		return fmt.Errorf("%w: image ID must be exactly lowercase 64-hex", errInvalidArgument)
	}
	root, err := openTrustedReportRoot(reportDirectory)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeRoot(root, "trusted report root"))
	}()
	if err := writeAtomicData(root, path, []byte(imageID+"\n"), nil, nil); err != nil {
		return fmt.Errorf("write image ID artifact: %w", err)
	}
	return nil
}

func validImageID(imageID string) bool {
	if len(imageID) != imageIDLength {
		return false
	}
	for _, character := range imageID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func writeAtomicJSON(root *os.Root, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode merged JSON: %w", err)
	}
	data = append(data, '\n')
	return writeAtomicData(root, path, data, nil, nil)
}

func writeAtomicDataWithHooks(
	reportDirectory string,
	path string,
	data []byte,
	beforeSnapshot func() error,
	beforeRename func() error,
) (returnErr error) {
	root, err := openTrustedReportRoot(reportDirectory)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeRoot(root, "trusted report root"))
	}()
	return writeAtomicData(root, path, data, beforeSnapshot, beforeRename)
}

func writeAtomicData(
	root *os.Root,
	path string,
	data []byte,
	beforeSnapshot func() error,
	beforeRename func() error,
) (returnErr error) {
	pinned, err := openPinnedParent(root, path)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, pinned.close())
	}()
	initial, err := snapshotOutput(pinned, path)
	if err != nil {
		return err
	}
	return publishAtomicData(pinned, path, data, initial, beforeSnapshot, beforeRename)
}

func publishAtomicData(
	pinned *pinnedParent,
	path string,
	data []byte,
	initial outputSnapshot,
	beforeSnapshot func() error,
	beforeRename func() error,
) (returnErr error) {
	temporary, temporaryName, err := createRootTemp(pinned.parent(), pinned.base)
	if err != nil {
		return err
	}
	keepTemporary := true
	defer func() {
		if keepTemporary {
			if removeErr := pinned.parent().Remove(temporaryName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				returnErr = errors.Join(
					returnErr,
					fmt.Errorf("remove temporary output %s: %w", temporaryName, removeErr),
				)
			}
		}
	}()
	temporaryInfo, err := writePreparedOutput(temporary, data)
	if err != nil {
		return err
	}
	renamed, err := renamePreparedOutput(
		pinned,
		path,
		temporaryName,
		temporaryInfo,
		initial,
		beforeSnapshot,
		beforeRename,
	)
	if renamed {
		keepTemporary = false
	}
	if err != nil {
		return err
	}
	return nil
}

func writePreparedOutput(temporary *os.File, data []byte) (os.FileInfo, error) {
	if _, writeErr := temporary.Write(data); writeErr != nil {
		return nil, errors.Join(
			fmt.Errorf("write temporary output: %w", writeErr),
			closeFile(temporary, "temporary output"),
		)
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		return nil, errors.Join(
			fmt.Errorf("sync temporary output: %w", syncErr),
			closeFile(temporary, "temporary output"),
		)
	}
	temporaryInfo, statErr := temporary.Stat()
	if statErr != nil {
		return nil, errors.Join(
			fmt.Errorf("stat temporary output: %w", statErr),
			closeFile(temporary, "temporary output"),
		)
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return nil, fmt.Errorf("close temporary output: %w", closeErr)
	}
	return temporaryInfo, nil
}

func renamePreparedOutput(
	pinned *pinnedParent,
	path string,
	temporaryName string,
	temporaryInfo os.FileInfo,
	initial outputSnapshot,
	beforeSnapshot func() error,
	beforeRename func() error,
) (bool, error) {
	if beforeSnapshot != nil {
		if hookErr := beforeSnapshot(); hookErr != nil {
			return false, fmt.Errorf("run pre-snapshot output hook: %w", hookErr)
		}
	}
	current, err := snapshotOutput(pinned, path)
	if err != nil {
		return false, err
	}
	if !sameOptionalFile(initial, current) {
		return false, fmt.Errorf("%w: output %s changed before atomic rename", errUnsafeOutput, path)
	}
	if beforeRename != nil {
		if hookErr := beforeRename(); hookErr != nil {
			return false, fmt.Errorf("run pre-rename output hook: %w", hookErr)
		}
	}
	if err := pinned.parent().Rename(temporaryName, pinned.base); err != nil {
		return false, fmt.Errorf("atomically replace output %s: %w", path, err)
	}
	if err := validatePublishedOutput(pinned, path, temporaryInfo); err != nil {
		return true, err
	}
	return true, nil
}

func validatePublishedOutput(pinned *pinnedParent, path string, temporaryInfo os.FileInfo) error {
	if err := pinned.validateAncestors(); err != nil {
		return fmt.Errorf("validate output ancestors after rename: %w", err)
	}
	published, err := pinned.parent().Lstat(pinned.base)
	if err != nil {
		return fmt.Errorf("lstat published output %s: %w", path, err)
	}
	if published.Mode()&os.ModeSymlink != 0 || !published.Mode().IsRegular() ||
		!os.SameFile(temporaryInfo, published) {
		return fmt.Errorf("%w: published output %s is not the rooted temporary file", errUnsafeOutput, path)
	}
	return nil
}

type outputSnapshot struct {
	info   os.FileInfo
	exists bool
}

func snapshotOutput(pinned *pinnedParent, path string) (outputSnapshot, error) {
	if err := pinned.validateAncestors(); err != nil {
		return outputSnapshot{}, err
	}
	info, err := pinned.parent().Lstat(pinned.base)
	if errors.Is(err, os.ErrNotExist) {
		return outputSnapshot{}, nil
	}
	if err != nil {
		return outputSnapshot{}, fmt.Errorf("lstat output %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return outputSnapshot{}, fmt.Errorf("%w: output %s is a symlink", errUnsafeOutput, path)
	}
	if !info.Mode().IsRegular() {
		return outputSnapshot{}, fmt.Errorf("%w: output %s is not a regular file", errUnsafeOutput, path)
	}
	return outputSnapshot{info: info, exists: true}, nil
}

func sameOptionalFile(first, second outputSnapshot) bool {
	if !first.exists || !second.exists {
		return first.exists == second.exists
	}
	return os.SameFile(first.info, second.info)
}

func createRootTemp(root *os.Root, base string) (*os.File, string, error) {
	for range maximumTempAttempts {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", fmt.Errorf("generate temporary output name: %w", err)
		}
		name := "." + base + ".tmp-" + hex.EncodeToString(random[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create rooted temporary output: %w", err)
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf(
		"create rooted temporary output after %d attempts: %w",
		maximumTempAttempts,
		errTempAttempts,
	)
}

func closeFile(file *os.File, description string) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", description, err)
	}
	return nil
}

func closeRoot(root *os.Root, description string) error {
	if err := root.Close(); err != nil {
		return fmt.Errorf("close %s: %w", description, err)
	}
	return nil
}
