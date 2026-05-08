// Echo reflector for RFC 9747 BFD echo interop testing.
//
// Listens on UDP port 3785 and reflects every received packet back
// to the sender. This simulates the echo reflection function that a
// remote system performs for BFD echo mode (RFC 5881 Section 4).
//
// In production, echo reflection is done by the remote's IP forwarding
// plane. This standalone reflector provides deterministic behavior for
// containerized interop tests where IP forwarding tricks are unreliable.
package main

import (
	"log"
	"net"
	"syscall"
)

func main() {
	addr, err := net.ResolveUDPAddr("udp4", ":3785")
	if err != nil {
		log.Fatalf("resolve address: %v", err)
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Fatalf("listen UDP :3785: %v", err)
	}

	if err := setUDPTTL(conn, 254); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			log.Printf("close UDP socket after TTL setup error: %v", closeErr)
		}
		log.Fatalf("set reflected packet TTL: %v", err)
	}
	defer conn.Close()

	log.Println("echo reflector listening on :3785")

	buf := make([]byte, 9000)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("read error: %v", err)
			continue
		}

		// Reflect the packet back to the sender on port 3785.
		dst := &net.UDPAddr{
			IP:   remote.IP,
			Port: 3785,
		}
		if _, err := conn.WriteToUDP(buf[:n], dst); err != nil {
			log.Printf("write error to %s: %v", dst, err)
		}
	}
}

func setUDPTTL(conn *net.UDPConn, ttl int) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return err
	}

	var sockErr error
	if err := rawConn.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
	}); err != nil {
		return err
	}
	return sockErr
}
