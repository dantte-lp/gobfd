package integrationrunner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	contentv1 "github.com/containerd/containerd/api/services/content/v1"
	imagesv1 "github.com/containerd/containerd/api/services/images/v1"
	leasesv1 "github.com/containerd/containerd/api/services/leases/v1"
	containerdtypes "github.com/containerd/containerd/api/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	ociManifestSchemaVersion  = 2
	ociManifestMediaType      = "application/vnd.oci.image.manifest.v1+json"
	ociConfigMediaType        = "application/vnd.oci.image.config.v1+json"
	ociLayerUncompressedMedia = "application/vnd.oci.image.layer.v1.tar"
	containerdLeaseExpire     = 15 * time.Minute
	containerdWriteChunkBytes = 256 << 10
)

type ociManifestDescriptor struct {
	MediaType string `json:"mediaType"` //nolint:tagliatelle // OCI image-spec v1 field.
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type ociImageManifest struct {
	SchemaVersion int                     `json:"schemaVersion"` //nolint:tagliatelle // OCI image-spec v1 field.
	MediaType     string                  `json:"mediaType"`     //nolint:tagliatelle // OCI image-spec v1 field.
	Config        ociManifestDescriptor   `json:"config"`
	Layers        []ociManifestDescriptor `json:"layers"`
}

func (store *kubernetesContainerdStore) importArchive(
	ctx context.Context,
	file *os.File,
	alias string,
	archive verifiedKubernetesArchive,
) (_ string, retErr error) {
	if store == nil || store.connection == nil || alias == "" || len(archive.Layers) == 0 {
		return "", errKubernetesContainerdStore
	}
	leaseID, createErr := store.createLease(ctx)
	if createErr != nil {
		return "", createErr
	}
	defer func() {
		retErr = errors.Join(retErr, store.deleteLease(ctx, leaseID))
	}()
	leasedCtx := metadata.AppendToOutgoingContext(
		ctx,
		"containerd-namespace", kubernetesContainerdNamespace,
		"containerd-lease", leaseID,
	)
	if ingestErr := store.ingestArchiveBlobs(leasedCtx, file, archive); ingestErr != nil {
		return "", ingestErr
	}
	manifestBytes, manifestDigest, gcLabels, manifestErr := buildKubernetesOCIManifest(archive)
	if manifestErr != nil {
		return "", manifestErr
	}
	manifestSize := int64(len(manifestBytes))
	manifestRef := "manifest-" + strings.ToLower(rand.Text())
	if writeErr := store.writeContentBlob(
		leasedCtx, manifestRef, bytes.NewReader(manifestBytes), manifestSize, manifestDigest, gcLabels,
	); writeErr != nil {
		return "", fmt.Errorf("ingest containerd image manifest for %q: %w", alias, writeErr)
	}
	if pubErr := store.publishCreateOnlyAlias(leasedCtx, alias, manifestDigest, manifestSize, gcLabels); pubErr != nil {
		return "", pubErr
	}
	return manifestDigest, nil
}

func (store *kubernetesContainerdStore) createLease(ctx context.Context) (string, error) {
	leaseID := "gobfd-k8s-import-" + strings.ToLower(rand.Text())
	expireAt := time.Now().UTC().Add(containerdLeaseExpire).Format(time.RFC3339)
	rpcCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	namespaced := metadata.AppendToOutgoingContext(rpcCtx, "containerd-namespace", kubernetesContainerdNamespace)
	resp, err := store.leases.Create(namespaced, &leasesv1.CreateRequest{
		ID:     leaseID,
		Labels: map[string]string{"containerd.io/gc.expire": expireAt},
	})
	if err != nil {
		return "", fmt.Errorf("create containerd import lease: %w", err)
	}
	if resp.GetLease() == nil || resp.GetLease().GetID() != leaseID {
		return "", fmt.Errorf("containerd returned unexpected lease identity: %w", errKubernetesContainerdStore)
	}
	return leaseID, nil
}

func (store *kubernetesContainerdStore) deleteLease(ctx context.Context, leaseID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	namespaced := metadata.AppendToOutgoingContext(cleanupCtx, "containerd-namespace", kubernetesContainerdNamespace)
	if _, err := store.leases.Delete(namespaced, &leasesv1.DeleteRequest{ID: leaseID}); err != nil {
		return fmt.Errorf("delete containerd import lease %q: %w", leaseID, err)
	}
	return nil
}

func (store *kubernetesContainerdStore) ingestArchiveBlobs(
	leasedCtx context.Context,
	file *os.File,
	archive verifiedKubernetesArchive,
) error {
	nonce := strings.ToLower(rand.Text())
	for index, layer := range archive.Layers {
		ref := fmt.Sprintf("layer-%s-%d", nonce, index)
		section := io.NewSectionReader(file, layer.Offset, layer.Size)
		if err := store.writeContentBlob(leasedCtx, ref, section, layer.Size, layer.Digest, nil); err != nil {
			return fmt.Errorf("ingest containerd layer %d (%s): %w", index, layer.Digest, err)
		}
	}
	configSection := io.NewSectionReader(file, archive.Config.Offset, archive.Config.Size)
	configRef := "config-" + nonce
	if err := store.writeContentBlob(
		leasedCtx, configRef, configSection, archive.Config.Size, archive.Config.Digest, nil,
	); err != nil {
		return fmt.Errorf("ingest containerd image config (%s): %w", archive.Config.Digest, err)
	}
	return nil
}

func buildKubernetesOCIManifest(
	archive verifiedKubernetesArchive,
) ([]byte, string, map[string]string, error) {
	descriptors := make([]ociManifestDescriptor, 0, len(archive.Layers))
	gcLabels := make(map[string]string, len(archive.Layers)+1)
	gcLabels["containerd.io/gc.ref.content.config"] = archive.Config.Digest
	for index, layer := range archive.Layers {
		descriptors = append(descriptors, ociManifestDescriptor{
			MediaType: ociLayerUncompressedMedia,
			Digest:    layer.Digest,
			Size:      layer.Size,
		})
		gcLabels[fmt.Sprintf("containerd.io/gc.ref.content.%d", index)] = layer.Digest
	}
	manifest := ociImageManifest{
		SchemaVersion: ociManifestSchemaVersion,
		MediaType:     ociManifestMediaType,
		Config: ociManifestDescriptor{
			MediaType: ociConfigMediaType,
			Digest:    archive.Config.Digest,
			Size:      archive.Config.Size,
		},
		Layers: descriptors,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", nil, fmt.Errorf("encode containerd OCI manifest: %w", err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	return data, digest, gcLabels, nil
}

func (store *kubernetesContainerdStore) writeContentBlob(
	leasedCtx context.Context,
	ref string,
	reader io.Reader,
	expectedSize int64,
	expectedDigest string,
	labels map[string]string,
) error {
	writeCtx, cancel := context.WithTimeout(leasedCtx, buildTimeout)
	defer cancel()
	stream, err := store.content.Write(writeCtx)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return store.verifyExistingContent(writeCtx, expectedDigest, expectedSize, labels)
		}
		return fmt.Errorf("open containerd content write stream: %w", err)
	}
	streamErr := streamContentChunks(stream, ref, reader, expectedSize, expectedDigest, labels)
	closeErr := stream.CloseSend()
	if status.Code(streamErr) == codes.AlreadyExists {
		return store.verifyExistingContent(writeCtx, expectedDigest, expectedSize, labels)
	}
	if streamErr != nil {
		return streamErr
	}
	if closeErr != nil {
		return fmt.Errorf("close containerd content write stream: %w", closeErr)
	}
	return nil
}

func streamContentChunks(
	stream contentv1.Content_WriteClient,
	ref string,
	reader io.Reader,
	expectedSize int64,
	expectedDigest string,
	labels map[string]string,
) error {
	buffer := make([]byte, containerdWriteChunkBytes)
	var offset int64
	first := true
	for offset < expectedSize {
		n, readErr := reader.Read(buffer[:min(int64(len(buffer)), expectedSize-offset)])
		if n > 0 {
			reqRef := ""
			if first {
				reqRef = ref
				first = false
			}
			if sendErr := sendWriteAction(
				stream, contentv1.WriteAction_WRITE, reqRef, expectedSize, expectedDigest, offset, buffer[:n], nil,
			); sendErr != nil {
				return sendErr
			}
			offset += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read content blob %s: %w", expectedDigest, readErr)
		}
	}
	if offset != expectedSize {
		return fmt.Errorf(
			"short read for %s (%d != %d): %w",
			expectedDigest, offset, expectedSize, errKubernetesContentMismatch,
		)
	}
	commitRef := ""
	if first {
		commitRef = ref
	}
	return commitContentWrite(stream, commitRef, expectedSize, expectedDigest, labels)
}

func sendWriteAction(
	stream contentv1.Content_WriteClient,
	action contentv1.WriteAction,
	ref string,
	total int64,
	expected string,
	offset int64,
	data []byte,
	labels map[string]string,
) error {
	sendErr := stream.Send(&contentv1.WriteContentRequest{
		Action:   action,
		Ref:      ref,
		Total:    total,
		Expected: expected,
		Offset:   offset,
		Data:     data,
		Labels:   labels,
	})
	resp, recvErr := stream.Recv()
	if recvErr != nil {
		return fmt.Errorf("receive containerd content write acknowledgement: %w", recvErr)
	}
	if sendErr != nil {
		return fmt.Errorf("send containerd content chunk: %w", sendErr)
	}
	if action == contentv1.WriteAction_COMMIT {
		if resp.GetDigest() != expected || resp.GetOffset() != total {
			return fmt.Errorf(
				"committed containerd content (%s, %d) differs from (%s, %d): %w",
				resp.GetDigest(), resp.GetOffset(), expected, total, errKubernetesContentMismatch,
			)
		}
	}
	return nil
}

func commitContentWrite(
	stream contentv1.Content_WriteClient,
	ref string,
	expectedSize int64,
	expectedDigest string,
	labels map[string]string,
) error {
	return sendWriteAction(
		stream,
		contentv1.WriteAction_COMMIT,
		ref,
		expectedSize,
		expectedDigest,
		expectedSize,
		nil,
		labels,
	)
}

func (store *kubernetesContainerdStore) verifyExistingContent(
	leasedCtx context.Context,
	expectedDigest string,
	expectedSize int64,
	labels map[string]string,
) error {
	infoResp, err := store.content.Info(leasedCtx, &contentv1.InfoRequest{Digest: expectedDigest})
	if err != nil {
		return fmt.Errorf("inspect existing containerd content %s: %w", expectedDigest, err)
	}
	info := infoResp.GetInfo()
	if info == nil || info.GetDigest() != expectedDigest || info.GetSize() != expectedSize {
		return fmt.Errorf(
			"existing containerd content %s failed verification: %w",
			expectedDigest, errKubernetesContentMismatch,
		)
	}
	if len(labels) == 0 {
		return nil
	}
	paths := make([]string, 0, len(labels))
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		paths = append(paths, "labels."+key)
	}
	updated, updateErr := store.content.Update(leasedCtx, &contentv1.UpdateRequest{
		Info:       &contentv1.Info{Digest: expectedDigest, Labels: maps.Clone(labels)},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	})
	if updateErr != nil {
		return fmt.Errorf("apply GC labels to existing containerd content %s: %w", expectedDigest, updateErr)
	}
	return verifyContentInfoLabels(updated.GetInfo(), expectedDigest, expectedSize, labels)
}

func (store *kubernetesContainerdStore) publishCreateOnlyAlias(
	leasedCtx context.Context,
	alias, manifestDigest string,
	manifestSize int64,
	gcLabels map[string]string,
) error {
	rpcCtx, cancel := context.WithTimeout(leasedCtx, httpTimeout)
	defer cancel()
	created, createErr := store.images.Create(rpcCtx, &imagesv1.CreateImageRequest{
		Image: &imagesv1.Image{
			Name: alias,
			Target: &containerdtypes.Descriptor{
				MediaType: ociManifestMediaType,
				Digest:    manifestDigest,
				Size:      manifestSize,
			},
		},
	})
	if status.Code(createErr) == codes.AlreadyExists {
		return fmt.Errorf("publish containerd image alias %q: %w", alias, errKubernetesImageAliasExists)
	}
	if createErr != nil {
		return fmt.Errorf("create containerd image alias %q: %w", alias, createErr)
	}
	if verifyErr := verifyPublishedImageDescriptor(
		created.GetImage(), alias, manifestDigest, manifestSize,
	); verifyErr != nil {
		return verifyErr
	}
	readback, getErr := store.images.Get(rpcCtx, &imagesv1.GetImageRequest{Name: alias})
	if getErr != nil {
		return fmt.Errorf("read back created containerd image alias %q: %w", alias, getErr)
	}
	if verifyErr := verifyPublishedImageDescriptor(
		readback.GetImage(), alias, manifestDigest, manifestSize,
	); verifyErr != nil {
		return verifyErr
	}
	infoResp, infoErr := store.content.Info(rpcCtx, &contentv1.InfoRequest{Digest: manifestDigest})
	if infoErr != nil {
		return fmt.Errorf("verify containerd manifest content %s: %w", manifestDigest, infoErr)
	}
	return verifyContentInfoLabels(infoResp.GetInfo(), manifestDigest, manifestSize, gcLabels)
}

func verifyPublishedImageDescriptor(
	image *imagesv1.Image,
	alias, manifestDigest string,
	manifestSize int64,
) error {
	if image == nil || image.GetName() != alias || image.GetTarget() == nil {
		return fmt.Errorf("containerd image readback for %q is incomplete: %w", alias, errKubernetesContainerdStore)
	}
	target := image.GetTarget()
	if target.GetDigest() != manifestDigest ||
		target.GetSize() != manifestSize ||
		target.GetMediaType() != ociManifestMediaType {
		return fmt.Errorf("containerd image %q target descriptor differs: %w", alias, errKubernetesContentMismatch)
	}
	return nil
}

func verifyContentInfoLabels(
	info *contentv1.Info,
	expectedDigest string,
	expectedSize int64,
	labels map[string]string,
) error {
	if info == nil || info.GetDigest() != expectedDigest || info.GetSize() != expectedSize {
		return fmt.Errorf("containerd content readback %s differs: %w", expectedDigest, errKubernetesContentMismatch)
	}
	actualLabels := info.GetLabels()
	for key, want := range labels {
		if actualLabels[key] != want {
			return fmt.Errorf("containerd content %s missing GC label %q: %w", expectedDigest, key, errKubernetesContentMismatch)
		}
	}
	return nil
}
