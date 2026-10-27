//go:build linux

package netio

import (
	"fmt"
	"log/slog"
	"math"

	"golang.org/x/sys/unix"
)

const maxSenderDSCP = 63

func (q SocketQoS) validate() error {
	if q.IPv4DSCP < 0 || q.IPv4DSCP > maxSenderDSCP {
		return fmt.Errorf("ipv4_dscp outside 0-63: %w", ErrInvalidSocketQoS)
	}
	if q.IPv6TrafficClass < 0 || q.IPv6TrafficClass > math.MaxUint8 {
		return fmt.Errorf("ipv6_traffic_class outside 0-255: %w", ErrInvalidSocketQoS)
	}
	if q.Priority < 0 || q.Priority > math.MaxInt32 {
		return fmt.Errorf("priority outside 0-2147483647: %w", ErrInvalidSocketQoS)
	}
	return nil
}

func setSenderQoS(
	fd int, qos SocketQoS, logger *slog.Logger, setInt func(int, int, int, int) error,
) error {
	for _, option := range []struct {
		name     string
		key      int
		value    int64
		required bool
	}{
		{"SO_PRIORITY", unix.SO_PRIORITY, int64(qos.Priority), qos.PriorityRequired},
		{"SO_MARK", unix.SO_MARK, int64(qos.Mark), qos.MarkRequired},
	} {
		if option.key == unix.SO_MARK && option.value == 0 {
			continue
		}
		// SO_MARK consumes all 32 bits; the conversion preserves its bit pattern on 32-bit hosts.
		value := int(option.value) // #nosec G115 -- the kernel socket option is a uint32 bit pattern.
		if err := setInt(fd, unix.SOL_SOCKET, option.key, value); err != nil {
			wrapped := fmt.Errorf("set %s: %w", option.name, err)
			if option.required {
				return wrapped
			}
			logger.Warn("optional BFD socket QoS option unavailable",
				slog.String("option", option.name), slog.Int64("value", option.value),
				slog.Any("error", wrapped))
		}
	}
	return nil
}
