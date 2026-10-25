# Actual Smallstep renewal qualification

This test-only profile exercises the production renewal adapter against a real
external `step-ca` process and a race-enabled GoBFD daemon. It does not operate
an existing CA, enroll identities or qualify a production CA deployment.
The default Go test suite skips this external-provider test unless its binary
environment is supplied.

## Provider source and dependencies

| Input | Pin |
|---|---|
| Upstream | `smallstep/certificates` v0.30.2, commit `6e8ec61405239cf3f37b2bbf260a587b7d2e4e31` |
| Source archive | [step-ca_0.30.2.tar.gz](https://github.com/smallstep/certificates/releases/download/v0.30.2/step-ca_0.30.2.tar.gz) |
| Archive SHA-256 | `944b205d5ba89f393cbdc09d68ab7ce485f5b44f44c28025d30508af956c1cba` |
| License | Apache-2.0; preserve the upstream source and license |
| Compiler | Repository-pinned Go 1.27.3 Debian trixie image |
| Variant | `0.30.2+gobfd.qual.1`, build time `qualification` |
| Dependency manifest SHA-256 | `e232e75630b914801bcfdc29105b03e58483994bd1ac3c0e6f95000073d2031f` |
| Dependency checksums SHA-256 | `206b5bfe79ca92b88bb56229eb5a49699b392a05bd77f3da6a326d12aa4b4384` |

The archive has a flat root. Extract it into a new private directory without
stripping a path component. Replace only that copy's `go.mod` and `go.sum`
with `qualified.mod` and `qualified.sum`. All upstream Go sources, plugins
and command packages remain intact. The profile updates stale transitive
dependencies; it does not alter GoBFD's runtime or tool manifests.

Inside the pinned compiler container, with the private source copy as the
working directory and a writable output directory at `/output`, build:

```bash
GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-p=1 go build \
  -mod=readonly -trimpath -buildvcs=false \
  -ldflags '-X main.Version=0.30.2+gobfd.qual.1 -X main.BuildTime=qualification' \
  -o /output/step-ca-qualified ./cmd/step-ca
go version -m /output/step-ca-qualified
/output/step-ca-qualified --version
```

Scan the full `./cmd/step-ca` package from the same source copy using the
repository-pinned govulncheck. Keep the profile as ordinary `go.mod`/`go.sum`;
do not export a scanner `GOFLAGS=-modfile=...`. Compare the binary's linked
module versions with the scanned graph and record source, profile, compiler
and binary hashes. Build inside bounded disposable resources, with writable
caches separate from shared seeds; release the build container before runtime.

The 2026-10-25 scan covers 153 modules including the main module, with zero
reachable and zero affected imported-package findings. Three module-only
advisories remain: `GO-2026-5932`, `GO-2022-0646`, `GO-2022-0635`. Their affected
OpenPGP and AWS S3 Crypto SDK packages are not imported by this full command.
This is a bounded disposition for a disposable test provider, not a production
CA security decision. Repeat the scan for changed inputs or advisory data.

## Runtime qualification

From the GoBFD checkout inside the pinned compiler environment, build:

```bash
export CGO_ENABLED=1
go build -race -buildvcs=false -o /output/gobfd ./cmd/gobfd
go test -race -c -o /output/stepca.test ./cmd/gobfd
go build -o /output/test2json cmd/test2json
```

Run only the prebuilt
executables in a new network-none container with loopback, `NET_RAW`, no engine
socket or host networking, bounded CPU/memory/PIDs and a private writable
`/tmp`. The fixture refuses a namespace containing interfaces other than `lo`.
Provide these absolute paths inside that runtime:

| Environment variable | Meaning |
|---|---|
| `GOBFD_TEST_STEP_CA_BINARY` | Qualified full provider executable |
| `GOBFD_TEST_DAEMON_BINARY` | Race-enabled daemon executable |
| `GOBFD_TEST_STEP_CA_REPORT_DIR` | Private writable evidence directory |

```bash
/qualification/test2json -t /qualification/stepca.test \
  -test.v -test.timeout=90s \
  -test.run='^TestActualSmallstepRenewalManagementLifecycle$'
```

Qualification requires five passing tests with no failures or skips and both
children joined with exit code zero. The fixture creates its own short-lived
root/intermediate and locally signed existing identities carrying the upstream
JWK provisioner extension. It tests renewal, not initial issuance. No provisioner
secret, bearer token or existing PKI is used.

The gate checks server/operator/monitor renewal with preserved keys and advancing
serial/expiry; a new management handshake loads the renewed server certificate,
the operator retains its principal-scoped session claim and the monitor remains
read-only. Disabled renewal, passive revocation, expired current credentials
and server-only EKU are rejected without changing certificate or key files.
Expiry is rejected by the adapter before an outbound request. Passive revocation
tests renewal denial, not CRL/OCSP enforcement or immediate API access removal.

Only sanitized hashes, process status and observations enter `actual-stepca.json`.
Private keys, PKI/database files and raw process logs stay in the fixture's
temporary directory. Join children before removing the runtime container and
its temporary data; retain the bounded evidence. The 2026-10-25 local run used
2 CPUs, 2 GiB hard memory, no swap and completed all five tests in 5.29 seconds.
This profile does not qualify other provisioners, cloud KMS, enrollment,
scheduling, reconnect policy or trust-root rollover.

See [the fixture](../../cmd/gobfd/stepca_actual_linux_test.go),
[ADR-0005](../../docs/en/adr/0005-smallstep-management-certificates.md),
[deployment](../../docs/en/06-deployment.md#management-certificate-renewal)
and [Russian deployment guidance](../../docs/ru/06-deployment.md#обновление-сертификатов-управления).
Beads `gobfd-qj0.8.2.3.7.4` owns qualification; separate lifecycle work remains
in `gobfd-qj0.8.2.3.7.5`.
