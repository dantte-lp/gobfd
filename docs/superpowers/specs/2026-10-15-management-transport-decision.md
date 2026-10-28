# Management API transport and authentication decision

Date: 2026-10-15. GitHub:
[#87](https://github.com/dantte-lp/gobfd/issues/87). Status: decided by
research; implements the approved
[v1 design](./2026-08-18-gobfd-v1-production-contract-design.md) sections
"Owners" (lines 131-138), "Management security" (lines 353-370) and the
security acceptance tests (lines 589-600).

## Problem

The ConnectRPC management API (`bfd.v1` BFD, Echo and Micro-BFD services plus
`grpc.health.v1`) can create and delete sessions. It has no authentication
and no TLS. Since `0314466` it listens on `127.0.0.1:50052` by default, but:

- with `hostNetwork: true` (the Kubernetes DaemonSet) or any other process on
  the host, loopback TCP is reachable by every local user and host-network
  pod, and the server cannot tell who called;
- a non-loopback `grpc.addr` only produces a startup warning; integration and
  test configurations bind wildcard addresses (`:50051`, `:50052`, `:50053`)
  inside containers;
- API owners are a fixed `compatibility` ID
  (`internal/bfd/session_identity.go:133`), so one client can release
  another client's claims.

## Facts this decision rests on

Codebase (inventory at `dev@ef4c5d2`):

- All three clients dial `http://` URLs through `connecthttp.NewTransport` and
  have no TLS or Unix-socket options (`cmd/gobfdctl/commands/root.go:38`,
  `cmd/gobfd-haproxy-agent/main.go:86`, `cmd/gobfd-exabgp-bridge/main.go:76`).
- Every companion in Compose and Kubernetes shares the daemon's network
  namespace; no cross-namespace API consumer exists. The only TCP-dependent
  consumers are the Kubernetes `tcpSocket` readiness probe and Service
  (`deployments/integrations/kubernetes/manifests/daemonset.yml:50`,
  `service.yml:22`).
- The server sets `Protocols` to HTTP/1.1 plus unencrypted HTTP/2 only
  (`cmd/gobfd/shutdown.go:192-194`).
- systemd runs the daemon as `User=gobfd`/`Group=gobfd` without
  `RuntimeDirectory=` (`deployments/systemd/gobfd.service:16-17`).
- `SessionOwner{Source, ID}` already supports distinct owner IDs.

External (sources S1-S20 at the end; facts marked "not measured" are
inferences from source code):

- connect-go v2 keeps the request context of `net/http`; a value added in
  `http.Server.ConnContext` reaches interceptors and handlers, and
  `connecthttp.ServerInfoForContext(ctx).TLS()` exposes the verified client
  certificate chain (`connectrpc.com/connect/v2@v2.0.0`, `connecthttp.go`).
- `http.Server.ConnContext` receives the raw accepted connection: a
  `*net.UnixConn` for a Unix listener, on which Linux `SO_PEERCRED`
  (`unix.GetsockoptUcred`) returns the peer UID/GID captured at `connect(2)`,
  mapped into the daemon's user namespace (unix(7); Linux
  `net/core/sock.c` `cred_to_ucred`).
- A non-nil `http.Server.Protocols` is the exact protocol set, and
  `ServeTLS` derives ALPN from it; without HTTP/2 a TLS listener advertises
  only `http/1.1`, and grpc-go enforces ALPN by default. h2c and TLS cannot
  share one port; they need separate listeners. That a grpc-go client reaches
  this server over `unix://` or TLS follows from the code but is not measured;
  phase 1 adds the wire tests.
- `x509.Verify` does no revocation checking; certificate and client-CA
  rotation needs `tls.Config.GetConfigForClient`.
- Prior art: FRR (vtysh over Unix sockets, group `frrvty`), BIRD (`bird.ctl`),
  Holo (`holod.sock` mode 0660, group `holoadm`, peer user from the kernel;
  TCP requires TLS), Docker (`docker.sock` + group; TCP needs `--tlsverify`),
  containerd (`containerd.sock`, TCP with `tcp_tls_*`), GoBGP v4 (TCP with
  optional mTLS `RequireAndVerifyClientCert`, `unix://` listeners). The common
  pattern is a Unix socket by default with file-permission access control,
  and TLS for any TCP exposure.

## Options

| Option | Local identity | Remote | Cost | Verdict |
|---|---|---|---|---|
| A. Loopback TCP only (today) | none | none | none | Rejected: every host user and host-network pod is anonymous and can mutate |
| B. Unix socket + `SO_PEERCRED` by default, mTLS for TCP | kernel peer UID | mTLS SAN/SPIFFE | new listener, client dialers, TLS config | **Chosen**; matches the v1 design and FRR/Holo/Docker practice |
| C. Bearer token on TCP | shared secret | token over TLS | secret files, rotation, argv leaks; grpc-go refuses per-RPC credentials on insecure transports except with `credentials/local` | Rejected: weaker identity than B, more secret handling |
| D. mTLS for local clients too | certificate | mTLS | certificates for every local tool | Rejected for local use: heavy for `gobfdctl`; kept for remote |
| E. systemd socket activation | as B | — | `sd_listen_fds` handling, no stdlib support | Deferred: `RuntimeDirectory=` plus a daemon-created socket is enough |

## Decision

1. **Unix socket, default.** The daemon creates `/run/gobfd/gobfd.sock`
   with mode 0660, owner `gobfd`, group `gobfd-admin`. Access control is the
   socket file mode and a dedicated admin group, as with FRR `frrvty`, Holo
   `holoadm` and Docker `docker`. The group must not be `gobfd`:
   `/etc/gobfd` is `0750 root:gobfd` (`deployments/systemd/gobfd.tmpfiles`)
   and holds BFD authentication secrets, so the daemon group would also grant
   read access to them. Consequences:
   - `gobfd.sysusers` adds `g gobfd-admin -`;
   - the unit adds `SupplementaryGroups=gobfd-admin` so the daemon can chown
     the socket, and `RuntimeDirectory=gobfd` with
     `RuntimeDirectoryMode=0755`, because `RuntimeDirectory=` is owned by
     `User=`/`Group=` and cannot carry the admin group; the socket mode is the
     gate;
   - the socket path is configurable (`grpc.socket.path`, `mode`, `group`)
     for containers, where a shared `emptyDir` carries it.

   The kernel peer UID from `SO_PEERCRED`, read in `ConnContext`, is the
   principal (`uid:<n>`); PID is never used for authorization.
2. **TCP is optional.** `grpc.addr` empty disables it.
   - Loopback TCP is a compatibility listener: reads are allowed;
     mutations require `grpc.unsafe_loopback_mutations: true`, which is
     logged at startup and reported by the `gobfd.management` health check
     as `NOT_SERVING` while enabled.
   - Non-loopback TCP requires `grpc.tls` (`cert_file`, `key_file`,
     `client_ca_file`); the daemon refuses to start with plaintext
     non-loopback TCP. TLS listeners serve HTTP/1.1 and HTTP/2 via ALPN;
     certificates and the client CA are re-read through
     `GetConfigForClient`. The principal is the certificate URI SAN
     (SPIFFE) or else the first DNS SAN; roles (read-only or mutating) per
     identity come in phase 3.
3. **Clients** accept `unix:///path`, `http://host:port` and
   `https://host:port` (with `--tls-ca`, `--tls-cert`, `--tls-key` or the
   matching YAML/environment keys). A bare `host:port` keeps meaning
   `http://`.
4. **No bearer tokens, no socket activation** in v1 (options C and E).

## Phasing

| Phase | Content | Breaking | Bead |
|---|---|---|---|
| 1a | Unix socket listener (`grpc.socket.*`), `SO_PEERCRED` principal in the request context, `unix://` dialing in `gobfdctl` and both companions; tests over a real Unix listener (principal reaches the handler, grpc-go `unix://` wire test, stale-socket handling) | no | `gobfd-330.1` |
| 1b | mTLS TCP listener with ALPN `h2`/`http/1.1`, reloadable certificates, TLS options in all clients | no | `gobfd-330.2` |
| 2 | Defaults: socket on, `grpc.addr` empty; `gobfd-admin` group in sysusers and the unit; reject plaintext non-loopback TCP; loopback mutations behind `grpc.unsafe_loopback_mutations`; systemd `RuntimeDirectory=`; Kubernetes exec probe over the socket; integration and test configurations move to loopback or the socket; migration notes | yes | `gobfd-330.3` |
| 3 | Owner IDs `api:<principal>` in Manager claims, cross-principal release rejected and audited, mTLS identity roles | yes (owner semantics) | `gobfd-330.4` |

Phase 1 adds capability without changing any default, so phase 2 can switch
defaults with every client already able to follow.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| TLS listener without HTTP/2 breaks gRPC clients (ALPN) | The listener's own `tls.Config` offers `NextProtos {h2, http/1.1}`; measured in `gobfd-330.2`: without `h2` grpc-go fails with "tls: no application protocol", while `Protocols.SetHTTP2` is not needed because the server does not use `ServeTLS` |
| Stale socket file after a crash blocks startup | Remove an existing socket file only if it is a socket and no server answers on it |
| Socket created with a loose mode before chmod | Create it inside the 0750 runtime directory, then chmod 0660 before serving |
| Peer UID differs across user namespaces (containers) | Principals are UIDs as seen by the daemon; document the Kubernetes pod-level `runAsUser`/`fsGroup` requirement for companions |
| Listener wrappers hide `*net.UnixConn` from `ConnContext` | Read credentials before any wrapping; a test asserts the principal reaches the handler |
| Certificate revocation not checked by Go | Short-lived certificates (cert-manager) and reload; optional CRL check deferred |
| Breaking defaults surprise upgrades | Phase 2 keeps configured listeners, adds migration notes and startup messages that name the opt-in; CHANGELOG marks it breaking |
| Kubernetes `tcpSocket` probe breaks without TCP | Exec probe with `gobfdctl --addr unix:///run/gobfd/gobfd.sock` health check in phase 2 |
| Browsers cannot reach a Unix socket (gRPC-Web) | Not a supported use; remote access is mTLS TCP |

## Acceptance

Taken from the v1 design security tests: non-loopback plaintext management
is rejected before serving; Unix peer UID and mTLS identities produce stable,
isolated principals across reconnects; unauthorized mutations and
cross-principal releases fail closed and are audited; the loopback
compatibility listener is visible in status; fresh defaults expose only the
Unix socket and loopback metrics; secrets never appear in logs, status or
errors.

## Sources

Read on 2026-10-15.

- S1 `connectrpc.com/connect/v2@v2.0.0` `docs/v2-guide.md:369-516`,
  `docs/v2-migration.md:713` (transport split, `CallInfo`, `connecthttp`).
- S2 same module `connecthttp/connecthttp.go:120-164` (`ServerInfoForContext`,
  `TLS()`), `handler.go:101-108`, `protocol_grpc.go:139-151` (request
  context).
- S3 Go 1.27.2 `net/http/server.go:2063` (h2c only without TLS),
  `:3164-3182` (`ConnContext`, `Protocols`), `:3647-3654` (`ServeTLS`,
  ALPN); `net/unixsock_posix.go:207-215` (socket file removal).
- S4 `google.golang.org/grpc@v1.84.0` `credentials/tls.go:141-151`,
  `internal/envconfig/envconfig.go:48-53` (ALPN enforcement),
  `credentials/local/local.go:67-82`, `internal/resolver/unix/unix.go`.
- S5 connect-go conformance reference server (`SetUnencryptedHTTP2` plus
  `SetHTTP2`), via context7 `/connectrpc/connect-go`.
- S6 `golang.org/x/sys@v0.49.0/unix` `GetsockoptUcred`, `Ucred`.
- S7 unix(7), https://man7.org/linux/man-pages/man7/unix.7.html
  (`SO_PEERCRED`, socket file permissions, abstract sockets).
- S8 Linux v6.12 `net/core/sock.c:1655-1666` (`cred_to_ucred`, namespace
  mapping).
- S9 systemd 257 systemd.exec(5) (`RuntimeDirectory=`, `UMask=`),
  systemd.socket(5), sd_listen_fds(3).
- S10 `github.com/osrg/gobgp/v4@v4.10.0` `cmd/gobgpd/main.go:66-76,195-217`,
  `pkg/server/grpc_server.go:149-151`.
- S11 FRR vtysh and installation docs, https://docs.frrouting.org/en/latest/vtysh.html
- S12 BIRD user guide, https://bird.network.cz/?get_doc&v=20&f=bird-4.html
- S13 Holo `holo-daemon/man/holod.8:94-128` at `5dd9432`.
- S14 Docker daemon and access protection docs,
  https://docs.docker.com/engine/security/protect-access/
- S15 containerd `docs/man/containerd-config.toml.5.md`.
- S16 Kubernetes volumes (`emptyDir`) and user namespaces docs.
- S17 cert-manager csi-driver docs, https://cert-manager.io/docs/usage/csi-driver/
- S18 Go 1.27.2 `crypto/tls/common.go` (`GetCertificate`,
  `GetConfigForClient`, `VerifyConnection`), `crypto/x509/verify.go:547`
  (no revocation checking).
- S19 GoBFD inventory at `dev@ef4c5d2` (paths cited inline).
- S20 approved v1 design, lines 131-138, 353-370, 555-600.
