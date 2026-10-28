package integrationrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec" //nolint:depguard // Inspect the native exit status of the executed fixture.
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func TestHAProxyOwnedLifecycle(t *testing.T) {
	for _, mode := range []string{
		"healthy", "partial up", "existing project", "foreign backend", "foreign cleanup", "wrong provider",
	} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			const project = "gobfd-haproxy-health"
			const monitor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			const sidecar = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			const network = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			const capture = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			state, networkState, trace := filepath.Join(directory, "state"), filepath.Join(directory, "network"),
				filepath.Join(directory, "trace")
			initial := ""
			if mode == "existing project" {
				initial = "abd"
			}
			for _, path := range []string{state, networkState} {
				if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const fixture = `#!/bin/sh
printf '%s\n' "$*" >> "$GOBFD_HAP_TRACE"
state=; IFS= read -r state < "$GOBFD_HAP_STATE" || :
net=; IFS= read -r net < "$GOBFD_HAP_NETWORK_STATE" || :
last=; for arg do last=$arg; done
case "$1" in
  ps)
    case "$state" in *a*) printf '%s\n' "$GOBFD_HAP_MONITOR";; esac
    case "$state" in *b*) printf '%s\n' "$GOBFD_HAP_SIDECAR";; esac
    case "$state" in *d*) printf '%s\n' "$GOBFD_HAP_CAPTURE";; esac ;;
  network)
    if [ "$2" = ls ]; then [ -z "$net" ] || printf '%s\n' "$GOBFD_HAP_NETWORK";
    elif [ "$2" = rm ]; then : > "$GOBFD_HAP_NETWORK_STATE"; else exit 65; fi ;;
  volume) [ "$2" = ls ] || exit 65 ;;
  container)
    if [ "$GOBFD_HAP_MODE" = 'foreign backend' ] && [ "$last" = backend2 ]; then exit 0; fi
    case "$last" in
      "$GOBFD_HAP_MONITOR") case "$state" in *a*) exit 0;; esac ;;
      "$GOBFD_HAP_SIDECAR") case "$state" in *b*) exit 0;; esac ;;
      "$GOBFD_HAP_CAPTURE") case "$state" in *d*) exit 0;; esac ;;
      gobfd-monitor|backend1-bfd|tshark-haproxy) [ -z "$state" ] || exit 0 ;;
    esac; exit 1 ;;
  inspect)
    case "$*" in
      *'{{json .}}'*)
        owner=$GOBFD_HAP_PROJECT
        [ "$GOBFD_HAP_MODE" != 'foreign cleanup' ] || owner=foreign-project
        printf '{"Id":"%s","Config":{"Labels":{"com.docker.compose.project":"%s"}},"Mounts":[]}\n' "$last" "$owner" ;;
      *'{{.ID}}|'*)
        case "$last" in
          gobfd-monitor) id=$GOBFD_HAP_MONITOR;; backend1-bfd) id=$GOBFD_HAP_SIDECAR;;
          tshark-haproxy) id=$GOBFD_HAP_CAPTURE;; *) exit 65;;
        esac
        printf '%s|%s\n' "$id" "$GOBFD_HAP_PROJECT" ;;
      *) printf 'foreign-project\n' ;;
    esac ;;
  compose)
    [ "$DOCKER_BUILDKIT" = 0 ] && [ "$PODMAN_COMPOSE_PROVIDER" = fixture-compose-provider ] || exit 71
    if [ "$2" = version ]; then
      if [ "$GOBFD_HAP_MODE" = 'wrong provider' ]; then printf '5.5.0\n'; else printf '5.6.0\n'; fi
      exit 0
    fi
    [ "$2" = -p ] && [ "$3" = "$GOBFD_HAP_PROJECT" ] && [ "$4" = -f ] || exit 65
    case "$6" in
      config)
        printf '{"services":{"monitor":{"build":{"context":"%s","dockerfile":"Containerfile"}}}}\n' \
          "$GOBFD_HAP_ROOT" ;;
      up)
        [ "$7" = -d ] && [ "$8" = --no-build ] || exit 65
        printf 'abd\n' > "$GOBFD_HAP_STATE"; printf '1\n' > "$GOBFD_HAP_NETWORK_STATE"
        [ "$GOBFD_HAP_MODE" != 'partial up' ] || exit 27 ;;
      *) exit 65 ;;
    esac ;;
  build)
    case " $* " in *' --cpu-period 100000 --cpu-quota 200000 '*) :;; *) exit 66;; esac
    case " $* " in *' --memory 2g --memory-swap 2g --jobs 1 '*) :;; *) exit 66;; esac
    case " $* " in
      *' --build-arg GIT_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa '*) :;;
      *) exit 67;;
    esac
    case " $* " in *' --label io.gobfd.image=gobfd-haproxy-health-monitor '*) :;; *) exit 68;; esac ;;
  rm)
    case "$last" in
      "$GOBFD_HAP_MONITOR") remove=a;; "$GOBFD_HAP_SIDECAR") remove=b;; "$GOBFD_HAP_CAPTURE") remove=d ;;
      *) exit 65 ;;
    esac
    case "$state" in *"$remove"*) state="${state%%"$remove"*}${state#*"$remove"}";; esac
    printf '%s\n' "$state" > "$GOBFD_HAP_STATE" ;;
  exec)
    case "$2" in
      "$GOBFD_HAP_MONITOR") [ "$3" = /bin/gobfdctl ] && [ "$6 $7" = 'session list' ] || exit 69 ;;
      "$GOBFD_HAP_CAPTURE") [ "$3" = tshark ] || exit 69 ;;
      *) exit 69;;
    esac ;;
  pause|unpause) [ "$2" = "$GOBFD_HAP_SIDECAR" ] || exit 70 ;;
  *) exit 65 ;;
esac
exit 0
`
			if err := os.WriteFile(filepath.Join(directory, "podman"), []byte(fixture), 0o700); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{
				"PATH": directory, "XDG_RUNTIME_DIR": directory, "GOBFD_BUILD_REVISION": strings.Repeat("a", 40),
				"GOBFD_HAP_PROJECT": project, "GOBFD_HAP_ROOT": directory, "GOBFD_HAP_MODE": mode,
				"GOBFD_HAP_STATE": state, "GOBFD_HAP_NETWORK_STATE": networkState, "GOBFD_HAP_TRACE": trace,
				"GOBFD_HAP_MONITOR": monitor, "GOBFD_HAP_SIDECAR": sidecar, "GOBFD_HAP_NETWORK": network,
				"GOBFD_HAP_CAPTURE":       capture,
				"PODMAN_COMPOSE_PROVIDER": "fixture-compose-provider", "DOCKER_BUILDKIT": "1",
			} {
				t.Setenv(key, value)
			}
			previous := http.DefaultTransport
			http.DefaultTransport = haproxyRoundTrip(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("nginx")),
					Header: make(http.Header), Request: request,
				}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			synctest.Test(t, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				err := Run(t.Context(), directory, []string{"haproxy"}, &stdout, &stderr)
				if (err != nil) != (mode != "healthy") {
					t.Errorf("owned lifecycle error=%v for %s", err, mode)
				}
				if mode == "partial up" {
					var commandErr *exec.ExitError
					if !errors.As(err, &commandErr) || commandErr.ExitCode() != 27 {
						t.Errorf("partial start error=%v, want native exit 27", err)
					}
					var exitErr *ExitError
					if !errors.As(err, &exitErr) || exitErr.Code != 27 {
						t.Errorf("partial start error=%v, want integrationctl exit 27", err)
					}
				}
				commands, readErr := os.ReadFile(trace)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !strings.HasPrefix(string(commands), "ps -a --no-trunc --filter label=com.docker.compose.project="+project) {
					t.Errorf("lifecycle did not check exact project ownership first: %s", commands)
				}
				remaining, readErr := os.ReadFile(state)
				if readErr != nil {
					t.Fatal(readErr)
				}
				wantRemaining := mode == "existing project" || mode == "foreign cleanup"
				if (strings.TrimSpace(string(remaining)) != "") != wantRemaining {
					t.Errorf("remaining fixture resources=%q, want preserved=%v: %s", remaining, wantRemaining, commands)
				}
				if wantRemaining || mode == "foreign backend" {
					if strings.Contains(string(commands), "rm -f") || strings.Contains(string(commands), "network rm") {
						t.Errorf("foreign or pre-existing resource was removed: %s", commands)
					}
				} else {
					networkRemaining, networkErr := os.ReadFile(networkState)
					if networkErr != nil || strings.TrimSpace(string(networkRemaining)) != "" {
						t.Errorf("owned network cleanup error=%v, remaining=%q", networkErr, networkRemaining)
					}
				}
			})
		})
	}
}

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
			const monitor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			const sidecar = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			const capture = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			tracePath := filepath.Join(directory, "commands")
			const fixture = `#!/bin/sh
printf '%s\n' "$*" >> "$GOBFD_HAPROXY_FIXTURE_TRACE"
last=; for arg do last=$arg; done
case "$*" in
  'container exists '*) exit 0 ;;
  'inspect --type container --format '* )
    case "$last" in
      gobfd-monitor) id=$GOBFD_HAPROXY_FIXTURE_MONITOR;; backend1-bfd) id=$GOBFD_HAPROXY_FIXTURE_SIDECAR;;
      tshark-haproxy) id=$GOBFD_HAPROXY_FIXTURE_CAPTURE;; *) exit 65;;
    esac
    printf '%s|gobfd-haproxy-health\n' "$id"; exit 0 ;;
  "exec $GOBFD_HAPROXY_FIXTURE_MONITOR /bin/gobfdctl --addr 127.0.0.1:50052 session list")
    exit "$GOBFD_HAPROXY_FIXTURE_CLI_EXIT" ;;
  'exec gobfd-monitor gobfdctl sessions'|'exec gobfd-monitor /bin/gobfdctl sessions') exit 64 ;;
  "pause $GOBFD_HAPROXY_FIXTURE_SIDECAR") exit "$GOBFD_HAPROXY_FIXTURE_PAUSE_EXIT" ;;
  "unpause $GOBFD_HAPROXY_FIXTURE_SIDECAR") exit "$GOBFD_HAPROXY_FIXTURE_UNPAUSE_EXIT" ;;
  "exec $GOBFD_HAPROXY_FIXTURE_CAPTURE tshark "*) exit "$GOBFD_HAPROXY_FIXTURE_CAPTURE_EXIT" ;;
  *) exit 65 ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "podman"), []byte(fixture), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory)
			t.Setenv("GOBFD_HAPROXY_FIXTURE_TRACE", tracePath)
			t.Setenv("GOBFD_HAPROXY_FIXTURE_MONITOR", monitor)
			t.Setenv("GOBFD_HAPROXY_FIXTURE_SIDECAR", sidecar)
			t.Setenv("GOBFD_HAPROXY_FIXTURE_CAPTURE", capture)
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
				controller, controllerErr := interopproject.NewProject(
					directory, "gobfd-haproxy-health", "base", &stdout, &stderr,
				)
				if controllerErr != nil {
					t.Fatal(controllerErr)
				}
				err := r.haproxy(ctx, controller)
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
				} else if !strings.Contains(commands,
					"exec "+monitor+" /bin/gobfdctl --addr 127.0.0.1:50052 session list\n",
				) {
					t.Errorf("required management command does not match shipped CLI/TCP endpoint: %s", commands)
				}
				if strings.Contains(commands, "pause backend1\n") {
					t.Errorf("HTTP backend was paused instead of its BFD sidecar: %s", commands)
				}
				if got := strings.Count(commands, "unpause "+sidecar+"\n"); got != test.wantUnpauses {
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
