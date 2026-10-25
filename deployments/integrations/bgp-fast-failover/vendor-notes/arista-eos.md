# Arista EOS BFD Verification Note

This note is optional. It records public Arista EOS verification points for
operators comparing the generic FRR/GoBGP example with an EOS environment. It
is not required to run `deployments/integrations/bgp-fast-failover/`.

## Public Behavior

The original check used Arista MCP. The 2026-10-25 documentation review uses
the official [EOS BFD manual](https://www.arista.com/en/um-eos/eos-bidirectional-forwarding-detection)
as its public reference; this is not a hardware interoperability result.

- `neighbor bfd` enables BFD as the failure detection mechanism for a BGP
  neighbor or peer group.
- When a BFD session established for a BGP neighbor goes Down, the associated
  BGP session is also changed to Down.
- `show bfd peers` displays BFD neighbors; its detailed output includes
  session state and type.
- [`show ip bgp neighbors`](https://www.arista.com/en/um-eos/eos-border-gateway-protocol-bgp)
  displays BGP neighbor connection status.
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

GoBFD has an opt-in Micro-BFD preview with guarded Linux kernel-bond, OVSDB
and NetworkManager actuator paths. Dedicated multicast MAC/member-wire
qualification and owner/backend interoperability remain incomplete. These
paths do not establish equivalence with EOS RFC 7130 port-channel enforcement.
See [Linux applicability](../../../../docs/en/adr/0003-linux-advanced-bfd-applicability.md)
and the [Russian configuration guide](../../../../docs/ru/03-configuration.md).
