# tenderguy

Система подготовки и проверки пакета документов тендерных заявок (44-ФЗ/223-ФЗ).
Проект в предпроектной фазе: артефакты — `tz.md` (проблематика), `analyze*.md` (аналитика),
`issues.md` (декомпозиция), `mvp.md`, `poc.md`. Go-модуль (`go.mod`) — каркас будущей
реализации задач P1.x.

## CI и бот Qwen Code — как проверить

В репозитории три workflow:

| Файл | Имя в Actions | Назначение |
|---|---|---|
| `.github/workflows/qwen.yml` | Qwen Code (self-hosted VPS) | бот `@qwencoder` в комментариях issue |
| `.github/workflows/ci.yml` | Go CI | golangci-lint + build + test |
| `.github/workflows/autofix.yml` | Auto-Fix on Failure | Qwen чинит упавший Go CI |

### Требования

- Self-hosted runner с метками `[self-hosted, vps]` (VPS, пользователь `runner`);
  на нём установлены `qwen` (0.27.4+), `git`, `gh`, `go` (версия из `go.mod`).
- Секреты репозитория (Settings → Secrets and variables → Actions):
  - `QWEN_API_KEY` — ключ QwenCloud Token Plan (`sk-sp-…`), **обязателен**;
  - `AUTOFIX_PAT` — PAT с правами Contents: R/W, Actions: Read, Pull requests: R/W,
    **для автоперезапуска Go CI после автофикса** (без него фикс пушится через
    `GITHUB_TOKEN`, и CI придётся перезапускать вручную — Re-run).

### 1. Проверка бота `@qwencoder`

1. Откройте любой issue (например, задачу `[P1.x]`).
2. Оставьте комментарий (только от аккаунта `acheonline`):
   ```
   @qwencoder реализуй задачу из этого issue
   ```
3. Вкладка **Actions** → run «Qwen Code (self-hosted VPS)»: шаг «Run Qwen Code»
   выполняет задачу, вывод — в логе и в Summary run'а.
4. Результат: ветка `qwen/issue-<N>-<run_id>`, PR в `main` и комментарий
   со ссылкой на PR в issue. Если изменений нет — PR не создаётся.

### 2. Проверка Go CI

Пока в репозитории нет `.go`-файлов, шаги lint/build/test пропускаются
(guard «Detect Go sources»), run зелёный. Для полной проверки добавьте в любой ветке
два файла и откройте PR:

`calc/calc.go` — намеренно с ошибкой:
```go
package calc

// Add возвращает сумму a и b.
func Add(a, b int) int {
	return a - b // BUG: должно быть a + b
}
```

`calc/calc_test.go`:
```go
package calc

import "testing"

func TestAdd(t *testing.T) {
	tests := []struct {
		name      string
		a, b, sum int
	}{
		{"positive", 2, 3, 5},
		{"zero", 0, 0, 0},
		{"negative", -1, -2, -3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Add(tt.a, tt.b); got != tt.sum {
				t.Errorf("Add(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.sum)
			}
		})
	}
}
```

Push в ветку запустит «Go CI» → шаг Test упадёт (красный run).

### 3. Проверка Auto-Fix

Продолжение сценария выше — **делать ничего не нужно**, всё автоматически:

1. После падения «Go CI» стартует run «Auto-Fix on Failure» на self-hosted runner.
2. Qwen читает логи упавших шагов (`gh run view --log-failed`), правит код
   (в примере — заменит `a - b` на `a + b`) и локально проверяет
   `go build/vet/test`.
3. Шаг «Commit and push fix» пушит в ту же ветку коммит
   `fix: [auto-fix] исправление после падения Go CI run #<id>`.
4. Если задан `AUTOFIX_PAT`, push перезапускает «Go CI» → run становится зелёным,
   бот оставляет комментарий/PR (при падении в `main` — создаёт ветку
   `autofix/run-<id>` и PR вместо прямого пуша).

Где смотреть: Actions → «Auto-Fix on Failure» → Summary («Qwen autofix output»).

### Защита от зацикливания

- Автофикс **не запускается**, если сообщение head-коммита уже содержит `[auto-fix]`
  (одна автоматическая попытка на цепочку падений).
- Не обрабатываются падения в кодах форков (`head_repository != repository`).
- Все job'ы Qwen сериализованы через `concurrency: qwen-vps` (один runner).

### Локальная разработка (Go)

```bash
go build ./...
go test -race ./...
golangci-lint run   # правила в .golangci.yml
```

Правила кода и коммитов — в `CONTRIBUTING.md`.
