//go:build e2e_bgp_failover_testcontainers

package bgp_failover_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBGPFailoverBuildContextContract(t *testing.T) {
	root := repositoryRoot(t)
	contract := newBGPFailoverContract(root)
	if contract.subnet != "172.22.0.0/24" || contract.gobfdIP != "172.22.0.10" ||
		contract.frrIP != "172.22.0.20" || contract.route != "10.20.0.0/24" {
		t.Fatalf("failover contract = %+v, want exact deployment addressing", contract)
	}
	if contract.gobgpImage != "docker.io/jauderho/gobgp:v3.37.0@sha256:"+
		"3bb7304d299c42383c738f5bde2464793e2def9c1ff7fa3f25707a5bb10aee37" {
		t.Fatalf("GoBGP image = %q, want pinned deployment image", contract.gobgpImage)
	}
	if contract.frrImage != "quay.io/frrouting/frr:10.7.0@sha256:"+
		"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68" {
		t.Fatalf("FRR image = %q, want pinned deployment image", contract.frrImage)
	}
	for _, path := range []string{contract.gobfdConfig, contract.gobgpConfig, contract.frrDaemons, contract.frrConfig} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("deployment config %s is unavailable: %v", path, err)
		}
	}

	contextDir := prepareGoBFDBuildContext(t, root)
	if _, err := os.Stat(filepath.Join(contextDir, "cmd/gobfd")); err != nil {
		t.Fatalf("bounded build context lacks cmd/gobfd: %v", err)
	}
	containerfile, err := os.ReadFile(filepath.Join(contextDir, "Containerfile"))
	if err != nil {
		t.Fatalf("read bounded Containerfile: %v", err)
	}
	for _, required := range [][]byte{
		[]byte("docker.io/library/golang:1.27.0-trixie@sha256:"),
		[]byte("CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd"),
	} {
		if !bytes.Contains(containerfile, required) {
			t.Fatalf("bounded Containerfile lacks %q", required)
		}
	}
}

func TestBGPFailoverGoBGPJSONContract(t *testing.T) {
	neighbors := `[{"state":{"neighbor_address":"192.0.2.1","session_state":6}},` +
		`{"state":{"neighbor_address":"172.22.0.20","session_state":6}}]`
	state, err := parseGoBGPNeighborState(neighbors, "172.22.0.20")
	if err != nil {
		t.Fatalf("parse exact GoBGP neighbor: %v", err)
	}
	if state != 6 {
		t.Fatalf("exact GoBGP neighbor state = %d, want Established enum 6", state)
	}
	if _, missingErr := parseGoBGPNeighborState(neighbors, "198.51.100.1"); missingErr == nil {
		t.Fatal("missing exact GoBGP neighbor succeeded")
	}

	present, err := parseGoBGPRIB(`{"10.20.0.0/24":[{"best":true}]}`, "10.20.0.0/24")
	if err != nil || !present {
		t.Fatalf("exact GoBGP RIB route = %t, error=%v, want present", present, err)
	}
	present, err = parseGoBGPRIB(`{"10.200.0.0/24":[{"best":true}]}`, "10.20.0.0/24")
	if err != nil || present {
		t.Fatalf("substring GoBGP RIB route = %t, error=%v, want absent", present, err)
	}
}

func TestBGPFailoverWaitRejectsReadyWithError(t *testing.T) {
	topology := new(bgpFailoverTopology)
	calls := 0
	topology.waitFor(t, "error-bearing result", time.Second, func(context.Context) (bool, error) {
		calls++
		if calls == 1 {
			return true, errors.New("GoBGP RIB query failed")
		}
		return true, nil
	})
	if calls != 2 {
		t.Fatalf("condition calls = %d, want error-bearing ready result rejected", calls)
	}
}

func TestBGPFailoverDiagnosticContract(t *testing.T) {
	reportDir := t.TempDir()
	diagnosticErr := writeBGPFailoverDiagnostic(
		reportDir, "containers.err", strings.Repeat("x", maxDiagnosticBytes+1),
	)
	if diagnosticErr == nil {
		t.Fatal("oversized diagnostic succeeded, want bounded truncation error")
	}
	path := filepath.Join(reportDir, "containers.err")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat bounded diagnostic: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != maxDiagnosticBytes {
		t.Fatalf("bounded diagnostic = mode %s size %d", info.Mode(), info.Size())
	}
	if err := initializeBGPFailoverDiagnostics(reportDir); err != nil {
		t.Fatalf("initialize diagnostics: %v", err)
	}
	for _, name := range []string{"containers.err", "packets.err"} {
		contents, readErr := os.ReadFile(filepath.Join(reportDir, name))
		if readErr != nil {
			t.Fatalf("read initialized %s: %v", name, readErr)
		}
		if len(contents) != 0 {
			t.Fatalf("initialized %s = %q, want empty", name, contents)
		}
	}
}
