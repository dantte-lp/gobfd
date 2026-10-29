# Arista EOS verification

The official [EOS BFD manual](https://www.arista.com/en/um-eos/eos-bidirectional-forwarding-detection) documents `neighbor ADDRESS bfd` and `show bfd peers`. Interface timers use milliseconds. Confirm the selected platform/release, authentication, multihop constraints and independent asynchronous/Echo offload support before comparison.

```text
show bfd peers
```

Record neighbor, interface, session type, state, negotiated timers and diagnostics. Correlate BFD failure/recovery with BGP and independent forwarding evidence.

This optional note does not qualify GoBFD against EOS hardware. GoBFD Micro-BFD preview does not establish EOS port-channel equivalence. References: [official four-vendor guide](../../../../docs/en/reference/vendor-bfd.md), [GoBFD applicability](../../../../docs/en/adr/0003-linux-advanced-bfd-applicability.md), [configuration](../../../../docs/en/03-configuration.md).
