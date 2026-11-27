package integrationrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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

func TestIntegrationOwnedLifecycle(t *testing.T) {
	for _, test := range []struct{ scenario, mode string }{
		{"haproxy", "healthy"},
		{"haproxy", "partial up"},
		{"haproxy", "existing project"},
		{"haproxy", "foreign backend"},
		{"haproxy", "foreign cleanup"},
		{"haproxy", "wrong provider"},
		{"observability", "healthy"},
		{"observability", "partial up"},
		{"observability", "existing project"},
		{"observability", "foreign cleanup"},
	} {
		t.Run(test.scenario+"/"+test.mode, func(t *testing.T) {
			mode := test.mode
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			project := "gobfd-haproxy-health"
			if test.scenario == "observability" {
				project = "gobfd-observability"
			}
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
			installHAProxyPodmanFixture(t, directory)
			for key, value := range map[string]string{
				"PATH": directory, "XDG_RUNTIME_DIR": directory, "GOBFD_BUILD_REVISION": strings.Repeat("a", 40),
				"GOBFD_HAP_PROJECT": project, "GOBFD_HAP_ROOT": directory, "GOBFD_HAP_MODE": mode,
				"GOBFD_HAP_STATE": state, "GOBFD_HAP_NETWORK_STATE": networkState, "GOBFD_HAP_TRACE": trace,
				"GOBFD_HAP_MONITOR": monitor, "GOBFD_HAP_SIDECAR": sidecar, "GOBFD_HAP_NETWORK": network,
				"GOBFD_HAP_CAPTURE":       capture,
				"GORACE":                  "atexit_sleep_ms=0",
				"PODMAN_COMPOSE_PROVIDER": "fixture-compose-provider", "DOCKER_BUILDKIT": "1",
			} {
				t.Setenv(key, value)
			}
			previous := http.DefaultTransport
			http.DefaultTransport = haproxyRoundTrip(func(request *http.Request) (*http.Response, error) {
				body := "nginx"
				if test.scenario == "observability" {
					body = `{"status":"success","data":{"activeTargets":[{"health":"up"}]}}`
				}
				return &http.Response{
					StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)),
					Header: make(http.Header), Request: request,
				}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			synctest.Test(t, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				err := Run(t.Context(), directory, []string{test.scenario}, &stdout, &stderr)
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
				imagePath := trace + ".image.json"
				_, imageErr := os.Stat(imagePath)
				if mode == "foreign cleanup" {
					if imageErr != nil {
						t.Errorf("foreign cleanup lost retained image state: %v", imageErr)
					}
				} else if !errors.Is(imageErr, os.ErrNotExist) {
					t.Errorf("owned image not cleaned: %v", imageErr)
				}
				if test.scenario == "observability" && mode != "existing project" {
					captures, err := filepath.Glob(filepath.Join(directory, "reports/e2e/observability-compose/run.*/bfd.pcapng"))
					if err != nil || len(captures) != 1 {
						t.Fatalf("retained capture files=%v error=%v", captures, err)
					}
					data, err := os.ReadFile(captures[0])
					if err != nil || string(data) != "retained capture fixture" {
						t.Fatalf("capture was lost during cleanup: %q %v", data, err)
					}
					if strings.Contains(string(commands), "--volumes") ||
						strings.Contains(string(commands), "stop frr-observability") {
						t.Errorf("observability used unowned lifecycle commands: %s", commands)
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
			installHAProxyPodmanFixture(t, directory)
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

func TestMain(m *testing.M) {
	if root := os.Getenv("GOBFD_K8S_NAMESPACE_FIXTURE"); root != "" && filepath.Base(os.Args[0]) == "kubectl" {
		code, handled := runKubernetesNamespaceFixture(root, os.Args[1:])
		if !handled {
			os.Exit(125)
		}
		os.Exit(code)
	}
	if os.Getenv("GOBFD_K8S_PROBE_FIXTURE") != "" && filepath.Base(os.Args[0]) == "kubectl" {
		os.Exit(runKubernetesProbeFixture())
	}
	if os.Getenv("GOBFD_K8S_BUILD_FIXTURE") != "" &&
		(filepath.Base(os.Args[0]) == "podman" || filepath.Base(os.Args[0]) == "kubectl") {
		os.Exit(runKubernetesBuildFixture())
	}
	if os.Getenv("GOBFD_K8S_IMPORT_FIXTURE") != "" && filepath.Base(os.Args[0]) == "podman" {
		os.Exit(runKubernetesImportFixture())
	}
	if filepath.Base(os.Args[0]) == "podman" {
		if os.Getenv("GOBFD_HAP_TRACE") != "" {
			os.Exit(runHAProxyOwnedPodmanFixture())
		}
		if os.Getenv("GOBFD_HAPROXY_FIXTURE_TRACE") != "" {
			os.Exit(runHAProxyRecoveryPodmanFixture())
		}
	}
	os.Exit(m.Run())
}

func installHAProxyPodmanFixture(t *testing.T, directory string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	if err := os.Symlink(executable, filepath.Join(directory, "podman")); err != nil {
		t.Fatalf("install Podman test-binary fixture: %v", err)
	}
}

func appendHAProxyFixtureTrace(path string, arguments []string) error {
	trace, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(trace, strings.Join(arguments, " "))
	return errors.Join(writeErr, trace.Close())
}

func writeHAProxyFixtureOutput(output string) int {
	if _, err := fmt.Fprint(os.Stdout, output); err != nil {
		return 125
	}
	return 0
}

func writeHAProxyFixtureState(name, state string) int {
	if err := os.WriteFile(os.Getenv(name), []byte(state), 0o600); err != nil {
		return 125
	}
	return 0
}

func haproxyFixtureArgument(arguments []string, index int) string {
	if index < 0 || index >= len(arguments) {
		return ""
	}
	return arguments[index]
}

func haproxyFixtureID(name, prefix string) string {
	switch name {
	case "gobfd-monitor", "gobfd-observability":
		return os.Getenv(prefix + "MONITOR")
	case "backend1-bfd", "frr-observability":
		return os.Getenv(prefix + "SIDECAR")
	case "tshark-haproxy", "tshark-observability":
		return os.Getenv(prefix + "CAPTURE")
	default:
		return ""
	}
}

type haproxyOwnedFixture struct {
	arguments []string
	state     string
	network   string
}

func runHAProxyOwnedPodmanFixture() int {
	arguments := os.Args[1:]
	if err := appendHAProxyFixtureTrace(os.Getenv("GOBFD_HAP_TRACE"), arguments); err != nil {
		return 125
	}
	state, err := os.ReadFile(os.Getenv("GOBFD_HAP_STATE"))
	if err != nil {
		return 125
	}
	network, err := os.ReadFile(os.Getenv("GOBFD_HAP_NETWORK_STATE"))
	if err != nil {
		return 125
	}
	fixture := haproxyOwnedFixture{
		arguments: arguments, state: strings.TrimSpace(string(state)), network: strings.TrimSpace(string(network)),
	}
	return fixture.run()
}

func (f haproxyOwnedFixture) run() int {
	switch haproxyFixtureArgument(f.arguments, 0) {
	case "info":
		return writeHAProxyFixtureOutput(`{"hostname":"fake-haproxy","graph_root":"/fake/storage","run_root":"/fake/run"}`)
	case "image":
		return f.imageCommand()
	case "ps":
		return writeHAProxyFixtureOutput(f.containerList())
	case "network":
		return f.networkCommand()
	case "volume":
		if haproxyFixtureArgument(f.arguments, 1) == "ls" {
			return 0
		}
	case "container":
		return f.containerExists()
	case "inspect":
		return f.inspect()
	case "compose":
		return f.compose()
	case "build":
		return f.build()
	case "rm":
		return f.remove()
	case "exec":
		return f.exec()
	case "pause", "unpause", "stop", "start":
		if haproxyFixtureArgument(f.arguments, 1) == os.Getenv("GOBFD_HAP_SIDECAR") {
			return 0
		}
		return 70
	}
	return 65
}

func (f haproxyOwnedFixture) containerList() string {
	var output strings.Builder
	for _, entry := range []struct{ marker, name string }{
		{"a", "MONITOR"}, {"b", "SIDECAR"}, {"d", "CAPTURE"},
	} {
		if strings.Contains(f.state, entry.marker) {
			output.WriteString(os.Getenv("GOBFD_HAP_" + entry.name))
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func (f haproxyOwnedFixture) networkCommand() int {
	switch haproxyFixtureArgument(f.arguments, 1) {
	case "ls":
		if f.network == "" {
			return 0
		}
		return writeHAProxyFixtureOutput(os.Getenv("GOBFD_HAP_NETWORK") + "\n")
	case "rm":
		return writeHAProxyFixtureState("GOBFD_HAP_NETWORK_STATE", "")
	default:
		return 65
	}
}

func (f haproxyOwnedFixture) containerExists() int {
	name := haproxyFixtureArgument(f.arguments, len(f.arguments)-1)
	if os.Getenv("GOBFD_HAP_MODE") == "foreign backend" && name == "backend2" {
		return 0
	}
	for _, entry := range []struct{ marker, name string }{
		{"a", "MONITOR"}, {"b", "SIDECAR"}, {"d", "CAPTURE"},
	} {
		if name == os.Getenv("GOBFD_HAP_"+entry.name) && strings.Contains(f.state, entry.marker) {
			return 0
		}
	}
	if haproxyFixtureID(name, "GOBFD_HAP_") != "" && f.state != "" {
		return 0
	}
	return 1
}

func (f haproxyOwnedFixture) inspect() int {
	joined := strings.Join(f.arguments, " ")
	name := haproxyFixtureArgument(f.arguments, len(f.arguments)-1)
	switch {
	case strings.Contains(joined, "{{json .}}"):
		owner := os.Getenv("GOBFD_HAP_PROJECT")
		if os.Getenv("GOBFD_HAP_MODE") == "foreign cleanup" {
			owner = "foreign-project"
		}
		return writeHAProxyFixtureOutput(fmt.Sprintf(
			"{\"Id\":%q,\"Config\":{\"Labels\":{\"com.docker.compose.project\":%q}},\"Mounts\":[]}\n", name, owner,
		))
	case strings.Contains(joined, "{{.ID}}|"):
		id := haproxyFixtureID(name, "GOBFD_HAP_")
		if id == "" {
			return 65
		}
		return writeHAProxyFixtureOutput(id + "|" + os.Getenv("GOBFD_HAP_PROJECT") + "\n")
	default:
		return writeHAProxyFixtureOutput("foreign-project\n")
	}
}

func (f haproxyOwnedFixture) compose() int {
	if os.Getenv("DOCKER_BUILDKIT") != "0" || os.Getenv("PODMAN_COMPOSE_PROVIDER") != "fixture-compose-provider" {
		return 71
	}
	if haproxyFixtureArgument(f.arguments, 1) == "version" {
		version := "5.8.0\n"
		if os.Getenv("GOBFD_HAP_MODE") == "wrong provider" {
			version = "5.5.0\n"
		}
		return writeHAProxyFixtureOutput(version)
	}
	if haproxyFixtureArgument(f.arguments, 1) != "-p" ||
		haproxyFixtureArgument(f.arguments, 2) != os.Getenv("GOBFD_HAP_PROJECT") ||
		haproxyFixtureArgument(f.arguments, 3) != "-f" {
		return 65
	}
	if len(f.arguments) < 5 {
		return 65
	}
	action := f.arguments[5:]
	overrideValidated := false
	if len(action) >= 2 && action[0] == "-f" {
		data, err := os.ReadFile(action[1])
		if err != nil {
			return 65
		}
		var state struct {
			Services map[string]struct {
				Image      string `json:"image"`
				PullPolicy string `json:"pull_policy"`
			} `json:"services"`
		}
		if json.Unmarshal(data, &state) != nil || len(state.Services) != 1 {
			return 65
		}
		image, err := readHAProxyFixtureImage()
		if err != nil || state.Services["monitor"].Image != image.ID || state.Services["monitor"].PullPolicy != "never" {
			return 65
		}
		action = action[2:]
		overrideValidated = true
	}
	switch haproxyFixtureArgument(action, 0) {
	case "config":
		return writeHAProxyFixtureOutput(fmt.Sprintf(
			"{\"services\":{\"monitor\":{\"build\":{\"context\":%q,\"dockerfile\":\"Containerfile\"}}}}\n",
			os.Getenv("GOBFD_HAP_ROOT"),
		))
	case "up":
		if !overrideValidated ||
			haproxyFixtureArgument(action, 1) != "-d" ||
			haproxyFixtureArgument(action, 2) != "--no-build" {
			return 65
		}
		if code := writeHAProxyFixtureState("GOBFD_HAP_STATE", "abd\n"); code != 0 {
			return code
		}
		if code := writeHAProxyFixtureState("GOBFD_HAP_NETWORK_STATE", "1\n"); code != 0 {
			return code
		}
		if os.Getenv("GOBFD_HAP_PROJECT") == "gobfd-observability" {
			captureDir := os.Getenv("GOBFD_OBSERVABILITY_CAPTURE_DIR")
			info, err := os.Lstat(captureDir)
			if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
				return 72
			}
			if os.WriteFile(filepath.Join(captureDir, "bfd.pcapng"), []byte("retained capture fixture"), 0o600) != nil {
				return 72
			}
		}
		if os.Getenv("GOBFD_HAP_MODE") == "partial up" {
			return 27
		}
		return 0
	default:
		return 65
	}
}

func (f haproxyOwnedFixture) build() int {
	joined := " " + strings.Join(f.arguments, " ") + " "
	for _, check := range []struct {
		value string
		code  int
	}{
		{" --cpu-period 100000 --cpu-quota 200000 ", 66},
		{" --memory 2g --memory-swap 2g --jobs 1 ", 66},
		{" --build-arg GIT_COMMIT=" + strings.Repeat("a", 40) + " ", 67},
	} {
		if !strings.Contains(joined, check.value) {
			return check.code
		}
	}
	labels := make(map[string]string)
	alias := ""
	for index := 0; index+1 < len(f.arguments); index++ {
		switch f.arguments[index] {
		case "--tag":
			alias = f.arguments[index+1]
		case "--label":
			key, value, ok := strings.Cut(f.arguments[index+1], "=")
			if !ok {
				return 68
			}
			labels[key] = value
		}
	}
	if !strings.HasPrefix(alias, "localhost/gobfd-interop-"+os.Getenv("GOBFD_HAP_PROJECT")+"-monitor:") ||
		labels["io.gobfd.image"] != alias || labels["io.gobfd.run"] == "" || labels["io.gobfd.test"] != "interop-compose" {
		return 68
	}
	image := haproxyFixtureImage{ID: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(alias))), Labels: labels}
	data, err := json.Marshal(image)
	if err != nil || os.WriteFile(os.Getenv("GOBFD_HAP_TRACE")+".image.json", data, 0o600) != nil {
		return 125
	}
	return 0
}

type haproxyFixtureImage struct {
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

func readHAProxyFixtureImage() (haproxyFixtureImage, error) {
	data, err := os.ReadFile(os.Getenv("GOBFD_HAP_TRACE") + ".image.json")
	if err != nil {
		return haproxyFixtureImage{}, err
	}
	var image haproxyFixtureImage
	if err := json.Unmarshal(data, &image); err != nil {
		return haproxyFixtureImage{}, err
	}
	return image, nil
}

func (f haproxyOwnedFixture) imageCommand() int {
	if haproxyFixtureArgument(f.arguments, 1) == "ls" {
		return 0
	}
	image, err := readHAProxyFixtureImage()
	if errors.Is(err, os.ErrNotExist) {
		return 1
	}
	if err != nil {
		return 125
	}
	reference := haproxyFixtureArgument(f.arguments, len(f.arguments)-1)
	if reference != image.ID && reference != image.Labels["io.gobfd.image"] {
		return 1
	}
	switch haproxyFixtureArgument(f.arguments, 1) {
	case "exists":
		return 0
	case "inspect":
		if encErr := json.NewEncoder(os.Stdout).Encode(image); encErr != nil {
			return 125
		}
		return 0
	case "rm":
		if reference != image.ID || haproxyFixtureArgument(f.arguments, 2) != "--no-prune" {
			return 65
		}
		if os.Remove(os.Getenv("GOBFD_HAP_TRACE")+".image.json") != nil {
			return 125
		}
		return 0
	default:
		return 65
	}
}

func (f haproxyOwnedFixture) remove() int {
	name := haproxyFixtureArgument(f.arguments, len(f.arguments)-1)
	marker := ""
	for _, entry := range []struct{ marker, name string }{
		{"a", "MONITOR"}, {"b", "SIDECAR"}, {"d", "CAPTURE"},
	} {
		if name == os.Getenv("GOBFD_HAP_"+entry.name) {
			marker = entry.marker
		}
	}
	if marker == "" {
		return 65
	}
	return writeHAProxyFixtureState("GOBFD_HAP_STATE", strings.Replace(f.state, marker, "", 1)+"\n")
}

func (f haproxyOwnedFixture) exec() int {
	switch haproxyFixtureArgument(f.arguments, 1) {
	case os.Getenv("GOBFD_HAP_MONITOR"):
		if haproxyFixtureArgument(f.arguments, 2) == "/bin/gobfdctl" &&
			haproxyFixtureArgument(f.arguments, 5) == "session" && haproxyFixtureArgument(f.arguments, 6) == "list" {
			return 0
		}
	case os.Getenv("GOBFD_HAP_CAPTURE"):
		if haproxyFixtureArgument(f.arguments, 2) == "tshark" {
			return 0
		}
	}
	return 69
}

func runHAProxyRecoveryPodmanFixture() int {
	arguments := os.Args[1:]
	if err := appendHAProxyFixtureTrace(os.Getenv("GOBFD_HAPROXY_FIXTURE_TRACE"), arguments); err != nil {
		return 125
	}
	joined := strings.Join(arguments, " ")
	monitor := os.Getenv("GOBFD_HAPROXY_FIXTURE_MONITOR")
	sidecar := os.Getenv("GOBFD_HAPROXY_FIXTURE_SIDECAR")
	capture := os.Getenv("GOBFD_HAPROXY_FIXTURE_CAPTURE")
	switch {
	case strings.HasPrefix(joined, "container exists "):
		return 0
	case strings.HasPrefix(joined, "inspect --type container --format "):
		id := haproxyFixtureID(haproxyFixtureArgument(arguments, len(arguments)-1), "GOBFD_HAPROXY_FIXTURE_")
		if id == "" {
			return 65
		}
		return writeHAProxyFixtureOutput(id + "|gobfd-haproxy-health\n")
	case joined == "exec "+monitor+" /bin/gobfdctl --addr 127.0.0.1:50052 session list":
		return haproxyFixtureExitCode("CLI")
	case joined == "exec gobfd-monitor gobfdctl sessions" || joined == "exec gobfd-monitor /bin/gobfdctl sessions":
		return 64
	case joined == "pause "+sidecar:
		return haproxyFixtureExitCode("PAUSE")
	case joined == "unpause "+sidecar:
		return haproxyFixtureExitCode("UNPAUSE")
	case strings.HasPrefix(joined, "exec "+capture+" tshark "):
		return haproxyFixtureExitCode("CAPTURE")
	default:
		return 65
	}
}

func haproxyFixtureExitCode(stage string) int {
	code, err := strconv.Atoi(os.Getenv("GOBFD_HAPROXY_FIXTURE_" + stage + "_EXIT"))
	if err != nil {
		return 125
	}
	return code
}
