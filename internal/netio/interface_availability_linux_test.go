//go:build linux

package netio

import (
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestCheckInterfaceAvailableUsesExactLinuxInventory(t *testing.T) {
	t.Parallel()

	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list real Linux interfaces: %v", err)
	}
	existing := ""
	names := make(map[string]struct{}, len(interfaces))
	for _, iface := range interfaces {
		names[iface.Name] = struct{}{}
		if existing == "" && iface.Name != "" &&
			iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagRunning != 0 {
			existing = iface.Name
		}
	}
	if existing == "" {
		t.Fatal("real Linux interface inventory contains no operational interface")
	}
	err = CheckInterfaceAvailable(existing)
	if err != nil {
		t.Fatalf("CheckInterfaceAvailable(existing %q): %v", existing, err)
	}

	missing := "gobfd-missing"
	if _, exists := names[missing]; exists {
		missing = "gobfd-absent"
	}
	if _, exists := names[missing]; exists {
		t.Fatalf("test missing interface candidates unexpectedly exist: %q", missing)
	}
	err = CheckInterfaceAvailable(missing)
	if !errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("missing interface error = %v, want ErrResourceUnavailable", err)
	}
	resource, ok := bfd.UnavailableResource(err)
	if !ok || resource != (bfd.ResourceRef{Kind: bfd.ResourceKindInterface, ID: missing}) {
		t.Fatalf("missing interface resource = (%+v, %t), want interface %q", resource, ok, missing)
	}
}

func TestCheckInterfacesAvailableRequiresUpAndRunningWithOneInventory(t *testing.T) {
	t.Parallel()

	inventoryCalls := 0
	pending, err := checkInterfacesAvailable(
		[]string{"ready0", "missing0", "down0", "not-running0", "missing0", ""},
		func() ([]net.Interface, error) {
			inventoryCalls++
			return []net.Interface{
				{Name: "ready0", Flags: net.FlagUp | net.FlagRunning},
				{Name: "down0", Flags: net.FlagRunning},
				{Name: "not-running0", Flags: net.FlagUp},
			}, nil
		},
	)
	if inventoryCalls != 1 {
		t.Fatalf("inventory calls = %d, want 1", inventoryCalls)
	}
	if pending != 4 {
		t.Fatalf("pending claims = %d, want 4", pending)
	}
	if !errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("batch availability error = %v, want ErrResourceUnavailable", err)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("batch availability error type = %T, want joined error", err)
	}
	leaves := joined.Unwrap()
	if len(leaves) != pending {
		t.Fatalf("batch unavailable leaves = %d, want %d", len(leaves), pending)
	}
	for _, unavailableErr := range leaves {
		if _, ok := bfd.UnavailableResource(unavailableErr); !ok {
			t.Errorf("batch error leaf = %v, want typed unavailable resource", unavailableErr)
		}
	}
}

func TestCheckInterfaceAvailableRejectsInvalidNamesBeforeInventory(t *testing.T) {
	t.Parallel()

	if err := CheckInterfaceAvailable(""); err != nil {
		t.Fatalf("empty optional interface: %v", err)
	}

	for _, name := range []string{
		".",
		"..",
		"eth/0",
		"/eth0",
		"eth:0",
		"eth 0",
		"eth\t0",
		"eth\n0",
		"eth\v0",
		"eth\f0",
		"eth\r0",
		"eth\x000",
		strings.Repeat("x", unix.IFNAMSIZ),
	} {
		err := checkInterfaceAvailable(name, func() ([]net.Interface, error) {
			t.Fatal("inventory called for invalid interface name")
			return nil, nil
		})
		if !errors.Is(err, ErrInvalidInterfaceName) {
			t.Errorf("CheckInterfaceAvailable(%q) error = %v, want ErrInvalidInterfaceName", name, err)
		}
		if errors.Is(err, bfd.ErrResourceUnavailable) {
			t.Errorf("invalid interface %q promoted to unavailable: %v", name, err)
		}
	}
}

func TestCheckInterfacesAvailableValidatesCompleteBatchBeforeInventory(t *testing.T) {
	t.Parallel()

	pending, err := checkInterfacesAvailable(
		[]string{"valid0", "bad/name", "also:bad"},
		func() ([]net.Interface, error) {
			t.Fatal("inventory called before complete interface-name validation")
			return nil, nil
		},
	)
	if pending != 0 {
		t.Fatalf("invalid batch pending claims = %d, want 0", pending)
	}
	if !errors.Is(err, ErrInvalidInterfaceName) {
		t.Fatalf("invalid batch error = %v, want ErrInvalidInterfaceName", err)
	}
	if errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("invalid batch promoted to unavailable: %v", err)
	}
}

func TestCheckInterfaceAvailableKeepsInventoryFailurePermanent(t *testing.T) {
	t.Parallel()

	want := errors.New("inventory failed")
	pending, err := checkInterfacesAvailable([]string{"eth0", "eth1"}, func() ([]net.Interface, error) {
		return nil, want
	})
	if pending != 0 {
		t.Fatalf("inventory failure pending claims = %d, want 0", pending)
	}
	if !errors.Is(err, want) {
		t.Fatalf("inventory error = %v, want wrapped %v", err, want)
	}
	if errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("inventory error promoted to unavailable: %v", err)
	}
}
