package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func newTestMicroServer(t *testing.T) *MicroBFDServer {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	return &MicroBFDServer{
		manager: bfd.NewManager(logger),
		logger:  logger,
		enabled: true,
	}
}

func validMicroRequest() *bfdv1.AddMicroBFDGroupRequest {
	return &bfdv1.AddMicroBFDGroupRequest{
		LagInterface:          "bond0",
		MemberLinks:           []string{"eth0", "eth1"},
		PeerAddress:           "192.0.2.10",
		LocalAddress:          "192.0.2.1",
		DesiredMinTxInterval:  durationpb.New(300 * time.Millisecond),
		RequiredMinRxInterval: durationpb.New(300 * time.Millisecond),
		DetectMultiplier:      3,
		MinActiveLinks:        1,
	}
}

func TestMicroPreviewDisabledRejectsAddWithoutMutation(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	s := &MicroBFDServer{manager: mgr, logger: logger}
	_, err := s.AddMicroBFDGroup(t.Context(), validMicroRequest())
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("disabled AddMicroBFDGroup error = %v, want FailedPrecondition", err)
	}
	if got := len(mgr.MicroBFDGroups()); got != 0 {
		t.Errorf("disabled AddMicroBFDGroup created %d groups", got)
	}
}

func TestMicroServer_AddMicroBFDGroup_HappyPath(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	resp, err := s.AddMicroBFDGroup(context.Background(), validMicroRequest())
	if err != nil {
		t.Fatalf("AddMicroBFDGroup: %v", err)
	}
	if got, want := resp.GetGroup().GetLagInterface(), "bond0"; got != want {
		t.Errorf("lag_interface: got %q, want %q", got, want)
	}
	if got, want := len(resp.GetGroup().GetMemberLinks()), 2; got != want {
		t.Errorf("member_links: got %d, want %d", got, want)
	}
}

func TestMicroServer_AddMicroBFDGroup_RejectsEmptyLag(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	req := validMicroRequest()
	req.LagInterface = ""
	if _, err := s.AddMicroBFDGroup(context.Background(), req); err == nil {
		t.Fatalf("expected error for empty lag_interface")
	}
}

func TestMicroServer_AddMicroBFDGroup_RejectsEmptyMembers(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	req := validMicroRequest()
	req.MemberLinks = nil
	if _, err := s.AddMicroBFDGroup(context.Background(), req); err == nil {
		t.Fatalf("expected error for empty member_links")
	}
}

func TestMicroServer_AddMicroBFDGroup_RejectsMinActiveOutOfRange(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	req := validMicroRequest()
	req.MinActiveLinks = 5 // 5 > len(member_links)=2
	if _, err := s.AddMicroBFDGroup(context.Background(), req); err == nil {
		t.Fatalf("expected error for min_active_links out of range")
	}
}

func TestMicroServer_AddMicroBFDGroup_RejectsZeroDetectMultiplier(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	req := validMicroRequest()
	req.DetectMultiplier = 0
	if _, err := s.AddMicroBFDGroup(context.Background(), req); err == nil {
		t.Fatalf("expected error for zero detect_multiplier")
	}
}

func TestMicroServer_DeleteMicroBFDGroup_RemovesByLAG(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	if _, err := s.AddMicroBFDGroup(context.Background(), validMicroRequest()); err != nil {
		t.Fatalf("AddMicroBFDGroup: %v", err)
	}
	s.enabled = false // Disabling admission must preserve inspection and cleanup.
	if _, err := s.DeleteMicroBFDGroup(context.Background(), &bfdv1.DeleteMicroBFDGroupRequest{
		LagInterface: "bond0",
	}); err != nil {
		t.Fatalf("DeleteMicroBFDGroup: %v", err)
	}

	list, err := s.ListMicroBFDGroups(context.Background(), &bfdv1.ListMicroBFDGroupsRequest{})
	if err != nil {
		t.Fatalf("ListMicroBFDGroups: %v", err)
	}
	if got := len(list.GetGroups()); got != 0 {
		t.Fatalf("expected zero groups after delete, got %d", got)
	}
}

func TestMicroServer_DeleteMicroBFDGroup_RejectsEmptyName(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	if _, err := s.DeleteMicroBFDGroup(context.Background(), &bfdv1.DeleteMicroBFDGroupRequest{
		LagInterface: "",
	}); err == nil {
		t.Fatalf("expected error for empty lag_interface")
	}
}

func TestMicroGroupSnapshotToProtoRejectsCountOverflow(t *testing.T) {
	t.Parallel()

	overflow64 := uint64(math.MaxUint32) + 1
	if strconv.IntSize < 64 {
		t.Skip("snapshot counts cannot exceed uint32 on this architecture")
	}
	overflow := int(overflow64)
	tests := []struct {
		name string
		snap bfd.MicroBFDGroupSnapshot
	}{
		{
			name: "up member count",
			snap: bfd.MicroBFDGroupSnapshot{UpCount: overflow, MinActiveLinks: -1},
		},
		{
			name: "minimum active links",
			snap: bfd.MicroBFDGroupSnapshot{UpCount: -1, MinActiveLinks: overflow},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := microGroupSnapshotToProto(tt.snap)
			if !errors.Is(err, errMicroSnapshotCountOverflow) {
				t.Fatalf("microGroupSnapshotToProto() error = %v, want errMicroSnapshotCountOverflow", err)
			}
		})
	}
}

func TestMicroServer_ListMicroBFDGroups_ReturnsMemberSessionContract(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	if _, err := s.AddMicroBFDGroup(context.Background(), validMicroRequest()); err != nil {
		t.Fatalf("AddMicroBFDGroup: %v", err)
	}
	list, err := s.ListMicroBFDGroups(context.Background(), &bfdv1.ListMicroBFDGroupsRequest{})
	if err != nil {
		t.Fatalf("ListMicroBFDGroups: %v", err)
	}
	if len(list.GetGroups()) != 1 {
		t.Fatalf("groups = %d, want 1", len(list.GetGroups()))
	}
	group := list.GetGroups()[0]
	if got := group.GetDesiredMinTxInterval().AsDuration(); got != 300*time.Millisecond {
		t.Errorf("desired_min_tx_interval = %v, want 300ms", got)
	}
	if got := group.GetRequiredMinRxInterval().AsDuration(); got != 300*time.Millisecond {
		t.Errorf("required_min_rx_interval = %v, want 300ms", got)
	}
	if got := group.GetDetectMultiplier(); got != 3 {
		t.Errorf("detect_multiplier = %d, want 3", got)
	}
	if got := group.GetMemberLinks(); len(got) != 2 || got[0] != "eth0" || got[1] != "eth1" {
		t.Errorf("member_links = %v, want [eth0 eth1]", got)
	}
}

func TestMicroServer_AddMicroBFDGroup_RejectsMemberOwnedByOtherLAG(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	if _, err := s.AddMicroBFDGroup(context.Background(), validMicroRequest()); err != nil {
		t.Fatalf("AddMicroBFDGroup bond0: %v", err)
	}
	req := validMicroRequest()
	req.LagInterface = "bond1"
	req.MemberLinks = []string{"eth1", "eth2"}
	_, err := s.AddMicroBFDGroup(context.Background(), req)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (err %v), want %v", got, err, connect.CodeFailedPrecondition)
	}
}

func TestMicroServer_AddMicroBFDGroup_RejectsZeroDesiredMinTx(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	req := validMicroRequest()
	req.DesiredMinTxInterval = durationpb.New(0)
	_, err := s.AddMicroBFDGroup(context.Background(), req)
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v (err %v), want %v", got, err, connect.CodeInvalidArgument)
	}
}

type microTestSender struct{}

func (microTestSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func TestMicroServer_DeleteMicroBFDGroup_RejectsReloadOwnedGroup(t *testing.T) {
	t.Parallel()

	s := newTestMicroServer(t)
	t.Cleanup(s.manager.Close)
	cfg := bfd.MicroBFDConfig{
		LAGInterface: "bond0", MemberLinks: []string{"eth0"},
		PeerAddr: netip.MustParseAddr("192.0.2.10"), LocalAddr: netip.MustParseAddr("192.0.2.1"),
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second,
		DetectMultiplier: 3, MinActiveLinks: 1,
	}
	if err := s.manager.ReconcileMicroBFDGroupsDetailed(t.Context(), []bfd.MicroBFDReconcileConfig{{
		Key: cfg.LAGInterface, Config: cfg,
		SenderLeaseFactory: func(string) bfd.SenderLeaseFactory {
			return bfd.NonOwningSenderLeaseFactory(microTestSender{})
		},
	}}).Err(); err != nil {
		t.Fatalf("reload group: %v", err)
	}
	_, err := s.DeleteMicroBFDGroup(context.Background(), &bfdv1.DeleteMicroBFDGroupRequest{LagInterface: "bond0"})
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (err %v), want %v", got, err, connect.CodeFailedPrecondition)
	}
}
