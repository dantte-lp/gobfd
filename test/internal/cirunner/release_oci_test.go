package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestValidateReleaseOCIAttestationsChecksBothPlatforms(t *testing.T) {
	t.Parallel()

	evidence := validReleaseOCIEvidence(t)
	rawAttestation, spdx, provenance := validOCIPayloads(t)
	raw := make([][]byte, 0, releaseOCIImageCount*6)
	for range releaseOCIImageCount * 2 {
		raw = append(raw, rawAttestation, spdx, provenance)
	}
	runner := &ociManifestRunner{raw: raw}
	environment := []string{"GH_TOKEN=secret", "PATH=/usr/bin", "HOME=/home/runner", "DOCKER_CONFIG=/docker"}
	root := t.TempDir()
	if err := validateReleaseOCIAttestations(
		context.Background(), runner, root, evidence, environment,
	); err != nil {
		t.Fatalf("validateReleaseOCIAttestations() error = %v", err)
	}
	if len(runner.calls) != 18 {
		t.Fatalf("OCI payload command count = %d, want 18", len(runner.calls))
	}
	wantEnv := []string{"PATH=/usr/bin", "HOME=/home/runner", "DOCKER_CONFIG=/docker"}
	for index, call := range runner.calls {
		if !reflect.DeepEqual(call.env, wantEnv) || call.dir != root || call.name != "docker" {
			t.Errorf("OCI payload call %d = %#v", index, call)
		}
	}
	if got := runner.calls[0].args; len(got) != 5 || got[3] != "--raw" ||
		got[4] != evidence[0].Image+"@"+evidence[0].Attestations[evidence[0].Runnable["linux/amd64"]] {
		t.Errorf("amd64 raw attestation args = %q", got)
	}
	if got := runner.calls[1].args; !slices.Contains(got, `{{json (index .SBOM "linux/amd64").SPDX}}`) {
		t.Errorf("amd64 SPDX args = %q", got)
	}
	if got := runner.calls[17].args; !slices.Contains(got, `{{json (index .Provenance "linux/arm64").SLSA}}`) {
		t.Errorf("arm64 provenance args = %q", got)
	}
}

func TestValidateReleaseOCIAttestationsRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	evidence := validReleaseOCIEvidence(t)
	rawAttestation, spdx, _ := validOCIPayloads(t)
	for _, test := range []struct {
		name string
		raw  [][]byte
	}{
		{
			name: "missing SLSA layer",
			raw:  [][]byte{[]byte(`{"layers":[{"annotations":{"in-toto.io/predicate-type":"https://spdx.dev/Document"}}]}`)},
		},
		{
			name: "empty SPDX packages",
			raw: [][]byte{rawAttestation, []byte(
				`{"SPDXID":"SPDXRef-DOCUMENT","dataLicense":"CC0-1.0","spdxVersion":"SPDX-2.3","documentNamespace":"urn:test","packages":[]}`,
			)},
		},
		{
			name: "missing provenance builder id",
			raw: [][]byte{rawAttestation, spdx, []byte(
				`{"buildDefinition":{"buildType":"` + ociSLSABuildTypeV1 + `","resolvedDependencies":[{}]},"runDetails":{"builder":{},"metadata":{"invocationId":"id","startedOn":"start","finishedOn":"finish"}}}`,
			)},
		},
		{
			name: "null provenance builder id",
			raw: [][]byte{rawAttestation, spdx, []byte(
				`{"buildDefinition":{"buildType":"` + ociSLSABuildTypeV1 + `","resolvedDependencies":[{}]},"runDetails":{"builder":{"id":null},"metadata":{"invocationId":"id","startedOn":"start","finishedOn":"finish"}}}`,
			)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &ociManifestRunner{raw: test.raw}
			if err := validateReleaseOCIAttestations(
				context.Background(), runner, t.TempDir(), evidence, nil,
			); err == nil {
				t.Fatal("validateReleaseOCIAttestations() error = nil, want payload rejection")
			}
		})
	}
}

func TestReleaseOCIEvidenceWritesExactDigestReceipt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	receiptRoot := t.TempDir()
	runner := &ociManifestRunner{raw: validReleaseOCICommandOutputs(t, false)}
	if err := ReleaseOCIEvidence(context.Background(), ReleaseOCIEvidenceOptions{
		Root: root, ReceiptRoot: receiptRoot, RefName: "v0.6.5",
		Environment: []string{"PATH=/usr/bin"}, Runner: runner,
	}); err != nil {
		t.Fatalf("ReleaseOCIEvidence() error = %v", err)
	}
	want := "ghcr.io/dantte-lp/gobfd:0.6.5 " + testOCIDigest("1") + "\n" +
		"ghcr.io/dantte-lp/gobfd:0.6.5-debian-trixie " + testOCIDigest("1") + "\n" +
		"ghcr.io/dantte-lp/gobfd:0.6.5-oraclelinux10 " + testOCIDigest("3") + "\n"
	data, err := os.ReadFile(receiptRoot + "/release-image-digests.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("release image digest receipt = %q, want %q", data, want)
	}
	info, err := os.Stat(receiptRoot + "/release-image-digests.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != 0o644 {
		t.Errorf("release image digest receipt mode = %s, want -rw-r--r--", info.Mode())
	}
	if len(runner.calls) != 21 {
		t.Errorf("release OCI evidence command count = %d, want 21", len(runner.calls))
	}
	for index, call := range runner.calls {
		if call.dir != root {
			t.Errorf("release OCI evidence command %d directory = %q, want %q", index, call.dir, root)
		}
	}
}

func TestReleaseOCIEvidenceRejectsPrimaryDebianDigestMismatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	receiptRoot := t.TempDir()
	runner := &ociManifestRunner{raw: validReleaseOCICommandOutputs(t, true)}
	if err := ReleaseOCIEvidence(context.Background(), ReleaseOCIEvidenceOptions{
		Root: root, ReceiptRoot: receiptRoot, RefName: "v0.6.5", Runner: runner,
	}); err == nil {
		t.Fatal("ReleaseOCIEvidence() error = nil, want primary/Debian mismatch")
	}
	if _, err := os.Lstat(receiptRoot + "/release-image-digests.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed release image digest receipt error = %v, want not exist", err)
	}
}

func TestReleaseOCIEvidenceRejectsReplacedReceiptRoot(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	receiptRoot := filepath.Join(parent, "workspace")
	if err := os.Mkdir(receiptRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	delegate := &ociManifestRunner{raw: validReleaseOCICommandOutputs(t, false)}
	runner := &hookedSpecRunner{delegate: delegate, hook: func() error {
		if err := os.Rename(receiptRoot, receiptRoot+".replaced"); err != nil {
			return err
		}
		return os.Mkdir(receiptRoot, 0o755)
	}}
	if err := ReleaseOCIEvidence(context.Background(), ReleaseOCIEvidenceOptions{
		Root: t.TempDir(), ReceiptRoot: receiptRoot, RefName: "v0.6.5", Runner: runner,
	}); err == nil {
		t.Fatal("ReleaseOCIEvidence() error = nil, want replaced receipt root rejection")
	}
	if _, err := os.Lstat(receiptRoot + "/release-image-digests.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("replacement root receipt error = %v, want not exist", err)
	}
	if _, err := os.Lstat(receiptRoot + ".replaced/release-image-digests.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("opened root receipt error = %v, want not exist", err)
	}
}

func validReleaseOCICommandOutputs(t *testing.T, mismatch bool) [][]byte {
	t.Helper()
	primary := validOCIManifestIndex("1")
	debian := primary
	if mismatch {
		debian = validOCIManifestIndex("2")
	}
	manifests := []ociManifestIndex{primary, debian, validOCIManifestIndex("3")}
	outputs := make([][]byte, 0, 3+releaseOCIImageCount*6)
	for _, manifest := range manifests {
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, data)
	}
	rawAttestation, spdx, provenance := validOCIPayloads(t)
	for range releaseOCIImageCount * 2 {
		outputs = append(outputs, rawAttestation, spdx, provenance)
	}
	return outputs
}

func validReleaseOCIEvidence(t *testing.T) []releaseOCIImageEvidence {
	t.Helper()
	images := []string{
		"ghcr.io/dantte-lp/gobfd:0.6.5",
		"ghcr.io/dantte-lp/gobfd:0.6.5-debian-trixie",
		"ghcr.io/dantte-lp/gobfd:0.6.5-oraclelinux10",
	}
	evidence := make([]releaseOCIImageEvidence, 0, len(images))
	for index, image := range images {
		item, err := validateOCIManifestIndex(image, validOCIManifestIndex(fmt.Sprintf("%d", index+1)))
		if err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, item)
	}
	return evidence
}

func validOCIPayloads(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	values := []any{
		map[string]any{"layers": []any{
			map[string]any{"annotations": map[string]string{"in-toto.io/predicate-type": "https://spdx.dev/Document"}},
			map[string]any{"annotations": map[string]string{"in-toto.io/predicate-type": "https://slsa.dev/provenance/v1"}},
		}},
		map[string]any{
			"SPDXID": "SPDXRef-DOCUMENT", "dataLicense": "CC0-1.0", "spdxVersion": "SPDX-2.3",
			"documentNamespace": "urn:test", "packages": []any{map[string]any{"name": "gobfd"}},
		},
		map[string]any{
			"buildDefinition": map[string]any{
				"buildType":            ociSLSABuildTypeV1,
				"resolvedDependencies": []any{map[string]any{"uri": "pkg:golang/gobfd"}},
			},
			"runDetails": map[string]any{
				"builder": map[string]any{"id": ""},
				"metadata": map[string]any{
					"invocationId": "id", "startedOn": "start", "finishedOn": "finish",
				},
			},
		},
	}
	result := make([][]byte, len(values))
	for index, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result[index] = data
	}
	return result[0], result[1], result[2]
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

type hookedSpecRunner struct {
	delegate SpecRunner
	hook     func() error
}

func (runner *hookedSpecRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	if runner.hook != nil {
		hook := runner.hook
		runner.hook = nil
		if err := hook(); err != nil {
			return err
		}
	}
	return runner.delegate.RunCommand(ctx, spec)
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
