package main

import "testing"

// gh-87: the API and metrics listeners have no authentication, so a bind
// that is reachable beyond loopback is reported at startup.
func TestUnauthenticatedBindIsExposed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		addr string
		want bool
	}{
		{":50051", true},
		{"0.0.0.0:9100", true},
		{"[::]:9100", true},
		{"192.0.2.1:9100", true},
		{"127.0.0.1:9100", false},
		{"127.0.0.2:9100", false},
		{"[::1]:50051", false},
		{"localhost:9100", false},
		{"bogus", true},
	} {
		if got := exposedBeyondLoopback(tc.addr); got != tc.want {
			t.Errorf("exposedBeyondLoopback(%q) = %t, want %t", tc.addr, got, tc.want)
		}
	}
}
