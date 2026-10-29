# Contributing to GoBFD

Contributions follow [GOVERNANCE.md](GOVERNANCE.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) and the repository contract in [AGENTS.md](AGENTS.md). Report vulnerabilities through [SECURITY.md](SECURITY.md); route other requests through [SUPPORT.md](SUPPORT.md). Review ownership is listed in [MAINTAINERS.md](MAINTAINERS.md) and [.github/CODEOWNERS](.github/CODEOWNERS).

## Prepare a contribution

1. Discuss significant behavior or compatibility changes in an issue. Record acceptance criteria and review ownership.
2. Start features from `dev` and target `dev`. Start maintenance fixes from their `release/vMAJOR.MINOR` line on `fix/vMAJOR.MINOR-*`; target that supported line.
3. Configure the pinned Podman Compose provider and a unique checkout project using [Development](docs/en/09-development.md).
4. Read affected callers, tests, configuration and public contracts before editing.
5. Keep the change focused; include tests and complete English/Russian documentation for changed behavior. Follow the [documentation standard](docs/en/reference/documentation-standard.md).

## Validate

```bash title="Routine validation"
make up
make test
make verify
```

`verify` covers build, race tests, gopls, lint, documentation, protobuf lint and controlled vulnerability audit. It does not execute every tagged test, interoperability topology or release gate. Select additional checks from [Development](docs/en/09-development.md) and [Interoperability](docs/en/05-interop.md).

For protobuf changes, run `make proto-gen`, `make proto-lint` and the applicable breaking check. Regenerate `pkg/bfdpb/`; do not edit generated files. Measure allocation and binary-size boundaries for hot-path or compiler changes using the [benchmark guide](docs/en/12-benchmarks.md).

Build/test/lint normally use the pinned container. Some retained bootstrap, integration and report entry points invoke Go directly; consult the execution-boundary table before using them. Repository tooling remains Go-owned; do not add Python environments or Bash scripts. The upstream ExaBGP runtime exception is limited to its pinned Debian peer image.

## Code requirements

| Area | Requirement |
|:---|:---|
| Errors | Context plus `%w`; `errors.Is`/`errors.As` |
| Concurrency | Context-bound lifetime; sender closes channels |
| Logging | Structured `log/slog` |
| Tests | Table-driven where useful; safe parallelism; race detector |
| Protocol | Exact RFC section, microsecond units, preserved validation |
| Memory | Named benchmark evidence for zero-allocation claims |
| Security | No handwritten unsafe; preserve socket error handling and trust boundaries |
| Dependencies | Runtime and isolated tools modules; fresh controlled security audit |
| Containers | Digest-pinned Debian trixie |

## Submit a pull request

1. Use descriptive Conventional Commit subjects; validate with `make lint-commit MSG='fix(bfd): reject invalid input'`.
2. State the concrete behavior, executed checks and remaining limitations in the request template.
3. Include exact revision and evidence paths for protocol, wire or runtime claims. Name skipped tests and required acceptance gates.
4. Update both changelogs for user-visible changes according to the [release guide](docs/en/10-changelog.md).
5. Request applicable owner review. Preserve the source branch and follow repository merge policy.

Published fixes remain on their supported line until separately delivered to `master` and, when applicable, `dev`. A merged development task is not stable release delivery. Tags, drafts and publication follow separate maintainer authorization.

Contributions are licensed under [Apache License 2.0](LICENSE). Russian procedures are available in [Разработка](docs/ru/09-development.md).
