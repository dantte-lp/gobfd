//go:build e2e_linux

package linux_test

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const kernelBondLAG = "gobfdkb0"

type kernelBondObservation struct {
	Phase  string   `json:"phase"`
	Slaves []string `json:"slaves"`
}

// TestKernelBondActuatorMovesMemberThroughNetlink drives the enforce-mode
// kernel-bond actuator built by NewLAGActuatorBackend against a real Linux
// bond: a member Down releases only that member (IFLA_MASTER 0), its Up
// enslaves it again, and the bonding slaves file shows each result. The gate
// container has CAP_NET_ADMIN and a read-only /sys.
func TestKernelBondActuatorMovesMemberThroughNetlink(t *testing.T) {
	assertNetworkNoneIsolation(t)
	nl := newRouteSocket(t)
	defer nl.close(t)
	members := []string{"gobfdk0a", "gobfdk1a"}
	t.Cleanup(func() {
		for _, m := range members {
			nl.deleteLinkByName(m)
		}
		nl.deleteLinkByName(kernelBondLAG)
	})
	if err := nl.addLink(kernelBondLAG, "bond"); err != nil {
		t.Fatalf("add bond %s: %v", kernelBondLAG, err)
	}
	lag, err := net.InterfaceByName(kernelBondLAG)
	if err != nil {
		t.Fatalf("resolve %s: %v", kernelBondLAG, err)
	}
	for _, m := range members {
		if err := nl.addVethPair(m, strings.TrimSuffix(m, "a")+"b"); err != nil {
			t.Fatalf("add veth %s: %v", m, err)
		}
		if err := nl.setMaster(m, lag.Index); err != nil {
			t.Fatalf("enslave %s: %v", m, err)
		}
	}
	if err := nl.setLinkUp(kernelBondLAG, true); err != nil {
		t.Fatalf("set %s up: %v", kernelBondLAG, err)
	}

	cfg := netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendKernelBond,
		OwnerPolicy: netio.LAGOwnerPolicyAllowExternal,
	}
	backend, err := netio.NewLAGActuatorBackend(cfg)
	if err != nil {
		t.Fatalf("NewLAGActuatorBackend: %v", err)
	}
	actuator, err := netio.NewLAGActuator(cfg, backend, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewLAGActuator: %v", err)
	}

	var observed []kernelBondObservation
	expect := func(phase string, want ...string) {
		t.Helper()
		got := readBondSlaves(t, kernelBondLAG)
		observed = append(observed, kernelBondObservation{Phase: phase, Slaves: got})
		if !slices.Equal(got, want) {
			t.Fatalf("%s: %s slaves = %q, want %q", phase, kernelBondLAG, got, want)
		}
	}
	member := func(old, next bfd.State) {
		t.Helper()
		if err := actuator.HandleMicroBFDMemberEvent(t.Context(), bfd.MicroBFDMemberEvent{
			LAGInterface: kernelBondLAG, MemberInterface: members[0], OldState: old, NewState: next,
		}); err != nil {
			t.Fatalf("member %s %s->%s: %v", members[0], old, next, err)
		}
	}

	expect("enslaved", members...)
	member(bfd.StateUp, bfd.StateDown)
	expect("member down", members[1])
	if _, err := os.Lstat(filepath.Join("/sys/class/net", members[0], "master")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s master link after release: %v", members[0], err)
	}
	if err := backend.RemoveMember(t.Context(), kernelBondLAG, members[0]); !errors.Is(err, netio.ErrLAGMemberNotInLAG) {
		t.Fatalf("second release error = %v, want ErrLAGMemberNotInLAG", err)
	}
	member(bfd.StateDown, bfd.StateUp)
	expect("member up", members...)
	writeJSONArtifact(t, "lag-kernel-bond.json", observed)
}

// readBondSlaves returns the sorted members listed in bonding/slaves.
func readBondSlaves(t *testing.T, bond string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("/sys/class/net", bond, "bonding", "slaves"))
	if err != nil {
		t.Fatalf("read %s slaves: %v", bond, err)
	}
	slaves := strings.Fields(string(raw))
	slices.Sort(slaves)
	return slaves
}

func (s *routeSocket) addLink(ifName, kind string) error {
	linkInfo := netlinkAttr(unix.IFLA_INFO_KIND, []byte(kind+"\x00"), false)
	attrs := append(netlinkAttr(unix.IFLA_IFNAME, []byte(ifName+"\x00"), false),
		netlinkAttr(unix.IFLA_LINKINFO, linkInfo, true)...)
	return s.request(unix.RTM_NEWLINK, unix.NLM_F_CREATE|unix.NLM_F_EXCL, ifInfoMsg(0, 0, 0), attrs)
}

func (s *routeSocket) setMaster(ifName string, masterIndex int) error {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return err
	}
	value := binary.NativeEndian.AppendUint32(nil, uint32(masterIndex))
	return s.request(unix.RTM_NEWLINK, 0, ifInfoMsg(iface.Index, 0, 0), netlinkAttr(unix.IFLA_MASTER, value, false))
}
