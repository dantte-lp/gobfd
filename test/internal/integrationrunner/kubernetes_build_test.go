package integrationrunner

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func kubernetesBuildRoot(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../..", kubernetesManifestParent, "daemonset.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if mkdirErr := os.MkdirAll(filepath.Join(root, kubernetesManifestParent), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	dsPath := filepath.Join(root, kubernetesManifestParent, "daemonset.yml")
	if writeErr := os.WriteFile(dsPath, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	return root
}

func TestKubernetesOwnedBuildAndApply(t *testing.T) {
	modes := []string{
		"healthy",
		"second build failure",
		"import failure",
		"apply failure",
		"cleanup failure",
		"replaced source alias",
		"existing containerd alias",
		"create-only alias race",
		"invalid template",
		"foreign namespace",
		"empty nodes",
		"unready nodes",
		"node query failure",
		"cluster changes before namespace",
		"configz query failure",
		"invalid runtime endpoint",
		"invalid image endpoint",
		"malformed configz",
		"distinct image endpoint",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			runKubernetesBuildAndApplyMode(t, mode)
		})
	}
}

func runKubernetesBuildAndApplyMode(t *testing.T, mode string) {
	t.Helper()
	root, fixture, bin, runtimeDir := kubernetesBuildRoot(t), t.TempDir(), t.TempDir(), t.TempDir()
	if chmodErr := os.Chmod(runtimeDir, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	runtimeSocket, imageSocket := setupBuildSockets(t, runtimeDir, mode)
	containerdFixture := setupBuildContainerdFixture(fixture, root, mode)
	startKubernetesContainerdFixture(t, runtimeSocket, containerdFixture)
	installBuildCommandFixtures(t, bin)
	configureBuildEnvironment(t, bin, fixture, root, runtimeDir, runtimeSocket, imageSocket, mode)
	prepareBuildModeInputs(t, root, fixture, mode)

	r := runner{
		root:   root,
		stdout: io.Discard,
		stderr: io.Discard,
		hostIdentity: func() (string, string, error) {
			return "machine-id", "boot-id", nil
		},
	}
	runErr := r.kubernetesBuildAndApply(t.Context())
	assertBuildAndApplyOutcome(t, mode, root, fixture, runErr, containerdFixture)
}

func setupBuildSockets(t *testing.T, runtimeDir, mode string) (string, string) {
	t.Helper()
	runtimeSocket := filepath.Join(runtimeDir, "containerd.sock")
	imageSocket := runtimeSocket
	if mode == "distinct image endpoint" {
		imageSocket = filepath.Join(runtimeDir, "containerd-image.sock")
		if err := os.Symlink(runtimeSocket, imageSocket); err != nil {
			t.Fatal(err)
		}
	}
	return runtimeSocket, imageSocket
}

func setupBuildContainerdFixture(fixture, root, mode string) *kubernetesContainerdFixture {
	cf := newKubernetesContainerdFixture()
	cf.existingAll = mode == "existing containerd alias"
	cf.imageCreateRace = mode == "create-only alias race"
	cf.onImageCreate = func(alias string) error {
		if preErr := verifyPreImportBuildReceipts(fixture, root, mode); preErr != nil {
			return status.Error(codes.Internal, preErr.Error())
		}
		if mode == "import failure" {
			return status.Error(codes.Unavailable, "injected containerd import failure")
		}
		images, err := readKubernetesFixtureImages(fixture)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		image, exists := lookupKubernetesFixtureImage(images, alias)
		if !exists {
			return status.Error(codes.Internal, "unknown imported image alias")
		}
		marker := filepath.Join(fixture, "imported-"+strings.TrimPrefix(image.ID, "sha256:"))
		if writeErr := os.WriteFile(marker, []byte(alias), 0o600); writeErr != nil {
			return status.Error(codes.Internal, writeErr.Error())
		}
		return nil
	}
	return cf
}

func installBuildCommandFixtures(t *testing.T, bin string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"podman", "kubectl"} {
		if symErr := os.Symlink(executable, filepath.Join(bin, name)); symErr != nil {
			t.Fatal(symErr)
		}
	}
}

func configureBuildEnvironment(
	t *testing.T,
	bin, fixture, root, runtimeDir, runtimeSocket, imageSocket, mode string,
) {
	t.Helper()
	env := map[string]string{
		"PATH":                       bin,
		"GOBFD_K8S_BUILD_FIXTURE":    fixture,
		"GOBFD_K8S_BUILD_MODE":       mode,
		"GOBFD_K8S_BUILD_ROOT":       root,
		"GOBFD_K8S_RUNTIME_ENDPOINT": "unix://" + runtimeSocket,
		"GOBFD_K8S_IMAGE_ENDPOINT":   "unix://" + imageSocket,
		"GOBFD_BUILD_REVISION":       strings.Repeat("a", 40),
		"COMPOSE_COMPATIBILITY":      "",
		"PODMAN_COMPOSE_PROVIDER":    "fixture-compose-provider",
		"XDG_RUNTIME_DIR":            runtimeDir,
		"GORACE":                     "atexit_sleep_ms=0",
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
}

func prepareBuildModeInputs(t *testing.T, root, fixture, mode string) {
	t.Helper()
	if mode == "invalid template" {
		dsPath := filepath.Join(root, kubernetesManifestParent, "daemonset.yml")
		if err := os.WriteFile(dsPath, []byte("kind: Secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "foreign namespace" {
		if err := writeNamespaceFixture(fixture, "foreign", "foreign-uid"); err != nil {
			t.Fatal(err)
		}
	}
}

func assertBuildAndApplyOutcome(
	t *testing.T,
	mode, root, fixture string,
	err error,
	cf *kubernetesContainerdFixture,
) {
	t.Helper()
	wantSuccess := mode == "healthy" || mode == "distinct image endpoint"
	if (err != nil) != !wantSuccess {
		t.Fatalf("mode=%s error=%v", mode, err)
	}
	assertBuildModeErrors(t, mode, fixture, err)
	assertLocalPodmanCleanup(t, mode, fixture)
	assertKubernetesApplyAndReceipts(t, mode, root, fixture, cf)
}

func assertBuildModeErrors(t *testing.T, mode, fixture string, err error) {
	t.Helper()
	if mode == "empty nodes" || mode == "unready nodes" ||
		mode == "invalid runtime endpoint" || mode == "invalid image endpoint" || mode == "malformed configz" {
		if !errors.Is(err, errKubernetesTarget) {
			t.Fatalf("invalid target inventory accepted: %v", err)
		}
	}
	if mode == "cluster changes before namespace" && !errors.Is(err, errKubernetesNamespaceOwnership) {
		t.Fatalf("cluster change was not rejected: %v", err)
	}
	if mode == "replaced source alias" && !errors.Is(err, errKubernetesImageIdentity) {
		t.Fatalf("source mismatch failure lost: %v", err)
	}
	if (mode == "existing containerd alias" || mode == "create-only alias race") &&
		!errors.Is(err, errKubernetesImageAliasExists) {
		t.Fatalf("containerd alias collision was not rejected: %v", err)
	}
	if mode == "import failure" && status.Code(err) != codes.Unavailable && !strings.Contains(err.Error(), "Unavailable") {
		t.Fatalf("containerd import failure lost: %v", err)
	}
	if mode == "foreign namespace" {
		if !errors.Is(err, errKubernetesNamespaceOwnership) {
			t.Fatalf("namespace guard lost: %v", err)
		}
		imports, globErr := filepath.Glob(filepath.Join(fixture, "imported-*"))
		if globErr != nil || len(imports) != 0 {
			t.Fatalf("foreign namespace allowed import: %v %v", imports, globErr)
		}
	}
	if mode == "second build failure" || mode == "apply failure" || mode == "cleanup failure" ||
		mode == "node query failure" || mode == "configz query failure" {
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 17 {
			t.Fatalf("original lifecycle exit lost: %v", err)
		}
	}
}

func assertLocalPodmanCleanup(t *testing.T, mode, fixture string) {
	t.Helper()
	images, err := readKubernetesFixtureImages(fixture)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "cleanup failure":
		if len(images) != 2 {
			t.Fatal("failed cleanup lost image state")
		}
	case "replaced source alias":
		if len(images) != 1 || images["sha256:"+strings.Repeat("f", 64)].Labels["foreign"] != "true" {
			t.Fatal("foreign replacement was removed or owned images remain")
		}
	default:
		if len(images) != 0 {
			t.Fatalf("local owned images remain: %+v", images)
		}
	}
}

func assertKubernetesApplyAndReceipts(
	t *testing.T,
	mode, root, fixture string,
	cf *kubernetesContainerdFixture,
) {
	t.Helper()
	_, applyErr := os.Stat(filepath.Join(fixture, "applied"))
	wantApplied := mode == "healthy" || mode == "distinct image endpoint" ||
		mode == "apply failure" || mode == "cleanup failure"
	if (applyErr == nil) != wantApplied {
		t.Fatalf("unexpected apply state: %v", applyErr)
	}
	plans, err := filepath.Glob(filepath.Join(root, "reports/e2e/kubernetes-build/run.*/images.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantPlan := mode != "invalid template" && mode != "second build failure"
	if (len(plans) == 1) != wantPlan {
		t.Fatalf("plan count=%d for %s", len(plans), mode)
	}
	if wantPlan {
		verifyPersistedBuildPlan(t, mode, plans[0], cf)
	}
	assertRejectedTargetsSkippedNamespaceAndImport(t, mode, fixture)
}

func verifyPersistedBuildPlan(t *testing.T, mode, planPath string, cf *kubernetesContainerdFixture) {
	t.Helper()
	data, _, err := interopproject.ReadPrivateReceipt(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Images map[string]kubernetesPersistedImage `json:"images"`
	}
	if unmarshalErr := json.Unmarshal(data, &plan); unmarshalErr != nil || len(plan.Images) != 2 {
		t.Fatalf("invalid persisted plan: %v", unmarshalErr)
	}
	wantManifest := mode == "healthy" || mode == "distinct image endpoint" ||
		mode == "apply failure" || mode == "cleanup failure"
	for _, image := range plan.Images {
		if !strings.HasPrefix(image.Alias, "localhost/gobfd-interop-gobfd-k8s-build-") || image.ID == "" {
			t.Fatalf("shared or unidentified image in plan: %+v", image)
		}
		if wantManifest && !containertest.IsImageDigest(image.ManifestDigest) {
			t.Fatalf("missing manifest digest in persisted plan: %+v", image)
		}
	}
	storeData, _, storeErr := interopproject.ReadPrivateReceipt(filepath.Join(filepath.Dir(planPath), "containerd.json"))
	wantStoreIdentity := mode == "healthy" || mode == "distinct image endpoint" ||
		mode == "foreign namespace" || mode == "import failure" ||
		mode == "apply failure" || mode == "cleanup failure" ||
		mode == "replaced source alias" || mode == "cluster changes before namespace" ||
		mode == "create-only alias race"
	if (storeErr == nil) != wantStoreIdentity {
		t.Fatalf("containerd identity receipt presence=%t, want %t: %v", storeErr == nil, wantStoreIdentity, storeErr)
	}
	if wantStoreIdentity {
		var identity struct {
			Schema     int    `json:"schema"`
			Namespace  string `json:"namespace"`
			Endpoint   string `json:"image_service_endpoint"`
			ServerUUID string `json:"server_uuid"`
		}
		if json.Unmarshal(storeData, &identity) != nil ||
			identity.Schema != 1 ||
			identity.Namespace != kubernetesContainerdNamespace ||
			identity.Endpoint != os.Getenv("GOBFD_K8S_IMAGE_ENDPOINT") ||
			identity.ServerUUID != "containerd-server-uuid" {
			t.Fatalf("invalid containerd identity receipt: %+v", identity)
		}
	}
	cf.mu.Lock()
	updateCalls := cf.imageUpdateCalls
	cf.mu.Unlock()
	if updateCalls != 0 {
		t.Fatalf("Images.Update called %d times", updateCalls)
	}
}

func assertRejectedTargetsSkippedNamespaceAndImport(t *testing.T, mode, fixture string) {
	t.Helper()
	rejected := mode == "empty nodes" || mode == "unready nodes" || mode == "node query failure" ||
		mode == "cluster changes before namespace" || mode == "configz query failure" ||
		mode == "invalid runtime endpoint" || mode == "invalid image endpoint" ||
		mode == "malformed configz" || mode == "existing containerd alias"
	if !rejected {
		return
	}
	imports, globErr := filepath.Glob(filepath.Join(fixture, "imported-*"))
	if globErr != nil || len(imports) != 0 {
		t.Fatalf("rejected target reached import: %v %v", imports, globErr)
	}
	if _, statErr := os.Stat(filepath.Join(fixture, "namespace.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected target created namespace: %v", statErr)
	}
}

func TestKubernetesTemplateRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, old, replacement string }{
		{"duplicate key", "kind: DaemonSet", "kind: DaemonSet\nkind: Secret"},
		{"extra document", "apiVersion: apps/v1", "kind: Secret\n---\napiVersion: apps/v1"},
		{"wrong namespace", "namespace: gobfd", "namespace: default"},
		{"wrong container", "name: gobgp\n", "name: duplicate\n"},
		{"pull policy", "imagePullPolicy: Never", "imagePullPolicy: Always"},
		{"annotations", "annotations:", "invalidAnnotations:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := kubernetesBuildRoot(t)
			path := filepath.Join(root, kubernetesManifestParent, "daemonset.yml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(data), test.old, test.replacement, 1)
			if changed == string(data) {
				t.Fatal("fixture did not change template")
			}
			if writeErr := os.WriteFile(path, []byte(changed), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			r := runner{root: root}
			if _, tmplErr := r.kubernetesTemplate(); tmplErr == nil {
				t.Fatal("invalid template accepted")
			}
		})
	}
}

func TestKubernetesTemplateRejectsSymlink(t *testing.T) {
	t.Parallel()
	root := kubernetesBuildRoot(t)
	path := filepath.Join(root, kubernetesManifestParent, "daemonset.yml")
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if symErr := os.Symlink("daemonset.yml.original", path); symErr != nil {
		t.Fatal(symErr)
	}
	r := runner{root: root}
	if _, tmplErr := r.kubernetesTemplate(); !errors.Is(tmplErr, errKubernetesTemplate) {
		t.Fatalf("nonregular template accepted: %v", tmplErr)
	}
}

func TestKubernetesRenderedImagesPreserveProbes(t *testing.T) {
	t.Parallel()
	r := runner{root: kubernetesBuildRoot(t)}
	template, err := r.kubernetesTemplate()
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]interopproject.BuiltImage{
		"gobfd": {Alias: "localhost/owned-gobfd:run", ID: "sha256:" + strings.Repeat("a", 64)},
		"gobgp": {Alias: "localhost/owned-gobgp:run", ID: "sha256:" + strings.Repeat("b", 64)},
	}
	before, contErr := kubernetesContainers(template)
	if contErr != nil {
		t.Fatal(contErr)
	}
	probe, probeErr := yaml.Marshal(before["gobgp"]["readinessProbe"])
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	data, renderErr := renderKubernetesDaemonSet(template, images)
	if renderErr != nil {
		t.Fatal(renderErr)
	}
	var rendered map[string]any
	if unmarshalErr := yaml.Unmarshal(data, &rendered); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	after, afterErr := kubernetesContainers(rendered)
	if afterErr != nil {
		t.Fatal(afterErr)
	}
	afterProbe, afterProbeErr := yaml.Marshal(after["gobgp"]["readinessProbe"])
	if afterProbeErr != nil || string(afterProbe) != string(probe) {
		t.Fatalf("readiness probe changed: %v", afterProbeErr)
	}
	for name, image := range images {
		if after[name]["image"] != image.Alias {
			t.Fatalf("wrong rendered image for %s", name)
		}
	}
	if _, nilErr := renderKubernetesDaemonSet(template, nil); !errors.Is(nilErr, errKubernetesImageIdentity) {
		t.Fatalf("missing image receipt accepted: %v", nilErr)
	}
}
