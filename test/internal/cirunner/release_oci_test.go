package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestInspectReleaseOCIManifestsValidatesThreeVersionedImages(t *testing.T) {
	t.Parallel()

	runner := &ociManifestRunner{manifests: []ociManifestIndex{
		validOCIManifestIndex("1"), validOCIManifestIndex("2"), validOCIManifestIndex("3"),
	}}
	root := t.TempDir()
	environment := []string{
		"GH_TOKEN=release-secret", "PATH=/usr/bin", "HOME=/home/runner",
		"GITHUB_TOKEN=other-secret", "DOCKER_CONFIG=/docker-config",
	}
	evidence, err := inspectReleaseOCIManifests(context.Background(), runner, root, "v0.6.5", environment)
	if err != nil {
		t.Fatalf("inspectReleaseOCIManifests() error = %v", err)
	}
	wantImages := []string{
		"ghcr.io/dantte-lp/gobfd:0.6.5",
		"ghcr.io/dantte-lp/gobfd:0.6.5-debian-trixie",
		"ghcr.io/dantte-lp/gobfd:0.6.5-oraclelinux10",
	}
	if len(evidence) != len(wantImages) || len(runner.calls) != len(wantImages) {
		t.Fatalf("evidence/calls = %d/%d, want %d", len(evidence), len(runner.calls), len(wantImages))
	}
	for index, image := range wantImages {
		if evidence[index].Image != image || evidence[index].Digest != testOCIDigest(string(rune('1'+index))) {
			t.Errorf("evidence[%d] = %#v", index, evidence[index])
		}
		wantCall := specInvocation{
			name: "docker", args: []string{
				"buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}", image,
			}, dir: root, env: []string{"PATH=/usr/bin", "HOME=/home/runner", "DOCKER_CONFIG=/docker-config"},
		}
		if !reflect.DeepEqual(runner.calls[index], wantCall) {
			t.Errorf("call[%d] = %#v, want %#v", index, runner.calls[index], wantCall)
		}
	}
}

func TestInspectReleaseOCIManifestsRejectsInvalidIndex(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*ociManifestIndex)
	}{
		{name: "duplicate runnable platform", mutate: func(index *ociManifestIndex) {
			index.Manifests[1].Platform.Architecture = "amd64"
		}},
		{name: "mismatched attestation subject", mutate: func(index *ociManifestIndex) {
			index.Manifests[3].Annotations[ociReferenceDigestAnnotation] = testOCIDigest("9")
		}},
		{name: "noncanonical top digest", mutate: func(index *ociManifestIndex) {
			index.Digest = "sha256:ABC"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validOCIManifestIndex("1")
			test.mutate(&manifest)
			runner := &ociManifestRunner{manifests: []ociManifestIndex{manifest}}
			if _, err := inspectReleaseOCIManifests(context.Background(), runner, t.TempDir(), "v0.6.5", nil); err == nil {
				t.Fatal("inspectReleaseOCIManifests() error = nil, want exact index rejection")
			}
			if len(runner.calls) != 1 {
				t.Errorf("commands after first invalid index = %d, want 1", len(runner.calls))
			}
		})
	}
}

func TestInspectReleaseOCIManifestsRejectsRawJSONHazards(t *testing.T) {
	t.Parallel()

	valid, err := json.Marshal(validOCIManifestIndex("1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{
			name: "duplicate member",
			data: bytes.Replace(valid, []byte(`{"digest":`), []byte(`{"digest":"`+testOCIDigest("other")+`","digest":`), 1),
		},
		{
			name: "case alias",
			data: bytes.Replace(valid, []byte(`"digest":`), []byte(`"Digest":`), 1),
		},
		{
			name: "null reserved annotation",
			data: bytes.Replace(
				valid, []byte(`"annotations":null`),
				[]byte(`"annotations":{"vnd.docker.reference.type":null}`), 1,
			),
		},
		{
			name: "invalid UTF-8",
			data: bytes.Replace(valid, []byte(`"linux"`), []byte{'"', 0xff, '"'}, 1),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &ociManifestRunner{raw: [][]byte{test.data}}
			if _, err := inspectReleaseOCIManifests(
				context.Background(), runner, t.TempDir(), "v0.6.5", nil,
			); err == nil {
				t.Fatal("inspectReleaseOCIManifests() error = nil, want raw JSON rejection")
			}
		})
	}
}

func validOCIManifestIndex(marker string) ociManifestIndex {
	amd64Digest := testOCIDigest(marker + "a")
	arm64Digest := testOCIDigest(marker + "b")
	return ociManifestIndex{
		Digest: testOCIDigest(marker),
		Manifests: []ociManifestDescriptor{
			{Digest: amd64Digest, Platform: ociPlatform{OS: "linux", Architecture: "amd64"}},
			{Digest: arm64Digest, Platform: ociPlatform{OS: "linux", Architecture: "arm64"}},
			{
				Digest:   testOCIDigest(marker + "c"),
				Platform: ociPlatform{OS: "unknown", Architecture: "unknown"},
				Annotations: map[string]string{
					ociReferenceTypeAnnotation: "attestation-manifest", ociReferenceDigestAnnotation: amd64Digest,
				},
			},
			{
				Digest:   testOCIDigest(marker + "d"),
				Platform: ociPlatform{OS: "unknown", Architecture: "unknown"},
				Annotations: map[string]string{
					ociReferenceTypeAnnotation: "attestation-manifest", ociReferenceDigestAnnotation: arm64Digest,
				},
			},
		},
	}
}

func testOCIDigest(marker string) string {
	digest := sha256.Sum256([]byte(marker))
	return "sha256:" + fmt.Sprintf("%x", digest)
}

type ociManifestRunner struct {
	manifests []ociManifestIndex
	raw       [][]byte
	calls     []specInvocation
}

func (runner *ociManifestRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, specInvocation{
		name: spec.Name, args: append([]string(nil), spec.Args...), dir: spec.Dir,
		env: append([]string(nil), spec.Env...),
	})
	callIndex := len(runner.calls) - 1
	if callIndex < len(runner.raw) {
		_, err := spec.Stdout.Write(runner.raw[callIndex])
		return err
	}
	if callIndex >= len(runner.manifests) {
		return errors.New("unexpected OCI manifest command")
	}
	data, err := json.Marshal(runner.manifests[callIndex])
	if err != nil {
		return err
	}
	if spec.Stdout == nil {
		return errors.New("OCI manifest stdout is nil")
	}
	_, err = io.Writer(spec.Stdout).Write(data)
	return err
}
