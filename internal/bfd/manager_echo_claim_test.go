package bfd

import (
	"errors"
	"net/netip"
	"testing"
)

func TestEchoClaimReconcileFailurePreservesOwners(t *testing.T) {
	t.Parallel()
	mgr := newEchoLeaseManager()
	t.Cleanup(mgr.Close)
	api, retained, shared, created, failed := new(echoLeaseCounter), new(echoLeaseCounter),
		new(echoLeaseCounter), new(echoLeaseCounter), new(echoLeaseCounter)
	apiConfig := echoLeaseConfig("192.0.2.91")
	apiDiscr, err := mgr.CreateEchoSessionWithSenderLease(t.Context(), apiConfig, api.factory(nil))
	if err != nil {
		t.Fatal(err)
	}
	old := EchoReconcileConfig{EchoSessionConfig: echoLeaseConfig("192.0.2.90"), SenderLeaseFactory: retained.factory(nil)}
	if _, _, err := mgr.ReconcileEchoSessions(t.Context(), []EchoReconcileConfig{old}); err != nil {
		t.Fatal(err)
	}
	factoryErr := errors.New("new Echo sender unavailable")
	result := mgr.ReconcileEchoSessionsDetailed(t.Context(), []EchoReconcileConfig{
		{EchoSessionConfig: apiConfig, SenderLeaseFactory: shared.factory(nil)},
		{EchoSessionConfig: echoLeaseConfig("192.0.2.92"), SenderLeaseFactory: created.factory(nil)},
		{EchoSessionConfig: echoLeaseConfig("192.0.2.93"), SenderLeaseFactory: failed.factory(factoryErr)},
	})
	if !errors.Is(result.Err(), factoryErr) || result.Created != 0 || result.Released != 0 {
		t.Errorf("failed claim set = %+v, want no net claims and factory error", result)
	}
	if len(mgr.EchoSessions()) != 2 || retained.closes.Load() != 0 || shared.opens.Load() != 0 {
		t.Error("failed preparation removed the old config or duplicated an API-owned wire")
	}
	if created.closes.Load() != 1 || failed.closes.Load() != 1 {
		t.Error("failed claim preparation leaked new/partial sender leases")
	}
	if err := mgr.DestroyEchoSession(apiDiscr); err != nil {
		t.Fatal(err)
	}
	if api.closes.Load() != 1 || len(mgr.EchoSessions()) != 1 {
		t.Error("rollback retained a new config claim or lost the old one")
	}
}

func TestEchoClaimCanonicalIdentityCannotAliasDelimiters(t *testing.T) {
	t.Parallel()
	first := echoLeaseConfig("fe80::1%a|fe80::2%b")
	first.LocalAddr = netip.MustParseAddr("fe80::3%c")
	second := first
	second.PeerAddr = netip.MustParseAddr("fe80::1%a")
	second.LocalAddr = netip.MustParseAddr("fe80::2%b|fe80::3%c")
	if err := ValidateEchoReconcileConfigs([]EchoReconcileConfig{
		{EchoSessionConfig: first}, {EchoSessionConfig: second},
	}); err != nil {
		t.Fatalf("distinct accepted Echo identities alias: %v", err)
	}
}
