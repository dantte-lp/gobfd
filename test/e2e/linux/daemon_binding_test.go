//go:build e2e_linux

package linux_test

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func claimDaemonBinding(t *testing.T, daemon *pendingDaemon, local netip.Addr, discriminator uint32) {
	t.Helper()
	request := &bfdv1.AddSessionRequest{
		PeerAddress: local.Next().String(), LocalAddress: local.String(), InterfaceName: pendingLink,
		Type: bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, DetectMultiplier: 3,
		DesiredMinTxInterval: durationpb.New(time.Second), RequiredMinRxInterval: durationpb.New(time.Second),
	}
	for attempt := range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		response := new(bfdv1.AddSessionResponse)
		err := daemon.conn.Invoke(ctx, "/"+bfdv1connect.BfdServiceName+"/AddSession", request, response)
		cancel()
		if attempt == 1 {
			if status.Code(err) != codes.AlreadyExists {
				t.Fatalf("repeated API claim: got %v, want AlreadyExists", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if response.GetSession().GetLocalDiscriminator() != discriminator {
			t.Fatal("API claim replaced the config-owned physical session")
		}
	}
}

func releaseDaemonBinding(t *testing.T, daemon *pendingDaemon, discriminator uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := daemon.conn.Invoke(ctx, "/"+bfdv1connect.BfdServiceName+"/DeleteSession",
		&bfdv1.DeleteSessionRequest{LocalDiscriminator: discriminator}, new(bfdv1.DeleteSessionResponse)); err != nil {
		t.Fatal(err)
	}
}

type daemonBindingProbe struct {
	Phase         string `json:"phase"`
	Received      uint64 `json:"received"`
	SourcePort    uint16 `json:"source_port"`
	Discriminator uint32 `json:"discriminator"`
	IfIndex       int    `json:"if_index"`
}

func probeDaemonRetainedBinding(
	t *testing.T, nl *routeSocket, daemon *pendingDaemon, local netip.Addr, phase string, priorReceived uint64,
) daemonBindingProbe {
	t.Helper()
	peerAddr := local.Next()
	peerInterface, err := net.InterfaceByName("gobfdq1")
	if err != nil {
		t.Fatal(err)
	}
	present, err := daemonAddressPresent(peerInterface.Index, peerAddr)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		prefix := uint8(24)
		if local.Is6() {
			prefix = 64
		}
		if assignErr := nl.addIPAddress(peerInterface.Index, peerAddr, prefix); assignErr != nil {
			t.Fatal(assignErr)
		}
	}
	receiver, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(peerAddr, netio.PortSingleHop)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := receiver.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	sender, err := netio.NewUDPSender(peerAddr, 49153, false, slog.New(slog.DiscardHandler),
		netio.WithBindDevice(pendingLink))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	before, err := daemon.observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	discriminator := before.Sessions[peerAddr.String()]
	if discriminator == 0 {
		t.Fatal("probe session missing")
	}
	packet := bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateInit, DetectMult: 3,
		MyDiscriminator: 177, YourDiscriminator: discriminator,
		DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	send := func() {
		t.Helper()
		var wire [bfd.MaxPacketSize]byte
		length, marshalErr := bfd.MarshalControlPacket(&packet, wire[:])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if sendErr := sender.SendPacket(t.Context(), wire[:length], local); sendErr != nil {
			t.Fatal(sendErr)
		}
	}
	started := time.Now()
	send()
	committed := daemon.await(t, phase+"-owner-commit", 2, true, func(observation daemonObservation) bool {
		return observation.Received[peerAddr.String()] > priorReceived &&
			observation.States[peerAddr.String()] == bfdv1.SessionState_SESSION_STATE_UP
	})
	if deadlineErr := receiver.SetReadDeadline(time.Now().Add(10 * time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	var settleUntil time.Time
	var awaitingSeparation bool
	var incoming [bfd.MaxPacketSize]byte
	for {
		n, remote, readErr := receiver.ReadFromUDPAddrPort(incoming[:])
		if readErr != nil {
			t.Fatal(readErr)
		}
		var control bfd.ControlPacket
		if decodeErr := bfd.UnmarshalControlPacket(incoming[:n], &control); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if control.MyDiscriminator != discriminator || remote.Addr().Unmap() != local.Unmap() || remote.Port() < 49152 {
			t.Fatalf("unexpected daemon TX: discriminator=%d remote=%s", control.MyDiscriminator, remote)
		}
		if control.YourDiscriminator != packet.MyDiscriminator {
			continue
		}
		if !control.Poll && control.State != bfd.StateUp {
			continue
		}
		packet.State, packet.Final = bfd.StateUp, control.Poll
		send()
		prior := committed.Received[peerAddr.String()]
		committed = daemon.await(t, phase+"-peer-commit", 2, true, func(observation daemonObservation) bool {
			return observation.Received[peerAddr.String()] > prior &&
				observation.States[peerAddr.String()] == bfdv1.SessionState_SESSION_STATE_UP
		})
		if control.Poll {
			awaitingSeparation = true
			continue
		}
		if awaitingSeparation {
			// Non-Poll TX follows Final processing; elapsed time bounds its RTT and separation (RFC 5880 Section 6.5).
			now := time.Now()
			settleUntil = now.Add(max(time.Microsecond, now.Sub(started)))
			awaitingSeparation = false
			continue
		}
		if time.Now().Before(settleUntil) {
			continue
		}
		iface, resolveErr := net.InterfaceByName(pendingLink)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		proof := daemonBindingProbe{
			Phase: phase, Received: committed.Received[peerAddr.String()],
			SourcePort: remote.Port(), Discriminator: discriminator, IfIndex: iface.Index,
		}
		family := "ipv4"
		if local.Is6() {
			family = "ipv6"
		}
		writeJSONArtifact(t, "daemon-binding-"+family+"-"+phase+".json", proof)
		return proof
	}
}
