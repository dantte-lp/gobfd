# ADR-0007: Фазы нагрузки и конечный учёт пакетов

| Поле | Значение |
|---|---|
| Статус | Принятая политика; runtime qualification отдельна |
| Дата | 2026-10-24 |

## Контекст

Независимые timers owner и peers могут остановить приём до конечного результата
последней успешной записи peer. Queue admission не является FSM commit;
окончание duration не подтверждает полноту учёта или latency acceptance.

## Решение

Использовать существующий Go-owned production-core workload и независимую модель
peer. После Up обоих участников публиковать общую будущую native monotonic epoch.
Каждый участник проверяет identity time namespace до перевода epoch в Go timer.
Приватные файлы в `os.Root` с проверенным владельцем ограничены 64 bytes;
публикация полных данных запрещает replacement, чтение отклоняет links и special
files. Conversion bracket не является независимой границей RX clock uncertainty.

Сначала зафиксировать measurement snapshot owner, затем остановить и join peers,
сохранить число фактических успешных записей и выполнить bounded drain owner.
Требовать равенство непересекающихся terminal outcomes:

```text
successful peer writes = owner committed + terminal rejections + observed kernel drops
```

Промежуточные accepted/queued stages не входят в сумму. Избыток terminal counts
завершает проверку ошибкой сразу, недостаток — по drain deadline с неполным
отчётом. Недостающие kernel overflow samples не дают основания подставлять
предполагаемые потери. Protocol success отделён от ledger settlement.

Сохранять frozen measurement и final stopped snapshots отдельно. Lifetime
counters включают handshake и drain; деление на запрошенную duration не
доказывает throughput этого окна. Снимки собираются последовательно; общая epoch
не доказывает одновременность наблюдений. Недостающие clock evidence остаются
unknown, а не нулевой uncertainty.

## Последствия

Команды без control directory сохраняют независимые timers и явные ограничения.
Успех fixture не квалифицирует reference load, полный daemon, clock accuracy или
stable delivery. Implementation acceptance принадлежит Beads
`gobfd-qj0.8.2.1.6.3.3`; reference distributions, regression policy, thread-pin A/B
и final wire qualification остаются отдельными. Компонентный результат не
обосновывает новый scheduler или kernel fast path.

## Источники

- [Границы workload и clock](../14-performance-analysis.md)
- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4)
- [Linux time namespaces](https://man7.org/linux/man-pages/man7/time_namespaces.7.html)
- [Linux socket overflow counters](https://man7.org/linux/man-pages/man7/socket.7.html)
- [Go filesystem operations внутри directory root](https://github.com/golang/go/blob/master/src/os/root.go)
