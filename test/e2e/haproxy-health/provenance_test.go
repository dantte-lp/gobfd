//go:build e2e_haproxy_testcontainers

package haproxy_health_test

import (
	"context"
	"encoding/json"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func haproxyImageFixture() (string, map[string]string) {
	return "sha256:" + strings.Repeat("1", 64), map[string]string{
		"org.opencontainers.image.source":   "https://github.com/dantte-lp/gobfd",
		"org.opencontainers.image.revision": strings.Repeat("a", 40),
		"org.opencontainers.image.created":  "2026-10-28T00:00:00Z",
		"org.opencontainers.image.version":  "fixture", "org.opencontainers.image.licenses": "Apache-2.0",
		"org.opencontainers.image.title": "fixture", "org.opencontainers.image.description": "fixture",
		"org.opencontainers.image.base.name":   "docker.io/library/debian:trixie-slim",
		"org.opencontainers.image.base.digest": "sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f",
		"io.gobfd.test":                        "haproxy-health-testcontainers",
		"io.gobfd.run":                         "fixture-run",
		"io.gobfd.image":                       "owned-child",
		"private-value":                        "PRIVATE_FIXTURE",
	}
}

func haproxyAPIClient(t *testing.T, handler http.Handler) *podmanapi.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "p.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client, err := podmanapi.NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestHAProxyBuiltImageProvenance(t *testing.T) {
	id, valid := haproxyImageFixture()
	for _, test := range []struct {
		name, key, value string
	}{
		{"valid", "", ""},
		{"missing annotation", "org.opencontainers.image.version", ""},
		{"wrong source", "org.opencontainers.image.source", "other"},
		{"wrong revision", "org.opencontainers.image.revision", strings.Repeat("b", 40)},
		{"invalid timestamp", "org.opencontainers.image.created", "bad"},
		{"stale timestamp", "org.opencontainers.image.created", "2026-10-27T00:00:00Z"},
		{"wrong base", "org.opencontainers.image.base.name", "other"},
		{"foreign run", "io.gobfd.run", "foreign"},
	} {
		t.Run(test.name, func(t *testing.T) {
			labels := maps.Clone(valid)
			if test.key != "" {
				labels[test.key] = test.value
			}
			client := haproxyAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/images/owned-child/json" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Config": map[string]any{
					"Labels": labels, "Env": []string{"PRIVATE=PRIVATE_FIXTURE"},
				}})
			}))
			topology := &haproxyHealthTopology{
				client: client, reportDir: t.TempDir(), runID: "fixture-run",
				revision: strings.Repeat("a", 40), buildDate: "2026-10-28T00:00:00Z",
			}
			returnedID, err := topology.recordBuiltImage(t.Context(), "owned-child")
			if test.key != "" {
				if err == nil {
					t.Fatal("invalid built image provenance accepted")
				}
				wantID := id
				if test.key == "io.gobfd.run" {
					wantID = ""
				}
				if returnedID != wantID {
					t.Fatalf("failed provenance returned ID=%q, want %q", returnedID, wantID)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(filepath.Join(topology.reportDir, "resources.json"))
			if err != nil {
				t.Fatal(err)
			}
			var retained struct {
				Images map[string]map[string]string `json:"image_provenance"`
			}
			if err := json.Unmarshal(contents, &retained); err != nil {
				t.Fatal(err)
			}
			if len(retained.Images[id]) != 9 || strings.Contains(string(contents), "PRIVATE_FIXTURE") {
				t.Fatal("image provenance missing or retains unapproved metadata")
			}
		})
	}
}

func TestHAProxyImageFailureCleanup(t *testing.T) {
	for _, failure := range []string{"provenance failure", "evidence failure"} {
		t.Run(failure, func(t *testing.T) {
			id, labels := haproxyImageFixture()
			if failure == "provenance failure" {
				labels["org.opencontainers.image.created"] = "invalid"
			}
			var mu sync.Mutex
			built, replaced, removed := false, false, false
			var deleted []string
			client := haproxyAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				reference := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v5.0.0/images/"), "/json")
				if r.Method == http.MethodDelete {
					if r.URL.Query().Get("force") != "false" || r.URL.Query().Get("noprune") != "true" {
						t.Errorf("unsafe image cleanup query: %s", r.URL.RawQuery)
					}
					deleted = append(deleted, reference)
					removed = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if !built || removed && reference == id {
					http.NotFound(w, r)
					return
				}
				responseID, responseLabels := id, maps.Clone(labels)
				if replaced && reference == "owned-child" {
					responseID = "sha256:" + strings.Repeat("2", 64)
					responseLabels["io.gobfd.run"] = "foreign"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": responseID, "Config": map[string]any{"Labels": responseLabels}})
			}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var callbacks []func()
			var reported []error
			var ownedID string
			err := containertest.RegisterImageCleanup(ctx, "owned-child", "fixture-run", "haproxy-health-testcontainers",
				&ownedID, client, func(fn func()) { callbacks = append(callbacks, fn) },
				func(err error) { reported = append(reported, err) })
			if err != nil {
				t.Fatal(err)
			}
			reportDir := t.TempDir()
			if failure == "evidence failure" {
				reportDir = filepath.Join(reportDir, "not-directory")
				if writeErr := os.WriteFile(reportDir, []byte("owned fixture"), 0o600); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			topology := &haproxyHealthTopology{
				client: client, reportDir: reportDir, runID: "fixture-run",
				revision: strings.Repeat("a", 40), buildDate: "2026-10-28T00:00:00Z",
			}
			mu.Lock()
			built = true
			mu.Unlock()
			ownedID, err = topology.recordBuiltImage(ctx, "owned-child")
			if err == nil || ownedID != id {
				t.Fatalf("failed build returned ID=%q error=%v, want ID=%q and error", ownedID, err, id)
			}
			mu.Lock()
			replaced = true
			mu.Unlock()
			cancel()
			for range 2 {
				for _, cleanup := range callbacks {
					cleanup()
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(reported) != 0 || len(deleted) != 1 || deleted[0] != id {
				t.Fatalf("failure cleanup deleted=%v errors=%v, want only immutable ID %q", deleted, reported, id)
			}
		})
	}
}
