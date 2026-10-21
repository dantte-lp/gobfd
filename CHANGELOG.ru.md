# Журнал изменений

Все заметные изменения в этом проекте документируются в данном файле.

Формат основан на [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/),
проект придерживается [Семантического версионирования](https://semver.org/lang/ru/spec/v2.0.0.html).

## [Не выпущено]

### Добавлено

- `cictl package-build` собирает DEB и RPM по пути релиза (GoReleaser
  v2.18.2, затем финализация жизненного цикла RPM) с версией 0.0.0, а
  `cictl package-lifecycle` устанавливает их в контейнеры с systemd, собранные
  из закреплённых базовых образов релиза Debian 13 и Oracle Linux 10. Тест
  проверяет усиленный юнит: запуск, состояние песочницы, удержание дольше
  `WatchdogSec=`, reload, перезапуск после `SIGKILL`, остановку при мёртвом
  пире, отсутствие отказов песочницы и удаление пакета.

- YAML `multihop_max_hops` (только `multi_hop`, 1-254, по умолчанию 1) задаёт
  нижнюю границу принимаемого TTL/Hop Limit 255 - N (1/2/5 hops: 254/253/250)
  в каждой сессии после демультиплексирования. Это политика GoBFD по
  ненормативному RFC 5082 Appendix A; RFC 5883 не задаёт правила для входящего
  TTL. API proto не изменён, API-сессии используют значение по умолчанию. Отказы
  учитываются как `demux`/`ttl_rejected`. Квалификация на реальных путях из 2 и
  5 hops остаётся открытой.

- Ограниченная обработка Linux interface events: затронутые sources становятся
  pending без освобождения старых claims и автоматически повторяют применение
  в сохранённом generation. Потеря событий запрашивает полную сверку интерфейсов.
  Non-Linux stub не обеспечивает автоматическое восстановление; отсутствие startup
  listener/overlay capabilities остаётся ограничением.

- Live-квалификация timer reload FRR/BIRD в testcontainers-топологии BASE:
  `300→100→500→300 ms` и RX0 с generation receipts, окнами пакетов Poll/Final,
  границами jitter RFC 5880, неизменной идентичностью сессии в ненулевых фазах
  и восстановлением baseline. Selective-Final loss, crossed Poll и remote Demand
  остаются открытыми.

- Целевой размер PDU RFC 9764 в public API, статусе сессий и
  `gobfdctl session add --padded-pdu-size`. Ноль в API/CLI отключает padding; ноль в
  YAML-сессии наследует global default. Общие границы проверяются до сужения
  API-значения; padded API senders включают IPv4 DF. RFC 9764 остаётся частичным;
  path MTU и peer-originated padding сохраняют отдельную область квалификации.

- Control delivery outcomes разделяют demux, admission, owner commit и
  terminal loss. Повторное использование заголовка не меняет queued packet;
  stop закрывает admission и один раз учитывает abandoned RX/control без
  подтверждения abandoned AdminDown. Фиксированный process-lifetime ledger
  доступен в ListSessions и `gobfd_bfd_delivery_outcomes_total`; session
  drop/auth counters передаются в Get/List/Control Watch и Prometheus.
  Kernel receive overflow остаётся отдельной задачей; изменения относятся к
  development scope.

- Echo RX возвращает типизированные full/closed outcomes и учитывает owner
  commit/stop abandonment. Отдельные фиксированные строки ledger охватывают
  Echo read/parser/TTL, все raw notification writers, stale/accepted/abandoned
  dispatch, queued/gapped originals каждого Watch recipient и legacy delivery.
  Private unsolicited cleanup переживает raw overflow через coalesced failure
  hint и один lazy parked worker с переиспользуемым timer на retained physical
  entry. Сохранены attempt, AdminDown, shared-claim и identity fences. Kernel
  overflow и RFC/stable qualification остаются отдельными задачами.

- Потеря public Control/Echo raw notification инвалидирует live revisioned
  subscribers через bounded coalesced GAP и authoritative resnapshot без
  следующего owner edge или reconnect. Старый queued status не заменяет
  resnapshot в общем Watch client и GoBGP. Recovery markers не добавляют
  original-delivery counters или synthetic Micro actions. Private unsolicited
  cleanup и legacy best-effort delivery сохраняют прежние границы; full RFC,
  stable и soak qualification остаются отдельными задачами.

- Control-session Watch публикует регистрацию до запуска владельца, включая
  пассивные сессии, остающиеся Down, и сохраняет полный API-статус по значению
  для additions, observations и removals. Конфигурация, таймеры и счётчики
  измеряются на границе публикации; `observation_version` покрывает причинный
  кортеж. Zero/false и отсутствующие timestamps заменяют прежний статус клиента.

- Echo Watch передаёт initial и post-sync Down rows, frozen owner status и
  синхронное версионированное удаление для API/config removal и rollback.
  Control и Echo используют общий observation clock и opaque owner identities;
  mixed snapshot имеет одну границу реестров/revision. Typed Echo RPC и
  Control List/Get/GoBGP snapshots сохраняют прежнюю область. Для mixed-сессий
  одного peer нужны явные companion selectors. RFC 9747 сохраняет preview.

- Остановка передачи RFC 9468 в owner loop после natural failure или истечения
  независимого окна установления с сохранением сроков discriminator/auth.
  Final и local AdminDown сохраняют процедуры RFC 5880; configured/API claims
  сохраняют обычный passive режим. Cleanup проверяет идентификатор локальной
  попытки и не удаляет более позднюю попытку. Режим остаётся небезопасным preview.

- Wildcard single-hop listeners RFC 9468 для IPv4/IPv6 с bind к интерфейсу
  принимают unsolicited-пакеты без настроенной сессии на destination address.
  Настроенные listeners сохраняют peer lookup; неизвестный ingress index
  разрешается только через startup snapshot включённых интерфейсов. Пустое
  имя включённого интерфейса отклоняется. Режим остаётся небезопасным preview.

- Negotiated AdminDown drain: owner commit, подтверждение успешной записи,
  retry через существующий TX timer и монотонно продлеваемое окно уведомления.
  Короткий бюджет возвращает `DrainIncompleteError`; shutdown использует общий BFD
  бюджет 10s вместо фиксированного sleep 2s. Owning UDP lease прерывает blocked
  write и ожидает callback до owner completion и освобождения source port.
  Локальный SIGTERM capture подтвердил повторные AdminDown записи в FRR и BIRD,
  окно уведомления не менее трёх секунд и неизменные sampled BGP uptime/routes.
  Обычный `SetAdminDown` подтверждает только commit; получение пиром остаётся
  отдельной границей. См. [RFC](docs/ru/08-rfc-compliance.md).

- Освобождение последнего Control-session claim выполняет drain сохранённой
  сессии до cancel/join и освобождения sender. Reconciliation и rollback
  используют один параллельный бюджет десять секунд; ранняя отмена caller
  сообщает неполный drain и сохраняет cleanup ресурсов. Нефинальный claim
  сохраняет wire state. Unsolicited failure cleanup пропускает drain по
  RFC 9468 Section 2 и проверяет ошибки release;
  API deletion сохраняет deadline/cancel codes. `Manager.Close()` сохраняет
  raw cleanup после общего drain демона.

- Причинные наблюдения сессии: согласованные локальные state/diagnostic,
  известные remote state/diagnostic, оба C-bit, причина и строковая
  `observation_version` в snapshot, Watch и CLI. Для C-bit сохраняется
  политика `hard-fail`; graceful restart не реализован. GoBGP сохраняет
  локальную/удалённую административную причину при retry и gap resync,
  проверяет текущие identity и policy перед disable. Control Watch передаёт
  полный измеренный статус; Echo использует тот же frozen contract
  (`gobfd-qj0.8.2.1.3.8`, `gobfd-qj0.8.2.1.3.10`).

- Per-configured-peer метрики GoBGP: flap penalty с decay на момент scrape,
  recovery suppression и его максимальная граница, ожидающие желаемые Up/Down
  с timestamp следующего retry/recheck (`gobfd-qj0.8.2.4.7`). Принятый reload
  заменяет allowlist пиров; scrape не изменяет routing decisions и не создаёт
  labels из API или unsolicited sessions.
- Версионированные snapshot сессий и поток Watch с сигналом разрыва
  (`gobfd-qj0.8.2.1.3.2`; H-29, M-08). В `bfd.v1` добавлены `revision` в
  `ListSessionsResponse`, `GetSessionResponse` и
  `WatchSessionEventsResponse`, а также типы событий `GAP` и
  `SYNC_COMPLETE`. `WatchSessionEvents` подписывается до снимка, закрывает
  каждый снимок событием `SYNC_COMPLETE` и на отставший поток отвечает `GAP`
  и новым снимком. События изменения состояния содержат локальный адрес,
  интерфейс, тип сессии и ревизию; Get и List возвращают
  `remote_min_rx_interval`. `gobfdctl monitor` выводит `Gap`,
  `SyncComplete` и `rev=`.
- Версионированные события `SESSION_DELETED` при освобождении последнего
  владельца wire session через API, reconciliation или unsolicited cleanup
  (`gobfd-qj0.8.2.1.3.6`). События содержат идентификаторы и последнее
  состояние удалённой сессии; освобождение общей claim не создаёт событие
  удаления. Снимки не содержат удалённые сессии, отставшие подписчики
  восстанавливают состояние через `GAP`. Обработчик GoBGP забывает удалённые
  сессии и отменяет их ожидающие Down и Up, в том числе при восстановлении
  удаления из снимка после gap.

- mTLS для TCP-listener управления (`grpc.tls.cert_file`, `key_file`,
  `client_ca_file`; `gobfd-330.2`). Клиенты предъявляют сертификат от CA
  клиентов; ALPN предлагает HTTP/2 и HTTP/1.1; файлы читаются заново при
  каждом handshake. Принципал — URI SAN сертификата клиента (SPIFFE) или
  `dns:<name>`. В `gobfdctl` добавлены `--tls-ca`, `--tls-cert` и
  `--tls-key`; компаньоны читают `GOBFD_TLS_CA_FILE`, `GOBFD_TLS_CERT_FILE`
  и `GOBFD_TLS_KEY_FILE`. Значения по умолчанию не меняются.
- Опциональный Unix-сокет управления (`grpc.socket.path`, `mode`, `group`;
  `gobfd-330.1`). Запросы через него несут UID peer от ядра как принципал,
  в логах `principal=uid:<n>`. `gobfdctl --addr`, `gobfd-haproxy-agent` и
  `gobfd-exabgp-bridge` принимают endpoint `unix:///path`, а также `http://`
  и `https://`. `grpc.addr` может быть пустым, если задан сокет. Значения по
  умолчанию не меняются.
- Сбои интеграции с GoBGP видны (GitHub #86): метрики
  `gobfd_gobgp_rpc_errors_total{rpc}` и `gobfd_gobgp_endpoint_compatible`, а
  также named health check `gobfd.gobgp`, который находится в `NOT_SERVING`
  до успешного preflight GoBGP v4 и после неудачного preflight или действия
  над пиром.
- Аутентификация RFC 9468 unsolicited (раздел 6.2): `unsolicited.auth`
  принимает только `meticulous_keyed_sha1`, самый сильный механизм GoBFD.
  Пакет, создающий сессию, проверяется до создания сессии, и сессия
  подписывает ответы ключом. Без `unsolicited.auth` пакеты с битом A
  отклоняются (RFC 5880, раздел 6.8.6). Изменение `unsolicited.auth` требует
  перезапуска.
- В справочник конфигурации добавлен раздел о совместимости с GoBGP: `dev`
  поддерживает только проверенную GoBGP v4.10.0; описано обновление с GoBGP v3.
- Preflight GoBGP отклоняет нативный BFD GoBGP v4 для пиров
  сконфигурированных сессий GoBFD, чтобы у пира не было двух владельцев BFD.
- Добавлен capability preflight GoBGP: старт отклоняет endpoint без API
  GoBGP v4 (например, демон GoBGP v3) с понятной ошибкой, а недоступный
  endpoint повторно проверяется в фоне до определённого результата.
- Добавлен закреплённый SHA-256 рецепт пира FRR 10.7.1 на Debian trixie
  с локальным smoke запуска/config/остановки на amd64. BASE, BGP и RFC прошли
  локальную interop-квалификацию; остальные Alpine-сценарии заблокированы.
- Добавлен ограниченный in-place reload TX/RX для базовых сессий, принадлежащих
  только config: подтверждение Poll/Final, явный RX zero и автоматическое
  обновление generation/readiness receipts. Поздние ответы не отменяют ошибку
  транзакции; live FRR/BIRD reload qualification ещё впереди.
- Добавлена userspace UDP-квалификация VXLAN/Geneve с race detector, точной
  изоляцией tunnel, отклонением повреждённых пакетов и logs для сверки с capture.
- Добавлены package-internal Go lifecycle owner и декларативные systemd inputs
  sysusers/tmpfiles для shellless lifecycle hooks DEB/RPM без изменения четырёх
  публичных binaries.

### Изменено

- Готовность gRPC health выводится из компонентов (H-30, `gobfd-qj0.8.2.3.1`).
  Пустой service есть AND пяти sources reconciliation, runtime workers и
  интерфейсов listeners с привязкой к устройству. `bfd.v1.BfdService` следует за
  base, VXLAN и Geneve sessions и control listeners, включая unsolicited;
  `bfd.v1.EchoService` — за Echo; `bfd.v1.MicroBFDService` — за groups, members
  и listeners member links Micro-BFD. Named services находятся в `NOT_SERVING`
  до первого converged generation и пока их sources pending или failed,
  например после ошибки создания sender или при отсутствии overlay backend.
  `grpc.health.v1.Health` остаётся `SERVING` как сигнал liveness. `gobfd.gobgp`
  и `gobfd.management` не входят в пустой service.
- Listener с привязкой к устройству, интерфейс которого отсутствует, выключен
  или пересоздан с другим ifindex, переводит свои services в `NOT_SERVING`.
  Пересозданный интерфейс оставляет `NOT_SERVING` до перезапуска, так как сокет
  сохраняет ifindex момента bind; перепривязка внутри процесса отслеживается в
  `gobfd-qj0.8.2.2.6.4`. Привязки проверяются по interface events и
  периодически раз в 2 с; ошибка inventory интерфейсов прерывает запуск, а во
  время работы сохраняет прежнее состояние.
- Потеря receive path или dispatch приводит к fail-stop. Если сокет control,
  Echo, Micro-BFD или overlay receiver закрывается либо dispatch loop
  завершается во время работы daemon, health переходит в `NOT_SERVING`, sessions
  дренируются, процесс завершается с ошибкой для systemd `Restart=on-failure`.
  Control receiver больше не зацикливается на закрытом сокете. Shutdown
  переводит health в `NOT_SERVING` и закрывает receive listeners после AdminDown
  drain, поэтому простаивающий listener больше не блокирует выход.
- Reconciliation Micro-BFD отражается в логах receipt поколения одним
  источником `micro` вместо `micro_group` и `micro_member`. Daemon пишет
  `micro-BFD reconciliation had errors` или `micro-BFD reconciliation complete`
  вместо отдельных сообщений группы и сессий (`gobfd-qj0.8.2.2.1`).

- Stable v1 допускает module-only исключение `GO-2026-4736` для GoBGP v4.10.0 по
  решению мейнтейнера от 2026-10-21 (`gobfd-qj0.8.4.1`), пока GHSA/OSV указывают
  исправленную версию не выше закреплённой, а govulncheck финального candidate
  сообщает advisory только на уровне модуля (без импортированного пакета и
  достижимого символа). Запись allowlist и решение dependency inventory называют
  owner `@dantte-lp` и дату expiry (пересмотра) 2026-12-31; запись удаляется,
  когда база уязвимостей Go укажет исправленную версию v4.
- `scripts/vuln-audit.go` падает, если запись allowlist не соответствует ни
  одному finding сканеров, и требует удалить устаревшую запись.
- Демультиплексирование RFC 5881/5883 отклоняет несовпадение транспортного
  класса на обоих уровнях demux: multihop пакет 4784 не попадает в single-hop
  сессию, пакет 3784 не попадает в multihop сессию (`demux`/`scope_mismatch`).
  Listener 4784 больше не применяет фиксированную границу TTL>=254. Single-hop
  сессия принимает только свой интерфейс приёма: при заданном `interface`
  отклоняются другие устройства listener, и каждая single-hop сессия сравнивает
  индекс интерфейса из PKTINFO с индексом, определённым при создании по
  `interface` или по интерфейсу, владеющему `local`; демультиплексированная
  single-hop сессия также требует TTL 255. YAML-сессия, адрес `local` которой не
  назначен ни одному или назначен нескольким интерфейсам, остаётся pending и
  повторяется при событиях интерфейсов, resync или SIGHUP, соседние сессии при
  этом создаются; пока сессия pending, readiness остаётся `NOT_SERVING`, а адрес
  на нескольких интерфейсах остаётся pending, пока `interface` не задан и не
  применён через SIGHUP. API возвращает `FailedPrecondition`; API-сессия только
  с zone привязывает отправителя к интерфейсу zone. Адрес `local` на `lo` или
  dummy интерфейсе привязывает сессию к этому интерфейсу; если пакеты приходят на
  физический интерфейс, задавайте `interface`. Без `interface` zone link-local
  адреса выбирает интерфейс сессии, устройство listener и отправителя, поэтому
  одна пара link-local адресов на двух линках образует две сессии; zone,
  противоречащая `interface`, отклоняется (API: `InvalidArgument`). Отправитель single-hop привязывается через
  `SO_BINDTODEVICE` к заданному в YAML или API `interface`. `multi_hop` с
  `interface` отклоняется валидацией YAML и API; раньше такие сессии загружались,
  но никогда не совпадали при demux. Рекомендации по аутентификации multihop
  следуют RFC 5883 Sections 3 и 6. Point-to-point initial demux остаётся
  отложенным.

- Пир совместимости BIRD обновлён с 3.3.2 до 3.4.0 с закреплённым по checksum
  исправлением upstream `7cf75587` для CVE-2026-112112 (`gobfd-qj0.8.6.6.2`).
  Рецепт записывает хеши исходников и исправления и OCI labels; регрессионный
  тест FlowSpec проверяет, что BIRD помечает ненулевой IPv6 destination offset
  как invalid. Топологии BASE и BGP+BFD используют тот же рецепт.
- Обновлены pins Actions upload/download-artifact до v7.0.2/v8.0.2 для повторных
  попыток при HTTP 429 с `Retry-After`, GoReleaser Action до v7.3.0 для
  переиспользования runner tool cache и Scorecard Action до v2.5.0 для обновления
  движка Scorecard до v5.6.0 (`gobfd-qj0.8.6.4.2`).
- Генерация dependency inventory использует явный observation epoch `dev` и
  решения deferred для release line (`gobfd-qj0.8.2.8.7.6`). Записываются
  фактические license responses точных версий; старые observations не
  переносятся, несобранные внешние проверки и безопасность остаются unverified.
- Модули `dev` обновлены до Prometheus `client_golang` v1.25.0 и Moby client
  v0.6.2 (`gobfd-qj0.8.6.2`). Ответы метрик включают заголовки `Vary` для
  согласованных форматов; клиент контейнерных интеграций исправляет отмену
  и deadline во время HTTP upgrade. Обновление dbus ожидает даты допуска
  2026-10-20.
- **Ломающее:** демпфирование flap-ов GoBGP никогда не задерживает BFD Down
  (`gobfd-qj0.8.2.4.1`, H-27, контракт демпфирования v1). Каждый значимый
  Down сразу отключает пир; в подавленном состоянии демпфирование удерживает
  только восстановление (Up), а Down отменяет удерживаемое восстановление.
  Валидация отклоняет `gobgp.dampening.reuse_threshold <= 0` и
  `max_suppress_time <= 0` (`ErrInvalidDampeningMaxSuppressTime`).
- **Ломающее:** `GetSession` по адресу пира, которому соответствует
  несколько сессий, завершается с `FAILED_PRECONDITION` и перечисляет
  локальные дискриминаторы вместо возврата первого совпадения (M-07).
- Обработчик GoBGP восстанавливается после потерянных изменений состояния
  BFD (`gobfd-qj0.8.2.4.1`): при разрыве потока он восстанавливает переходы
  Down и Up по версионированному снимку и последнему увиденному состоянию
  каждой сессии, поэтому пир больше не остаётся включённым над отказавшим
  путём или отключённым после восстановления.
- **Ломающее:** обработчик GoBGP действует только по канонической
  single-hop или multi-hop сессии BGP-соседа (`gobfd-qj0.8.2.4.2`, H-31).
  Сессии Micro-BFD member link, VXLAN, Geneve и Echo к адресу соседа больше
  не отключают и не включают пир; IPv4-mapped адреса пиров приводятся к
  IPv4. При `gobgp.enabled` валидация конфигурации отклоняет две сессии к
  одному адресу пира; при второй базовой сессии, созданной во время работы,
  BFD Down не отключает пир и переводит `gobfd.gobgp` в `NOT_SERVING`, а BFD
  Up по-прежнему включает пир, отключённый GoBFD.
- Обработчик GoBGP включает только пиры, которые отключил сам
  (`gobfd-qj0.8.2.4.1`). Перед `DisablePeer` он читает admin state соседа и
  не трогает пиры, отключённые оператором, ни при BFD Down, ни при Up.
  Запись хранится в памяти: после перезапуска пиры, отключённые GoBFD,
  остаются отключёнными, пока их не включит оператор.
- **Breaking:** дамп flight recorder `/debug/flightrecorder` больше не
  обслуживается на listener метрик без аутентификации (`gobfd-qj0.8.2.3.2`).
  С новым `grpc.flight_recorder: true` он доступен на сервере управления
  вызывающим с принципалом (Unix-сокет или mTLS-идентичность, разрешённая
  `grpc.mutating_identities`); анонимные клиенты на loopback получают `403`.
  Recorder продолжает работать для дампа при остановке.
- Claims сессий API принадлежат принципалу вызвавшего (`gobfd-330.4`,
  GitHub #87): UID peer Unix-сокета, mTLS-идентичность или `local` для
  анонимных клиентов на loopback в режиме `grpc.unsafe_loopback_mutations`.
  `DeleteSession` от другого принципала возвращает `PermissionDenied` и
  логируется; два принципала, добавившие одну сессию, разделяют её. Новый
  `grpc.mutating_identities` (требует `grpc.tls`) ограничивает изменяющие RPC
  перечисленными mTLS-идентичностями; остальные mTLS-клиенты только читают.
- **Breaking:** API управления по умолчанию работает через Unix-сокет
  `/run/gobfd/gobfd.sock` и не открывает TCP (`gobfd-330.3`, GitHub #87).
  Plaintext `grpc.addr` на non-loopback или wildcard адресе останавливает
  запуск; используйте loopback или `grpc.tls`. Анонимные клиенты на loopback
  TCP могут читать, но получают `PermissionDenied` для `AddSession`,
  `DeleteSession`, `AddEchoSession`, `DeleteEchoSession`, `AddMicroBFDGroup`
  и `DeleteMicroBFDGroup`, если не задан `grpc.unsafe_loopback_mutations:
  true`; этот режим логируется и переводит новый health check
  `gobfd.management` в `NOT_SERVING`. `gobfdctl --addr`,
  `gobfd-haproxy-agent` и `gobfd-exabgp-bridge` по умолчанию используют
  `unix:///run/gobfd/gobfd.sock`. Пакеты добавляют группу `gobfd-admin`
  (`gobfd.sysusers`), systemd unit получает `RuntimeDirectory=gobfd` и
  `SupplementaryGroups=gobfd-admin`; конфигурация из пакета задаёт группу
  сокета `gobfd-admin`. Образы создают `/run/gobfd` и больше не объявляют
  `EXPOSE 50051`. Пример Kubernetes использует сокет и exec readiness probe.
  Миграция: привяжите TCP к loopback или добавьте mTLS, переведите изменяющих
  клиентов на сокет и добавьте операторов в `gobfd-admin`.
- **Breaking:** listener без аутентификации по умолчанию слушают loopback
  (GitHub #87). `grpc.addr` по умолчанию `127.0.0.1:50052`, `metrics.addr`
  — `127.0.0.1:9100`, в том числе в `configs/gobfd.example.yml`, который
  пакеты ставят как `/etc/gobfd/gobfd.yml`. Порт 50051 — порт gobgpd по
  умолчанию и `gobgp.addr` по умолчанию, поэтому GoBFD и GoBGP теперь
  запускаются рядом с настройками по умолчанию, а адрес API совпадает со
  значением по умолчанию `gobfd-haproxy-agent` и `gobfd-exabgp-bridge`;
  `gobfdctl --addr` по умолчанию `127.0.0.1:50052`. `gobfd-haproxy-agent`
  слушает agent-check на `127.0.0.1`, если новый ключ `listen_addr` не задаёт
  другой адрес. При запуске выводится предупреждение для API или метрик,
  доступных за пределами loopback. Аутентификация API и Unix-сокет по
  умолчанию из дизайна v1 — в `gobfd-330`.
- **Breaking для Go-потребителей `pkg/bfdpb`:** ConnectRPC переведён на
  connect-go v2.0.0 и grpchealth v2.0.0. `bfdv1connect` перегенерирован
  `protoc-gen-connect-go` v2.0.0: клиенты принимают `*connect.Client`
  (`connect.NewClient(connecthttp.NewTransport(httpClient, baseURL))`),
  обработчики регистрируются на `*connect.Server`. Wire-протокол `bfd.v1`
  не изменён; тест закрепляет вызовы grpc-go, `grpc.health.v1`, Connect и gRPC-Web.
  Демон, `gobfdctl` и оба моста применяют лимит connect-go v2 по умолчанию
  4 MiB на сообщение вместо отсутствия лимита; один ответ `ListSessions`
  вмещает около 28 000 сессий по 147 байт.
  Перехватчики логирования и восстановления охватывают и потоковые вызовы;
  закрытие `WatchSessionEvents` клиентом логируется как `rpc canceled` на Info.
- **Breaking:** приём RFC 9468 unsolicited ограничен и проверяется по
  connected subnet (находки аудита H-17, H-18). Пакет создаёт сессию, только
  если он пришёл single-hop с TTL 255 (не на multi-hop listener), его source
  принадлежит подсетям локального адреса приёма, входит в
  `allowed_prefixes`, когда список задан, и не совпадает с локальным адресом;
  ненумерованный путь приёма отклоняется. При `unsolicited.enabled: true`
  `max_sessions` должен быть >= 1 (проверка конфигурации отклоняет 0, ранее
  означавший отсутствие лимита); новые сессии допускаются не чаще 10 в
  секунду с burst 20.
- **Breaking:** включённая политика RFC 9468 unsolicited проверяется при
  загрузке (находка аудита H-19): интервалы `session_defaults` от 1 мкс до
  2^32-1 мкс, `required_min_rx` ненулевой, `detect_mult` от 1 до 255,
  `cleanup_timeout` >= 0, хотя бы один интерфейс включён, каждый элемент
  `allowed_prefixes` — корректный CIDR. Пропущенные `session_defaults`
  получают документированные 1s, 1s и 3; ранее они были нулевыми, и создание
  каждой автосессии завершалось ошибкой.
- GitHub Actions обновлены с учётом правила возраста релиза до peeled
  commit SHA: `anchore/sbom-action` v0.24.3, `bufbuild/buf-action` v1.6.0,
  `codecov/codecov-action` v7.1.1, `docker/setup-buildx-action` v4.4.1,
  `docker/setup-qemu-action` v4.4.0, `github/codeql-action` v4.38.2 и
  `SonarSource/sonarqube-scan-action` v8.3.0. `dependabot.yml` направляет
  обновления версий в `dev`; GitHub читает его только из ветки по умолчанию,
  поэтому настройка заработает после попадания в `master`. Обновления
  безопасности остаются в ветке по умолчанию.
- Пиры наблюдаемости работают на Debian trixie: Prometheus v3.15.0 и Grafana
  13.2.4 (восемь исправлений CVE в 13.2.1-13.2.3 и четыре в 13.2.4) из
  закреплённых по checksum upstream-архивов в `test/interop/prometheus` и
  `test/interop/grafana`, а FRR — из общего trixie-рецепта; образы Grafana на
  Alpine, Prometheus на BusyBox и FRR на Alpine заменены. Grafana запускается
  через exec-form entrypoint без upstream shell-обёртки.
- Пример Kubernetes запускает GoBGP v4.10.0 как `gobgp:local`, собранный из
  общего рецепта и импортируемый в k3s через `integrationctl`, вместо
  `docker.io/jauderho/gobgp:v3.37.0`. Этот образ больше нигде в репозитории не
  используется. На живом кластере k3s пример не запускался.
- Образ GoBFD для Containerlab собирает sidecar GoBGP v4.10.0 из общего
  source pin вместо копирования из `docker.io/jauderho/gobgp:v3.37.0`;
  контрактный тест поддерживает совпадение pin. Совместимость с vendor NOS на
  GoBGP v4 ещё не квалифицирована.
- Обновлены базовые образы: индекс `debian:trixie-slim` `a29215f6` (Debian
  13.7, `trixie-20261005`) во всех собственных рецептах, `haproxy:3.4.6-trixie`
  (исправления 3.4.4-3.4.6, включая BUG/MAJOR в QUIC, H3 и HTX) и
  `nginx:1.31.6-trixie` (исправление HTTP/3 CVE-2026-90439).
  `oraclelinux:10-slim` последует с 2026-10-19.
- ExaBGP Anycast использует общий рецепт GoBGP v4.10.0 и образ ExaBGP 5.0.13
  на Debian trixie с `gobfd-exabgp-bridge` вместо
  `docker.io/jauderho/gobgp:v3.37.0` и `ghcr.io/exa-networks/exabgp`. Gate
  выполняется через контроллер проекта с лимитами и завершается ошибкой, если
  маршрут не анонсирован, не отозван после отказа BFD на стороне приёмника или
  не восстановлен. Прежний сценарий ставил на паузу GoBFD, питающий bridge, и
  только выводил предупреждения.
- **Breaking:** клиент интеграции GoBGP переведён с
  `github.com/osrg/gobgp/v3` v3.37.0 на `github.com/osrg/gobgp/v4` v4.10.0.
  GoBGP v3 `/apipb.GobgpApi` и v4 `/api.GoBgpService` несовместимы по wire:
  GoBFD из `dev` требует демон GoBGP v4. Общий рецепт пира GoBGP собирает
  v4.10.0 для потребителей BGP, RFC, BGP fast-failover E2E и интеграции;
  локальные amd64 testcontainers gates BGP+BFD и BGP fast-failover проходят,
  RFC testcontainers gate проходит все четыре race-сценария за 162.12 с,
  ручные пути Compose не перезапускались. ExaBGP anycast,
  Kubernetes и Containerlab по-прежнему используют образы GoBGP v3.37.0 и
  несовместимы до миграции. `release/v0.6` сохраняет GoBGP v3.37.0.
- BGP fast-failover Compose и testcontainers переиспользуют общие рецепты
  FRR 10.7.1 и GoBGP (v3.37.0 на момент квалификации, сейчас v4.10.0) на
  trixie с лимитами сборок/runtime. Ручной
  запуск использует существующий контроллер владения ресурсами и запрещает
  неявные сборки; capture хранится в контейнере. Локальный amd64 race-сценарий
  проходит отзыв/восстановление маршрута, packet evidence и точный cleanup.
- RFC Compose и testcontainers переиспользуют общие рецепты FRR 10.7.1 и
  GoBGP (v3.37.0 на момент квалификации, сейчас v4.10.0) на trixie: явный
  выбор BFD/BGP-демонов, лимиты сборок и
  всех восьми runtime-контейнеров, host revision и очистка образов до сборки.
  Полный локальный amd64 testcontainers gate проходит четыре race-сценария
  без пропусков и проверяет удаление своих ресурсов; wire subcode 10 RFC 9384
  и истечение unsolicited-сессий остаются неквалифицированными.
- BGP Compose и testcontainers используют общий рецепт Debian FRR 10.7.1
  с явным выбором BGP-демонов и лимитами сборки/runtime. Ручной lifecycle
  переиспользует сборку с лимитами и проверкой владения ресурсами.
  GoBGP (тогда v3.37.0) и ExaBGP 5.0.13 используют закреплённые SHA-256 upstream
  исходники на trixie; системный Python разрешён только для runtime внешнего
  ExaBGP, без Python tooling. Ручной и полный testcontainers BGP gates
  пройдены на amd64: все четыре race-теста каждого пути, без пропусков,
  с проверенным удалением своих ресурсов.
- Базовые four-peer определения Compose и testcontainers собирают Debian FRR
  локально и ограничивают ресурсы runtime. Ручные BASE-сборки, сборки
  testcontainers и генератор invalid vectors также ограничены по CPU/RAM;
  ручной запуск запрещает неявную сборку. Оба BASE gates локально пройдены
  на amd64 с явными RFC-пропусками и проверенным cleanup;
  переключение остальных FRR-потребителей ещё впереди.
- Все 16 shell-shebang fixtures, встроенных в Go-тесты, заменены на guarded
  Go helper processes с сохранением контрактов argv, потоков, exit status,
  отмены и cleanup owned resources.
- Последние два tracked shell hook nFPM заменены нативными Go executables.
  Release packaging теперь переписывает RPM lifecycle program tags, обновляет
  artifact manifest и checksums GoReleaser, проверяет точную package matrix и
  загружает её в draft только после проверки.
- Публичные roadmap, дизайн release-веток и исполняемый план сверены с
  опубликованными неизменяемыми evidence v0.6.4 после исправления и приёмки
  всех трёх P1-находок maintenance-ревью в Beads.

### Исправлено

- Штатная остановка завершается с кодом 0, если AdminDown drain не может
  завершиться из-за недоступного пира или пира без discriminator; drain
  записывается как предупреждение. Ошибки admission, commit, owner, неудачная
  запись AdminDown и другие локальные ошибки drain по-прежнему завершают процесс
  с ошибкой, fail-stop при потере receive path не изменён.

- Debian-пакет зависит от `procps`, который предоставляет `/bin/kill` для
  `ExecReload=`; на минимальной Debian 13 только с `systemd` команда
  `systemctl reload gobfd` завершалась с `status=203/EXEC`.

- Hosted jobs E2E Evidence выполняются на `ubuntu-26.04` (Podman 5.7.0). На
  `ubuntu-latest` (Ubuntu 24.04) Podman 4.9.3 собран с Debian imagebuilder
  1.2.5, который отклоняет `ADD --checksum` в interop Containerfile; рецепты с
  закреплёнными checksum не изменены. `toolbootstrap podman-runtime` пишет в
  лог образ runner и версию Podman (`gobfd-22v`).

- Hosted E2E на `ubuntu-26.04` снова читает и останавливает захват пакетов.
  AppArmor 5.0.2 в Ubuntu 26.04 привязывает профиль `tshark` к
  `/usr/bin/tshark`, а rootless Podman запускает контейнеры без профиля.
  Профиль запрещал `tshark -r /captures/bfd.pcapng` (exit status 3) и сигналы
  остановки контейнера. Образ захвата запускает `tshark` и `dumpcap` из
  `/usr/local/bin`. Jobs E2E также разрешают профилю `pasta` принимать сигналы
  Podman, без которых не завершается rootless network teardown
  (LP: #2154379), и выводят отказы AppArmor после прогона (`gobfd-dah`).

- Ошибки групп Micro-BFD отображаются в клиентские коды (`gobfd-qj0.8.2.2.1`):
  `ErrMicroBFDGroupExists` возвращает `AlreadyExists`, `ErrMicroBFDGroupNotFound`
  — `NotFound`, расходящаяся группа с тем же LAG
  (`MicroBFDConfigConflictError`) — `FailedPrecondition` вместо `Internal`.

- Группы Micro-BFD получают типизированные claim владельцев, как сессии
  (`gobfd-qj0.8.2.2.1`). Reload удаляет только свои группы и не может забрать
  member link сохраняющейся группы API; одинаковая группа API и reload
  разделяется, каждый источник освобождает только свой claim.
  `DeleteMicroBFDGroup` для группы, которой вызывающий не владеет, например
  группы reload, возвращает `FailedPrecondition`
  (`ErrMicroBFDGroupOwnerClaimNotFound`). Группы API используют principal
  вызывающего.

- Каждый member новой группы Micro-BFD начинает с состояния своей существующей
  member-сессии (RFC 7130 Section 3). Member, перенесённый между LAG за один
  reload, или сессия, созданная через API до группы, раньше оставались Down в
  новой группе при сессии в Up; теперь такой member считается Up и actuator
  получает его в порядке dispatch.

- Группа Micro-BFD фиксируется вместе со своими member-сессиями (RFC 7130
  Section 2, `gobfd-qj0.8.2.2.1`, H-24). Reload применяет один источник
  Micro-BFD: ошибка member-сессии откатывает новые группы и сессии прохода до
  освобождения любой зафиксированной группы или сессии; группа неудачного
  прохода не попадает ни в dispatch, ни в actuator. Member link, указанный в
  группах двух LAG-интерфейсов в любом address family, теперь отклоняет весь
  кандидат; раньше такая конфигурация загружалась, а события member уходили в
  произвольную группу. `AddMicroBFDGroup` возвращает `FailedPrecondition` для
  member link другого LAG. Dispatch находит
  единственного владельца по индексу `{member link, address family}`.

- Контракт RFC 5880 каждой member-сессии Micro-BFD проверяется на пути
  кандидата daemon и прямом пути Manager/API: адрес пира, wire-границы
  интервалов, detect multiplier и непустые имена LAG и member (RFC 7130
  Section 2.2, M-09). Незаданные таймеры группы наследуют `bfd.default_*`.
  Группы хранят таймеры, `ListMicroBFDGroups` возвращает их, изменение таймера
  работающей группы — конфликт конфигурации. `AddMicroBFDGroup` возвращает
  `InvalidArgument` для явного нулевого интервала или пустого имени member.

- Actuator Micro-BFD вызывается после освобождения блокировок Manager, в
  порядке dispatch, с context deadline 5 секунд на вызов (RFC 7130 Section 5,
  M-10). Заблокированный backend LAG больше не останавливает CRUD групп; ошибка
  или deadline пишет `micro-BFD actuator failed` с `deadline_exceeded`.
  Backend, игнорирующий context, по-прежнему задерживает следующие уведомления.

- Обновление таймеров из config вне Up, включая AdminDown, вступает в силу
  немедленно, receipt подтверждает его Poll (RFC 5880 Section 6.8.3).
  Восстановление, принятое после того, как локальный нулевой Required Min RX
  перевёл сессию в Down, раньше ожидало Up; пир, соблюдающий ноль, молчал,
  обновление завершалось ошибкой, reconciliation оставалась незавершённой.
  Receipt получает Applied, только если действующий кортеж совпадает с
  подтверждённым; Poll вне Up без Final завершает receipt ошибкой, не блокируя
  следующие поколения; вход в Up через Final сохраняет односекундный порог TX
  до recovery Poll (`gobfd-qj0.8.2.1.1.2`).

- После одного Detection Time без пакетов в любом состоянии
  `bfd.RemoteMinRxInterval` возвращается к 1 микросекунде, `bfd.RemoteDemandMode`
  — к 0, начальным значениям RFC 5880 Section 6.8.1 (Section 6.8.18,
  `gobfd-qj0.8.2.1.1.2`). Если сброс меняет TX-интервал или разрешение
  передачи, следующий пакет планируется от предыдущей успешной отправки. Пир,
  объявивший нулевой или большой Required Min RX и замолчавший, больше не
  удерживает периодический TX; Active-drain в AdminDown после одного Detection
  Time без пакетов отправляет уведомление Section 6.8.16 и может завершиться в
  пределах бюджета drain,
  Passive-сессия молчит. FRR 10.7.1 не может объявить нулевой RX и не имеет
  Demand; BIRD 3.4.0 не имеет Demand, поддержка нулевого RX не проверена. Wire
  evidence ведётся в `gobfd-qj0.8.2.1.1.2.3`.

- Некорректные protobuf durations в generic, Echo и Micro-BFD запросах,
  клиентские интервалы, отклонённые ядром, и повторяющиеся Micro-BFD members
  возвращают `InvalidArgument` (`gobfd-qj0.8.2.1.3.5`). Пропущенные generic/Micro
  интервалы сохраняют `1s`, явный generic RX zero допустим, Echo требует
  положительный интервал. Классификация внутренних ошибок Manager сохранена;
  проверка и применение структурно корректных таймеров Micro-BFD members
  остаются в `gobfd-qj0.8.2.2.1` (M09).

- Положительные настроенные Control TX/RX интервалы меньше микросекунды
  отклоняются с `ErrInvalidWireInterval` до создания сессии или reconciliation
  (`gobfd-qj0.8.2.1.1.3`). Явный RX zero и дробные микросекунды выше минимума
  сохраняют прежнюю семантику.

- Изученный peer discriminator сохраняется при корректных Down/AdminDown и
  локальном PathDown (`gobfd-qj0.8.2.1.2.4`). Ответы Final на Poll и повторы после
  ошибки отправки сохраняют корректный `YourDiscriminator`, включая passive role;
  owner detection timer сбрасывает его после одного Detection Time без приёма.

- `RemoteDiscr` сбрасывается после одного Detection Time в любом состоянии
  сессии (`gobfd-qj0.8.2.1.2.3`) независимо от истечения authentication sequence
  после двух Detection Time. Последние remote administrative facts и causal
  version сохраняются, когда меняется только wire discriminator. Passive-сессии
  с неизвестным discriminator сохраняют pending Final без передачи пакетов,
  включая прямые FSM sends и повторы.

- AdminDown коммитится только в session owner loop с ограниченным контекстом
  commit receipt, включая pre-Run и заполненную очередь
  (`gobfd-qj0.8.2.1.3.3`, prerequisite H-09). `Run` имеет одного owner за
  время жизни сессии. Manager drain выполняет fanout по конкретным сессиям вне
  locks и объединяет ошибки; shutdown сохраняет их. Receipt предшествует TX и
  не доказывает доставку. Повторы, negotiated drain и bounded I/O H-12 остаются
  открытыми; фиксированное двухсекундное окно уведомления сохранено.

- Проверка Keyed MD5/SHA1 использует объявленный BFD `Length`, включая PDU
  extensions и исключая transport padding (`gobfd-qj0.8.2.1.5.2`). Независимые
  wire vectors покрывают пять типов аутентификации. Утверждённая политика
  конфигурации v1 отклоняет authenticated padding targets 24/25; target 26
  сохраняет более длинные PDU. YAML defaults и overrides отклоняют numeric
  wraparound, дроби и boolean conversion до декодирования. Authenticated
  padded TX 128 байт квалифицирован с FRR Simple Password и BIRD Keyed SHA1
  (`gobfd-qj0.8.2.1.5`): нулевой padding и IPv4 DF подтверждены. Проверка
  peer RX использует обычные authenticated PDU; path MTU и public API padding
  остаются отдельными задачами.

- Приём сохраняет полный BFD transport payload до 9000 байт при неизменной
  границе кодека 64 байта (`gobfd-qj0.8.2.1.5.1`). Linux отклоняет усечённые
  UDP datagrams; listeners проверяют read size и возвращают заимствованные
  буферы. Userspace VXLAN/Geneve учитывают максимальный поддерживаемый framing
  вокруг inner payload до 9000 байт на RX и TX. Владение authenticated wire
  сохраняется; authenticated peer interoperability остаётся отдельной проверкой.

- Истечение authentication sequence использует монотонное время последнего
  корректного приёма (`gobfd-qj0.8.2.1.2.2`, M-04). `AuthSeqKnown` сбрасывается
  после двух Detection Time в любом состоянии, включая проверку перед
  аутентификацией, когда RX обрабатывается раньше готового timeout.
  `RcvAuthSeq` сохраняется до корректного приёма; ошибка аутентификации не
  откладывает expiry.

- Словарь настроек логирования проверяется до применения запуска или SIGHUP
  (`gobfd-8f3.9`): уровни `debug`, `info`, `warn`, `error` без учёта регистра;
  форматы — строчные `json` или `text`. Явные пустые строки, пробелы и
  неизвестные значения дают ограниченные типизированные ошибки полей.
  Некорректный reload сохраняет generation, health, log level, sessions и ресурсы отправителей.

- `socket.read_buffer_size` / `socket.write_buffer_size` применяются к control,
  Echo, Micro-BFD и unsolicited UDP-путям, а также к двунаправленным userspace
  VXLAN/Geneve-сокетам (`gobfd-8f3.7`). Ноль сохраняет настройки ОС;
  некорректные неотрицательные целые отклоняются с ограниченными ошибками полей.
  Политика запуска действует и для создаваемых позже отправителей и остаётся
  неизменяемой при SIGHUP.

- Исправлено описание буферов сокетов (`gobfd-8f3`): `read_buffer_size`
  применяется и к привязанным к интерфейсу wildcard single-hop listeners IPv4
  и IPv6 для каждого enabled unsolicited interface. Прежнее описание указывало,
  что приём unsolicited BFD использует существующие control listeners.

- Конфигурация отклоняет разные interface для одного control listener
  (parsed local address и single-hop/multi-hop type) или enabled Echo
  listener (parsed local address) независимо от порядка записей
  (`gobfd-8f3.8`). Типизированная ошибка содержит только ограниченный
  идентификатор поля конфигурации; владение listeners не меняется.
- Конфигурация отклоняет переполнение detection multiplier, дроби и boolean до
  weak decoding для base, Echo, VXLAN, Geneve и Micro-BFD.

- Хранилище authentication keys отклоняет повторяющиеся KeyID, включая
  одинаковые rotation keys: выбор ключа передачи и приёма однозначен.

- HAProxy и ExaBGP переподключают Watch после ошибки и EOF, сбрасывают состояние
  при обрыве и `GAP`, применяют снимок после `SYNC_COMPLETE`. Удаление сессии
  отзывает health и маршрут. Selectors local/interface/type разделяют сессии
  одного peer; неоднозначность даёт отказ. ExaBGP сверяет маршрут после reconnect
  и перезапуска процесса, завершается при ошибке или неполной записи stdout.
  HAProxy backend ports должны различаться.

- `AddSession` возвращает те же поля снимка Manager, что Get и List,
  включая начальное значение `remote_min_rx_interval`.

- Production Compose использует сеть хоста: Prometheus опрашивает loopback
  metrics GoBFD, Grafana подключается к источнику данных Prometheus
  (`gobfd-6de`). Prometheus и Grafana теперь слушают только host loopback на
  портах 9090 и 3000; удалённый доступ требует tunnel или reverse proxy.
- Оставшиеся ссылки на RFC 5882 Section 4.3 (Graceful Restart) для реакции
  GoBGP в коде, примерах конфигурации и матрице соответствия RFC заменены на
  Section 10.2 (Interactions with BGP) (H-27).
- Пассивные сессии RFC 9468 отвечают с локального адреса и интерфейса, на
  которые пришёл пакет, каждая со своим source port RFC 5881 из общего
  аллокатора демона, для IPv4 и IPv6 (находка аудита C-07). Общий IPv4
  wildcard sender удалён; ответ с IPv6 link-local адреса привязан к
  интерфейсу приёма.
- `MarshalControlPacket` проверяет секцию аутентификации до записи первого
  байта (находка аудита H-03). Бит A и секция должны совпадать; Auth Len
  равен 24 для Keyed MD5, 28 для Keyed SHA1 и 3 плюс длина пароля для
  Simple Password; дайджест — 16 или 20 байт, пароль — от 1 до 16 байт;
  зарезервированные и неизвестные типы отклоняются (RFC 5880, разделы
  4.2-4.4). Отклонённый пакет не изменяет буфер; marshal по-прежнему без
  аллокаций.
- Обработчик GoBGP хранит для каждого пира последнее неприменённое действие
  DisablePeer или EnablePeer. Временные ошибки GoBGP повторяются с задержкой
  от 1 до 30 с без нового события BFD; InvalidArgument, Unimplemented,
  PermissionDenied, Unauthenticated и закрытый клиент не повторяются. Down,
  удержанный подавлением флапов, применяется после снятия подавления
  (RFC 5882, раздел 3.1). Ожидающий Down отбрасывается, когда сессия
  переходит в AdminDown (RFC 5882, раздел 4.2); ожидающий Up сохраняется
  при AdminDown. Удаление сессии отменяет оба ожидающих направления.
  Комментарии обработчика больше не относят реакцию BGP к
  разделу 4.3 RFC 5882.
- Сборка cached Control packet без аутентификации больше не выделяет объект
  пакета на каждую передачу из-за неиспользуемой ветки подписи.
- Final пира не завершает локальный Poll до успешной отправки Poll-пакета.
  Ошибки отправки и crossed Final-only ответы не подтверждают передачу;
  новая последовательность не использует подтверждение предыдущей.
- Переход из медленного режима в Up запускает локальный Poll при уменьшении
  объявляемого TX-интервала ниже одной секунды, сохраняя Final retry и исключение
  одновременных P/F без дополнительной передачи только ради Poll.
- Начальные базовые BFD-сессии сохраняют явный нулевой receive interval в YAML
  и generic API, а пропущенные поля наследуют defaults. Ноль запрашивает
  остановку периодических Control packets пира, не выключая локальный передатчик.
- Изменения receive interval пира теперь пересчитывают срок передачи от
  последней успешной отправки. Периодический TX останавливается при нулевом
  интервале пира или активном remote Demand без локального Poll; повторные
  попытки отправки Final сохраняются.
- Ответ Final сохраняется при ошибках подписи, кодирования и отправки и
  снимается только после успешной отправки. Final исключает Poll в том же
  пакете, не отменяя локальную Poll sequence; повторная попытка не блокируется
  запретом периодической передачи.
- Overlay listeners возвращают выделенный inner source port ровно один раз при
  закрытии. VXLAN/Geneve send и receive учитывают отмену context без нарушения
  работы общего listener и следующих операций.
- `golang.org/x/crypto` обновлён до `v0.56.0` в runtime- и tools-графах, что
  устраняет достижимые SSH denial-of-service уязвимости GO-2026-6354 и
  GO-2026-6355.
- Изолированный tools-граф обновлён до `github.com/ulikunitz/xz v0.5.16` и
  `github.com/klauspost/compress v1.18.7`, что устраняет GO-2025-3922 и
  GO-2026-5841 из зависимостей release packaging.
- Release notes теперь включают все maintenance-записи после предыдущего
  опубликованного стабильного релиза в той же линии, а также привязанные к тегу
  ссылки на changelog и сравнение. Release closeout также требует, чтобы
  принятая стабильная история и оба changelog попали в `master` до завершения.
- First-party Go toolchain обновлён с `go1.27.0` до `go1.27.2`, а
  `golang.org/x/net` — с `v0.58.0` до `v0.61.0` в runtime- и tools-графах
  модулей, что устраняет двенадцать достижимых advisory: GO-2026-6599, 6600,
  6603, 6604, 6605, 6607, 6608, 6610, 6611, 6612, 6613 и 6617. Все потребители
  `golang:1.27.2-trixie` используют один index digest. GO-2026-4736 обрабатывается миграцией на
  GoBGP v4 и записью allowlist на уровне модуля.
- Runtime-модули обновлены с учётом правила возраста релиза семь дней:
  `google.golang.org/grpc` v1.84.0, `github.com/knadh/koanf/v2` v2.3.8,
  `github.com/prometheus/client_model` v0.6.3, `github.com/moby/moby/api`
  v1.56.1, `github.com/moby/moby/client` v0.6.1 и сгенерированный модуль
  protovalidate 20260825. client_golang v1.25.0, moby client v0.6.2 и dbus
  v5.2.4 последуют с дат допуска.
- Инструменты разработки и релиза обновлены с учётом правила возраста
  релиза: golangci-lint v2.14.0 (92 линтера, новых замечаний нет), gosec
  v2.29.0, govulncheck v1.8.0, osv-scanner v2.6.0, buf v1.73.0, закреплённый
  по checksum провайдер Docker Compose 5.6.0, syft v1.54.1, trivy v0.75.0 и
  GoReleaser v2.18.2. govulncheck v1.9.0, osv-scanner v2.7.0 и GoReleaser
  v2.18.3 последуют с дат допуска.

### Безопасность

- Пакетный `gobfd.service` получил измеренный контракт песочницы:
  `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK`,
  `SystemCallFilter=@system-service` с `SystemCallErrorNumber=EPERM`,
  `LockPersonality=true`, `ProtectProc=invisible`, `ProcSubset=pid`,
  `UMask=0077` и `LimitNOFILE=16384`. Инвентарь `strace` всех транспортных
  режимов, GoBGP, reload и shutdown показал только эти семейства и 50
  системных вызовов из `@system-service`. Под systemd 257 в Debian 13 и Oracle
  Linux 10 юнит запускается, выполняет reload, перезапускается после сбоя и
  штатно останавливается; exposure `systemd-analyze security` снижен с 5.2 до
  3.3. `ProcSubset=pid` снижает backlog listener Go до 128. Существующий
  `ProtectKernelTunables=` оставляет `/sys` только для чтения, поэтому actuator
  `kernel-bond` в режиме `enforce` не может писать в bonding sysfs под этим
  юнитом; opt-in drop-in `deployments/systemd/gobfd-kernel-bond.conf` открывает
  на запись только `bonding/slaves` и выдаёт `CAP_DAC_OVERRIDE`, которая нужна
  для файла sysfs `0644 root:root` и снимает для сервиса проверки чтения файлов;
  квалификация записи остаётся открытой (`gobfd-qj0.8.2.3.3.1`).

- First-party Go toolchain обновлён с `go1.27.2` до `go1.27.3` в `go.mod`,
  `tools/go.mod`, CI- и release-workflow, Containerfile и тестовых harness.
  Все потребители `golang:1.27.3-trixie` используют один index digest.
  Устраняет достижимую
  [GO-2026-6809 / CVE-2026-111943](https://pkg.go.dev/vuln/GO-2026-6809)
  в `crypto/x509` `Certificate.Verify`. Мейнтейнер принял `go1.27.3`
  2026-10-21 как security-исключение из правила возраста релиза семь дней.

## [0.6.4] - 2026-08-27

### Исправлено

- Changelog pipe GoReleaser остаётся включённым, поэтому `--release-notes`
  загружает точный датированный раздел CHANGELOG в body черновика до
  fail-closed проверки. Неизменяемый тег `v0.6.3` и его полный, но
  неопубликованный draft остаются неудачной recovery-попыткой; `v0.6.4`
  исправляет её вперёд без перемещения или повторного использования тега.

## [0.6.3] - 2026-08-27

> Статус релиза: неизменяемый тег, полный draft, артефакты и versioned OCI
> images были созданы, но draft остался неопубликованным из-за пустых release
> notes. Неудачная попытка сохранена и заменена опубликованным релизом
> `v0.6.4`.

### Исправлено

- Release qualification теперь записывает четыре smoke-build binary вне
  checkout и устанавливает checksum-pinned Podman Compose и uv test tools в
  обоих full-race jobs, а строгий Go lint остаётся в digest-pinned контейнере
  Debian Trixie. Поэтому сгенерированные binary и отсутствующие runner CLI
  больше не блокируют release gate до создания артефактов. Неизменяемый тег
  `v0.6.2` остаётся первой неудачной попыткой без GitHub Release и артефактов;
  `v0.6.3` является её fix-forward recovery release.

## [0.6.2] - 2026-08-27

> Статус релиза: неизменяемый тег создан, но workflow run `33083358370`
> завершился ошибкой до создания draft или артефактов. Неудачная попытка
> сохранена; следующий recovery cut `v0.6.3` также остался неопубликованным, и
> обе попытки заменены опубликованным релизом `v0.6.4`.

### Добавлено

- Compatibility-тесты Go 1.27 для duplicate members в JSON, замены
  некорректного UTF-8, границы количества HTTP header values и реальных
  Podman build contexts.

### Изменено

- Quick Start использует отслеживаемый `configs/gobfd.example.yml`, а
  дорожная карта EN/RU является датированной проекцией Beads. В legacy-плане
  S12 отмечены реализованные Echo/Micro CRUD и отсутствующий Overlay API.
- Документация graceful shutdown теперь соответствует фиксированному
  двухсекундному drain в коде и описывает доставку AdminDown как best effort, а
  не как гарантию.
- Публичная документация RFC и производительности теперь указывает, что wire
  subcode 10 RFC 9384 не реализован с GoBGP v3, помечает неполные RFC-возможности
  как partial или preview и ограничивает заявления об аллокациях и throughput
  точными этапами измеряемых benchmarks. RX до enqueue и TX без socket теперь
  имеют stage-specific имена.
- Нативный OVS LAG backend переведён с архивного модуля `ovn-org` на активный
  `github.com/ovn-kubernetes/libovsdb` v0.8.1 с сохранением существующего
  transaction-контракта RFC 7130.
- Репозиторные проверки Markdown и PR title переведены на stdlib-checker Go
  1.27, а spelling — на закреплённый в uv codespell 2.4.3. Node.js, npm,
  markdownlint-cli2, CSpell и commitlint удалены из first-party development и
  CI-инструментов качества.
- Alpine Linux больше не используется в first-party build, runtime, benchmark,
  smoke, interop и integration images. Эти пути переведены на immutable OCI
  indexes Debian trixie; единственная другая разрешённая OS baseline — Oracle
  Linux 10.
- `make int-bgp-failover` запускает Go 1.27 testcontainers Podman gate для
  GoBFD, GoBGP v3.37.0 и FRR 10.7.0, сохраняет пакетные/container-артефакты и
  доказывает точную очистку; операционный Compose-пример сохранён.
- Опциональная проверка некорректных BFD-векторов теперь использует packet
  codecs репозитория на Go для сохранённых 18 групп и 1055 пакетов. Legacy ID
  сервиса `scapy` оставлен для совместимости топологии, но Scapy и его Python
  runtime больше не являются зависимостями.
- HTML-отчёт cross-implementation benchmarks теперь формирует тестируемая
  команда на Go 1.27 вместо inline Python heredoc. Входы ограничены по размеру
  и проверяются по identity, metadata содержит ровно один JSON-документ, а
  отчёт mode 0600 публикуется только атомарной заменой в том же каталоге.
- Оркестрация подготовки вендорных образов containerlab перенесена в
  ограниченную команду Go 1.27. Python оставлен только для подготовки
  ISO/rootfs VyOS и импорта предоставленных оператором архивов Arista/Cisco
  через три явных subcommands frozen uv.
- Обязательная матрица совместимости четырёх BFD-пиров использует FRR 10.7.0,
  BIRD 3.3.2, immutable Holo 0.9.0 и Thoro/bfd. Holo настраивается через
  healthy-gated одноразовый YANG loader; lifecycle tests требуют свежих
  подтверждений отказа и восстановления из пакетов и состояния демона.
- First-party toolchain baseline переведён на Go 1.27.0 в `go.mod`, GitHub
  Actions, development/release images и test harness builders. Compiler images
  закреплены по immutable multi-platform OCI indexes. Go 1.27 включает
  исправление `net/http` `ReadHeaderTimeout`, также выпущенное в Go 1.26.7.
- Timer tests используют `testing/synctest.Sleep`, а HTTP tests — новый в Go
  1.27 helper `httptest.NewTestServer(t, handler)`, когда custom listener не
  требуется.
- Строгий набор `golangci-lint` сохранён под Go 1.27 через upstream-обновление
  `honnef.co/go/tools`; используется `golangci-lint v2.13.1` и стабильный
  Staticcheck 2026.2, `staticcheck` остаётся включённым.
- Runtime-модули обновлены до актуальных совместимых релизов, включая
  ConnectRPC `v1.20.0`, gRPC health `v1.5.0`, koanf `v2.3.6`, Prometheus client
  `v1.24.1`, console `v0.5.0` и protobuf `v1.36.12`. First-party YAML imports
  переведены на поддерживаемый путь `go.yaml.in/yaml/v3 v3.0.5`.
- Development tooling воспроизводимо закреплён: Buf `v1.72.0`, gopls
  `v0.23.0`, govulncheck `v1.7.0`, gotestsum `v1.13.0`, а также точные версии
  uv-managed Python-инструментов для lint, type, security, YAML и отчётов.
  Единый Python 3.14.7 `uv.lock` заменяет pip-bootstrap
  и независимые tool-окружения в CI и development-образах.
  Containerlab устанавливается из проверенных архивов `v0.79.0`; CI использует
  Docker Compose `v5.5.0` как checksum-pinned Go provider для
  `podman compose`, Syft `v1.51.0` и GoReleaser `v2.18.0`.
- Изолированный граф lint-инструментов использует MIT-licensed revision
  `github.com/tenntenn/text/transform`, не изменяя runtime-зависимости.
  Dependabot теперь отслеживает корневой Go module и module в `tools/`. CI
  публикует раздельные runtime/tools vulnerability JSON и CycloneDX SBOM.
- Машиночитаемый dependency inventory теперь связывает все 40 registry packages
  из `uv.lock` с license evidence точного PyPI release и artifact hash, а также
  хранит per-version license evidence для всех 21 объявленных инструментов.
  Build commit и source checksum больше не классифицируются как инструменты.
  Каждая принятая или сохранённая запись теперь содержит явное исключение с
  датой пересмотра для каждой незакрытой блокирующей оценки релиза.
- GitHub Actions обновлены до актуальных релизов и immutable commit SHA.
  Interop/integration peers используют digest-pinned FRR `10.7.0`, GoBGP
  `v3.37.0`, ExaBGP `5.0.13`, Prometheus `v3.14.0` и Grafana `13.2.0`.
  Переход на GoBGP v4 остаётся отдельной несовместимой миграцией для v1.0.0.
- OSV Scanner обновлён с `v2.3.5` до `v2.5.1`; его Go analysis stack
  поддерживает синтаксис Go 1.27.
- Временное исключение для GoBGP v3 NEXT_HOP denial-of-service продлено только
  до 2026-09-30; переход на v4 остаётся отслеживаемым исправлением.
- Security fixes обновляют gRPC до `v1.83.2`, `moby/go-archive` до `v0.3.3`,
  `klauspost/compress` до `v1.19.2` и `x/mod` до `v0.40.0`. Исключение для
  module-only advisory `x/crypto/openpgp` ограничено по времени: этот пакет
  отсутствует в build graph.

### Исправлено

- Follow-up независимого ревью v0.6.2 защищает lifecycle вендорной
  Containerlab-лаборатории lock, run labels и receipts с exact ID; создаёт
  evidence core/BGP в приватных уникальных каталогах; читает digest-pinned
  образ FRR из tracked topology; требует все переименованные benchmark stages
  на обеих сторонах PR; удаляет неподтверждённые заявления об изоляции
  scheduler и отсутствии пауз GC; сохраняет авторитетный RFC support status
  только в compliance matrix.
- На metrics и ConnectRPC servers ограничено количество повторяющихся HTTP
  header values; parser-level test проверяет ответ HTTP 431 на 129-е значение.
- Vulnerability allowlist теперь fail closed проверяет scanner, package и
  reachable symbol; module-only exception не может разрешить import
  затронутого пакета.
- Удалены устаревшие эксперименты `goroutineleakprofile` и `noswissmap`,
  отсутствующие в Go 1.27.

### Удалено

- Удалены заброшенный пир aiobfd, его зависимость bitstring и repository-owned
  Python benchmark service со сравнениями. Cross-language отчёты теперь
  содержат результаты GoBFD, FRR-style C и BIRD-style C.
- Каталог `.archive/` удалён из репозитория. Sprint planning records,
  cleanup plan и промо-черновики больше не трекаются. Каталог остаётся
  в `.gitignore`, чтобы maintainer мог держать локальные scratch-файлы
  без риска снова закоммитить их в git. Содержимое сохранено в истории
  коммитов (`git log -- .archive/`).

## [0.6.1] - 2026-05-09

### Исправлено

- Обновлена пиннинг-версия `github.com/golangci/golangci-lint/v2` с
  `v2.11.4` до `v2.12.1` в `go.mod`. Релизный workflow вызывает
  `go tool golangci-lint`, который использует именно эту пиннинг-версию;
  v2.11.4 не знала линтера `gomodguard_v2`, добавленного в `v0.6.0`,
  из-за чего release CI на теге `v0.6.0` падал на шаге Lint.

## [0.6.0] - 2026-05-09

### Добавлено

- gRPC сервис `EchoService` (RFC 9747) с `AddEchoSession`,
  `DeleteEchoSession` и `ListEchoSessions`. Сервер валидирует
  положительный `TxInterval` и `DetectMultiplier` в диапазоне
  `[1, 255]`. Зарегистрирован на HTTP/2 ConnectRPC mux демона по пути
  `/bfd.v1.EchoService/...` и анонсируется через `grpc.health.v1`.
- gRPC сервис `MicroBFDService` (RFC 7130) с `AddMicroBFDGroup`,
  `DeleteMicroBFDGroup` и `ListMicroBFDGroups`. Сервер проверяет
  инвариант RFC 7130 `1 <= min_active_links <= len(member_links)`.
- Подкоманды `gobfdctl echo {add, list, delete}` и
  `gobfdctl micro {add, list, delete}` с форматами вывода table,
  JSON и YAML.
- Roadmap-документ `docs/en/roadmap.md` с waterfall-планом спринтов
  S12-S20 до версии `v1.0.0`.
- End-to-end цели `make e2e-core`, `make e2e-routing`, `make e2e-rfc`,
  `make e2e-overlay`, `make e2e-linux` и `make e2e-vendor` с Podman-only
  выполнением, packet captures и стандартизованными артефактами.
- `make e2e-help` и `make gopls-check` для покрытия E2E build tags.
- Podman-only путь `make interop-clab` для vendor NOS interop с Arista cEOS
  и FRRouting IPv4/IPv6 BFD evidence.
- Профили vendor NOS на публичных образах: Arista cEOS, Nokia SR Linux,
  SONiC-VS и VyOS. Cisco XRd остаётся opt-in и operator-provided.
- Worktree-безопасная автоматизация development Compose: generated container
  names, `COMPOSE_PROJECT_NAME`, `make dev-project`, `make dev-ps`.
- Конвейер GoReleaser snapshot собирает OCI-образы `linux/amd64` и
  `linux/arm64` на Debian trixie и Oracle Linux 10, deb/rpm пакеты и
  Syft SBOMs.
- Vendored `buf/validate/validate.proto` из `bufbuild/protovalidate`
  `v1.2.0`: `buf lint` работает без подключения к Buf Schema Registry.
- Reusable GitHub Actions Podman installer с pin `podman-compose` `1.5.0`.
- Workflow E2E evidence в GitHub Actions с PR-safe, nightly и manual vendor
  gates и 30-дневным сроком хранения артефактов.

### Изменено

- Документация реорганизована: канонические reference-документы только в
  `docs/en/01..16-*.md`; sprint-планы перенесены в `.archive/sprints/`;
  введены architecture decision records в `docs/en/adr/`; вспомогательные
  справочники в `docs/en/reference/`. RU-зеркало эквивалентно EN
  файл-в-файл.
- Унифицирован шаблон шапки в `docs/en/12..16-*.md`: badge-row,
  декларативный summary, заголовок Table of Contents уровня h2.
- Добавлены `doc.go` для всех пакетов `internal/` (`bfd`, `gobgp`,
  `sdnotify`, `version`); существующие inline package-комментарии удалены
  из логических файлов.
- `.golangci.yml` мигрирован с устаревшего `gomodguard` на `gomodguard_v2`
  с новой схемой module-list.

### Безопасность

- Toolchain Go поднят до `1.26.3` в `go.mod`, dev-контейнере,
  runtime-контейнерах и GitHub Actions. Закрывает
  [GO-2026-4986 / CVE-2026-39820](https://pkg.go.dev/vuln/GO-2026-4986)
  в `html/template`.

### Исправлено

- Новая Make-цель `dev-ensure` пересобирает dev-контейнер, если его
  bind-mount source не совпадает с `$(CURDIR)`. Устраняет ошибку
  `crun: getcwd: Operation not permitted`, всплывавшую после удаления
  worktree без пересборки dev-контейнера.
- Цель `lint-spell` больше не ссылается на документы, перенесённые в
  `.archive/sprints/`; теперь линтит каноничный `roadmap.md`.
- `lint-yaml` исключает каталоги `.archive/` и `.serena/`.
- В dev-контейнере `golangci-lint` поднят до `v2.12.1`, чтобы
  соответствовать миграции на `gomodguard_v2` на хосте.
- Linux rtnetlink interface monitor shutdown теперь ограничен receive
  timeout, поэтому cancellation завершается детерминированно, даже если
  закрытие netlink file descriptor не прерывает receive syscall сразу.
- Authenticated BFD sessions теперь сериализуют authentication section в
  cached transmit packet перед отправкой, поэтому declarative RFC 5880 auth
  sessions могут устанавливаться с peers, требующими authentication.
- Устранены `goconst` нарушения: литералы `single-hop`/`multi-hop`
  вынесены в константы `cmd/gobfdctl/commands`, `_uuid` -- в
  `internal/netio.ovsdbUUIDColumn`.
- Удалены устаревшие `//nolint:gosec` директивы из `internal/bfd/manager.go`,
  `internal/netio/rawsock_linux.go`, `internal/netio/sender.go` и
  `test/interop-rfc/echo-reflector/main.go`.

## [0.5.2] - 2026-05-01

### Исправлено

- Восстановлен каноничный текст лицензии Apache-2.0, чтобы pkg.go.dev мог
  определить лицензию модуля и показывать документацию пакетов.

## [0.5.1] - 2026-05-01

### Добавлено

- Badge pkg.go.dev в README и package documentation для command packages:
  `gobfd`, `gobfdctl`, `gobfd-haproxy-agent` и `gobfd-exabgp-bridge`.

### Изменено

- PR benchmark comparison теперь запускает только стабильные hot-path
  benchmarks и использует явный timeout для `go test`.

## [0.5.0] - 2026-05-01

### Добавлено

- Repository governance и community-health files:
  `CODE_OF_CONDUCT.md`, `SUPPORT.md`, `GOVERNANCE.md`, `MAINTAINERS.md`,
  `.github/CODEOWNERS`, `.github/pull_request_template.md`, issue forms и
  `.github/repository-settings.md`.
- Аудит консистентности кодовой базы
  `docs/ru/codebase-consistency-audit.md`, сверяющий README/docs/API/CLI/config
  с фактической реализацией и независимой production-применимостью в сетевых
  сценариях.
- Linux rtnetlink monitor интерфейсов для событий `RTM_NEWLINK` /
  `RTM_DELLINK`, с немедленным переводом BFD-сессий на отказавшем интерфейсе
  в `Down` / `Path Down`.
- Исследовательская заметка S4 по Linux netlink и eBPF с обоснованием выбора
  rtnetlink для мониторинга состояния интерфейсов.
- Каноничный поэтапный план разработки `docs/ru/implementation-plan.md`,
  согласованный с Keep a Changelog, SemVer, Conventional Commits,
  Compose Specification, Containerfile, `.containerignore` и containers.conf.
- Podman-only проверки документации: `make lint-md`, `make lint-yaml`,
  `make lint-spell`, `make lint-docs` и `make lint-commit`.
- Конфигурации `.containerignore`, Markdown lint, YAML lint, cspell и
  commitlint на уровне репозитория.
- CI-задачи для проверки документации и Conventional Commit в заголовках pull
  request.
- CI spell-check paths теперь используют каноничные planning docs из
  `docs/en/` и community-health files.
- Gate `make gopls-check` на базе `gopls v0.21.1` в Podman dev-контейнере.
- Декларативное подключение аутентификации RFC 5880 для BFD-сессий из YAML,
  включая валидацию статического хранилища ключей и отображение типа auth в
  API/snapshot сессии.
- Поля управления ключами RFC 5880 в gRPC `AddSession`: `auth_type`,
  `auth_key_id` и `auth_secret`.
- Флаги аутентификации `gobfdctl session add`: `--auth-type`,
  `--auth-key-id` и `--auth-secret`.
- Словарь типов сессий публичного API для RFC 9747 Echo, RFC 7130 Micro-BFD,
  RFC 8971 VXLAN и RFC 9521 Geneve.
- Production security policy для BFD authentication, экспозиции ConnectRPC,
  GoBGP TLS/localhost границ, контейнерных привилегий и ownership
  vulnerability gate.
- Заметка о применимости Micro-BFD, VXLAN BFD и Geneve BFD в Linux:
  `docs/ru/linux-advanced-bfd-applicability.md`.
- Generic production runbooks в `docs/en/16-production-runbooks.md` и
  `docs/ru/16-production-runbooks.md` для Kubernetes, BGP failover,
  Prometheus alerts, packet verification и открытых production gaps.
- Runbook FRR/GoBGP BGP fast-failover с RFC packet checks,
  troubleshooting и optional public Arista EOS verification notes.
- Micro-BFD actuator hook и guarded policy layer `netio.LAGActuator` для Linux
  LAG enforcement.
- Owner-aware конфигурация `micro_bfd.actuator` и daemon dry-run wiring для
  kernel bond, OVS и NetworkManager backend-ов Micro-BFD enforcement.
- Linux kernel-bond backend для Micro-BFD enforcement, который пишет RFC 7130
  remove/add действия через bonding sysfs при явном `backend: kernel-bond` и
  `owner_policy: allow-external`.
- OVS backend для Micro-BFD enforcement, который запускает команды
  `ovs-vsctl del-bond-iface` и `ovs-vsctl add-bond-iface` при явном
  `backend: ovs` и `owner_policy: allow-external`.
- OVSDB API research, фиксирующий OVSDB JSON-RPC как native OVS integration
  path и `libovsdb` как предпочтительный Go route для следующего backend.
- Native OVSDB backend для Micro-BFD enforcement с `backend: ovs`, который
  использует `libovsdb` transactions против `Port.interfaces` и настраиваемый
  `micro_bfd.actuator.ovsdb_endpoint`.
- NetworkManager D-Bus backend для Micro-BFD enforcement с `backend:
  networkmanager`, который использует `GetDeviceByIpIface`,
  `ActiveConnection`, `DeactivateConnection`, `AvailableConnections`,
  `GetSettings` и `ActivateConnection` для управления NM-owned bond port
  profiles.
- Модель overlay backend для VXLAN/Geneve с явным ownership `userspace-udp`
  и зарезервированными именами `kernel`, `ovs`, `ovn`, `cilium`, `calico` и `nsx`.
- Каноничная структура документации: English sources в `docs/en/`, русский
  перевод в `docs/ru/`, в корне `docs/` только глобальный индекс
  `docs/README.md`.
- Русские переводы S8 planning, consistency audit, Linux advanced BFD,
  Linux netlink/eBPF и OVSDB API research документов.

### Изменено

- Documentation style теперь использует декларативные status tables,
  official standards, RFCs, primary vendor/library references и не содержит
  internal validation process artifacts в published documents.
- RFC compliance docs, примеры конфигурации и комментарии кода теперь отделяют
  реализованное обнаружение Micro-BFD от будущего Linux bond/team/OVS
  enforcement, а также описывают ограничения ownership userspace-сокетов
  VXLAN/Geneve для kernel, OVS, Cilium, Calico и NSX dataplane.
- S7.1 разделён на неразрушающий actuator config wiring, explicit
  kernel-bond enforcement, transitional OVS CLI fallback, native OVSDB backend
  и NetworkManager D-Bus backend.
- Overlay sender reconciliation теперь использует runtime VXLAN/Geneve backend,
  который уже обслуживает receiver, без повторного bind на UDP 4789/6081.
- `backend: ovs` теперь выбирает native OVSDB implementation; прежний
  `OVSLAGBackend` остаётся explicit CLI fallback type.
- Roadmap S7 теперь нацелен на независимые production integration assets, без
  привязки к site-specific контуру применимости.
- Kubernetes integration manifests теперь используют согласованные app labels,
  named ports, Linux node selection, TCP readiness/liveness probes и
  host-network DNS policy.
- Observability alert rules теперь отделяют "нет активных
  сконфигурированных сессий" от реального BFD transition Up-to-Down и
  используют flapping detection по счётчику transitions, совпадающему с
  экспортируемыми метриками GoBFD.
- `make gopls-check` теперь проверяет Linux target через `go list`, включает
  проектные build tags и падает при любых diagnostics `gopls check`, вместо
  прежнего вывода diagnostics с exit code 0.
- RFC-статус в README теперь согласован с подробными RFC compliance документами
  для Echo, Micro-BFD, VXLAN, Geneve, Unsolicited BFD, common intervals и large
  packets.
- `make all` теперь включает проверки документации; `make verify` является
  каноничным регулярным gate для сборки, тестов, линтеров, proto lint и аудита
  уязвимостей.
- Makefile-цели interop Go tests теперь выполняются через Podman dev-контейнер,
  а не через локальный Go toolchain.
- Dev-контейнер теперь включает Node.js и Python-анализаторы документации, а
  доступ к Podman socket исправлен через `security_opt: label=disable`.
- CI workflow теперь использует read-only token policy на уровне workflow и
  именованные задачи, согласованные с локальными quality gates.
- Go tools в CI и release workflow теперь запускаются через Go `tool`
  directives, записанные в `go.mod`/`go.sum`: `gotestsum`, `benchstat` и
  `golangci-lint`; Node и Python analyzer installs закрепляют версии
  `markdownlint-cli2`, `cspell`, `commitlint`, `yamllint` и `junit2html`, а
  также используют package-manager controls согласно supply-chain scanners.
- Форматирование `gobfdctl` list/show/event теперь отображает advanced session
  families вместо `unknown`.
- Политика покрытия SonarCloud и Codecov теперь исключает command entrypoints
  и host-network integration boundaries, проверяемые build, lint, security и
  system/container checks.

### Исправлено

- Graceful drain теперь проводит `SetAdminDown` через control channel сессии,
  когда session goroutine запущена: goroutine-confined cached state остаётся
  согласованным с atomic state, а отправляемый control packet несёт
  `AdminDown` / `DiagAdminDown`.
- Путь приема RFC 9747 Echo теперь принимает только looped-back пакеты с
  TTL/Hop Limit 254, сохраняя проверку TTL 255 для single-hop BFD.
- RFC interop packet capture теперь включает UDP 3785 Echo-пакеты.
- Создание сессии теперь отклоняет аутентификацию без хранилища ключей вместо
  panic во время подписи cached packet.
- Проверка hash-auth теперь отклоняет отсутствие raw wire bytes вместо panic,
  если legacy/internal caller передал только разобранный пакет.
- Аутентифицированные сессии теперь сбрасывают receive sequence window после
  2x Detection Time без валидных пакетов, а пакеты с ошибкой auth больше не
  обновляют `LastPacketReceived` и `PacketsReceived`.
- gRPC `AddSession` теперь отклоняет неполный или неожиданный auth key material
  вместо тихого создания неаутентифицированной сессии.
- gRPC `AddSession` теперь отклоняет распознанные transport-specific типы
  сессий до появления dedicated API для Echo, Micro-BFD, VXLAN и Geneve.
- Записи vulnerability allowlist теперь требуют owner, expiry, reason и
  mitigation metadata; expired entries ломают audit gate.

## [0.4.0] - 2026-02-24

### Добавлено

- Полное тестовое покрытие `cmd/gobfd/main.go` -- 32+ table-driven тестов для `configSessionToBFD`, `buildUnsolicitedPolicy`, `configEchoToBFD`, `configMicroBFDToBFD`, `buildOverlaySessionConfig`, `loadConfig`, `newLoggerWithLevel`.
- Fuzz-тесты для overlay-кодеков: `FuzzVXLANHeader`, `FuzzGeneveHeader`, `FuzzInnerPacket` с round-trip и raw-input вариантами для ненадёжного сетевого ввода.
- Бенчмарки overlay-кодеков: `BenchmarkBuildInnerPacket`, `BenchmarkStripInnerPacket`, `BenchmarkVXLAN/GeneveHeaderMarshal/Unmarshal` (0 аллокаций/оп).
- Тестовое покрытие `internal/version` -- формат `Full()`, значения по умолчанию.
- Тестовое покрытие `gobfd-haproxy-agent` -- конкурентность `stateMap`, `handleAgentCheck` через `net.Pipe()`, `loadConfig`, `envOrDefault`.
- Тестовое покрытие `gobfd-exabgp-bridge` -- `handleStateChange` с перехватом stdout, `envOrDefault`.
- Бенчмарки масштабирования сессий: `BenchmarkManagerCreate100/1000Sessions`, `BenchmarkManagerDemux1000Sessions` (проверка O(1) демультиплексирования), `BenchmarkManagerReconcile`.
- Настраиваемые буферы сокетов через `socket.read_buffer_size` и `socket.write_buffer_size` (по умолчанию 4 МиБ каждый) для `SO_RCVBUF`/`SO_SNDBUF` на слушателях и отправителях.
- `os.Root` для безопасного доступа к файлам конфигурации в `config.Load` и `gobfd-haproxy-agent` `loadConfig` (защита Go 1.26 от обхода пути).
- `GOEXPERIMENT=goroutineleakprofile` в dev-контейнере для профилирования утечек горутин в runtime.
- HTTP-endpoint `runtime/trace.FlightRecorder` для посмертной отладки.
- Комментарии к PR с результатами бенчмарков в CI через `actions/github-script`.
- Пакет `internal/sdnotify` вместо внешней зависимости `go-systemd`.
- Тесты config, server, netio и интеграции GoBGP (Sprint 1 quality foundation).

### Изменено

- Версия golangci-lint зафиксирована на `v2.1.6` в CI и release воркфлоу (было `@latest`).
- Добавлен флаг `-race` в SonarQube тестовый воркфлоу для обнаружения гонок данных.
- CI-бенчмарки расширены с `./internal/bfd/` до `./...` для покрытия overlay-кодеков.
- Замена `errors.As` на `errors.AsType[T]()` Go 1.26 в тестах сервера для типобезопасного сопоставления ошибок.
- 15 тестов с таймерами конвертированы в `testing/synctest` для детерминированного выполнения с виртуальным временем.
- Замена внешней зависимости `go-systemd` на `internal/sdnotify` (ноль внешних зависимостей для systemd notify).

## [0.3.0] - 2026-02-24

### Добавлено

- RFC 7419: поддержка общего интервала для согласования таймеров BFD-сессий.
- RFC 9468: незапрашиваемый режим BFD для бессессионных приложений с пассивным слушателем.
- RFC 9747: неаффилированная функция эхо BFD с приёмником и рефлектором эхо-пакетов.
- RFC 7130: Micro-BFD для LAG-интерфейсов с посессионным мониторингом участников и агрегированным состоянием.
- RFC 8971: BFD для VXLAN-туннелей с обработкой пакетов с учётом оверлея.
- RFC 9521: BFD для Geneve-туннелей с инкапсуляцией option-C.
- RFC 9384: BGP Cease NOTIFICATION подкод 10 (BFD Down) через интеграцию с GoBGP.
- Скрипт подготовки вендорной interop-лаборатории (`test/interop-clab/bootstrap.py`): автоматическая подготовка образов для Nokia SR Linux, SONiC-VS, FRRouting, VyOS, Arista cEOS, Cisco XRd.
- RFC-специфичный набор interop-тестов (`test/interop-rfc/`): выделенные тесты для незапрашиваемого BFD, функции эхо и BGP Cease notification.
- Поддержка вендорного interop Cisco XRd с конфигурацией XR и обработкой лимитов PID.
- Улучшения interop SONiC-VS с надёжным скриптом конфигурации BGP/BFD.

### Изменено

- Вендорный interop `run.sh` корректно пропускает вендоров с ошибкой инициализации вместо прерывания.

## [0.2.0] - 2026-02-23

### Добавлено

- Тестирование BFD с двойным стеком IPv6 в наборе вендорных interop-тестов (RFC 5881 Section 5): Arista cEOS, Nokia SR Linux, FRRouting с ULA-адресами fd00::/8 и /127-префиксами по RFC 6164.
- Интеграция SonarCloud для непрерывного анализа качества кода.
- Интеграция Codecov для отслеживания покрытия тестами.
- Рабочие процессы CodeQL и gosec SARIF для глубокого анализа безопасности.
- Конфигурация Dependabot для автоматического обновления зависимостей (Go, Docker, GitHub Actions).
- Руководство по ведению changelog (docs/en/10-changelog.md, docs/ru/10-changelog.md).
- Сканирование уязвимостей `osv-scanner` в CI и Makefile (`make osv-scan`).
- Форматировщики `gofumpt` и `golines` (max-len: 120) в golangci-lint.
- Полноцикловые interop-тесты BGP+BFD: GoBFD+GoBGP <-> FRR, BIRD3, ExaBGP (3 сценария с проверкой маршрутов).
- Вендорные BFD interop-тесты Containerlab: Nokia SR Linux, FRRouting, Arista cEOS (доступны); Cisco XRd, SONiC-VS, VyOS (определены, пропуск при отсутствии образа).
- Поддержка Arista cEOS 4.35.2F: `start_arista_ceos()` с 8 обязательными переменными окружения, `wait_arista_ceos()` проверка загрузки, BFD через BGP.
- Исправление таймеров BFD Nokia SR Linux: перезапуск субинтерфейса после коммита конфигурации для согласования на 300 мс.
- Интеграция netlab задокументирована как перспективное направление для тестирования на VM вендоров.
- Пример интеграции: GoBFD + GoBGP + FRR (быстрое переключение BGP с отзывом маршрутов).
- Пример интеграции: GoBFD + HAProxy (agent-check для проверки здоровья бэкенда с переключением менее секунды).
- Пример интеграции: GoBFD + Prometheus + Grafana (наблюдаемость с 4 правилами алертинга).
- Пример интеграции: GoBFD + ExaBGP (anycast-анонсирование сервиса через BFD-управляемый process API).
- Пример интеграции: GoBFD DaemonSet в Kubernetes (k3s с GoBGP sidecar и host networking).
- Новый бинарник: `gobfd-haproxy-agent` — мост agent-check HAProxy для мониторинга здоровья через BFD.
- Новый бинарник: `gobfd-exabgp-bridge` — мост process API ExaBGP для управления анонсами маршрутов через BFD.
- tshark-сайдкар для верификации пакетов во всех интеграционных стеках.
- Документация по интеграциям (docs/en/11-integrations.md, docs/ru/11-integrations.md).
- Цели Makefile для всех примеров интеграции (`int-bgp-failover`, `int-haproxy`, `int-observability`, `int-exabgp-anycast`, `int-k8s`).
- Отображение версии (`--version`) для всех бинарников с хешем коммита и датой сборки.
- Общий пакет версии (`internal/version`) с инжекцией через ldflags.
- Инжекция версии в Makefile, CI, GoReleaser и всех Containerfile.

### Изменено

- `make build` теперь инжектирует версию, хеш коммита и дату сборки через ldflags для всех 4 бинарников.
- Замена `c-bata/go-prompt` на `reeflective/console` для интерактивной оболочки.
- Расширение golangci-lint с 39 до 68 линтеров со строгой конфигурацией, ориентированной на безопасность.
- Разделение CI-воркфлоу на параллельные задачи (build-and-test, lint, vulnerability-check, sonarcloud, buf).
- Улучшение воркфлоу релиза для извлечения release notes из CHANGELOG.md.
- Переименование метрики Prometheus `gobfd_bfd_sessions_total` в `gobfd_bfd_sessions` (исправление конвенции).

## [0.1.0] - 2026-02-21

### Добавлено

- Кодек BFD Control-пакетов с round-trip fuzz-тестированием.
- Табличная FSM, соответствующая RFC 5880 Section 6.8.6.
- Пять режимов аутентификации: Simple Password, Keyed MD5/SHA1, Meticulous MD5/SHA1.
- Абстракция raw-сокетов для Linux (UDP 3784/4784, TTL=255 GTSM).
- Менеджер сессий с аллокацией дискриминаторов и detection timeout.
- Сервер ConnectRPC/gRPC с перехватчиками восстановления и логирования.
- CLI `gobfdctl` с командами Cobra и интерактивной оболочкой.
- Интеграция GoBGP с демпфированием осцилляций BFD (RFC 5882 Section 3.2).
- Коллектор метрик Prometheus и дашборд Grafana.
- Интеграция systemd (Type=notify, watchdog, SIGHUP горячая перезагрузка).
- Конфигурация YAML с наложением переменных окружения (koanf/v2).
- Фреймворк interop-тестов на 4 пирах (FRR, BIRD3, aiobfd, Thoro/bfd).
- Пакеты Debian и RPM через GoReleaser nfpms.
- Docker-образ, публикуемый в ghcr.io/dantte-lp/gobfd.
- CI-пайплайн: сборка, тесты, линтер, govulncheck, buf lint/breaking.
- Двуязычная документация (английский и русский).

[Не выпущено]: https://github.com/dantte-lp/gobfd/compare/v0.6.4...HEAD
[0.6.4]: https://github.com/dantte-lp/gobfd/compare/v0.6.3...v0.6.4
[0.6.3]: https://github.com/dantte-lp/gobfd/compare/v0.6.2...v0.6.3
[0.6.2]: https://github.com/dantte-lp/gobfd/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/dantte-lp/gobfd/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/dantte-lp/gobfd/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/dantte-lp/gobfd/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/dantte-lp/gobfd/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/dantte-lp/gobfd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/dantte-lp/gobfd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
