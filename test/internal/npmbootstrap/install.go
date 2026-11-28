package npmbootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	downloadTimeout  = 30 * time.Second
	tarEntryOverhead = 1024
	tarTrailerLimit  = 4096
)

// Install verifies each upstream archive before extracting it into a private directory.
// It replaces two bundled packages and applies the bound preprocessing repair
// before returning the private installation. It never executes package code.
func Install(ctx context.Context, directory string, contract Contract, client *http.Client) error {
	if err := contract.validate(); err != nil {
		return err
	}
	if contract.Artifacts[0] != npmRepairArtifact() {
		return fmt.Errorf("npm archive differs from the qualified repair source: %w", ErrInvalid)
	}
	return install(ctx, directory, contract, client, npmRepairSourceHashes())
}

func install(ctx context.Context, directory string, contract Contract, client *http.Client,
	hashes [3]npmRepairHashes,
) (returnErr error) {
	if err := contract.validate(); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open bootstrap directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, closeResource(root, "bootstrap root")) }()
	for _, artifact := range contract.Artifacts {
		data, err := download(ctx, artifact, client)
		if err != nil {
			return err
		}
		target := "npm-bootstrap"
		if artifact.Name != "npm" {
			target = filepath.Join(target, "node_modules", artifact.Name)
		}
		if err := createPackageDirectory(root, target, artifact.Name); err != nil {
			return fmt.Errorf("create bootstrap package directory: %w", err)
		}
		if err := installArchive(root, target, artifact, data, hashes); err != nil {
			return err
		}
	}
	return nil
}

func download(ctx context.Context, artifact Artifact, supplied *http.Client) ([]byte, error) {
	client := http.Client{Timeout: downloadTimeout}
	if supplied != nil {
		client.Transport = supplied.Transport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	url := "https://registry.npmjs.org/" + artifact.Name + "/-/" + artifact.Name + "-" + artifact.Version + ".tgz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create bootstrap download request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download bootstrap %s: %w", artifact.Name, err)
	}
	data, readErr := readArchiveResponse(response, artifact)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return nil, fmt.Errorf("read bootstrap %s: %w", artifact.Name, err)
	}
	return data, nil
}

func readArchiveResponse(response *http.Response, artifact Artifact) ([]byte, error) {
	if response.StatusCode != http.StatusOK ||
		(response.ContentLength >= 0 && response.ContentLength != artifact.ArchiveBytes) {
		return nil, fmt.Errorf("bootstrap response status or declared length: %w", ErrInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, artifact.ArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded bootstrap response: %w", err)
	}
	if int64(len(data)) != artifact.ArchiveBytes {
		return nil, fmt.Errorf("bootstrap response length: %w", ErrInvalid)
	}
	digest := sha512.Sum512(data)
	if "sha512-"+base64.StdEncoding.EncodeToString(digest[:]) != artifact.Integrity {
		return nil, fmt.Errorf("bootstrap archive integrity mismatch: %w", ErrInvalid)
	}
	return data, nil
}

func installArchive(root *os.Root, target string, artifact Artifact, data []byte,
	hashes [3]npmRepairHashes,
) (returnErr error) {
	input := bytes.NewReader(data)
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return fmt.Errorf("open bootstrap gzip: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, closeResource(compressed, "bootstrap gzip")) }()
	compressed.Multistream(false)
	limit := artifact.ExpandedBytes + int64(artifact.Entries)*tarEntryOverhead + tarTrailerLimit
	limited := &io.LimitedReader{R: compressed, N: limit + 1}
	if err := extractTar(root, target, artifact, tar.NewReader(limited), hashes); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return fmt.Errorf("finish bootstrap gzip verification: %w", err)
	}
	if limited.N == 0 || input.Len() != 0 {
		return fmt.Errorf("bootstrap gzip exceeds its single-stream bound: %w", ErrInvalid)
	}
	return nil
}

func extractTar(root *os.Root, target string, artifact Artifact, reader *tar.Reader, hashes [3]npmRepairHashes) error {
	seen := make(map[string]bool)
	var expanded int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return verifyTarContents(artifact, count, expanded, seen)
		}
		if err != nil {
			return fmt.Errorf("read bootstrap tar header: %w", err)
		}
		name, err := regularEntryName(header)
		if err != nil {
			return err
		}
		if seen[name] || count >= artifact.Entries || header.Size > artifact.ExpandedBytes-expanded {
			return fmt.Errorf("bootstrap tar duplicate path or resource bound: %w", ErrInvalid)
		}
		seen[name] = true
		expanded += header.Size
		if err := writeArtifactEntry(root, target, artifact.Name, name, reader, header.Size, hashes); err != nil {
			return err
		}
	}
}

func verifyTarContents(artifact Artifact, count int, expanded int64, seen map[string]bool) error {
	if count != artifact.Entries || expanded != artifact.ExpandedBytes {
		return fmt.Errorf("bootstrap tar entry count or expanded size: %w", ErrInvalid)
	}
	if artifact.Name == "npm" {
		return verifyNPMRepairPresence(seen)
	}
	return nil
}

func writeArtifactEntry(root *os.Root, target, artifact, name string, reader io.Reader,
	size int64, hashes [3]npmRepairHashes,
) error {
	if artifact == "npm" {
		if replacedEntry(name) {
			return nil
		}
		return writeNPMEntry(root, target, name, reader, size, hashes)
	}
	return writeEntry(root, filepath.Join(target, filepath.FromSlash(name)), reader, size)
}

func regularEntryName(header *tar.Header) (string, error) {
	name, found := strings.CutPrefix(header.Name, "package/")
	if !found || !fs.ValidPath(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:") ||
		header.Linkname != "" || header.Size < 0 || header.Typeflag != tar.TypeReg {
		return "", fmt.Errorf("bootstrap tar requires canonical regular package files: %w", ErrInvalid)
	}
	return name, nil
}

func replacedEntry(name string) bool {
	for _, prefix := range []string{"node_modules/undici", "node_modules/ip-address"} {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

func writeEntry(root *os.Root, name string, reader io.Reader, size int64) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return fmt.Errorf("create rooted bootstrap directory: %w", err)
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create rooted regular bootstrap file: %w", err)
	}
	_, copyErr := io.CopyN(file, reader, size)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return fmt.Errorf("write rooted bootstrap file: %w", err)
	}
	return nil
}

func createPackageDirectory(root *os.Root, target, name string) error {
	var err error
	if name == "npm" {
		err = root.Mkdir(target, 0o700)
	} else {
		err = root.MkdirAll(target, 0o700)
	}
	if err != nil {
		return fmt.Errorf("create private bootstrap package: %w", err)
	}
	return nil
}

func closeResource(resource io.Closer, purpose string) error {
	if err := resource.Close(); err != nil {
		return fmt.Errorf("close %s: %w", purpose, err)
	}
	return nil
}
