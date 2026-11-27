package integrationrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kubernetesNodeFixture = `{"apiVersion":"v1","kind":"NodeList","items":[` +
	`{"metadata":{"name":"node.example.test","uid":"node-uid","labels":{"kubernetes.io/os":"linux"}},` +
	`"status":{"nodeInfo":{"architecture":"amd64","operatingSystem":"linux","machineID":"machine-id",` +
	`"bootID":"boot-id","containerRuntimeVersion":"containerd://2.0.0"},` +
	`"conditions":[{"type":"Ready","status":"True"}]}}]}`

func TestKubernetesNodeInventory(t *testing.T) {
	t.Parallel()
	modes := []string{
		"healthy",
		"arm64",
		"cordoned",
		"multiple nodes",
		"empty",
		"not ready",
		"missing Ready",
		"duplicate Ready",
		"duplicate name",
		"duplicate UID",
		"non Linux",
		"OS mismatch",
		"foreign runtime",
		"missing runtime version",
		"missing machine ID",
		"null boot ID",
		"alias field",
		"duplicate JSON key",
		"oversized JSON",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			data := mutateNodeInventoryFixture(t, mode)
			nodes, err := decodeKubernetesNodes(data)
			healthy := mode == "healthy" || mode == "arm64" || mode == "cordoned" || mode == "multiple nodes"
			if !healthy {
				if !errors.Is(err, errKubernetesTarget) {
					t.Fatalf("bad inventory accepted: %v", err)
				}
				return
			}
			if err != nil || len(nodes) == 0 {
				t.Fatalf("valid inventory rejected: %v", err)
			}
			if mode == "multiple nodes" && (len(nodes) != 2 || nodes[0].Name != "a.example.test") {
				t.Fatalf("inventory not canonical: %+v", nodes)
			}
		})
	}
}

func mutateNodeInventoryFixture(t *testing.T, mode string) []byte {
	t.Helper()
	data := []byte(kubernetesNodeFixture)
	switch mode {
	case "arm64":
		return bytes.ReplaceAll(data, []byte("amd64"), []byte("arm64"))
	case "cordoned":
		return bytes.Replace(data, []byte(`"metadata":`), []byte(`"spec":{"unschedulable":true},"metadata":`), 1)
	case "multiple nodes", "duplicate name", "duplicate UID":
		return duplicateNodeInventoryItem(t, data, mode)
	case "empty":
		return []byte(`{"apiVersion":"v1","kind":"NodeList","items":[]}`)
	case "not ready":
		return bytes.ReplaceAll(data, []byte(`"status":"True"`), []byte(`"status":"False"`))
	case "missing Ready":
		return bytes.ReplaceAll(data, []byte(`"type":"Ready"`), []byte(`"type":"DiskPressure"`))
	case "duplicate Ready":
		dupReady := `[{"type":"Ready","status":"True"},{"type":"Ready","status":"True"}]`
		return bytes.ReplaceAll(data, []byte(`[{"type":"Ready","status":"True"}]`), []byte(dupReady))
	default:
		return mutateInvalidNodeInventoryField(data, mode)
	}
}

func duplicateNodeInventoryItem(t *testing.T, data []byte, mode string) []byte {
	t.Helper()
	var list map[string]json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	items := make([]json.RawMessage, 0, 2)
	if unmarshalErr := json.Unmarshal(list["items"], &items); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	other := bytes.Clone(items[0])
	if mode != "duplicate name" {
		other = bytes.Replace(other, []byte("node.example.test"), []byte("a.example.test"), 1)
	}
	if mode != "duplicate UID" {
		other = bytes.Replace(other, []byte("node-uid"), []byte("other-uid"), 1)
	}
	items = append(items, other)
	itemsJSON, itemsErr := json.Marshal(items)
	if itemsErr != nil {
		t.Fatal(itemsErr)
	}
	list["items"] = itemsJSON
	encoded, encErr := json.Marshal(list)
	if encErr != nil {
		t.Fatal(encErr)
	}
	return encoded
}

func mutateInvalidNodeInventoryField(data []byte, mode string) []byte {
	switch mode {
	case "non Linux":
		return bytes.ReplaceAll(data, []byte("linux"), []byte("windows"))
	case "OS mismatch":
		return bytes.ReplaceAll(data, []byte(`"operatingSystem":"linux"`), []byte(`"operatingSystem":"windows"`))
	case "foreign runtime":
		return bytes.ReplaceAll(data, []byte("containerd://"), []byte("docker://"))
	case "missing runtime version":
		return bytes.ReplaceAll(data, []byte("containerd://2.0.0"), []byte("containerd://"))
	case "missing machine ID":
		return bytes.ReplaceAll(data, []byte(`"machineID":"machine-id"`), []byte(`"machineID":""`))
	case "null boot ID":
		return bytes.ReplaceAll(data, []byte(`"bootID":"boot-id"`), []byte(`"bootID":null`))
	case "alias field":
		return bytes.ReplaceAll(data, []byte(`"apiVersion":`), []byte(`"APIVersion":`))
	case "duplicate JSON key":
		return bytes.Replace(data, []byte(`"items":`), []byte(`"items":[],"items":`), 1)
	case "oversized JSON":
		oversized := fmt.Appendf(
			nil,
			`"metadata":{"annotations":{"oversized":%q},`,
			string(bytes.Repeat([]byte("x"), maxHTTPBodySize)),
		)
		return bytes.Replace(data, []byte(`"metadata":{`), oversized, 1)
	default:
		return data
	}
}

func TestKubernetesLocalNodeMatch(t *testing.T) {
	t.Parallel()
	node := kubernetesNodeIdentity{
		Name:      "node.example.test",
		UID:       "node-uid",
		MachineID: "machine-id",
		BootID:    "boot-id",
	}
	cases := []struct {
		name  string
		nodes []kubernetesNodeIdentity
		want  bool
	}{
		{name: "unique match", nodes: []kubernetesNodeIdentity{node}, want: true},
		{
			name: "missing match",
			nodes: []kubernetesNodeIdentity{{
				Name:      "other.example.test",
				MachineID: "other-machine",
				BootID:    "boot-id",
			}},
		},
		{
			name: "duplicate match",
			nodes: []kubernetesNodeIdentity{
				node,
				{
					Name:      "other.example.test",
					UID:       "other-uid",
					MachineID: "machine-id",
					BootID:    "boot-id",
				},
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := matchKubernetesLocalNode(test.nodes, "machine-id", "boot-id")
			if (err == nil) != test.want {
				t.Fatalf("match result=%+v error=%v", got, err)
			}
			if test.want && got.UID != node.UID {
				t.Fatalf("wrong node selected: %+v", got)
			}
		})
	}
}

func TestReadKubernetesHostIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		data string
		want string
		ok   bool
	}{
		{name: "one line ending", data: "machine-id\n", want: "machine-id", ok: true},
		{
			name: "maximum identity with line ending",
			data: strings.Repeat("x", 128) + "\n",
			want: strings.Repeat("x", 128),
			ok:   true,
		},
		{name: "embedded whitespace", data: "machine id\n"},
		{name: "oversize identity", data: strings.Repeat("x", 129)},
		{name: "oversize file", data: strings.Repeat("x", 130)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "identity")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			got, readErr := readKubernetesHostIdentity(path)
			if (readErr == nil) != test.ok || (readErr == nil && got != test.want) {
				t.Fatalf("identity=%q error=%v", got, readErr)
			}
		})
	}
}

func TestDecodeKubernetesRuntimeEndpoints(t *testing.T) {
	t.Parallel()
	const base = `{"kubeletconfig":{"apiVersion":"kubelet.config.k8s.io/v1beta1",` +
		`"kind":"KubeletConfiguration","containerRuntimeEndpoint":"unix:///run/containerd.sock"}}`
	const legacy = `{"kubeletconfig":{"containerRuntimeEndpoint":"unix:///run/containerd.sock"}}`
	defaultEndpoints := kubernetesRuntimeEndpoints{
		Container: "unix:///run/containerd.sock",
		Image:     "unix:///run/containerd.sock",
	}
	cases := []struct {
		name string
		data string
		want kubernetesRuntimeEndpoints
		ok   bool
	}{
		{name: "configz without optional type metadata", data: legacy, want: defaultEndpoints, ok: true},
		{name: "image endpoint defaults to runtime", data: base, want: defaultEndpoints, ok: true},
		{
			name: "empty image endpoint defaults to runtime",
			data: strings.TrimSuffix(base, `}}`) + `,"imageServiceEndpoint":""}}`,
			want: defaultEndpoints,
			ok:   true,
		},
		{
			name: "distinct unix image endpoint",
			data: strings.TrimSuffix(base, `}}`) + `,"imageServiceEndpoint":"unix:///run/images.sock"}}`,
			want: kubernetesRuntimeEndpoints{
				Container: "unix:///run/containerd.sock",
				Image:     "unix:///run/images.sock",
			},
			ok: true,
		},
		{name: "wrong configz key", data: `{"kubeletConfig":{}}`},
		{name: "duplicate key", data: strings.Replace(base, `"kind":`, `"kind":"Other","kind":`, 1)},
		{name: "wrong api version", data: strings.Replace(base, "kubelet.config.k8s.io/v1beta1", "v1", 1)},
		{name: "wrong kind", data: strings.Replace(base, "KubeletConfiguration", "Secret", 1)},
		{
			name: "missing runtime endpoint",
			data: `{"kubeletconfig":{"apiVersion":"kubelet.config.k8s.io/v1beta1","kind":"KubeletConfiguration"}}`,
		},
		{
			name: "tcp runtime endpoint",
			data: strings.Replace(base, "unix:///run/containerd.sock", "tcp://localhost:9000", 1),
		},
		{
			name: "unix endpoint authority",
			data: strings.Replace(base, "unix:///run/containerd.sock", "unix://host/run/containerd.sock", 1),
		},
		{
			name: "unix endpoint query",
			data: strings.Replace(base, "unix:///run/containerd.sock", "unix:///run/containerd.sock?x=y", 1),
		},
		{
			name: "relative unix endpoint",
			data: strings.Replace(base, "unix:///run/containerd.sock", "unix://containerd.sock", 1),
		},
		{
			name: "traversal unix endpoint",
			data: strings.Replace(base, "unix:///run/containerd.sock", "unix:///run/../containerd.sock", 1),
		},
		{
			name: "non unix image endpoint",
			data: strings.TrimSuffix(base, `}}`) + `,"imageServiceEndpoint":"tcp://localhost:9000"}}`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeKubernetesRuntimeEndpoints([]byte(test.data))
			if (err == nil) != test.ok {
				t.Fatalf("endpoints=%+v error=%v", got, err)
			}
			if test.ok && got != test.want {
				t.Fatalf("endpoints=%+v want=%+v", got, test.want)
			}
			if !test.ok && !errors.Is(err, errKubernetesTarget) {
				t.Fatalf("wrong endpoint error: %v", err)
			}
		})
	}
}
