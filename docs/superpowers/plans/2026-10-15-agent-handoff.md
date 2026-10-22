# GoBFD handoff in Beads

Executable plans, priorities, dependencies, acceptance criteria, blockers and
resume steps live exclusively in Beads. This file is a stable entrypoint,
not an execution plan or a second task tracker.

```bash
bd show gobfd-qj0.8
bd show gobfd-qj0.8.2.1.6
bd show gobfd-qj0.8.2.6
bd ready --priority 0
```

Read each issue's current criteria, dependencies and notes before acting.
Source and runtime evidence paths are recorded in those notes; a historical
receipt does not qualify later code. The local-only delivery direction remains
recorded under `bd memories delivery-local-only-20261022`.

The full former Markdown plans and handoff were imported into the historical
`design` archive of `gobfd-qj0.6` from commit
`2797c363e48a84aebbf62c07502aefdbc478f195`, with byte-for-byte readback.
Their old checkboxes, commands and status snapshots are historical only.
