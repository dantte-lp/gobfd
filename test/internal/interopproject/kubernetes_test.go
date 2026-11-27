package interopproject

import (
	"io"
	"path/filepath"
	"testing"
)

func TestKubernetesBuildProjectHasNoRuntimeContainers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c, err := NewProject(root, "gobfd-k8s-build", "kubernetes-build", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.required) != 0 || len(c.optional) != 0 ||
		c.composeFile != filepath.Join(root, "deployments/integrations/kubernetes/compose.build.yml") {
		t.Fatalf("unexpected Kubernetes build project: %+v", c)
	}
	if _, err := c.Image(t.Context(), "gobfd"); err == nil {
		t.Fatal("image lookup without held lifecycle accepted")
	}
}
