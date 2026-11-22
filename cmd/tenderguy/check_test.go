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
