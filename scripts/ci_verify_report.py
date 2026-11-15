#!/usr/bin/env python3
"""Группировка единственного Make Verify и краткая сводка для GitHub CI."""

import argparse
import json
import os
from pathlib import Path
import sys

TIMINGS = Path("reports/ci-verify-timings.json")
AUDIT = Path("reports/npm-audit.json")
AUDIT_EXIT = Path("reports/npm-audit.exit")
MAKE_EXIT = Path("reports/ci-verify.exit")
NOT_RUN = "NOT RUN"

def group(message):
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print(message, flush=True)


def seconds(value):
    minutes, rest = divmod(round(value), 60)
    return f"{minutes} мин {rest:02d} с" if minutes else f"{rest} с"


def audit_data():
    if not AUDIT.is_file():
        return NOT_RUN, {}, [], "Отчёт npm audit отсутствует"
    try:
        payload = json.loads(AUDIT.read_text(encoding="utf-8"))
    except (ValueError, OSError):
        return NOT_RUN, {}, [], "npm audit вернул невалидный JSON"
    if not isinstance(payload, dict) or payload.get("error"):
        code = (payload.get("error") or {}).get("code", "unknown") if isinstance(payload, dict) else "unknown"
        return NOT_RUN, {}, [], f"npm audit завершился с ошибкой: {code}"
    vulnerabilities = payload.get("vulnerabilities")
    if not isinstance(vulnerabilities, dict) or not isinstance(payload.get("metadata", {}).get("vulnerabilities"), dict):
        return NOT_RUN, {}, [], "В JSON нет обязательной структуры npm audit"
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
        return NOT_RUN, {}, [], f"npm audit вернул неожиданный код завершения: {exit_status}"
    return "REVIEW", counts, items, None


def append_timings(lines):
    """Показать прямой Make Verify без недостоверной поэтапной детализации."""
    if not TIMINGS.is_file():
        lines.append(f"Измерение Make не было выполнено: **{NOT_RUN}**.")
        return
    try:
        result = json.loads(TIMINGS.read_text(encoding="utf-8"))
        exit_code = int(MAKE_EXIT.read_text(encoding="utf-8").strip())
        final_status = "PASS" if exit_code == 0 else "FAIL"
        lines.extend(["| Этап | Длительность | Результат |", "|---|---:|---|",
                      f"| **Полный Make Verify** | **{seconds(result['elapsed_seconds'])}** | **{final_status}** |",
                      "",
                      "Внутренние цели GNU Make видны в прямом GitHub-логе. Отдельные времена фаз не измеряются, "
                      "чтобы не пропускать вывод через потенциально блокирующий конвейер."])
    except (OSError, ValueError, KeyError, TypeError):
        lines.append(f"Время Make не удалось прочитать: **{NOT_RUN}**.")

def append_audit_findings(lines, counts, findings):
    """Показать находки npm без автоматического принятия или исправления риска."""
    levels = ("critical", "high", "moderate", "low", "info")
    lines.append("Найдено пакетов с advisory по уровням: " + ", ".join(
        f"{key}={counts.get(key, 0)}" for key in levels) + ".")
    if not findings:
        lines.append("Advisories не найдены в текущем lockfile.")
        return
    lines.extend(["", "| Пакет | Серьёзность | Advisory ID | Статус |", "|---|---|---|---|"])
    for item in findings[:30]:
        ids = ", ".join(item["advisory_ids"]) or "косвенная зависимость"
        lines.append(f"| {item['name']} | {item['severity']} | {ids} | OPEN: требует разбора |")
    if len(findings) > 30:
        lines.append(f"\nЕщё {len(findings) - 30} пакетов: см. приложенный JSON.")
    group(f"::warning::npm audit: {len(findings)} пакетов с открытыми advisory; см. итог проверки и npm-audit.json")


def append_audit_summary(lines):
    """Учитывать информационные npm advisory отдельно от Docker/Grype Gate."""
    _state, counts, findings, error = audit_data()
    lines.extend(["", "### npm audit: зависимости Nuxt (отчёт по lockfile)", ""])
    if error:
        lines.append(f"**{NOT_RUN}:** {error}. Это не результат проверки уязвимостей.")
        group(f"::warning::{error}")
    else:
        append_audit_findings(lines, counts, findings)
    lines.extend(["", "**Важно:** npm audit здесь инвентаризирует advisory и не меняет существующий порог "
                  "обязательного Docker/Grype Gate. LOW/MODERATE не считаются автоматически принятым риском; "
                  "HIGH/CRITICAL требуют отдельного разбора. Автоматическое исправление зависимостей не запускается."])


def summarize():
    lines = ["## Проверка проекта: время и предупреждения", ""]
    append_timings(lines)
    append_audit_summary(lines)
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
    sub.add_parser("summary", help="Записать итоги в GitHub Actions Job Summary")
    parser.parse_args()
    return summarize()


if __name__ == "__main__":
    sys.exit(main())
