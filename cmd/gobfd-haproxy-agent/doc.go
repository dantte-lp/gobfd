/*
Gobfd-haproxy-agent exposes GoBFD session state through the HAProxy agent-check
protocol.

The agent watches BFD session events over the GoBFD ConnectRPC API and serves a
TCP listener for each configured backend. HAProxy reads a short text response
from the listener: an Up BFD session returns "up ready\n"; any other state
returns "down\n".

Usage:

	gobfd-haproxy-agent -config /etc/gobfd/haproxy-agent.yml
	gobfd-haproxy-agent -gobfd-addr unix:///run/gobfd/gobfd.sock -config haproxy-agent.yml
	gobfd-haproxy-agent -version

The flags are:

	-config
		Path to the YAML backend configuration file.
	-gobfd-addr
		GoBFD ConnectRPC endpoint: http://host:port, https://host:port,
		unix:///path or a bare host:port. The default is GOBFD_ADDR or
		unix:///run/gobfd/gobfd.sock. For an https:// endpoint the client TLS
		files come from GOBFD_TLS_CA_FILE, GOBFD_TLS_CERT_FILE and
		GOBFD_TLS_KEY_FILE.
	-version
		Print build information and exit.
*/
package main
