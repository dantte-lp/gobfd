# Contributing to GoBFD

Thank you for your interest in contributing to GoBFD. This document explains
the process for contributing changes and the standards we follow.

## Getting Started

1. Fork the repository and clone it locally
2. Set up the development environment:
   ```bash
   podman-compose -f deployments/compose/compose.dev.yml up -d --build
   ```
3. Make your changes on a feature branch
4. Submit a pull request

## Development Workflow

### Build and Test

All Go commands run inside Podman containers. No local Go toolchain required.

```bash
make up            # Start dev container
make build         # Compile gobfd and gobfdctl
make test          # Run tests with -race -count=1
make lint          # Run golangci-lint v2
make all           # build + test + lint
```

### Interoperability Tests

If your change affects protocol behavior, run the interop test suite:

```bash
make interop       # Full cycle: build, start 4-peer topology, test, cleanup
```

### Protobuf Changes

If you modify `api/bfd/v1/bfd.proto`:

```bash
make proto-gen     # Regenerate Go code
make proto-lint    # Lint proto definitions
```

Never modify generated files in `pkg/bfdpb/` manually.

## Code Standards

### Go Conventions

- **Errors**: Always wrap with context using `%w`:
  `fmt.Errorf("send control packet to %s: %w", peer, err)`
- **Error handling**: Use `errors.Is`/`errors.As`, never string matching
- **Context**: First parameter, never stored in struct
- **Logging**: Only `log/slog` with structured fields. Never `fmt.Println` or `log`
- **Naming**: Avoid stutter (`package bfd; type Session` not `BFDSession`)
- **Imports**: stdlib, blank line, external, blank line, internal
- **Tests**: Table-driven, `t.Parallel()` where safe, always `-race`

### RFC Compliance

- FSM transitions **must** match RFC 5880 Section 6.8.6 exactly
- Timer intervals are in **microseconds** per RFC -- do not confuse with milliseconds
- Every protocol behavior change must reference the relevant RFC section

### Linting

The project uses golangci-lint v2 with a strict configuration (35+ linters).
Your code must pass:

```bash
make lint
```

### Security

- Never use the `unsafe` package -- GoBFD handles untrusted network input
- Never use `math/rand` -- use `math/rand/v2` or `crypto/rand`
- Run `govulncheck ./...` before adding new dependencies

## Pull Request Process

1. Open an issue first to discuss significant changes
2. Create a feature branch from `master`
3. Make focused, reviewable commits with descriptive messages
4. Ensure all checks pass: `make all`
5. Update documentation if your change affects user-facing behavior
6. Add or update tests for new functionality

### PR Checklist

- [ ] Tests added or updated
- [ ] `make all` passes (build + test + lint)
- [ ] `buf lint` passes (if proto files changed)
- [ ] Documentation updated (if applicable)
- [ ] Commit messages are descriptive

## Reporting Issues

- **Bugs**: Use the GitHub Issues tab with a clear description, steps to
  reproduce, expected vs actual behavior, and Go/OS version
- **Features**: Open a GitHub Issue to discuss before implementing
- **Security**: See [SECURITY.md](SECURITY.md) for responsible disclosure

## License

By contributing to GoBFD, you agree that your contributions will be licensed
under the [Apache License 2.0](LICENSE).
