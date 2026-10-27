package netio

import "errors"

// SocketQoS is the immutable marking and local queue policy of an ordinary UDP sender.
// It does not configure overlay outer sockets or local address probes.
type SocketQoS struct {
	IPv4DSCP         int
	IPv6TrafficClass int
	Priority         int
	Mark             uint32
	PriorityRequired bool
	MarkRequired     bool
}

// ErrInvalidSocketQoS rejects out-of-range direct sender options before socket creation.
var ErrInvalidSocketQoS = errors.New("invalid BFD sender socket QoS")
