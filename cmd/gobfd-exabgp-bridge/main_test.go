package main

import (
	"bytes"
	"errors"
	"io"
	"syscall"
	"testing"
)

const (
	testPeerAddr   = "10.0.0.1"
	testPrefixAddr = "198.51.100.1/32"
)

func TestRouteStateReconcilesAcrossDisconnects(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	route := routeState{output: &output, prefix: testPrefixAddr}
	// Startup withdrawal also repairs a previous bridge process's announcement.
	for _, up := range []bool{false, false, true, true, false, false, true, false} {
		if err := route.apply(up); err != nil {
			t.Fatal(err)
		}
	}
	want := "withdraw route 198.51.100.1/32 next-hop self\n" +
		"announce route 198.51.100.1/32 next-hop self\n" +
		"withdraw route 198.51.100.1/32 next-hop self\n" +
		"announce route 198.51.100.1/32 next-hop self\n" +
		"withdraw route 198.51.100.1/32 next-hop self\n"
	if output.String() != want {
		t.Fatalf("commands = %q, want %q", output.String(), want)
	}
}

type failingOutput struct{ err error }

func (w failingOutput) Write(p []byte) (int, error) { return len(p) - 1, w.err }

func TestRouteStateDoesNotConfirmFailedWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		err, want error
	}{
		{name: "EPIPE", err: syscall.EPIPE, want: syscall.EPIPE},
		{name: "short write", want: io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			route := routeState{output: failingOutput{err: tc.err}, prefix: testPrefixAddr}
			if err := route.apply(true); !errors.Is(err, tc.want) {
				t.Fatalf("write error = %v, want %v", err, tc.want)
			}
			if route.known || route.announced {
				t.Fatal("failed write confirmed a route announcement")
			}
			route.known, route.announced = true, true
			if err := route.apply(false); !errors.Is(err, tc.want) {
				t.Fatalf("withdraw error = %v, want %v", err, tc.want)
			}
			if !route.known || !route.announced {
				t.Fatal("failed withdrawal changed applied state")
			}
		})
	}
}

func TestEnvOrDefault_EnvSet(t *testing.T) {
	t.Setenv("TEST_BRIDGE_ENV", "custom-value")
	if got := envOrDefault("TEST_BRIDGE_ENV", "fallback"); got != "custom-value" {
		t.Fatalf("value = %q", got)
	}
}

func TestEnvOrDefault_EnvUnset(t *testing.T) {
	t.Setenv("TEST_BRIDGE_ENV", "")
	if got := envOrDefault("TEST_BRIDGE_ENV", "fallback"); got != "fallback" {
		t.Fatalf("value = %q", got)
	}
}
