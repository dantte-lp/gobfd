package integrationrunner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeActiveReadyLocalPod(t *testing.T) {
	t.Parallel()
	const podListMulti = `{"apiVersion":"v1","kind":"PodList","items":[` +
		`{"metadata":{"name":"gobfd-terminating","namespace":"gobfd",` +
		`"deletionTimestamp":"2026-11-27T12:00:00Z"},` +
		`"spec":{"nodeName":"node.example.test"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}},` +
		`{"metadata":{"name":"gobfd-remote","namespace":"gobfd"},` +
		`"spec":{"nodeName":"other.example.test"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}},` +
		`{"metadata":{"name":"gobfd-pending","namespace":"gobfd"},` +
		`"spec":{"nodeName":"node.example.test"},` +
		`"status":{"phase":"Pending","conditions":[{"type":"Ready","status":"False"}]}},` +
		`{"metadata":{"name":"gobfd-unready","namespace":"gobfd"},` +
		`"spec":{"nodeName":"node.example.test"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"False"}]}},` +
		`{"metadata":{"name":"gobfd-local","namespace":"gobfd"},` +
		`"spec":{"nodeName":"node.example.test"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}` +
		`]}`
	const duplicateLocal = `{"apiVersion":"v1","kind":"PodList","items":[` +
		`{"metadata":{"name":"gobfd-a","namespace":"gobfd"},"spec":{"nodeName":"node.example.test"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}},` +
		`{"metadata":{"name":"gobfd-b","namespace":"gobfd"},"spec":{"nodeName":"node.example.test"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}` +
		`]}`
	cases := []struct {
		name string
		data string
		node string
		want string
		ok   bool
	}{
		{
			name: "selects only active ready pod on local node",
			data: podListMulti,
			node: "node.example.test",
			want: "gobfd-local",
			ok:   true,
		},
		{
			name: "rejects when local node has no active ready pod",
			data: podListMulti,
			node: "missing.example.test",
		},
		{
			name: "rejects multiple active ready pods on same local node",
			data: duplicateLocal,
			node: "node.example.test",
		},
		{
			name: "rejects empty pod list",
			data: `{"apiVersion":"v1","kind":"PodList","items":[]}`,
			node: "node.example.test",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeActiveReadyLocalPod([]byte(test.data), test.node)
			if (err == nil) != test.ok {
				t.Fatalf("pod=%q error=%v, want ok=%t", got, err, test.ok)
			}
			if test.ok && got != test.want {
				t.Fatalf("selected pod=%q, want %q", got, test.want)
			}
			if !test.ok && !errors.Is(err, errKubernetesPodSelection) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestKubernetesCompatibilityWitness(t *testing.T) {
	t.Parallel()
	const metric = "gobfd_gobgp_endpoint_compatible"
	for _, test := range []struct {
		name, data string
		reject     bool
	}{
		{"canonical", metric + " 1\n", false},
		{"float", metric + " 1.0\n", false},
		{"exponent", metric + " 1e0\n", false},
		{"missing", "", true},
		{"zero", metric + " 0\n", true},
		{"nan", metric + " NaN\n", true},
		{"infinite", metric + " +Inf\n", true},
		{"malformed", metric + " malformed-private-value\n", true},
		{"duplicate", metric + " 1\n" + metric + " 1\n", true},
		{"labelled sibling", metric + " 1\n" + metric + "{unexpected=\"x\"} 0\n", true},
		{"labelled only", metric + "{unexpected=\"x\"} 1\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := kubernetesCompatibilitySample(test.data)
			if (err != nil) != test.reject {
				t.Fatalf("witness error=%v reject=%t", err, test.reject)
			}
			if test.reject && !errors.Is(err, errKubernetesCompatibility) {
				t.Fatalf("witness error classification lost: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "malformed-private-value") {
				t.Fatal("error reflected untrusted metric value")
			}
		})
	}
}

func TestKubernetesRequiredPodChecks(t *testing.T) {
	modes := []string{
		"healthy empty sessions",
		"session failure",
		"gobgp failure",
		"metrics failure",
		"empty metrics",
		"incompatible metrics",
		"duplicate gauge",
		"malformed gauge",
		"oversized metrics",
		"invalid pod",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			root, bin := t.TempDir(), t.TempDir()
			executable, execErr := os.Executable()
			if execErr != nil {
				t.Fatal(execErr)
			}
			if symErr := os.Symlink(executable, filepath.Join(bin, "kubectl")); symErr != nil {
				t.Fatal(symErr)
			}
			env := map[string]string{
				"PATH":                    bin,
				"GOBFD_K8S_PROBE_FIXTURE": root,
				"GOBFD_K8S_PROBE_MODE":    mode,
				"GORACE":                  "atexit_sleep_ms=0",
			}
			for key, value := range env {
				t.Setenv(key, value)
			}
			pod := "gobfd-fixture"
			if mode == "invalid pod" {
				pod = "../../nodes"
			}
			var output bytes.Buffer
			r := runner{root: root, stdout: &output, stderr: io.Discard}
			checkErr := r.kubernetesPodChecks(t.Context(), pod)
			if (checkErr != nil) != (mode != "healthy empty sessions") {
				t.Fatalf("mode=%s error=%v", mode, checkErr)
			}
			if mode == "session failure" || mode == "gobgp failure" || mode == "metrics failure" {
				var exitErr *ExitError
				if !errors.As(checkErr, &exitErr) || exitErr.Code != 17 {
					t.Fatalf("original probe exit lost: %v", checkErr)
				}
			}
			trace, readErr := os.ReadFile(filepath.Join(root, "trace"))
			if mode == "invalid pod" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("invalid pod allowed a command")
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			lines := strings.Split(strings.TrimSpace(string(trace)), "\n")
			if mode == "session failure" && len(lines) != 1 {
				t.Fatalf("failed session probe allowed dependent checks: %s", trace)
			}
			if mode == "gobgp failure" && len(lines) != 2 {
				t.Fatalf("failed GoBGP probe allowed metrics: %s", trace)
			}
		})
	}
}

func runKubernetesProbeFixture() int {
	args := os.Args[1:]
	root, mode := os.Getenv("GOBFD_K8S_PROBE_FIXTURE"), os.Getenv("GOBFD_K8S_PROBE_MODE")
	trace, err := os.OpenFile(filepath.Join(root, "trace"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 125
	}
	_, writeErr := fmt.Fprintln(trace, strings.Join(args, " "))
	if errors.Join(writeErr, trace.Close()) != nil {
		return 125
	}
	const sessionCmd = "-n gobfd exec gobfd-fixture -c gobfd -- " +
		"/bin/gobfdctl --addr unix:///run/gobfd/gobfd.sock session list"
	const gobgpCmd = "-n gobfd exec gobfd-fixture -c gobgp -- " +
		"/usr/local/bin/gobgp --host 127.0.0.1 --port 50051 global"
	const metricsCmd = "get --raw /api/v1/namespaces/gobfd/pods/gobfd-fixture:9100/proxy/metrics"
	switch strings.Join(args, " ") {
	case sessionCmd:
		if mode == "session failure" {
			return 17
		}
		return 0
	case gobgpCmd:
		if mode == "gobgp failure" {
			return 17
		}
		return 0
	case metricsCmd:
		return writeProbeMetricsFixture(mode)
	default:
		return 125
	}
}

func writeProbeMetricsFixture(mode string) int {
	if mode == "metrics failure" {
		return 17
	}
	switch mode {
	case "empty metrics":
		return 0
	case "incompatible metrics":
		fmt.Fprintln(os.Stdout, "gobfd_gobgp_endpoint_compatible 0")
	case "duplicate gauge":
		fmt.Fprintln(os.Stdout, "gobfd_gobgp_endpoint_compatible 1\ngobfd_gobgp_endpoint_compatible 1")
	case "malformed gauge":
		fmt.Fprintln(os.Stdout, "gobfd_gobgp_endpoint_compatible invalid")
	case "oversized metrics":
		fmt.Fprintln(os.Stdout, strings.Repeat("a", maxHTTPBodySize+1))
	default:
		sample := "gobfd_gobgp_endpoint_compatible 1\n" +
			"gobfd_bfd_delivery_outcomes_total{source=\"control\",outcome=\"accepted\"} 0"
		fmt.Fprintln(os.Stdout, sample)
	}
	return 0
}
