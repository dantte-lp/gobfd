package interopproject

import (
	"errors"
	"strings"
	"testing"
)

func TestRecoverImagePlanPreservesValidatedIdentity(t *testing.T) {
	t.Parallel()
	const alias = "localhost/owned:test"
	id := "sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name, storedID, storedAlias, run string
		recovered, rejected              bool
	}{
		{name: "checkpoint not published", storedAlias: alias, run: "owned", recovered: true},
		{name: "checkpoint already published", storedID: id, storedAlias: alias, run: "owned"},
		{
			name:        "foreign ID",
			storedID:    "sha256:" + strings.Repeat("b", 64),
			storedAlias: alias,
			run:         "owned",
			rejected:    true,
		},
		{name: "foreign alias", storedAlias: "localhost/foreign:test", run: "owned", rejected: true},
		{name: "foreign run", storedAlias: alias, run: "foreign", rejected: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c := Controller{
				images: &composeImageState{
					Owned: composeImageOwner{
						Run:    "owned",
						Images: map[string]composeOwnedImage{"service": {Alias: alias, ID: id}},
					},
				},
			}
			stored := composeImageState{
				Services: map[string]composeOwnedService{
					"service": {Image: alias, PullPolicy: composePullPolicyNever},
				},
				Owned: composeImageOwner{
					Run: test.run,
					Images: map[string]composeOwnedImage{
						"service": {Alias: test.storedAlias, ID: test.storedID},
					},
				},
			}
			recovered, err := c.recoverImagePlan(&stored)
			if errors.Is(err, ErrUnsafeReceipt) != test.rejected ||
				(err != nil) != test.rejected ||
				recovered != test.recovered {
				t.Fatalf(
					"recovered=%t error=%v, want recovered=%t rejection=%t",
					recovered, err, test.recovered, test.rejected,
				)
			}
			if test.recovered && (stored.Owned.Images["service"].ID != id || stored.Services["service"].Image != id) {
				t.Fatal("recovery lost validated ID before the required persistence checkpoint")
			}
		})
	}
}

func TestComposeBuildArgsIncludesStageOwnershipArgs(t *testing.T) {
	t.Parallel()
	args, err := composeBuildArgs(
		[]byte(`{"context":"/tmp/ctx","dockerfile":"Containerfile"}`),
		"localhost/gobfd-interop-proj-svc:runid",
		strings.Repeat("a", 40),
		"2026-11-27T00:00:00Z",
		"runid",
	)
	if err != nil {
		t.Fatalf("composeBuildArgs() error = %v", err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"--build-arg GOBFD_IMAGE_RUN=runid",
		"--build-arg GOBFD_IMAGE_TEST=interop-compose",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("composeBuildArgs() = %q, missing %q", joined, expected)
		}
	}
}
