#!/usr/bin/env python3
"""Группировка единственного Make Verify и краткая сводка для GitHub CI."""

import argparse
import json
import os
from pathlib import Path
import re
import sys
import time

TIMINGS = Path("reports/ci-verify-timings.json")
AUDIT = Path("reports/npm-audit.json")
AUDIT_EXIT = Path("reports/npm-audit.exit")
MAKE_EXIT = Path("reports/ci-verify.exit")
TRACE = re.compile(r"^\S+?:\d+: update target '([^']+)' due to:")

PHASES = {
    "lint-go": "01 · Анализ Go, Python, Nuxt, SQL и дубликатов",
    "lint-python": "01 · Анализ Go, Python, Nuxt, SQL и дубликатов",
    "lint-web": "01 · Анализ Go, Python, Nuxt, SQL и дубликатов",
    "lint-sql": "01 · Анализ Go, Python, Nuxt, SQL и дубликатов",
    "duplicates": "01 · Анализ Go, Python, Nuxt, SQL и дубликатов",
    "sdk-check": "02 · Контракт API и TypeScript SDK",
    "setup": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "config": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "docker-check": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "python-tests": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "api-checks": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "web-checks": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "test": "03 · Конфигурация, Dockerfile и тесты компонентов",
    "build": "04 · Сборка Docker-образов установки",
    "db-test": "05 · PostgreSQL: схема, роли и миграции",
    "publishing-test": "06 · Публикация: API, SSR и браузер",
    "image-audit": "07 · Безопасность: CVE всех Docker-образов",
}


def group(message):
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print(message, flush=True)


def seconds(value):
    minutes, rest = divmod(round(value), 60)
    return f"{minutes} мин {rest:02d} с" if minutes else f"{rest} с"


def trace_make():
    TIMINGS.parent.mkdir(parents=True, exist_ok=True)
    active = None
    started = time.monotonic()
    phase_started = started
    entries = []
    plugin_timing_warnings = 0

    def close_phase():
        nonlocal active
        if active:
            entries.append({"phase": active, "seconds": round(time.monotonic() - phase_started, 2)})
            group("::endgroup::")
            active = None

    for line in sys.stdin:
        if "[PLUGIN_TIMINGS]" in line:
            plugin_timing_warnings += 1
        match = TRACE.match(line)
        if match:
            phase = PHASES.get(match.group(1))
            if phase and phase != active:
                close_phase()
                active = phase
                phase_started = time.monotonic()
                group(f"::group::{phase}")
        print(line, end="", flush=True)
    close_phase()
    record = {
        "elapsed_seconds": round(time.monotonic() - started, 2),
        "phases": entries,
        "unrecognized_trace": not bool(entries),
        "rolldown_plugin_timing_warnings": plugin_timing_warnings,
    }
    TIMINGS.write_text(json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    if not entries:
        group("::warning::GNU Make --trace не удалось разобрать; см. полный GitHub-лог")
    return 0


def audit_data():
    if not AUDIT.is_file():
        return "NOT RUN", {}, [], "Отчёт npm audit отсутствует"
    try:
        payload = json.loads(AUDIT.read_text(encoding="utf-8"))
    except (ValueError, OSError):
        return "NOT RUN", {}, [], "npm audit вернул невалидный JSON"
    if not isinstance(payload, dict) or payload.get("error"):
        code = (payload.get("error") or {}).get("code", "unknown") if isinstance(payload, dict) else "unknown"
        return "NOT RUN", {}, [], f"npm audit завершился с ошибкой: {code}"
    vulnerabilities = payload.get("vulnerabilities")
    if not isinstance(vulnerabilities, dict) or not isinstance(payload.get("metadata", {}).get("vulnerabilities"), dict):
        return "NOT RUN", {}, [], "В JSON нет обязательной структуры npm audit"
    counts = payload["metadata"]["vulnerabilities"]
    items = []
    for pkg, detail in sorted(vulnerabilities.items()):
        if not isinstance(detail, dict):
            continue
        advisory_ids = sorted({str(via["source"]) for via in detail.get("via", [])
                               if isinstance(via, dict) and isinstance(via.get("source"), int)})
        items.append({"name": pkg, "severity": detail.get("severity", "unknown"),
                      "advisory_ids": advisory_ids})
    exit_status = AUDIT_EXIT.read_text(encoding="utf-8").strip() if AUDIT_EXIT.is_file() else "unknown"
    if exit_status not in ("0", "1"):
        return "NOT RUN", {}, [], f"npm audit вернул неожиданный код завершения: {exit_status}"
    return "REVIEW", counts, items, None


def summarize():
    lines = ["## Проверка проекта: время и предупреждения", ""]
    if TIMINGS.is_file():
        try:
            result = json.loads(TIMINGS.read_text(encoding="utf-8"))
            lines += ["| Этап Make | Длительность | Результат |", "|---|---:|---|"]
            exit_code = int(MAKE_EXIT.read_text(encoding="utf-8").strip())
            phases = result.get("phases", [])
            for index, item in enumerate(phases):
                verdict = "FAIL" if index == len(phases) - 1 and exit_code != 0 else "PASS"
                lines.append(f"| {item['phase']} | {seconds(item['seconds'])} | {verdict} |")
            final_status = "PASS" if exit_code == 0 else "FAIL"
            lines.append(f"| **Итого (один вызов Make)** | **{seconds(result['elapsed_seconds'])}** | **{final_status}** |")
            if result.get("unrecognized_trace"):
                lines.append("\n**Внимание:** не распознаны этапы GNU Make; см. полный GitHub-лог.")
            if result.get("rolldown_plugin_timing_warnings", 0):
                lines.append("\n**Rolldown PLUGIN_TIMINGS:** предупреждение о времени сборочного плагина; "
                             "не является CVE и не блокирует Gate. Профилировать при повторяемом замедлении.")
        except (OSError, ValueError, KeyError, TypeError):
            lines.append("Время Make не удалось прочитать: **NOT RUN**.")
    else:
        lines.append("Измерение Make не было выполнено: **NOT RUN**.")
    _state, counts, findings, error = audit_data()
    lines += ["", "### npm audit: зависимости Nuxt (отчёт по lockfile)", ""]
    if error:
        lines.append(f"**NOT RUN:** {error}. Это не результат проверки уязвимостей.")
        group(f"::warning::{error}")
    else:
        levels = ("critical", "high", "moderate", "low", "info")
        lines.append("Найдено пакетов с advisory по уровням: " + ", ".join(f"{key}={counts.get(key, 0)}" for key in levels) + ".")
        if findings:
            lines += ["", "| Пакет | Серьёзность | Advisory ID | Статус |", "|---|---|---|---|"]
            for item in findings[:30]:
                ids = ", ".join(item["advisory_ids"]) or "косвенная зависимость"
                lines.append(f"| {item['name']} | {item['severity']} | {ids} | OPEN: требует разбора |")
            if len(findings) > 30:
                lines.append(f"\nЕщё {len(findings) - 30} пакетов: см. приложенный JSON.")
            group(f"::warning::npm audit: {len(findings)} пакетов с открытыми advisory; см. итог проверки и npm-audit.json")
        else:
            lines.append("Advisories не найдены в текущем lockfile.")
    lines += ["", "**Важно:** npm audit здесь инвентаризирует advisory и не меняет существующий порог обязательного Docker/Grype Gate. "
              "LOW/MODERATE не считаются автоматически принятым риском; HIGH/CRITICAL требуют отдельного разбора. "
              "Автоматическое исправление зависимостей не запускается."]
    summary = "\n".join(lines) + "\n"
    dest = os.getenv("GITHUB_STEP_SUMMARY")
    if dest:
        with open(dest, "a", encoding="utf-8") as writer:
            writer.write(summary)
    else:
        print(summary)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("trace", help="Группировать стандартный вывод GNU Make --trace")
    sub.add_parser("summary", help="Записать итоги в GitHub Actions Job Summary")
    args = parser.parse_args()
    if args.action == "trace":
        return trace_make()
    return summarize()


if __name__ == "__main__":
    sys.exit(main())
