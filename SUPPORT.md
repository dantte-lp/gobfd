# Support

## Request routing

| Request | Channel | Required evidence |
|:---|:---|:---|
| Reproducible defect | [GitHub issue](https://github.com/dantte-lp/gobfd/issues/new/choose) | Commit/version, Linux/kernel, deployment, enabled profile, minimal configuration and reproduction |
| Feature | [GitHub issue](https://github.com/dantte-lp/gobfd/issues/new/choose) | Operational use case, expected behavior and RFC/vendor source |
| Documentation correction | [GitHub issue](https://github.com/dantte-lp/gobfd/issues/new/choose) | Page/anchor, conflicting source and proposed correction |
| Vulnerability | [Private security advisory](https://github.com/dantte-lp/gobfd/security/advisories/new) | Follow [Security policy](SECURITY.md); do not use a public issue |

## Reproduction record

1. Record the exact commit and component version. Identify `dev`, stable or
   experimental scope; use the matching documentation.
2. Reduce the configuration to the failing session/profile. Remove credentials
   and private topology data before sharing it.
3. Record expected and observed state, terminal status and relevant structured
   logs. For timing defects, retain clock source, packet timestamps, CPU/memory
   limits and both endpoints' negotiated intervals.
4. Distinguish daemon state, client action and route state. Include a bounded
   reproduction rather than an unqualified failover-time claim.

## Support boundaries

| Area | Contract |
|:---|:---|
| Runtime | Linux sockets and interface/backend behavior; see [Deployment](docs/en/06-deployment.md) |
| Build | Repository-pinned compiler and Podman workflow; see [Development](docs/en/09-development.md) |
| Equipment | A specific NOS/platform/profile requires evidence; see [Vendor references](docs/en/reference/vendor-bfd.md) |
| API | Pre-1.0 compatibility and supported release-line contracts remain explicit |
| Service | No private support SLA or third-party network consulting is provided |

[Contributing](CONTRIBUTING.md) defines change review and checks.
[Maintainers](MAINTAINERS.md) defines decision ownership.
