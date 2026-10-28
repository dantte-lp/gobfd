# Comparing BFD implementations

A comparison identifies implementation, release, platform, enabled procedure and evidence. Protocol coverage, operational integration and performance require different checks. GoBFD's supported boundary is the [RFC matrix](08-rfc-compliance.md); vendor documentation is indexed separately in [vendor BFD reference](reference/vendor-bfd.md).

## Evidence classes

| Source | Established scope | Exclusion |
|:---|:---|:---|
| Go component benchmarks | Named body time and allocations | UDP throughput, timer precision, scale |
| Repository C models | Local replica operations | Upstream FRR/BIRD performance or language ranking |
| Base peer tests | Configured FRR/BIRD3/Holo/Thoro interactions | Every extension or release |
| BGP tests | Selected GoBGP/FRR/BIRD3/ExaBGP coupling | Untested failure/reload continuity |
| Containerlab source | Defined owned FRR scenario | Native acceptance without a run |
| Vendor manual | Documented feature for the named product/release | GoBFD interoperability or hardware acceptance |
| Workload fixture | Observed ledger and cleanup for its inputs | Reference/full-daemon acceptance |

## Select a deployment

1. Identify required address families, single/multihop paths, authentication, Echo and forwarding-plane ownership.
2. Check the exact peer manual and GoBFD procedure boundary.
3. Select common intervals and Detect Multiplier; retain actual negotiated values.
4. Run the relevant [interop scenario](05-interop.md) with packet evidence.
5. Qualify the actual failure path and routing/actuator response under deployment load.

[Vendor reference](reference/vendor-bfd.md) links official EOS, NX-OS, Junos and Huawei material. Feature presence in that source does not admit retained-unavailable lab images or qualify this repository against those platforms. Hardware offload, timer floors, capacity and fate sharing remain exact-model/release/service questions.

## Compare software measurements

Keep session count, address families, authentication, padding, negotiated intervals, loss, CPU/memory/PID limits and measurement duration equivalent. Separate BFD load, route updates and management activity. Record actual peer behavior and build/source identities.

Local [C models](../../bench/c/) are separately compiled repository programs. Their validation, lookup and scheduling differ from Go bodies. C uses CLOCK_MONOTONIC, fixed iteration counts (default ten million; creation 10000) and six repetitions; Go uses adaptive iterations. Sequential benchmark-cross does not make those bodies equivalent.

Current Control/Echo owner goroutines have no permanent per-session thread pins. Neither goroutine ownership nor the removal of pins reserves CPU or establishes scale; matched native A/B and full workload acceptance remain open. See [runtime boundaries](14-performance-analysis.md).

## Interpret timers and packet rates

For Asynchronous Control, local Detection Time is received Detect Multiplier × max(local RequiredMinRxInterval,remote DesiredMinTxInterval). Nominal TX uses max(local DesiredMinTxInterval,remote RequiredMinRxInterval) before jitter; non-Up/held slow-start policy raises the local floor to at least one second. Zero remote RX, remote Demand and Poll transactions alter periodic sending. Source: [session_packet.go](../../internal/bfd/session_packet.go) and [protocol](02-protocol.md).

At 1000 sessions and nominal 100 ms, 10000 packets/s in one direction is arithmetic before jitter/suppression. It is not a measured supported rate. Compare actual wire arrivals, committed packets, loss and timer distributions under a complete ledger.

## Report a comparison

Record source/binary/image identities, topology, effective limits, common measurement phase, actual writes/receipts/commits, clock model, distributions, test skips and cleanup. State unresolved native or release gates. Avoid blanket fastest, zero-GC, production-grade, full-RFC or all-vendor conclusions from component data.

[Benchmarks](12-benchmarks.md), [runtime measurement](14-performance-analysis.md), [retained raw samples](../../testdata/benchmarks/).
