# Руководство по Changelog

![Keep a Changelog](https://img.shields.io/badge/Keep_a_Changelog-1.1.0-E05735?style=for-the-badge)
![SemVer](https://img.shields.io/badge/SemVer-2.0.0-3F4551?style=for-the-badge)

> Как вести changelog проекта по стандарту [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) и [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

---

### Содержание

- [Формат](#формат)
- [Когда добавлять записи](#когда-добавлять-записи)
- [Типы секций](#типы-секций)
- [Как писать хорошие записи](#как-писать-хорошие-записи)
- [Процесс релиза](#процесс-релиза)
- [Семантическое версионирование](#семантическое-версионирование)
- [Примеры](#примеры)

### Формат

Файл changelog -- `CHANGELOG.md` в корне репозитория. Он следует спецификации [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/):

```markdown
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Описание новой функциональности.

## [1.2.0] - 2026-03-15

### Fixed
- Описание исправления.

[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/dantte-lp/gobfd/releases/tag/v1.2.0
```

Правила:

- Секция `[Unreleased]` всегда присутствует наверху.
- Версии перечислены в обратном хронологическом порядке (новые первыми).
- Даты в формате ISO 8601: `ГГГГ-ММ-ДД`.
- Ссылки сравнения внизу файла для навигации по diff на GitHub.
- Заголовок каждой версии -- ссылка: `## [X.Y.Z] - ГГГГ-ММ-ДД`.

### Когда добавлять записи

Каждый pull request, меняющий видимое пользователю поведение, **обязан** включать запись в `CHANGELOG.md` в секции `[Unreleased]`.

Добавляйте запись, когда PR:

- Добавляет новую функцию, команду CLI, API-эндпоинт или метрику.
- Изменяет существующее поведение (формат конфига, значения по умолчанию, обработку протокола).
- Исправляет баг, с которым может столкнуться пользователь.
- Устраняет уязвимость безопасности.
- Помечает функцию как устаревшую или удаляет её.

**Не** добавляйте записи для:

- Внутреннего рефакторинга без видимого пользователю эффекта.
- Изменений только в тестах.
- Настроек CI/CD пайплайна.
- Исправлений опечаток в документации.

### Типы секций

| Секция | Когда использовать | Пример для GoBFD |
|---|---|---|
| **Added** | Новая функция или возможность | `Added BFD multihop support per RFC 5883.` |
| **Changed** | Изменение существующего поведения | `Changed default DetectMultiplier from 3 to 5.` |
| **Deprecated** | Функция помечена для будущего удаления | `Deprecated JSON output format in favor of YAML.` |
| **Removed** | Функция удалена | `Removed legacy configuration file format.` |
| **Fixed** | Исправление бага | `Fixed authentication sequence number wraparound at 2^32.` |
| **Security** | Исправление уязвимости | `Fixed timing side-channel in HMAC-SHA1 comparison (CVE-XXXX-YYYY).` |

Включайте только секции, в которых есть записи. Не добавляйте пустые секции.

### Как писать хорошие записи

Пишите для **пользователей**, а не для разработчиков. Фокусируйтесь на том, **что** изменилось, а не **как**.

| Качество | Пример |
|---|---|
| Плохо | Refactored FSM event loop to use channel-based dispatch. |
| Хорошо | Improved session convergence time under high peer count. |
| Плохо | Fixed nil pointer in `manager.go:142`. |
| Хорошо | Fixed crash when removing a session during reconciliation. |
| Плохо | Updated protobuf dependency. |
| Хорошо | Fixed compatibility issue with GoBGP v3.37+ API changes. |

Рекомендации:

- Начинайте с глагола: Added, Changed, Fixed, Removed.
- Ссылайтесь на секции RFC, когда релевантно: `Added Echo mode per RFC 5880 Section 6.4.`
- Ссылайтесь на CVE для исправлений безопасности: `Fixed CVE-2026-XXXX.`
- Будьте лаконичны -- одна строка на изменение.
- Группируйте связанные изменения в одну запись, когда уместно.

### Процесс релиза

При подготовке релиза:

1. **Перенесите записи** из `[Unreleased]` в новую секцию версии:

   ```markdown
   ## [Unreleased]

   ## [1.3.0] - 2026-04-01

   ### Added
   - (записи перенесены из Unreleased)
   ```

2. **Обновите ссылки сравнения** внизу файла:

   ```markdown
   [Unreleased]: https://github.com/dantte-lp/gobfd/compare/v1.3.0...HEAD
   [1.3.0]: https://github.com/dantte-lp/gobfd/compare/v1.2.0...v1.3.0
   [1.2.0]: https://github.com/dantte-lp/gobfd/releases/tag/v1.2.0
   ```

3. **Зафиксируйте** обновление changelog:

   ```bash
   git add CHANGELOG.md
   git commit -m "Prepare release v1.3.0"
   ```

4. **Создайте тег и отправьте**:

   ```bash
   git tag -a v1.3.0 -m "Release v1.3.0"
   git push origin master --tags
   ```

5. **GitHub Actions** автоматически:
   - Запускает полный набор тестов.
   - Извлекает release notes из CHANGELOG.md для версии 1.3.0.
   - Собирает бинарники (linux/amd64, linux/arm64), .deb, .rpm пакеты.
   - Публикует Docker-образ в `ghcr.io/dantte-lp/gobfd:1.3.0`.
   - Создаёт GitHub Release с содержимым changelog в качестве описания.

### Семантическое версионирование

Проект следует [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html): `MAJOR.MINOR.PATCH`.

| Компонент | Когда увеличивать | Пример для GoBFD |
|---|---|---|
| **MAJOR** | Ломающие изменения API, формата конфига или протокола | Удалены устаревшие ключи конфига; изменена структура ответа gRPC API. |
| **MINOR** | Новые функции, обратно совместимые | Добавлена поддержка RFC 5883 multihop; новая команда `gobfdctl monitor`. |
| **PATCH** | Исправления багов, документация, обновление зависимостей | Исправлен расчёт detection timeout; обновлена зависимость Go. |

Предрелизные версии используют суффиксы: `v1.0.0-rc.1`, `v1.0.0-beta.2`.

### Примеры

#### Добавление новой функции (PR)

Отредактируйте `CHANGELOG.md`, добавьте в `[Unreleased]`:

```markdown
## [Unreleased]

### Added
- BFD Echo mode implementation per RFC 5880 Section 6.4.
```

#### Исправление бага (PR)

```markdown
## [Unreleased]

### Fixed
- Detection timeout not recalculated after remote MinRxInterval change.
```

#### Исправление безопасности (PR)

```markdown
## [Unreleased]

### Security
- Enforce constant-time comparison for all authentication digests.
```

### Связанные документы

- [CHANGELOG.md](../../CHANGELOG.md) -- Changelog проекта.
- [09-development.md](./09-development.md) -- Рабочий процесс разработки и вклад в проект.
- [CONTRIBUTING.md](../../CONTRIBUTING.md) -- Руководство по вкладу.

---

*Последнее обновление: 2026-02-21*
