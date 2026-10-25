# Эксплуатационные инструкции для интеграций

![Linux](https://img.shields.io/badge/Linux-Hosts-FCC624?style=for-the-badge&logo=linux&logoColor=black)
![Kubernetes](https://img.shields.io/badge/Kubernetes-DaemonSet-326CE5?style=for-the-badge&logo=kubernetes&logoColor=white)
![BGP](https://img.shields.io/badge/BGP-Fast_Failover-00ADD8?style=for-the-badge)
![Overlay](https://img.shields.io/badge/Overlay-Lab_Only-ea4335?style=for-the-badge)

> Инструкции по развёртыванию и проверке GoBFD в Linux, Kubernetes, BGP и оверлейных сетях.

---

## База проверки

- RFC 5880: в асинхронном режиме время обнаружения равно удалённому Detect Mult,
  умноженному на согласованный интервал приёма.
- RFC 5881: single-hop BFD использует UDP-порт назначения 3784, порт источника
  из динамического диапазона и TTL/Hop Limit 255.
- RFC 7130: Micro-BFD создаёт независимые сессии для каждого участника LAG и
  использует UDP-порт 6784.
- RFC 8971: VXLAN BFD использует Management VNI, внутренний UDP-порт назначения
  3784 и внутренний TTL/Hop Limit 255.
- RFC 9521: Geneve BFD работает для туннелей Geneve типа точка-точка с внутренним
  TTL/Hop Limit 255 и требованиями к управлению частотой трафика.
- Текущие пути приёма VXLAN/Geneve в пространстве пользователя являются
  небезопасными неполными предварительными реализациями: валидация внутреннего
  пакета и полная привязка идентичности туннельной сессии не прошли квалификацию
  для рабочей эксплуатации.
- Kubernetes pods в сетевом пространстве узла требуют `dnsPolicy: ClusterFirstWithHostNet`
  при `hostNetwork: true`.
- Примеры для оборудования требуют публичной документации производителя.

## Профили развёртывания

| Профиль | Сценарий | Артефакты |
|---|---|---|
| Переключение BGP | Linux-маршрутизатор или лабораторная пара FRR/GoBGP | `deployments/integrations/bgp-fast-failover/` |
| Демон Kubernetes | Один экземпляр GoBFD на узел в его сетевом пространстве | `deployments/integrations/kubernetes/` |
| Наблюдаемость | Оповещения Prometheus и панель Grafana | `deployments/integrations/observability/` |
| Оверлейная лаборатория | Только изолированная проверка VXLAN/Geneve | [ADR-0003](./adr/0003-linux-advanced-bfd-applicability.md) |

## Демон Kubernetes в сетевом пространстве узла

Используйте этот вариант, когда BFD-пиры достижимы из сетевого пространства узла.
Универсальный DaemonSet не задаёт топологию площадки, частные адреса или
допущения для конкретного производителя.

Обязательные свойства:

- `hostNetwork: true`
- `dnsPolicy: ClusterFirstWithHostNet`
- только узлы Linux
- `NET_RAW` для пакетных сокетов BFD
- `NET_ADMIN` для включённых опций сокетов и операций с интерфейсами
- проверка готовности через запуск `gobfdctl` и Unix-сокет; проверка работоспособности через TCP-порт метрик

До применения манифестов поместите `gobfd:local` и `gobgp:local` в хранилище
образов каждого подходящего узла: манифест задаёт `imagePullPolicy: Never`.
Пример runner импортирует образы только в локальное хранилище k3s и сохраняет
несовпадение меток pod и устаревшие проверки (`gobfd-qj0.8.2.8.7.1.4.1`). Это
отдельные ограничения runner, а не приведённых ниже команд применения манифестов.

Развёртывание:

```bash
kubectl apply -f deployments/integrations/kubernetes/manifests/
kubectl -n gobfd rollout status daemonset/gobfd --timeout=120s
kubectl -n gobfd get pods -o wide
```

Проверка сетевого режима узла и проверок состояния:

```bash
kubectl -n gobfd describe daemonset/gobfd
kubectl -n gobfd get endpoints gobfd
kubectl -n gobfd logs -l app.kubernetes.io/name=gobfd -c gobfd --tail=100
```

Эксплуатационные замечания:

- Для статических окружений фиксируйте BFD-сессии в `configmap-gobfd.yml`.
- Для динамических окружений создавайте сессии через `gobfdctl` или контроллер
  после обнаружения узлов и пиров.
- Сохраняйте Unix-сокет по умолчанию. Удалённое управление через TCP требует
  mTLS; доверенная сеть не разрешает plaintext-listener вне loopback.

Импорт образов Kubernetes и квалификация на работающем кластере не завершены.
Эти манифесты и проверки являются примерами, а не принятым развёртыванием
кластера. Готовность подтверждает доступ к управлению, но не установление
сессий или сходимость маршрутов.

## Проверка быстрого переключения BGP

Запуск универсальной FRR/GoBGP-интеграции:

```bash
make int-bgp-failover-up
make int-bgp-failover-logs
```

Полная документация примера:
[`deployments/integrations/bgp-fast-failover/README.md`](../../deployments/integrations/bgp-fast-failover/README.md).

Исходные проверки:

```bash
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd -c 5
```

До имитации отказа подтвердите BFD Up и наличие маршрута `10.20.0.0/24`.
Команды ниже показывают наблюдения; отдельная проверка с утверждениями об
отзыве и восстановлении запускается через `make int-bgp-failover`.

Имитация отказа:

```bash
podman pause frr-bgp-failover
sleep 2
podman exec gobgp-bgp-failover gobgp global rib
podman unpause frr-bgp-failover
sleep 5
podman exec gobgp-bgp-failover gobgp global rib
```

Ожидаемый результат:

- BFD переходит из Up в Down до истечения обычного таймера удержания BGP.
- GoBGP отключает затронутого пира.
- Маршрут `10.20.0.0/24` возвращается в GoBGP RIB после BFD Up; пересылка трафика проверяется отдельно.

## Проверка оповещений Prometheus

Используйте изолированный хост: лаборатория публикует порты хоста 9090/3000 и
использует стандартные учётные данные Grafana; см. [Интеграции](11-integrations.md#наблюдаемость).
Ниже приведена локальная процедура; эта правка документации не фиксирует новый
прогон. Требуются `curl`, `jq`, `timeout` и оболочка.

Запустите стек наблюдаемости:

```bash
make int-observability-up
```

Проверьте цели сбора метрик и правила:

```bash
curl -fsS http://localhost:9090/api/v1/targets
curl -fsS http://localhost:9090/api/v1/rules
```

До проверки подтвердите работоспособность цели GoBFD, состояние FRR-сессии Up
и наличие как минимум двух успешных сборов метрик. Интервалы сбора и вычисления
правил равны 15 секундам. Вызовите переход в Down и ожидайте конкретное оповещение не
дольше 60 секунд; одного HTTP 200 недостаточно:

```bash
(
  set -eu
  trap 'podman start frr-observability' EXIT
  podman stop frr-observability
  timeout 60s sh -c '
    until curl --max-time 3 -fsS http://localhost:9090/api/v1/alerts |
      jq -e '\''any(.data.alerts[]; .labels.alertname == "BFDSessionDownTransition" and .state == "firing")'\''
    do
      sleep 2
    done
  '
)
```

Код 0 подтверждает наблюдённое оповещение в состоянии `firing`; timeout означает
неуспешную проверку. Обработчик `trap` запускает пира заново при обоих исходах.
После запуска снова проверьте BFD Up и работоспособность целей, затем отсутствие
оповещения после минутного окна `increase` и следующего вычисления правил.
Поля ответа определяет Prometheus
[alerts API](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts).

Назначение оповещений:

| Оповещение | Значение |
|---|---|
| `BFDNoActiveSessions` | Нет активных объектов сессий; поставляемое правило пропускает exporter, ещё не создавший сессий (`gobfd-qj0.8.2.8.7.2.8`). |
| `BFDSessionDownTransition` | Сессия перешла из Up в Down. |
| `BFDSessionFlapping` | Сессия изменила состояние более 3 раз за 5 минут. |
| `BFDAuthFailures` | Проверка аутентификации по RFC 5880 завершилась ошибкой. |
| `BFDPacketDrops` | Выросло число отброшенных пакетов допущенных пиров; результаты разбора и демультиплексирования до сессии учитывает отдельный журнал доставки. |

## Проверка пакетов

Проверяйте поведение на проводе по захваченным пакетам, а не только по логам:

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
| UDP-порт назначения | 3784 |
| UDP-порт источника | 49152-65535 |
| TTL/Hop Limit | 255 |
| Версия BFD | 1 |
| Последовательность состояний | Down -> Init -> Up |

## Публичные заметки по оборудованию

Примеры GoBFD не зависят от производителя. Фрагменты конфигурации оборудования
требуют первичной документации производителя.

Опубликованные дополнительные заметки:

- [Проверка BFD в Arista EOS](../../deployments/integrations/bgp-fast-failover/vendor-notes/arista-eos.md)

Используйте первичные ссылки заметки для точного релиза и профиля оборудования.
Дополнительный пример команды производителя не подтверждает аппаратную
обработку, применение состояния Micro-BFD к участнику LAG или непрерывность
пересылки в GoBFD.

## Оставшиеся ограничения рабочей эксплуатации

| Ограничение | Статус |
|---|---|
| Создание Echo и Micro-BFD через API/CLI | Предварительная реализация; квалификация транспорта, владельца и RFC остаётся отдельной |
| Отдельные API/CLI создания VXLAN/Geneve | Не реализованы; используются явно включаемые декларативные профили |
| Оверлейные backend ядра/OVS/OVN/Cilium/Calico/NSX помимо `userspace-udp` | Будущая работа |
| OCI SBOM/provenance и контрольные суммы релиза | Workflow реализован; точный кандидат проверяется до публикации; шаг подписи не настроен |

---

*Сверено с исходниками `dev`: 2026-10-24.*
