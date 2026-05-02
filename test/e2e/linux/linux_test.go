//go:build e2e_linux

// Package linux_test validates S10.5 Linux dataplane ownership boundaries.
package linux_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
	"golang.org/x/sys/unix"
)

const (
	testLinkA = "gobfdl0"
	testLinkB = "gobfdl1"

	nlaFNested   = 1 << 15
	vethInfoPeer = 1
)

type observedLinkEvent struct {
	IfName  string `json:"if_name"`
	IfIndex int    `json:"if_index"`
	Up      bool   `json:"up"`
}

func TestRTNetlinkVethDownUpInIsolatedNamespace(t *testing.T) {
	assertNetworkNoneIsolation(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mon, err := netio.NewLinuxInterfaceMonitor(logger)
	if err != nil {
		t.Fatalf("NewLinuxInterfaceMonitor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- mon.Run(ctx)
	}()
	defer func() {
		cancel()
		if err := mon.Close(); err != nil && !errors.Is(err, unix.EBADF) {
			t.Errorf("close interface monitor: %v", err)
		}
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("interface monitor stopped with error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("interface monitor did not stop after cancellation")
		}
	}()
	time.Sleep(100 * time.Millisecond)

	nl := newRouteSocket(t)
	defer nl.close(t)
	defer nl.deleteLinkByName(testLinkA)

	if err := nl.addVethPair(testLinkA, testLinkB); err != nil {
		t.Fatalf("add veth pair: %v", err)
	}
	if err := nl.setLinkUp(testLinkA, true); err != nil {
		t.Fatalf("set %s up: %v", testLinkA, err)
	}
	if err := nl.setLinkUp(testLinkB, true); err != nil {
		t.Fatalf("set %s up: %v", testLinkB, err)
	}

	upEvent := waitForLinkEvent(t, mon.Events(), testLinkA, true)
	if err := nl.setLinkUp(testLinkA, false); err != nil {
		t.Fatalf("set %s down: %v", testLinkA, err)
	}
	downEvent := waitForLinkEvent(t, mon.Events(), testLinkA, false)

	writeJSONArtifact(t, "link-events.json", []observedLinkEvent{
		eventRecord(upEvent),
		eventRecord(downEvent),
	})
}

func TestLinuxLAGBackendsStayPolicyGated(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	slavesPath := filepath.Join(root, "bond0", "bonding", "slaves")
	if err := os.MkdirAll(filepath.Dir(slavesPath), 0o755); err != nil {
		t.Fatalf("mkdir fake sysfs: %v", err)
	}
	if err := os.WriteFile(slavesPath, []byte{}, 0o600); err != nil {
		t.Fatalf("create fake slaves file: %v", err)
	}

	kernel := netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{SysfsRoot: root})
	if err := kernel.RemoveMember(ctx, "bond0", "eth0"); err != nil {
		t.Fatalf("kernel remove member: %v", err)
	}
	assertFileContent(t, slavesPath, "-eth0\n")
	if err := kernel.AddMember(ctx, "bond0", "eth0"); err != nil {
		t.Fatalf("kernel add member: %v", err)
	}
	assertFileContent(t, slavesPath, "+eth0\n")

	if _, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendKernelBond,
		OwnerPolicy: netio.LAGOwnerPolicyRefuseIfManaged,
	}); !errors.Is(err, netio.ErrUnsupportedLAGOwnerPolicy) {
		t.Fatalf("kernel-bond refused-owner error = %v, want ErrUnsupportedLAGOwnerPolicy", err)
	}

	if _, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendOVS,
		OwnerPolicy: netio.LAGOwnerPolicyRefuseIfManaged,
	}); !errors.Is(err, netio.ErrUnsupportedLAGOwnerPolicy) {
		t.Fatalf("ovs refused-owner error = %v, want ErrUnsupportedLAGOwnerPolicy", err)
	}

	if _, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendNetworkManager,
		OwnerPolicy: netio.LAGOwnerPolicyAllowExternal,
	}); !errors.Is(err, netio.ErrUnsupportedLAGOwnerPolicy) {
		t.Fatalf("networkmanager owner error = %v, want ErrUnsupportedLAGOwnerPolicy", err)
	}

	rec := &recordingLAGBackend{}
	actuator, err := netio.NewLAGActuator(netio.LAGActuatorConfig{
		Mode:       netio.LAGActuatorModeDryRun,
		Backend:    netio.LAGActuatorBackendKernelBond,
		DownAction: netio.LAGActuatorActionRemoveMember,
		UpAction:   netio.LAGActuatorActionAddMember,
	}, rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewLAGActuator dry-run: %v", err)
	}
	if err := actuator.HandleMicroBFDMemberEvent(ctx, bfd.MicroBFDMemberEvent{
		LAGInterface:    "bond0",
		MemberInterface: "eth0",
		OldState:        bfd.StateUp,
		NewState:        bfd.StateDown,
	}); err != nil {
		t.Fatalf("dry-run member event: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("dry-run backend calls = %v, want none", rec.calls)
	}

	writeJSONArtifact(t, "lag-backends.json", map[string]any{
		"kernel_bond_fake_sysfs": "remove/add commands verified",
		"ovs_owner_policy":       "refuse-if-managed rejected for enforce mode",
		"networkmanager_policy":  "networkmanager-dbus required for enforce mode",
		"dry_run":                "no backend mutation calls",
	})
}

func assertNetworkNoneIsolation(t *testing.T) {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list network interfaces: %v", err)
	}
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	if len(names) != 1 || names[0] != "lo" {
		t.Fatalf("test must start in isolated --network none namespace, interfaces=%v", names)
	}
}

func waitForLinkEvent(
	t *testing.T,
	events <-chan netio.InterfaceEvent,
	ifName string,
	up bool,
) netio.InterfaceEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	var seen []netio.InterfaceEvent
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("interface event channel closed before %s up=%t; seen=%v", ifName, up, seen)
			}
			seen = append(seen, ev)
			if ev.IfName == ifName && ev.Up == up {
				return ev
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s up=%t; seen=%v", ifName, up, seen)
		}
	}
}

func eventRecord(ev netio.InterfaceEvent) observedLinkEvent {
	return observedLinkEvent{IfName: ev.IfName, IfIndex: ev.IfIndex, Up: ev.Up}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func writeJSONArtifact(t *testing.T, name string, value any) {
	t.Helper()
	reportDir := os.Getenv("E2E_LINUX_REPORT_DIR")
	if reportDir == "" {
		reportDir = "/report"
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, name), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

type recordingLAGBackend struct {
	calls []string
}

func (r *recordingLAGBackend) RemoveMember(_ context.Context, lagInterface, memberInterface string) error {
	r.calls = append(r.calls, "remove "+lagInterface+" "+memberInterface)
	return nil
}

func (r *recordingLAGBackend) AddMember(_ context.Context, lagInterface, memberInterface string) error {
	r.calls = append(r.calls, "add "+lagInterface+" "+memberInterface)
	return nil
}

type routeSocket struct {
	fd  int
	seq uint32
}

func newRouteSocket(t *testing.T) *routeSocket {
	t.Helper()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatalf("open route netlink socket: %v", err)
	}
	return &routeSocket{fd: fd}
}

func (s *routeSocket) close(t *testing.T) {
	t.Helper()
	if err := unix.Close(s.fd); err != nil {
		t.Fatalf("close route socket: %v", err)
	}
}

func (s *routeSocket) addVethPair(ifName string, peerName string) error {
	attrs := append([]byte{},
		netlinkAttr(unix.IFLA_IFNAME, []byte(ifName+"\x00"), false)...)

	peer := append(ifInfoMsg(0, 0, 0), netlinkAttr(unix.IFLA_IFNAME, []byte(peerName+"\x00"), false)...)
	infoData := netlinkAttr(vethInfoPeer, peer, true)
	linkInfo := append([]byte{},
		netlinkAttr(unix.IFLA_INFO_KIND, []byte("veth\x00"), false)...)
	linkInfo = append(linkInfo, netlinkAttr(unix.IFLA_INFO_DATA, infoData, true)...)
	attrs = append(attrs, netlinkAttr(unix.IFLA_LINKINFO, linkInfo, true)...)

	return s.request(unix.RTM_NEWLINK, unix.NLM_F_CREATE|unix.NLM_F_EXCL, ifInfoMsg(0, 0, 0), attrs)
}

func (s *routeSocket) setLinkUp(ifName string, up bool) error {
	index, err := net.InterfaceByName(ifName)
	if err != nil {
		return err
	}
	flags := uint32(0)
	if up {
		flags = unix.IFF_UP
	}
	return s.request(unix.RTM_NEWLINK, 0, ifInfoMsg(index.Index, flags, unix.IFF_UP), nil)
}

func (s *routeSocket) deleteLinkByName(ifName string) {
	index, err := net.InterfaceByName(ifName)
	if err != nil {
		return
	}
	_ = s.request(unix.RTM_DELLINK, 0, ifInfoMsg(index.Index, 0, 0), nil)
}

func (s *routeSocket) request(msgType uint16, flags uint16, body []byte, attrs []byte) error {
	s.seq++
	payload := append(append([]byte{}, body...), attrs...)
	msg := make([]byte, unix.NLMSG_HDRLEN+len(payload))
	binary.NativeEndian.PutUint32(msg[0:4], uint32(len(msg)))
	binary.NativeEndian.PutUint16(msg[4:6], msgType)
	binary.NativeEndian.PutUint16(msg[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK|flags)
	binary.NativeEndian.PutUint32(msg[8:12], s.seq)
	copy(msg[unix.NLMSG_HDRLEN:], payload)

	if err := unix.Sendto(s.fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return err
	}
	return s.readAck(s.seq)
}

func (s *routeSocket) readAck(seq uint32) error {
	buf := make([]byte, 8192)
	for {
		n, _, err := unix.Recvfrom(s.fd, buf, 0)
		if err != nil {
			return err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return err
		}
		for _, msg := range msgs {
			if msg.Header.Seq != seq {
				continue
			}
			if msg.Header.Type != unix.NLMSG_ERROR {
				continue
			}
			if len(msg.Data) < 4 {
				return fmt.Errorf("short netlink ack: %d", len(msg.Data))
			}
			code := int32(binary.NativeEndian.Uint32(msg.Data[:4]))
			if code == 0 {
				return nil
			}
			return unix.Errno(-code)
		}
	}
}

func ifInfoMsg(index int, flags uint32, change uint32) []byte {
	buf := make([]byte, unix.SizeofIfInfomsg)
	buf[0] = unix.AF_UNSPEC
	binary.NativeEndian.PutUint32(buf[4:8], uint32(index))
	binary.NativeEndian.PutUint32(buf[8:12], flags)
	binary.NativeEndian.PutUint32(buf[12:16], change)
	return buf
}

func netlinkAttr(attrType int, value []byte, nested bool) []byte {
	if nested {
		attrType |= nlaFNested
	}
	length := unix.SizeofRtAttr + len(value)
	aligned := (length + 3) &^ 3
	buf := make([]byte, aligned)
	binary.NativeEndian.PutUint16(buf[0:2], uint16(length))
	binary.NativeEndian.PutUint16(buf[2:4], uint16(attrType))
	copy(buf[unix.SizeofRtAttr:], value)
	return buf
}
