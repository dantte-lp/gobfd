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
