# Документация GoBFD

![Version](https://img.shields.io/badge/Version-1.0.0-1a73e8?style=for-the-badge)
![Documents](https://img.shields.io/badge/Documents-15-34a853?style=for-the-badge)
![Language](https://img.shields.io/badge/Lang-Русский-ea4335?style=for-the-badge)
![Status](https://img.shields.io/badge/Status-Active-brightgreen?style=for-the-badge)

> Полная техническая документация **GoBFD** -- production-ready демона протокола BFD (RFC 5880/5881) на Go 1.26.

---

## Карта документации

```mermaid
graph TD
    IDX["docs/ru/README.md<br/>(Вы здесь)"]

    subgraph "Архитектура"
        A1["01-architecture.md<br/>Архитектура системы"]
        A2["02-protocol.md<br/>Протокол BFD"]
    end

    subgraph "Операции"
        B1["03-configuration.md<br/>Конфигурация"]
        B2["04-cli.md<br/>Справочник CLI"]
        B3["06-deployment.md<br/>Развёртывание"]
        B4["15-security.md<br/>Политика безопасности"]
    end

    subgraph "Тестирование и качество"
        C1["05-interop.md<br/>Тесты совместимости"]
        C2["07-monitoring.md<br/>Мониторинг"]
        C3["09-development.md<br/>Разработка"]
    end

    subgraph "Производительность"
        E1["12-benchmarks.md<br/>Бенчмарки"]
        E2["13-competitive-analysis.md<br/>Конкурентный анализ"]
        E3["14-performance-analysis.md<br/>Анализ производительности"]
    end

    subgraph "Справочник"
        D1["08-rfc-compliance.md<br/>Соответствие RFC"]
        D2["rfc/<br/>Тексты RFC"]
        D3["10-changelog.md<br/>Руководство по Changelog"]
        D4["11-integrations.md<br/>Интеграции"]
    end

    IDX --> A1
    IDX --> A2
    IDX --> B1
    IDX --> B2
    IDX --> B3
    IDX --> B4
    IDX --> C1
    IDX --> C2
    IDX --> C3
    IDX --> D1
    IDX --> E1

    A1 --> A2
    A2 --> D1
    B1 --> B2
    B3 --> C2
    C1 --> C3
    D1 --> D2
    E1 --> E2
    E2 --> E3

    style IDX fill:#1a73e8,color:#fff
```

---

## Содержание

### Архитектура

| # | Документ | Описание |
|---|---|---|
| 01 | [**Архитектура**](./01-architecture.md) | Архитектура системы, диаграмма пакетов, путь пакета, дизайн FSM |
| 02 | [**Протокол BFD**](./02-protocol.md) | FSM, таймеры, джиттер, формат пакета, аутентификация (RFC 5880) |

### Операции

| # | Документ | Описание |
|---|---|---|
| 03 | [**Конфигурация**](./03-configuration.md) | Справочник YAML-конфига, переменные окружения, горячая перезагрузка |
| 04 | [**Справочник CLI**](./04-cli.md) | Команды gobfdctl, интерактивная оболочка, форматы вывода |
| 05 | [**Тесты совместимости**](./05-interop.md) | Наборы тестов: 4-пировый, BGP+BFD, RFC-специфичный, вендорные NOS |
| 06 | [**Развёртывание**](./06-deployment.md) | systemd, Podman Compose, контейнерный образ, production |
| 07 | [**Мониторинг**](./07-monitoring.md) | Метрики Prometheus, дашборд Grafana, алертинг |
| 15 | [**Политика безопасности**](./15-security.md) | Production policy для API, GoBGP, BFD auth, контейнеров и vulnerability gate |

### Справочник

| # | Документ | Описание |
|---|---|---|
| 08 | [**Соответствие RFC**](./08-rfc-compliance.md) | Матрица соответствия RFC, заметки по реализации |
| 09 | [**Разработка**](./09-development.md) | Рабочий процесс, Make-цели, тестирование, линтинг |
| 10 | [**Руководство по Changelog**](./10-changelog.md) | Ведение CHANGELOG.md, процесс релиза, семантическое версионирование |
| 11 | [**Интеграции**](./11-integrations.md) | BGP failover, HAProxy, наблюдаемость, ExaBGP, Kubernetes |

### Производительность

| # | Документ | Описание |
|---|---|---|
| 12 | [**Бенчмарки**](./12-benchmarks.md) | Как запускать, читать и интерпретировать результаты бенчмарков |
| 13 | [**Конкурентный анализ**](./13-competitive-analysis.md) | Сравнение с FRR, BIRD, aiobfd, аппаратными платформами |
| 14 | [**Анализ производительности**](./14-performance-analysis.md) | GoBFD vs реализации на C: бенчмарки, архитектура, поведение при нагрузке |

### Исходные тексты RFC

| Файл | RFC | Название |
|---|---|---|
| [rfc5880.txt](../rfc/rfc5880.txt) | RFC 5880 | Bidirectional Forwarding Detection (BFD) |
| [rfc5881.txt](../rfc/rfc5881.txt) | RFC 5881 | BFD for IPv4 and IPv6 (Single Hop) |
| [rfc5882.txt](../rfc/rfc5882.txt) | RFC 5882 | Generic Application of BFD |
| [rfc5883.txt](../rfc/rfc5883.txt) | RFC 5883 | BFD for Multihop Paths |
| [rfc5884.txt](../rfc/rfc5884.txt) | RFC 5884 | BFD for MPLS Label Switched Paths |
| [rfc5885.txt](../rfc/rfc5885.txt) | RFC 5885 | BFD for PW VCCV |
| [rfc7130.txt](../rfc/rfc7130.txt) | RFC 7130 | Bidirectional Forwarding Detection (BFD) on LAG |

---

## Быстрый старт

```bash
# Клонирование и сборка
git clone https://github.com/dantte-lp/gobfd.git && cd gobfd
make build

# Запуск тестов
make test

# Запуск среды разработки
make up
```

См. [06-deployment.md](./06-deployment.md) для развёртывания в production и [09-development.md](./09-development.md) для полного рабочего процесса разработки.

---

*Последнее обновление: 2026-02-24*
