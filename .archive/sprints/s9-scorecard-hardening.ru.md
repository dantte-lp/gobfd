# OpenSSF Scorecard Hardening

Дата: 2026-05-01

Область: repository security posture для `dantte-lp/gobfd` при одном активном
maintainer.

## Текущий Score

| Источник | Значение |
|---|---:|
| Scorecard run | `25216591582` |
| Commit | `9fda579a2d0a02f9c2b52f4596c97872b68f1d9a` |
| Scorecard version | `v5.3.0`, commit `c22063e786c11f9dd714d777a687ff7c4599b600` |
| Overall score | `6.3` |

## Статус проверок

| Check | Score | Status | Действие при одном maintainer |
|---|---:|---|---|
| Dependency-Update-Tool | 10 | Complete | Оставить Dependabot включённым. |
| Dangerous-Workflow | 10 | Complete | Не добавлять `pull_request_target` и unsafe `workflow_run` patterns. |
| Security-Policy | 10 | Complete | Поддерживать `SECURITY.md` актуальным. |
| Binary-Artifacts | 10 | Complete | Не хранить generated binaries в git. |
| SAST | 10 | Complete | Оставить CodeQL на каждом change. |
| Packaging | 10 | Complete | Поддерживать release workflow, archives, packages и images. |
| Fuzzing | 10 | Complete | Сохранять Go fuzz targets в protocol hot paths. |
| License | 10 | Complete | Сохранять canonical Apache-2.0 text. |
| CI-Tests | 10 | Complete | Оставить required CI checks на pull requests. |
| Vulnerabilities | 9 | Action required | Разобрать `GO-2026-4736`; обновить, заменить или документировать non-impact в scanner policy. |
| Token-Permissions | 8 | Action required | Добавить top-level read-only permissions во все workflows; write scopes держать только на job level. |
| Pinned-Dependencies | 6 | Action required | Hash-pin или удалить `downloadThenRun`, `pipCommand` и `npmCommand` findings. |
| CII-Best-Practices | 0 | Action required | Зарегистрировать проект в OpenSSF Best Practices и добавить badge после выдачи статуса. |
| Signed-Releases | 0 | Action required | Публиковать release signatures и SLSA/in-toto provenance для release assets. |
| Branch-Protection | 0 | Action required | Сначала запретить force pushes и branch deletion; блокирующие two-reviewer settings отложить до второго maintainer. |
| Code-Review | 1 | Structural limit | Оставить PR workflow; привлечь external reviewer перед universal independent review. |
| Contributors | 0 | Structural limit | Считать ecosystem signal; synthetic remediation не применять. |
| Maintained | 0 | Temporal limit | Нет remediation до возраста репозитория больше 90 дней. |

## План S9

| Sprint | Goal | Exit criteria | Release required |
|---|---|---|---:|
| S9.1 | Documentation truth table | Repository settings, release docs и RFC capability wording совпадают с кодом и live GitHub state. | No |
| S9.2 | Workflow token hardening | Каждый workflow имеет top-level read-only permissions и scoped job-level writes. | No |
| S9.3 | Dependency pinning | Scorecard не сообщает unpinned `downloadThenRun`, `pipCommand` или `npmCommand` findings. | No |
| S9.4 | Ruleset hardening | Force push и branch deletion отключены для default branch ruleset. | No |
| S9.5 | Best Practices badge | OpenSSF Best Practices project entry существует, README содержит issued badge. | No |
| S9.6 | Signed release pipeline | Future releases публикуют signatures и SLSA/in-toto provenance. | Yes, next functional or security release |

## Отложенные Controls

| Control | Причина |
|---|---|
| Two required reviewers | Блокирует repository с одним maintainer. |
| Required CODEOWNERS review | Блокирует repository с одним maintainer до появления второго maintainer. |
| Latest-push approval | Блокирует единственного maintainer при обновлении собственного pull request. |
| Contributor organization score | Требует реальных external contributors из нескольких организаций. |

## Официальные источники

- OpenSSF Scorecard checks:
  <https://github.com/ossf/scorecard/blob/c22063e786c11f9dd714d777a687ff7c4599b600/docs/checks.md>
- OpenSSF Best Practices:
  <https://www.bestpractices.dev/>
- GitHub repository rulesets:
  <https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets>
- GitHub Actions permissions:
  <https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions>
- SLSA provenance:
  <https://slsa.dev/spec/v1.0/provenance>
