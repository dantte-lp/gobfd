// Command bfd-fuzz sends the repository's RFC invalid-vector corpus over UDP.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	defaultTarget       = "172.20.0.10"
	bfdDestinationPort  = 3784
	bfdSourcePort       = 49152
	fuzzerTimeout       = 90 * time.Second
	settleTime          = 2 * time.Second
	packetWriteTimeout  = 2 * time.Second
	detailPreviewLength = 5
)

var (
	errInvalidIPv4Target  = errors.New("invalid IPv4 target")
	errShortDatagramWrite = errors.New("short UDP datagram write")
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	ctx, cancel := context.WithTimeout(context.Background(), fuzzerTimeout)
	defer cancel()
	if err := run(ctx, os.Getenv, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "BFD invalid-vector fuzzer: %v\n", err)
		return 2
	}
	return 0
}

func run(ctx context.Context, getenv func(string) string, output io.Writer) (runErr error) {
	targetText := getenv("GOBFD_IP")
	if targetText == "" {
		targetText = defaultTarget
	}
	target, parseErr := netip.ParseAddr(targetText)
	if parseErr != nil {
		return fmt.Errorf("parse IPv4 GOBFD_IP %q: %w", targetText, parseErr)
	}
	if !target.Is4() {
		return fmt.Errorf("parse IPv4 GOBFD_IP %q: %w", targetText, errInvalidIPv4Target)
	}

	sender, err := newUDPSender(target)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, sender.Close())
	}()

	cases, err := invalidVectorCases()
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "BFD Go invalid-vector fuzzer: targeting %s:%d\n", target, bfdDestinationPort)
	fmt.Fprintf(output, "Running %d test cases...\n\n", len(cases))
	for _, testCase := range cases {
		if sendErr := sendVectorCase(ctx, sender, testCase); sendErr != nil {
			return sendErr
		}
		writeCaseSummary(output, testCase)
	}

	fmt.Fprintf(output, "\nWaiting %s for GoBFD to process packets...\n", settleTime)
	if settleErr := waitForSettle(ctx); settleErr != nil {
		return settleErr
	}
	probe, err := validProbePacket()
	if err != nil {
		return err
	}
	if err := sender.Send(ctx, probe); err != nil {
		return fmt.Errorf("send final valid probe: %w", err)
	}
	fmt.Fprintln(output, "All invalid vectors and the valid probe were sent; caller verifies GoBFD liveness.")
	return nil
}

type udpSender struct {
	conn        *net.UDPConn
	ipv4        *ipv4.PacketConn
	destination *net.UDPAddr
}

func newUDPSender(target netip.Addr) (*udpSender, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: bfdSourcePort})
	if err != nil {
		return nil, fmt.Errorf("listen on BFD source port %d: %w", bfdSourcePort, err)
	}
	return &udpSender{
		conn:        conn,
		ipv4:        ipv4.NewPacketConn(conn),
		destination: net.UDPAddrFromAddrPort(netip.AddrPortFrom(target, bfdDestinationPort)),
	}, nil
}

func (sender *udpSender) Send(ctx context.Context, packet vectorPacket) error {
	if err := sender.ipv4.SetTTL(packet.TTL); err != nil {
		return fmt.Errorf("set TTL %d for %s: %w", packet.TTL, packet.Detail, err)
	}
	deadline := time.Now().Add(packetWriteTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := sender.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set write deadline for %s: %w", packet.Detail, err)
	}
	written, err := sender.conn.WriteToUDP(packet.Payload, sender.destination)
	if err != nil {
		return fmt.Errorf("send %s discriminator %#x: %w", packet.Detail, discriminator(packet.Payload), err)
	}
	if written != len(packet.Payload) {
		return fmt.Errorf("send %s: wrote %d bytes, want %d: %w",
			packet.Detail, written, len(packet.Payload), errShortDatagramWrite)
	}
	return nil
}

func (sender *udpSender) Close() error {
	if err := sender.ipv4.Close(); err != nil {
		return fmt.Errorf("close IPv4 packet connection: %w", err)
	}
	return nil
}

func sendVectorCase(ctx context.Context, sender *udpSender, testCase vectorCase) error {
	for _, packet := range testCase.Packets {
		if err := sender.Send(ctx, packet); err != nil {
			return fmt.Errorf("send vector group %s: %w", testCase.Name, err)
		}
	}
	return nil
}

func writeCaseSummary(output io.Writer, testCase vectorCase) {
	preview := min(len(testCase.Packets), detailPreviewLength)
	fmt.Fprintf(output, "  SENT  %s: ", testCase.Name)
	for index, packet := range testCase.Packets[:preview] {
		if index != 0 {
			fmt.Fprint(output, ", ")
		}
		fmt.Fprint(output, packet.Detail)
	}
	fmt.Fprintln(output)
	if remaining := len(testCase.Packets) - preview; remaining > 0 {
		fmt.Fprintf(output, "        ... and %d more\n", remaining)
	}
}

func waitForSettle(ctx context.Context) error {
	timer := time.NewTimer(settleTime)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for GoBFD packet processing: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
