package netio

import (
	"errors"
	"net"
	"testing"
)

func TestOverlayBufferErrorsAreWrapped(t *testing.T) {
	t.Parallel()
	for _, sizes := range [][2]int{{8192, 0}, {0, 8192}} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if err := configureOverlayBuffers(conn, sizes[0], sizes[1]); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("configureOverlayBuffers error = %v, want wrapped net.ErrClosed", err)
		}
	}
}
