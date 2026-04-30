# Arista EOS BFD Verification Note

This note is optional. It records public Arista EOS verification points for
operators comparing the generic FRR/GoBGP example with an EOS environment. It
is not required to run `deployments/integrations/bgp-fast-failover/`.

## MCP-Validated Public Behavior

Arista MCP was used to validate these EOS facts:

- `neighbor bfd` enables BFD as the failure detection mechanism for a BGP
  neighbor or peer group.
- When a BFD session established for a BGP neighbor goes Down, the associated
  BGP session is also changed to Down.
- `show bfd peers` displays BFD state and session type. Documented states
  include `Init`, `Down`, `AdminDown`, and `Up`; documented session types
  include `Normal`, `Per-link`, `RFC7130`, and `Vxlan`.
- `show ip bgp neighbors` displays IPv4 BGP and TCP-session data for a
  specified BGP neighbor, or for all IPv4 BGP neighbors when no address is
  specified.
- RFC 7130 port-channel mode removes a member from the port channel when its
  micro-session is Down, and adds it back when the session comes Up.

## Suggested Verification Commands

Use site-specific addresses, ASNs, VRFs, and interfaces. These commands are
verification aids, not a complete EOS configuration template:

```text
show bfd peers
show ip bgp neighbors
show port-channel <N> detail
```

For BGP fast-failover comparisons, verify that the BGP neighbor state follows
the BFD session state. For RFC 7130 Micro-BFD comparisons, verify both
`show bfd peers` session type and `show port-channel <N> detail` member state.

## Boundary

GoBFD currently detects and reports Micro-BFD session state. Linux LAG
enforcement that disables or removes a bond/team/OVS member is tracked as S7.1
in the project roadmap. Do not treat the current GoBFD Micro-BFD support as the
same behavior as EOS RFC 7130 port-channel member enforcement.
