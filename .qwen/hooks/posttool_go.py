#!/usr/bin/env python3
"""PostToolUse: после правки .go файла сразу доводит до модели gofmt-замечания."""
import json
import os
import subprocess
import sys


def main():
    try:
        ev = json.load(sys.stdin)
    except Exception:
        sys.exit(0)

    ti = ev.get("tool_input") or {}
    path = ti.get("file_path") or ti.get("path") or ""
    if not path.endswith(".go") or not os.path.isfile(path):
        sys.exit(0)

    try:
        p = subprocess.run(["gofmt", "-l", path], capture_output=True, text=True, timeout=20)
        unformatted = p.stdout.strip()
    except Exception:
        sys.exit(0)

    out = {"hookSpecificOutput": {"hookEventName": "PostToolUse"}}
    if unformatted:
        out["hookSpecificOutput"]["additionalContext"] = (
            "gofmt требует форматирования: %s. Выполни gofmt -w на этом файле." % path)
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main()
