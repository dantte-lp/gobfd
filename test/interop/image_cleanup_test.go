//go:build interop_testcontainers

package interop_test

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFourPeerFailedBuildCleanupUsesImageID(t *testing.T) {
	const image = "localhost/failed-peer:fixture"
	id := "sha256:" + strings.Repeat("a", 64)
	stageID := "sha256:" + strings.Repeat("b", 64)
	var mu sync.Mutex
	built := false
	stageRemoved := false
	var deleted []string
	socket := filepath.Join(t.TempDir(), "p.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/images/json" {
				if !built || stageRemoved {
					_, _ = w.Write([]byte("[]\n"))
					return
				}
				type listedImage struct {
					ID     string            `json:"Id"`     //nolint:tagliatelle // Podman API field.
					Labels map[string]string `json:"Labels"` //nolint:tagliatelle // Podman API field.
				}
				payload, marshalErr := json.Marshal([]listedImage{{
					ID: stageID,
					Labels: map[string]string{
						"io.gobfd.stage-run":  image,
						"io.gobfd.stage-test": "four-peer-testcontainers",
					},
				}})
				if marshalErr != nil {
					t.Errorf("marshal stage image list: %v", marshalErr)
					return
				}
				_, _ = w.Write(append(payload, '\n'))
				return
			}
			if !built {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodDelete {
				if r.URL.Query().Get("force") != "false" || r.URL.Query().Get("noprune") != "true" {
					t.Errorf("unsafe image cleanup query: %s", r.URL.RawQuery)
				}
				target := strings.TrimPrefix(r.URL.Path, "/v5.0.0/images/")
				deleted = append(deleted, target)
				if target == stageID {
					stageRemoved = true
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if strings.Contains(r.URL.Path, stageID) {
				if stageRemoved {
					http.NotFound(w, r)
					return
				}
				if encErr := json.NewEncoder(w).Encode(map[string]any{
					"Id": stageID,
					"Config": map[string]any{"Labels": map[string]string{
						"io.gobfd.stage-run":  image,
						"io.gobfd.stage-test": "four-peer-testcontainers",
					}},
				}); encErr != nil {
					t.Errorf("encode stage inspection: %v", encErr)
				}
				return
			}
			if encErr := json.NewEncoder(w).Encode(map[string]any{
				"Id": id,
				"Config": map[string]any{"Labels": map[string]string{
					"io.gobfd.test": "four-peer-testcontainers", "io.gobfd.run": image, "io.gobfd.image": image,
				}},
			}); encErr != nil {
				t.Errorf("encode image inspection: %v", encErr)
			}
		}),
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("close image API fixture: %v", closeErr)
		}
		if srvErr := <-done; !errors.Is(srvErr, http.ErrServerClosed) {
			t.Errorf("serve image API fixture: %v", srvErr)
		}
	})
	t.Run("partial build", func(t *testing.T) {
		registerFourPeerImageCleanup(t.Context(), t, "unix://"+socket, image, new(fourPeerResources))
		mu.Lock()
		built = true
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != 2 || deleted[0] != id || deleted[1] != stageID {
		t.Fatalf("cleanup references = %v, want immutable IDs [%s %s]", deleted, id, stageID)
	}
}
