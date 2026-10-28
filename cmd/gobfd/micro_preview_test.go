package main

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestMicroPreviewDisabledRejectsCandidateAndOpensNoListeners(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface: "bond0", MemberLinks: []string{"eth0"},
		PeerAddr: "192.0.2.2", LocalAddr: "192.0.2.1", MinActiveLinks: 1,
	}}
	if _, err := compileMicroBFDCandidates(cfg); !errors.Is(err, config.ErrMicroBFDPreviewDisabled) {
		t.Errorf("disabled candidate error = %v, want ErrMicroBFDPreviewDisabled", err)
	}
	if got := microListenerConfigs(cfg); len(got) != 0 {
		t.Errorf("disabled Micro-BFD requested %d listeners", len(got))
	}
}

func TestMicroPreviewDaemonAPIAdmission(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.DiscardHandler)
			mgr := bfd.NewManager(logger)
			t.Cleanup(mgr.Close)
			srv := newGRPCServer(config.GRPCConfig{UnsafeLoopbackMutations: true}, enabled,
				mgr, nil, nil, newDaemonHealthChecker(), nil, logger, nil)
			baseURL := serveLoopback(t, srv)
			client := bfdv1connect.NewMicroBFDServiceClient(
				connect.NewClient(connecthttp.NewTransport(http.DefaultClient, baseURL)))
			_, err := client.AddMicroBFDGroup(t.Context(), &bfdv1.AddMicroBFDGroupRequest{
				LagInterface: "bond0", MemberLinks: []string{"eth0"},
				PeerAddress: "192.0.2.2", LocalAddress: "192.0.2.1",
				DesiredMinTxInterval: durationpb.New(time.Second), RequiredMinRxInterval: durationpb.New(time.Second),
				DetectMultiplier: 3, MinActiveLinks: 1,
			})
			wantGroups := 0
			if enabled {
				wantGroups = 1
				if err != nil {
					t.Fatalf("enabled AddMicroBFDGroup: %v", err)
				}
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("disabled AddMicroBFDGroup: %v, want FailedPrecondition", err)
			}
			list, err := client.ListMicroBFDGroups(t.Context(), new(bfdv1.ListMicroBFDGroupsRequest))
			if err != nil || len(list.GetGroups()) != wantGroups {
				t.Fatalf("ListMicroBFDGroups = %v, %v, want %d groups", list, err, wantGroups)
			}
		})
	}
}

func TestMicroPreviewEnableChangeRequiresRestart(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		cfg := config.DefaultConfig()
		cfg.MicroBFD.Enabled = enabled
		contract := newStartupRuntimeContract(cfg)
		cfg.MicroBFD.Enabled = !enabled
		if changed := contract.changedFields(cfg); !slices.Contains(changed, startupFieldMicroEnabled) {
			t.Fatalf("enable change from %v accepted without restart: %v", enabled, changed)
		}
	}
}
