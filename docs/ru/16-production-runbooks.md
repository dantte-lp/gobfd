# Регламент: перечитывание конфигурации (Linux systemd)

Процедура заменяет декларативную конфигурацию одного управляемого Linux host с поставляемым `gobfd.service` и выведена из кода. Production execution/elapsed-time receipt отсутствует. Предпосылки: принятые binaries/unit, root shell, исправный service, защищённый кандидат `/etc/gobfd/gobfd.candidate.yml` и независимый доступ восстановления.

## 0. Правила без исключений

1. Согласовать один host и явный набор peers/consumers. Перед routing/auth changes обеспечить независимую консоль/доступ.
2. Записать точные binary/source identities и принятую config до apply. Не помещать secrets в logs/shared artifacts.
3. Классифицировать поля по [startup contract](03-configuration.md#перечитывание-sighup). Auth сохранённой сессии и startup fields требуют maintenance restart.
4. Сохранить принятый binary/package для полного rollback; здесь меняется только config.
5. Остановиться при rejected candidate, pending reconciliation, peer divergence или consumer forwarding failure. Успешная отправка сигнала не доказывает приёмку.

## 1. Предварительные проверки

Выполнять в одном root shell; последующие блоки используют `backup_dir`.

```sh title="reload-baseline.sh" showLineNumbers
backup_dir=$(mktemp -d /var/backups/gobfd.XXXXXXXX)
chmod 0700 "$backup_dir"
install -m 0600 /etc/gobfd/gobfd.yml "$backup_dir/gobfd.yml"
cmp /etc/gobfd/gobfd.yml "$backup_dir/gobfd.yml"
gobfd -version
gobfdctl version
systemctl show gobfd -p ActiveState -p SubState -p MainPID -p NRestarts
systemctl cat gobfd
stat -c '%a %U %G %n' /run/gobfd/gobfd.sock
gobfdctl --format json session list > "$backup_dir/sessions-before.json"
gobfdctl --format json capabilities > "$backup_dir/capabilities-before.json"
journalctl -u gobfd --since '-2 min' --no-pager
test -s /etc/gobfd/gobfd.candidate.yml
test -s "$backup_dir/gobfd.yml"
printf '%s\n' "$backup_dir"
```

Продолжить только после успешного сравнения backup, active/running service, исправных ожидаемых sessions/consumers, корректных прав socket и принадлежности candidate этой операции. Flag `-check` отсутствует. До apply проверить полный YAML/default/environment effect; сравнивать secrets только в защищённом процессе. Сохранить backup path в закрытом change record.

## 2. Установка и перечитывание

### 2.1 Перечитываемая конфигурация

```sh title="reload-apply.sh"
install -o root -g gobfd -m 0640 /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  cmp /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  systemctl reload gobfd
journalctl -u gobfd --since '-2 min' --no-pager
gobfdctl --format json session list > "$backup_dir/sessions-after.json"
```

Продолжить только при совпадении файла, converged reconciliation без rejected/pending resources и ожидаемом состоянии затронутых sessions. Совместимое изменение таймера сохраняет discriminator; снятие config claim может оставить API-owned сессию.

### 2.2 Startup-owned изменения

Использовать согласованный maintenance interval с ожидаемым перерывом sessions. Сначала выполнить раздел 1. При включённой интеграции GoBGP сохранить administrative states и журнал, доказывающий, какие peers отключил GoBFD. Ownership map хранится в памяти и теряется при restart. Не перезапускать во время отказа без принятого решения восстановления peers.

```sh title="gobgp-restart-baseline.sh"
gobgp neighbor > "$backup_dir/gobgp-before.txt"
journalctl -u gobfd --since '-1 hour' --no-pager > "$backup_dir/daemon-before.log"
gobgp neighbor 192.0.2.2
```

Адрес соответствует примеру конфигурации; подставить согласованного соседа. Administrative Down сам по себе не доказывает ownership GoBFD. До enable сохранить отдельное намерение оператора.

```sh title="restart-apply.sh"
install -o root -g gobfd -m 0640 /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  cmp /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  systemctl restart gobfd
systemctl show gobfd -p ActiveState -p SubState -p MainPID -p NRestarts
journalctl -u gobfd --since '-2 min' --no-pager
gobfdctl session list
```

Продолжить после запуска нового процесса, сходимости и восстановления peers/consumers. `daemon-reload` нужен при изменении unit/drop-in; он не перечитывает YAML демона и не перезапускает процесс. Источники: [ExecReload](../../deployments/systemd/gobfd.service), [systemctl manual](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html).

Если после BFD Up согласованный peer остаётся administratively disabled, доказать, что его отключил GoBFD и более поздняя операция не требует сохранить disable. Только после этого явно восстановить этот peer:

```sh title="gobgp-owned-peer-recovery.sh"
gobgp neighbor 192.0.2.2 enable
gobgp neighbor 192.0.2.2
gobgp global rib
```

Требуются Established, нужные routes и независимый forwarding probe. Не включать массово все disabled neighbors. Источники: [ownership в памяти](../../internal/gobgp/handler.go), [официальный GoBGP CLI](https://github.com/osrg/gobgp/blob/master/docs/sources/cli-operations.md).

## 3. Итоговая проверка

```sh title="reload-postcheck.sh"
gobfdctl --format json session list
gobfdctl --format json capabilities
systemctl show gobfd -p ActiveState -p SubState -p MainPID -p NRestarts
journalctl -u gobfd --since '-2 min' --no-pager
```

Сравнить закрытые before/after inventories: peer/local/interface, discriminator, state, diagnostics, negotiated timers. Проверить writer/read-only policy и доступность metrics. Измерить consumer route/backend behavior и независимый forwarding/application probe; management access недостаточно.

| Gate | Требуемый результат | Evidence |
| :--- | :--- | :--- |
| Конфигурация | Принята нужная генерация, нет pending resources | Закрытая выдержка journal |
| Сохранённая сессия | Ожидаемые identity/discriminator/negotiated state | Before/after JSON |
| Consumer | Нужное route/backend действие и восстановление | Consumer readback |
| Пересылка | Probe успешен на нужном пути | Внешний probe с временем |
| Ресурсы | Нет неожиданного restart/OOM, соблюдён budget | Service/cgroup/host measurement |

Отдельный BGP gate для управляемой disposable FRR topology:

```sh
make int-bgp-failover-testcontainers
```

Он проверяет withdrawal в пределах 20 секунд и restoration, не гарантирует 900 мс или production forwarding. Observability counterpart — `make int-observability-testcontainers` со scraping, alert firing и recovery. Не запускать пересекающиеся fixed-name manual labs одновременно. См. [квалификацию интеграций](11-integrations.md).

## 4. Откат

Восстановить backup в том же shell; при потере shell подставить записанный закрытый backup path.

```sh title="reload-rollback.sh"
test -s "$backup_dir/gobfd.yml" && \
  install -o root -g gobfd -m 0640 "$backup_dir/gobfd.yml" /etc/gobfd/gobfd.yml && \
  cmp "$backup_dir/gobfd.yml" /etc/gobfd/gobfd.yml && \
  systemctl reload gobfd
journalctl -u gobfd --since '-2 min' --no-pager
gobfdctl session list
```

Для startup-owned rollback заменить reload на `systemctl restart gobfd` и повторить раздел 3. После binary/package upgrade сначала восстановить точный принятый artifact по его процедуре.

Rollback восстанавливает desired config, но не uptime, прежний discriminator после recreation, transient withdrawals или историю consumer. API claims могут требовать owner-specific provisioning после restart. Сохранить evidence/backup до закрытия приёмки; cleanup — отдельная согласованная операция.

## 5. Типовые препятствия

Процедура не выполнена на production host. Ниже — определённые кодом failure checks, не вымышленные наблюдения инцидента.

| Симптом | Причина | Проверка/исправление |
| :--- | :--- | :--- |
| Reload успешен, настройки прежние | Rejected candidate/startup-field change | Прочитать поля отказа; восстановить или назначить restart |
| Сессия отсутствует | Pending address/interface/listener | Прочитать source receipt; восстановить binding и подтвердить сходимость |
| CLI не пишет по TCP | Anonymous/read-only principal | Использовать разрешённый socket/mTLS |
| BGP не действует | Несколько base sessions/native owner/API mismatch | Обеспечить одного owner и совместимый GoBGP |
| Удаление оставляет wire | Остался чужой principal/config claim | Освободить только нужного владельца |

## 6. Краткий справочник команд

```sh title="reload-cheat-sheet.sh"
# Read current session state.
gobfdctl session list
# Read preview maturity and local readiness.
gobfdctl capabilities
# Request reload of reloadable YAML.
systemctl reload gobfd
# Inspect daemon acceptance/rejection.
journalctl -u gobfd --since '-2 min' --no-pager
# Read service process state.
systemctl show gobfd -p ActiveState -p MainPID -p NRestarts
```

## Ссылки

- [Конфигурация](03-configuration.md): defaults, поля и restart boundary.
- [Развёртывание](06-deployment.md): artifacts, sandbox и Kubernetes.
- [Мониторинг](07-monitoring.md): знаменатели сигналов и alerts.
- [Безопасность](15-security.md): identity, secrets и actuator permissions.
- [BGP example](../../deployments/integrations/bgp-fast-failover/README.md): точная disposable topology/gate.
- [Vendor references](reference/vendor-bfd.md): Cisco, Arista, Juniper, Huawei и platform boundaries.
