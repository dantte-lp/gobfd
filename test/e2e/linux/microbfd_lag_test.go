//go:build e2e_linux

package linux_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// Micro-BFD LAG topology: GoBFD owns the kernel bond microLAG with two veth
// member links; the far veth ends carry an AF_PACKET peer in the same
// namespace. The gate container has NET_ADMIN and NET_RAW only, so it cannot
// create a second network namespace (CAP_SYS_ADMIN).
const (
	microLAG = "gobfdlag0"

	microCaptureWindow = 4 * time.Second
	ethPAll            = 0x0003
	ethTypeIPv4        = 0x0800
	ethTypeARP         = 0x0806
	packetOutgoing     = 4
)

// microDedicatedMAC returns the RFC 7130 Section 2.3 micro-BFD destination MAC.
func microDedicatedMAC() net.HardwareAddr {
	return net.HardwareAddr{0x01, 0x00, 0x5e, 0x90, 0x00, 0x01}
}

type microMember struct {
	local string // GoBFD end, member link of microLAG
	peer  string // far end, AF_PACKET peer
}

func microMembers() []microMember {
	return []microMember{
		{local: "gobfdm0a", peer: "gobfdm0b"},
		{local: "gobfdm1a", peer: "gobfdm1b"},
	}
}

func microLocalAddr() netip.Addr { return netip.AddrFrom4([4]byte{10, 254, 0, 1}) }
func microPeerAddr() netip.Addr  { return netip.AddrFrom4([4]byte{10, 254, 0, 2}) }

// microFrame is one Ethernet frame seen on a member link's far end.
type microFrame struct {
	Member    string `json:"member"`
	Offset    string `json:"offset"`
	DstMAC    string `json:"dst_mac"`
	SrcMAC    string `json:"src_mac"`
	EtherType string `json:"ether_type"`
	Summary   string `json:"summary"`
	TTL       uint8  `json:"ttl,omitempty"`
	DstPort   uint16 `json:"dst_port,omitempty"`
	BFDState  string `json:"bfd_state,omitempty"`
	BFDDiag   string `json:"bfd_diag,omitempty"`
	MyDiscr   uint32 `json:"my_discriminator,omitempty"`
	YourDiscr uint32 `json:"your_discriminator,omitempty"`

	raw []byte
	at  time.Time
}

// TestMicroBFDLAGMemberWire is the RFC 7130 integration gate. Phase 1 checks
// the wire identity of each member session's initial Down packets: UDP
// destination port 6784 (Section 2.2), the dedicated destination MAC
// 01-00-5E-90-00-01 (Section 2.3, MUST in Down/Init), TTL 255 (RFC 5881
// Section 5), on the member link the session is bound to. It runs with the
// members enslaved to the bond and with the members standalone, the state a
// member is in after the kernel-bond actuator removes it (Sections 3 and 5).
func TestMicroBFDLAGMemberWire(t *testing.T) {
	assertNetworkNoneIsolation(t)

	nl := newRouteSocket(t)
	defer nl.close(t)
	for _, phase := range []string{"enslaved", "standalone"} {
		t.Run(phase, func(t *testing.T) {
			createMicroLAG(t, nl, phase == "enslaved")

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			startMicroBFD(ctx, t)

			frames := captureMicroMembers(t, phase, microCaptureWindow)
			writeJSONArtifact(t, "microbfd-wire-"+phase+".json", frames)

			for _, m := range microMembers() {
				assertMicroInitialDown(t, m, frames[m.peer])
			}
		})
	}
}

func assertMicroInitialDown(t *testing.T, m microMember, frames []microFrame) {
	t.Helper()
	local, err := net.InterfaceByName(m.local)
	if err != nil {
		t.Fatalf("resolve %s: %v", m.local, err)
	}
	var first *microFrame
	for i := range frames {
		if frames[i].DstPort == netio.PortMicroBFD {
			first = &frames[i]
			break
		}
	}
	if first == nil {
		t.Errorf("member %s: no UDP %d frame within %s; frames=%s",
			m.local, netio.PortMicroBFD, microCaptureWindow, frameSummaries(frames))
		return
	}
	if first.DstMAC != microDedicatedMAC().String() {
		t.Errorf("member %s: initial %s packet dst MAC %s, want %s (RFC 7130 Section 2.3)",
			m.local, first.BFDState, first.DstMAC, microDedicatedMAC())
	}
	if first.SrcMAC != local.HardwareAddr.String() {
		t.Errorf("member %s: src MAC %s, want member MAC %s", m.local, first.SrcMAC, local.HardwareAddr)
	}
	if first.TTL != 255 {
		t.Errorf("member %s: TTL %d, want 255", m.local, first.TTL)
	}
	if first.BFDState != bfd.StateDown.String() {
		t.Errorf("member %s: first state %s, want Down", m.local, first.BFDState)
	}
}

func frameSummaries(frames []microFrame) string {
	var b bytes.Buffer
	for i, f := range frames {
		if i == 8 {
			fmt.Fprintf(&b, " ... (%d total)", len(frames))
			break
		}
		fmt.Fprintf(&b, "[%s %s>%s %s] ", f.Offset, f.SrcMAC, f.DstMAC, f.Summary)
	}
	if b.Len() == 0 {
		return "none"
	}
	return b.String()
}

// createMicroLAG builds microLAG (balance-rr) with microLocalAddr and both
// member links, enslaved when enslave is set, and removes it at cleanup.
func createMicroLAG(t *testing.T, nl *routeSocket, enslave bool) {
	t.Helper()
	t.Cleanup(func() {
		for _, m := range microMembers() {
			nl.deleteLinkByName(m.local)
		}
		nl.deleteLinkByName(microLAG)
	})
	if addErr := nl.addLink(microLAG, "bond"); addErr != nil {
		t.Fatalf("add bond %s: %v", microLAG, addErr)
	}
	lag, lagErr := net.InterfaceByName(microLAG)
	if lagErr != nil {
		t.Fatalf("resolve %s: %v", microLAG, lagErr)
	}
	names := make([]string, 0, 1+2*len(microMembers()))
	names = append(names, microLAG)
	for _, m := range microMembers() {
		if err := nl.addVethPair(m.local, m.peer); err != nil {
			t.Fatalf("add veth %s/%s: %v", m.local, m.peer, err)
		}
		names = append(names, m.local, m.peer)
		if !enslave {
			continue
		}
		if err := nl.setMaster(m.local, lag.Index); err != nil {
			t.Fatalf("enslave %s to %s: %v", m.local, microLAG, err)
		}
	}
	for _, name := range names {
		if err := nl.setLinkUp(name, true); err != nil {
			t.Fatalf("set %s up: %v", name, err)
		}
	}
	if err := nl.addIPv4Address(lag.Index, microLocalAddr(), 30); err != nil {
		t.Fatalf("add %s to %s: %v", microLocalAddr(), microLAG, err)
	}
	slaves, readErr := os.ReadFile(filepath.Join("/sys/class/net", microLAG, "bonding", "slaves"))
	if readErr != nil {
		t.Fatalf("read bond slaves: %v", readErr)
	}
	t.Logf("%s slaves: %s", microLAG, bytes.TrimSpace(slaves))
}

// startMicroBFD runs the GoBFD Micro-BFD path the daemon wires in
// cmd/gobfd/micro_bfd.go: one device-bound port 6784 listener per member,
// and one group reconciled with member senders using WithDstPort(6784) and
// WithBindDevice(member).
func startMicroBFD(ctx context.Context, t *testing.T) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	go mgr.RunDispatch(ctx)

	listeners, members := openMicroListeners(t) //nolint:contextcheck // NewListener takes no context.
	go func() { _ = netio.NewReceiver(mgr, logger).Run(ctx, listeners...) }()

	result := mgr.ReconcileMicroBFDGroupsDetailed(ctx, []bfd.MicroBFDReconcileConfig{{
		Key: microLAG,
		Config: bfd.MicroBFDConfig{
			LAGInterface:          microLAG,
			MemberLinks:           members,
			PeerAddr:              microPeerAddr(),
			LocalAddr:             microLocalAddr(),
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
			MinActiveLinks:        1,
		},
		SenderLeaseFactory: microSenderLeases(logger), //nolint:contextcheck // NewUDPSender takes no context.
	}})
	if err := result.Err(); err != nil {
		t.Fatalf("reconcile Micro-BFD group: %v", err)
	}
}

// captureMicroMembers runs an AF_PACKET peer on every member's far end for
// window. The peer answers ARP for microPeerAddr like a host on the link and
// records every received frame; each member's frames also land in a pcap.
func captureMicroMembers(t *testing.T, phase string, window time.Duration) map[string][]microFrame {
	t.Helper()
	start := time.Now()
	out := make(map[string][]microFrame, len(microMembers()))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, m := range microMembers() {
		wg.Go(func() {
			frames, err := capturePeer(m.peer, start, window)
			if err != nil {
				t.Errorf("capture on %s: %v", m.peer, err)
			}
			for i := range frames {
				frames[i].Member = m.local
			}
			writePcapArtifact(t, "microbfd-"+phase+"-"+m.local+".pcap", frames)
			mu.Lock()
			out[m.peer] = frames
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

func capturePeer(ifName string, start time.Time, window time.Duration) ([]microFrame, error) {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", ifName, err)
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(ethPAll)))
	if err != nil {
		return nil, fmt.Errorf("open AF_PACKET socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(ethPAll), Ifindex: iface.Index}); err != nil {
		return nil, fmt.Errorf("bind AF_PACKET to %s: %w", ifName, err)
	}
	tv := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return nil, fmt.Errorf("set SO_RCVTIMEO: %w", err)
	}

	var frames []microFrame
	buf := make([]byte, 2048)
	for time.Since(start) < window {
		n, from, err := unix.Recvfrom(fd, buf, 0)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return frames, fmt.Errorf("receive on %s: %w", ifName, err)
		}
		if ll, ok := from.(*unix.SockaddrLinklayer); ok && ll.Pkttype == packetOutgoing {
			continue
		}
		frame := decodeMicroFrame(append([]byte(nil), buf[:n]...), time.Now(), start)
		frames = append(frames, frame)
		if reply := arpReplyFor(frame.raw, iface.HardwareAddr, microPeerAddr()); reply != nil {
			if _, err := unix.Write(fd, reply); err != nil {
				return frames, fmt.Errorf("send ARP reply on %s: %w", ifName, err)
			}
		}
	}
	return frames, nil
}

func decodeMicroFrame(raw []byte, at, start time.Time) microFrame {
	f := microFrame{raw: raw, at: at, Offset: at.Sub(start).Round(time.Millisecond).String()}
	if len(raw) < 14 {
		f.Summary = "runt"
		return f
	}
	f.DstMAC = net.HardwareAddr(raw[0:6]).String()
	f.SrcMAC = net.HardwareAddr(raw[6:12]).String()
	etherType := binary.BigEndian.Uint16(raw[12:14])
	f.EtherType = fmt.Sprintf("0x%04x", etherType)
	payload := raw[14:]
	switch etherType {
	case ethTypeARP:
		if len(payload) >= 28 {
			op := binary.BigEndian.Uint16(payload[6:8])
			f.Summary = fmt.Sprintf("ARP op %d who-has %s tell %s", op,
				netip.AddrFrom4([4]byte(payload[24:28])), netip.AddrFrom4([4]byte(payload[14:18])))
		}
	case ethTypeIPv4:
		if len(payload) < 20 {
			f.Summary = "short IPv4"
			return f
		}
		ihl := int(payload[0]&0x0f) * 4
		f.TTL = payload[8]
		f.Summary = fmt.Sprintf("IPv4 %s > %s proto %d",
			netip.AddrFrom4([4]byte(payload[12:16])), netip.AddrFrom4([4]byte(payload[16:20])), payload[9])
		if payload[9] != unix.IPPROTO_UDP || len(payload) < ihl+8 {
			return f
		}
		udp := payload[ihl:]
		f.DstPort = binary.BigEndian.Uint16(udp[2:4])
		f.Summary += fmt.Sprintf(" udp %d > %d", binary.BigEndian.Uint16(udp[0:2]), f.DstPort)
		var pkt bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(udp[8:], &pkt); err == nil {
			f.BFDState = pkt.State.String()
			f.BFDDiag = pkt.Diag.String()
			f.MyDiscr = pkt.MyDiscriminator
			f.YourDiscr = pkt.YourDiscriminator
		}
	default:
		f.Summary = "other"
	}
	return f
}

// arpReplyFor returns an ARP reply when raw is a request for addr.
func arpReplyFor(raw []byte, mac net.HardwareAddr, addr netip.Addr) []byte {
	if len(raw) < 42 || binary.BigEndian.Uint16(raw[12:14]) != ethTypeARP {
		return nil
	}
	req := raw[14:42]
	target := addr.As4()
	if binary.BigEndian.Uint16(req[6:8]) != 1 || !bytes.Equal(req[24:28], target[:]) {
		return nil
	}
	reply := make([]byte, 42)
	copy(reply[0:6], raw[6:12])
	copy(reply[6:12], mac)
	binary.BigEndian.PutUint16(reply[12:14], ethTypeARP)
	arp := reply[14:]
	copy(arp[0:6], req[0:6]) // htype, ptype, hlen, plen
	binary.BigEndian.PutUint16(arp[6:8], 2)
	copy(arp[8:14], mac)
	copy(arp[14:18], target[:])
	copy(arp[18:24], req[8:14])
	copy(arp[24:28], req[14:18])
	return reply
}

// writePcapArtifact writes frames as a classic Ethernet pcap file.
func writePcapArtifact(t *testing.T, name string, frames []microFrame) {
	t.Helper()
	var b bytes.Buffer
	header := []any{uint32(0xa1b2c3d4), uint16(2), uint16(4), int32(0), uint32(0), uint32(65535), uint32(1)}
	for _, v := range header {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	for _, f := range frames {
		usec := f.at.UnixMicro()
		for _, v := range []uint32{uint32(usec / 1e6), uint32(usec % 1e6), uint32(len(f.raw)), uint32(len(f.raw))} {
			_ = binary.Write(&b, binary.LittleEndian, v)
		}
		b.Write(f.raw)
	}
	reportDir := os.Getenv("E2E_LINUX_REPORT_DIR")
	if reportDir == "" {
		reportDir = "/report"
	}
	if err := os.WriteFile(filepath.Join(reportDir, name), b.Bytes(), 0o600); err != nil {
		t.Errorf("write %s: %v", name, err)
	}
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

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
	value := make([]byte, 4)
	binary.NativeEndian.PutUint32(value, uint32(masterIndex))
	return s.request(unix.RTM_NEWLINK, 0, ifInfoMsg(iface.Index, 0, 0), netlinkAttr(unix.IFLA_MASTER, value, false))
}

// openMicroListeners opens one device-bound port 6784 listener per member.
func openMicroListeners(t *testing.T) ([]*netio.Listener, []string) {
	t.Helper()
	listeners := make([]*netio.Listener, 0, len(microMembers()))
	members := make([]string, 0, len(microMembers()))
	for _, m := range microMembers() {
		ln, err := netio.NewListener(netio.ListenerConfig{
			Addr: microLocalAddr(), IfName: m.local, Port: netio.PortMicroBFD,
		})
		if err != nil {
			t.Fatalf("listen %s port %d: %v", m.local, netio.PortMicroBFD, err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		listeners = append(listeners, ln)
		members = append(members, m.local)
	}
	return listeners, members
}

// microSenderLeases returns member senders as cmd/gobfd/micro_bfd.go builds
// them: source port from the RFC 5881 range, destination port 6784 and
// SO_BINDTODEVICE on the member link.
func microSenderLeases(logger *slog.Logger) func(member string) bfd.SenderLeaseFactory {
	ports := netio.NewSourcePortAllocator()
	return func(member string) bfd.SenderLeaseFactory {
		return func() (*bfd.SenderLease, error) {
			port, err := ports.Allocate()
			if err != nil {
				return nil, fmt.Errorf("allocate source port: %w", err)
			}
			sender, err := netio.NewUDPSender(microLocalAddr(), port, false, logger,
				netio.WithDstPort(netio.PortMicroBFD), netio.WithBindDevice(member))
			if err != nil {
				ports.Release(port)
				return nil, fmt.Errorf("create %s sender: %w", member, err)
			}
			return bfd.NewSenderLease(sender, func() error {
				ports.Release(port)
				return sender.Close()
			}), nil
		}
	}
}
