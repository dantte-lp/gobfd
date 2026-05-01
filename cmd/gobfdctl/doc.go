/*
Gobfdctl manages a running GoBFD daemon from the command line.

The client connects to the daemon ConnectRPC API and provides commands for
listing, showing, creating, deleting, and monitoring BFD sessions. Output can be
rendered as a table, JSON, or YAML, and an interactive shell is available for
operator workflows.

Usage:

	gobfdctl [--addr host:port] [--format table|json|yaml] <command>

The primary commands are:

	session list
		List all BFD sessions.
	session show <peer-address-or-discriminator>
		Show one BFD session.
	session add --peer <address> [flags]
		Create a single-hop or multi-hop BFD session.
	session delete <discriminator>
		Delete a BFD session by local discriminator.
	monitor [--current]
		Stream BFD session events.
	shell
		Start an interactive command shell.
	version
		Print build information.
*/
package main
