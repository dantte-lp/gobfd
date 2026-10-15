//go:build interop_bgp || interop_bgp_testcontainers

package interop_bgp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"google.golang.org/protobuf/types/known/timestamppb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

var (
	errIncompleteBFDObservation = errors.New("missing BFD peer or remote discriminator field")
	errIncompleteBGPObservation = errors.New("missing BGP peer or valid uptime")
)

// The CLI view schema is defined in cmd/gobfdctl/commands/format.go.
type bfdObservation struct {
	PeerAddress         string  `json:"peer_address"`
	LocalState          string  `json:"local_state"`
	LocalDiagnostic     string  `json:"local_diagnostic"`
	RemoteKnown         bool    `json:"remote_known"`
	RemoteState         string  `json:"remote_state"`
	RemoteDiagnostic    string  `json:"remote_diagnostic"`
	RemoteDiscriminator *uint32 `json:"remote_discriminator"`
	ObservationVersion  uint64  `json:"observation_version"`
	Cause               string  `json:"cause"`
	DetectionTime       string  `json:"detection_time"`
}

func parseBFDObservation(input []byte, peer string) (bfdObservation, error) {
	var sessions []bfdObservation
	if err := json.Unmarshal(input, &sessions); err != nil {
		return bfdObservation{}, fmt.Errorf("decode gobfdctl sessions JSON: %w", err)
	}
	for _, session := range sessions {
		if session.PeerAddress == peer && session.RemoteDiscriminator != nil {
			return session, nil
		}
	}
	return bfdObservation{}, fmt.Errorf("select peer %s: %w", peer, errIncompleteBFDObservation)
}

func TestParseBFDObservation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		valid bool
	}{
		{name: "expired discriminator", input: `[{"peer_address":"172.21.0.20","remote_discriminator":0}]`, valid: true},
		{
			name:  "duplicate uses last value",
			input: `[{"peer_address":"172.21.0.20","remote_discriminator":1,"remote_discriminator":0}]`,
			valid: true,
		},
		{name: "invalid UTF-8 peer", input: "[{\"peer_address\":\"172.21.0.20\xff\",\"remote_discriminator\":0}]"},
		{name: "missing discriminator", input: `[{"peer_address":"172.21.0.20"}]`},
		{name: "null discriminator", input: `[{"peer_address":"172.21.0.20","remote_discriminator":null}]`},
		{name: "wrong peer", input: `[{"peer_address":"172.21.0.30","remote_discriminator":1}]`},
		{name: "malformed", input: `[`},
		{name: "wrong field type", input: `[{"peer_address":"172.21.0.20","remote_discriminator":"0"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseBFDObservation([]byte(tc.input), frrBGPIP)
			if (err == nil) != tc.valid {
				t.Fatalf("parseBFDObservation = %+v, %v; valid=%t", got, err, tc.valid)
			}
			if tc.valid && *got.RemoteDiscriminator != 0 {
				t.Fatalf("remote discriminator = %d, want explicit zero", *got.RemoteDiscriminator)
			}
		})
	}
}

func readFRRBFDObservation(ctx context.Context, t *testing.T) bfdObservation {
	t.Helper()
	output, err := containerExec(ctx, gobfdBGPContainer,
		"/bin/gobfdctl", "--addr", "127.0.0.1:50052", "session", "list", "--format", "json")
	if err != nil {
		t.Fatalf("read gobfdctl sessions: %v", err)
	}
	observation, err := parseBFDObservation([]byte(output), frrBGPIP)
	if err != nil {
		t.Fatalf("parse FRR observation: %v: raw=%s", err, output)
	}
	return observation
}

// GoBGP's CLI marshals Peer using encoding/json; unrelated capability oneofs cannot round-trip.
type bgpObservation struct {
	State struct {
		NeighborAddress string                          `json:"neighbor_address"`
		AdminState      gobgpapi.PeerState_AdminState   `json:"admin_state"`
		SessionState    gobgpapi.PeerState_SessionState `json:"session_state"`
	} `json:"state"`
	Timers struct {
		State struct {
			Uptime *timestamppb.Timestamp `json:"uptime"`
		} `json:"state"`
	} `json:"timers"`
}

func parseFRRBGPState(input []byte) (bgpObservation, error) {
	var neighbors []bgpObservation
	if err := json.Unmarshal(input, &neighbors); err != nil {
		return bgpObservation{}, fmt.Errorf("decode GoBGP neighbors: %w", err)
	}
	for _, neighbor := range neighbors {
		stamp := neighbor.Timers.State.Uptime
		if neighbor.State.NeighborAddress == frrBGPIP && stamp != nil && stamp.CheckValid() == nil {
			return neighbor, nil
		}
	}
	return bgpObservation{}, fmt.Errorf("select FRR: %w", errIncompleteBGPObservation)
}

func TestParseFRRBGPState(t *testing.T) {
	t.Parallel()
	// Projection of the live v4.10.0 payload that rejected full api.Peer decoding.
	const input = `[{"state":{"neighbor_address":"172.21.0.20","session_state":6,"admin_state":1,
		"remote_cap":[{"Cap":{"MultiProtocol":{"family":{"afi":1,"safi":1}}}}]},
		"timers":{"state":{"uptime":{"seconds":1792105325}}}}]`
	got, err := parseFRRBGPState([]byte(input))
	if err != nil || got.State.AdminState != gobgpapi.PeerState_ADMIN_STATE_UP ||
		got.State.SessionState != gobgpapi.PeerState_SESSION_STATE_ESTABLISHED ||
		got.Timers.State.Uptime.GetSeconds() != 1792105325 {
		t.Fatalf("parse FRR live projection = %+v, %v", got, err)
	}
	for _, invalid := range []string{
		`[]`, `[{"state":{"neighbor_address":"172.21.0.20"}}]`,
		`[{"state":{"neighbor_address":"172.21.0.20"},"timers":{"state":{"uptime":{"nanos":-1}}}}]`,
		`[{"state":{"admin_state":"UP"}}]`, `[] {}`,
	} {
		if _, err := parseFRRBGPState([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid FRR observation: %s", invalid)
		}
	}
}

func readFRRBGPState(ctx context.Context, t *testing.T) bgpObservation {
	t.Helper()
	output, err := gobgpCmd(ctx, "neighbor", "-j")
	if err != nil {
		t.Fatalf("read GoBGP neighbors: %v", err)
	}
	peer, err := parseFRRBGPState([]byte(output))
	if err != nil {
		t.Fatalf("parse FRR BGP state: %v: raw=%s", err, output)
	}
	return peer
}

func requireFRRBGPUnchanged(ctx context.Context, t *testing.T, uptime time.Time) {
	t.Helper()
	peer := readFRRBGPState(ctx, t)
	state := peer.State
	if state.AdminState != gobgpapi.PeerState_ADMIN_STATE_UP ||
		state.SessionState != gobgpapi.PeerState_SESSION_STATE_ESTABLISHED {
		t.Fatalf("FRR BGP changed: admin=%s session=%s", state.AdminState, state.SessionState)
	}
	stamp := peer.Timers.State.Uptime
	if !stamp.AsTime().Equal(uptime) {
		t.Fatalf("FRR BGP uptime changed: got %v, want %s", stamp, uptime)
	}
	exists, err := gobgpRouteExists(ctx, frrRoute)
	if err != nil || !exists {
		t.Fatalf("FRR route %s lost: exists=%t error=%v", frrRoute, exists, err)
	}
}

func configureFRRBFD(ctx context.Context, shutdown bool) error {
	command := "no shutdown"
	if shutdown {
		command = "shutdown"
	}
	output, err := containerExec(ctx, frrContainer, "vtysh",
		"-c", "configure terminal", "-c", "bfd", "-c", "peer "+gobfdBGPIP, "-c", command)
	if err != nil {
		return fmt.Errorf("configure FRR BFD %s: %w: %s", command, err, output)
	}
	return nil
}

// RFC 5882 Sections 3.2 and 4.2 preserve BGP's independent liveness on remote AdminDown.
func TestBGPBFD_FRRRemoteAdminDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	waitFRRBFDUp(ctx, t, bfdUpTimeout)
	waitBGPEstablished(ctx, t, frrBGPIP)
	waitRouteExists(ctx, t, frrRoute)
	baseline := readFRRBFDObservation(ctx, t)
	if baseline.LocalState != "Up" || !baseline.RemoteKnown || baseline.RemoteState != "Up" {
		t.Fatalf("GoBFD baseline is not Up: %+v", baseline)
	}
	stamp := readFRRBGPState(ctx, t).Timers.State.Uptime
	uptime := stamp.AsTime()
	requireFRRBGPUnchanged(ctx, t, uptime)
	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := configureFRRBFD(restoreCtx, false); err != nil {
			t.Errorf("restore FRR BFD: %v", err)
		}
		if t.Failed() {
			dumpGoBGPState(t)
			dumpGoBFDLogs(t)
		}
	})
	started := time.Now()
	if err := configureFRRBFD(ctx, true); err != nil {
		t.Fatalf("shutdown FRR BFD: %v", err)
	}

	adminCtx, cancelAdmin := context.WithTimeout(ctx, 5*time.Second)
	defer cancelAdmin()
	var admin bfdObservation
	for {
		admin = readFRRBFDObservation(adminCtx, t)
		if admin.LocalState == "Down" && admin.RemoteKnown && admin.RemoteState == "AdminDown" {
			break
		}
		select {
		case <-adminCtx.Done():
			t.Fatalf("remote AdminDown not observed: %+v: %v", admin, adminCtx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if admin.RemoteDiagnostic != "AdminDown" || admin.LocalDiagnostic != "NeighborDown" ||
		admin.Cause != bfdv1.ObservationCause_OBSERVATION_CAUSE_REMOTE_ADMIN_DOWN.String() ||
		admin.ObservationVersion <= baseline.ObservationVersion {
		t.Fatalf("initial remote AdminDown facts: %+v", admin)
	}
	t.Logf("remote AdminDown observed after %s: %+v", time.Since(started), admin)
	requireFRRBGPUnchanged(ctx, t, uptime)
	detection, err := time.ParseDuration(admin.DetectionTime)
	if err != nil || detection <= 0 || detection > 3*time.Second {
		t.Fatalf("unexpected detection time %q: %v", admin.DetectionTime, err)
	}

	// FRR's configured shutdown sends one AdminDown packet and stops its timers.
	// Sampling and the unchanged BGP uptime prove the polling contract, not continuous wire visibility.
	window := 2*detection + time.Second
	windowCtx, cancelWindow := context.WithTimeout(ctx, window+5*time.Second)
	defer cancelWindow()
	windowTimer := time.NewTimer(window)
	defer windowTimer.Stop()
	expired := false
	samples := 0
	for {
		observed := readFRRBFDObservation(windowCtx, t)
		if observed.LocalState != "Down" || !observed.RemoteKnown || observed.RemoteState != "AdminDown" ||
			observed.RemoteDiagnostic != "AdminDown" {
			t.Fatalf("remote administrative fact lost after %s: %+v", time.Since(started), observed)
		}
		if observed.LocalDiagnostic == "ControlTimeExpired" && *observed.RemoteDiscriminator == 0 &&
			observed.Cause == bfdv1.ObservationCause_OBSERVATION_CAUSE_DETECTION_TIMEOUT.String() &&
			observed.ObservationVersion > admin.ObservationVersion {
			if !expired {
				t.Logf("detection expiry observed after %s: %+v", time.Since(started), observed)
			}
			expired = true
		}
		requireFRRBGPUnchanged(windowCtx, t, uptime)
		samples++
		select {
		case <-windowCtx.Done():
			t.Fatalf("AdminDown sampling interrupted: %v", windowCtx.Err())
		case <-windowTimer.C:
			if !expired {
				t.Fatalf("no detection expiry with retained remote AdminDown in %d samples", samples)
			}
			t.Logf("BGP admin Up/Established/unchanged uptime and route retained across %d samples over %s",
				samples, time.Since(started))
			if err := configureFRRBFD(ctx, false); err != nil {
				t.Fatalf("restore FRR BFD: %v", err)
			}
			waitFRRBFDUp(ctx, t, bfdUpTimeout)
			waitForCondition(t, "GoBFD FRR session restored Up", bfdUpTimeout, func() (bool, error) {
				return readFRRBFDObservation(ctx, t).LocalState == "Up", nil
			})
			requireFRRBGPUnchanged(ctx, t, uptime)
			t.Log("FRR BFD Up restored; BGP uptime and route unchanged")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
