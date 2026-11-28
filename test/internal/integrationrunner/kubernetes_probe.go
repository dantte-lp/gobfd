package integrationrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxPodMetricPreviewLines = 10
	maxDNSNameLen            = 253
	maxDNSLabelLen           = 63
	metricSampleFieldCount   = 2
	maxKubernetesPods        = 4096
)

var (
	errKubernetesPodName       = errors.New("invalid Kubernetes pod name")
	errKubernetesPodSelection  = errors.New("kubernetes pod inventory did not yield one active ready local pod")
	errKubernetesCompatibility = errors.New("GoBFD GoBGP compatibility metric is not exactly one successful sample")
	kubernetesDNSLabel         = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

func (r *runner) selectKubernetesLocalPod(ctx context.Context, localNodeName string) (string, error) {
	if !kubernetesDNSName(localNodeName) {
		return "", errKubernetesPodSelection
	}
	output := kubernetesOutput{maxBytes: maxHTTPBodySize}
	err := r.commandEnv(
		ctx,
		commandTimeout,
		&output,
		r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath},
		"kubectl", "-n", kubernetesNamespaceName, "get", "pods",
		"-l", "app.kubernetes.io/name=gobfd", "-o", "json",
	)
	if err != nil {
		return "", err
	}
	return decodeActiveReadyLocalPod([]byte(output.String()), localNodeName)
}

func decodeActiveReadyLocalPod(data []byte, localNodeName string) (string, error) {
	if !kubernetesDNSName(localNodeName) {
		return "", errKubernetesPodSelection
	}
	items, listErr := decodeKubernetesJSONList(data, "PodList", maxKubernetesPods)
	if listErr != nil {
		return "", errors.Join(errKubernetesPodSelection, listErr)
	}
	selected := ""
	seenNames := make(map[string]bool, len(items))
	for _, item := range items {
		podName, matches, itemErr := evaluateKubernetesPodCandidate(item, localNodeName, seenNames)
		if itemErr != nil {
			return "", itemErr
		}
		if !matches {
			continue
		}
		if selected != "" {
			return "", errKubernetesPodSelection
		}
		selected = podName
	}
	if selected == "" {
		return "", errKubernetesPodSelection
	}
	return selected, nil
}

func evaluateKubernetesPodCandidate(
	item json.RawMessage,
	localNodeName string,
	seenNames map[string]bool,
) (string, bool, error) {
	object, objErr := kubernetesObject(item)
	if objErr != nil {
		return "", false, errors.Join(errKubernetesPodSelection, objErr)
	}
	metaFields, metaErr := kubernetesObject(object["metadata"])
	if metaErr != nil {
		return "", false, errors.Join(errKubernetesPodSelection, metaErr)
	}
	name, nameErr := kubernetesString(metaFields, "name")
	if nameErr != nil || !kubernetesDNSName(name) || seenNames[name] {
		return "", false, errKubernetesPodSelection
	}
	seenNames[name] = true
	namespace, nsErr := kubernetesString(metaFields, "namespace")
	if nsErr != nil || namespace != kubernetesNamespaceName {
		return "", false, errKubernetesPodSelection
	}
	if !jsonFieldAbsentOrNull(metaFields["deletionTimestamp"]) {
		return "", false, nil
	}
	specFields, specErr := kubernetesObject(object["spec"])
	if specErr != nil {
		return "", false, errors.Join(errKubernetesPodSelection, specErr)
	}
	if jsonFieldAbsentOrNull(specFields["nodeName"]) {
		return "", false, nil
	}
	nodeName, nodeErr := kubernetesString(specFields, "nodeName")
	if nodeErr != nil || !kubernetesDNSName(nodeName) {
		return "", false, errKubernetesPodSelection
	}
	if nodeName != localNodeName {
		return "", false, nil
	}
	ready, readyErr := podStatusActiveAndReady(object["status"])
	if readyErr != nil {
		return "", false, readyErr
	}
	return name, ready, nil
}

func jsonFieldAbsentOrNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func podStatusActiveAndReady(rawStatus json.RawMessage) (bool, error) {
	statusFields, statusErr := kubernetesObject(rawStatus)
	if statusErr != nil {
		return false, errors.Join(errKubernetesPodSelection, statusErr)
	}
	phase, phaseErr := kubernetesString(statusFields, "phase")
	if phaseErr != nil {
		return false, errKubernetesPodSelection
	}
	if phase != "Running" {
		return false, nil
	}
	ready := requireSingleReadyCondition(statusFields["conditions"], errKubernetesPodSelection) == nil
	return ready, nil
}

func (r *runner) kubernetesPodChecks(ctx context.Context, pod string) error {
	if !kubernetesDNSName(pod) {
		return errKubernetesPodName
	}
	if err := r.kubectl(ctx, "-n", kubernetesNamespaceName, "exec", pod, "-c", kubernetesNamespaceName, "--",
		"/bin/gobfdctl", "--addr", "unix:///run/gobfd/gobfd.sock", "session", "list"); err != nil {
		return fmt.Errorf("query required GoBFD management API: %w", err)
	}
	if err := r.kubectl(ctx, "-n", kubernetesNamespaceName, "exec", pod, "-c", kubernetesSidecarName, "--",
		"/usr/local/bin/gobgp", "--host", "127.0.0.1", "--port", "50051", "global"); err != nil {
		return fmt.Errorf("query required GoBGP API: %w", err)
	}
	output := kubernetesOutput{maxBytes: maxHTTPBodySize}
	proxyPath := "/api/v1/namespaces/gobfd/pods/" + pod + ":9100/proxy/metrics"
	err := r.commandEnv(
		ctx,
		commandTimeout,
		&output,
		r.stderr,
		[]string{"KUBECONFIG=" + kubeconfigPath},
		"kubectl", "get", "--raw", proxyPath,
	)
	if err != nil {
		return fmt.Errorf("query required pod metrics through API proxy: %w", err)
	}
	if sampleErr := kubernetesCompatibilitySample(output.String()); sampleErr != nil {
		return sampleErr
	}
	written := 0
	for line := range strings.Lines(output.String()) {
		if strings.HasPrefix(line, "gobfd_bfd_") && written < maxPodMetricPreviewLines {
			if _, writeErr := fmt.Fprintln(r.stdout, line); writeErr != nil {
				return fmt.Errorf("write pod metric readback: %w", writeErr)
			}
			written++
		}
	}
	return nil
}

func kubernetesDNSName(name string) bool {
	if len(name) == 0 || len(name) > maxDNSNameLen {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if len(label) > maxDNSLabelLen || !kubernetesDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func kubernetesCompatibilitySample(metrics string) error {
	const metric = "gobfd_gobgp_endpoint_compatible"
	count := 0
	for line := range strings.Lines(metrics) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], metric+"{") {
			return errKubernetesCompatibility
		}
		if fields[0] != metric {
			continue
		}
		if len(fields) != metricSampleFieldCount {
			return errKubernetesCompatibility
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return fmt.Errorf("%w: malformed numeric sample", errKubernetesCompatibility)
		}
		if value != 1 {
			return errKubernetesCompatibility
		}
		count++
	}
	if count != 1 {
		return errKubernetesCompatibility
	}
	return nil
}
