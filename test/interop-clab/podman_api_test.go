//go:build interop_clab

package interop_clab_test

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
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", podmanSocketPath)
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
	if decErr := json.NewDecoder(createResp.Body).Decode(&createResult); decErr != nil {
		return "", fmt.Errorf("decode exec create response: %w", decErr)
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

	// Step 3: Check exit code (best-effort — return output regardless).
	inspectReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		podmanAPIURL("/exec/"+createResult.ID+"/json"),
		nil,
	)
	if err != nil {
		return output, nil
	}

	inspectResp, err := client.Do(inspectReq)
	if err != nil {
		return output, nil
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

// containerExists checks if a container exists (any state) via the Podman API.
func containerExists(ctx context.Context, container string) bool {
	client := podmanHTTPClient()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		podmanAPIURL("/containers/"+container+"/json"),
		nil,
	)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
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
