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
	"os"
	"os/exec" //nolint:depguard // Preserve the exit status from the shared image lifecycle.
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const (
	kubernetesManifestParent  = "deployments/integrations/kubernetes/manifests"
	kubernetesNamespaceName   = "gobfd"
	kubernetesSidecarName     = "gobgp"
	kubernetesContainerCount  = 2
	kubernetesPlanSchema      = 1
	maxKubernetesTemplateSize = 1 << 20
)

var errKubernetesTemplate = errors.New("invalid Kubernetes DaemonSet template")

type kubernetesPersistedImage struct {
	Alias          string `json:"alias"`
	ID             string `json:"id"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
}

func (r *runner) kubernetesBuildAndApply(ctx context.Context) error {
	template, err := r.kubernetesTemplate()
	if err != nil {
		return err
	}
	controller, err := interopproject.NewProject(r.root, "gobfd-k8s-build", "kubernetes-build", r.stdout, r.stderr)
	if err != nil {
		return fmt.Errorf("create Kubernetes build project controller: %w", err)
	}
	runErr := controller.Lifecycle(ctx, func(ctx context.Context, owner *interopproject.Controller) error {
		return r.runLockedKubernetesBuildAndApply(ctx, owner, template)
	})
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return &ExitError{Code: exitErr.ExitCode(), Err: runErr}
	}
	if runErr != nil {
		return fmt.Errorf("kubernetes build and apply lifecycle: %w", runErr)
	}
	return nil
}

func (r *runner) runLockedKubernetesBuildAndApply(
	ctx context.Context,
	owner *interopproject.Controller,
	template map[string]any,
) (returnErr error) {
	lock, lockErr := interopproject.AcquireLock("gobfd-k8s-namespace")
	if lockErr != nil {
		return fmt.Errorf("acquire Kubernetes namespace lock: %w", lockErr)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release Kubernetes namespace lock: %w", closeErr))
		}
	}()
	images, manifest, directory, planInfo, planHash, planErr := r.buildAndPersistKubernetesPlan(ctx, owner, template)
	if planErr != nil {
		return planErr
	}
	_ = manifest
	cluster, endpoints, nodeErr := r.recordKubernetesNodes(ctx, directory)
	if nodeErr != nil {
		return nodeErr
	}
	store, storeErr := openKubernetesContainerdStore(ctx, endpoints.Image)
	if storeErr != nil {
		return storeErr
	}
	defer func() { returnErr = errors.Join(returnErr, store.close()) }()
	aliases := []string{images[kubernetesNamespaceName].Alias, images[kubernetesSidecarName].Alias}
	if absentErr := store.requireImagesAbsent(ctx, aliases); absentErr != nil {
		return absentErr
	}
	if idErr := recordKubernetesContainerdIdentity(directory, endpoints.Image, store.serverUUID); idErr != nil {
		return idErr
	}
	if nsErr := r.createKubernetesNamespace(ctx, directory, cluster); nsErr != nil {
		return nsErr
	}
	if importErr := r.importAllKubernetesImages(ctx, store, images, directory, planInfo, planHash); importErr != nil {
		return importErr
	}
	return r.applyKubernetesManifests(ctx, directory)
}

func (r *runner) buildAndPersistKubernetesPlan(
	ctx context.Context,
	owner *interopproject.Controller,
	template map[string]any,
) (map[string]interopproject.BuiltImage, []byte, string, os.FileInfo, [sha256.Size]byte, error) {
	var zeroHash [sha256.Size]byte
	images := make(map[string]interopproject.BuiltImage, kubernetesContainerCount)
	for _, name := range []string{kubernetesNamespaceName, kubernetesSidecarName} {
		image, buildErr := owner.Image(ctx, name)
		if buildErr != nil {
			return nil, nil, "", nil, zeroHash, fmt.Errorf("resolve built Kubernetes image %s: %w", name, buildErr)
		}
		images[name] = image
	}
	manifest, renderErr := renderKubernetesDaemonSet(template, images)
	if renderErr != nil {
		return nil, nil, "", nil, zeroHash, renderErr
	}
	directory, persistErr := r.persistKubernetesImages(images, manifest)
	if persistErr != nil {
		return nil, nil, "", nil, zeroHash, persistErr
	}
	rawPlan, planInfo, readErr := interopproject.ReadPrivateReceipt(filepath.Join(directory, "images.json"))
	if readErr != nil {
		return nil, nil, "", nil, zeroHash, fmt.Errorf("read persisted Kubernetes image plan: %w", readErr)
	}
	return images, manifest, directory, planInfo, sha256.Sum256(rawPlan), nil
}

func (r *runner) importAllKubernetesImages(
	ctx context.Context,
	store *kubernetesContainerdStore,
	images map[string]interopproject.BuiltImage,
	directory string,
	planInfo os.FileInfo,
	planHash [sha256.Size]byte,
) error {
	persisted := make(map[string]kubernetesPersistedImage, len(images))
	for _, name := range []string{kubernetesNamespaceName, kubernetesSidecarName} {
		image := images[name]
		manifestDigest, importErr := r.importKubernetesImage(ctx, store, image.Alias, image.ID, directory)
		if importErr != nil {
			return fmt.Errorf("import owned Kubernetes image %s: %w", name, importErr)
		}
		persisted[name] = kubernetesPersistedImage{
			Alias:          image.Alias,
			ID:             image.ID,
			ManifestDigest: manifestDigest,
		}
	}
	return updatePersistedKubernetesImages(filepath.Join(directory, "images.json"), persisted, planInfo, planHash)
}

func updatePersistedKubernetesImages(
	planPath string,
	images map[string]kubernetesPersistedImage,
	expectedInfo os.FileInfo,
	expectedHash [sha256.Size]byte,
) error {
	current, currentInfo, readErr := interopproject.ReadPrivateReceipt(planPath)
	if readErr != nil {
		return fmt.Errorf("verify Kubernetes image plan before manifest digest checkpoint: %w", readErr)
	}
	if expectedInfo == nil || !os.SameFile(expectedInfo, currentInfo) || expectedHash != sha256.Sum256(current) {
		return fmt.Errorf(
			"kubernetes image plan changed before manifest digest checkpoint: %w",
			interopproject.ErrUnsafeReceipt,
		)
	}
	data, marshalErr := json.Marshal(struct {
		Schema int                                 `json:"schema"`
		Images map[string]kubernetesPersistedImage `json:"images"`
	}{Schema: kubernetesPlanSchema, Images: images})
	if marshalErr != nil {
		return fmt.Errorf("encode Kubernetes image plan manifest digests: %w", marshalErr)
	}
	if replaceErr := interopproject.ReplacePrivateReceipt(planPath, data); replaceErr != nil {
		return fmt.Errorf("record Kubernetes image manifest digests: %w", replaceErr)
	}
	return nil
}

func (r *runner) applyKubernetesManifests(ctx context.Context, directory string) error {
	const manifestArgCount = 9
	arguments := make([]string, 0, manifestArgCount)
	arguments = append(arguments, "apply")
	for _, name := range []string{"configmap-gobfd.yml", "configmap-gobgp.yml", "service.yml"} {
		arguments = append(arguments, "-f", filepath.Join(r.root, kubernetesManifestParent, name))
	}
	arguments = append(arguments, "-f", filepath.Join(directory, "daemonset.yml"))
	return r.kubectl(ctx, arguments...)
}

func (r *runner) kubernetesTemplate() (map[string]any, error) {
	data, readErr := readRegularTemplateBytes(r.root, filepath.Join(kubernetesManifestParent, "daemonset.yml"))
	if readErr != nil {
		return nil, readErr
	}
	document, decodeErr := decodeSingleYAMLDocument(data)
	if decodeErr != nil {
		return nil, decodeErr
	}
	if validateErr := validateKubernetesDaemonSetTemplate(document); validateErr != nil {
		return nil, validateErr
	}
	return document, nil
}

func readRegularTemplateBytes(repoRoot, relativePath string) ([]byte, error) {
	root, openRootErr := os.OpenRoot(repoRoot)
	if openRootErr != nil {
		return nil, fmt.Errorf("open repository root for Kubernetes template: %w", openRootErr)
	}
	info, statErr := root.Lstat(relativePath)
	if statErr != nil {
		return nil, fmt.Errorf("inspect Kubernetes template: %w", errors.Join(statErr, root.Close()))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("validate Kubernetes template mode: %w", errors.Join(errKubernetesTemplate, root.Close()))
	}
	file, openErr := root.Open(relativePath)
	if openErr != nil {
		return nil, fmt.Errorf("open Kubernetes template: %w", errors.Join(openErr, root.Close()))
	}
	opened, openedErr := file.Stat()
	if openedErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		joined := errors.Join(errKubernetesTemplate, openedErr, file.Close(), root.Close())
		return nil, fmt.Errorf("verify opened Kubernetes template: %w", joined)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxKubernetesTemplateSize+1))
	closeErr := errors.Join(file.Close(), root.Close())
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read Kubernetes template: %w", errors.Join(readErr, closeErr))
	}
	if len(data) == 0 || len(data) > maxKubernetesTemplateSize {
		return nil, fmt.Errorf("%w: template size is outside bounds", errKubernetesTemplate)
	}
	return data, nil
}

func decodeSingleYAMLDocument(data []byte) (map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document map[string]any
	if decodeErr := decoder.Decode(&document); decodeErr != nil {
		return nil, fmt.Errorf("decode Kubernetes template: %w", decodeErr)
	}
	var extra any
	if extraErr := decoder.Decode(&extra); !errors.Is(extraErr, io.EOF) {
		return nil, fmt.Errorf("reject trailing Kubernetes YAML document: %w", errors.Join(errKubernetesTemplate, extraErr))
	}
	return document, nil
}

func validateKubernetesDaemonSetTemplate(document map[string]any) error {
	metaMap, metaErr := kubernetesMapping(document, "metadata")
	if metaErr != nil {
		return metaErr
	}
	if document["apiVersion"] != "apps/v1" ||
		document["kind"] != "DaemonSet" ||
		metaMap["name"] != kubernetesNamespaceName ||
		metaMap["namespace"] != kubernetesNamespaceName {
		return fmt.Errorf("%w: unexpected resource identity", errKubernetesTemplate)
	}
	if _, containerErr := kubernetesContainers(document); containerErr != nil {
		return containerErr
	}
	_, annErr := kubernetesMapping(document, "spec", "template", "metadata", "annotations")
	return annErr
}

func kubernetesMapping(document map[string]any, keys ...string) (map[string]any, error) {
	for _, key := range keys {
		mapping, ok := document[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %s must be a mapping", errKubernetesTemplate, key)
		}
		document = mapping
	}
	return document, nil
}

func kubernetesContainers(document map[string]any) (map[string]map[string]any, error) {
	spec, err := kubernetesMapping(document, "spec", "template", "spec")
	if err != nil {
		return nil, err
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != kubernetesContainerCount {
		return nil, fmt.Errorf("%w: exactly two containers are required", errKubernetesTemplate)
	}
	result := make(map[string]map[string]any, kubernetesContainerCount)
	for _, value := range containers {
		container, isMap := value.(map[string]any)
		if !isMap {
			return nil, errKubernetesTemplate
		}
		name, isString := container["name"].(string)
		validName := isString && (name == kubernetesNamespaceName || name == kubernetesSidecarName)
		if !validName || result[name] != nil || container["imagePullPolicy"] != "Never" {
			return nil, fmt.Errorf("%w: container identity or pull policy differs", errKubernetesTemplate)
		}
		result[name] = container
	}
	return result, nil
}

func renderKubernetesDaemonSet(document map[string]any, images map[string]interopproject.BuiltImage) ([]byte, error) {
	containers, err := kubernetesContainers(document)
	if err != nil {
		return nil, err
	}
	annotations, err := kubernetesMapping(document, "spec", "template", "metadata", "annotations")
	if err != nil {
		return nil, err
	}
	if len(images) != kubernetesContainerCount {
		return nil, errKubernetesImageIdentity
	}
	for _, name := range []string{kubernetesNamespaceName, kubernetesSidecarName} {
		image := images[name]
		if image.Alias == "" || !containertest.IsImageDigest(image.ID) {
			return nil, errKubernetesImageIdentity
		}
		containers[name]["image"] = image.Alias
		annotations["io.gobfd.image."+name] = image.ID
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode owned Kubernetes DaemonSet: %w", err)
	}
	return data, nil
}

func (r *runner) persistKubernetesImages(
	images map[string]interopproject.BuiltImage,
	manifest []byte,
) (_ string, returnErr error) {
	root, openErr := os.OpenRoot(r.root)
	if openErr != nil {
		return "", fmt.Errorf("open repository root for Kubernetes image plan: %w", openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close repository root handle: %w", closeErr))
		}
	}()
	const parent = "reports/e2e/kubernetes-build"
	if mkdirErr := root.MkdirAll(parent, 0o700); mkdirErr != nil {
		return "", fmt.Errorf("create Kubernetes image report parent: %w", mkdirErr)
	}
	relative := filepath.Join(parent, "run."+strings.ToLower(rand.Text()))
	if runDirErr := root.Mkdir(relative, 0o700); runDirErr != nil {
		return "", fmt.Errorf("create exclusive Kubernetes image report: %w", runDirErr)
	}
	directory := filepath.Join(r.root, relative)
	data, marshalErr := json.Marshal(struct {
		Schema int                                  `json:"schema"`
		Images map[string]interopproject.BuiltImage `json:"images"`
	}{Schema: kubernetesPlanSchema, Images: images})
	if marshalErr != nil {
		return "", fmt.Errorf("encode Kubernetes image plan: %w", marshalErr)
	}
	planReceipt := filepath.Join(directory, "images.json")
	if planWriteErr := interopproject.CreatePrivateReceipt(planReceipt, data); planWriteErr != nil {
		return "", fmt.Errorf("write Kubernetes image plan receipt: %w", planWriteErr)
	}
	dsReceipt := filepath.Join(directory, "daemonset.yml")
	if dsWriteErr := interopproject.CreatePrivateReceipt(dsReceipt, manifest); dsWriteErr != nil {
		return "", fmt.Errorf("write rendered Kubernetes DaemonSet receipt: %w", dsWriteErr)
	}
	r.info("Kubernetes image plan: %s", directory)
	return directory, nil
}
