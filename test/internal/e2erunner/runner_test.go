package e2erunner

import "testing"

func TestDevServiceImageMatchesComposeV2Naming(t *testing.T) {
	t.Parallel()
	if got, want := devServiceImage("gobfd"), "localhost/gobfd-dev:latest"; got != want {
		t.Fatalf("devServiceImage(gobfd) = %q, want %q", got, want)
	}
}
