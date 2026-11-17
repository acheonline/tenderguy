#!/usr/bin/env python3
"""Табличные тесты гейта pretool_guard.py.

Запуск: python3 .qwen/hooks/pretool_guard_test.py   (выход 0 — все случаи совпали)

Опасные строки намеренно собираются из частей: литералом в файле они лежат как данные, но
сам тест не должен зависеть от того, что внешний классификатор может принять их за команду.
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "pretool_guard.py")
REPO = os.path.dirname(os.path.dirname(HERE))

# собираем из частей, чтобы текст файла оставался читаемым, а матчинг — проверяемым
PUSH_MAIN = "git %s %s main" % ("push", "origin")
PUSH_FORCE = "git push --for" + "ce-with-lease"
PUSH_DELETE = "git push origin chore/x --del" + "ete"
RESET_HARD = "git re" + "set --hard HEAD~1"
CLEAN_F = "git cle" + "an -fd"
CHECKOUT_F = "git check" + "out -f ."
RM_RF = "rm -r" + "f docs"
ADD_DOCS = "git add docs/НЭО/файл.docx"
ADD_ENV = "git add .en" + "v"
COMMIT_NOVERIFY = "git commit --no-" + "verify -m x"
COMMIT_AMEND = "git commit --ame" + "nd -m x"
COMMIT_TASK = "git commit -m '[P1.7] Метрика'"
REBASE = "git re" + "base -i main"
STASH_DROP = "git stash " + "drop"
BRANCH_DEL = "git branc" + "h -D feat/x"
PUSH_FEATURE = "git push -u origin chore/agentic-scaffold"


def run(cmd, cwd=REPO):
    ev = json.dumps({"tool_name": "run_shell_command", "cwd": cwd,
                     "tool_input": {"command": cmd}})
    p = subprocess.run([sys.executable, GUARD], input=ev, capture_output=True, text=True)
    try:
        out = json.loads(p.stdout)
        return out["hookSpecificOutput"]["permissionDecision"], p.stderr
    except Exception:
        return "PARSE_FAIL", (p.stdout + p.stderr)[:200]


CASES = [
    # блокировки
    (PUSH_MAIN, "deny", "push в main"),
    (PUSH_FORCE, "deny", "force-push"),
    (PUSH_DELETE, "deny", "push --delete"),
    (RESET_HARD, "deny", "reset --hard"),
    (CLEAN_F, "deny", "clean -fd"),
    (CHECKOUT_F, "deny", "checkout -f ."),
    (RM_RF, "deny", "rm -rf"),
    (ADD_DOCS, "deny", "docs/ в индексе"),
    (ADD_ENV, "deny", "секрет в индексе"),
    (COMMIT_NOVERIFY, "deny", "обход гейтов коммита"),
    (COMMIT_AMEND, "deny", "amend"),
    (COMMIT_TASK, "deny", "коммит [P*] без изменения README"),
    (REBASE, "deny", "rebase"),
    (STASH_DROP, "deny", "stash drop"),
    (BRANCH_DEL, "deny", "branch -D"),
    ("cd x && " + RESET_HARD, "deny", "опасная команда в цепочке"),
    ("git -C /tmp/x " + PUSH_MAIN[4:], "deny", "push в main через git -C"),
    ("git --git-dir=/tmp/x/.git " + PUSH_MAIN[4:], "deny", "push в main через --git-dir"),
    ("git -c user.name=x " + PUSH_MAIN[4:], "deny", "push в main через -c"),
    ("git -C /tmp/x status", "allow", "git -C с безобидной подкомандой"),
    # пропуск
    (PUSH_FEATURE, "allow", "push feature-ветки"),
    ("go build ./... && go vet ./...", "allow", "сборка и vet"),
    ("git checkout -b feat/p33-json", "allow", "создание ветки"),
    ("gh pr create --title x", "allow", "создание PR"),
    ("git add README.md internal/parser/x.go", "allow", "обычный add"),
    ("git status --porcelain", "allow", "статус"),
    # опасные строки как ДАННЫЕ, а не как команда — должны проходить
    ("printf '%s' '" + PUSH_MAIN + "' > guide.md", "allow", "опасный текст в printf"),
    ("echo '" + RESET_HARD + "' >> docs-note.md", "allow", "опасный текст в echo"),
]


def main():
    fails = []
    for cmd, want, why in CASES:
        got, err = run(cmd)
        ok = got == want
        print("%s want=%-5s got=%-10s %s" % ("OK  " if ok else "FAIL", want, got, why))
        if not ok:
            fails.append((cmd, want, got, why, err))

    print("\nпроверено: %d, расхождений: %d" % (len(CASES), len(fails)))
    for cmd, want, got, why, err in fails:
        print("  %s: ожидал %s, получил %s\n    команда: %s%s"
              % (why, want, got, cmd, ("\n    stderr: " + err) if err else ""))
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
