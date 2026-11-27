package integrationrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const foreignNamespaceReceipt = `{"schema":1,"cluster_uid":"cluster-uid",` +
	`"owner":"foreignownerabcdefghijklmnop","namespace_uid":"foreign-uid"}`

func TestKubernetesNamespaceCustody(t *testing.T) {
	modes := []string{
		"healthy",
		"foreign namespace",
		"replaced UID",
		"replaced label",
		"changed cluster",
		"lost create response",
		"create failure",
		"delete conflict",
		"delete failure",
		"wait failure",
		"replacement after wait",
		"malformed namespace",
		"duplicate namespace field",
		"replaced pending receipt",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			runNamespaceCustodyMode(t, mode)
		})
	}
}

func runNamespaceCustodyMode(t *testing.T, mode string) {
	t.Helper()
	root, fixture, bin, runtimeDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if chmodErr := os.Chmod(runtimeDir, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	executable, execErr := os.Executable()
	if execErr != nil {
		t.Fatal(execErr)
	}
	if symErr := os.Symlink(executable, filepath.Join(bin, "kubectl")); symErr != nil {
		t.Fatal(symErr)
	}
	env := map[string]string{
		"PATH":                        bin,
		"XDG_RUNTIME_DIR":             runtimeDir,
		"GOBFD_K8S_NAMESPACE_FIXTURE": fixture,
		"GORACE":                      "atexit_sleep_ms=0",
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	directory := filepath.Join(root, "reports/e2e/kubernetes-build/run.fixture")
	if mkdirErr := os.MkdirAll(directory, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	r := runner{root: root, stdout: io.Discard, stderr: io.Discard}
	if mode == "foreign namespace" {
		if writeErr := writeNamespaceFixture(fixture, "foreign", "foreign-uid"); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if mode == "lost create response" || mode == "create failure" ||
		mode == "malformed namespace" || mode == "duplicate namespace field" ||
		mode == "replaced pending receipt" {
		t.Setenv("GOBFD_K8S_NAMESPACE_MODE", mode)
	}
	createErr := r.createKubernetesNamespace(t.Context(), directory, "cluster-uid")
	createFails := mode == "foreign namespace" || mode == "lost create response" ||
		mode == "create failure" || mode == "malformed namespace" ||
		mode == "duplicate namespace field" || mode == "replaced pending receipt"
	if (createErr != nil) != createFails {
		t.Fatalf("create error=%v mode=%s", createErr, mode)
	}
	t.Setenv("GOBFD_K8S_NAMESPACE_MODE", "")
	if verifyEarlyNamespaceMode(t, &r, fixture, mode, createErr) {
		return
	}
	mutateNamespaceBeforeDown(t, fixture, mode)
	t.Setenv("GOBFD_K8S_NAMESPACE_MODE", mode)
	downErr := r.deleteKubernetesNamespace(t.Context())
	verifyNamespaceDownOutcome(t, &r, fixture, directory, mode, downErr)
}

func verifyEarlyNamespaceMode(t *testing.T, r *runner, fixture, mode string, createErr error) bool {
	t.Helper()
	if mode == "replaced pending receipt" {
		if !errors.Is(createErr, interopproject.ErrUnsafeReceipt) {
			t.Fatalf("changed receipt accepted: %v", createErr)
		}
		data, readErr := os.ReadFile(r.namespaceReceiptPath())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(data) != foreignNamespaceReceipt {
			t.Fatal("foreign receipt overwritten")
		}
		return true
	}
	if mode == "foreign namespace" {
		if delErr := r.deleteKubernetesNamespace(t.Context()); !errors.Is(delErr, errKubernetesNamespaceOwnership) {
			t.Fatalf("foreign namespace teardown=%v", delErr)
		}
		if _, statErr := os.Stat(filepath.Join(fixture, "deleted")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("foreign namespace deleted")
		}
		return true
	}
	return false
}

func mutateNamespaceBeforeDown(t *testing.T, fixture, mode string) {
	t.Helper()
	if mode != "replaced UID" && mode != "replaced label" {
		return
	}
	nsPath := filepath.Join(fixture, "namespace.json")
	data, readErr := os.ReadFile(nsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var ns kubernetesNamespace
	if unmarshalErr := json.Unmarshal(data, &ns); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if mode == "replaced UID" {
		ns.Metadata.UID = "replacement-uid"
	} else {
		ns.Metadata.Labels[kubernetesNamespaceOwnerLabel] = "foreign"
	}
	encoded, marshalErr := json.Marshal(ns)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if writeErr := os.WriteFile(nsPath, encoded, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func verifyNamespaceDownOutcome(
	t *testing.T,
	r *runner,
	fixture, directory, mode string,
	downErr error,
) {
	t.Helper()
	downFails := mode != "healthy" && mode != "lost create response" &&
		mode != "create failure" && mode != "malformed namespace" && mode != "duplicate namespace field"
	if (downErr != nil) != downFails {
		t.Fatalf("down error=%v mode=%s", downErr, mode)
	}
	if mode == "create failure" {
		verifyCreateFailurePendingReceipt(t, r, fixture, directory)
	}
	if mode == "healthy" || mode == "lost create response" {
		if repeatErr := r.deleteKubernetesNamespace(t.Context()); repeatErr != nil {
			t.Fatalf("repeated down=%v", repeatErr)
		}
		if _, statErr := os.Stat(r.namespaceReceiptPath()); statErr != nil {
			t.Fatalf("receipt lost: %v", statErr)
		}
	}
	if mode == "healthy" {
		verifySecondNamespaceRun(t, r, directory)
	}
	if mode == "replaced UID" || mode == "replaced label" ||
		mode == "changed cluster" || mode == "delete conflict" || mode == "delete failure" {
		if _, statErr := os.Stat(filepath.Join(fixture, "namespace.json")); statErr != nil {
			t.Fatal("foreign or failed-delete namespace removed")
		}
	}
	if mode == "delete conflict" || mode == "delete failure" || mode == "wait failure" {
		var exitErr *ExitError
		if !errors.As(downErr, &exitErr) || exitErr.Code != 17 {
			t.Fatalf("exit status lost: %v", downErr)
		}
	}
}

func verifyCreateFailurePendingReceipt(t *testing.T, r *runner, fixture, directory string) {
	t.Helper()
	before, readErr := os.ReadFile(r.namespaceReceiptPath())
	if readErr != nil {
		t.Fatal(readErr)
	}
	next := filepath.Join(filepath.Dir(directory), "run.retry")
	if mkdirErr := os.Mkdir(next, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	t.Setenv("GOBFD_K8S_NAMESPACE_MODE", "")
	retryErr := r.createKubernetesNamespace(t.Context(), next, "cluster-uid")
	if !errors.Is(retryErr, errKubernetesNamespaceOwnership) {
		t.Fatalf("unresolved create replayed: %v", retryErr)
	}
	after, afterErr := os.ReadFile(r.namespaceReceiptPath())
	if afterErr != nil {
		t.Fatal(afterErr)
	}
	if string(before) != string(after) {
		t.Fatal("pending nonce lost")
	}
	requests, reqErr := os.ReadFile(filepath.Join(fixture, "create-requests"))
	if reqErr != nil || string(requests) != "create\n" {
		t.Fatalf("pending creation replayed: %s %v", requests, reqErr)
	}
}

func verifySecondNamespaceRun(t *testing.T, r *runner, directory string) {
	t.Helper()
	previous, prevErr := r.readNamespaceReceipt()
	if prevErr != nil {
		t.Fatal(prevErr)
	}
	next := filepath.Join(filepath.Dir(directory), "run.second")
	if mkdirErr := os.Mkdir(next, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if createErr := r.createKubernetesNamespace(t.Context(), next, "cluster-uid"); createErr != nil {
		t.Fatalf("new run after teardown: %v", createErr)
	}
	current, curErr := r.readNamespaceReceipt()
	if curErr != nil {
		t.Fatal(curErr)
	}
	if previous.Owner == current.Owner || previous.NamespaceUID == current.NamespaceUID {
		t.Fatal("new run reused namespace ownership")
	}
	if downErr := r.deleteKubernetesNamespace(t.Context()); downErr != nil {
		t.Fatalf("second run teardown: %v", downErr)
	}
}

func TestKubernetesNamespaceReceiptRejectsInvalidFields(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"schema", "cluster_uid", "owner", "namespace_uid", "null namespace UID"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			r := runner{root: t.TempDir()}
			if err := os.MkdirAll(filepath.Dir(r.namespaceReceiptPath()), 0o700); err != nil {
				t.Fatal(err)
			}
			data := strings.Replace(foreignNamespaceReceipt, `"`+key+`":`, `"`+strings.ToUpper(key)+`":`, 1)
			if key == "null namespace UID" {
				data = strings.Replace(foreignNamespaceReceipt, `"namespace_uid":"foreign-uid"`, `"namespace_uid":null`, 1)
			}
			if writeErr := os.WriteFile(r.namespaceReceiptPath(), []byte(data), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if _, readErr := r.readNamespaceReceipt(); !errors.Is(readErr, errKubernetesNamespaceOwnership) {
				t.Fatalf("case alias accepted: %v", readErr)
			}
		})
	}
}

func writeNamespaceFixture(root, owner, uid string) error {
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":            "gobfd",
			"uid":             uid,
			"resourceVersion": "19",
			"labels":          map[string]string{kubernetesNamespaceOwnerLabel: owner},
		},
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("encode namespace fixture: %w", err)
	}
	if writeErr := os.WriteFile(filepath.Join(root, "namespace.json"), data, 0o600); writeErr != nil {
		return fmt.Errorf("write namespace fixture: %w", writeErr)
	}
	return nil
}

func runKubernetesNamespaceFixture(root string, args []string) (int, bool) {
	mode := os.Getenv("GOBFD_K8S_NAMESPACE_MODE")
	joined := strings.Join(args, " ")
	if joined == "get namespace kube-system --ignore-not-found -o json" {
		uid := "cluster-uid"
		if mode == "changed cluster" {
			uid = "other-cluster-uid"
		}
		fmt.Fprintf(
			os.Stdout,
			`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system","uid":%q,"resourceVersion":"1"}}`,
			uid,
		)
		return 0, true
	}
	if joined == "get namespace gobfd --ignore-not-found -o json" {
		return handleGetNamespaceGobfdFixture(root), true
	}
	if len(args) == 5 && args[0] == "create" && args[1] == "-f" && args[3] == "-o" && args[4] == "json" {
		return handleCreateNamespaceGobfdFixture(root, mode, args[2]), true
	}
	if len(args) == 5 && args[0] == "delete" && args[1] == "--raw" &&
		args[2] == "/api/v1/namespaces/gobfd" && args[3] == "-f" {
		return handleDeleteNamespaceGobfdFixture(root, mode, args[4]), true
	}
	if joined == "wait --for=delete namespace/gobfd --timeout=30s" {
		if mode == "wait failure" {
			return 17, true
		}
		if mode == "replacement after wait" {
			if writeNamespaceFixture(root, "foreign", "replacement-uid") != nil {
				return 125, true
			}
		}
		return 0, true
	}
	return 0, false
}

func handleGetNamespaceGobfdFixture(root string) int {
	data, err := os.ReadFile(filepath.Join(root, "namespace.json"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		return 125
	}
	if _, writeErr := os.Stdout.Write(data); writeErr != nil {
		return 125
	}
	return 0
}

func handleCreateNamespaceGobfdFixture(root, mode, manifestPath string) int {
	requests, openErr := os.OpenFile(filepath.Join(root, "create-requests"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if openErr != nil {
		return 125
	}
	_, writeReqErr := requests.WriteString("create\n")
	if errors.Join(writeReqErr, requests.Close()) != nil {
		return 125
	}
	if mode == "create failure" {
		return 17
	}
	data, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		return 125
	}
	var ns kubernetesNamespace
	if json.Unmarshal(data, &ns) != nil ||
		ns.Metadata.Name != "gobfd" ||
		ns.Metadata.Labels[kubernetesNamespaceOwnerLabel] == "" {
		return 125
	}
	receiptPath := filepath.Join(filepath.Dir(filepath.Dir(manifestPath)), "namespace.json")
	pending, _, pendErr := interopproject.ReadPrivateReceipt(receiptPath)
	if pendErr != nil {
		return 125
	}
	var receipt kubernetesNamespaceReceipt
	owner := ns.Metadata.Labels[kubernetesNamespaceOwnerLabel]
	if json.Unmarshal(pending, &receipt) != nil ||
		receipt.ClusterUID != "cluster-uid" ||
		receipt.Owner != owner ||
		receipt.NamespaceUID != "" {
		return 125
	}
	if _, statErr := os.Stat(filepath.Join(root, "namespace.json")); !errors.Is(statErr, os.ErrNotExist) {
		return 17
	}
	if writeNamespaceFixture(root, owner, "created-"+owner) != nil {
		return 125
	}
	return finishCreateNamespaceGobfdFixture(root, mode, receiptPath)
}

func finishCreateNamespaceGobfdFixture(root, mode, receiptPath string) int {
	if mode == "replaced pending receipt" {
		if os.Remove(receiptPath) != nil || os.WriteFile(receiptPath, []byte(foreignNamespaceReceipt), 0o600) != nil {
			return 125
		}
	}
	switch mode {
	case "lost create response":
		return 17
	case "malformed namespace":
		fmt.Fprint(os.Stdout, `{"kind":"Namespace"}`)
		return 0
	case "duplicate namespace field":
		fmt.Fprint(os.Stdout, `{"kind":"Namespace","kind":"Secret"}`)
		return 0
	}
	created, err := os.ReadFile(filepath.Join(root, "namespace.json"))
	if err != nil {
		return 125
	}
	if _, writeErr := os.Stdout.Write(created); writeErr != nil {
		return 125
	}
	return 0
}

func handleDeleteNamespaceGobfdFixture(root, mode, optionsPath string) int {
	data, readErr := os.ReadFile(optionsPath)
	if readErr != nil {
		return 125
	}
	var options struct {
		APIVersion    string            `json:"apiVersion"` //nolint:tagliatelle // Kubernetes API field.
		Kind          string            `json:"kind"`
		Preconditions map[string]string `json:"preconditions"`
	}
	current, curErr := os.ReadFile(filepath.Join(root, "namespace.json"))
	if curErr != nil {
		return 125
	}
	var namespace kubernetesNamespace
	if json.Unmarshal(current, &namespace) != nil {
		return 125
	}
	validOptions := json.Unmarshal(data, &options) == nil &&
		options.APIVersion == "v1" &&
		options.Kind == "DeleteOptions" &&
		options.Preconditions["uid"] == namespace.Metadata.UID &&
		options.Preconditions["resourceVersion"] == namespace.Metadata.ResourceVersion
	if !validOptions {
		return 125
	}
	if mode == "delete conflict" {
		if writeNamespaceFixture(root, "foreign", "replacement-uid") != nil {
			return 125
		}
	}
	if mode == "delete conflict" || mode == "delete failure" {
		return 17
	}
	if os.Remove(filepath.Join(root, "namespace.json")) != nil ||
		os.WriteFile(filepath.Join(root, "deleted"), data, 0o600) != nil {
		return 125
	}
	return 0
}
