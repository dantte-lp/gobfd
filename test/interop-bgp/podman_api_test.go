//go:build interop_bgp

package interop_bgp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// podmanSocketPath is the default Podman API socket.
const podmanSocketPath = "/run/podman/podman.sock"

// podmanHTTPClient returns an HTTP client that connects via the Podman unix socket.
func podmanHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", podmanSocketPath)
			},
		},
	}
}

// podmanAPIURL builds a URL for the Podman REST API (compat endpoint).
func podmanAPIURL(path string) string {
	return "http://d/v5.0.0" + path
}

// containerExec runs a command inside a container via the Podman API and returns stdout.
func containerExec(ctx context.Context, container string, command ...string) (string, error) {
	client := podmanHTTPClient()

	// Step 1: Create exec instance.
	createBody := map[string]any{
		"Cmd":          command,
		"AttachStdout": true,
		"AttachStderr": true,
	}
	bodyJSON, err := json.Marshal(createBody)
	if err != nil {
		return "", fmt.Errorf("marshal exec create body: %w", err)
	}

	createReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		podmanAPIURL("/containers/"+container+"/exec"),
		bytes.NewReader(bodyJSON),
	)
	if err != nil {
		return "", fmt.Errorf("create exec request: %w", err)
	}
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(createReq)
	if err != nil {
		return "", fmt.Errorf("exec create request to %s: %w", container, err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(createResp.Body)
		return "", fmt.Errorf("exec create %s: status %d: %s", container, createResp.StatusCode, respBody)
	}

	var createResult struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&createResult); err != nil {
		return "", fmt.Errorf("decode exec create response: %w", err)
	}

	// Step 2: Start exec and capture output.
	startBody := map[string]any{
		"Detach": false,
	}
	startJSON, err := json.Marshal(startBody)
	if err != nil {
		return "", fmt.Errorf("marshal exec start body: %w", err)
	}

	startReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		podmanAPIURL("/exec/"+createResult.ID+"/start"),
		bytes.NewReader(startJSON),
	)
	if err != nil {
		return "", fmt.Errorf("create exec start request: %w", err)
	}
	startReq.Header.Set("Content-Type", "application/json")

	startResp, err := client.Do(startReq)
	if err != nil {
		return "", fmt.Errorf("exec start request: %w", err)
	}
	defer startResp.Body.Close()

	// The response is a multiplexed stream (Docker stream protocol).
	// Each frame: [8 bytes header][payload]
	// Header: [stream_type(1)][0(3)][size(4)]
	output, err := demuxDockerStream(startResp.Body)
	if err != nil {
		return "", fmt.Errorf("read exec output from %s: %w", container, err)
	}

	// Step 3: Check exit code.
	inspectReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		podmanAPIURL("/exec/"+createResult.ID+"/json"),
		nil,
	)
	if err != nil {
		return output, nil //nolint:nilerr // best-effort exit code check
	}

	inspectResp, err := client.Do(inspectReq)
	if err != nil {
		return output, nil //nolint:nilerr // best-effort exit code check
	}
	defer inspectResp.Body.Close()

	var inspectResult struct {
		ExitCode int `json:"ExitCode"`
	}
	if err := json.NewDecoder(inspectResp.Body).Decode(&inspectResult); err == nil {
		if inspectResult.ExitCode != 0 {
			return output, fmt.Errorf("exec in %s exited with code %d: %s",
				container, inspectResult.ExitCode, output)
		}
	}

	return output, nil
}

// demuxDockerStream reads the Docker multiplexed stream protocol.
// Each frame: [stream_type(1 byte)][padding(3 bytes)][size(4 bytes BE)][payload].
func demuxDockerStream(r io.Reader) (string, error) {
	var buf strings.Builder
	header := make([]byte, 8)

	for {
		_, err := io.ReadFull(r, header)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return buf.String(), err
		}

		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size == 0 {
			continue
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return buf.String(), err
		}

		buf.Write(payload)
	}

	return buf.String(), nil
}

// containerStop stops a container via the Podman API.
func containerStop(ctx context.Context, container string) error {
	client := podmanHTTPClient()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		podmanAPIURL("/containers/"+container+"/stop?t=10"),
		nil,
	)
	if err != nil {
		return fmt.Errorf("create stop request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("stop %s: %w", container, err)
	}
	defer resp.Body.Close()

	// 204 = stopped, 304 = already stopped.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stop %s: status %d: %s", container, resp.StatusCode, body)
	}
	return nil
}

// containerStart starts a container via the Podman API.
func containerStart(ctx context.Context, container string) error {
	client := podmanHTTPClient()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		podmanAPIURL("/containers/"+container+"/start"),
		nil,
	)
	if err != nil {
		return fmt.Errorf("create start request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("start %s: %w", container, err)
	}
	defer resp.Body.Close()

	// 204 = started, 304 = already running.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("start %s: status %d: %s", container, resp.StatusCode, body)
	}
	return nil
}

// containerPause pauses a container via the Podman API.
func containerPause(ctx context.Context, container string) error {
	client := podmanHTTPClient()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		podmanAPIURL("/containers/"+container+"/pause"),
		nil,
	)
	if err != nil {
		return fmt.Errorf("create pause request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pause %s: %w", container, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pause %s: status %d: %s", container, resp.StatusCode, body)
	}
	return nil
}

// containerUnpause unpauses a container via the Podman API.
func containerUnpause(ctx context.Context, container string) error {
	client := podmanHTTPClient()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		podmanAPIURL("/containers/"+container+"/unpause"),
		nil,
	)
	if err != nil {
		return fmt.Errorf("create unpause request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unpause %s: %w", container, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unpause %s: status %d: %s", container, resp.StatusCode, body)
	}
	return nil
}

// containerLogs returns the last N lines of container logs via the Podman API.
func containerLogs(ctx context.Context, container string, tail int) (string, error) {
	client := podmanHTTPClient()
	url := fmt.Sprintf("/containers/%s/logs?stdout=true&stderr=true&tail=%d", container, tail)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, podmanAPIURL(url), nil)
	if err != nil {
		return "", fmt.Errorf("create logs request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("logs %s: %w", container, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("logs %s: status %d: %s", container, resp.StatusCode, body)
	}

	output, err := demuxDockerStream(resp.Body)
	if err != nil {
		return output, nil //nolint:nilerr // partial output is OK
	}
	return output, nil
}
