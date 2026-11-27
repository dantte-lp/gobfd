package integrationrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/toolbootstrap"
)

type kubernetesFixtureImage struct {
	Alias  string            `json:"alias"`
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

func readKubernetesFixtureImages(root string) (map[string]kubernetesFixtureImage, error) {
	data, err := os.ReadFile(filepath.Join(root, "image-state.json"))
	if os.IsNotExist(err) {
		return make(map[string]kubernetesFixtureImage), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fixture image state: %w", err)
	}
	var result map[string]kubernetesFixtureImage
	if unmarshalErr := json.Unmarshal(data, &result); unmarshalErr != nil {
		return nil, fmt.Errorf("decode fixture image state: %w", unmarshalErr)
	}
	return result, nil
}

func saveKubernetesFixtureImages(root string, images map[string]kubernetesFixtureImage) int {
	data, err := json.Marshal(images)
	if err != nil || os.WriteFile(filepath.Join(root, "image-state.json"), data, 0o600) != nil {
		return 125
	}
	return 0
}

func lookupKubernetesFixtureImage(
	images map[string]kubernetesFixtureImage,
	reference string,
) (kubernetesFixtureImage, bool) {
	for _, image := range images {
		if image.ID == reference || image.Alias == reference {
			return image, true
		}
	}
	return kubernetesFixtureImage{}, false
}

func runKubernetesBuildFixture() int {
	root, mode := os.Getenv("GOBFD_K8S_BUILD_FIXTURE"), os.Getenv("GOBFD_K8S_BUILD_MODE")
	args := os.Args[1:]
	images, err := readKubernetesFixtureImages(root)
	if err != nil || len(args) == 0 {
		return 125
	}
	switch filepath.Base(os.Args[0]) {
	case "podman":
		return runKubernetesPodmanFixture(root, mode, args, images)
	case "kubectl":
		return runKubernetesKubectlFixture(root, mode, args, images)
	default:
		return 125
	}
}

func runKubernetesPodmanFixture(
	root, mode string,
	args []string,
	images map[string]kubernetesFixtureImage,
) int {
	switch args[0] {
	case "ps", "network", "volume":
		return 0
	case "info":
		fmt.Fprintln(os.Stdout, `{"hostname":"fixture","graph_root":"/fixture/images","run_root":"/fixture/run"}`)
		return 0
	case "compose":
		return runKubernetesComposeFixture(args)
	case "build":
		return runKubernetesImageBuildFixture(root, mode, args, images)
	case "image":
		return runKubernetesImageSubcommandFixture(root, mode, args, images)
	case "save":
		return runKubernetesImageSaveFixture(args, images)
	default:
		return 125
	}
}

func runKubernetesComposeFixture(args []string) int {
	if slices.Equal(args, []string{"compose", "version", "--short"}) {
		fmt.Fprintln(os.Stdout, toolbootstrap.ComposeVersion)
		return 0
	}
	if len(args) != 8 || args[1] != "-p" || args[2] != "gobfd-k8s-build" || args[3] != "-f" ||
		!strings.HasSuffix(args[4], "kubernetes/compose.build.yml") ||
		strings.Join(args[5:], " ") != "config --format json" {
		return 125
	}
	project := os.Getenv("GOBFD_K8S_BUILD_ROOT")
	gobgpDir := filepath.Join(project, "test/interop/gobgp")
	fmt.Fprintf(
		os.Stdout,
		`{"services":{"gobfd":{"build":{"context":%q,"dockerfile":"deployments/docker/Containerfile"}},`+
			`"gobgp":{"build":{"context":%q,"dockerfile":"Containerfile"}}}}`,
		project,
		gobgpDir,
	)
	return 0
}

func runKubernetesImageBuildFixture(
	root, mode string,
	args []string,
	images map[string]kubernetesFixtureImage,
) int {
	tag := slices.Index(args, "--tag")
	if tag < 0 || tag+1 >= len(args) ||
		!strings.HasPrefix(args[tag+1], "localhost/gobfd-interop-gobfd-k8s-build-") {
		return 125
	}
	requiredFlags := [][2]string{
		{"--cpu-quota", "200000"},
		{"--memory", "2g"},
		{"--memory-swap", "2g"},
		{"--jobs", "1"},
	}
	for _, pair := range requiredFlags {
		i := slices.Index(args, pair[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != pair[1] {
			return 125
		}
	}
	labels := make(map[string]string)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--label" {
			key, value, ok := strings.Cut(args[i+1], "=")
			if !ok {
				return 125
			}
			labels[key] = value
		}
	}
	alias := args[tag+1]
	_, id, err := kubernetesArchiveTestBytes(alias, labels)
	if err != nil {
		return 125
	}
	image := kubernetesFixtureImage{Alias: alias, ID: id, Labels: labels}
	images[image.ID] = image
	if code := saveKubernetesFixtureImages(root, images); code != 0 {
		return code
	}
	if mode == "second build failure" && strings.Contains(alias, "-gobgp:") {
		return 17
	}
	return 0
}

func runKubernetesImageSubcommandFixture(
	root, mode string,
	args []string,
	images map[string]kubernetesFixtureImage,
) int {
	if len(args) < 2 {
		return 125
	}
	if args[1] == "ls" {
		return 0
	}
	if len(args) < 3 {
		return 125
	}
	reference := args[len(args)-1]
	image, exists := lookupKubernetesFixtureImage(images, reference)
	switch args[1] {
	case "exists":
		if exists {
			return 0
		}
		return 1
	case "inspect":
		return inspectKubernetesFixtureImage(root, mode, args, reference, image, exists, images)
	case "rm":
		if len(args) != 4 || args[2] != "--no-prune" ||
			!exists || reference != image.ID || image.Labels["foreign"] == "true" {
			return 125
		}
		if mode == "cleanup failure" {
			return 17
		}
		delete(images, image.ID)
		return saveKubernetesFixtureImages(root, images)
	default:
		return 125
	}
}

func inspectKubernetesFixtureImage(
	root, mode string,
	args []string,
	reference string,
	image kubernetesFixtureImage,
	exists bool,
	images map[string]kubernetesFixtureImage,
) int {
	if !exists {
		return 1
	}
	if slices.Contains(args, "{{.Id}}") {
		if mode == "replaced source alias" && strings.Contains(reference, "-gobfd:") {
			original := image
			original.Alias = ""
			images[original.ID] = original
			image = kubernetesFixtureImage{
				Alias:  reference,
				ID:     "sha256:" + strings.Repeat("f", 64),
				Labels: map[string]string{"foreign": "true"},
			}
			images[image.ID] = image
			if code := saveKubernetesFixtureImages(root, images); code != 0 {
				return code
			}
		}
		fmt.Fprintln(os.Stdout, image.ID)
		return 0
	}
	err := json.NewEncoder(os.Stdout).Encode(struct {
		ID     string            `json:"id"`
		Labels map[string]string `json:"labels"`
	}{image.ID, image.Labels})
	if err != nil {
		return 125
	}
	return 0
}

func runKubernetesImageSaveFixture(args []string, images map[string]kubernetesFixtureImage) int {
	if len(args) != 8 ||
		strings.Join(args[:5], " ") != "save --format docker-archive --multi-image-archive=false --output" {
		return 125
	}
	image, exists := lookupKubernetesFixtureImage(images, args[6])
	if !exists || image.ID != args[6] || image.Alias != args[7] {
		return 125
	}
	data, id, err := kubernetesArchiveTestBytes(image.Alias, image.Labels)
	if err != nil || id != image.ID || os.WriteFile(args[5], data, 0o600) != nil {
		return 125
	}
	return 0
}

func verifyPreImportBuildReceipts(fixtureRoot, buildRoot, mode string) error {
	namespaceData, nsReadErr := os.ReadFile(filepath.Join(fixtureRoot, "namespace.json"))
	if nsReadErr != nil {
		return fmt.Errorf("read fixture namespace: %w", nsReadErr)
	}
	var namespace kubernetesNamespace
	if unmarshalErr := json.Unmarshal(namespaceData, &namespace); unmarshalErr != nil {
		return fmt.Errorf("decode fixture namespace: %w", unmarshalErr)
	}
	receiptPath := filepath.Join(buildRoot, "reports/e2e/kubernetes-build/namespace.json")
	receiptData, _, receiptErr := interopproject.ReadPrivateReceipt(receiptPath)
	if receiptErr != nil {
		return fmt.Errorf("read namespace receipt: %w", receiptErr)
	}
	var receipt kubernetesNamespaceReceipt
	if json.Unmarshal(receiptData, &receipt) != nil ||
		receipt.NamespaceUID == "" ||
		receipt.NamespaceUID != namespace.Metadata.UID ||
		receipt.Owner != namespace.Metadata.Labels[kubernetesNamespaceOwnerLabel] {
		return errors.New("namespace receipt mismatch before import")
	}
	plans, globErr := filepath.Glob(filepath.Join(buildRoot, "reports/e2e/kubernetes-build/run.*/images.json"))
	if globErr != nil || len(plans) != 1 {
		return errors.New("missing unique Kubernetes image plan")
	}
	runDir := filepath.Dir(plans[0])
	for _, name := range []string{"images.json", "daemonset.yml"} {
		if _, _, readErr := interopproject.ReadPrivateReceipt(filepath.Join(runDir, name)); readErr != nil {
			return fmt.Errorf("read %s receipt: %w", name, readErr)
		}
	}
	return verifyNodesReceiptBeforeImport(runDir, mode, receipt.ClusterUID)
}

func verifyNodesReceiptBeforeImport(runDir, mode, clusterUID string) error {
	nodeData, _, readErr := interopproject.ReadPrivateReceipt(filepath.Join(runDir, "nodes.json"))
	if readErr != nil {
		return fmt.Errorf("read nodes receipt: %w", readErr)
	}
	var inventory struct {
		Schema        int                        `json:"schema"`
		ClusterUID    string                     `json:"cluster_uid"`
		LocalNodeName string                     `json:"local_node_name"`
		LocalNodeUID  string                     `json:"local_node_uid"`
		Endpoints     kubernetesRuntimeEndpoints `json:"runtime_endpoints"`
		Nodes         []kubernetesNodeIdentity   `json:"nodes"`
	}
	if json.Unmarshal(nodeData, &inventory) != nil {
		return errors.New("malformed nodes receipt")
	}
	wantRuntime := os.Getenv("GOBFD_K8S_RUNTIME_ENDPOINT")
	wantImage := wantRuntime
	if mode == "distinct image endpoint" {
		wantImage = os.Getenv("GOBFD_K8S_IMAGE_ENDPOINT")
	}
	valid := inventory.Schema == 1 &&
		inventory.ClusterUID == clusterUID &&
		inventory.LocalNodeName == "node.example.test" &&
		inventory.LocalNodeUID == "node-uid" &&
		inventory.Endpoints.Container == wantRuntime &&
		inventory.Endpoints.Image == wantImage &&
		len(inventory.Nodes) == 1 &&
		inventory.Nodes[0].UID == inventory.LocalNodeUID
	if !valid {
		return errors.New("unexpected nodes receipt contents")
	}
	return nil
}

func runKubernetesKubectlFixture(
	root, mode string,
	args []string,
	images map[string]kubernetesFixtureImage,
) int {
	if slices.Equal(args, []string{"get", "nodes", "-o", "json"}) {
		return runKubectlNodesFixture(mode)
	}
	if len(args) == 3 && args[0] == "get" && args[1] == "--raw" && strings.HasSuffix(args[2], "/proxy/configz") {
		return runKubectlConfigzFixture(mode)
	}
	if mode == "cluster changes before namespace" &&
		slices.Equal(args, []string{"get", "namespace", "kube-system", "--ignore-not-found", "-o", "json"}) {
		return runClusterDriftFixture(root)
	}
	if code, handled := runKubernetesNamespaceFixture(root, args); handled {
		return code
	}
	return runKubectlApplyFixture(root, mode, args, images)
}

func runKubectlNodesFixture(mode string) int {
	switch mode {
	case "node query failure":
		return 17
	case "empty nodes":
		fmt.Fprintln(os.Stdout, `{"apiVersion":"v1","kind":"NodeList","items":[]}`)
		return 0
	case "unready nodes":
		fmt.Fprintln(os.Stdout, strings.ReplaceAll(kubernetesNodeFixture, `"status":"True"`, `"status":"False"`))
		return 0
	default:
		fmt.Fprintln(os.Stdout, kubernetesNodeFixture)
		return 0
	}
}

func runKubectlConfigzFixture(mode string) int {
	if mode == "configz query failure" {
		return 17
	}
	runtimeEndpoint := os.Getenv("GOBFD_K8S_RUNTIME_ENDPOINT")
	if runtimeEndpoint == "" {
		runtimeEndpoint = "unix:///run/k3s/containerd/containerd.sock"
	}
	config := fmt.Sprintf(
		`{"apiVersion":"kubelet.config.k8s.io/v1beta1","kind":"KubeletConfiguration","containerRuntimeEndpoint":%q}`,
		runtimeEndpoint,
	)
	switch mode {
	case "invalid runtime endpoint":
		config = strings.Replace(config, runtimeEndpoint, "tcp://localhost:9000", 1)
	case "invalid image endpoint":
		config = strings.TrimSuffix(config, `}`) + `,"imageServiceEndpoint":"unix://containerd.sock"}`
	case "distinct image endpoint":
		imageEndpoint := os.Getenv("GOBFD_K8S_IMAGE_ENDPOINT")
		config = strings.TrimSuffix(config, `}`) + fmt.Sprintf(`,"imageServiceEndpoint":%q}`, imageEndpoint)
	case "malformed configz":
		fmt.Fprintln(os.Stdout, `{"kubeletConfig":{}}`)
		return 0
	}
	fmt.Fprintf(os.Stdout, `{"kubeletconfig":%s}`, config)
	return 0
}

func runClusterDriftFixture(root string) int {
	countPath := filepath.Join(root, "cluster-query-count")
	data, readErr := os.ReadFile(countPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return 125
	}
	count, parseErr := strconv.Atoi(string(data))
	if parseErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return 125
	}
	count++
	if os.WriteFile(countPath, []byte(strconv.Itoa(count)), 0o600) != nil {
		return 125
	}
	uid := "cluster-uid"
	if count > 2 {
		uid = "other-cluster"
	}
	fmt.Fprintf(
		os.Stdout,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system","uid":%q,"resourceVersion":"1"}}`,
		uid,
	)
	return 0
}

func runKubectlApplyFixture(
	root, mode string,
	args []string,
	images map[string]kubernetesFixtureImage,
) int {
	if len(args) != 9 || args[0] != "apply" {
		return 125
	}
	manifest := args[len(args)-1]
	if !strings.Contains(manifest, "reports/e2e/kubernetes-build/run.") {
		return 125
	}
	data, _, err := interopproject.ReadPrivateReceipt(manifest)
	if err != nil {
		return 125
	}
	var document map[string]any
	if yaml.Unmarshal(data, &document) != nil {
		return 125
	}
	containers, contErr := kubernetesContainers(document)
	if contErr != nil {
		return 125
	}
	annotations, annErr := kubernetesMapping(document, "spec", "template", "metadata", "annotations")
	if annErr != nil {
		return 125
	}
	for name, container := range containers {
		alias, ok := container["image"].(string)
		if !ok {
			return 125
		}
		image, exists := lookupKubernetesFixtureImage(images, alias)
		if !exists || annotations["io.gobfd.image."+name] != image.ID {
			return 125
		}
		marker := filepath.Join(root, "imported-"+strings.TrimPrefix(image.ID, "sha256:"))
		if _, statErr := os.Stat(marker); statErr != nil {
			return 125
		}
	}
	if os.WriteFile(filepath.Join(root, "applied"), data, 0o600) != nil {
		return 125
	}
	if mode == "apply failure" {
		return 17
	}
	return 0
}
