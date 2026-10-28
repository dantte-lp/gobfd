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
