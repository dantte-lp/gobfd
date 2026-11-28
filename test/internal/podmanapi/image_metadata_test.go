package podmanapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientImageMetadataContracts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		body      string
		wantLabel string
		wantError bool
	}{
		{"duplicate ID last wins", `{"Id":"old","Id":"sha256:fixture","Config":{"Labels":{"label":"last"}}}`, "last", false},
		{
			"invalid UTF8 replacement", "{\"Id\":\"sha256:fixture\",\"Config\":{\"Labels\":{\"label\":\"\xff\"}}}",
			"\ufffd", false,
		},
		{"invalid label type", `{"Id":"sha256:fixture","Config":{"Labels":{"label":7}}}`, "", true},
		{"missing ID", `{"Config":{"Labels":{"label":"value"}}}`, "", true},
		{"oversize labels", `{"Id":"sha256:fixture","Config":{"Labels":{"label":"` +
			strings.Repeat("x", maxResponseBodySize) + `"}}}`, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			socket := filepath.Join(t.TempDir(), "podman.sock")
			server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(socket)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := client.ImageMetadata(t.Context(), "fixture")
			if (err != nil) != test.wantError {
				t.Fatalf("metadata error=%v, wantError=%t", err, test.wantError)
			}
			if !test.wantError && (metadata.ID != "sha256:fixture" || metadata.Labels["label"] != test.wantLabel) {
				t.Fatalf("JSON v1 metadata compatibility changed: %+v", metadata)
			}
		})
	}
}

func TestClientImageMetadata(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "podman.sock")
	server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/images/fixture/json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Id":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
"Config":{"Labels":{"org.opencontainers.image.source":"https://github.com/dantte-lp/gobfd",
"org.opencontainers.image.revision":"fixture-revision"},"Env":["PRIVATE_VALUE=do-not-retain"]}}`))
	}))
	defer server.Close()
	client, err := NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := client.ImageMetadata(t.Context(), "fixture")
	if err != nil || metadata.ID != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" ||
		metadata.Labels["org.opencontainers.image.source"] != "https://github.com/dantte-lp/gobfd" ||
		metadata.Labels["org.opencontainers.image.revision"] != "fixture-revision" {
		t.Fatalf("image metadata=%+v err=%v", metadata, err)
	}
	wire, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "do-not-retain") {
		t.Fatal("image metadata retained runtime environment")
	}
}

func TestClientStageImagesFiltersOwnedStages(t *testing.T) {
	t.Parallel()
	stageID := "sha256:" + strings.Repeat("c", 64)
	foreignID := "sha256:" + strings.Repeat("d", 64)
	finalID := "sha256:" + strings.Repeat("e", 64)
	socket := filepath.Join(t.TempDir(), "podman.sock")
	server := newUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/images/json" || r.URL.Query().Get("all") != "true" {
			http.NotFound(w, r)
			return
		}
		filters := r.URL.Query().Get("filters")
		if !strings.Contains(filters, "io.gobfd.stage-run=run-1") ||
			!strings.Contains(filters, "io.gobfd.stage-test=unit") {
			http.Error(w, "missing stage label filters", http.StatusBadRequest)
			return
		}
		err := json.NewEncoder(w).Encode([]map[string]any{
			{
				"Id": strings.TrimPrefix(stageID, "sha256:"),
				"Labels": map[string]string{
					"io.gobfd.stage-run":  "run-1",
					"io.gobfd.stage-test": "unit",
				},
			},
			{
				"Id": foreignID,
				"Labels": map[string]string{
					"io.gobfd.stage-run":  "other-run",
					"io.gobfd.stage-test": "unit",
				},
			},
			{
				"Id": finalID,
				"Labels": map[string]string{
					"io.gobfd.stage-run":  "run-1",
					"io.gobfd.stage-test": "unit",
					"io.gobfd.image":      "localhost/final:tag",
				},
			},
		})
		if err != nil {
			t.Errorf("encode stage images: %v", err)
		}
	}))
	defer server.Close()
	client, err := NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	stages, stageErr := client.StageImages(t.Context(), "run-1", "unit")
	if stageErr != nil {
		t.Fatalf("StageImages() error = %v", stageErr)
	}
	if len(stages) != 1 || stages[0].ID != stageID {
		t.Fatalf("StageImages() = %+v, want only %s", stages, stageID)
	}
	if _, emptyErr := client.StageImages(t.Context(), "", "unit"); emptyErr == nil {
		t.Fatal("StageImages() accepted empty runID")
	}
}
