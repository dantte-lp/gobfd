//go:build !linux

package gobgp

import "net"

type unixTransportIdentity struct {
	peerPID       int32
	peerUID       uint32
	socketDev     uint64
	socketIno     uint64
	bootID        string
	procStartTime uint64
}

func captureUnixTransportIdentity(_ net.Conn, _ string) (unixTransportIdentity, bool) {
	return unixTransportIdentity{}, false
}
