package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestAPIParameterGenericIntervals(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		tx         *durationpb.Duration
		rx         *durationpb.Duration
		wantLeases int
	}{
		{name: "TX inconsistent sign", tx: &durationpb.Duration{Seconds: 1, Nanos: -1}},
		{name: "TX nanos outside range", tx: &durationpb.Duration{Nanos: 1_000_000_000}},
		{name: "TX seconds outside range", tx: &durationpb.Duration{Seconds: 315_576_000_001}},
		{name: "RX inconsistent sign", rx: &durationpb.Duration{Seconds: 1, Nanos: -1}},
		{name: "RX nanos outside range", rx: &durationpb.Duration{Nanos: 1_000_000_000}},
		{name: "RX seconds outside range", rx: &durationpb.Duration{Seconds: 315_576_000_001}},
		{name: "negative TX", tx: durationpb.New(-time.Microsecond), wantLeases: 1},
		{name: "negative RX", rx: durationpb.New(-time.Microsecond), wantLeases: 1},
		{name: "TX 1ns truncates to wire zero", tx: durationpb.New(time.Nanosecond), wantLeases: 1},
		{name: "RX 999ns truncates to wire zero", rx: durationpb.New(999 * time.Nanosecond), wantLeases: 1},
		{name: "TX exceeds wire range", tx: durationpb.New(bfd.MaxWireInterval + time.Microsecond), wantLeases: 1},
		{name: "RX exceeds wire range", rx: durationpb.New(bfd.MaxWireInterval + time.Microsecond), wantLeases: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.DiscardHandler)
			mgr := bfd.NewManager(logger)
			t.Cleanup(mgr.Close)
			factory := &echoLeaseTestSenderFactory{}
			s := &BFDServer{manager: mgr, senderFactory: factory, logger: logger}
			_, err := s.AddSession(context.Background(), &bfdv1.AddSessionRequest{
				PeerAddress:           "192.0.2.10",
				LocalAddress:          "192.0.2.1",
				Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
				DesiredMinTxInterval:  tt.tx,
				RequiredMinRxInterval: tt.rx,
				DetectMultiplier:      3,
			})
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("AddSession code = %s, want InvalidArgument (error = %v)", got, err)
			}
			sessions, echo, groups := len(mgr.Sessions()), len(mgr.EchoSessions()), len(mgr.MicroBFDGroups())
			if sessions != 0 || echo != 0 || groups != 0 {
				t.Errorf("state after rejection: sessions/echo/groups = %d/%d/%d, want 0/0/0", sessions, echo, groups)
			}
			if factory.opens != tt.wantLeases || factory.closes != tt.wantLeases {
				t.Errorf("sender opens/closes = %d/%d, want %d/%d",
					factory.opens, factory.closes, tt.wantLeases, tt.wantLeases)
			}
		})
	}
}

func TestAPIParameterEchoIntervals(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		tx   *durationpb.Duration
	}{
		{name: "inconsistent sign", tx: &durationpb.Duration{Seconds: 1, Nanos: -1}},
		{name: "nanos outside range", tx: &durationpb.Duration{Nanos: 1_000_000_000}},
		{name: "seconds outside range", tx: &durationpb.Duration{Seconds: 315_576_000_001}},
		{name: "omitted"},
		{name: "zero", tx: durationpb.New(0)},
		{name: "negative", tx: durationpb.New(-time.Microsecond)},
		{name: "exceeds wire range", tx: durationpb.New(bfd.MaxWireInterval + time.Microsecond)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newTestEchoServer(t)
			factory := &echoLeaseTestSenderFactory{}
			s.senderFactory = factory
			_, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
				PeerAddress:      "192.0.2.10",
				LocalAddress:     "192.0.2.1",
				TxInterval:       tt.tx,
				DetectMultiplier: 3,
			})
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("AddEchoSession code = %s, want InvalidArgument (error = %v)", got, err)
			}
			sessions, echo, groups := len(s.manager.Sessions()), len(s.manager.EchoSessions()), len(s.manager.MicroBFDGroups())
			if sessions != 0 || echo != 0 || groups != 0 {
				t.Errorf("state after rejection: sessions/echo/groups = %d/%d/%d, want 0/0/0", sessions, echo, groups)
			}
			if factory.opens != 0 || factory.closes != 0 {
				t.Errorf("sender opens/closes = %d/%d, want 0/0", factory.opens, factory.closes)
			}
		})
	}
}

func TestAPIParameterMicroGroup(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		mutate func(*bfdv1.AddMicroBFDGroupRequest)
	}{
		{
			name: "TX inconsistent sign",
			mutate: func(req *bfdv1.AddMicroBFDGroupRequest) {
				req.DesiredMinTxInterval = &durationpb.Duration{Seconds: 1, Nanos: -1}
			},
		},
		{
			name: "RX nanos outside range",
			mutate: func(req *bfdv1.AddMicroBFDGroupRequest) {
				req.RequiredMinRxInterval = &durationpb.Duration{Nanos: 1_000_000_000}
			},
		},
		{
			name: "duplicate member link",
			mutate: func(req *bfdv1.AddMicroBFDGroupRequest) {
				req.MemberLinks = []string{"eth0", "eth0"}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newTestMicroServer(t)
			t.Cleanup(s.manager.Close)
			req := validMicroRequest()
			tt.mutate(req)
			_, err := s.AddMicroBFDGroup(context.Background(), req)
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("AddMicroBFDGroup code = %s, want InvalidArgument (error = %v)", got, err)
			}
			sessions, echo, groups := len(s.manager.Sessions()), len(s.manager.EchoSessions()), len(s.manager.MicroBFDGroups())
			if sessions != 0 || echo != 0 || groups != 0 {
				t.Errorf("state after rejection: sessions/echo/groups = %d/%d/%d, want 0/0/0", sessions, echo, groups)
			}
		})
	}
}

func TestAPIParameterManagerErrorCodes(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{name: "RX interval", err: bfd.ErrInvalidRxInterval, want: connect.CodeInvalidArgument},
		{name: "wire interval", err: bfd.ErrInvalidWireInterval, want: connect.CodeInvalidArgument},
		{name: "duplicate member", err: bfd.ErrMicroBFDDuplicateMember, want: connect.CodeInvalidArgument},
		{name: "internal discriminator", err: bfd.ErrInvalidDiscriminator, want: connect.CodeInternal},
		{name: "internal auth wiring", err: bfd.ErrMissingAuthKeyStore, want: connect.CodeInternal},
		{name: "unknown failure", err: errors.New("sender failed"), want: connect.CodeInternal},
		{name: "canceled", err: context.Canceled, want: connect.CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: connect.CodeDeadlineExceeded},
		{name: "existing duplicate", err: bfd.ErrDuplicateSession, want: connect.CodeAlreadyExists},
		{name: "missing session", err: bfd.ErrSessionNotFound, want: connect.CodeNotFound},
		{name: "missing owner claim", err: bfd.ErrSessionOwnerClaimNotFound, want: connect.CodePermissionDenied},
		{name: "existing TX validation", err: bfd.ErrInvalidTxInterval, want: connect.CodeInvalidArgument},
		{name: "unavailable interface", err: bfd.NewResourceUnavailableError(bfd.ResourceRef{
			Kind: bfd.ResourceKindInterface, ID: "eth3",
		}), want: connect.CodeUnavailable},
		{name: "receive listener", err: ErrReceiveListenerUnavailable, want: connect.CodeFailedPrecondition},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapManagerError(fmt.Errorf("manager operation: %w", tt.err), "add session")
			if got.Code() != tt.want {
				t.Errorf("mapManagerError code = %s, want %s", got.Code(), tt.want)
			}
		})
	}
}

func TestAPIParameterCompatibleIntervals(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		rx   *durationpb.Duration
		want time.Duration
	}{
		{name: "generic omitted intervals", want: time.Second},
		{name: "generic explicit RX zero", rx: durationpb.New(0)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.DiscardHandler)
			mgr := bfd.NewManager(logger)
			t.Cleanup(mgr.Close)
			factory := &echoLeaseTestSenderFactory{}
			s := &BFDServer{manager: mgr, senderFactory: factory, logger: logger}
			resp, err := s.AddSession(context.Background(), &bfdv1.AddSessionRequest{
				PeerAddress:           "192.0.2.10",
				Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
				RequiredMinRxInterval: tt.rx,
				DetectMultiplier:      3,
			})
			if err != nil {
				t.Fatalf("AddSession: %v", err)
			}
			if got := resp.GetSession().GetDesiredMinTxInterval().AsDuration(); got != time.Second {
				t.Errorf("TX interval = %s, want 1s", got)
			}
			if got := resp.GetSession().GetRequiredMinRxInterval().AsDuration(); got != tt.want {
				t.Errorf("RX interval = %s, want %s", got, tt.want)
			}
			mgr.Close()
			if factory.opens != 1 || factory.closes != 1 {
				t.Errorf("sender opens/closes after close = %d/%d, want 1/1", factory.opens, factory.closes)
			}
		})
	}

	t.Run("Micro omitted intervals", func(t *testing.T) {
		t.Parallel()

		s := newTestMicroServer(t)
		t.Cleanup(s.manager.Close)
		req := validMicroRequest()
		req.DesiredMinTxInterval = nil
		req.RequiredMinRxInterval = nil
		resp, err := s.AddMicroBFDGroup(context.Background(), req)
		if err != nil {
			t.Fatalf("AddMicroBFDGroup: %v", err)
		}
		if got := resp.GetGroup().GetDesiredMinTxInterval().AsDuration(); got != time.Second {
			t.Errorf("TX interval = %s, want 1s", got)
		}
		if got := resp.GetGroup().GetRequiredMinRxInterval().AsDuration(); got != time.Second {
			t.Errorf("RX interval = %s, want 1s", got)
		}
	})

	t.Run("Echo positive interval", func(t *testing.T) {
		t.Parallel()

		s := newTestEchoServer(t)
		factory := &echoLeaseTestSenderFactory{}
		s.senderFactory = factory
		resp, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
			PeerAddress:      "192.0.2.10",
			LocalAddress:     "192.0.2.1",
			TxInterval:       durationpb.New(50 * time.Millisecond),
			DetectMultiplier: 3,
		})
		if err != nil {
			t.Fatalf("AddEchoSession: %v", err)
		}
		if got := resp.GetSession().GetTxInterval().AsDuration(); got != 50*time.Millisecond {
			t.Errorf("TX interval = %s, want 50ms", got)
		}
		s.manager.Close()
		if factory.opens != 1 || factory.closes != 1 {
			t.Errorf("sender opens/closes after close = %d/%d, want 1/1", factory.opens, factory.closes)
		}
	})
}
