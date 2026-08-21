// Package routingartifacts validates and merges routing E2E JSON artifacts.
package routingartifacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	maxArtifactInputSize = 2 << 20
	imageIDLength        = 64
	imageIDArtifactSize  = imageIDLength + 1
)

var (
	errInvalidArgument = errors.New("invalid routing artifact argument")
	errInvalidJSON     = errors.New("invalid routing artifact JSON")
	errUnsafeInput     = errors.New("unsafe routing artifact input")
	errUnsafeOutput    = errors.New("unsafe routing artifact output")
)

// Input identifies one routing suite's container inventory.
type Input struct {
	Name string
	Path string
}

type document struct {
	Suites map[string][]json.RawMessage `json:"suites"`
}

// Merge validates JSON-array inventories and atomically writes their suite map.
func Merge(output string, inputs []Input) error {
	if output == "" {
		return fmt.Errorf("%w: output path is empty", errInvalidArgument)
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
		items, err := readJSONArray(input.Path)
		if err != nil {
			return fmt.Errorf("read routing artifact suite %s: %w", input.Name, err)
		}
		suites[input.Name] = items
	}

	if err := writeAtomicJSON(output, document{Suites: suites}); err != nil {
		return fmt.Errorf("write merged routing artifact: %w", err)
	}
	return nil
}

func readJSONArray(path string) ([]json.RawMessage, error) {
	data, err := readBoundedInput(path)
	if err != nil {
		return nil, err
	}
	items, err := decodeJSONArray(path, data)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func readBoundedInput(path string) ([]byte, error) {
	return readBoundedInputWithHook(path, maxArtifactInputSize, nil)
}

func readBoundedInputWithHook(path string, limit int, afterOpen func() error) ([]byte, error) {
	initial, err := lstatBoundedInput(path, limit)
	if err != nil {
		return nil, err
	}
	return readOpenedInput(path, initial, limit, afterOpen)
}

func lstatBoundedInput(path string, limit int) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("lstat input %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: input %s is a symlink", errUnsafeInput, path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: input %s is not a regular file", errUnsafeInput, path)
	}
	if info.Size() > int64(limit) {
		return nil, fmt.Errorf(
			"%w: input %s is %d bytes, limit is %d",
			errUnsafeInput,
			path,
			info.Size(),
			limit,
		)
	}
	return info, nil
}

func readOpenedInput(path string, initial os.FileInfo, limit int, afterOpen func() error) ([]byte, error) {
	file, err := os.Open(path)
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
	current, lstatErr := os.Lstat(path)
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

// ReadImageID returns one exact lowercase 64-hex image ID from a safe artifact.
func ReadImageID(path string) (string, error) {
	data, err := readBoundedInputWithHook(path, imageIDArtifactSize, nil)
	if err != nil {
		return "", fmt.Errorf("read image ID artifact: %w", err)
	}
	if len(data) != imageIDArtifactSize || data[imageIDLength] != '\n' || !validImageID(string(data[:imageIDLength])) {
		return "", fmt.Errorf("%w: image ID artifact must be exactly lowercase 64-hex plus newline", errInvalidArgument)
	}
	return string(data[:imageIDLength]), nil
}

// WriteImageID atomically persists one exact lowercase 64-hex image ID.
func WriteImageID(path, imageID string) error {
	if !validImageID(imageID) {
		return fmt.Errorf("%w: image ID must be exactly lowercase 64-hex", errInvalidArgument)
	}
	if err := writeAtomicData(path, []byte(imageID+"\n")); err != nil {
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

func writeAtomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode merged JSON: %w", err)
	}
	data = append(data, '\n')
	return writeAtomicData(path, data)
}

func writeAtomicData(path string, data []byte) (returnErr error) {
	if err := validateOutputPath(path); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("lstat output directory %s: %w", directory, err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return fmt.Errorf("%w: directory %s is not a regular directory", errUnsafeOutput, directory)
	}

	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create same-directory temporary output: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if keepTemporary {
			if removeErr := os.Remove(temporaryPath); removeErr != nil {
				returnErr = errors.Join(
					returnErr,
					fmt.Errorf("remove temporary output %s: %w", temporaryPath, removeErr),
				)
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("chmod temporary output: %w", err), closeFile(temporary, "temporary output"))
	}
	if _, err := temporary.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write temporary output: %w", err), closeFile(temporary, "temporary output"))
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync temporary output: %w", err), closeFile(temporary, "temporary output"))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := validateOutputPath(path); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("atomically replace output %s: %w", path, err)
	}
	keepTemporary = false
	return nil
}

func validateOutputPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat output %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: output %s is a symlink", errUnsafeOutput, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: output %s is not a regular file", errUnsafeOutput, path)
	}
	return nil
}

func closeFile(file *os.File, description string) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", description, err)
	}
	return nil
}
