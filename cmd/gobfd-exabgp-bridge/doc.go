/*
Gobfd-exabgp-bridge announces and withdraws ExaBGP routes from GoBFD session
state.

ExaBGP runs this command as a process. The bridge watches GoBFD session events
over ConnectRPC, writes ExaBGP route commands to standard output, and writes
logs to standard error. When the selected BFD peer is Up, the bridge announces
the configured prefix with next-hop self. When the peer is Down or AdminDown,
the bridge withdraws the prefix.

Usage:

	GOBFD_ADDR=http://127.0.0.1:50052 \
	GOBFD_PEER=192.0.2.2 \
	ANYCAST_PREFIX=198.51.100.1/32 \
	gobfd-exabgp-bridge

The environment variables are:

	GOBFD_ADDR
		GoBFD ConnectRPC endpoint. The default is http://127.0.0.1:50052.
	GOBFD_PEER
		BFD peer address to watch. This variable is required.
	ANYCAST_PREFIX
		Route prefix to announce and withdraw. This variable is required.
*/
package main
