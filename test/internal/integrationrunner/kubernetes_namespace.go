package integrationrunner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/strictjson"
)

const (
	kubernetesNamespaceOwnerLabel = "io.gobfd.namespace-owner"
	kubernetesReceiptFieldCount   = 4
	kubernetesReceiptSchema       = 1
	minOwnerLabelLen              = 26
	maxOwnerLabelLen              = 63
	maxOpaqueIdentityLen          = 128
)

var (
	errKubernetesNamespaceOwnership = errors.New("kubernetes namespace ownership is not established")
	errKubernetesNamespaceAbsent    = errors.New("kubernetes namespace is absent")
)

type kubernetesNamespace struct {
	APIVersion string `json:"apiVersion"` //nolint:tagliatelle // Kubernetes API field.
	Kind       string `json:"kind"`
	Metadata   struct {
		Name              string            `json:"name"`
		UID               string            `json:"uid"`
		ResourceVersion   string            `json:"resourceVersion"` //nolint:tagliatelle // Kubernetes API field.
		Labels            map[string]string `json:"labels"`
		DeletionTimestamp *string           `json:"deletionTimestamp"` //nolint:tagliatelle // Kubernetes API field.
	} `json:"metadata"`
}

type kubernetesNamespaceReceipt struct {
	Schema       int    `json:"schema"`
	ClusterUID   string `json:"cluster_uid"`
	Owner        string `json:"owner"`
	NamespaceUID string `json:"namespace_uid"`
	info         os.FileInfo
	hash         [sha256.Size]byte
}

func (r *runner) namespaceReceiptPath() string {
	return filepath.Join(r.root, "reports/e2e/kubernetes-build/namespace.json")
}

func (r *runner) readKubernetesNamespace(ctx context.Context, name string) (*kubernetesNamespace, error) {
	output := kubernetesOutput{maxBytes: maxHTTPBodySize}
	err := r.commandEnv(
		ctx,
		commandTimeout,
		&output,
		r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath},
		"kubectl", "get", "namespace", name, "--ignore-not-found", "-o", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes namespace identity: %w", err)
	}
	if strings.TrimSpace(output.String()) == "" {
		return nil, errKubernetesNamespaceAbsent
	}
	return decodeKubernetesNamespace([]byte(output.String()), name)
}

func decodeKubernetesNamespace(data []byte, name string) (*kubernetesNamespace, error) {
	if err := strictjson.Validate(data); err != nil {
		return nil, errors.Join(errKubernetesNamespaceOwnership, err)
	}
	var namespace kubernetesNamespace
	if err := json.Unmarshal(data, &namespace); err != nil {
		return nil, errors.Join(errKubernetesNamespaceOwnership, err)
	}
	if namespace.APIVersion != "v1" || namespace.Kind != "Namespace" || namespace.Metadata.Name != name ||
		!kubernetesOpaqueIdentity(namespace.Metadata.UID) ||
		!kubernetesOpaqueIdentity(namespace.Metadata.ResourceVersion) {
		return nil, errKubernetesNamespaceOwnership
	}
	return &namespace, nil
}

func kubernetesOpaqueIdentity(value string) bool {
	return len(value) > 0 && len(value) <= maxOpaqueIdentityLen && !strings.ContainsAny(value, "\x00\r\n\t ")
}

func (r *runner) kubernetesClusterUID(ctx context.Context) (string, error) {
	namespace, err := r.readKubernetesNamespace(ctx, "kube-system")
	if errors.Is(err, errKubernetesNamespaceAbsent) {
		return "", errKubernetesNamespaceOwnership
	}
	if err != nil {
		return "", err
	}
	return namespace.Metadata.UID, nil
}

func (r *runner) readNamespaceReceipt() (kubernetesNamespaceReceipt, error) {
	var receipt kubernetesNamespaceReceipt
	data, info, err := interopproject.ReadPrivateReceipt(r.namespaceReceiptPath())
	if err != nil {
		return receipt, fmt.Errorf("read Kubernetes namespace receipt: %w", err)
	}
	if validateErr := validateNamespaceReceiptJSON(data); validateErr != nil {
		return receipt, validateErr
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&receipt); decodeErr != nil {
		return receipt, errors.Join(errKubernetesNamespaceOwnership, decodeErr)
	}
	if !validNamespaceReceiptValues(receipt) {
		return receipt, errKubernetesNamespaceOwnership
	}
	receipt.info, receipt.hash = info, sha256.Sum256(data)
	return receipt, nil
}

func validateNamespaceReceiptJSON(data []byte) error {
	if err := strictjson.Validate(data); err != nil {
		return errors.Join(errKubernetesNamespaceOwnership, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.Join(errKubernetesNamespaceOwnership, err)
	}
	if len(fields) != kubernetesReceiptFieldCount {
		return errKubernetesNamespaceOwnership
	}
	if bytes.Equal(bytes.TrimSpace(fields["namespace_uid"]), []byte("null")) {
		return errKubernetesNamespaceOwnership
	}
	for key := range fields {
		switch key {
		case "schema", "cluster_uid", "owner", "namespace_uid":
		default:
			return errKubernetesNamespaceOwnership
		}
	}
	return nil
}

func validNamespaceReceiptValues(receipt kubernetesNamespaceReceipt) bool {
	if receipt.Schema != kubernetesReceiptSchema || !kubernetesOpaqueIdentity(receipt.ClusterUID) {
		return false
	}
	if len(receipt.Owner) < minOwnerLabelLen ||
		len(receipt.Owner) > maxOwnerLabelLen ||
		!kubernetesDNSLabel.MatchString(receipt.Owner) {
		return false
	}
	return receipt.NamespaceUID == "" || kubernetesOpaqueIdentity(receipt.NamespaceUID)
}

func (r *runner) writeNamespaceReceipt(receipt *kubernetesNamespaceReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode Kubernetes namespace receipt: %w", err)
	}
	receiptPath := r.namespaceReceiptPath()
	if writeErr := persistNamespaceReceiptBytes(receiptPath, data, receipt); writeErr != nil {
		return writeErr
	}
	published, info, pubErr := interopproject.ReadPrivateReceipt(receiptPath)
	if pubErr != nil {
		return fmt.Errorf("read back Kubernetes namespace receipt: %w", pubErr)
	}
	if !bytes.Equal(data, published) {
		return interopproject.ErrUnsafeReceipt
	}
	receipt.info, receipt.hash = info, sha256.Sum256(published)
	return nil
}

func persistNamespaceReceiptBytes(receiptPath string, data []byte, receipt *kubernetesNamespaceReceipt) error {
	if receipt.info == nil {
		if createErr := interopproject.CreatePrivateReceipt(receiptPath, data); createErr != nil {
			return fmt.Errorf("create Kubernetes namespace receipt: %w", createErr)
		}
		return nil
	}
	current, info, readErr := interopproject.ReadPrivateReceipt(receiptPath)
	if readErr != nil {
		return fmt.Errorf("verify Kubernetes namespace receipt before update: %w", readErr)
	}
	if !os.SameFile(receipt.info, info) || receipt.hash != sha256.Sum256(current) {
		return fmt.Errorf("namespace receipt changed before checkpoint: %w", interopproject.ErrUnsafeReceipt)
	}
	if replaceErr := interopproject.ReplacePrivateReceipt(receiptPath, data); replaceErr != nil {
		return fmt.Errorf("replace Kubernetes namespace receipt: %w", replaceErr)
	}
	return nil
}

// createKubernetesNamespace requires the shared namespace lock held by the caller.
func (r *runner) createKubernetesNamespace(ctx context.Context, directory, expectedCluster string) error {
	previous, cluster, preflightErr := r.preflightNamespaceCreation(ctx, expectedCluster)
	if preflightErr != nil {
		return preflightErr
	}
	receipt := kubernetesNamespaceReceipt{
		Schema:     kubernetesReceiptSchema,
		ClusterUID: cluster,
		Owner:      strings.ToLower(rand.Text()),
		info:       previous.info,
		hash:       previous.hash,
	}
	if writeErr := r.writeNamespaceReceipt(&receipt); writeErr != nil {
		return fmt.Errorf("persist pending namespace ownership before create: %w", writeErr)
	}
	planPath, planErr := writeKubernetesNamespaceCreateManifest(directory, receipt.Owner)
	if planErr != nil {
		return planErr
	}
	output := kubernetesOutput{maxBytes: maxHTTPBodySize}
	createErr := r.commandEnv(
		ctx,
		commandTimeout,
		&output,
		r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath},
		"kubectl", "create", "-f", planPath, "-o", "json",
	)
	if createErr != nil {
		return fmt.Errorf("create owned namespace; preserve pending receipt without replay: %w", createErr)
	}
	namespace, decodeErr := decodeKubernetesNamespace([]byte(output.String()), kubernetesNamespaceName)
	if decodeErr != nil {
		return decodeErr
	}
	if namespace.Metadata.Labels[kubernetesNamespaceOwnerLabel] != receipt.Owner ||
		namespace.Metadata.DeletionTimestamp != nil {
		return errKubernetesNamespaceOwnership
	}
	receipt.NamespaceUID = namespace.Metadata.UID
	return r.writeNamespaceReceipt(&receipt)
}

func (r *runner) preflightNamespaceCreation(
	ctx context.Context,
	expectedCluster string,
) (kubernetesNamespaceReceipt, string, error) {
	var empty kubernetesNamespaceReceipt
	cluster, err := r.kubernetesClusterUID(ctx)
	if err != nil {
		return empty, "", err
	}
	if expectedCluster == "" || cluster != expectedCluster {
		return empty, "", errKubernetesNamespaceOwnership
	}
	_, existingErr := r.readKubernetesNamespace(ctx, kubernetesNamespaceName)
	if existingErr == nil {
		return empty, "", fmt.Errorf(
			"preserve existing namespace gobfd; complete its owned teardown first: %w",
			errKubernetesNamespaceOwnership,
		)
	}
	if !errors.Is(existingErr, errKubernetesNamespaceAbsent) {
		return empty, "", existingErr
	}
	previous, prevErr := r.readNamespaceReceipt()
	if prevErr != nil && !errors.Is(prevErr, os.ErrNotExist) {
		return empty, "", prevErr
	}
	if prevErr == nil && previous.ClusterUID != cluster {
		return empty, "", errKubernetesNamespaceOwnership
	}
	if prevErr == nil && previous.NamespaceUID == "" {
		return empty, "", fmt.Errorf(
			"preserve unresolved pending namespace creation before another startup: %w",
			errKubernetesNamespaceOwnership,
		)
	}
	return previous, cluster, nil
}

func writeKubernetesNamespaceCreateManifest(directory, owner string) (string, error) {
	var namespacePlan struct {
		APIVersion string `json:"apiVersion"` //nolint:tagliatelle // Kubernetes API field.
		Kind       string `json:"kind"`
		Metadata   struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	namespacePlan.APIVersion = "v1"
	namespacePlan.Kind = "Namespace"
	namespacePlan.Metadata.Name = kubernetesNamespaceName
	namespacePlan.Metadata.Labels = map[string]string{
		kubernetesNamespaceOwnerLabel: owner,
		"app.kubernetes.io/name":      kubernetesNamespaceName,
		"app.kubernetes.io/part-of":   "gobfd-demo",
	}
	manifest, err := json.Marshal(namespacePlan)
	if err != nil {
		return "", fmt.Errorf("encode Kubernetes namespace manifest: %w", err)
	}
	path := filepath.Join(directory, "namespace-create.json")
	if writeErr := interopproject.CreatePrivateReceipt(path, manifest); writeErr != nil {
		return "", fmt.Errorf("write Kubernetes namespace manifest receipt: %w", writeErr)
	}
	return path, nil
}

func (r *runner) deleteKubernetesNamespace(ctx context.Context) (returnErr error) {
	lock, lockErr := interopproject.AcquireLock("gobfd-k8s-namespace")
	if lockErr != nil {
		return fmt.Errorf("acquire Kubernetes namespace teardown lock: %w", lockErr)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release Kubernetes namespace teardown lock: %w", closeErr))
		}
	}()
	cluster, clusterErr := r.kubernetesClusterUID(ctx)
	if clusterErr != nil {
		return clusterErr
	}
	namespace, nsErr := r.readKubernetesNamespace(ctx, kubernetesNamespaceName)
	if errors.Is(nsErr, errKubernetesNamespaceAbsent) {
		return nil
	}
	if nsErr != nil {
		return nsErr
	}
	receipt, verifyErr := r.verifyNamespaceTeardownOwnership(cluster, namespace)
	if verifyErr != nil {
		return verifyErr
	}
	return r.executeOwnedNamespaceDeletion(ctx, namespace, receipt)
}

func (r *runner) verifyNamespaceTeardownOwnership(
	cluster string,
	namespace *kubernetesNamespace,
) (kubernetesNamespaceReceipt, error) {
	receipt, err := r.readNamespaceReceipt()
	if err != nil {
		return kubernetesNamespaceReceipt{}, errors.Join(errKubernetesNamespaceOwnership, err)
	}
	if receipt.ClusterUID != cluster ||
		namespace.Metadata.Labels[kubernetesNamespaceOwnerLabel] != receipt.Owner ||
		(receipt.NamespaceUID != "" && receipt.NamespaceUID != namespace.Metadata.UID) {
		return kubernetesNamespaceReceipt{}, errKubernetesNamespaceOwnership
	}
	// A pending nonce recovers a lost create response without replaying the create.
	if receipt.NamespaceUID == "" {
		receipt.NamespaceUID = namespace.Metadata.UID
		if writeErr := r.writeNamespaceReceipt(&receipt); writeErr != nil {
			return kubernetesNamespaceReceipt{}, writeErr
		}
	}
	return receipt, nil
}

func (r *runner) executeOwnedNamespaceDeletion(
	ctx context.Context,
	namespace *kubernetesNamespace,
	receipt kubernetesNamespaceReceipt,
) error {
	options, marshalErr := json.Marshal(struct {
		APIVersion    string            `json:"apiVersion"` //nolint:tagliatelle // Kubernetes API field.
		Kind          string            `json:"kind"`
		Preconditions map[string]string `json:"preconditions"`
	}{
		APIVersion: "v1",
		Kind:       "DeleteOptions",
		Preconditions: map[string]string{
			"uid":             receipt.NamespaceUID,
			"resourceVersion": namespace.Metadata.ResourceVersion,
		},
	})
	if marshalErr != nil {
		return fmt.Errorf("encode Kubernetes namespace DeleteOptions: %w", marshalErr)
	}
	// Keep an exclusive request file as evidence; never mutate an existing request.
	deleteName := "namespace-delete." + strings.ToLower(rand.Text()) + ".json"
	deletePath := filepath.Join(filepath.Dir(r.namespaceReceiptPath()), deleteName)
	if receiptErr := interopproject.CreatePrivateReceipt(deletePath, options); receiptErr != nil {
		return fmt.Errorf("write Kubernetes namespace DeleteOptions receipt: %w", receiptErr)
	}
	if namespace.Metadata.DeletionTimestamp == nil {
		if delErr := r.kubectl(ctx, "delete", "--raw", "/api/v1/namespaces/gobfd", "-f", deletePath); delErr != nil {
			return fmt.Errorf("delete owned namespace with identity preconditions: %w", delErr)
		}
	}
	if waitErr := r.kubectl(ctx, "wait", "--for=delete", "namespace/gobfd", "--timeout=30s"); waitErr != nil {
		return waitErr
	}
	_, remErr := r.readKubernetesNamespace(ctx, kubernetesNamespaceName)
	if errors.Is(remErr, errKubernetesNamespaceAbsent) {
		return nil
	}
	if remErr != nil {
		return remErr
	}
	return errKubernetesNamespaceOwnership
}
