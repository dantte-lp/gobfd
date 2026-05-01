/*
Gobfd runs the GoBFD daemon.

The daemon implements Bidirectional Forwarding Detection control-plane
behavior for single-hop, multi-hop, unsolicited, echo, Micro-BFD, VXLAN, and
Geneve sessions. It loads a YAML configuration file, opens BFD packet
listeners, exposes a ConnectRPC API for session management, exports Prometheus
metrics, and integrates with GoBGP for routing-protocol notifications.

Usage:

	gobfd -config /etc/gobfd/gobfd.yml
	gobfd -version

The flags are:

	-config
		Path to the YAML configuration file.
	-version
		Print build information and exit.

The daemon expects Linux networking privileges required by the selected
dataplane backend. Raw UDP BFD packet I/O requires CAP_NET_RAW; integrations
that reconcile Linux network state can also require CAP_NET_ADMIN.
*/
package main
