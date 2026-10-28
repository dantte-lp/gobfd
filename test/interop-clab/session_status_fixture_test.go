//go:build interop_clab

package interop_clab_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sessionOracleSocket(t *testing.T, table, document string, exitCode int) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "s.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body []byte
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/exec"):
			var command struct {
				Args []string `json:"Cmd"` //nolint:tagliatelle // Podman wire key.
			}
			if decodeErr := json.NewDecoder(request.Body).Decode(&command); decodeErr != nil {
				t.Error(decodeErr)
				http.Error(w, "invalid command", http.StatusBadRequest)
				return
			}
			body = []byte(`{"Id":"table"}`)
			if slices.Contains(command.Args, "json") ||
				(len(command.Args) != 0 && strings.HasSuffix(command.Args[len(command.Args)-1], " json")) {
				body = []byte(`{"Id":"json"}`)
			}
			w.WriteHeader(http.StatusCreated)
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start"):
			output := table
			if strings.Contains(request.URL.Path, "/json/") {
				output = document
			}
			for _, stream := range []struct {
				kind byte
				text string
			}{{1, output}, {2, "warning: peer 10.0.6.2 Up NeighborDown\n"}} {
				header := make([]byte, 8)
				header[0] = stream.kind
				binary.BigEndian.PutUint32(header[4:], uint32(len(stream.text)))
				body = append(body, header...)
				body = append(body, stream.text...)
			}
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/json"):
			encoded, encodeErr := json.Marshal(struct {
				ExitCode int `json:"ExitCode"` //nolint:tagliatelle // Podman wire key.
			}{ExitCode: exitCode})
			if encodeErr != nil {
				t.Error(encodeErr)
			}
			body = encoded
		default:
			http.NotFound(w, request)
			return
		}
		if _, writeErr := w.Write(body); writeErr != nil {
			t.Error(writeErr)
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		if serveErr := <-done; !errors.Is(serveErr, http.ErrServerClosed) {
			t.Error(serveErr)
		}
	})
	t.Setenv("PODMAN_HOST", "unix://"+socket)
}

func TestGoBFDStateOracle(t *testing.T) {
	for _, test := range []struct {
		name      string
		table     string
		document  string
		wantUp    bool
		wantDown  bool
		wantStuck bool
		wantErr   bool
		peer      vendorPeer
	}{
		{
			name:     "local Down remote Up",
			wantDown: true,
			table:    "1 10.0.6.2 10.0.6.1 single-hop Down Up ControlTimeExpired\n",
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Down","remote_state":"Up"}]`,
		},
		{
			name:  "local AdminDown remote Up",
			table: "1 10.0.6.2 10.0.6.1 single-hop AdminDown Up AdminDown\n",
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"AdminDown","remote_state":"Up"}]`,
		},
		{
			name: "local Up remote Up NeighborDown", wantUp: true,
			table: "1 10.0.6.2 10.0.6.1 single-hop Up Up NeighborDown\n", //nolint:dupword // Local and remote states.
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Up","remote_state":"Up","diagnostic":"NeighborDown"}]`,
		},
		{
			name: "remote AdminDown", wantDown: true, wantStuck: true,
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Down","remote_state":"AdminDown"}]`,
		},
		{
			name: "remote Down local Up",
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Up","remote_state":"Down"}]`,
		},
		{
			name: "similar peer address", wantErr: true,
			document: `[{"peer_address":"10.0.6.20","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Up","remote_state":"Up"}]`,
		},
		{
			name: "wrong local address", wantErr: true,
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.9",
				"type":"single-hop","local_state":"Up","remote_state":"Up"}]`,
		},
		{
			name: "duplicate tuple", wantErr: true,
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Up","remote_state":"Up"},
				{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"single-hop","local_state":"Down","remote_state":"Up"}]`,
		},
		{
			name: "expanded IPv6", wantUp: true,
			peer: vendorPeer{peerIP: "fd00:0:6::1", localIP: "fd00:0:6::"},
			document: `[{"peer_address":"fd00:0000:0006:0000:0000:0000:0000:0001",
				"local_address":"fd00:0000:0006:0000:0000:0000:0000:0000",
				"type":"single-hop","local_state":"Up","remote_state":"Up"}]`,
		},
		{
			name: "wrong session type", wantErr: true,
			document: `[{"peer_address":"10.0.6.2","local_address":"10.0.6.1",
				"type":"multi-hop","local_state":"Up","remote_state":"Up"}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessionOracleSocket(t, test.table, test.document, 0)
			peer := vendorPeer{peerIP: "10.0.6.2", localIP: "10.0.6.1"}
			if test.peer.peerIP != "" {
				peer = test.peer
			}
			up, err := gobfdBFDSessionUp(t.Context(), peer)
			if (err != nil) != test.wantErr || up != test.wantUp {
				t.Fatalf("GoBFD Up=%t error=%v, want Up=%t", up, err, test.wantUp)
			}
			down, err := gobfdBFDSessionDown(t.Context(), peer)
			if (err != nil) != test.wantErr || down != test.wantDown {
				t.Fatalf("GoBFD Down=%t error=%v, want Down=%t", down, err, test.wantDown)
			}
			if stuck := bfdSessionStuck(t.Context(), peer); stuck != test.wantStuck {
				t.Fatalf("GoBFD stuck=%t, want %t", stuck, test.wantStuck)
			}
		})
	}
}

func TestGoBFDStateOracleRejectsInvalidOutput(t *testing.T) {
	for index, document := range []string{
		``, `null`, `[]`, `{}`, `[{`,
		`[{"peer_address":"10.0.6.2","local_address":"10.0.6.1","type":"single-hop",
			"local_state":"Up","local_state":"Down","remote_state":"Up"}]`,
		"[{\"peer_address\":\"10.0.6.2\",\"note\":\"\xff\"}]",
		`[{"peer_address":"10.0.6.2","local_address":"10.0.6.1","type":"single-hop",
			"local_state":"Up","remote_state":"unknown"}]`,
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			sessionOracleSocket(t, "Up NeighborDown", document, 0)
			peer := vendorPeer{peerIP: "10.0.6.2", localIP: "10.0.6.1"}
			if up, err := gobfdBFDSessionUp(t.Context(), peer); err == nil || up {
				t.Fatalf("invalid state output accepted: Up=%t error=%v", up, err)
			}
		})
	}
	t.Run("exec failure", func(t *testing.T) {
		sessionOracleSocket(t, "Up", "[]", 1)
		peer := vendorPeer{peerIP: "10.0.6.2", localIP: "10.0.6.1"}
		if up, err := gobfdBFDSessionUp(t.Context(), peer); err == nil || up {
			t.Fatalf("exec failure accepted: Up=%t error=%v", up, err)
		}
	})
}

func TestFRRStateOracle(t *testing.T) {
	for _, test := range []struct {
		name     string
		document string
		wantUp   bool
		wantErr  bool
	}{
		{
			name: "target Down sibling Up",
			document: `[{"peer":"10.0.6.1","status":"down","multihop":false},
				{"peer":"fd00:0:6::","status":"up","multihop":false}]`,
		},
		{
			name: "exact tuple", wantUp: true,
			document: `[{"peer":"10.0.6.1","local":"10.0.6.2","status":"up","multihop":false}]`,
		},
		{
			name: "local omitted", wantUp: true,
			document: `[{"peer":"10.0.6.1","status":"up","multihop":false}]`,
		},
		{
			name: "wrong local", wantErr: true,
			document: `[{"peer":"10.0.6.1","local":"10.0.6.9","status":"up","multihop":false}]`,
		},
		{
			name: "duplicate peer", wantErr: true,
			document: `[{"peer":"10.0.6.1","status":"up","multihop":false},
				{"peer":"10.0.6.1","status":"down","multihop":false}]`,
		},
		{
			name: "multihop", wantErr: true,
			document: `[{"peer":"10.0.6.1","status":"up","multihop":true}]`,
		},
		{
			name: "missing multihop", wantErr: true,
			document: `[{"peer":"10.0.6.1","status":"up"}]`,
		},
		{name: "missing peer", document: `[]`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessionOracleSocket(t, "peer 10.0.6.1 down; sibling up", test.document, 0)
			peer := vendorPeer{
				baseName: "frr", container: "clab-gobfd-vendors-frr",
				peerIP: "10.0.6.2", localIP: "10.0.6.1",
			}
			up, err := vendorBFDSessionUp(t.Context(), peer)
			if (err != nil) != test.wantErr || up != test.wantUp {
				t.Fatalf("FRR Up=%t error=%v, want Up=%t", up, err, test.wantUp)
			}
		})
	}
}
