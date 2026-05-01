# Support

## Support Channels

| Request type | Channel | Required information |
|---|---|---|
| Bug report | GitHub issue form | GoBFD version, OS, deployment mode, config excerpt, logs, reproduction steps |
| Feature request | GitHub issue form | Use case, expected behavior, protocol or operational reference |
| Documentation issue | GitHub issue form | Affected page, expected correction, source reference |
| Security vulnerability | GitHub Security Advisory | Affected versions, impact, reproduction, mitigation |

## Unsupported Channels

- Security vulnerabilities are not handled in public issues.
- Private support SLAs are not provided by this repository.
- Operational consulting for third-party networks is outside repository support.

## Compatibility Scope

| Area | Policy |
|---|---|
| Go version | See `go.mod` and CI configuration. |
| Operating system | Linux is the production target for raw sockets and interface monitoring. |
| Containers | Podman is the project validation environment. |
| Public API | Pre-1.0 API stability follows Semantic Versioning rule 4. |
| Releases | Supported version is the latest published release unless stated otherwise in `SECURITY.md`. |

## References

- Security policy: [SECURITY.md](./SECURITY.md)
- Contributing rules: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Development workflow: [docs/en/09-development.md](./docs/en/09-development.md)
