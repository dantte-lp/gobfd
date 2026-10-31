package dependencyinventory

import (
	"context"
	"strings"
	"testing"
)

func TestSelectedModuleGraphUsesReplacementArtifactIdentity(t *testing.T) {
	t.Parallel()

	data := []byte(`{"Path":"example.invalid/main","Main":true}
{"Path":"example.invalid/old","Version":"v1.0.0","Indirect":true,"Replace":{"Path":"example.invalid/new/v2",
	"Version":"v2.1.0"}}
{"Path":"example.invalid/same","Version":"v1.0.0","Replace":{"Path":"example.invalid/same","Version":"v1.2.0"}}`)
	modules, err := parseSelectedModuleGraph(data, "tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 || modules[0].Path != "example.invalid/new/v2" || modules[0].Version != "v2.1.0" ||
		!modules[0].Indirect || modules[1].Path != "example.invalid/same" || modules[1].Version != "v1.2.0" {
		t.Fatalf("replacement artifact identities = %#v", modules)
	}
}

func TestSelectedModuleGraphRejectsIncompleteReplacement(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		`{"Path":"example.invalid/old","Version":"v1.0.0","Replace":{"Version":"v2.1.0"}}`,
		`{"Path":"example.invalid/old","Replace":{"Path":"example.invalid/new","Version":"v2.1.0"}}`,
		`{"Path":"example.invalid/old","Version":"v1.0.0"} invalid`,
	} {
		if _, err := parseSelectedModuleGraph([]byte(data), "tools"); err == nil {
			t.Fatalf("incomplete selected graph accepted: %s", data)
		}
	}
}

func TestSyftSelectedGraphKeepsQualifiedSourceBoundaries(t *testing.T) {
	t.Parallel()
	graph, ok := moduleGraphFiles("syft")
	if !ok {
		t.Fatal("isolated Syft graph missing")
	}
	modules, err := selectedModuleGraph(context.Background(), repositoryRoot(t), graph)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]string{
		"github.com/anchore/syft":             "v1.55.0",
		"golang.org/x/net":                    "v0.62.0",
		"github.com/go-jose/go-jose/v4":       "v4.1.7",
		"github.com/containerd/containerd/v2": "v2.4.2",
		"github.com/sigstore/fulcio":          "v1.9.1",
		"github.com/yuin/goldmark":            "v1.8.7",
		"github.com/cilium/ebpf":              "v0.22.0",
		"go.etcd.io/etcd/client/pkg/v3":       "v3.7.3",
		"golang.org/x/image":                  "v0.47.0",
		"github.com/chromedp/chromedp":        "v0.21.0",
		"github.com/ledongthuc/pdf":           "v0.0.0-20261020125853-5e46805f718e",
		"github.com/docker/distribution":      "v2.8.4-0.20260923142519-db037680f2ac+incompatible",
	}
	for _, module := range modules {
		for _, excluded := range []string{
			"rsc.io/pdf", "go.step.sm/crypto", "github.com/go-jose/go-jose/v3",
			"github.com/chromedp/chromedp/test",
		} {
			if module.Path == excluded || strings.HasPrefix(module.Path, excluded+"/") {
				t.Fatalf("unqualified old source identity retained: %s@%s", module.Path, module.Version)
			}
		}
		if version, ok := required[module.Path]; ok {
			if module.Version != version {
				t.Errorf("%s effective version=%s, want qualified %s", module.Path, module.Version, version)
			}
			delete(required, module.Path)
		}
	}
	if len(required) != 0 {
		t.Fatalf("qualified source identities missing: %v", required)
	}
}
