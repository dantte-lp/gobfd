package bfd_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func addressUnavailable(t *testing.T, addr string) error {
	t.Helper()
	err := bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKindAddress, ID: addr})
	if _, ok := bfd.UnavailableResource(err); !ok {
		t.Fatalf("address resource error = %v, want a valid typed unavailable resource", err)
	}
	return err
}

func TestResourceKindAddressIsTypedUnavailable(t *testing.T) {
	t.Parallel()

	err := addressUnavailable(t, "192.0.2.1")
	if !errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("address resource error = %v, want ErrResourceUnavailable", err)
	}
	if got := bfd.ResourceKindAddress.String(); got != "address" {
		t.Fatalf("ResourceKindAddress = %q, want address", got)
	}
}

// A sender that cannot bind an unassigned local address leaves the claim
// pending, like an unresolved interface, instead of failing the source.
func TestReconcileSenderAddressUnavailableIsPending(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	unavailable := addressUnavailable(t, "192.0.2.1")
	result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), bfd.ConfigReconciliationOwner(),
		[]bfd.ReconcileConfig{{
			Key: "base", SessionConfig: bfd.SessionConfig{
				PeerAddr: netip.MustParseAddr("192.0.2.2"), LocalAddr: netip.MustParseAddr("192.0.2.1"),
				Type: bfd.SessionTypeMultiHop, Role: bfd.RoleActive,
				DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
			},
			SenderLeaseFactory: func() (*bfd.SenderLease, error) {
				return nil, fmt.Errorf("bind sender: %w", unavailable)
			},
		}})
	if result.ResourcePending != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want one resource-pending claim and no failure", result)
	}
}

func TestReconcileEchoSenderAddressUnavailableIsPending(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	unavailable := addressUnavailable(t, "192.0.2.1")
	result := mgr.ReconcileEchoSessionsDetailed(t.Context(), []bfd.EchoReconcileConfig{{
		Key: "echo", EchoSessionConfig: bfd.EchoSessionConfig{
			PeerAddr: netip.MustParseAddr("192.0.2.2"), LocalAddr: netip.MustParseAddr("192.0.2.1"),
			TxInterval: time.Hour, DetectMultiplier: 3,
		},
		SenderLeaseFactory: func() (*bfd.SenderLease, error) {
			return nil, fmt.Errorf("bind echo sender: %w", unavailable)
		},
	}})
	if result.ResourcePending != 1 || result.Failed != 0 || result.Created != 0 {
		t.Fatalf("result = %+v, want one resource-pending Echo claim, nothing created", result)
	}
	if len(mgr.EchoSessions()) != 0 {
		t.Fatal("pending Echo pass left a session")
	}
}
