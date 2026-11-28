package bfd

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

// gobfd-330.4: API claims belong to the caller's principal. Two principals
// share one wire session, each releases only its own claim, and a principal
// without a claim cannot release the session.
func TestAPIClaimsArePrincipalScoped(t *testing.T) {
	t.Parallel()

	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	cfg := senderLeaseTestConfig("192.0.2.40")
	alice, bob, eve := APISessionOwner("uid:1000"), APISessionOwner("spiffe://gobfd.test/ops"), APISessionOwner("uid:2000")

	first, err := mgr.CreateSessionAs(context.Background(), cfg, ownershipSenderLeaseFactory(), alice)
	if err != nil {
		t.Fatalf("CreateSessionAs(alice): %v", err)
	}
	second, err := mgr.CreateSessionAs(context.Background(), cfg, ownershipSenderLeaseFactory(), bob)
	if err != nil {
		t.Fatalf("CreateSessionAs(bob): %v", err)
	}
	discr := first.LocalDiscriminator()
	if second.LocalDiscriminator() != discr {
		t.Fatalf("bob's claim made discriminator %d, want shared %d", second.LocalDiscriminator(), discr)
	}
	_, err = mgr.CreateSessionAs(context.Background(), cfg, ownershipSenderLeaseFactory(), alice)
	if !errors.Is(err, ErrDuplicateSession) {
		t.Fatalf("second claim by alice error = %v, want ErrDuplicateSession", err)
	}

	if err := mgr.DestroySessionAs(context.Background(), discr, eve); !errors.Is(err, ErrSessionOwnerClaimNotFound) {
		t.Fatalf("release by eve error = %v, want ErrSessionOwnerClaimNotFound", err)
	}
	if err := mgr.DestroySessionAs(context.Background(), discr, alice); err != nil {
		t.Fatalf("release by alice: %v", err)
	}
	if _, ok := mgr.LookupByDiscriminator(discr); !ok {
		t.Fatal("session removed while bob still claims it")
	}
	if err := mgr.DestroySessionAs(context.Background(), discr, bob); err != nil {
		t.Fatalf("release by bob: %v", err)
	}
	if _, ok := mgr.LookupByDiscriminator(discr); ok {
		t.Fatal("session kept after the last claim was released")
	}
}
