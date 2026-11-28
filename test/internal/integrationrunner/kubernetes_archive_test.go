package integrationrunner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

type kubernetesArchiveTestMember struct {
	name string
	data []byte
	link string
}

func kubernetesArchiveTestMembers(
	image string,
	labels map[string]string,
) ([]kubernetesArchiveTestMember, string, error) {
	var layer bytes.Buffer
	w := tar.NewWriter(&layer)
	if err := w.WriteHeader(&tar.Header{Name: "fixture", Mode: 0o644, Size: 5}); err != nil {
		return nil, "", fmt.Errorf("write test layer header: %w", err)
	}
	if _, err := w.Write([]byte("owned")); err != nil {
		return nil, "", fmt.Errorf("write test layer data: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("close test layer tar: %w", err)
	}
	layerID := fmt.Sprintf("sha256:%x", sha256.Sum256(layer.Bytes()))
	configObj := map[string]any{
		"os":           "linux",
		"architecture": "amd64",
		"config":       map[string]any{"Image": image, "Labels": labels},
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{layerID}},
	}
	config, cfgErr := json.Marshal(configObj)
	if cfgErr != nil {
		return nil, "", fmt.Errorf("encode test image config: %w", cfgErr)
	}
	id := fmt.Sprintf("sha256:%x", sha256.Sum256(config))
	configName := strings.TrimPrefix(id, "sha256:") + ".json"
	layerName := strings.TrimPrefix(layerID, "sha256:") + ".tar"
	manifestObj := []map[string]any{{
		"Config":   configName,
		"RepoTags": []string{image},
		"Layers":   []string{layerName},
	}}
	manifest, manifestErr := json.Marshal(manifestObj)
	if manifestErr != nil {
		return nil, "", fmt.Errorf("encode test image manifest: %w", manifestErr)
	}
	members := []kubernetesArchiveTestMember{
		{layerName, layer.Bytes(), ""},
		{configName, config, ""},
		{"legacy/layer.tar", nil, "../" + layerName},
		{"legacy/json", []byte(`{}`), ""},
		{"legacy/VERSION", []byte("1.0"), ""},
		{"manifest.json", manifest, ""},
		{"repositories", []byte(`{}`), ""},
	}
	return members, id, nil
}

func encodeKubernetesArchiveTestMembers(members []kubernetesArchiveTestMember) ([]byte, error) {
	var output bytes.Buffer
	w := tar.NewWriter(&output)
	for _, member := range members {
		header := tar.Header{
			Name:     member.name,
			Mode:     0o600,
			Size:     int64(len(member.data)),
			Typeflag: tar.TypeReg,
		}
		if member.link != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = member.link
		}
		if err := w.WriteHeader(&header); err != nil {
			return nil, fmt.Errorf("write archive test header: %w", err)
		}
		if _, err := w.Write(member.data); err != nil {
			return nil, fmt.Errorf("write archive test data: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close archive test writer: %w", err)
	}
	return output.Bytes(), nil
}

func kubernetesArchiveTestBytes(image string, labels map[string]string) ([]byte, string, error) {
	members, id, err := kubernetesArchiveTestMembers(image, labels)
	if err != nil {
		return nil, "", err
	}
	data, encErr := encodeKubernetesArchiveTestMembers(members)
	return data, id, encErr
}

func checkKubernetesArchiveTestEvidence(directory, image, id string, archive []byte) error {
	receiptName := "archive-" + strings.TrimPrefix(id, "sha256:") + ".json"
	data, _, err := interopproject.ReadPrivateReceipt(filepath.Join(directory, receiptName))
	if err != nil {
		return fmt.Errorf("read archive receipt: %w", err)
	}
	var evidence kubernetesArchiveEvidence
	if unmarshalErr := json.Unmarshal(data, &evidence); unmarshalErr != nil {
		return fmt.Errorf("decode archive receipt: %w", unmarshalErr)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	if evidence.Schema != 1 ||
		evidence.Image != image ||
		evidence.SourceID != id ||
		evidence.Bytes != int64(len(archive)) ||
		evidence.Digest != wantDigest {
		return errKubernetesArchiveContent
	}
	return nil
}

func TestKubernetesArchiveContent(t *testing.T) {
	t.Parallel()
	modes := []string{
		"healthy",
		"wrong config ID",
		"wrong config bytes",
		"wrong layer bytes",
		"wrong tag",
		"multiple images",
		"duplicate member",
		"duplicate JSON field",
		"alias JSON field",
		"alias rootfs field",
		"hybrid OCI",
		"unsafe member",
		"unsafe link",
		"symlink config",
		"missing layer",
		"missing manifest",
		"trailing payload",
		"truncated archive",
		"dangling GNU",
		"dangling PAX",
		"cancelled",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			runArchiveContentSubtest(t, mode)
		})
	}
}

func runArchiveContentSubtest(t *testing.T, mode string) {
	t.Helper()
	members, id, err := kubernetesArchiveTestMembers("localhost/fixture:owned", nil)
	if err != nil {
		t.Fatal(err)
	}
	members, id = mutateArchiveTestMembers(mode, members, id)
	data, encErr := encodeKubernetesArchiveTestMembers(members)
	if encErr != nil {
		t.Fatal(encErr)
	}
	data = mutateEncodedArchiveBytes(t, mode, data)
	file, createErr := os.Create(filepath.Join(t.TempDir(), "image.tar"))
	if createErr != nil {
		t.Fatal(createErr)
	}
	t.Cleanup(func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, writeErr := file.Write(data); writeErr != nil {
		t.Fatal(writeErr)
	}
	ctx := t.Context()
	if mode == "cancelled" {
		var cancel func()
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	evidence, verifyErr := verifyKubernetesArchive(ctx, file, "localhost/fixture:owned", id)
	if mode != "healthy" {
		if verifyErr == nil {
			t.Fatalf("accepted %s archive", mode)
		}
		if mode == "cancelled" && !errors.Is(verifyErr, context.Canceled) {
			t.Fatalf("cancellation lost: %v", verifyErr)
		}
		return
	}
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if evidence.SourceID != id || evidence.Bytes != int64(len(data)) || evidence.Digest != wantDigest {
		t.Fatalf("wrong archive receipt: %+v", evidence)
	}
}

func mutateArchiveTestMembers(
	mode string,
	members []kubernetesArchiveTestMember,
	id string,
) ([]kubernetesArchiveTestMember, string) {
	switch mode {
	case "wrong config ID":
		id = "sha256:" + strings.Repeat("a", 64)
	case "wrong config bytes":
		members[1].data = append(members[1].data, ' ')
	case "wrong layer bytes":
		members[0].data[0] ^= 1
	case "wrong tag":
		members[5].data = bytes.ReplaceAll(
			members[5].data, []byte("localhost/fixture:owned"), []byte("localhost/foreign:owned"),
		)
	case "multiple images":
		raw := members[5].data
		dup := append([]byte{'['}, raw[1:len(raw)-1]...)
		dup = append(dup, ',')
		dup = append(dup, raw[1:]...)
		members[5].data = dup
	case "duplicate member":
		members = append(members, members[0])
	case "duplicate JSON field":
		members[5].data = bytes.Replace(
			members[5].data, []byte(`"Config":`), []byte(`"Config":"foreign","Config":`), 1,
		)
	case "alias JSON field":
		members[5].data = bytes.Replace(members[5].data, []byte(`"Config":`), []byte(`"config":`), 1)
	case "alias rootfs field":
		members[1].data = bytes.Replace(
			members[1].data, []byte(`"rootfs":`), []byte(`"Rootfs":{},"rootfs":`), 1,
		)
		id = fmt.Sprintf("sha256:%x", sha256.Sum256(members[1].data))
	default:
		members = mutateArchiveStructure(mode, members)
	}
	return members, id
}

func mutateArchiveStructure(
	mode string,
	members []kubernetesArchiveTestMember,
) []kubernetesArchiveTestMember {
	switch mode {
	case "hybrid OCI":
		return append(members, kubernetesArchiveTestMember{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`), ""})
	case "unsafe member":
		return append(members, kubernetesArchiveTestMember{"../foreign", []byte("foreign"), ""})
	case "unsafe link":
		members[2].link = "../../foreign"
	case "symlink config":
		members[1].data = nil
		members[1].link = members[0].name
	case "missing layer":
		return members[1:]
	case "missing manifest":
		return members[:5]
	}
	return members
}

func mutateEncodedArchiveBytes(t *testing.T, mode string, data []byte) []byte {
	t.Helper()
	switch mode {
	case "trailing payload":
		return append(data, []byte("foreign")...)
	case "truncated archive":
		return data[:len(data)-1024]
	case "dangling GNU", "dangling PAX":
		var suffix bytes.Buffer
		writer := tar.NewWriter(&suffix)
		format := tar.FormatGNU
		if mode == "dangling PAX" {
			format = tar.FormatPAX
		}
		header := &tar.Header{Name: strings.Repeat("x", 200), Mode: 0o600, Format: format}
		if writeErr := writer.WriteHeader(header); writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr := writer.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		return append(data[:len(data)-1024], suffix.Bytes()[:1024]...)
	default:
		return data
	}
}

func FuzzKubernetesArchiveContent(f *testing.F) {
	const image = "localhost/fixture:owned"
	valid, id, err := kubernetesArchiveTestBytes(image, nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add(valid[:len(valid)-1024])
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		file, createErr := os.Create(filepath.Join(t.TempDir(), "image.tar"))
		if createErr != nil {
			t.Fatal(createErr)
		}
		t.Cleanup(func() {
			if closeErr := file.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
		if _, writeErr := file.Write(data); writeErr != nil {
			t.Fatal(writeErr)
		}
		evidence, verifyErr := verifyKubernetesArchive(t.Context(), file, image, id)
		if verifyErr != nil {
			if bytes.Equal(data, valid) {
				t.Fatalf("valid seed rejected: %v", verifyErr)
			}
			return
		}
		wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		if evidence.Schema != 1 ||
			evidence.Image != image ||
			evidence.SourceID != id ||
			evidence.Bytes != int64(len(data)) ||
			evidence.Digest != wantDigest {
			t.Fatalf("accepted archive evidence differs: %+v", evidence)
		}
	})
}
