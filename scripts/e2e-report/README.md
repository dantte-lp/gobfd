# E2E report presentation contract

This directory contains no shared renderer executable. Current evidence producers are [e2erunner](../../test/internal/e2erunner/) and the selected [E2E packages](../../test/e2e/). Their JSON, logs and Markdown are usable without an HTML index.

## Input boundary

The producer root is reports/e2e/TARGET/RUN_ID. Run IDs differ by producer; consumers must not assume a timestamp-only name. [Artifact layout](../../test/e2e/README.md#artifact-layout) defines current files.

| Input | Interpretation |
|:---|:---|
| summary.md / environment.json | Producer status and environment identity |
| go-test.json / go-test.log | Actual package/test outcomes and diagnostics |
| containers.json / containers.log | Bounded runtime state/log evidence |
| packets.csv / packets.pcapng | Target-dependent decoded/captured evidence |
| Target-specific JSON | Typed scenario receipts and explicit skips |

Missing evidence remains missing. Overlay CSV is synthetic; vendor manifest checks and Linux ownership fixtures do not promise PCAP. Failed startup can leave diagnostics only. No consumer may promote those inputs to a passing wire or vendor result.

## Presentation requirements

A future offline index.html may present target/run identity, exit status, duration, environment, parsed test outcomes, packet evidence, container state, artifact links and collapsible logs. The presentation must retain explicit unknown/skipped/missing states.

Use one local stylesheet and shared renderer, keyboard-readable links and sufficient contrast. Do not fetch remote scripts, fonts, CDNs or other assets. Link raw evidence so the rendered summary remains auditable.

This reference defines no runnable renderer command and does not change producer output or claim completed report generation.

[English development](../../docs/en/09-development.md), [русское руководство](../../docs/ru/09-development.md).
