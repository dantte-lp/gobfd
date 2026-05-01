# Competitive Analysis: BFD Implementations

> Comparison of GoBFD with open-source and vendor BFD implementations.

---

### Table of Contents

- [Software Implementations](#software-implementations)
  - [FRR bfdd](#frr-bfdd)
  - [BIRD2/BIRD3](#bird2bird3)
  - [aiobfd](#aiobfd)
  - [Go Implementations](#go-implementations)
- [Feature Matrix](#feature-matrix)
- [Performance Comparison](#performance-comparison)
- [Hardware Reference](#hardware-reference)
- [Production Timer Targets](#production-timer-targets)
- [Methodology and Fair Disclosure](#methodology-and-fair-disclosure)

---

### Software Implementations

#### FRR bfdd

[FRR](https://frrouting.org/) (Free Range Routing) is the de-facto standard open-source routing suite. Its BFD daemon (`bfdd`) is the most widely deployed software BFD implementation.

**Defaults**: 300ms TX/RX, 50ms echo TX/RX, detect multiplier 3.

**Scaling**: SONiC (Azure/Microsoft) validated **64 BFD sessions** at 300ms detection time on commodity hardware. This is the documented ceiling for their software-based deployment. FRR itself has no hard session limit — 64 is simply the threshold tested by SONiC.

**Architecture**: Single-threaded. BFD and BGP compete for the same process. Under CPU load (e.g., processing 890K BGP routes on a route reflector), `bgpd` occupies 100% of one core for 1-2 seconds, causing BFD session flapping (FRR GitHub Issue #9078).

**Packet handling**: FRR rebuilds the BFD packet on every send. The SONiC HLD explicitly notes this inefficiency: "In the current FRR implementation, the BFD packet is rebuilt on every send — this is overhead given that BFD needs to send packets every few milliseconds." The recommendation is to cache the packet in memory and replay it.

**Hardware offload**: Supports distributed BFD data plane (`--dplaneaddr`), allowing TX/RX to be offloaded to hardware/ASIC, but only on vendor platforms.

#### BIRD2/BIRD3

[BIRD](https://bird.network.cz/) is a lightweight routing daemon popular in IXP and research environments.

**RFC support**: RFC 5880/5881/5883/5882. No echo mode, no demand mode.

**CPI bit**: The Control Plane Independent bit is **not set** in BFD packets (confirmed by tcpdump analysis, Vincent Bernat). This means BFD sessions suffer when BIRD's CPU is under load.

**Scaling**: No public scaling benchmarks. Used in production at Wikimedia Foundation (with Juniper MX) and in Cilium/Kubernetes environments with 300ms intervals.

#### aiobfd

[aiobfd](https://github.com/network-tools/aiobfd) is a Python asyncio BFD implementation.

**Timer achievement**: Successfully tested with **10ms intervals** when paired with hardware routers. Software-to-software requires ~300ms detection time.

**Limitations**:
- GIL (Global Interpreter Lock) limits true parallelism
- asyncio event loop jitter: ~1-15ms timer accuracy
- No echo mode, no authentication, no demand mode
- Not tested for scaling — likely limited to tens of sessions with aggressive timers

#### Go Implementations

**Thoro/bfd** (8 stars): gRPC interface for monitoring. Known bugs: panic when updating timers during an active session. Only simple password authentication. No benchmarks.

**jthurman42/go-bfd**: Self-described as "Experimental, incomplete and probably very wrong." Packet parsing library, not a daemon. Default timers 1s. Supports all authentication types.

**google/gopacket**: Contains a BFD layer for packet parsing, not a protocol implementation.

**GoBFD**: First production-oriented Go BFD implementation with zero-allocation hot path, base RFC 5880/5881 coverage, authentication, unaffiliated echo, overlay support (VXLAN/Geneve), and comprehensive benchmarks.

---

### Feature Matrix

| Feature | GoBFD | FRR bfdd | BIRD3 | aiobfd |
|---------|:-----:|:--------:|:-----:|:------:|
| RFC 5880 (base BFD) | Yes | Yes | Yes | Partial |
| RFC 5881 (IPv4/IPv6) | Yes | Yes | Yes | Partial |
| Echo mode (RFC 9747) | Yes | Yes | No | No |
| Demand mode | Yes | Partial | No | No |
| Auth MD5 (RFC 5880 §6.7) | Yes | Yes | Limited | No |
| Auth SHA1 (RFC 5880 §6.7) | Yes | Yes | Limited | No |
| CPI bit (Control Plane Independent) | Yes | Partial | No | No |
| VXLAN BFD (RFC 8971) | Yes | No | No | No |
| Geneve BFD (RFC 9521) | Yes | No | No | No |
| Micro-BFD (RFC 7130) | Yes | No | No | No |
| Zero-alloc hot path | Yes | N/A (C) | N/A (C) | No |
| GoBGP integration | Yes | N/A | N/A | No |
| Prometheus metrics | Yes | Via SNMP | No | No |
| ConnectRPC API | Yes | Vtysh CLI | CLI | No |

---

### Performance Comparison

> All GoBFD numbers are from micro-benchmarks (`go test -bench`). FRR and BIRD numbers are from public documentation, issue trackers, and third-party reports. These are **not** head-to-head benchmarks — different languages, architectures, and measurement methodologies make direct comparison imprecise. We present them for context, not as definitive rankings.

#### Packet Processing

| Metric | GoBFD | FRR bfdd | aiobfd |
|--------|------:|----------|--------|
| Packet marshal | 5.9 ns/op, 0 allocs | Rebuilt every send (SONiC HLD) | Python object serialization |
| Packet unmarshal | 11.0 ns/op, 0 allocs | C struct cast | Python parsing |
| Full RX path | 64 ns/op, 0 allocs | Not published | Not published |
| Full TX path | 6.7 ns/op, 0 allocs | Not published | Not published |

**Key insight**: GoBFD uses cached pre-built packets (copy, not rebuild). FRR rebuilds the BFD packet on every transmit. At 1000 sessions with 100ms intervals, this is 10,000 rebuilds per second in FRR vs. 10,000 memory copies in GoBFD.

#### Session Scaling

| Metric | GoBFD | FRR bfdd (SONiC) | aiobfd |
|--------|------:|:-----------------:|:------:|
| Max sessions tested | 1,000 (benchmark) | 64 (validated) | ~10 (estimated) |
| Session demux | O(1), 51 ns/op | Linear scan | Python dict |
| Memory per session | ~3 KB (create benchmark) | malloc/free per packet | Python objects |
| Config reconciliation | 94 us/15 sessions | Full restart | N/A |

#### Theoretical Throughput

At 64 ns/op full receive path, GoBFD can theoretically process **15.6 million packets per second** on a single core. At 6.7 ns/op full transmit path, theoretical TX throughput is **149 million packets per second**. These are micro-benchmark numbers — real-world throughput is lower due to system call overhead, network stack latency, and context switching.

---

### Hardware Reference

Vendor BFD implementations use dedicated hardware (ASIC/NPU) for packet processing. This is **fundamentally different** from software BFD and not directly comparable. We include this table as a reference point for understanding the BFD landscape.

| Platform | Max Sessions | Min Timer | Hardware Offload |
|----------|-------------:|----------:|:----------------:|
| Cisco ASR 9000 (NPU) | 8,000/LC at 50ms; 600/LC at 3.3ms | 3.3ms | NPU, 7 fixed intervals |
| Cisco 8000 (Silicon One) | 10,000 (VXLAN MH BFD) | Platform-dependent | Yes |
| Cisco NCS 5500 | 1,000/node | 15ms (up to 100 sessions) | OAMP engine |
| Juniper MX (inline BFD) | Contact support | 10ms (distributed), 100ms (centralized) | ASIC firmware |
| Nokia SR Linux | 1,152 (8-slot chassis) | Configurable | Hardware-based |
| Nokia 7750 SR | 250/IOM + CPM-np | 10ms (CPM-np) | CPM-np hardware |
| Arista EOS | 200/ASIC (HW accelerated) | 50ms | Default on supported platforms |

**Expert context** (Manav Bhatia, IETF BFD WG contributor): "BFD is hard when you start scaling. It becomes a LOT worse when you add security on top of it. Not only do you need to process packets at a high rate, you also need to compute SHA or MD5 digest for each one."

**Expert context** (Ivan Pepelnjak, ipSpace.net): "Aggressive BFD timers might trigger false positives. Most platforms implement BFD in software — if higher-priority processes use all CPU, BFD starves."

---

### Production Timer Targets

Reference table for BFD timer configurations in real deployments:

| Interval | Multiplier | Detection Time | Context | Achievability in Software |
|---------:|-----------:|---------------:|---------|---------------------------|
| 1000ms | 5 | 5000ms | Google Cloud Router default | Trivial |
| 300ms | 3 | 900ms | FRR default, NANOG "safe value", Juniper RE | Standard, reliable |
| 100ms | 3 | 300ms | SONiC target, Juniper distributed | Achievable in Go with CPU isolation |
| 50ms | 3 | 150ms | Cisco/Arista HW minimum | Requires careful optimization |
| 10ms | 3 | 30ms | aiobfd achieved with HW peer | Theoretically possible, needs RT tuning |

**GoBFD target**: 100ms intervals with stable detection time on commodity hardware, which places it between the software standard (300ms) and hardware minimum (10-50ms).

**Detection time formula** (RFC 5880): `DetectionTime = DetectMult * max(RequiredMinRxInterval, remote DesiredMinTxInterval)`. Up to 25% random jitter MUST be added to TX intervals (section 6.8.7). Additional 10% jitter when DetectMult = 1.

---

### Methodology and Fair Disclosure

**Measurement approach**:

- All GoBFD numbers are from Go micro-benchmarks (`go test -bench -benchmem -count=6`)
- Statistical analysis via `benchstat` (median, 95% confidence intervals)
- FRR and BIRD numbers are from public documentation, GitHub issue trackers, and vendor HLDs
- aiobfd numbers are from its README and test reports
- Hardware numbers are from vendor datasheets and configuration guides

**What we do NOT claim**:

- We have not run head-to-head benchmarks between GoBFD and FRR/BIRD/aiobfd
- Micro-benchmark throughput (ns/op) does not directly translate to system-level performance
- Different languages (Go vs C vs Python) have fundamentally different performance characteristics
- Memory measurements from benchmarks (B/op) are per-operation, not total daemon footprint

**Reproducibility**:

All GoBFD benchmarks can be reproduced:

```bash
# Requires only Podman — no local Go toolchain
make up
make benchmark-save BENCH_VERSION=v0.4.0
cat testdata/benchmarks/v0.4.0/meta.json    # commit, Go version, date
```

We encourage independent verification and welcome corrections to any claims in this document.
