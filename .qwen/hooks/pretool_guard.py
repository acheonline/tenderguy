#!/usr/bin/env python3
"""PreToolUse: блокирует операции, которые агент не имеет права выполнять. Fail-closed.

Два принципа, которые надо помнить при правке этого файла:

1. Матчинг по сегментам и токенам, а не по подстроке всей команды: опасный текст внутри
   литерала (echo, printf, фикстура теста, документация про git) не должен блокировать вызов.
2. Глобальные опции git (-C, -c, --git-dir, …) разбираются как опции, иначе
   `git -C dir push origin main` обходил бы гейт.

Тесты гейта: python3 .qwen/hooks/pretool_guard_test.py
"""
import json
import re
import shlex
import subprocess
import sys

SECRET_PATH = re.compile(r"(\.env(\.[a-z]+)?$|secrets?\.[a-z]+$|credentials?\.json$|\.pem$|token)", re.I)
TASK_MSG = re.compile(r"\[P\d")
WRAPPERS = ("sudo", "env", "command", "time", "nice", "nohup")
SEG_SPLIT = re.compile(r"&&|\|\||;|\||\n")
# опции git, у которых есть отдельный токен-значение
GIT_VALUED_OPTS = {"-C", "-c", "--git-dir", "--work-tree", "--namespace",
                   "--exec-path", "--super-prefix"}


def emit(reason=None):
    out = {"hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny" if reason else "allow",
        "permissionDecisionReason": reason or "гейты tenderguy: возражений нет"}}
    print(json.dumps(out, ensure_ascii=False))
    sys.exit(0)


def tokens_of(segment):
    """Токены команды без обёрток (sudo/env/…) и префикса VAR=значение."""
    try:
        toks = shlex.split(segment)
    except ValueError:
        toks = segment.split()
    i = 0
    while i < len(toks) and (re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", toks[i]) or toks[i] in WRAPPERS):
        i += 1
    head = toks[i:]
    while head and head[0].startswith("-"):   # обёртка с флагами: env -i git …
        head = head[1:]
    return head


def git_sub_and_args(head):
    """Подкоманда git и её аргументы — с пропуском глобальных опций."""
    i = 1
    while i < len(head) and head[i].startswith("-"):
        opt = head[i].split("=")[0]
        i += 2 if (opt in GIT_VALUED_OPTS and "=" not in head[i]) else 1
    if i >= len(head):
        return "", []
    return head[i], head[i + 1:]


def readme_changed(cwd):
    """Изменён ли README.md относительно HEAD (в индексе или рабочем дереве).

    Упоминание README.md в git add ничего не доказывает: файл мог не меняться, а конвенция
    требует именно правку раздела «Локальный запуск» в том же PR.
    """
    try:
        staged = subprocess.run(["git", "diff", "--cached", "--name-only"], cwd=cwd,
                                capture_output=True, text=True, timeout=15)
        if staged.returncode == 0 and "README.md" in staged.stdout.split():
            return True
        dirty = subprocess.run(["git", "status", "--porcelain", "--", "README.md"], cwd=cwd,
                               capture_output=True, text=True, timeout=15)
        return dirty.returncode == 0 and bool(dirty.stdout.strip())
    except Exception:
        return False


def check_git(head, cwd):
    sub, args = git_sub_and_args(head)

    if sub == "push":
        if any(t in ("--force", "--force-with-lease", "-f") for t in args):
            return "force-push перезаписывает историю удалённого репозитория — только человек"
        if "--delete" in args:
            return "git push --delete удаляет удалённую ветку — только человек"
        pushed = [args[i + 1] for i, t in enumerate(args[:-1])
                  if t in ("origin", "upstream")]
        if any(b in ("main", "master") for b in pushed):
            return "push в main запрещён: изменение попадает в main только через PR после вашего мержа"
        return None

    if sub == "commit":
        if "--no-verify" in args:
            return "--no-verify обходит локальные гейты"
        if "--amend" in args:
            return "git commit --amend переписывает историю — только человек"
        if TASK_MSG.search(" ".join(args)) and not readme_changed(cwd):
            return ("Коммит задачи [P*] без изменения README.md. По конвенции в тот же PR входит раздел "
                    "«Локальный запуск» с командой, флагами, примером и ожидаемым результатом — сначала "
                    "правка README.md, затем git add README.md, затем коммит.")
        return None

    if sub == "add":
        for t in args:
            if t.startswith("docs/") or t == "docs":
                return ("docs/ — клиентские тендерные документы заказчиков. В индекс они не "
                        "добавляются, даже с -f; фикстуры тестов — base64-константы в _test.go.")
            if SECRET_PATH.search(t):
                return ("В индекс попадает файл, похожий на секрет (.env, ключевой токен, .pem). "
                        "Значения ключей не коммитятся.")
        return None

    if sub == "reset" and "--hard" in args:
        return "git reset --hard затирает незакоммиченные изменения — только человек"
    if sub == "clean" and any(t.startswith("-") and "f" in t for t in args):
        return "git clean -f удаляет untracked-файлы"
    if sub in ("rebase", "filter-branch"):
        return "переписывание истории (%s) — только человек" % sub
    if sub == "branch" and "-D" in args:
        return "удаление ветки без слияния (-D) — только человек"
    if sub == "stash" and args and args[0] in ("drop", "clear"):
        return "stash drop/clear теряет спрятанные изменения — только человек"
    if sub == "checkout" and ({"-f", "--force"} & set(args) or "." in args):
        return "git checkout с принудительной перезаписью рабочего дерева — только человек"
    if sub == "restore" and (any(a.startswith("--source") for a in args) or "." in args):
        return "git restore перезаписывает незакоммиченные правки — только человек"
    return None


def main():
    try:
        ev = json.load(sys.stdin)
    except Exception:
        emit("событие PreToolUse не разобрано — блокируем (fail-closed)")

    if ev.get("tool_name") != "run_shell_command":
        emit()

    cmd = (ev.get("tool_input") or {}).get("command") or ""
    cwd = ev.get("cwd") or "."

    for segment in SEG_SPLIT.split(cmd):
        head = tokens_of(segment)
        if not head:
            continue
        if head[0] == "rm" and any(re.match(r"^-[a-z]*[rf][a-z]*$", t) for t in head[1:]):
            emit("Рекурсивное удаление файлов (rm -rf) — только человек")
        if head[0] == "git":
            reason = check_git(head, cwd)
            if reason:
                emit("Заблокировано гейтом: %s\nСегмент: %s" % (reason, segment.strip()[:160]))

    emit()


if __name__ == "__main__":
    main()
