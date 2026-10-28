# ADR-0008: Граница экспериментального prerelease

| Поле | Значение |
|:---|:---|
| Status | Proposed; no publication authorization |
| Date | — |

## Контекст

Experimental `v1.0.0-alpha.1` предполагает меньший advertised scope, чем stable acceptance. Существующие [release preflight](../../../test/internal/cirunner/release_preflight.go) и [notes validation](../../../test/internal/cirunner/release_notes.go) принимают canonical stable tags. Prerelease требует отдельного пути.

## Решение

Предлагается отдельный явно заданный prerelease channel. Stable validation и OCI alias promotion сохраняются. Будущий channel требует reviewed release branch, immutable tag ownership, проверенных draft assets и dedicated checks; stable aliases не продвигаются. Functional/security failures блокируют advertised scope.

Разработка остаётся в `dev` до готовности candidate и checks. Existing remote-delivery hold действует. Этот Proposed record не разрешает tag, draft, asset либо publication. Prerelease acceptance/delivery и stable qualification остаются отдельными.

Long qualification выполняется независимо от interactive SSH на owned VM в разрешённом контуре `um-gate/netlab2`. Provisioning принадлежит isolated `um-iac` Pulumi stack с reviewed preview. До workload проверьте actual guest resources, clocks, source и binary; сохраните logs, terminal result и evidence. Final qualification фиксирует exact post-fix source.

## Последствия

Prerelease status не доказывает supported scale, stable API compatibility, full RFC coverage либо production readiness. CPU fixture 25% не квалифицирует reference workload 80% либо 24-hour acceptance; hypervisor capacity не равна guest admission.

Исходный pinned Control/Echo candidate не мог выполнить reference bound 48 threads с 1000 owners. Local consolidation instruction владельца от 2026-10-28 отдельно разрешает source integration с удалением permanent owner pins. Она не закрывает native A/B, bound 48 threads, 24-hour operation и final wire gates. Conservative PID budget из [ADR-0007](0007-workload-phase-accounting.md) сохраняется до измеренного пересмотра.

## Источники

- [Control owner](../../../internal/bfd/session_loop.go), [Echo owner](../../../internal/bfd/echo.go).
- [Workload accounting](0007-workload-phase-accounting.md).
- [Датированная roadmap projection](../roadmap.md).
