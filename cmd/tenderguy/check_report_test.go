package main

import (
	"bytes"
	"encoding/json"
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
				"Замечаний: 0, блокирующих 0, предупреждений 0",
				"Вердикт: блокирующих замечаний нет",
				"Примечание: проверять нечего",
			},
			noOut: []string{"Ошибки разбора", "Замечания:"},
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
// недостижимы, а ветки «Замечания:», цитаты и адреса обязаны быть проверены.
func TestWriteCheckTextFindings(t *testing.T) {
	docxRef := trace.Ref{File: "/пакет/заявка.docx", Format: trace.FormatDocx, Page: 1, Para: 2}
	xlsxRef := trace.Ref{File: "/пакет/лист.xlsx", Format: trace.FormatXlsx, Sheet: "Sheet1", Cell: "B7"}

	tests := []struct {
		name    string
		rep     rules.Report
		want    []string
		notWant []string
	}{
		{
			name: "блокирующее и предупреждение, ошибки разбора",
			rep: rules.Report{
				InputDir:   "/пакет",
				RulesTotal: 2,
				Blocking:   1,
				Warnings:   1,
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
				"Замечаний: 2, блокирующих 1, предупреждений 1",
				"Вердикт: ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ: 1",
				"Замечания:",
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
			notWant: []string{"Примечание:"},
		},
		{
			name: "только предупреждение — вердикт без блокирующих, примечание напечатано",
			rep: rules.Report{
				InputDir:   "/пакет",
				RulesTotal: 1,
				Warnings:   1,
				Note:       "проверять нечего",
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
				"предупреждение",
				"цитата: 01.10.2026",
			},
			notWant: []string{"ЕСТЬ БЛОКИРУЮЩИЕ", "Ошибки разбора:"},
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
		})
	}
}
