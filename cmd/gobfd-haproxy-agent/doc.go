/*
Gobfd-haproxy-agent exposes GoBFD session state through the HAProxy agent-check
protocol.

The agent watches BFD session events over the GoBFD ConnectRPC API and serves a
TCP listener for each configured backend. HAProxy reads a short text response
from the listener: an Up BFD session returns "up ready\n"; any other state
returns "down\n".

Usage:

	gobfd-haproxy-agent -config /etc/gobfd/haproxy-agent.yml
	gobfd-haproxy-agent -gobfd-addr http://127.0.0.1:50052 -config haproxy-agent.yml
	gobfd-haproxy-agent -version

The flags are:

	-config
		Path to the YAML backend configuration file.
	-gobfd-addr
		GoBFD ConnectRPC endpoint. The default is GOBFD_ADDR or
		http://127.0.0.1:50052.
	-version
		Print build information and exit.
*/
package main
