# ADR-0008: Граница экспериментального prerelease

| Поле | Значение |
|---|---|
| Статус | Proposed; публикация не разрешена |
| Дата | — |

## Контекст

Запрошенная `v1.0.0-alpha.1` — экспериментальная delivery milestone. До stable
остаются реализация и квалификация, помимо 24-часового прогона. Текущие release
preflight и release notes намеренно принимают только canonical stable tags;
prerelease нельзя выпустить через этот путь без изменения реализации.

## Решение

Предложить отдельный prerelease channel с явной экспериментальной scope и
известными ограничениями. Статус prerelease не подтверждает supported scale,
stable API compatibility, полное RFC coverage или production readiness.
Функциональные и security failures блокируют заявленную scope альфы.
Исполнимые acceptance и delivery plan принадлежат Beads `gobfd-qj0.8.2.6.4`;
stable gates сохраняются в существующих задачах.

Сохранить stable tag validation и продвижение stable OCI aliases. Будущий
prerelease path должен явно определять channel, сохранять reviewed release
branch и immutable tag ownership, проверять draft assets до публикации и не
продвигать stable aliases. Продолжать разработку в `dev` до готовности alpha
candidate и его отдельных проверок. Запрет remote delivery сохраняется;
это предложение не создаёт tag, draft или asset.

Длительную квалификацию выполнять независимо от интерактивной SSH session на
собственной VM в разрешённом контуре `um-gate/netlab2`. Provisioning VM принадлежит
`um-iac`: отдельный Pulumi stack и проверенный preview. До запуска workload
проверять фактические guest resources, clock, source и binary; одной ёмкости
гипервизора недостаточно. Сохранять logs, terminal status и evidence после
завершения. Final qualification должна указывать точный post-fix source;
baseline на прежнем candidate остаётся диагностическим свидетельством.

## Последствия

Текущие Control и Echo loops сохраняют закрепление OS threads. Не менее 1000
Control threads не укладываются в stable reference bound 48 threads.
Перенос этого candidate на VM не устраняет пробел реализации. Альфа должна
раскрывать сохранённые ограничения; fixture с CPU 25% не подтверждает
утверждённую reference workload 80% или 24-часовую acceptance.

## Источники

- [Текущий thread pin владельца Control](../../../internal/bfd/session_loop.go)
- [Текущий thread pin владельца Echo](../../../internal/bfd/echo.go)
- [Preflight stable release](../../../test/internal/cirunner/release_preflight.go)
- [Валидация notes stable release](../../../test/internal/cirunner/release_notes.go)
- [SemVer prerelease semantics](https://semver.org/spec/v2.0.0.html#spec-item-9)
- [Граница workload accounting](./0007-workload-phase-accounting.md)
- [Датированная сводка](../roadmap.md)
