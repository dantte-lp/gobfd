package containertest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

type stubOwnedImageClient struct {
	images  map[string]podmanapi.ImageMetadata
	stages  []podmanapi.ImageMetadata
	removed []string
}

func (s *stubOwnedImageClient) ImageExists(_ context.Context, image string) (bool, error) {
	_, ok := s.images[image]
	return ok, nil
}

func (s *stubOwnedImageClient) ImageMetadata(_ context.Context, image string) (podmanapi.ImageMetadata, error) {
	meta, ok := s.images[image]
	if !ok {
		return podmanapi.ImageMetadata{}, errors.New("missing fixture image")
	}
	return meta, nil
}

func (s *stubOwnedImageClient) StageImages(
	_ context.Context, runID, testKind string,
) ([]podmanapi.ImageMetadata, error) {
	var out []podmanapi.ImageMetadata
	for _, stage := range s.stages {
		if _, alive := s.images[stage.ID]; !alive {
			continue
		}
		if stage.Labels["io.gobfd.stage-run"] == runID &&
			stage.Labels["io.gobfd.stage-test"] == testKind &&
			stage.Labels["io.gobfd.image"] == "" {
			out = append(out, stage)
		}
	}
	return out, nil
}

func (s *stubOwnedImageClient) RemoveImage(_ context.Context, image string) error {
	s.removed = append(s.removed, image)
	delete(s.images, image)
	return nil
}

func TestStageBuildArgsClonesBaseAndStampsOwnership(t *testing.T) {
	t.Parallel()
	rev := "abc123"
	base := map[string]*string{"VCS_REF": &rev}
	got := StageBuildArgs(base, "run-42", "interop")
	if base["GOBFD_IMAGE_RUN"] != nil {
		t.Fatal("StageBuildArgs mutated caller map")
	}
	if got["VCS_REF"] == nil || *got["VCS_REF"] != rev ||
		got["GOBFD_IMAGE_RUN"] == nil || *got["GOBFD_IMAGE_RUN"] != "run-42" ||
		got["GOBFD_IMAGE_TEST"] == nil || *got["GOBFD_IMAGE_TEST"] != "interop" {
		t.Fatalf("unexpected StageBuildArgs result: %+v", got)
	}
}

func TestRegisterImageCleanupRemovesFinalAndBuilderStageImages(t *testing.T) {
	t.Parallel()
	const imageName = "localhost/gobfd-test:owned"
	const runID = "run-99"
	const testKind = "unit-kind"
	finalID := "sha256:" + strings.Repeat("1", 64)
	stageID := "sha256:" + strings.Repeat("2", 64)
	foreignStageID := "sha256:" + strings.Repeat("3", 64)

	client := &stubOwnedImageClient{
		images: make(map[string]podmanapi.ImageMetadata),
	}
	var ownedID string
	var cleanupFn func()
	var reported []error
	err := RegisterImageCleanup(
		t.Context(),
		imageName,
		runID,
		testKind,
		&ownedID,
		client,
		func(fn func()) { cleanupFn = fn },
		func(e error) { reported = append(reported, e) },
	)
	if err != nil {
		t.Fatalf("RegisterImageCleanup() error = %v", err)
	}
	finalMeta := podmanapi.ImageMetadata{
		ID: finalID,
		Labels: map[string]string{
			"io.gobfd.image": imageName,
			"io.gobfd.run":   runID,
			"io.gobfd.test":  testKind,
		},
	}
	stageMeta := podmanapi.ImageMetadata{
		ID: stageID,
		Labels: map[string]string{
			"io.gobfd.stage-run":  runID,
			"io.gobfd.stage-test": testKind,
		},
	}
	foreignStageMeta := podmanapi.ImageMetadata{
		ID: foreignStageID,
		Labels: map[string]string{
			"io.gobfd.stage-run":  "other-run",
			"io.gobfd.stage-test": testKind,
		},
	}
	client.images[finalID] = finalMeta
	client.images[stageID] = stageMeta
	client.images[foreignStageID] = foreignStageMeta
	client.stages = []podmanapi.ImageMetadata{stageMeta, foreignStageMeta}
	ownedID = finalID

	cleanupFn()
	if len(reported) != 0 {
		t.Fatalf("unexpected cleanup errors: %v", reported)
	}
	if !slices.Equal(client.removed, []string{finalID, stageID}) {
		t.Fatalf("removed = %v, want [%s %s]", client.removed, finalID, stageID)
	}
	if _, preserved := client.images[foreignStageID]; !preserved {
		t.Fatal("foreign builder stage was removed")
	}
}
