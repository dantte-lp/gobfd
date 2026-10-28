# Проверка совместимости

Доказательство совместимости содержит точную revision GoBFD, артефакт соседа, сценарий и capture. Определения тестов являются контрактами исходного кода; исторический pass не квалифицирует изменённого кандидата. [RFC coverage](08-rfc-compliance.md) определяет реализованные и неполные процедуры; [справочник vendor](reference/vendor-bfd.md) — внешнюю конфигурационную семантику.

## Выбор проверки

| Проверка | Команда | Граница |
|:---|:---|:---|
| Базовые peers | make interop | Race-enabled testcontainers: FRR, BIRD3, Holo, Thoro/bfd и crafted profiles |
| Связь с BGP | make interop-bgp | Race-enabled testcontainers: GoBGP с FRR/BIRD3/ExaBGP |
| Сценарии RFC | make interop-rfc | Race-enabled testcontainers: 7419, action path 9384, 9468 и Echo preview |
| Routing aggregate | make e2e-routing | Отдельные retained Compose suites; не все testcontainers profiles |
| RFC aggregate | make e2e-rfc | Legacy Compose/no-race runner; отдельно от interop-rfc |
| Containerlab | make interop-clab | Owned FRR-only dual-stack topology; приёмка в нативной среде отдельно |
| Vendor manifests | make e2e-vendor | Metadata/policy evidence; без выполнения vendor NOS |

Первые три команды — алиасы явных -testcontainers targets. [Makefile](../../Makefile) задаёт suite filters, environment и artifact directories. [Реестр E2E](../../test/e2e/targets.md) определяет различия выполнения и очистки.

## Подготовка и выполнение

1. Подготовьте [среду разработки](09-development.md), закреплённый провайдер Compose и нужный Podman API.
2. Прочитайте выбранную topology и правила выполнения. Проверьте имена проектов, фиксированные имена, subnets, limits и ownership до создания ресурсов.
3. Выберите новый evidence location и сохраните полный commit исходников.
4. Выполните нужный проверку полного цикла:

```bash title="Независимые interop gates"
make interop-testcontainers
make interop-bgp-testcontainers
make interop-rfc-testcontainers
```

5. Проверьте named results, skips, проверки полей пакетов и записи очистки. Сохраните исходные JSON и журналы, PCAP и SHA-256 sidecars.
6. Повторите затронутые profiles после изменений рабочего кода. Прежний запись для точного SHA не квалифицирует новую thread/transport/timer revision.

Base artifacts находятся в reports/e2e/interop-testcontainers; BGP/RFC используют соответствующие -bgp-/-rfc- names. GOBFD_REQUIRE_PODMAN=1 превращает отсутствие обязательного Podman в отказ вместо silent skip. Full-cycle owners сохраняют diagnostics до exact-owned cleanup.

## Базовая топология

```mermaid
flowchart LR
    gobfd["Проверяемый GoBFD"]:::srv -->|"BFD"| frr["FRR bfdd"]:::srv
    gobfd -->|"BFD"| bird["BIRD3"]:::srv
    gobfd -->|"BFD"| holo["Holo"]:::srv
    gobfd -->|"BFD"| thoro["Thoro/bfd"]:::srv
    capture["Capture в общем namespace"]:::mgt -.-> gobfd
    classDef srv fill:#e1bee7,stroke:#7b1fa2,color:#000
    classDef mgt fill:#d7ccc8,stroke:#5d4037,color:#000
```

Peer recipes находятся в [test/interop](../../test/interop/). Закреплённые исходники используют FRR 10.7.1, BIRD 3.4.0 с сохранённым fix, immutable Holo 0.9.0 и pinned Thoro source build. Получайте реальные идентификаторы сборки и образов из recipes и каждого run; названия версий недостаточно.

Base subnet — 172.20.0.0/24. Assertions проверяют handshake, AdminDown, negotiated timers, failure/recovery и captured Control fields. Authentication, padding и crafted procedures имеют отдельные profiles. Независимый Go peer использует repository codec; это не независимая реализация codec.

### Crafted peer

TestCraftedPeerTestcontainers запускает GoBFD, tshark в общем namespace и Go peer 172.20.0.40. Peer использует TTL 255, source port 49152, без auth, SIGUSR1 для смены phase.

| Фаза | Input | Требуемое evidence |
|:---|:---|:---|
| rx0/poll | Required RX zero и successive Polls | Один Final на Poll; без periodic TX минимум десять negotiated intervals |
| silent | Остановка peer TX | Down diag 1 и expiry timing; первый Down с Your Discriminator zero; минимум три slow-rate intervals |
| recover | Новый handshake | Up с intervals 300 мс |
| demand | D=1/Poll; reload 500 мс | Final, periodic suppression, local Poll/peer Final, converged receipt |
| demand-clear | D=0/Poll | Final, periodic TX, двенадцать intervals и mean/jitter checks |
| final-loss/poll/final-restore | Потеря Finals, crossing Poll, восстановление | Повтор active tuple 400 мс; candidate 800 мс после прежнего Final/exchange spacing; P/F не одновременно |

Silent bound — от DT−1 мс до DT+max(2 мс,DT/50) от last peer frame. Первый GoBFD Down имеет дополнительный допуск 50 мс. Multiplier 1 использует jitter 75–90% с mean 0.825T; остальные проверенные multipliers — 75–100% с mean 0.875T. Возобновлённая phase 500 мс проверяет mean в трёх standard errors.

Authenticated Async FRR/BIRD3 меняет multiplier 1→5→3 без local Poll. Remote Demand использует 5→3 с отдельными Poll/Final/receipt/quiet interval на изменение. Capture сохраняет port/length, auth type/key/sequence, discriminators и timers. Local receive Detection Time использует peer multiplier; peer Async и local Demand bounds подписаны отдельно. Crafted peer не имеет detection timer.

### Padding и multihop

Prepared padding проверяет authenticated 128→192→128 sizes, auth+padding rejection, enable/disable и combined-candidate rejection. Default keyed SHA1 и TestFourPeerMeticulousTopologyTestcontainers восстанавливают собственный immutable startup baseline. Capture проверяет DF, UDP payload, declared PDU length, zero padding, MAC domain и replay state между phases. SIGUSR2 проверяет текущий crafted Poll без сброса Demand.

TestCraftedMultihopTTLTestcontainers использует peer-multihop: UDP 4784, meticulous SHA1, local RX 3 с, TX 300 мс. Async/Demand меняют max-hops 1→5→1, затем отклоняют auth+hop-policy input. Последовательные probes по 200 мс повторяют accepted wire, дают fresh below-floor Poll и его повтор at-floor; один accepted RX и одна намеренная auth replay failure сохраняют счётчики переходов. Это локальная policy 255−N, не routed/cloud acceptance.

Final capture ограничивает decoded matching rows после display filtering. Capture read limit может скрыть последующие frames. Обязательные initial Down diag 0 и detection-expiry assertions нельзя silently skip. Pinned BIRD Linux high-port helper не обеспечивает RFC 5881 source range; retained padding capture содержит 938 packets с port 43990. Это указанное отклонение peer, а не GoBFD coverage.

### Сохранённые wire receipts

| Область | Точный source / receipt | Измеренный результат | Ограничение |
|:---|:---|:---|:---|
| Detect multiplier | d9e67064e51012c0d25a1a22dc9bd469600d8469; reports/detect-wire-20261024 | Обе topologies pass | Padding/TTL/final gate отдельно |
| Padding | b97729458a696aee21023ba147af51417ed9ff7b; reports/padding-wire-20261024 | Три записи выполнения, 675 проверенных файлов контрольных сумм | Baseline skips и final disposition отдельно |
| TTL | 491b2214994f07e017eb93ab67a29a95378c619e; reports/ttl-wire-20261024 | 162.94 с crafted, 81.98 с multihop; семь windows; 863 хешей исходников, 212 sidecars | Unified final/stable gate отдельно |
| Default FourPeer | e4e79a8eee8e7c38bfc78321bdc5e9030909c214; reports/final-wire-20261024 | 429.47 с; десять scenarios; expiry gap 0.901 с; один BIRD RFC skip | Повтор четырёх final profiles после thread/scale changes |

FourPeer receipt сохраняет шесть идентификаторы образов, 867 matching хешей исходников, 310 sidecars и четырнадцать dual-service SERVING probes. Runner 512 MiB фиксирует ноль max/OOM events и swap; отсутствие точных образов проверяется. Это dated observations, а не текущий run или полная квалификация выпуска.

## Связь с BGP

Topology 172.21.0.0/24 соединяет GoBFD/GoBGP с FRR native BFD+BGP, BIRD3 native BFD+BGP и GoBFD/ExaBGP. ExaBGP использует GoBFD bridge; это не native BFD peer.

| Peer path | Announced prefix |
|:---|:---|
| FRR | 10.20.0.0/24 |
| BIRD3 | 10.30.0.0/24 |
| ExaBGP bridge | 10.40.0.0/24 |

Сохраняйте BFD/BGP established, route presence, failure withdrawal и restored route/BGP отдельно. Shared namespaces выравнивают source addresses BFD/BGP. Текущий dev использует GoBGP v4 API; поддерживаемая v0.6 сохраняет v3 и не использует endpoint contract v4.

## Сценарии RFC

Topology 172.22.0.0/24 содержит interval-alignment FRR, GoBFD/GoBGP BGP path, unsolicited FRR и независимый Echo reflector.

| Сценарий | Assertion | Граница квалификации |
|:---|:---|:---|
| 7419 | Configured 80 мс → 100 мс на линии | Точный interval scenario |
| Action path с названием 9384 | BFD Down отключает BGP; Up включает | GoBGP DisablePeer выдаёт Cease/2; требуемый subcode 10 не реализован |
| 9468 | Unconfigured incoming session достигает Up и держится пять секунд | Не проверяет expiry/cleanup |
| Echo с названием 9747 | Reflector Up, paused failure, recovery | Echo preview; без полной RFC return validation/authentication |

Нормативная область задана в [RFC 5880](../rfc/rfc5880.txt), [RFC 5881](../rfc/rfc5881.txt) и [RFC matrix](08-rfc-compliance.md). Passing action path не меняет RFC implementation status.

## Сохранённый Compose workflow

Используйте для намеренно выбранного retained project; filters и failure handling отличаются от testcontainers gates.

```bash title="Retained base topology"
make interop-up
make interop-test
make interop-logs
```

После разрешения owned cleanup используйте interop-down. BGP/RFC имеют соответствующие -up/-test/-logs/-down. Capture helpers — interop-capture, interop-pcap, interop-pcap-summary. Routing aggregate base/BGP и legacy RFC aggregate не используют race detector. Fixed names/subnets могут конфликтовать между projects.

## Процедура FRR в Containerlab

Execution policy задают [profiles.json](../../test/e2e/vendor/profiles.json) и [vendorprofiles](../../test/internal/vendorprofiles/manifest.go). Arista cEOS, Nokia SR Linux, SONiC-VS, VyOS и deferred Cisco XRd остаются retained-unavailable. Cached images, historical config, registry metadata и vendor manuals не дают разрешения выполнения и приёмки. e2e-vendor сообщает policy без deployment.

1. Используйте native Linux amd64/arm64, Podman с нужным Unix API socket, GNU Make/coreutils/flock и точный containerlab 0.82.0 из controller pin.
2. Предзагрузите SDK/Debian references из [test_compiler_source.go](../../test/internal/clabbootstrap/test_compiler_source.go). Оставьте CLAB_LABDIR_BASE незаданным.
3. Выполните полный цикл или явные owned stages:

```bash title="Owned FRR Containerlab lifecycle"
make interop-clab-bootstrap ARGS=--dry-run
make interop-clab-up
make interop-clab-test
```

4. Проверьте package PASS и десять named PASS: dual-stack establishment, failure, negotiation, detection timing, packet format и all-available. Только SessionIndependence может skip при одном FRR.
5. Выполните разрешённый interop-clab-down и подтвердите отсутствие точных записанных IDs.

interop-clab выполняет полный FRR cycle. Prepare-only bootstrap сохраняет оба staged image IDs/receipt для --deploy --skip-build или owned cleanup; skip-build не доверяет latest. Help/dry-run/down также собирают новый controller. Старые vendor archive/ISO flags разбираются, но не разрешают preparation.

Cold compiler получает private source snapshot без engine socket и с limits. Payload компилируется отдельно с race detector в network-disabled SDK; Debian runtime получает exact hashed binaries и selected engine socket без compiler. Read-only socket mount сохраняет engine authority. Native host controller имеет timeout без эквивалентного hard memory cap. Payload limits — 2 CPU/2 GiB/без дополнительного swap/512 PIDs.

Проверки до/после compile валидируют schema-3 receipt, полные IDs и метки owner/run через CLI и однажды выбранный API client; каждый context — 30 секунд. Changed/malformed receipts, foreign fixed-name collisions и retained-NOS roster отклоняются. Host-global UID lock сериализует fixed lab между worktrees.

Containerlab соединяет запущенные ext-container applications без management network. IPv6 addresses ждут DAD до 30 секунд. Выбирайте sessions по точному tuple и typed local/remote state; другая session или diagnostic со словом Down недостаточны.

Cleanup валидирует identities до мутации, удаляет exact application image IDs без force/pruning и receipt последним. Failed destroy сохраняет discovery/receipt для retry. Historical schema-1/2 служат только cleanup inputs. Shared SDK/runtime images сохраняются. Test-result JSON остаётся в test/interop-clab/gobfd/.runtime.

FRR assertions используют nominal/negotiated 300 мс и detection 900 мс; timing test допускает пять секунд. Packet-format требует двадцать полных dual-stack frames. Native FRR lifecycle/compiler-engine qualification и все пять NOS qualifications остаются незавершёнными; сохраните записи фактического выполнения до заявления приёмки.

## Диагностика

| Симптом | Проверка | Действие |
|:---|:---|:---|
| Нет required runtime | Provider/socket и GOBFD_REQUIRE_PODMAN | Восстановите runtime; не принимайте skip |
| Fixed-name/subnet collision | Project manifest и exact live ownership | Выберите isolated project; сохраните чужие ресурсы |
| Session остаётся Down | Local/peer tuple, capabilities, TTL/auth/ports | Исправьте input; сохраните packets |
| Пустой capture | Namespace/filter, start time, matching rows | Повторите bounded capture в assertion window |
| RFC-name pass без wire guarantee | Assertions и RFC matrix | Укажите точную action-path scope |
| Cleanup не завершён | Receipt IDs, labels, API selection | Повторите owning down; сохраните recovery state |
| Vendor profile unavailable | Policy и source/license evidence | Сохраните unavailable до отдельного допуска |

[Разработка](09-development.md), [E2E harness](../../test/e2e/README.md), [vendor reference](reference/vendor-bfd.md), [измерения runtime](14-performance-analysis.md).
