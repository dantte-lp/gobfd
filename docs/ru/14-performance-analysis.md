# Производительность и границы runtime-измерений

![Go](https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![FRR](https://img.shields.io/badge/FRR-bfdd-dc3545?style=for-the-badge)
![BIRD3](https://img.shields.io/badge/BIRD3-BFD-28a745?style=for-the-badge)
![Podman](https://img.shields.io/badge/Podman-Reproducible-892CA0?style=for-the-badge&logo=podman)

> Receive telemetry, учёт workload и границы ресурсов текущей реализации.
> Компонентные benchmarks и исторические результаты отделены от runtime
> qualification финального кандидата.

---

## Граница Linux Receive Telemetry

Committed-latency baseline отслеживается в `gobfd-qj0.8.2.1.6`.
Срез receive metadata включает `SO_TIMESTAMPNS_NEW` и `SO_RXQ_OVFL` на Linux
listeners при поддержке kernel. `ENOPROTOOPT` оставляет соответствующее
измерение недоступным, сохраняя работу BFD. Другие ошибки socket option
содержат её имя и прекращают создание listener. TTL и packet-info остаются
обязательными. Квалификация требует фактических samples: недоступность
не означает нулевую задержку или отсутствие потерь.
Ancillary buffer 128 байт вмещает timestamp, drops, TTL/Hop Limit
и destination/interface metadata для IPv4 и IPv6. `MSG_CTRUNC` возвращает
`ErrAncillaryTruncated` до demultiplexing.

`PacketMeta.KernelRXUnixNano` — сырой software `CLOCK_REALTIME` sample;
ноль означает отсутствие либо некорректные данные. Native-endian time64
принимается при нормализованных nanoseconds и результате в положительном
int64 Unix nanoseconds. Источник: [kernel timestamp ABI](https://www.kernel.org/doc/html/latest/networking/timestamping.html).

`SocketDrops` — cumulative uint32 sample из `SO_RXQ_OVFL`;
`SocketDropsKnown` указывает наличие корректного сообщения counter.
Kernel не передаёт нулевые drop messages. Sample описывает drops до постановки
этой datagram в очередь; aggregation должна учитывать wrap и последнюю
принятую datagram после остановки нагрузки. Источники:
[socket(7)](https://man7.org/linux/man-pages/man7/socket.7.html),
[`sock_recv_drops`](https://github.com/torvalds/linux/blob/master/net/socket.c).

Каждый Control listener владеет paired realtime/monotonic calibration.
Sample преобразуется в монотонное время через realtime offset от calibration;
значения до calibration, из будущего, вне числового диапазона либо без
monotonic reading исключаются. Наблюдаемая разность realtime и monotonic
elapsed должна оставаться в пределах 1 мс при receive и commit. Больший
наблюдаемый скачок отзывает calibration на весь срок жизни listener, включая
уже queued samples через общий atomic-флаг. Пересоздание listener задаёт
новую calibration; повторный запуск receiver с прежним listener отзыв не
снимает. Это исключает угадывание эпохи queued timestamps после скачка назад.

Граница определяет пригодность измерения, а не гарантирует точность меньше
миллисекунды. Скачок с возвратом между observations таким способом не
обнаруживается. Контракт Go: [monotonic clocks](https://pkg.go.dev/time#hdr-Monotonic_Clocks).
Clock outcomes не меняют приём пакетов и detection deadlines: они сохраняют
прежнее монотонное время принятого пакета.

Timing передаётся по значению через discriminator, peer и unsolicited admission.
`gobfd_bfd_control_rx_timing_total{outcome}` считает каждый нормально завершённый
Control owner packet по шести фиксированным outcomes: `valid`, `unavailable`,
`invalid`, `before_calibration`, `future`, `clock_step`. Только valid samples
попадают в `gobfd_bfd_control_rx_commit_seconds`. Validation refusal, enqueue,
queue refusal, abandonment и panic не являются completed samples; существующие
delivery denominators остаются отдельными. Peer labels отсутствуют у обеих метрик.
Overlay и legacy callers без kernel timing сообщают `unavailable`.

Реальные IPv4/IPv6 UDP tests дают ненулевые committed samples для обоих demux
paths. Real-collector owner benchmark ждёт каждый commit, но исключает UDP,
wire parsing и authentication; synthetic clock input применяется только для
измерения стоимости. Исходные 16 B/op и 2 allocs/op приходились на форматирование
адресов в metrics. Теперь активные зарегистрированные peer/local pairs хранят
четыре packet counter handles; прежний benchmark даёт 0 B/op и 0 allocs/op
в трёх локальных запусках. Счётчик регистраций сохраняет общий cache до последней
сессии. В cache остаются только активные пары; исторические CounterVec series
сохраняются, незарегистрированные callers используют прежний vector lookup.
Регистрация публикует нулевые packet/auth counters до первого обновления.
Это не квалификация lifetime series retention либо полного UDP pipeline.
Before/after evidence: `reports/collector-owner-20261022/`.
Bounded CPU/GC/loss distributions, calibrated regression policy, supported scale
и финальная квалификация остаются в родительской задаче.

## Граница таймерных измерений

Control и Echo owner loops сохраняют фактический timestamp из `time.Timer.C`.
Go [sendTime](https://github.com/golang/go/blob/master/src/time/sleep.go)
возвращает отложенный channel timestamp к запланированному моменту firing.
Обычные runtime readings используют монотонную арифметику Go; network
realtime samples в этих измерениях не участвуют.

`dispatch` измеряет время от scheduled firing до обработки owner. TX `effect`
фиксируется после успешного `SendPacket`, до последующего Poll bookkeeping;
это не обещание wire/peer arrival. Detection `effect` фиксирует публикацию
перехода в Down по timeout до logs, notifications и отправки объявления.
Deadline, jitter, suppression и FSM actions сохраняются.

Четыре фиксированных timer kinds дают 20 counter series и восемь histograms
для valid стадий dispatch/effect. Zero/future invalid timestamps остаются
отдельными outcomes и не входят в valid latency histograms. `no_effect`
объединяет нормальную обработку без полезного send/transition: policy
suppression, send failure и idle detection. Это не счётчик потерь пакетов.
Panic или незавершённый handler могут оставить dispatch без effect/no-effect.
Действия вне timer event не учитываются. False-Down rate, supported scale
и обязательные нагрузочные распределения требуют actual UDP workload
и accounting из Beads; эти метрики сами по себе их не подтверждают.

## Граница измерений потерь сокета

Linux [socket cmsgs](https://github.com/torvalds/linux/blob/a1c2b1da93f5f612aa7a33190c4900c98e51f6da/net/socket.c)
не передают нулевой dropcount; [UDP enqueue path](https://github.com/torvalds/linux/blob/a1c2b1da93f5f612aa7a33190c4900c98e51f6da/net/ipv4/udp.c)
сохраняет cumulative snapshot в датаграмме. Старые пакеты очереди не показывают
последующие потери. После опустошения очереди требуются свежие post-load markers
и повторный marker, который не должен повторно прибавлять те же потери.
Отсутствие cmsg также может означать неподдерживаемую telemetry и не доказывает
отсутствие потерь.

Каждый base UDP listener сохраняет синхронизированный scalar high-water между
запусками receiver. Новые сокеты daemon начинают с нуля; existing/custom сокеты
требуют первого известного baseline. Новый listener начинает новую эпоху
наблюдений. Только forward modular distance меньше `2^31` обновляет mark и
добавляет increment. Повторный известный sample даёт нулевой increment;
устаревшие и half-range значения дают `ambiguous`, сохраняя прежний mark.
Используется ограниченная [serial arithmetic](https://www.rfc-editor.org/rfc/rfc1982.html#section-3),
численное уменьшение не считается безусловным доказательством wrap.

Эпоха счётчика должна сохраняться, observation gap — оставаться меньше половины
пространства `uint32`. Несколько ненаблюдаемых wraps и reset, имитирующий forward
progress, не обнаруживаются. Initial sample existing transport, unavailable
интервалы и invalid samples не доказывают полный учёт потерь. Два фиксированных
kinds и пять outcomes дают десять sample counters и два increment counters без
peer labels. Handles создаются один раз; Reset и удаление vector series не
поддерживаются.

Наблюдение предшествует отбрасыванию по read size, TTL и BFD parser. Payload
truncation сохраняет независимо валидные полные ancillary metadata и typed
read error; усечённые, повреждённые или дублированные drop cmsgs дают invalid.
Обычные read errors, closure и timeouts не являются samples прочитанной
датаграммы. Overlay paths этой границей listener не инструментированы.
Wire loss, supported scale и финальная runtime qualification не подтверждаются.

## Наблюдение окружения нагрузки

Go-компонент `test/internal/bfdload.CaptureEnvironment` измеряет собственный
процесс. Команда `go run ./test/cmd/bfdload environment` внутри закреплённого
development-контейнера выдаёт JSON-снимок: [RSS и потоки](https://www.kernel.org/doc/html/latest/filesystems/proc.html),
FD, [переключения контекста всего процесса](https://man7.org/linux/man-pages/man2/getrusage.2.html),
фактический `GOMAXPROCS`, настройки GC и накопительные гистограммы runtime.
RSS берётся из `smaps_rollup`; FD включают дескрипторы наблюдения.
[CPU affinity](https://man7.org/linux/man-pages/man2/sched_setaffinity.2.html)
относится к наблюдающему потоку. Начало и длительность ограничивают
последовательный, неатомарный снимок; накладные расходы требуют калибровки.

[Гистограммы](https://github.com/golang/go/blob/2aae1000ea1d4b28923b96db28acf4e3b99d6aff/src/runtime/metrics/histogram.go)
сохраняют копии счётчиков и границы полуоткрытых интервалов; бесконечные границы
представлены JSON-строками. Scheduler delay отделён от kernel RX-to-owner latency.

[Данные cgroup v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)
относятся к текущей видимой группе. Bandwidth-счётчики `cpu.stat` не включают
родительский throttling; доступность `cpu.stat.local` указана отдельно.
Отсутствующие и повреждённые данные недоступны для оценки. Лимиты различают
`max` и численный ноль. Скрытые родители, reference admission, изоляция
генератора и квалификация нагрузки остаются отдельными задачами Beads.

Снимки сохраняют `pids.current` и `pids.max` как число задач. До создания
сессий именованные owner profiles проверяют дополнительный запас `sessions + 64`
у каждого видимого предка: 1064 задачи для reference и 5064 для engineering.
Эта консервативная политика учитывает текущий закреплённый Control baseline,
а не измеренную верхнюю границу. Отчёт сохраняет budget также при отказе;
проверка не резервирует capacity и не отменяет отдельные CPU, memory,
thread-pin A/B и runtime qualification gates. См. [ADR-0007](./adr/0007-workload-phase-accounting.md).

Owner workload создаёт запрошенное CPU pressure ограниченными helper workers
внутри собственной quota. `--cpu-load-percent` использует общий profile validator;
именованные profiles сохраняют утверждённый вход 80%. При включённой CPU pressure
создаётся один thread-pinned worker на effective `GOMAXPROCS`; значения больше
восьми отклоняются. Каждое окно 100 ms запрашивает эту долю thread CPU time
в пределах wall-clock deadline окна. JSON receipt `cpu_pressure`
разделяет requested duty, фактическое thread CPU time, сумму worker wall time,
start/end lateness и пропущенные duty windows по 100 ms. CPU time включает clock
polling и runtime overhead workers; elapsed wall time не равно потреблённому
CPU time. Workers останавливаются и join до frozen owner snapshot и packet drain.
Завершение генерации не доказывает удержание нагрузки 80% или reference acceptance;
использовать реальные CPU/cgroup observations и clock-validity evidence отдельно.

`--gc-pressure` добавляет выключенную по умолчанию фиксированную нагрузку
внутри owner quota: до 4 MiB за 100 ms объектами по 64 KiB, с удержанием ссылок
не более чем на 16 MiB. Peers передают тот же профиль, но эту нагрузку не создают.
Отчёт `gc_pressure` разделяет фактические bytes, objects, batches, пропущенные
и незавершённые batches, пиковое число удерживаемых ссылок, освобождённые ссылки
и phase lateness. Пропущенное окно не компенсируется позже. Workers завершаются
и очищают ring до measurement freeze и drain; старые недостижимые объекты
ожидают обычного GC, поэтому это не лимит heap/RSS. Helper не вызывает GC
принудительно и не меняет runtime settings. Существующие GC cycles и pause
window охватывают весь owner process и не доказывают причинность, калибровку
давления, latency acceptance или reference support.
`released_objects` считает занятые ring entries, освобождённые при выходе worker,
а не все прежние перезаписи или объекты, память которых вернул GC.

`--scheduler-pressure` добавляет четыре owner helper на effective `GOMAXPROCS`;
значения больше восьми отклоняются, а не уменьшают число workers. Максимум —
32 helper. В каждом общем окне 100 ms helper выполняет до 64 раундов по
1024 compute iterations с последующим `runtime.Gosched`, останавливаясь по
deadline или отмене и пропуская истекшие окна. Отчёт `scheduler_pressure`
сохраняет фактические rounds/yield calls, завершённых workers, skipped/incomplete
worker windows, checksum и phase lateness. Yield calls не подтверждают число
OS context switches или глубину runnable queue. Прежние scheduler-window,
production timer и committed-RX histograms остаются отдельными наблюдениями.
Workers завершаются до freeze/drain; вариант выключен по умолчанию, сохраняет
именованные входные условия, не меняет runtime settings и не подтверждает
калиброванные reference distributions.

Вариант `--session-churn`, выключенный по умолчанию, освобождает и создаёт
заново одну реальную допущенную сессию Manager из явного manifest peers.
Первый цикл начинается через 250 ms после общей эпохи; период — пять секунд,
разрыв после реального final-claim drain — 200 ms. Объявляемый AdminDown TX
floor в одну секунду требует не менее трёх секунд notification drain при
multiplier 3. Поэтому fixture требует `--duration=6s` или больше. Цикл с остатком
менее пяти секунд пропускается и считается отдельно. Это admission budget,
а не гарантия задержки drain или recovery. Mixed process fixture использует
11-секундное окно measurement для проверки поколений IPv4 и IPv6.
Ожидание Up ограничено пятью секундами после создания и концом measurement.
Включённое окно без реального recovery завершается ошибкой, а не выдаётся за
выполненный churn.

`session_churn` хранит реальные release/create/Up recovery, времена фаз,
source ports и старые/новые поколения для корреляции с независимым peer report.
Начало вызова release и окончание drain ограничивают момент удаления из registry;
timestamp окончания не является точным временем удаления. Down вне `[start, end)`
исключаются; notification-gap loss учитывается консервативно.
Слоты вне Up пропускаются; причина false Down не выводится автоматически.
Фиксированные слоты и сериализованный inventory исключают выдачу завершённого
поколения за текущую регистрацию. `retired_received` отделён от RX текущего
поколения; `terminal_packets` использует lifetime ledger commits/rejections/
kernel drops. Активные `registrations.known_peers` и `active_metric_registrations`
отделены от сохранённых session/packet series. Series считаются как реальные
collector DTO label sets, а не развёрнутые histogram buckets или peer-Up counts.
После остановки сохраняются последние samples сессий с `registered=false`.
Вариант повторно использует подготовленные wire identities и не квалифицирует
рост произвольных уникальных peer labels. Churn завершается до measurement
freeze; ограниченный журнал вмещает все пятисекундные слоты 24-часового профиля
и явно считает дальнейшие пропуски.

`test/internal/bfdload.StartOwner` запускает production UDP listeners, receiver,
session manager и владеющие сокетами UDP senders с отдельным реальным collector.
Типизированный снимок содержит настроенные endpoints, source ports, наблюдения
сессий, delivery counters, метрики collector и ресурсы собственного процесса.
Отмена контекста или отказ receiver завершает все собственные workers и закрывает
сокеты; `Close` возвращает ошибки завершения. Control receive ports исключают
Echo port 3785 и диапазон source ports TX 49152–65535. IPv4/IPv6 component fixture
использует изолированные альтернативные порты и проверяет фактические packet commit,
rejection и ответную TX. Это не квалификация RFC ports, независимого генератора,
ресурсов всего daemon или reference workload.

`test/internal/bfdload.RunPeers` передаёт реальные UDP-пакеты с собственной
моделью unauthenticated asynchronous Control, независимой от FSM целевых сессий.
Отдельные seeded PCG streams обслуживают каждый peer и направление; protocol
jitter использует другой поток. Random omissions и timed blackholes учитываются
отдельно от успешных UDP writes, canceled writes и необработанного входа.
Частичный запуск и отмена закрывают собственные сокеты и завершают workers.
Именованные профили `reference` и `engineering` отклоняют сокращённые входные
условия; `fixture` явно не квалифицирован. Отчёт относится к генератору,
содержит raw kernel-drop samples и не подтверждает reference qualification.
`bfdload peers PROFILE` и `bfdload owner PROFILE` принимают типизированные флаги,
повторяемые пары `--session=localAddrPort,peerAddrPort` и `--interface` для
именованных профилей. Повторные скалярные флаги, некорректные адреса и сокращённые
именованные профили отклоняются до открытия сокетов. SIGINT/SIGTERM отменяют
нагрузку, завершают workers и выводят частичный JSON-отчёт; при блокировке stdout
после очистки сохраняется стандартное завершение по сигналу.

Команды без `--control-dir` сохраняют независимые timers и lifetime totals,
включающие запуск и остановку. Controlled owner/peer commands используют
существующий Go-owned topology controller: bind обоих участников, Up, общая
будущая monotonic epoch, frozen measurement snapshot owner, остановка и join peers,
затем terminal drain до остановки owner. Приватные bounded control files запрещают
replacement и проверяют общий time namespace. Недостающие terminal receipts
завершают drain ошибкой по deadline вместо ложного completion.
Conversion bracket не устанавливает независимую погрешность RX timing.
Деление lifetime totals на requested measurement duration не доказывает window
throughput; снимки остаются sampled observations.
См. [ADR-0007](./adr/0007-workload-phase-accounting.md).

Independent-process IPv4/IPv6 fixture 2026-10-24 проходит за 47,98 с:
79 actual writes, 79 owner commits, одинаковые phase identities, идемпотентная
настройка namespace и удаление четырёх контейнеров. Evidence:
`reports/udp-resume-20261024/`. Оба отчёта сохраняют `reference_qualified=false`
и unknown total clock uncertainty. Это production-core fixture receipt, а не
reference-load или complete-daemon qualification. Implementation acceptance
принадлежит Beads `gobfd-qj0.8.2.1.6.3.3`.
Локальная implementation acceptance зафиксирована на
`b2e68ad2a13824a05a9b92528fe55c9a8d4fd6d1`; исправленная process fixture
проходит за 47,67 с с тем же ledger 79/79 и явным непустым measurement snapshot.
Mutation и malformed-report проверки подтверждают snapshot/failure boundaries.
Reference и absolute-clock qualification остаются открытыми.

`RunOwner` сохраняет начальный и конечный снимки production collector, измеренные
пики ресурсов, средние RSS за первые и последние шесть часов, дельты runtime
гистограмм и ограниченный журнал Down. Notification gaps и переполнение журнала
считаются, но конечный timer не синхронизирован с buffered или ещё не dispatched
изменениями. Пустой журнал не доказывает отсутствие Down. Beads
`gobfd-qj0.8.2.1.6.3.4.3.5` требует cutoff fencing до false-Down qualification.
Без независимого отчёта генератора Down не признаётся намеренным.
`completed` означает завершение периода наблюдения; controlled mode
дополнительно требует terminal-ledger settlement. Непрерывный Up всех сессий
этим не подтверждается. Допуск reference требует лимитов дочерней cgroup ровно 8 CPU/
8 GiB, GOMAXPROCS 8, закреплённого компилятора и отсутствия более тесных лимитов
у видимых предков. Скрытая иерархия отклоняется. Допуск не резервирует ресурсы
и не доказывает видимость host namespace: это отдельно проверяет изолированный
runner. Изоляция процессов, 24 часа Up после установления сессий, фоновая CPU
нагрузка, независимая проверка часов и квалификация всего daemon остаются
отдельными условиями в Beads. Источник: [контракт иерархии cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html).

`clock_calibrations` listener сохраняют монотонную скобку из трёх чтений вокруг
wall/monotonic pair и номинальные разрешения native clocks. Условная граница
преобразования равна `span_ns + realtime_resolution_ns + monotonic_resolution_ns`.
Модель предполагает native clocks с постоянным realtime-minus-monotonic offset.
Ошибки всех чтений каждого clock должны находиться в одном фиксированном интервале
шириной не больше его номинального разрешения; модель не означает абсолютную ошибку
`+/-resolution`. Номинальное разрешение не подтверждает эту предпосылку или
физическую точность часов. Допуск проверки согласованности 1 ms остаётся отдельной величиной.

Пусть `B,A` — внешние monotonic-чтения, `M,R` — monotonic и realtime anchor,
а `C,K` — чтения owner commit и kernel RX. Измеренная задержка равна `C-M-(K-R)`.
Оба anchor-чтения находятся внутри скобки, а наблюдаемые monotonic значения
сохраняют порядок. После сокращения постоянных смещений ошибка относительно
идеальной задержки ограничена `A-B` и одной разностью ошибок чтений каждого clock.
Такие разности ограничены шириной фиксированных интервалов, поэтому для обоих
порядков anchor-чтений действует граница `span + qMonotonic + qRealtime`.
Постоянные смещения ошибок сокращаются. Независимые conversion-fixtures проверяют
ненулевой pairing skew и противоположные концы обоих интервалов ошибок.

`ClockEpochGuard` нагрузки устанавливает неблокирующий
[realtime timerfd](https://man7.org/linux/man-pages/man2/timerfd_create.2.html)
с absolute `CANCEL_ON_SET` до калибровки. `Finish` проверяет cancellation после
последнего измерения и закрывает FD. Успешная неблокирующая проверка выставляет
`no_cancellation_observed`, но не доказывает отсутствие скачков часов.
[Timekeeping публикует offset до уведомления](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/kernel/time/timekeeping.c#L1468-L1474);
[уведомление после resume откладывается](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/fs/timerfd.c#L118-L131).
Эти upstream v6.12 sources не квалифицируют vendor kernel. Отмена, истечение, недоступные
данные и abort не дают пригодного свидетельства. Lifetime ограничен семью днями.
Операций syscall на каждый пакет и отдельной guard goroutine не добавляется.
`WithClockEpoch` требует монотонные метки текущего процесса для всей скобки
калибровки и остановки owner. UTC остаётся метаданными отчёта; чтение JSON не
восстанавливает эти свидетельства и не позволяет повторно квалифицировать запуск.
Принимаются только известные калибровки внутри завершённого окна.
Максимальная условная граница преобразования попадает в
`conditional_timing_uncertainty_ns`; постоянство clock offset остаётся
предпосылкой модели. Общая `timing_uncertainty_ns` сохраняет null и явную причину
unknown, а прежний признак reference qualification сбрасывается. Invalid и
uncovered данные сбрасывают обе границы. Для полной нагрузочной приёмки требуется
независимое подтверждение валидности часов; одного cancellation witness недостаточно.
Это не подтверждение точности hardware timestamps, reference admission и supported load.

## Граница компонентного сравнения

C-программы в [`bench/c/`](../../bench/c/) — собственные модели репозитория,
а не upstream-бинарники FRR или BIRD. Эта область указана в заголовках
`bench_frr.c` и `bench_bird.c`. FRR-style FSM использует вложенный switch;
общая session map — локальная реализация linear probing. C harness использует
`CLOCK_MONOTONIC`, фиксированное число iterations на benchmark (по умолчанию
10 миллионов, creation — 10 000) и шесть повторений; Go адаптивно выбирает iterations. Операции и validation недостаточно
эквивалентны для рейтинга продуктов или языков.

`make benchmark-cross` последовательно запускает C-модели и Go benchmarks
и записывает `bench-results/bench-c-frr.txt`, `bench-c-bird.txt`, `bench-go.txt`.
`make benchmark-save` записывает только Go-результаты в `testdata/benchmarks/`.
Ни одна команда не подтверждает daemon throughput или поддерживаемый масштаб.
Старый Make target `benchmark-report` вызывает `go run` напрямую; запускайте
генератор отчёта внутри закреплённого development container, без установки Go
на хост. Исходные исторические Go samples сохранены в
[`testdata/benchmarks/v0.4.0/`](../../testdata/benchmarks/v0.4.0/).

Не исключайте этапы workload из denominator при сравнении. Buffered enqueue,
stateless FSM lookup и committed owner receive — три разные операции.
Парсинг аутентификации создаёт `AuthSection`, digest которого ссылается на
входной буфер; signing и session-owned copies имеют собственные lifetime и
стоимость. Production VXLAN/Geneve TX использует caller-buffer
`BuildInnerPacketInto`; allocating compatibility wrapper — отдельный путь.

## Runtime-архитектура и лимиты ресурсов

Control и Echo работают через owner goroutine; оба вызывают
`runtime.LockOSThread` на всё время её цикла. Это ограничение по thread resources,
а не изоляция CPU или гарантия scheduling. Как минимум 1 000 закреплённых
Control owners не укладываются в принятый reference bound 48 threads.
Задача thread-pin A/B открыта; перенос кандидата на другую VM не устраняет
это ограничение.

Daemon не задаёт общий project override `GOMAXPROCS`. Ограниченный workload
требует своего объявленного значения и cgroup limits; фактические runtime
defaults нужно измерять. CPU quota контейнера не резервирует CPU time,
а `GOMAXPROCS` не ограничивает общее число OS threads. Сохраняйте стандартный
`GOGC`; выбирайте `GOMEMLIMIT` только по измерениям deployment. Construction
B/op и выбор языка не дают численных гарантий RSS на сессию, startup time
или GC pause.

Выбранные steady-state операции имеют цель zero allocations. Она не охватывает
все receive-, authentication-, management-, lifecycle- и notification-пути
и не исключает runtime pauses. Утверждение об allocations должно ссылаться
на конкретное тело benchmark и результат для точного toolchain.

## Измерения deployment

Используйте фактические enabled paths, процедуру peer и нагрузку. Сохраняйте
identity исходников и бинарника, effective CPU/memory/PID limits, affinity,
pressure, throttling, clock validity, terminal packet accounting, false Down
и результаты очистки. Отдельно измеряйте Linux receive, owner commit, timer
dispatch/effect и wire-поведение: timestamps и success denominators различаются.

Принятый reference — 1 000 смешанных IPv4/IPv6 sessions, номинальный интервал
100 ms и 24 часа после setup. Engineering profile — 5 000 sessions,
300 ms и 30 минут. Named profiles сохраняют обязательные CPU/loss/blackhole
inputs. Ограниченные CPU, GC, scheduler-pressure и session-churn drivers для
фиксированного manifest реализованы; варианты GC, scheduler и churn выключены
по умолчанию. Калиброванные stress distributions и независимое clock evidence
не завершены. Короткий fixture или более долгий запуск без этих свидетельств
не являются reference acceptance. Текущие inputs находятся в
[`profile_linux.go`](../../test/internal/bfdload/profile_linux.go); Beads
определяет приёмку и исполнение.

Поля конфигурации `network.dscp` нет. Квалифицируйте host queueing и QoS
внешними средствами, не копируйте неподдерживаемую настройку daemon.
Фактические имена метрик и значения labels описаны в [Мониторинге](07-monitoring.md);
active-session gauges не отражают peer Up, а delivery outcomes не измеряют
все wire losses.

## Связанные документы

- [Руководство по benchmarks](12-benchmarks.md) — команды и границы воспроизводимости.
- [Сравнения](13-competitive-analysis.md) — сопоставление deployment.
- [ADR учёта workload](adr/0007-workload-phase-accounting.md) — фазы и ledger.
- [Соответствие RFC](08-rfc-compliance.md) — процедуры и квалификация.
- [Roadmap](roadmap.md) — датированные результаты; текущая работа хранится в Beads.
