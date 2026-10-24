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

До запуска именованного owner profile сохранять `pids.current` и `pids.max`
каждого видимого non-root cgroup ancestor. Закреплённый baseline требует
дополнительного запаса задач: число сессий плюс принятый запас 64 задачи.
Отсутствующие или повреждённые observations и недостаточный запас отклонять до
создания сессий; вычитать текущее потребление из лимита без unsigned overflow.
Linux PID limits учитывают потоки и потребление descendants; `GOMAXPROCS` не
ограничивает создание потоков. Последовательная проверка не резервирует capacity
и не доказывает runtime peak. Сохранять консервативный baseline budget до его
пересмотра на основании отдельно измеренного thread-pin A/B.

Генерировать запрошенное CPU pressure внутри owner quota не более чем восемью
workers; число workers равно effective `GOMAXPROCS`. Подготовить workers до
общей epoch. Каждый ограниченный helper закрепляет свой поток только для
атрибуции `CLOCK_THREAD_CPUTIME_ID` и освобождает его при выходе. В фиксированных
duty windows по 100 ms запрашивать thread CPU time, равное заданному проценту
окна; последнее окно обрезать по концу measurement. Прекращать вычисления по wall
deadline окна и считать пропущенные budgets без переноса долга на следующие
окна. Отмена или ошибка clock останавливает и join workers до завершения owner
report. Границы measurement snapshot и packet drain сохраняются.

Сохранять раздельно requested duty, фактические worker CPU и wall time,
start/end lateness, число окон и пропущенных budgets. Worker CPU time включает
clock polling и runtime work этих helper threads. Наблюдения не доказывают
удержание host load на 80% и не заменяют CPU/throttling calibration на допущенных
reference resources. Scheduler settings и production session pins не меняются.

Явный вариант `--gc-pressure` добавляет один owner worker с той же эпохой,
сохраняя CPU, loss, blackhole и число сессий именованного профиля. В каждом
окне 100 ms он выделяет до 64 объектов по 64 KiB и удерживает не более 256
ссылок (16 MiB). Обращаться к каждой OS page, пропускать истекшие окна без
накопления долга и сохранять фактические allocation, skipped/truncated batch,
lateness и released-reference counts. Завершать worker до measurement freeze
и drain. Очистка ссылок не освобождает heap немедленно и не ограничивает RSS.
Не вызывать GC принудительно и не менять `GOGC`, `GOMEMLIMIT` или scheduler
settings. GC cycles и pause histograms относятся ко всему процессу;
приписывать их только allocation helper нельзя. Режим выключен по умолчанию;
завершённый fixture не подтверждает reference GC qualification.

Вариант `--scheduler-pressure`, выключенный по умолчанию, готовит четыре
runnable helper на effective `GOMAXPROCS`, не более 32 helpers. В каждом общем
окне 100 ms helper выполняет до 64 раундов прежнего CPU mixing primitive из
1024 итераций с последующим `runtime.Gosched`. Между раундами проверять отмену
и wall deadline; истекшие окна пропускать без накопления долга. Не закреплять
эти helpers за OS threads и не менять runtime settings. Сохранять фактические
rounds, yield calls, skipped/incomplete worker windows, checksum и phase
lateness; yield call не подтверждает OS context switch. При успехе и ошибке
завершать CPU, GC и scheduler helpers до measurement freeze и drain. Peers
передают запрос, но owner pressure не создают. Наблюдения scheduler, timer и
committed RX процесса отделены от калиброванных reference distributions;
неизвестная clock uncertainty сохраняется.

Вариант `--session-churn`, выключенный по умолчанию, последовательно меняет
одну допущенную сессию из явного manifest endpoints. Первый цикл начинается
через 250 ms после эпохи измерения; следующие используют период пять секунд
без повторения пропущенных циклов. Реальная последняя claim Manager
освобождается с завершением drain; затем выдерживается разрыв регистрации
200 ms. Та же wire identity создаётся заново, и наблюдаемый Up требуется до
конца measurement. AdminDown объявляет интервал не менее секунды; multiplier 3
требует notification drain не менее трёх секунд. Включённая fixture требует
не менее шести секунд. Цикл не начинается, если до конца осталось менее пяти
секунд; пропущенные завершающие циклы считаются отдельно. Ожидание Up ограничено
пятью секундами после создания и концом measurement; окно без реального recovery
завершается ошибкой. Подписка начинается до churn; Down-события setup/drain
исключаются из measurement-интервала. Потери notification-gap учитываются
консервативно. Слоты вне
Up пропускаются с отдельным счётчиком; их состояние не приписывается churn.
Независимые peers продолжают трафик во время разрыва и восстановления.

Один owner lock сериализует изменение inventory, Close и снимки membership
вместе с collector. После начала закрытия новые сессии отклоняются. Фиксированный
слот владеет только текущей sender lease; packet counters завершённых поколений
суммируются после join без сохранения каждого старого объекта. Записываются
старые/новые поколения, discriminators, source ports, времена фаз и фактическая
cardinality Manager/collector до удаления, во время отсутствия и после Up.
Начало вызова release и окончание drain ограничивают момент удаления из registry;
окончание drain не является моментом удаления.
Число known peers определяется взаимно однозначными session/demux registrations
Manager; active session gauge и сохранённые session/packet metric series
учитываются отдельно. Ограниченный журнал вмещает все пятисекундные циклы
24-часового профиля; дальнейшие пропуски считаются явно. Churn завершается до
measurement freeze; terminal ledger охватывает все поколения. Только RX
последнего поколения не позволяет учесть peer writes. Вариант использует
фиксированный manifest, не добавляет peer addresses и не квалифицирует рост
произвольных уникальных labels. Размерности named baseline сохраняются;
reference qualification не устанавливается.

На measurement end брать denominator Down journal из фактического retained counter
collector `Up`→`Down`, увеличиваемым до timestamp notification и enqueue.
Запускать один bounded consumer вместе с подпиской и сохранять чтение на setup,
measurement и shutdown. Сохранять не более `maxDownEvents` компактных Down
projections, оставляя монотонные timestamps до получения фактической фазы.
Считать все gaps и omissions. После завершения всех owner goroutines ожидать
consumer не более пяти секунд; на timeout отменять и завершать его. Фиксировать
measurement snapshot до stop/drain и journal join в controlled и uncontrolled
режимах; использовать его для histogram deltas. Считать каждую наблюдаемую notification,
разделяя counts до и после cutoff, но сохранять строки журнала только в
`[start,end)`. Требовать равенства final source total сумме обоих observed counts
и before-cutoff count <= cutoff source <= final source. Gaps, overflow,
некорректные counters, незакрытая подписка и несовпадение counts отклоняют
completeness. Последующие flaps не компенсируют пропущенное раннее событие;
полностью доставленные последующие flaps остаются за пределами измеренного
журнала. Не вычитать sampled setup history: пропущенные startup flaps
консервативно отклоняют этот контракт. Использовать production metrics без новых
hot-path fences или counters. Полнота журнала не классифицирует Down как
намеренный и не квалифицирует commit timestamps или clocks.

## Последствия

Команды без control directory сохраняют независимые timers и явные ограничения.
Успех fixture не квалифицирует reference load, полный daemon, clock accuracy или
stable delivery. Implementation acceptance принадлежит Beads
`gobfd-qj0.8.2.1.6.3.3`; reference distributions, regression policy, thread-pin A/B
и final wire qualification остаются отдельными. Компонентный результат не
обосновывает новый scheduler или kernel fast path.

## Источники

- [Общая workload epoch и протокол stop/drain](../../../test/internal/bfdload/control_phase_linux.go)
- [Конечный учёт owner](../../../test/internal/bfdload/controlled_owner_linux.go)
- [Допуск по PID headroom](../../../test/internal/bfdload/admission_linux.go)
- [CPU pressure и измерение thread CPU](../../../test/internal/bfdload/cpu_pressure_linux.go)
- [Ограниченный GC pressure](../../../test/internal/bfdload/gc_pressure_linux.go)
- [Ограниченный scheduler pressure](../../../test/internal/bfdload/scheduler_pressure_linux.go)
- [Границы workload и clock](../14-performance-analysis.md)
- [Реальный session churn и завершение recovery](../../../test/internal/bfdload/session_churn_linux.go)
- [Сериализованный inventory и завершение leases](../../../test/internal/bfdload/owner_churn_linux.go)
- [Сверка Down journal с source](../../../test/internal/bfdload/down_journal_linux.go)
- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4)
- [Linux time namespaces](https://man7.org/linux/man-pages/man7/time_namespaces.7.html)
- [Linux socket overflow counters](https://man7.org/linux/man-pages/man7/socket.7.html)
- [Go filesystem operations внутри directory root](https://github.com/golang/go/blob/master/src/os/root.go)
- [Linux cgroup v2 PID controller](https://docs.kernel.org/admin-guide/cgroup-v2.html#pid)
- [Go thread affinity](https://pkg.go.dev/runtime#LockOSThread)
- [Linux thread CPU clocks](https://man7.org/linux/man-pages/man3/clock_gettime.3.html)
