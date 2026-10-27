package integrationrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
)

type haproxyRoundTrip func(*http.Request) (*http.Response, error)

func (f haproxyRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHAProxyRequiredChecksAndRecovery(t *testing.T) {
	for name, test := range map[string]struct {
		cliExit, pauseExit, unpauseExit, captureExit int
		failRequest, status                          int
		body                                         string
		cancelAfterPause                             bool
		wantError                                    bool
		wantCause                                    error
		wantExit, wantUnpauses                       int
	}{
		"healthy":                            {wantUnpauses: 1},
		"required RPC fails before mutation": {cliExit: 23, wantError: true, wantExit: 23},
		"required traffic status fails before mutation": {
			failRequest: 1, status: http.StatusServiceUnavailable, wantError: true, wantCause: errHTTPStatus,
		},
		"required traffic body fails before mutation": {failRequest: 1, body: "unrelated service", wantError: true},
		"traffic failure restores paused sidecar": {
			failRequest: 2, status: http.StatusServiceUnavailable, wantError: true,
			wantCause: errHTTPStatus, wantUnpauses: 1,
		},
		"cancellation restores paused sidecar": {
			cancelAfterPause: true, wantError: true, wantCause: context.Canceled, wantUnpauses: 1,
		},
		"failed pause still attempts restoration": {pauseExit: 26, wantError: true, wantExit: 26, wantUnpauses: 1},
		"failed unpause propagates and cleanup retries once": {
			unpauseExit: 24, wantError: true, wantExit: 24, wantUnpauses: 2,
		},
		"optional packet capture fails": {captureExit: 25, wantUnpauses: 1},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			tracePath := filepath.Join(directory, "commands")
			const fixture = `#!/bin/sh
printf '%s\n' "$*" >> "$GOBFD_HAPROXY_FIXTURE_TRACE"
case "$*" in
  'exec gobfd-monitor /bin/gobfdctl --addr 127.0.0.1:50052 session list') exit "$GOBFD_HAPROXY_FIXTURE_CLI_EXIT" ;;
  'exec gobfd-monitor gobfdctl sessions'|'exec gobfd-monitor /bin/gobfdctl sessions') exit 64 ;;
  'pause backend1'|'pause backend1-bfd') exit "$GOBFD_HAPROXY_FIXTURE_PAUSE_EXIT" ;;
  'unpause backend1'|'unpause backend1-bfd') exit "$GOBFD_HAPROXY_FIXTURE_UNPAUSE_EXIT" ;;
  'exec tshark-haproxy tshark '*) exit "$GOBFD_HAPROXY_FIXTURE_CAPTURE_EXIT" ;;
  *) exit 65 ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "podman"), []byte(fixture), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory)
			t.Setenv("GOBFD_HAPROXY_FIXTURE_TRACE", tracePath)
			for key, value := range map[string]int{
				"CLI": test.cliExit, "PAUSE": test.pauseExit, "UNPAUSE": test.unpauseExit, "CAPTURE": test.captureExit,
			} {
				t.Setenv("GOBFD_HAPROXY_FIXTURE_"+key+"_EXIT", strconv.Itoa(value))
			}
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				requests := 0
				var stdout, stderr bytes.Buffer
				r := runner{root: directory, stdout: &stdout, stderr: &stderr, http: &http.Client{
					Transport: haproxyRoundTrip(func(request *http.Request) (*http.Response, error) {
						requests++
						if request.URL.String() != "http://localhost:8080" {
							t.Errorf("unexpected HTTP endpoint %s", request.URL)
						}
						if test.cancelAfterPause && requests == 2 {
							cancel()
							return nil, context.Canceled
						}
						status, body := http.StatusOK, "Welcome to nginx"
						if requests == test.failRequest {
							if test.status != 0 {
								status = test.status
							} else {
								body = test.body
							}
						}
						return &http.Response{
							StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
							Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request,
						}, nil
					}),
				}}
				err := r.haproxy(ctx)
				if (err != nil) != test.wantError {
					t.Errorf("haproxy() error=%v, want error=%v", err, test.wantError)
				}
				if test.wantCause != nil && !errors.Is(err, test.wantCause) {
					t.Errorf("haproxy() error=%v, want cause=%v", err, test.wantCause)
				}
				var exitErr *ExitError
				if test.wantExit != 0 && (!errors.As(err, &exitErr) || exitErr.Code != test.wantExit) {
					t.Errorf("haproxy() error=%v, want command exit=%d", err, test.wantExit)
				}
				trace, readErr := os.ReadFile(tracePath)
				if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal(readErr)
				}
				commands := string(trace)
				if test.failRequest == 1 {
					if commands != "" {
						t.Errorf("failed required traffic check still invoked commands: %s", commands)
					}
				} else if !strings.HasPrefix(commands,
					"exec gobfd-monitor /bin/gobfdctl --addr 127.0.0.1:50052 session list\n",
				) {
					t.Errorf("required management command does not match shipped CLI/TCP endpoint: %s", commands)
				}
				if strings.Contains(commands, "pause backend1\n") {
					t.Errorf("HTTP backend was paused instead of its BFD sidecar: %s", commands)
				}
				if got := strings.Count(commands, "unpause backend1-bfd\n"); got != test.wantUnpauses {
					t.Errorf("restoration count=%d, want %d: %s", got, test.wantUnpauses, commands)
				}
				if test.cliExit != 0 && strings.Contains(commands, "pause ") {
					t.Errorf("failed required RPC check still mutated a container: %s", commands)
				}
				if test.captureExit != 0 && !strings.Contains(stderr.String(), "tshark capture is not available") {
					t.Errorf("optional capture failure was not reported: %s", stderr.String())
				}
			})
		})
	}
}
