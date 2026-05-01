# Production runbooks интеграций

> Универсальные runbooks для развёртывания и проверки GoBFD в независимых
> Linux, Kubernetes, BGP и overlay-network окружениях.

---

## База проверки

Эти runbooks основаны на:

- RFC 5880: в Asynchronous mode detection time равен удалённому Detect Mult,
  умноженному на согласованный receive interval.
- RFC 5881: single-hop BFD использует UDP destination port 3784, source port из
  dynamic range и TTL/Hop Limit 255.
- RFC 7130: Micro-BFD создаёт независимые сессии на каждый LAG member и
  использует UDP port 6784.
- RFC 8971: VXLAN BFD использует Management VNI, inner UDP destination port
  3784 и inner TTL/Hop Limit 255.
- RFC 9521: Geneve BFD работает для point-to-point Geneve tunnels с inner
  TTL/Hop Limit 255 и требованиями к управлению частотой трафика.
- MCP validation: Kubernetes `hostNetwork: true` использует
  `dnsPolicy: ClusterFirstWithHostNet`; Arista EOS имеет публичные команды BFD
  для BGP и RFC 7130/VXLAN verification, но примеры GoBFD по умолчанию
  остаются vendor-neutral.

## Профили развёртывания

| Профиль | Сценарий | Артефакты |
|---|---|---|
| BGP failover | Linux routing host или лабораторная пара FRR/GoBGP | `deployments/integrations/bgp-fast-failover/` |
| Kubernetes daemon | Один экземпляр GoBFD на узел через host networking | `deployments/integrations/kubernetes/` |
| Наблюдаемость | Prometheus alerts и Grafana dashboard | `deployments/integrations/observability/` |
| Overlay endpoint | Выделенный VXLAN/Geneve management endpoint | `docs/04-linux-advanced-bfd-applicability.md` |

## Kubernetes host-network daemon

Используйте этот паттерн, когда BFD-пиры достижимы из network namespace узла.
DaemonSet намеренно универсален: он не кодирует site topology, private
addresses или vendor-specific assumptions.

Обязательные свойства:

- `hostNetwork: true`
- `dnsPolicy: ClusterFirstWithHostNet`
- только Linux nodes
- `NET_RAW` для BFD packet sockets
- `NET_ADMIN` для socket options и будущей link-state integration
- TCP readiness/liveness probes для gRPC и metrics ports

Развёртывание:

```bash
kubectl apply -f deployments/integrations/kubernetes/manifests/
kubectl -n gobfd rollout status daemonset/gobfd
kubectl -n gobfd get pods -o wide
```

Проверка host networking и probes:

```bash
kubectl -n gobfd describe daemonset/gobfd
kubectl -n gobfd get endpoints gobfd
kubectl -n gobfd logs -l app.kubernetes.io/name=gobfd -c gobfd --tail=100
```

Операционные заметки:

- Для статических окружений фиксируйте BFD-сессии в `configmap-gobfd.yml`.
- Для динамических окружений создавайте сессии через `gobfdctl` или controller
  после node/peer discovery.
- Держите GoBFD API на localhost или доверенной management-сети, если mTLS не
  настроен.

## BGP fast-failover drill

Запуск универсальной FRR/GoBGP-интеграции:

```bash
make int-bgp-failover-up
make int-bgp-failover-logs
```

Полная документация примера:
[`deployments/integrations/bgp-fast-failover/README.md`](../../deployments/integrations/bgp-fast-failover/README.md).

Baseline checks:

```bash
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd -c 5
```

Failure drill:

```bash
podman pause frr-bgp-failover
sleep 2
podman exec gobgp-bgp-failover gobgp global rib
podman unpause frr-bgp-failover
sleep 5
podman exec gobgp-bgp-failover gobgp global rib
```

Ожидаемый результат:

- BFD переходит из Up в Down до истечения обычного BGP hold timer.
- GoBGP отключает или отзывает затронутого пира согласно выбранной стратегии.
- Доступность маршрута восстанавливается после возврата BFD-сессии в Up.

## Проверка Prometheus alerts

Запустите observability stack:

```bash
make int-observability-up
```

Проверьте targets и rules:

```bash
curl -fsS http://localhost:9090/api/v1/targets
curl -fsS http://localhost:9090/api/v1/rules
```

Вызовите down transition:

```bash
podman stop frr-observability
sleep 5
curl -fsS http://localhost:9090/api/v1/alerts
podman start frr-observability
```

Назначение alerts:

| Alert | Значение |
|---|---|
| `BFDNoActiveSessions` | У GoBFD нет активных сконфигурированных сессий. |
| `BFDSessionDownTransition` | Сессия перешла из Up в Down. |
| `BFDSessionFlapping` | Сессия изменила состояние более 3 раз за 5 минут. |
| `BFDAuthFailures` | Проверка authentication по RFC 5880 завершилась ошибкой. |
| `BFDPacketDrops` | Были packet validation, demux или buffer drops. |

## Packet verification

Проверяйте видимое RFC-поведение через packet captures, а не только по логам:

```bash
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd \
  -T fields \
  -e frame.time_relative \
  -e ip.src \
  -e ip.dst \
  -e udp.srcport \
  -e udp.dstport \
  -e ip.ttl \
  -e bfd.sta \
  -e bfd.detect_time_multiplier \
  -E header=y \
  -E separator=,
```

Ожидаемые single-hop значения:

| Поле | Ожидаемо |
|---|---|
| UDP destination port | 3784 |
| UDP source port | 49152-65535 |
| TTL/Hop Limit | 255 |
| BFD version | 1 |
| State progression | Down -> Init -> Up |

## Public vendor notes

Примеры GoBFD должны оставаться vendor-neutral. Vendor snippets можно добавлять
как optional public interop notes, если они проверены по первичной vendor
документации или MCP.

Опубликованные optional notes:

- [Arista EOS BFD verification note](../../deployments/integrations/bgp-fast-failover/vendor-notes/arista-eos.md)

Публичное поведение Arista EOS, проверенное через Arista MCP:

- `neighbor bfd` включает BFD для BGP neighbor или peer group.
- Если BFD-сессия перешла Down, связанная BGP-сессия также переводится Down.
- `show bfd peers` отображает BFD state и session type, включая RFC7130 и Vxlan
  там, где они поддерживаются.
- `show port-channel <N> detail` может показать members inactive из-за RFC 7130
  micro-session state.

## Открытые production gaps

| Gap | Плановый sprint |
|---|---|
| Dedicated API/CLI create flows для Echo, Micro-BFD, VXLAN и Geneve | S5b |
| OVS/NetworkManager backend для Micro-BFD enforcement | S7.1d |
| VXLAN/Geneve backend model для kernel/OVS dataplane coexistence | S7.2 |
| pkg.go.dev polish и v0.5.0 release dry-run | S8 |

---

*Последнее обновление: 2026-05-01*
