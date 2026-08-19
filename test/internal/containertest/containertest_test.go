package containertest

import (
	"testing"

	testcontainers "github.com/testcontainers/testcontainers-go"
)

func TestResolvePodmanEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		sockets   map[string]bool
		want      string
		wantFound bool
	}{
		{
			name:      "docker host has precedence",
			env:       map[string]string{"DOCKER_HOST": "unix:///custom/docker.sock", "PODMAN_HOST": "unix:///custom/podman.sock"},
			sockets:   map[string]bool{"/custom/docker.sock": true, "/custom/podman.sock": true},
			want:      "unix:///custom/docker.sock",
			wantFound: true,
		},
		{
			name:      "podman host",
			env:       map[string]string{"PODMAN_HOST": "unix:///run/podman/podman.sock"},
			sockets:   map[string]bool{"/run/podman/podman.sock": true},
			want:      "unix:///run/podman/podman.sock",
			wantFound: true,
		},
		{
			name:      "container host fallback",
			env:       map[string]string{"CONTAINER_HOST": "unix:///run/user/1000/podman/podman.sock"},
			sockets:   map[string]bool{"/run/user/1000/podman/podman.sock": true},
			want:      "unix:///run/user/1000/podman/podman.sock",
			wantFound: true,
		},
		{
			name:      "rootful default",
			sockets:   map[string]bool{"/run/podman/podman.sock": true},
			want:      "unix:///run/podman/podman.sock",
			wantFound: true,
		},
		{
			name:      "rootless default",
			env:       map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
			sockets:   map[string]bool{"/run/user/1000/podman/podman.sock": true},
			want:      "unix:///run/user/1000/podman/podman.sock",
			wantFound: true,
		},
		{
			name: "no socket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, found := resolvePodmanEndpoint(
				func(key string) string { return tt.env[key] },
				func(path string) bool { return tt.sockets[path] },
			)
			if got != tt.want || found != tt.wantFound {
				t.Fatalf("resolvePodmanEndpoint() = (%q, %t), want (%q, %t)", got, found, tt.want, tt.wantFound)
			}
		})
	}
}

func TestPodmanRequest(t *testing.T) {
	t.Parallel()

	request := podmanRequest(testcontainers.ContainerRequest{Image: "example.invalid/image@sha256:deadbeef"})
	if request.ProviderType != testcontainers.ProviderPodman {
		t.Fatalf("provider = %v, want ProviderPodman", request.ProviderType)
	}
	if !request.Started {
		t.Fatal("request must start the container")
	}
	if request.Image != "example.invalid/image@sha256:deadbeef" {
		t.Fatalf("image = %q, want preserved request image", request.Image)
	}
}
