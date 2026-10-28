# Развёртывание

GoBFD работает на Linux с UDP, raw sockets и учётом интерфейсов. Репозиторий определяет архивы, DEB/RPM и OCI; наличие определения не доказывает публикацию или приёмку конкретного кандидата. Источники: [release config](../../.goreleaser.yml), [Containerfile](../../deployments/docker/Containerfile), [systemd unit](../../deployments/systemd/gobfd.service).

## Артефакты и установка

| Артефакт | Состав | Граница |
| :--- | :--- | :--- |
| Linux amd64/arm64 архив | Четыре бинарника, YAML-пример, unit | Учётные записи и установка принадлежат оператору |
| DEB/RPM | Бинарники, noreplace config, unit, sysusers/tmpfiles, lifecycle | При обновлении сохраняется конфигурация |
| Debian trixie OCI | Бинарники, release labels/SBOM config | Проверить точный digest и evidence кандидата |
| Development Compose | Source mount и build tools | Среда разработки без демона/file watcher |

Прикладные бинарники: `gobfd`, `gobfdctl`, `gobfd-haproxy-agent`, `gobfd-exabgp-bridge`. Oracle Linux 10 исключён из v1 OCI qualification; generic RPM сохраняется. Alpine, bookworm и исполняемый scratch runtime вне контракта. Версии принадлежат [текущим manifests](../../go.mod) и digest-pinned Containerfiles, а не непроверенному тегу latest.

1. Выбрать принятый release/candidate; записать SHA, checksum и архитектуру.
2. Проверить опубликованный checksum/provenance; сохранить результат.
3. Установить выбранный пакет/архив; сохранить бинарник и конфигурацию для rollback.
4. Проверить `gobfd -version` и `gobfdctl version` до запуска.
5. Сверить [конфигурацию](03-configuration.md); задать peer/local/interface и защитить auth.

URL загрузки или изменяемый image tag не заменяет проверку кандидата.

## systemd

Unit запускает `/usr/local/bin/gobfd -config /etc/gobfd/gobfd.yml` как `gobfd:gobfd` с дополнительной группой `gobfd-admin`. Применяются `Type=notify`, watchdog 30 секунд и restart on-failure через 5 секунд. `ExecReload` отправляет SIGHUP без подтверждения сходимости.

| Контроль | Значение | Следствие |
| :--- | :--- | :--- |
| Runtime directory | `/run/gobfd`, `0755` | Жизненный цикл сокета принадлежит service |
| UMask | `0077` | Ограничение новых файлов |
| Capabilities | `CAP_NET_RAW CAP_NET_ADMIN` | Сетевые привилегии без полного privileged |
| Файловая система | `ProtectSystem=strict`, config read-only, private tmp | Запись в runtime directory/private tmp |
| Ядро/процессы | Защищены tunables/modules/cgroups, ограничена видимость процессов | Actuator требует отдельной проверки |
| Address families | Unix, IPv4, IPv6, netlink | Управление, UDP/TCP, interface monitor |
| Descriptors | `16384` | Бюджет `2*sessions+256`; проверить реальную нагрузку |

Источники: [unit](../../deployments/systemd/gobfd.service), [accounts](../../deployments/systemd/gobfd.sysusers), [directories](../../deployments/systemd/gobfd.tmpfiles).

Операторы входят в `gobfd-admin`, а не в читающую secrets группу демона. После установки проверить effective unit и права сокета. Запуск, перечитывание и rollback определены в [регламенте](16-production-runbooks.md).

Опциональный [kernel-bond drop-in](../../deployments/systemd/gobfd-kernel-bond.conf) открывает запись одного sysfs slaves и добавляет `CAP_DAC_OVERRIDE`, расширяя чтение root-owned файлов. Bond должен существовать при запуске. Native-приёмка требует реальной записи/чтения состояния участника и проверки отказа/восстановления пересылки на хосте с bonding module. Dry-run и наличие drop-in не доказывают enforcement.

## Podman Compose

Поддерживается `podman compose` с checksum-pinned Go provider. Python `podman-compose` и прямой вызов provider вне контракта. Версия/checksum принадлежат bootstrap, а не старой инструкции.

```sh title="compose-provider.sh"
go run ./test/cmd/toolbootstrap compose --install-dir "$HOME/.local/bin"
export PODMAN_COMPOSE_PROVIDER="$HOME/.local/bin/docker-compose"
export PODMAN_COMPOSE_WARNING_LOGS=false
export DOCKER_BUILDKIT=0
podman compose version
podman compose -f deployments/compose/compose.yml config
```

Bootstrap выполняется на хосте с repository toolchain. Требуется цепочка реальных каталогов с безопасным ownership; проверка предшествует atomic publication. `DOCKER_BUILDKIT=0` сохраняет поддерживаемый Podman API path. Источники: [bootstrap](../../test/cmd/toolbootstrap), [Makefile](../../Makefile).

```sh title="development.sh"
make up
make build
make test-run RUN=TestFSMTransition PKG=./internal/bfd
```

[Production Compose specimen](../../deployments/compose/compose.yml) использует host networking, read-only config, `NET_RAW`/`NET_ADMIN` и loopback Prometheus/Grafana. Это исходный образец, не site receipt. Проверить image identities, доступность socket group, host port conflicts и права до запуска. Existing fixed names/shared volumes требуют проверки владельца.

Host networking не разделяет filesystem paths. Compose не монтирует `/run/gobfd` на host; читать default socket внутри контейнера:

```sh
podman exec gobfd gobfdctl session list
```

Доступ host CLI требует отдельно проверенного mount каталога сокета/прав либо явно настроенного TCP management transport.

## Kubernetes

[DaemonSet](../../deployments/integrations/kubernetes/manifests/daemonset.yml) выбирает Linux, `hostNetwork: true`, `ClusterFirstWithHostNet`, runtime-default seccomp и `NET_RAW`/`NET_ADMIN`. Оба контейнера используют local images с `imagePullPolicy: Never`; доставить их в runtime каждого eligible node до apply. Host port conflicts и открытые host-network metrics требуют явной host-network политики.

Readiness выполняет `gobfdctl session list` через сокет; liveness проверяет metrics TCP. Они не доказывают BFD Up или сходимость BGP. GoBGP API/probes используют loopback. Приёмка требует image import на каждом выбранном node, правильного выбора labels/probes в runner, rollout и отказа/восстановления при заявленных CPU/memory limits фактического кластера. Образец не содержит live-cluster receipt.

После согласования кластера/context и импорта обоих local images на все выбранные nodes:

```sh title="kubernetes-rollout.sh"
kubectl apply -f deployments/integrations/kubernetes/manifests/
kubectl -n gobfd rollout status daemonset/gobfd --timeout=120s
kubectl -n gobfd get daemonset gobfd
kubectl -n gobfd get pods -l app.kubernetes.io/name=gobfd -o wide
kubectl -n gobfd describe daemonset/gobfd
kubectl -n gobfd logs -l app.kubernetes.io/name=gobfd -c gobfd --tail=100
```

Требовать совпадения desired/current/ready и одного нужного pod на каждом eligible node. Проверить restart/OOM и container status каждого pod. Для каждого найденного pod подставить точное имя:

```sh
kubectl -n gobfd exec POD_NAME -c gobfd -- /bin/gobfdctl session list
```

Требовать заданного peer state и отдельной route/forwarding проверки каждого node. Сохранить прежние ConfigMaps/DaemonSet и image identities для rollback; не удалять общие namespace/resources вместо rollback. Источник команд: [процедура Kubernetes DaemonSet](https://kubernetes.io/docs/tasks/manage-daemon/create-daemon-set/).

## Ресурсы и приёмка

Задать `GOMEMLIMIT` по измеренным RSS, packet rate, числу сессий и limit контейнера. Сохранить default `GOGC` без измеренного обоснования изменения. Heap target не ограничивает RSS: остаются descriptors, kernel buffers, goroutines, traces и peer processes.

Перед production-приёмкой сохранить:

1. Точные source/artifact identities, конфигурацию и rollback copy.
2. Проверки management permissions и доступности listeners без аутентификации.
3. Negotiated peer state/timers и пакетные проверки.
4. Consumer route/backend response и независимый forwarding probe при отказе/восстановлении.
5. Resource/OOM/restart behavior при заявленной нагрузке.
6. [Integration evidence](11-integrations.md), [security gates](15-security.md) и ограничения preview.

Локальный тест, принятая конфигурация и HTTP health доказывают только собственные измеренные границы.
