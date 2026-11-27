package integrationrunner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

const (
	maxKubernetesArchiveBytes    = 4 << 30
	maxKubernetesArchiveEntries  = 4096
	maxKubernetesArchivePathLen  = 4096
	maxKubernetesConfigBytes     = 4 << 20
	archiveReadBufferBytes       = 32 << 10
	minTarTerminalBytes          = 1024
	maxTarTerminalBytes          = 1535
	maxTarTrailingPaddingBytes   = 10240
	expectedRootfsFieldCount     = 2
	archiveReceiptSchemaVersion  = 1
	archiveRelativeParentSegment = ".."
)

var errKubernetesArchiveContent = errors.New("kubernetes image archive content differs from the selected image")

type kubernetesArchiveEvidence struct {
	Schema   int    `json:"schema"`
	Image    string `json:"image"`
	SourceID string `json:"source_id"`
	Digest   string `json:"archive_digest"`
	Bytes    int64  `json:"archive_bytes"`
}

type kubernetesArchiveBlob struct {
	Offset int64
	Size   int64
	Digest string
}

type verifiedKubernetesArchive struct {
	Evidence kubernetesArchiveEvidence
	Config   kubernetesArchiveBlob
	Layers   []kubernetesArchiveBlob
}

type kubernetesArchiveMember struct {
	offset int64
	size   int64
	digest string
	link   string
}

type kubernetesArchiveReader struct {
	checkCancel func() error
	reader      io.Reader
	offset      int64
}

func (r *kubernetesArchiveReader) Read(data []byte) (int, error) {
	if r.checkCancel != nil {
		if cancelErr := r.checkCancel(); cancelErr != nil {
			return 0, fmt.Errorf("archive read canceled: %w", cancelErr)
		}
	}
	n, readErr := r.reader.Read(data)
	r.offset += int64(n)
	if errors.Is(readErr, io.EOF) {
		return n, io.EOF
	}
	if readErr != nil {
		return n, fmt.Errorf("read archive stream: %w", readErr)
	}
	return n, nil
}

func verifyKubernetesArchive(
	ctx context.Context,
	file *os.File,
	image, expectedID string,
) (kubernetesArchiveEvidence, error) {
	verified, err := inspectKubernetesArchive(ctx, file, image, expectedID)
	if err != nil {
		return kubernetesArchiveEvidence{}, err
	}
	return verified.Evidence, nil
}

func inspectKubernetesArchive(
	ctx context.Context,
	file *os.File,
	image, expectedID string,
) (verifiedKubernetesArchive, error) {
	var empty verifiedKubernetesArchive
	before, statErr := file.Stat()
	if statErr != nil {
		return empty, fmt.Errorf("stat kubernetes archive before verification: %w", statErr)
	}
	if !validArchiveFileStat(before, expectedID) {
		return empty, errKubernetesArchiveContent
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		return empty, fmt.Errorf("rewind kubernetes archive for verification: %w", seekErr)
	}
	hash := sha256.New()
	limited := io.LimitReader(file, before.Size()+1)
	input := kubernetesArchiveReader{
		checkCancel: ctx.Err,
		reader:      io.TeeReader(limited, hash),
	}
	members, scanErr := scanKubernetesArchiveTar(file, &input, before.Size())
	if scanErr != nil {
		return empty, scanErr
	}
	configBlob, layerBlobs, memberErr := verifyKubernetesArchiveMembers(file, members, image, expectedID)
	if memberErr != nil {
		return empty, memberErr
	}
	after, afterErr := file.Stat()
	if afterErr != nil {
		return empty, fmt.Errorf("stat kubernetes archive after verification: %w", afterErr)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return empty, errKubernetesArchiveContent
	}
	return verifiedKubernetesArchive{
		Evidence: kubernetesArchiveEvidence{
			Schema:   archiveReceiptSchemaVersion,
			Image:    image,
			SourceID: expectedID,
			Digest:   fmt.Sprintf("sha256:%x", hash.Sum(nil)),
			Bytes:    before.Size(),
		},
		Config: configBlob,
		Layers: layerBlobs,
	}, nil
}

func validArchiveFileStat(info os.FileInfo, expectedID string) bool {
	return info.Mode().IsRegular() &&
		info.Size() > 0 &&
		info.Size() <= maxKubernetesArchiveBytes &&
		containertest.IsImageDigest(expectedID)
}

func scanKubernetesArchiveTar(
	file *os.File,
	input *kubernetesArchiveReader,
	totalSize int64,
) (map[string]kubernetesArchiveMember, error) {
	reader := tar.NewReader(input)
	members := make(map[string]kubernetesArchiveMember)
	buffer := make([]byte, archiveReadBufferBytes)
	for {
		position := input.offset
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			if termErr := verifyTarTerminalBlocks(file, position, input.offset-position, buffer); termErr != nil {
				return nil, termErr
			}
			break
		}
		if nextErr != nil {
			return nil, errors.Join(errKubernetesArchiveContent, nextErr)
		}
		member, okErr := readKubernetesTarMember(reader, header, input.offset, totalSize, len(members), buffer)
		if okErr != nil {
			return nil, okErr
		}
		if _, exists := members[header.Name]; exists {
			return nil, errKubernetesArchiveContent
		}
		members[header.Name] = member
	}
	if padErr := verifyTarTrailingPadding(input, totalSize); padErr != nil {
		return nil, padErr
	}
	return members, nil
}

func verifyTarTerminalBlocks(file *os.File, position, endingBytes int64, buffer []byte) error {
	if endingBytes < minTarTerminalBytes || endingBytes > maxTarTerminalBytes {
		return errKubernetesArchiveContent
	}
	ending := buffer[:endingBytes]
	if _, readErr := file.ReadAt(ending, position); readErr != nil {
		return errors.Join(errKubernetesArchiveContent, readErr)
	}
	for _, value := range ending {
		if value != 0 {
			return errKubernetesArchiveContent
		}
	}
	return nil
}

func verifyTarTrailingPadding(input *kubernetesArchiveReader, totalSize int64) error {
	trailer, readErr := io.ReadAll(io.LimitReader(input, maxTarTrailingPaddingBytes+1))
	if readErr != nil {
		return errors.Join(errKubernetesArchiveContent, readErr)
	}
	if len(trailer) > maxTarTrailingPaddingBytes || input.offset != totalSize {
		return errKubernetesArchiveContent
	}
	if !bytes.Equal(trailer, make([]byte, len(trailer))) {
		return errKubernetesArchiveContent
	}
	return nil
}

func readKubernetesTarMember(
	reader io.Reader,
	header *tar.Header,
	offset, totalSize int64,
	existingCount int,
	buffer []byte,
) (kubernetesArchiveMember, error) {
	if existingCount >= maxKubernetesArchiveEntries ||
		!kubernetesArchivePath(header.Name) ||
		header.Size < 0 ||
		header.Size > totalSize {
		return kubernetesArchiveMember{}, errKubernetesArchiveContent
	}
	member := kubernetesArchiveMember{offset: offset, size: header.Size}
	switch header.Typeflag {
	case tar.TypeReg:
		digest, hashErr := hashBoundedTarMember(reader, header.Size, buffer)
		if hashErr != nil {
			return kubernetesArchiveMember{}, hashErr
		}
		member.digest = digest
		return member, nil
	case tar.TypeSymlink:
		resolved, ok := resolveArchiveSymlinkTarget(header.Name, header.Linkname, header.Size)
		if !ok {
			return kubernetesArchiveMember{}, errKubernetesArchiveContent
		}
		member.link = resolved
		return member, nil
	default:
		return kubernetesArchiveMember{}, errKubernetesArchiveContent
	}
}

func hashBoundedTarMember(reader io.Reader, expectedSize int64, buffer []byte) (string, error) {
	digest := sha256.New()
	remaining := expectedSize
	for remaining > 0 {
		chunkLen := min(int64(len(buffer)), remaining)
		n, readErr := reader.Read(buffer[:chunkLen])
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
			remaining -= int64(n)
		}
		if errors.Is(readErr, io.EOF) && remaining == 0 {
			break
		}
		if readErr != nil {
			return "", errors.Join(errKubernetesArchiveContent, readErr)
		}
	}
	var extra [1]byte
	extraRead, extraErr := reader.Read(extra[:])
	if extraRead != 0 || !errors.Is(extraErr, io.EOF) {
		return "", errors.Join(errKubernetesArchiveContent, extraErr)
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

func resolveArchiveSymlinkTarget(memberName, linkTarget string, size int64) (string, bool) {
	if size != 0 || linkTarget == "" || path.IsAbs(linkTarget) || strings.ContainsRune(linkTarget, '\\') {
		return "", false
	}
	var segments []string
	parent := path.Dir(memberName)
	if parent != "." && parent != "" {
		segments = append(segments, strings.Split(parent, "/")...)
	}
	for part := range strings.SplitSeq(linkTarget, "/") {
		switch part {
		case "", ".":
			continue
		case archiveRelativeParentSegment:
			if len(segments) == 0 {
				return "", false
			}
			segments = segments[:len(segments)-1]
		default:
			segments = append(segments, part)
		}
	}
	if len(segments) == 0 {
		return "", false
	}
	resolved := strings.Join(segments, "/")
	if !kubernetesArchivePath(resolved) {
		return "", false
	}
	return resolved, true
}

func kubernetesArchivePath(name string) bool {
	return name != "" &&
		len(name) <= maxKubernetesArchivePathLen &&
		name != "." &&
		name != archiveRelativeParentSegment &&
		!path.IsAbs(name) &&
		path.Clean(name) == name &&
		!strings.HasPrefix(name, "../") &&
		!strings.ContainsRune(name, '\\')
}

func readKubernetesArchiveJSON(file *os.File, member kubernetesArchiveMember, limit int64) ([]byte, error) {
	if member.digest == "" || member.size <= 0 || member.size > limit {
		return nil, errKubernetesArchiveContent
	}
	data, readErr := io.ReadAll(io.NewSectionReader(file, member.offset, member.size))
	if readErr != nil {
		return nil, errors.Join(errKubernetesArchiveContent, readErr)
	}
	if int64(len(data)) != member.size || fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != member.digest {
		return nil, errKubernetesArchiveContent
	}
	if validateErr := strictjson.Validate(data); validateErr != nil {
		return nil, errors.Join(errKubernetesArchiveContent, validateErr)
	}
	return data, nil
}

func verifyKubernetesArchiveMembers(
	file *os.File,
	members map[string]kubernetesArchiveMember,
	image, expectedID string,
) (kubernetesArchiveBlob, []kubernetesArchiveBlob, error) {
	configPath, layers, manifestErr := parseKubernetesArchiveManifest(file, members["manifest.json"], image)
	if manifestErr != nil {
		return kubernetesArchiveBlob{}, nil, manifestErr
	}
	configMember := members[configPath]
	if configMember.digest != expectedID {
		return kubernetesArchiveBlob{}, nil, errKubernetesArchiveContent
	}
	diffIDs, configErr := parseKubernetesArchiveDiffIDs(file, configMember, len(layers))
	if configErr != nil {
		return kubernetesArchiveBlob{}, nil, configErr
	}
	layerBlobs, layoutErr := verifyKubernetesArchiveLayout(members, configPath, layers, diffIDs)
	if layoutErr != nil {
		return kubernetesArchiveBlob{}, nil, layoutErr
	}
	configBlob := kubernetesArchiveBlob{
		Offset: configMember.offset,
		Size:   configMember.size,
		Digest: configMember.digest,
	}
	return configBlob, layerBlobs, nil
}

func parseKubernetesArchiveManifest(
	file *os.File,
	manifestMember kubernetesArchiveMember,
	image string,
) (string, []string, error) {
	manifest, readErr := readKubernetesArchiveJSON(file, manifestMember, maxHTTPBodySize)
	if readErr != nil {
		return "", nil, readErr
	}
	var items []map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(manifest, &items); unmarshalErr != nil || len(items) != 1 {
		return "", nil, errKubernetesArchiveContent
	}
	item := items[0]
	for key := range item {
		switch key {
		case "Config", "RepoTags", "Layers", "Parent", "LayerSources":
		default:
			return "", nil, errKubernetesArchiveContent
		}
	}
	var configPath string
	var tags, layers []string
	if json.Unmarshal(item["Config"], &configPath) != nil ||
		json.Unmarshal(item["RepoTags"], &tags) != nil ||
		json.Unmarshal(item["Layers"], &layers) != nil ||
		len(tags) != 1 ||
		tags[0] != image ||
		layers == nil {
		return "", nil, errKubernetesArchiveContent
	}
	return configPath, layers, nil
}

func parseKubernetesArchiveDiffIDs(
	file *os.File,
	configMember kubernetesArchiveMember,
	layerCount int,
) ([]string, error) {
	config, readErr := readKubernetesArchiveJSON(file, configMember, maxKubernetesConfigBytes)
	if readErr != nil {
		return nil, readErr
	}
	var configuration map[string]json.RawMessage
	if json.Unmarshal(config, &configuration) != nil {
		return nil, errKubernetesArchiveContent
	}
	for key := range configuration {
		if key != "rootfs" && strings.EqualFold(key, "rootfs") {
			return nil, errKubernetesArchiveContent
		}
	}
	var rootfs map[string]json.RawMessage
	if json.Unmarshal(configuration["rootfs"], &rootfs) != nil || len(rootfs) != expectedRootfsFieldCount {
		return nil, errKubernetesArchiveContent
	}
	var kind string
	var diffIDs []string
	if json.Unmarshal(rootfs["type"], &kind) != nil ||
		kind != "layers" ||
		json.Unmarshal(rootfs["diff_ids"], &diffIDs) != nil ||
		diffIDs == nil ||
		len(diffIDs) != layerCount {
		return nil, errKubernetesArchiveContent
	}
	return diffIDs, nil
}

func verifyKubernetesArchiveLayout(
	members map[string]kubernetesArchiveMember,
	configPath string,
	layers, diffIDs []string,
) ([]kubernetesArchiveBlob, error) {
	allowed := map[string]bool{"manifest.json": true, "repositories": true, configPath: true}
	physicalLayers := make(map[string]bool, len(layers))
	layerBlobs := make([]kubernetesArchiveBlob, 0, len(layers))
	for index, name := range layers {
		member := members[name]
		if !containertest.IsImageDigest(diffIDs[index]) || member.digest != diffIDs[index] || member.size <= 0 {
			return nil, errKubernetesArchiveContent
		}
		allowed[name] = true
		physicalLayers[name] = true
		layerBlobs = append(layerBlobs, kubernetesArchiveBlob{
			Offset: member.offset,
			Size:   member.size,
			Digest: member.digest,
		})
	}
	for name, member := range members {
		if member.link == "" {
			continue
		}
		if path.Base(name) != "layer.tar" || !physicalLayers[member.link] || member.link == name {
			return nil, errKubernetesArchiveContent
		}
		allowed[name] = true
		allowed[path.Join(path.Dir(name), "VERSION")] = true
		allowed[path.Join(path.Dir(name), "json")] = true
	}
	for name, member := range members {
		if !allowed[name] || (member.link != "" && name == "repositories") {
			return nil, errKubernetesArchiveContent
		}
	}
	return layerBlobs, nil
}
