# Actual Smallstep renewal qualification

This test profile runs the production renewal adapter against a full external step-ca process and a race-enabled GoBFD daemon. It tests renewal of existing identities in a disposable namespace. It does not operate an existing CA, enroll identities or qualify production CA deployment.

The default test suite skips the external-provider fixture unless binary paths are supplied. The fixture is [stepca_actual_linux_test.go](../../cmd/gobfd/stepca_actual_linux_test.go); [ADR-0005](../../docs/en/adr/0005-smallstep-management-certificates.md) defines management-certificate ownership.

## Provider inputs

| Input | Identity |
|:---|:---|
| Upstream source | smallstep/certificates v0.30.2; 6e8ec61405239cf3f37b2bbf260a587b7d2e4e31 |
| Archive | [step-ca_0.30.2.tar.gz](https://github.com/smallstep/certificates/releases/download/v0.30.2/step-ca_0.30.2.tar.gz) |
| Archive SHA-256 | 944b205d5ba89f393cbdc09d68ab7ce485f5b44f44c28025d30508af956c1cba |
| Source license | Apache-2.0; preserve source and license |
| Compiler | Repository-pinned Go 1.27.3 Debian trixie image |
| Variant metadata | 0.30.2+gobfd.qual.2; build time qualification |
| qualified.mod SHA-256 | 487f2e84af68c1ef6676612898fa748b97babc04c60626691c12aef5c20b3713 |
| qualified.sum SHA-256 | 625c21810d2fe9537b3594179b51887b138132283d85a6a7e6c441d734591c05 |

[qualified.mod](qualified.mod) and [qualified.sum](qualified.sum) belong only to this provider copy. They do not change runtime/tools module selections.

## Build and scan the provider

1. Verify the archive identity and extract its flat root into a new private directory; do not strip a leading component.
2. Preserve all upstream Go source, plugins, command packages and license files.
3. Replace only that copy's go.mod/go.sum with the qualification manifests.
4. Build inside the pinned compiler with writable caches separate from shared seeds and bounded CPU/memory/PIDs. Mount the private output directory at /output:

```sh
GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-p=1 go build \
  -mod=readonly -trimpath -buildvcs=false \
  -ldflags '-X main.Version=0.30.2+gobfd.qual.2 -X main.BuildTime=qualification' \
  -o /output/step-ca-qualified ./cmd/step-ca
go version -m /output/step-ca-qualified
/output/step-ca-qualified --version
```

5. Scan the complete ./cmd/step-ca with the repository-pinned govulncheck from the same source copy. Keep ordinary go.mod/go.sum; do not export a scanner modfile override.
6. Compare scanned and linked module sets. Retain source/profile/compiler/binary hashes and full scan disposition.
7. Release the build container before starting the separate runtime.

A provider pin alone is not current security evidence. Repeat build/scan/link validation after any source, manifest, toolchain or advisory-data change.

## Compile the fixture

Build from the GoBFD checkout inside the pinned compiler environment:

```sh
export CGO_ENABLED=1
go build -race -buildvcs=false -o /output/gobfd ./cmd/gobfd
go test -race -c -o /output/stepca.test ./cmd/gobfd
go build -o /output/test2json cmd/test2json
```

Run prebuilt executables in a new network-none container with loopback, NET_RAW, no engine socket/host networking, bounded resources and a private writable /tmp. The fixture refuses any network interface other than lo.

| Runtime variable | Absolute path |
|:---|:---|
| GOBFD_TEST_STEP_CA_BINARY | Qualified provider executable |
| GOBFD_TEST_DAEMON_BINARY | Race-enabled daemon executable |
| GOBFD_TEST_STEP_CA_REPORT_DIR | Private writable evidence directory |

```sh
/qualification/test2json -t /qualification/stepca.test \
  -test.v -test.timeout=90s \
  -test.run='^TestActualSmallstepRenewalManagementLifecycle$'
```

Require five passing tests, no fail/skip and both child processes joined with exit zero. Retain actual-stepca.json and bounded test output; verify cleanup of only the owned runtime.

## Assertions and exclusions

The fixture creates a short-lived root/intermediate and locally signed existing identities with the upstream JWK provisioner extension. It uses no provisioner secret, bearer token or existing PKI.

| Case | Required observation |
|:---|:---|
| Server/operator/monitor renewal | Key preserved; serial changes; expiry extends |
| Server TLS replacement | New management handshake loads renewed certificate |
| Operator identity | Principal-scoped session claim survives rotation |
| Monitor identity | Access remains read-only |
| Disabled renewal, passive revocation, expired credential, server-only EKU | Renewal rejected without certificate/key file changes |

The adapter rejects expired credentials before outbound renewal. Passive revocation tests renewal denial; it does not establish CRL/OCSP enforcement or immediate API-access removal. Other provisioners, cloud KMS, enrollment, scheduling, reconnect policy and trust-root rollover remain outside this profile.

Private keys, PKI/database files and raw process logs remain in fixture temporary storage. Only sanitized hashes, process statuses and observations enter the retained JSON. Join children before authorized removal of the owned container and temporary data.

## Retained qualification

The dated 2026-10-28 report records 154 SBOM entries: 152 linked dependencies, main module and standard library. Linked/scanned dependency sets match; reachable and affected-imported-package findings are zero. The graph selects x/net v0.62.0 for the retained x/net remediation.

Module-only GO-2026-5932, GO-2022-0646 and GO-2022-0635 remain; their OpenPGP/AWS S3 Crypto SDK packages are absent from the full command's imports. This is a bounded disposable-provider disposition, not production CA risk acceptance.

The retained runtime uses 2 CPU, 2 GiB hard memory, no swap and five passes in 5.33 seconds. These historical values are not a fresh run on a later candidate. Revalidate the full profile after changed inputs.

[Deployment](../../docs/en/06-deployment.md), [развёртывание](../../docs/ru/06-deployment.md), [dependency risk](../../docs/en/reference/dependency-risk.md).
