# ADR-0005: External Smallstep CA renewal for management certificates

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-23 |

## Context

The management server consumes PEM certificates on each new handshake.
Existing clients load credentials when their transport is constructed.
Smallstep `step-ca` supports mTLS-authenticated `POST /renew`
for an existing certificate. Certificate issuance does not establish GoBFD
authorization: a TLS URI must never acquire kernel-authenticated Unix UID
privileges or share its owner namespace.

The initial scope is existing-credential renewal. No CA address, root,
provisioner or issuance policy is supplied by the maintainer; the adapter
therefore acts only on explicit operator input and never changes a live CA.

## Decision

Use an external step-ca and a small standard-library-only `internal/stepca`
adapter. `gobfdctl certificate renew --ca-url <https-origin> --ca-root <file>
--cert <file> --key <file>` renews one existing PEM identity. The command does
not connect to the GoBFD RPC endpoint. A successful request replaces only the
public certificate file; the private key is never changed or printed.

The protocol reference is `smallstep/certificates` v0.30.2, checked against its
release notes and exact API sources. This is a protocol reference, not a new
Go dependency or a qualified provider image. Native HTTP avoids embedding the
CA dependency graph; spawning the step CLI is not used.

The trust boundary requires:

- Explicit HTTPS origin and explicit root certificates; no system-root,
  proxy, redirect, insecure-TLS or bearer-token fallback. Private CA addresses
  are intentional operator configuration, never an RPC-controlled fetch URL.
- Caller cancellation and a finite HTTP timeout, bounded response and chain
  sizes, strict JSON and certificate-only PEM. Go 1.27 `encoding/json/v2` is
  adopted only at this new protocol boundary to reject duplicate names and
  malformed UTF-8; existing JSON v1 consumers remain unchanged.
- A verified, currently usable non-CA leaf with the same public key,
  subject, exact SAN and key-usage/EKU profile. Changed certificates must
  advance expiry; an identical validated chain is an unchanged result.
  Verify the chain separately for every declared EKU; unrestricted leaves
  must remain usable for both server and client TLS authentication.
  Redundant response fields must agree. Response TLS options cannot alter
  the daemon security policy.
- Protected regular private-key input, rooted no-follow file access and
  atomic same-directory certificate replacement preserving mode and UID/GID.
  A trusted credential directory and cooperative renewal lock are required;
  directories and inputs belong to root or the calling UID, including when
  the command runs as root. Directory identity and all inputs are rechecked.
  identity/hash rechecks detect changed inputs but do not promise compare-and-swap
  against privileged non-cooperative writers. Linked inputs fail closed.
  The bootstrap key is parsed once and rechecked before commit, never rewritten.
  Temporary-file cleanup and close errors are checked.
- Trusted transport provenance for management authorization. Raw configured
  URI/DNS role identities remain compatible; TLS URI, TLS DNS and Unix UID
  owner namespaces are disjoint.

## Consequences

The four existing binaries, YAML and protobuf contracts remain unchanged.
Existing PEM mode and external renewal tooling remain usable. Existing daemon
TLS connections retain their negotiated identity until reconnect; a new
handshake loads the replaced certificate. Existing clients need transport
recreation or restart to load a new client certificate. Repeated runs request renewal rather
than creating another BFD session or key. An identical replacement is a no-op;
errors preserve the previous certificate before the atomic commit. Errors
after rename report a committed result with uncertain durability; they never
promise rollback of an already visible replacement.

Initial enrollment, ACME/provisioner integration, unattended scheduling,
connection recycling and trust-root rollover require separate lifecycle
decisions. No automatic CA discovery or root download is introduced.
Local TLS fixtures are not actual step-ca qualification. The plan and remaining
acceptance criteria live in Beads `gobfd-qj0.8.2.3.7`; transport provenance is
`gobfd-qj0.8.2.3.5`.

## References

- [Renewal CLI](../../../cmd/gobfdctl/commands/certificate.go)
- [Bounded CA transport](../../../internal/stepca/client.go)
- [Certificate identity and EKU verification](../../../internal/stepca/certificate.go)
- [Rooted credential access and atomic replacement](../../../internal/stepca/files_linux.go)
- [step-ca renewal](https://smallstep.com/docs/step-ca/renewal/)
- [Renew API source](https://github.com/smallstep/certificates/blob/v0.30.2/api/renew.go)
- [Response schema](https://github.com/smallstep/certificates/blob/v0.30.2/api/sign.go)
- [Release notes](https://github.com/smallstep/certificates/releases/tag/v0.30.2)
- [Go specification](https://go.dev/ref/spec)
- [Go 1.27 release notes](https://go.dev/doc/go1.27)
