#!/usr/bin/env python3
"""SubagentStop: не отпускать кодера/тестировщика без доказательства прогоном в транскрипте."""
import json
import re
import sys


def main():
    try:
        ev = json.load(sys.stdin)
    except Exception:
        sys.exit(0)

    if ev.get("stop_hook_active"):          # блокировка уже была — не зацикливать
        sys.exit(0)

    transcript = ev.get("agent_transcript_path") or ""
    last = ev.get("last_assistant_message") or ""
    blob = last
    try:
        with open(transcript, "r", errors="replace") as fh:
            blob = fh.read()
    except Exception:
        pass

    claims_done = re.search(r"(готово|завершено|проверено|зелён|зеленый|passed|готов к PR)", last, re.I)
    ran = re.search(r"go\s+(test|build|vet)\b", blob) or re.search(r"golangci-lint", blob)

    if claims_done and not ran:
        print(json.dumps({"decision": "block",
                          "reason": "В отчёте заявлено «проверено», но в транскрипте нет запуска "
                                    "go build/vet/test или golangci-lint. Прогони гейты и приведи "
                                    "вывод дословно."}, ensure_ascii=False))
    else:
        print(json.dumps({"decision": "allow"}, ensure_ascii=False))


if __name__ == "__main__":
    main()
