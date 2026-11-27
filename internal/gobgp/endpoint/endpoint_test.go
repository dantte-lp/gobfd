package endpoint_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/gobgp/endpoint"
)

func TestLocalUnixSocketPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		wantPath string
		wantOK   bool
	}{
		{name: "triple slash absolute", in: "unix:///node/speaker.sock", wantPath: "/node/speaker.sock", wantOK: true},
		{name: "single slash absolute", in: "unix:/run/gobgp/gobgpd.sock", wantPath: "/run/gobgp/gobgpd.sock", wantOK: true},
		{name: "empty", in: "", wantOK: false},
		{name: "tcp loopback", in: "127.0.0.1:50051", wantOK: false},
		{name: "non-empty authority", in: "unix://node/speaker.sock", wantOK: false},
		{name: "root slash", in: "unix:///", wantOK: false},
		{name: "trailing slash", in: "unix:///node/speaker/", wantOK: false},
		{name: "duplicate slash", in: "unix:////node/speaker.sock", wantOK: false},
		{name: "dot traversal", in: "unix:///node/../speaker.sock", wantOK: false},
		{name: "relative opaque", in: "unix:speaker.sock", wantOK: false},
		{name: "empty opaque", in: "unix:", wantOK: false},
		{name: "abstract scheme", in: "unix-abstract:speaker", wantOK: false},
		{name: "abstract at prefix", in: "unix:@speaker", wantOK: false},
		{name: "query", in: "unix:///node/speaker.sock?k=v", wantOK: false},
		{name: "force query", in: "unix:///node/speaker.sock?", wantOK: false},
		{name: "fragment", in: "unix:///node/speaker.sock#frag", wantOK: false},
		{name: "literal nul", in: "unix:///node/speaker\x00.sock", wantOK: false},
		{name: "escaped nul", in: "unix:///node/speaker%00.sock", wantOK: false},
		{name: "whitespace padded", in: " unix:///node/speaker.sock", wantOK: false},
		{name: "uppercase scheme", in: "UNIX:///node/speaker.sock", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotPath, gotOK := endpoint.LocalUnixSocketPath(tc.in)
			if gotOK != tc.wantOK || gotPath != tc.wantPath {
				t.Fatalf("LocalUnixSocketPath(%q) = (%q, %v), want (%q, %v)",
					tc.in, gotPath, gotOK, tc.wantPath, tc.wantOK)
			}
		})
	}
}
