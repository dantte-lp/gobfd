package integrationrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

var errKubernetesTarget = errors.New("kubernetes target node inventory is not admitted")

const (
	maxKubernetesHostIdentityBytes = 129
	maxKubernetesNodes             = 4096
	maxKubernetesRuntimeVersionLen = 256
	kubernetesNodesReceiptSchema   = 1
	containerdRuntimePrefix        = "containerd://"
)

type kubernetesNodeIdentity struct {
	Name         string `json:"name"`
	UID          string `json:"uid"`
	Architecture string `json:"architecture"`
	MachineID    string `json:"machine_id"`
	BootID       string `json:"boot_id"`
	Runtime      string `json:"runtime"`
}

type kubernetesRuntimeEndpoints struct {
	Container string `json:"container_runtime_endpoint"`
	Image     string `json:"image_service_endpoint"`
}

func (r *runner) kubernetesNodes(ctx context.Context) ([]kubernetesNodeIdentity, error) {
	output := kubernetesOutput{maxBytes: maxHTTPBodySize}
	err := r.commandEnv(
		ctx,
		commandTimeout,
		&output,
		r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath},
		"kubectl", "get", "nodes", "-o", "json",
	)
	if err != nil {
		return nil, err
	}
	return decodeKubernetesNodes([]byte(output.String()))
}

func (r *runner) kubernetesLocalNode(ctx context.Context) (kubernetesNodeIdentity, []kubernetesNodeIdentity, error) {
	nodes, err := r.kubernetesNodes(ctx)
	if err != nil {
		return kubernetesNodeIdentity{}, nil, err
	}
	machineID, bootID, idErr := r.localKubernetesIdentity()
	if idErr != nil {
		return kubernetesNodeIdentity{}, nil, idErr
	}
	localNode, matchErr := matchKubernetesLocalNode(nodes, machineID, bootID)
	if matchErr != nil {
		return kubernetesNodeIdentity{}, nil, matchErr
	}
	return localNode, nodes, nil
}

func kubernetesObject(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, errors.Join(errKubernetesTarget, err)
	}
	return fields, nil
}

func kubernetesString(fields map[string]json.RawMessage, key string) (string, error) {
	var value *string
	if json.Unmarshal(fields[key], &value) != nil || value == nil {
		return "", errKubernetesTarget
	}
	return *value, nil
}

func decodeKubernetesNodes(data []byte) ([]kubernetesNodeIdentity, error) {
	items, listErr := decodeKubernetesJSONList(data, "NodeList", maxKubernetesNodes)
	if listErr != nil {
		return nil, listErr
	}
	if len(items) == 0 {
		return nil, errKubernetesTarget
	}
	nodes := make([]kubernetesNodeIdentity, 0, len(items))
	names, ids := make(map[string]bool, len(items)), make(map[string]bool, len(items))
	for _, item := range items {
		node, include, itemErr := decodeKubernetesNodeItem(item, names, ids)
		if itemErr != nil {
			return nil, itemErr
		}
		if include {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		return nil, errKubernetesTarget
	}
	slices.SortFunc(nodes, func(a, b kubernetesNodeIdentity) int { return strings.Compare(a.Name, b.Name) })
	return nodes, nil
}

func decodeKubernetesJSONList(data []byte, wantKind string, maxItems int) ([]json.RawMessage, error) {
	if len(data) == 0 || len(data) > maxHTTPBodySize {
		return nil, errKubernetesTarget
	}
	if validateErr := strictjson.Validate(data); validateErr != nil {
		return nil, errors.Join(errKubernetesTarget, validateErr)
	}
	list, objErr := kubernetesObject(data)
	if objErr != nil {
		return nil, objErr
	}
	version, verErr := kubernetesString(list, "apiVersion")
	if verErr != nil || version != "v1" {
		return nil, errKubernetesTarget
	}
	kind, kindErr := kubernetesString(list, "kind")
	if kindErr != nil || kind != wantKind {
		return nil, errKubernetesTarget
	}
	var items []json.RawMessage
	if json.Unmarshal(list["items"], &items) != nil || items == nil || len(items) > maxItems {
		return nil, errKubernetesTarget
	}
	return items, nil
}

func decodeKubernetesNodeItem(
	item json.RawMessage,
	names, ids map[string]bool,
) (kubernetesNodeIdentity, bool, error) {
	object, objErr := kubernetesObject(item)
	if objErr != nil {
		return kubernetesNodeIdentity{}, false, objErr
	}
	metaFields, metaErr := kubernetesObject(object["metadata"])
	if metaErr != nil {
		return kubernetesNodeIdentity{}, false, metaErr
	}
	node, isLinux, idErr := decodeNodeMetadata(metaFields, names, ids)
	if idErr != nil || !isLinux {
		return kubernetesNodeIdentity{}, false, idErr
	}
	statusFields, statusErr := kubernetesObject(object["status"])
	if statusErr != nil {
		return kubernetesNodeIdentity{}, false, statusErr
	}
	if infoErr := populateKubernetesNodeInfo(statusFields, &node); infoErr != nil {
		return kubernetesNodeIdentity{}, false, infoErr
	}
	if readyErr := requireSingleReadyCondition(statusFields["conditions"], errKubernetesTarget); readyErr != nil {
		return kubernetesNodeIdentity{}, false, readyErr
	}
	return node, true, nil
}

func decodeNodeMetadata(
	metaFields map[string]json.RawMessage,
	names, ids map[string]bool,
) (kubernetesNodeIdentity, bool, error) {
	var node kubernetesNodeIdentity
	name, nameErr := kubernetesString(metaFields, "name")
	if nameErr != nil || !kubernetesDNSName(name) || names[name] {
		return node, false, errKubernetesTarget
	}
	uid, uidErr := kubernetesString(metaFields, "uid")
	if uidErr != nil || !kubernetesOpaqueIdentity(uid) || ids[uid] {
		return node, false, errKubernetesTarget
	}
	node.Name, node.UID = name, uid
	names[name], ids[uid] = true, true
	labels, labelErr := kubernetesObject(metaFields["labels"])
	if labelErr != nil {
		return node, false, errKubernetesTarget
	}
	if _, ok := labels["kubernetes.io/os"]; !ok {
		return node, false, nil
	}
	osLabel, osErr := kubernetesString(labels, "kubernetes.io/os")
	if osErr != nil {
		return node, false, errKubernetesTarget
	}
	return node, osLabel == "linux", nil
}

func populateKubernetesNodeInfo(statusFields map[string]json.RawMessage, node *kubernetesNodeIdentity) error {
	info, infoErr := kubernetesObject(statusFields["nodeInfo"])
	if infoErr != nil {
		return infoErr
	}
	osName, osErr := kubernetesString(info, "operatingSystem")
	if osErr != nil || osName != "linux" {
		return errKubernetesTarget
	}
	fields := map[string]*string{
		"architecture":            &node.Architecture,
		"machineID":               &node.MachineID,
		"bootID":                  &node.BootID,
		"containerRuntimeVersion": &node.Runtime,
	}
	for key, target := range fields {
		value, fieldErr := kubernetesString(info, key)
		if fieldErr != nil {
			return fieldErr
		}
		*target = value
	}
	if !validKubernetesNodeInfo(*node) {
		return errKubernetesTarget
	}
	return nil
}

func validKubernetesNodeInfo(node kubernetesNodeIdentity) bool {
	validArch := node.Architecture == "amd64" || node.Architecture == "arm64"
	validRuntime := strings.HasPrefix(node.Runtime, containerdRuntimePrefix) &&
		len(node.Runtime) > len(containerdRuntimePrefix) &&
		len(node.Runtime) <= maxKubernetesRuntimeVersionLen &&
		!strings.ContainsAny(node.Runtime, "\x00\r\n\t ")
	return validArch &&
		kubernetesOpaqueIdentity(node.MachineID) &&
		kubernetesOpaqueIdentity(node.BootID) &&
		validRuntime
}

func requireSingleReadyCondition(raw json.RawMessage, sentinel error) error {
	var conditions []map[string]json.RawMessage
	if json.Unmarshal(raw, &conditions) != nil {
		return sentinel
	}
	ready := false
	for _, condition := range conditions {
		typeName, typeErr := kubernetesString(condition, "type")
		if typeErr != nil {
			return sentinel
		}
		if typeName != "Ready" {
			continue
		}
		state, stateErr := kubernetesString(condition, "status")
		if stateErr != nil || state != "True" || ready {
			return sentinel
		}
		ready = true
	}
	if !ready {
		return sentinel
	}
	return nil
}

func (r *runner) recordKubernetesNodes(
	ctx context.Context,
	directory string,
) (string, kubernetesRuntimeEndpoints, error) {
	cluster, err := r.kubernetesClusterUID(ctx)
	if err != nil {
		return "", kubernetesRuntimeEndpoints{}, err
	}
	localNode, nodes, nodeErr := r.kubernetesLocalNode(ctx)
	if nodeErr != nil {
		return "", kubernetesRuntimeEndpoints{}, nodeErr
	}
	endpoints, epErr := r.kubernetesRuntimeEndpoints(ctx, localNode.Name)
	if epErr != nil {
		return "", kubernetesRuntimeEndpoints{}, epErr
	}
	current, curErr := r.kubernetesClusterUID(ctx)
	if curErr != nil {
		return "", kubernetesRuntimeEndpoints{}, curErr
	}
	if current != cluster {
		return "", kubernetesRuntimeEndpoints{}, errKubernetesTarget
	}
	data, marshalErr := json.Marshal(struct {
		Schema        int                        `json:"schema"`
		ClusterUID    string                     `json:"cluster_uid"`
		LocalNodeName string                     `json:"local_node_name"`
		LocalNodeUID  string                     `json:"local_node_uid"`
		Endpoints     kubernetesRuntimeEndpoints `json:"runtime_endpoints"`
		Nodes         []kubernetesNodeIdentity   `json:"nodes"`
	}{kubernetesNodesReceiptSchema, cluster, localNode.Name, localNode.UID, endpoints, nodes})
	if marshalErr != nil {
		return "", kubernetesRuntimeEndpoints{}, fmt.Errorf("encode Kubernetes nodes receipt: %w", marshalErr)
	}
	if writeErr := interopproject.CreatePrivateReceipt(filepath.Join(directory, "nodes.json"), data); writeErr != nil {
		return "", kubernetesRuntimeEndpoints{}, fmt.Errorf("write Kubernetes nodes receipt: %w", writeErr)
	}
	return cluster, endpoints, nil
}

func (r *runner) kubernetesRuntimeEndpoints(
	ctx context.Context,
	nodeName string,
) (kubernetesRuntimeEndpoints, error) {
	if !kubernetesDNSName(nodeName) {
		return kubernetesRuntimeEndpoints{}, errKubernetesTarget
	}
	output := kubernetesOutput{maxBytes: maxHTTPBodySize}
	rawPath := "/api/v1/nodes/" + nodeName + "/proxy/configz"
	err := r.commandEnv(
		ctx,
		commandTimeout,
		&output,
		r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath},
		"kubectl", "get", "--raw", rawPath,
	)
	if err != nil {
		return kubernetesRuntimeEndpoints{}, err
	}
	return decodeKubernetesRuntimeEndpoints([]byte(output.String()))
}

func decodeKubernetesRuntimeEndpoints(data []byte) (kubernetesRuntimeEndpoints, error) {
	config, cfgErr := parseKubeletConfigObject(data)
	if cfgErr != nil {
		return kubernetesRuntimeEndpoints{}, cfgErr
	}
	runtime, rtErr := kubernetesString(config, "containerRuntimeEndpoint")
	if rtErr != nil || validateKubernetesUnixEndpoint(runtime) != nil {
		return kubernetesRuntimeEndpoints{}, errKubernetesTarget
	}
	image := runtime
	if _, hasImage := config["imageServiceEndpoint"]; hasImage {
		imageVal, imgErr := kubernetesString(config, "imageServiceEndpoint")
		if imgErr != nil {
			return kubernetesRuntimeEndpoints{}, errKubernetesTarget
		}
		if imageVal != "" {
			image = imageVal
		}
	}
	if validateKubernetesUnixEndpoint(image) != nil {
		return kubernetesRuntimeEndpoints{}, errKubernetesTarget
	}
	return kubernetesRuntimeEndpoints{Container: runtime, Image: image}, nil
}

func parseKubeletConfigObject(data []byte) (map[string]json.RawMessage, error) {
	if len(data) == 0 || len(data) > maxHTTPBodySize {
		return nil, errKubernetesTarget
	}
	if validateErr := strictjson.Validate(data); validateErr != nil {
		return nil, errors.Join(errKubernetesTarget, validateErr)
	}
	configz, objErr := kubernetesObject(data)
	if objErr != nil {
		return nil, objErr
	}
	config, cfgErr := kubernetesObject(configz["kubeletconfig"])
	if cfgErr != nil {
		return nil, errKubernetesTarget
	}
	if typeErr := checkOptionalStringField(config, "apiVersion", "kubelet.config.k8s.io/v1beta1"); typeErr != nil {
		return nil, typeErr
	}
	if kindErr := checkOptionalStringField(config, "kind", "KubeletConfiguration"); kindErr != nil {
		return nil, kindErr
	}
	return config, nil
}

func checkOptionalStringField(fields map[string]json.RawMessage, key, want string) error {
	if _, present := fields[key]; !present {
		return nil
	}
	value, err := kubernetesString(fields, key)
	if err != nil || value != want {
		return errKubernetesTarget
	}
	return nil
}

func validateKubernetesUnixEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil ||
		parsed.Scheme != "unix" ||
		parsed.Opaque != "" ||
		parsed.Host != "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		!filepath.IsAbs(parsed.Path) ||
		filepath.Clean(parsed.Path) != parsed.Path ||
		strings.ContainsAny(parsed.Path, "\x00\r\n\t ") {
		return errKubernetesTarget
	}
	return nil
}

func (r *runner) localKubernetesIdentity() (string, string, error) {
	if r.hostIdentity != nil {
		return r.hostIdentity()
	}
	machineID, err := readKubernetesHostIdentity("/etc/machine-id")
	if err != nil {
		return "", "", fmt.Errorf("read local Kubernetes machine identity: %w", err)
	}
	bootID, err := readKubernetesHostIdentity("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", "", fmt.Errorf("read local Kubernetes boot identity: %w", err)
	}
	return machineID, bootID, nil
}

func readKubernetesHostIdentity(targetPath string) (string, error) {
	root, openRootErr := os.OpenRoot(filepath.Dir(targetPath))
	if openRootErr != nil {
		return "", fmt.Errorf("open host identity parent directory: %w", openRootErr)
	}
	file, openErr := root.Open(filepath.Base(targetPath))
	if openErr != nil {
		return "", fmt.Errorf("open host identity file: %w", errors.Join(openErr, root.Close()))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxKubernetesHostIdentityBytes+1))
	closeErr := errors.Join(file.Close(), root.Close())
	if readErr != nil || closeErr != nil {
		return "", fmt.Errorf("read host identity file: %w", errors.Join(readErr, closeErr))
	}
	if len(data) > maxKubernetesHostIdentityBytes {
		return "", errKubernetesTarget
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	identity := string(data)
	if !kubernetesOpaqueIdentity(identity) {
		return "", errKubernetesTarget
	}
	return identity, nil
}

func matchKubernetesLocalNode(
	nodes []kubernetesNodeIdentity,
	machineID, bootID string,
) (kubernetesNodeIdentity, error) {
	if !kubernetesOpaqueIdentity(machineID) || !kubernetesOpaqueIdentity(bootID) {
		return kubernetesNodeIdentity{}, errKubernetesTarget
	}
	var match kubernetesNodeIdentity
	for _, node := range nodes {
		if node.MachineID != machineID || node.BootID != bootID {
			continue
		}
		if match.Name != "" {
			return kubernetesNodeIdentity{}, errKubernetesTarget
		}
		match = node
	}
	if match.Name == "" {
		return kubernetesNodeIdentity{}, errKubernetesTarget
	}
	return match, nil
}
