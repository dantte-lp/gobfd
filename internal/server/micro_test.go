package server

import (
	"context"
	"log/slog"
	"testing"
	"time"

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
