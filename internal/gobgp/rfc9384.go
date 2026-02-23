// RFC 9384 — BGP Cease NOTIFICATION Message Subcode for BFD-Down.
//
// RFC 9384 defines Cease subcode 10 ("BFD Down") for BGP NOTIFICATION
// messages when a BFD session failure triggers BGP session teardown.
//
// GoBGP v3 does not expose per-subcode control in its DisablePeer API;
// it uses Administrative Shutdown (subcode 2) with a communication string
// per RFC 8203. This package enriches the communication string with
// RFC 9384-compliant context so that operators can identify BFD-triggered
// shutdowns in logs and monitoring systems.

package gobgp

import (
	"fmt"
	"strings"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// CeaseSubcodeBFDDown is the IANA-assigned Cease NOTIFICATION subcode
// for BFD session failures (RFC 9384 Section 3).
const CeaseSubcodeBFDDown uint8 = 10

// bfdDownPrefix is the standardized prefix for RFC 9384 communication messages.
const bfdDownPrefix = "BFD Down (RFC 9384 Cease/10)"

// FormatBFDDownCommunication formats a BFD-Down shutdown communication
// string per RFC 9384. The returned string is suitable for the GoBGP
// DisablePeerRequest.Communication field (RFC 8203 administrative reason).
//
// Format: "BFD Down (RFC 9384 Cease/10): diag=<diagnostic_string>".
func FormatBFDDownCommunication(diag bfd.Diag) string {
	return fmt.Sprintf("%s: diag=%s", bfdDownPrefix, diag.String())
}

// ParseBFDDownCommunication checks whether a communication string was
// formatted by FormatBFDDownCommunication and extracts the diagnostic
// description. Returns the diagnostic string and true if the prefix
// matches, or empty string and false otherwise.
func ParseBFDDownCommunication(communication string) (string, bool) {
	prefix := bfdDownPrefix + ": diag="
	if !strings.HasPrefix(communication, prefix) {
		return "", false
	}

	return communication[len(prefix):], true
}
