#!/usr/bin/env python3
"""ExaBGP process API route announcer for BGP+BFD interoperability testing.

Announces 10.40.0.0/24 to GoBGP at 172.21.0.10 (ASN 65001).
This script is kept as a fallback if static route configuration is not used.

ExaBGP 5.x: ACK handling is disabled via exabgp.api.ack=false environment variable.
"""

import sys
import time

# Announce route on startup.
print("announce route 10.40.0.0/24 next-hop self", flush=True)

# Keep running — ExaBGP terminates the BGP session if the process exits.
try:
    while True:
        time.sleep(60)
except KeyboardInterrupt:
    sys.exit(0)
