package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCheckRules пишет JSON-конфиг во временный каталог и возвращает путь.
func writeCheckRules(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("запись файла правил: %v", err)
	}
	return path
}

// checkRun запускает CLI и возвращает код выхода, stdout и stderr.
func checkRun(args []string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunCheck(t *testing.T) {
	emptyRules := `{"version": 1}`

	tests := []struct {
		name       string
		prepare    func(t *testing.T) (inputDir, rulesPath string)
		extra      []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		verifyJSON func(t *testing.T, stdout string)
	}{
		{
			name: "пустой каталог и конфиг без правил — код 0, примечание в text",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, emptyRules)
			},
			wantExit: exitOK,
			wantOut:  []string{"проверять нечего", "Правил в конфиге: 0", "Вердикт: блокирующих замечаний нет"},
		},
		{
			name: "пустой каталог и конфиг без правил — код 0, note в json",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, emptyRules)
			},
			extra:    []string{"--format", "json"},
			wantExit: exitOK,
			verifyJSON: func(t *testing.T, stdout string) {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
					t.Fatalf("JSON не читается: %v\n%s", err, stdout)
				}
				if string(raw["note"]) != `"проверять нечего"` {
					t.Errorf("note = %s, хотели %q", raw["note"], "проверять нечего")
				}
				if string(raw["findings"]) != "[]" {
					t.Errorf("findings = %s, хотели []", raw["findings"])
				}
				if string(raw["failed"]) != "[]" {
					t.Errorf("failed = %s, хотели []", raw["failed"])
				}
			},
		},
		{
			name: "json-отчёт: findings и failed — пустые массивы, не null",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, emptyRules)
			},
			extra:    []string{"--format", "json"},
			wantExit: exitOK,
			verifyJSON: func(t *testing.T, stdout string) {
				if strings.Contains(stdout, `"findings": null`) {
					t.Error("findings = null, хотели []")
				}
				if strings.Contains(stdout, `"failed": null`) {
					t.Error("failed = null, хотели []")
				}
				if !strings.Contains(stdout, `"findings": []`) {
					t.Errorf("в JSON нет \"findings\": []\n%s", stdout)
				}
				if !strings.Contains(stdout, `"failed": []`) {
					t.Errorf("в JSON нет \"failed\": []\n%s", stdout)
				}
			},
		},
		{
			name: "нет --rules — код 2",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), ""
			},
			wantExit:   exitUsage,
			wantErrOut: []string{"требует --rules"},
		},
		{
			name: "нет --input-dir — код 2",
			prepare: func(t *testing.T) (string, string) {
				return "", writeCheckRules(t, emptyRules)
			},
			wantExit:   exitUsage,
			wantErrOut: []string{"требует --input-dir"},
		},
		{
			name: "--with-text при check — код 2",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, emptyRules)
			},
			extra:      []string{"--with-text"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--with-text не используется в check"},
		},
		{
			name: "--file при check — код 2",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, emptyRules)
			},
			extra:      []string{"--file", "документ.docx"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--file не используется в check"},
		},
		{
			name: "несуществующий файл --rules — код 3",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "нет-правил.json")
			},
			wantExit:   exitBadPath,
			wantErrOut: []string{"не удалось прочитать файл правил"},
		},
		{
			name: "несуществующий --input-dir — код 3",
			prepare: func(t *testing.T) (string, string) {
				return filepath.Join(t.TempDir(), "нет-каталога"), writeCheckRules(t, emptyRules)
			},
			wantExit:   exitBadPath,
			wantErrOut: []string{"tenderguy:"},
		},
		{
			name: "битый JSON конфига — код 2",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, `{не json`)
			},
			wantExit:   exitUsage,
			wantErrOut: []string{"битый JSON"},
		},
		{
			name: "неверная версия конфига — код 2",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, `{"version": 99}`)
			},
			wantExit:   exitUsage,
			wantErrOut: []string{"version = 99", "только версия 1"},
		},
		{
			name: "конфиг с незарегистрированным классом — код 2, сообщение называет класс",
			prepare: func(t *testing.T) (string, string) {
				rules := `{"version": 1, "rules": [{"id": "r1", "class": "reqs.compare", "severity": "blocking"}]}`
				return t.TempDir(), writeCheckRules(t, rules)
			},
			wantExit:   exitUsage,
			wantErrOut: []string{`"reqs.compare"`, "неизвестно движку"},
		},
		{
			name: "неизвестный --format — код 2",
			prepare: func(t *testing.T) (string, string) {
				return t.TempDir(), writeCheckRules(t, emptyRules)
			},
			extra:      []string{"--format", "yaml"},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --format "yaml"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputDir, rulesPath := tc.prepare(t)
			args := []string{"--command", "check"}
			if inputDir != "" {
				args = append(args, "--input-dir", inputDir)
			}
			if rulesPath != "" {
				args = append(args, "--rules", rulesPath)
			}
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
			for _, want := range tc.wantErrOut {
				if !strings.Contains(stderr, want) {
					t.Errorf("в stderr нет %q\n%s", want, stderr)
				}
			}
			if tc.verifyJSON != nil {
				tc.verifyJSON(t, stdout)
			}
		})
	}
}

// reqsRulesConfig — конфиг класса «реквизиты» (P2.3): эталон поставщика и
// одно правило класса reqs.
func reqsRulesConfig(t *testing.T) string {
	t.Helper()

	return writeCheckRules(t, `{"version": 1,
		"orgs": [{"id": "supplier", "name": "ООО «Ромашка»", "inn": "7707083893"}],
		"rules": [{"id": "reqs-1", "class": "reqs", "severity": "blocking"}]}`)
}

// reqsDocxDir собирает пакет из двух .docx: заявка с реквизитами эталона и
// договор с чужими ИНН и наименованием.
func reqsDocxDir(t *testing.T, secondBody string) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "заявка.docx",
		textParagraph("Полное наименование организации: ООО «Ромашка»")+
			textParagraph("ИНН: 7707083893"))
	writeDocxFile(t, dir, "договор.docx", textParagraph(secondBody))
	return dir
}

// TestRunCheckReqsClass — сквозной прогон класса «реквизиты» через CLI:
// совпадающий пакет даёт код 0, пакет с чужими реквизитами — код 1 и
// замечания со значением из документа, значением эталона и адресом в источнике.
func TestRunCheckReqsClass(t *testing.T) {
	t.Run("совпадающий пакет — код 0", func(t *testing.T) {
		dir := reqsDocxDir(t,
			"Полное наименование организации: ООО «Ромашка» ИНН: 7707083893")
		code, stdout, stderr := checkRun([]string{
			"--command", "check", "--input-dir", dir,
			"--rules", reqsRulesConfig(t),
		})
		if code != exitOK {
			t.Errorf("код = %d, хотели %d\nstdout:\n%s\nstderr: %s", code, exitOK, stdout, stderr)
		}
		if !strings.Contains(stdout, "Вердикт: блокирующих замечаний нет") {
			t.Errorf("в stdout нет вердикта:\n%s", stdout)
		}
		if strings.Contains(stdout, "не совпадает с эталоном") {
			t.Errorf("совпадающий пакет дал замечание сверки:\n%s", stdout)
		}
	})

	t.Run("чужие реквизиты — код 1, замечание со значением, эталоном и адресом", func(t *testing.T) {
		dir := reqsDocxDir(t,
			"Полное наименование организации: ЗАО «Вектор» ИНН: 1234567894")
		rules := reqsRulesConfig(t)
		code, stdout, stderr := checkRun([]string{
			"--command", "check", "--input-dir", dir,
			"--rules", rules, "--format", "json",
		})
		if code != exitProblems {
			t.Errorf("код = %d, хотели %d\nstdout:\n%s\nstderr: %s", code, exitProblems, stdout, stderr)
		}

		var rep struct {
			Blocking int `json:"blocking"`
			Findings []struct {
				Rule     string `json:"rule"`
				Class    string `json:"class"`
				Severity string `json:"severity"`
				Text     string `json:"text"`
				Refs     []struct {
					File string `json:"file"`
					Para int    `json:"para"`
				} `json:"refs"`
			} `json:"findings"`
		}
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("JSON отчёта не читается: %v\n%s", err, stdout)
		}
		if rep.Blocking == 0 {
			t.Fatalf("blocking = 0, хотели >0:\n%s", stdout)
		}

		var innMismatch, nameMismatch, inconsistent bool
		for _, f := range rep.Findings {
			if f.Rule != "reqs-1" || f.Class != "reqs" {
				t.Errorf("rule/class = %q/%q, хотели reqs-1/reqs", f.Rule, f.Class)
			}
			if f.Severity != "blocking" && f.Severity != "warning" {
				t.Errorf("severity = %q — допустимы blocking или warning", f.Severity)
			}
			if len(f.Refs) == 0 || f.Refs[0].File == "" || f.Refs[0].Para == 0 {
				t.Errorf("у замечания нет полного адреса в источнике: %+v", f.Refs)
			}
			switch {
			case strings.Contains(f.Text, "различается между документами"):
				inconsistent = true
				for _, want := range []string{"заявка.docx", "договор.docx"} {
					if !strings.Contains(f.Text, want) {
						t.Errorf("в тексте консистентности нет %q: %s", want, f.Text)
					}
				}
			case strings.Contains(f.Text, "не совпадает с эталоном"):
				// Сверка с эталоном идёт по каждому виду реквизита отдельно:
				// наименование и ИНН дают собственные замечания.
				if strings.Contains(f.Text, "наименование") {
					nameMismatch = true
					for _, want := range []string{"ЗАО «Вектор»", "ООО «Ромашка»"} {
						if !strings.Contains(f.Text, want) {
							t.Errorf("в тексте сверки наименования нет %q: %s", want, f.Text)
						}
					}
				}
				if strings.Contains(f.Text, "ИНН") {
					innMismatch = true
					for _, want := range []string{"1234567894", "7707083893"} {
						if !strings.Contains(f.Text, want) {
							t.Errorf("в тексте сверки ИНН нет %q: %s", want, f.Text)
						}
					}
				}
			}
		}
		if !innMismatch {
			t.Errorf("нет замечания сверки ИНН с эталоном:\n%s", stdout)
		}
		if !nameMismatch {
			t.Errorf("нет замечания сверки наименования с эталоном:\n%s", stdout)
		}
		if !inconsistent {
			t.Errorf("нет замечания внутренней консистентности:\n%s", stdout)
		}
	})
}

// TestRunCheckOutputDirRejected — --output-dir не принадлежит check.
func TestRunCheckOutputDirRejected(t *testing.T) {
	inputDir := t.TempDir()
	rules := writeCheckRules(t, `{"version": 1}`)
	outDir := filepath.Join(t.TempDir(), "out")

	code, _, stderr := checkRun([]string{
		"--command", "check",
		"--input-dir", inputDir,
		"--rules", rules,
		"--output-dir", outDir,
	})
	if code != exitUsage {
		t.Errorf("код = %d, хотели %d\nstderr: %s", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "--output-dir используется только командой dump-docs") {
		t.Errorf("stderr не называет причину: %s", stderr)
	}
}

// TestRunCheckRulesRejectedByOtherCommand — --rules не принадлежит scan/parse-*/metric.
func TestRunCheckRulesRejectedByOtherCommand(t *testing.T) {
	rules := writeCheckRules(t, `{"version": 1}`)
	inputDir := t.TempDir()

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "scan",
			args: []string{"--input-dir", inputDir, "--rules", rules},
		},
		{
			name: "metric",
			args: []string{"--command", "metric", "--input-dir", inputDir, "--rules", rules},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := checkRun(tc.args)
			if code != exitUsage {
				t.Errorf("код = %d, хотели %d\nstderr: %s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, "--rules используется только командой check") {
				t.Errorf("stderr не называет причину: %s", stderr)
			}
		})
	}
}

func TestCheckUsageMentionedInHelp(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"check", "--rules", "--input-dir", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
