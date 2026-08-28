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
		if existing == "" && iface.Name != "" {
			existing = iface.Name
		}
	}
	if existing == "" {
		t.Fatal("real Linux interface inventory contains no non-empty names")
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

func TestCheckInterfaceAvailableKeepsInventoryFailurePermanent(t *testing.T) {
	t.Parallel()

	want := errors.New("inventory failed")
	err := checkInterfaceAvailable("eth0", func() ([]net.Interface, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("inventory error = %v, want wrapped %v", err, want)
	}
	if errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("inventory error promoted to unavailable: %v", err)
	}
}
