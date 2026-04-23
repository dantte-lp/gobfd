// Package sdnotify implements the systemd sd_notify protocol for
// readiness, stopping, and watchdog notifications.
//
// The protocol writes state strings to a Unix datagram socket whose
// address is read from $NOTIFY_SOCKET. When the environment variable
// is unset or empty, all operations are silent no-ops (the daemon is
// not running under systemd).
//
// See sd_notify(3) and systemd.service(5) for the full specification.
package sdnotify

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// Notification state constants per sd_notify(3).
const (
	// Ready signals that daemon initialization is complete.
	Ready = "READY=1"
	// Stopping signals that the daemon is beginning graceful shutdown.
	Stopping = "STOPPING=1"
	// Watchdog signals a keepalive for the systemd watchdog.
	Watchdog = "WATCHDOG=1"
)

// Notify sends a notification state string to systemd via the
// $NOTIFY_SOCKET Unix datagram socket. Returns (false, nil) when
// not running under systemd (NOTIFY_SOCKET unset).
func Notify(state string) (bool, error) {
	socketAddr := os.Getenv("NOTIFY_SOCKET")
	if socketAddr == "" {
		return false, nil
	}

	// Abstract namespace sockets start with '@' in the env var;
	// the kernel expects a leading NUL byte instead.
	if socketAddr[0] == '@' {
		socketAddr = "\x00" + socketAddr[1:]
	}

	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{
		Name: socketAddr,
		Net:  "unixgram",
	})
	if err != nil {
		return false, fmt.Errorf("dial notify socket: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(state)); err != nil {
		return false, fmt.Errorf("write notify state %q: %w", state, err)
	}

	return true, nil
}

// WatchdogEnabled checks whether the systemd watchdog is enabled and
// returns the configured interval. Returns (0, nil) when the watchdog
// is not enabled or the current process is not the watched PID.
func WatchdogEnabled() (time.Duration, error) {
	usecStr := os.Getenv("WATCHDOG_USEC")
	if usecStr == "" {
		return 0, nil
	}

	// If WATCHDOG_PID is set, it must match our PID.
	if pidStr := os.Getenv("WATCHDOG_PID"); pidStr != "" {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			return 0, fmt.Errorf("parse WATCHDOG_PID %q: %w", pidStr, err)
		}

		if pid != os.Getpid() {
			return 0, nil
		}
	}

	usec, err := strconv.ParseInt(usecStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse WATCHDOG_USEC %q: %w", usecStr, err)
	}

	return time.Duration(usec) * time.Microsecond, nil
}
