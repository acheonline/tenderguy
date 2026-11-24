package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/rules"
	"tenderguy/internal/trace"
)

// Тесты ниже закрывают ветки check.go, недостижимые из TestRunCheck с пустым
// каталогом: цикл checkPackage по разобранным документам, секцию Failed
// (ошибка разбора → код 1), печать замечаний в writeCheckText и severityLabel.
// Фикстуры синтетические: .docx собирает writeDocxParts из parse_test.go,
// «битый» файл — мусорные байты с расширением .docx.

// TestRunCheckParsesPackage — успешный путь checkPackage: в каталоге валидный
// .docx, движок его разбирает, замечаний и ошибок нет, код выхода 0.
func TestRunCheckParsesPackage(t *testing.T) {
	prepare := func(t *testing.T) (dir, rulesPath string) {
		t.Helper()
		dir = t.TempDir()
		writeDocxFile(t, dir, "заявка.docx", textParagraph("Поставка RedCheck")+tableFixture())
		return dir, writeCheckRules(t, `{"version": 1}`)
	}

	tests := []struct {
		name       string
		extra      []string
		wantExit   int
		wantOut    []string
		noOut      []string
		verifyJSON func(t *testing.T, stdout string)
	}{
		{
			name:     "text: валидный .docx разобран, ошибок разбора нет, код 0",
			wantExit: exitOK,
			wantOut: []string{
				"Правил в конфиге: 0",
				"Сводка: замечаний нет — пакет проходит проверку",
				"Вердикт: блокирующих замечаний нет",
				"Примечание: проверять нечего",
				// В конфиге нет правил, поэтому оба зарегистрированных класса
				// показаны непроверенными с причиной (критерий P2.6).
				"Непроверенные классы:",
				"reqs — нет правил класса в конфиге",
				"composition — нет правил класса в конфиге",
			},
			noOut: []string{"Ошибки разбора", "Замечания ["},
		},
		{
			name:     "json: failed пуст, код 0",
			extra:    []string{"--format", "json"},
			wantExit: exitOK,
			verifyJSON: func(t *testing.T, stdout string) {
				var rep rules.Report
				if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
					t.Fatalf("JSON не читается: %v\n%s", err, stdout)
				}
				if len(rep.Failed) != 0 {
					t.Errorf("failed = %+v, хотели пустой список", rep.Failed)
				}
				if len(rep.Findings) != 0 {
					t.Errorf("findings = %+v, хотели пустой список", rep.Findings)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, rulesPath := prepare(t)
			args := []string{"--command", "check", "--input-dir", dir, "--rules", rulesPath}
			args = append(args, tc.extra...)

			code, stdout, stderr := checkRun(args)
			if code != tc.wantExit {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantExit, stdout, stderr)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(stdout, want) {
					t.Errorf("в stdout нет %q\n%s", want, stdout)
				}
			}
			for _, notWant := range tc.noOut {
				if strings.Contains(stdout, notWant) {
					t.Errorf("в stdout не должно быть %q\n%s", notWant, stdout)
				}
			}
			if strings.Contains(stderr, "вход неполон") {
				t.Errorf("stderr сообщает о неполном входе при разобранном пакете: %s", stderr)
			}
			if tc.verifyJSON != nil {
				tc.verifyJSON(t, stdout)
			}
		})
	}
}

// TestRunCheckBrokenDocx — ветка Failed в checkPackage: обход признаёт файл
// документом Word, но парсер его не открывает. Мусорные байты с расширением
// .docx не годятся: классификация по содержимому уводит их в неподдерживаемый
// тип, и checkPackage такой файл пропускает. Нужен zip с word/document.xml,
// но без части word/_rels/document.xml.rels — приём из parse_test.go
// (writeDocxParts с withRels=false): обход считает его .docx, библиотека —
// не открывает. Пакет неполон → код 1, в text-отчёте секция «Ошибки разбора»,
// в json — непустой failed с ключами path и error.
func TestRunCheckBrokenDocx(t *testing.T) {
	prepare := func(t *testing.T) (dir, broken, rulesPath string) {
		t.Helper()
		dir = t.TempDir()
		broken = writeDocxParts(t, dir, "битый.docx", false, textParagraph("Текст есть, связей нет"))
		return dir, broken, writeCheckRules(t, `{"version": 1}`)
	}

	t.Run("text: секция Ошибки разбора и код 1", func(t *testing.T) {
		dir, _, rulesPath := prepare(t)
		code, stdout, stderr := checkRun([]string{
			"--command", "check", "--input-dir", dir, "--rules", rulesPath,
		})
		if code != exitProblems {
			t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", code, exitProblems, stdout, stderr)
		}
		for _, want := range []string{
			"Ошибки разбора:",
			"битый.docx —",
			"Вердикт: блокирующих замечаний нет",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("в stdout нет %q\n%s", want, stdout)
			}
		}
		if !strings.Contains(stderr, "вход неполон") {
			t.Errorf("в stderr нет предупреждения о неполном входе: %s", stderr)
		}
	})

	t.Run("json: failed — непустой массив с path и error", func(t *testing.T) {
		dir, broken, rulesPath := prepare(t)
		code, stdout, stderr := checkRun([]string{
			"--command", "check", "--input-dir", dir, "--rules", rulesPath, "--format", "json",
		})
		if code != exitProblems {
			t.Fatalf("код выхода = %d, хотели %d\nstderr:\n%s", code, exitProblems, stderr)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			t.Fatalf("JSON не читается: %v\n%s", err, stdout)
		}
		var failed []map[string]string
		if err := json.Unmarshal(raw["failed"], &failed); err != nil {
			t.Fatalf("поле failed не массив объектов: %v\n%s", err, raw["failed"])
		}
		if len(failed) != 1 {
			t.Fatalf("failed содержит %d записей, хотели 1: %s", len(failed), raw["failed"])
		}
		entry := failed[0]
		if entry["path"] != broken {
			t.Errorf("path = %q, хотели %q", entry["path"], broken)
		}
		if _, ok := entry["error"]; !ok {
			t.Errorf("в записи failed нет ключа error: %+v", entry)
		} else if entry["error"] == "" {
			t.Errorf("error = %q, хотели непустую причину", entry["error"])
		}
	})
}

// TestSeverityLabel — человекочитаемые названия уровней критичности.
func TestSeverityLabel(t *testing.T) {
	tests := []struct {
		name string
		in   rules.Severity
		want string
	}{
		{name: "blocking", in: rules.SeverityBlocking, want: "БЛОКИРУЮЩЕЕ"},
		{name: "warning", in: rules.SeverityWarning, want: "предупреждение"},
		{name: "неизвестный уровень печатается как есть", in: rules.Severity("fatal"), want: "fatal"},
		{name: "пустой уровень", in: rules.Severity(""), want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := severityLabel(tc.in); got != tc.want {
				t.Errorf("severityLabel(%q) = %q, хотели %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestWriteCheckTextFindings — печать отчёта с замечаниями напрямую: движок
// P2.1 не имеет зарегистрированных классов, поэтому замечания через CLI
// недостижимы, а ветки группировки по классам, цитаты, адреса и сводка
// обязаны быть проверены.
func TestWriteCheckTextFindings(t *testing.T) {
	docxRef := trace.Ref{File: "/пакет/заявка.docx", Format: trace.FormatDocx, Page: 1, Para: 2}
	xlsxRef := trace.Ref{File: "/пакет/лист.xlsx", Format: trace.FormatXlsx, Sheet: "Sheet1", Cell: "B7"}

	tests := []struct {
		name    string
		rep     rules.Report
		want    []string
		notWant []string
		// earlier/later — пара строк, порядок которых в отчёте обязан
		// соблюдаться (группировка по классам: pkg.compose раньше reqs.compare).
		earlier, later string
	}{
		{
			name: "блокирующее и предупреждение, ошибки разбора",
			rep: rules.Report{
				InputDir:   "/пакет",
				RulesTotal: 2,
				Blocking:   1,
				Warnings:   1,
				Summary: []rules.ClassSummary{
					{Class: "pkg.compose", Total: 1, Warnings: 1},
					{Class: "reqs.compare", Total: 1, Blocking: 1},
				},
				Findings: []rules.Finding{
					{
						Rule:     "r1",
						Class:    "reqs.compare",
						Severity: rules.SeverityBlocking,
						Text:     "ИНН не совпадает с карточкой",
						Quote:    "ИНН 7714567890",
						Refs:     []trace.Ref{docxRef},
					},
					{
						Rule:     "r2",
						Class:    "pkg.compose",
						Severity: rules.SeverityWarning,
						Text:     "значение выглядит устаревшим",
						Refs:     []trace.Ref{xlsxRef},
					},
				},
				Failed: []rules.Failed{{Path: "/пакет/битый.docx", Error: "не открылся как .docx"}},
			},
			want: []string{
				"Каталог: /пакет",
				"Правил в конфиге: 2",
				"Сводка: всего замечаний 2, блокирующих 1, предупреждений 1",
				"pkg.compose — всего 1, блокирующих 0, предупреждений 1",
				"reqs.compare — всего 1, блокирующих 1, предупреждений 0",
				"Вердикт: ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ: 1",
				"Замечания [reqs.compare]:",
				"Замечания [pkg.compose]:",
				"БЛОКИРУЮЩЕЕ",
				"reqs.compare",
				"r1 — ИНН не совпадает с карточкой",
				"цитата: ИНН 7714567890",
				"адрес: /пакет/заявка.docx · стр. 1 · абз. 2",
				"предупреждение",
				"pkg.compose",
				"r2 — значение выглядит устаревшим",
				"адрес: /пакет/лист.xlsx · Sheet1!B7",
				"Ошибки разбора:",
				"битый.docx — не открылся как .docx",
			},
			notWant: []string{"Примечание:", "Непроверенные классы:"},
			earlier: "Замечания [pkg.compose]:",
			later:   "Замечания [reqs.compare]:",
		},
		{
			name: "только предупреждение — вердикт без блокирующих, примечание и непроверенный класс напечатаны",
			rep: rules.Report{
				InputDir:   "/пакет",
				RulesTotal: 1,
				Warnings:   1,
				Note:       "проверять нечего",
				Summary:    []rules.ClassSummary{{Class: "fresh.date", Total: 1, Warnings: 1}},
				Skipped:    []rules.Skipped{{Class: "composition", Reason: "нет правил класса в конфиге"}},
				Findings: []rules.Finding{
					{
						Rule:     "r1",
						Class:    "fresh.date",
						Severity: rules.SeverityWarning,
						Text:     "дата старше месяца",
						Quote:    "01.10.2026",
						Refs:     []trace.Ref{docxRef},
					},
				},
				Failed: []rules.Failed{},
			},
			want: []string{
				"Вердикт: блокирующих замечаний нет",
				"Примечание: проверять нечего",
				"Сводка: всего замечаний 1, блокирующих 0, предупреждений 1",
				"Непроверенные классы:",
				"composition — нет правил класса в конфиге",
				"предупреждение",
				"цитата: 01.10.2026",
			},
			notWant: []string{"ЕСТЬ БЛОКИРУЮЩИЕ", "Ошибки разбора:", "замечаний нет — пакет проходит проверку"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeCheckText(&buf, tc.rep)
			out := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("в отчёте нет %q\n%s", want, out)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(out, notWant) {
					t.Errorf("в отчёте не должно быть %q\n%s", notWant, out)
				}
			}
			if tc.earlier != "" {
				i, j := strings.Index(out, tc.earlier), strings.Index(out, tc.later)
				if i < 0 || j < 0 || i > j {
					t.Errorf("%q обязано идти раньше %q\n%s", tc.earlier, tc.later, out)
				}
			}
		})
	}
}

// TestRunCheckMarkdownReport — check --format markdown пишет report.md в
// --output-dir (каталог создаётся, если его нет), в консоль печатает краткий
// итог, а повторный прогон по тому же входу даёт побайтово тот же файл
// (детерминизм отчёта, критерий P2.6).
func TestRunCheckMarkdownReport(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(t *testing.T) (inputDir, rulesPath string)
		wantExit   int
		wantStdout []string
		wantFile   []string
	}{
		{
			name: "конфиг без правил — итоговая строка и непроверенные классы",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, `{"version": 1}`)
			},
			wantExit: exitOK,
			wantStdout: []string{
				"Вердикт: блокирующих замечаний нет",
			},
			wantFile: []string{
				"# Отчёт о проверке пакета",
				"## Сводка",
				"Замечаний нет — пакет проходит проверку.",
				"## Непроверенные классы",
				"`reqs` — нет правил класса в конфиге",
				"`composition` — нет правил класса в конфиге",
			},
		},
		{
			name: "чужие реквизиты — замечания по классам, сводка-таблица и адреса",
			prepare: func(t *testing.T) (string, string) {
				return reqsDocxDir(t,
						"Полное наименование организации: ЗАО «Вектор» ИНН: 1234567894"),
					reqsRulesConfig(t)
			},
			wantExit: exitProblems,
			wantStdout: []string{
				"Вердикт: ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ",
			},
			wantFile: []string{
				"Всего замечаний:",
				"| Класс | Всего | Блокирующих | Предупреждений |",
				"| reqs |",
				"## Замечания: класс `reqs`",
				"**БЛОКИРУЮЩЕЕ**",
				"цитата:",
				"адрес: `",
				"## Непроверенные классы",
				"`composition` — нет правил класса в конфиге",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, rulesPath := tc.prepare(t)
			outDir := filepath.Join(t.TempDir(), "вложенный", "отчёт")
			args := []string{
				"--command", "check", "--input-dir", dir,
				"--rules", rulesPath, "--format", "markdown", "--output-dir", outDir,
			}

			code, stdout, stderr := checkRun(args)
			if code != tc.wantExit {
				t.Fatalf("код = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantExit, stdout, stderr)
			}
			if !strings.Contains(stdout, "Отчёт: "+filepath.Join(outDir, checkReportFileName)) {
				t.Errorf("в stdout нет пути к отчёту: %s", stdout)
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout, want) {
					t.Errorf("в stdout нет %q: %s", want, stdout)
				}
			}

			path := filepath.Join(outDir, checkReportFileName)
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("report.md не записан: %v", err)
			}
			for _, want := range tc.wantFile {
				if !strings.Contains(string(first), want) {
					t.Errorf("в report.md нет %q:\n%s", want, first)
				}
			}

			if code, _, stderr = checkRun(args); code != tc.wantExit {
				t.Fatalf("код повторного прогона = %d, stderr: %s", code, stderr)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("report.md не читается после повторного прогона: %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Errorf("повторный прогон не детерминирован:\n%s\n---\n%s", first, second)
			}
		})
	}
}

// TestWriteCheckMarkdownSections — ветви markdown-отчёта, недостижимые через
// CLI-прогоны выше одной фикстурой: примечание, таблица состава, непроверенный
// класс, замечание с адресом и ошибки разбора.
func TestWriteCheckMarkdownSections(t *testing.T) {
	docxRef := trace.Ref{File: "/пакет/опись.docx", Format: trace.FormatDocx, Page: 1, Para: 2}
	rep := rules.Report{
		InputDir:    "/пакет",
		RulesTotal:  1,
		Blocking:    1,
		Note:        "тестовое примечание",
		Summary:     []rules.ClassSummary{{Class: "composition", Total: 1, Blocking: 1}},
		Composition: []rules.CompositionStatus{{Item: "c1", Title: "Договор", Required: true, Present: true, Files: []string{"договор.docx"}}},
		Skipped:     []rules.Skipped{{Class: "freshness", Reason: "в каталоге нет документов с датами"}},
		Findings: []rules.Finding{{
			Rule:     "r1",
			Class:    "composition",
			Severity: rules.SeverityBlocking,
			Text:     "нет документа в составе",
			Quote:    "Договор поставки",
			Refs:     []trace.Ref{docxRef},
		}},
		Failed: []rules.Failed{{Path: "/пакет/битый.docx", Error: "не открылся как .docx"}},
	}

	path, err := writeCheckMarkdown(t.TempDir(), rep)
	if err != nil {
		t.Fatalf("writeCheckMarkdown: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("отчёт не читается: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"- Примечание: тестовое примечание",
		"| Класс | Всего | Блокирующих | Предупреждений |",
		"| composition | 1 | 1 | 0 |",
		"## Состав пакета",
		"| c1 | Договор | да | есть | договор.docx |",
		"## Непроверенные классы",
		"- `freshness` — в каталоге нет документов с датами",
		"## Замечания: класс `composition`",
		"- **БЛОКИРУЮЩЕЕ** · класс `composition` · правило `r1` — нет документа в составе",
		"  - цитата: Договор поставки",
		"  - адрес: `/пакет/опись.docx · стр. 1 · абз. 2`",
		"## Ошибки разбора",
		"- `битый.docx` — не открылся как .docx",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в report.md нет %q:\n%s", want, out)
		}
	}
}
