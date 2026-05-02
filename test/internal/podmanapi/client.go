// Package podmanapi provides small test-only helpers for the Podman REST API.
package podmanapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	defaultRootfulSocket = "/run/podman/podman.sock"
	defaultAPIBaseURL    = "http://d/v5.0.0"
	maxDockerFrameSize   = 64 << 20
)

var (
	errEmptySocketPath      = errors.New("podman socket path is empty")
	errNoPodmanSocket       = errors.New("podman socket not found")
	errEmptyExecCommand     = errors.New("exec command is empty")
	errExecCreateStatus     = errors.New("exec create returned non-success status")
	errExecCreateIDEmpty    = errors.New("exec create returned empty exec ID")
	errExecNonZero          = errors.New("exec exited with non-zero code")
	errLogsStatus           = errors.New("container logs returned non-success status")
	errInspectStatus        = errors.New("container inspect returned non-success status")
	errListContainersStatus = errors.New("container list returned non-success status")
	errDockerFrameTooLarge  = errors.New("docker stream frame exceeds size limit")
	errContainerActionState = errors.New("container action returned non-success status")
	errReadResponseBody     = errors.New("read response body")
)

// Client is a minimal Podman REST API client for integration tests.
type Client struct {
	httpClient *http.Client
	baseURL    string
	socketPath string
}

// ExecResult captures stdout, stderr, and exit code for a container exec.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ContainerSummary captures the container fields needed by integration tests.
type ContainerSummary struct {
	ID     string            `json:"Id"`     //nolint:tagliatelle // Docker-compatible API field.
	Names  []string          `json:"Names"`  //nolint:tagliatelle // Docker-compatible API field.
	Labels map[string]string `json:"Labels"` //nolint:tagliatelle // Docker-compatible API field.
}

// NewClient creates a Podman REST API client bound to a Unix socket path.
func NewClient(socketPath string) (*Client, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, errEmptySocketPath
	}

	return &Client{
		baseURL:    defaultAPIBaseURL,
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}, nil
}

// NewClientFromEnvironment creates a client from common rootful/rootless sockets.
func NewClientFromEnvironment() (*Client, error) {
	for _, candidate := range socketCandidates() {
		if isSocket(candidate) {
			return NewClient(candidate)
		}
	}
	return nil, errNoPodmanSocket
}

// SocketPath returns the Unix socket used by the client.
func (c *Client) SocketPath() string {
	return c.socketPath
}

// Exec runs argv inside container and returns demultiplexed stdout/stderr.
func (c *Client) Exec(ctx context.Context, container string, argv []string) (ExecResult, error) {
	if len(argv) == 0 {
		return ExecResult{}, errEmptyExecCommand
	}

	execID, err := c.createExec(ctx, container, argv)
	if err != nil {
		return ExecResult{}, err
	}

	result, err := c.startExec(ctx, container, execID)
	if err != nil {
		return result, err
	}
	result.ExitCode = c.execExitCode(ctx, execID)

	if result.ExitCode != 0 {
		return result, fmt.Errorf("%w: container %s code %d: %s%s",
			errExecNonZero, container, result.ExitCode, result.Stdout, result.Stderr)
	}
	return result, nil
}

func (c *Client) createExec(ctx context.Context, container string, argv []string) (string, error) {
	createBody := map[string]any{
		"Cmd":          argv,
		"AttachStdout": true,
		"AttachStderr": true,
	}
	createJSON, err := json.Marshal(createBody)
	if err != nil {
		return "", fmt.Errorf("marshal exec create body: %w", err)
	}

	createResp, err := c.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", createJSON)
	if err != nil {
		return "", fmt.Errorf("exec create request to %s: %w", container, err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		body, err := readBody(createResp.Body, "exec create error body")
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: container %s status %d: %s", errExecCreateStatus, container, createResp.StatusCode, body)
	}

	var created struct {
		ID string `json:"Id"` //nolint:tagliatelle // Docker-compatible API field.
	}
	if decodeErr := json.NewDecoder(createResp.Body).Decode(&created); decodeErr != nil {
		return "", fmt.Errorf("decode exec create response: %w", decodeErr)
	}
	if created.ID == "" {
		return "", fmt.Errorf("%w: container %s", errExecCreateIDEmpty, container)
	}
	return created.ID, nil
}

func (c *Client) startExec(ctx context.Context, container, execID string) (ExecResult, error) {
	startJSON, err := json.Marshal(map[string]any{"Detach": false})
	if err != nil {
		return ExecResult{}, fmt.Errorf("marshal exec start body: %w", err)
	}

	startResp, err := c.doJSON(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/start", startJSON)
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec start request: %w", err)
	}
	defer startResp.Body.Close()

	stdout, stderr, err := DemuxDockerStream(startResp.Body)
	if err != nil {
		return ExecResult{Stdout: stdout, Stderr: stderr}, fmt.Errorf("read exec output from %s: %w", container, err)
	}

	return ExecResult{Stdout: stdout, Stderr: stderr}, nil
}

func (c *Client) execExitCode(ctx context.Context, execID string) int {
	inspectResp, err := c.do(ctx, http.MethodGet, "/exec/"+url.PathEscape(execID)+"/json")
	if err != nil {
		return 0
	}
	defer inspectResp.Body.Close()

	var inspected struct {
		ExitCode int `json:"ExitCode"` //nolint:tagliatelle // Docker-compatible API field.
	}
	if err := json.NewDecoder(inspectResp.Body).Decode(&inspected); err != nil {
		return 0
	}
	return inspected.ExitCode
}

// Logs returns recent container logs via the Docker-compatible API endpoint.
func (c *Client) Logs(ctx context.Context, container string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	path := fmt.Sprintf("/containers/%s/logs?stdout=true&stderr=true&tail=%d",
		url.PathEscape(container), tail)
	resp, err := c.do(ctx, http.MethodGet, path)
	if err != nil {
		return "", fmt.Errorf("logs %s: %w", container, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := readBody(resp.Body, "logs error body")
		if readErr != nil {
			return "", readErr
		}
		return "", fmt.Errorf("%w: container %s status %d: %s", errLogsStatus, container, resp.StatusCode, body)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read logs %s response: %w", container, err)
	}

	stdout, stderr, err := DemuxDockerStream(bytes.NewReader(raw))
	if err != nil {
		return string(raw), nil
	}
	return stdout + stderr, nil
}

// Inspect returns raw JSON container inspection data.
func (c *Client) Inspect(ctx context.Context, container string) (json.RawMessage, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json")
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", container, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := readBody(resp.Body, "inspect error body")
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("%w: container %s status %d: %s", errInspectStatus, container, resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("inspect %s response: %w", container, err)
	}
	return json.RawMessage(body), nil
}

// Containers returns all containers visible through the Podman API socket.
func (c *Client) Containers(ctx context.Context) ([]ContainerSummary, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/json?all=true")
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := readBody(resp.Body, "container list error body")
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("%w: status %d: %s", errListContainersStatus, resp.StatusCode, body)
	}

	var containers []ContainerSummary
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode container list response: %w", err)
	}
	return containers, nil
}

// Exists reports whether the container exists in any state.
func (c *Client) Exists(ctx context.Context, container string) bool {
	_, err := c.Inspect(ctx, container)
	return err == nil
}

// Stop stops a container.
func (c *Client) Stop(ctx context.Context, container string) error {
	return c.containerAction(ctx, container, "stop?t=10", http.StatusNoContent, http.StatusNotModified)
}

// Start starts a container.
func (c *Client) Start(ctx context.Context, container string) error {
	return c.containerAction(ctx, container, "start", http.StatusNoContent, http.StatusNotModified)
}

// Pause pauses a container.
func (c *Client) Pause(ctx context.Context, container string) error {
	return c.containerAction(ctx, container, "pause", http.StatusNoContent)
}

// Unpause unpauses a container.
func (c *Client) Unpause(ctx context.Context, container string) error {
	return c.containerAction(ctx, container, "unpause", http.StatusNoContent)
}

// DemuxDockerStream reads Docker's multiplexed stream format.
func DemuxDockerStream(r io.Reader) (string, string, error) {
	var out, errOut strings.Builder
	header := make([]byte, 8)

	for {
		_, readErr := io.ReadFull(r, header)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				break
			}
			return out.String(), errOut.String(), readErr
		}

		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size == 0 {
			continue
		}
		if size > maxDockerFrameSize {
			return out.String(), errOut.String(), fmt.Errorf("%w: size %d limit %d", errDockerFrameTooLarge, size, maxDockerFrameSize)
		}

		payload := make([]byte, size)
		if _, readErr := io.ReadFull(r, payload); readErr != nil {
			return out.String(), errOut.String(), readErr
		}

		switch header[0] {
		case 1:
			out.Write(payload)
		case 2:
			errOut.Write(payload)
		default:
			out.Write(payload)
		}
	}

	return out.String(), errOut.String(), nil
}

func (c *Client) containerAction(ctx context.Context, container, action string, okStatuses ...int) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/"+action)
	if err != nil {
		return fmt.Errorf("%s %s: %w", action, container, err)
	}
	defer resp.Body.Close()

	if slices.Contains(okStatuses, resp.StatusCode) {
		return nil
	}
	body, err := readBody(resp.Body, "container action error body")
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: action %s container %s status %d: %s", errContainerActionState, action, container, resp.StatusCode, body)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

func (c *Client) do(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

func readBody(r io.Reader, context string) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", errReadResponseBody, context, err)
	}
	return string(body), nil
}

func socketCandidates() []string {
	var candidates []string
	if host := os.Getenv("PODMAN_HOST"); strings.HasPrefix(host, "unix://") {
		candidates = append(candidates, strings.TrimPrefix(host, "unix://"))
	}
	candidates = append(candidates, defaultRootfulSocket)
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidates = append(candidates, filepath.Join(runtimeDir, "podman", "podman.sock"))
	}
	return candidates
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
