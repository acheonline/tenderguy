package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tenderguy/internal/rules"
	"tenderguy/internal/zd"
)

// Сквозные тесты CLI extract-zd (P2.8): коды выхода как часть контракта
// (0 — все три структуры full, 1 — partial/not_found или документ не .docx,
// 2 — ошибка флагов, 3 — неверный путь), все значения --emit в обоих
// форматах и сквозная проверка критерия #63: вывод --emit composition без
// правок принимает rules.LoadConfig и исполняет NewCompositionHandler.

// zdTOCPara — строка оглавления: номер, табуляция, заголовок, табуляция, страница.
func zdTOCPara(number int, title string, page int) string {
	return `<w:p><w:r><w:t xml:space="preserve">` + strconv.Itoa(number) + `.</w:t></w:r>` +
		`<w:r><w:tab/></w:r>` +
		`<w:r><w:t xml:space="preserve">` + title + `</w:t></w:r>` +
		`<w:r><w:tab/></w:r>` +
		`<w:r><w:t xml:space="preserve">` + strconv.Itoa(page) + `</w:t></w:r></w:p>`
}

// zdFullDocx — ЗД, в которой все три структуры извлекаются полностью
// (код выхода 0): оглавление из двух разделов, раздел требований к
// участнику, таблица состава и раздел образцов форм с закрытой формой и
// блоком инструкций.
func zdFullDocx(t *testing.T) string {
	t.Helper()

	body := strings.Join([]string{
		zdTOCPara(1, "ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ", 2),
		zdTOCPara(2, "Образцы основных форм документов", 3),
		textParagraph("ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ"),
		textParagraph("Участник должен иметь лицензию на осуществление видов деятельности."),
		zdTable([][]string{
			{
				"№ документа в томе",
				"Наименование документа/ссылка на пункт Закупочной документации",
				"Наименование файла в заявке",
				"Требования к формату и расширению файла",
			},
			{"1", "Опись заявки.", "«Опись»", "Doc, Pdf"},
		}),
		textParagraph("Образцы основных форм документов"),
		textParagraph("Опись заявки"),
		textParagraph("начало формы"),
		textParagraph("Таблица описи документов."),
		textParagraph("конец формы"),
		textParagraph("Инструкции по заполнению"),
		textParagraph("Заполните строки описи."),
	}, "")
	return writeDocxFile(t, t.TempDir(), "ЗД ЭФ полная.docx", body)
}

// TestRunExtractZDExitCodes — коды выхода отдельными случаями (контракт README).
func TestRunExtractZDExitCodes(t *testing.T) {
	full := zdFullDocx(t)
	absent := filepath.Join(t.TempDir(), "нет.docx")
	txt := filepath.Join(t.TempDir(), "файл.txt")
	if err := os.WriteFile(txt, []byte("текст"), 0o600); err != nil {
		t.Fatalf("запись %s: %v", txt, err)
	}
	scanDir := t.TempDir()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name:     "все три структуры full, текст → 0",
			args:     []string{"--command", "extract-zd", "--file", full},
			wantCode: exitOK,
			wantStdout: []string{
				"Требуемый состав пакета: full",
				"Пары «форма ↔ инструкция по заполнению»: full",
				"Требования к участнику: full",
				"zd-02",
			},
		},
		{
			name:     "недопустимый --emit → 2",
			args:     []string{"--command", "extract-zd", "--file", full, "--emit", "everything"},
			wantCode: exitUsage,
			wantStderr: []string{
				`неизвестный --emit "everything"`,
				"допустимы all, composition, forms, requirements",
			},
		},
		{
			name:       "--emit не-all у другой команды → 2",
			args:       []string{"--command", "scan", "--input-dir", scanDir, "--emit", "forms"},
			wantCode:   exitUsage,
			wantStderr: []string{"--emit используется только командой extract-zd"},
		},
		{
			name:       "--input-dir вместо --file → 2",
			args:       []string{"--command", "extract-zd", "--input-dir", scanDir},
			wantCode:   exitUsage,
			wantStderr: []string{"--input-dir не используется в extract-zd"},
		},
		{
			name:       "--rules у extract-zd → 2",
			args:       []string{"--command", "extract-zd", "--file", full, "--rules", "r.json"},
			wantCode:   exitUsage,
			wantStderr: []string{"--rules используется только командой check"},
		},
		{
			name:       "--output-dir у extract-zd → 2",
			args:       []string{"--command", "extract-zd", "--file", full, "--output-dir", scanDir},
			wantCode:   exitUsage,
			wantStderr: []string{"--output-dir используется только командами"},
		},
		{
			name:       "--with-text у extract-zd → 2",
			args:       []string{"--command", "extract-zd", "--file", full, "--with-text"},
			wantCode:   exitUsage,
			wantStderr: []string{"--with-text не используется в extract-zd"},
		},
		{
			name:       "несуществующий путь → 3",
			args:       []string{"--command", "extract-zd", "--file", absent},
			wantCode:   exitBadPath,
			wantStderr: []string{"нет.docx"},
		},
		{
			name:       "документ не .docx → 1",
			args:       []string{"--command", "extract-zd", "--file", txt},
			wantCode:   exitProblems,
			wantStderr: []string{"не .docx"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("код выхода = %d, хочу %d\nstderr: %s", code, tt.wantCode, stderr.String())
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("в stdout нет %q:\n%s", want, stdout.String())
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("в stderr нет %q:\n%s", want, stderr.String())
				}
			}
			if tt.wantCode == exitOK && stderr.Len() != 0 {
				t.Errorf("при коде 0 stderr не пуст: %s", stderr.String())
			}
		})
	}
}

// TestRunExtractZDEmits — каждое значение --emit в обоих форматах.
func TestRunExtractZDEmits(t *testing.T) {
	full := zdFullDocx(t)

	t.Run("emit forms, текст", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", full, "--emit", "forms"}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("код выхода = %d, хочу %d (stderr: %s)", code, exitOK, stderr.String())
		}
		out := stdout.String()
		for _, want := range []string{
			"Пары «форма ↔ инструкция по заполнению»",
			"раздел: Образцы основных форм документов",
			"Опись заявки",
			"инструкция: found (пунктов 1)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("в отчёте нет %q:\n%s", want, out)
			}
		}
	})

	t.Run("emit forms, json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", full, "--emit", "forms", "--format", "json"}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("код выхода = %d, хочу %d (stderr: %s)", code, exitOK, stderr.String())
		}
		var rep zd.FormsResult
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("вывод --emit forms не JSON схемы zd.FormsResult: %v\n%s", err, stdout.String())
		}
		if rep.Status != "full" || len(rep.Forms) != 1 {
			t.Fatalf("отчёт форм: %+v", rep)
		}
		f := rep.Forms[0]
		if f.Title != "Опись заявки" || !f.Closed || f.InstructionStatus != "found" || len(f.InstructionItems) != 1 {
			t.Errorf("форма извлечена неверно: %+v", f)
		}
	})

	t.Run("emit requirements, текст", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", full, "--emit", "requirements"}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("код выхода = %d, хочу %d (stderr: %s)", code, exitOK, stderr.String())
		}
		out := stdout.String()
		for _, want := range []string{
			"Требования к участнику",
			"раздел: ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ",
			"[лицензии и право на вид деятельности]",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("в отчёте нет %q:\n%s", want, out)
			}
		}
	})

	t.Run("emit requirements, json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", full, "--emit", "requirements", "--format", "json"}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("код выхода = %d, хочу %d (stderr: %s)", code, exitOK, stderr.String())
		}
		var rep zd.RequirementsResult
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("вывод --emit requirements не JSON: %v\n%s", err, stdout.String())
		}
		if rep.Status != "full" || len(rep.Items) != 1 ||
			rep.Items[0].Category != "лицензии и право на вид деятельности" {
			t.Errorf("отчёт требований: %+v", rep)
		}
	})

	t.Run("emit all, json: полный отчёт при коде 0", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", full, "--emit", "all", "--format", "json"}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("код выхода = %d, хочу %d (stderr: %s)", code, exitOK, stderr.String())
		}
		var rep map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("отчёт не JSON: %v", err)
		}
		for _, key := range []string{"file", "pagination", "sections", "composition", "forms", "requirements", "instructions"} {
			if _, ok := rep[key]; !ok {
				t.Errorf("в отчёте нет раздела %q", key)
			}
		}
		for _, section := range []string{"composition", "forms", "requirements"} {
			block, _ := rep[section].(map[string]any)
			if status, _ := block["status"].(string); status != "full" {
				t.Errorf("%s: статус %q, хочу full", section, status)
			}
		}
	})
}

// TestRunExtractZDCompositionFeedsRulesHandler — сквозная проверка критерия
// #63 на уровне CLI: вывод --emit composition без правок оборачивается в
// конфиг, принимается строгим rules.LoadConfig (DisallowUnknownFields) и
// исполняется rules.NewCompositionHandler.
func TestRunExtractZDCompositionFeedsRulesHandler(t *testing.T) {
	full := zdFullDocx(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--command", "extract-zd", "--file", full, "--emit", "composition"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("код выхода = %d, хочу %d (stderr: %s)", code, exitOK, stderr.String())
	}

	config := fmt.Sprintf(`{"version":1,"composition":%s,"rules":[{"id":"c1","class":"composition","severity":"warning"}]}`,
		stdout.String())
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	cfg, err := rules.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig отверг вывод --emit composition: %v", err)
	}
	if len(cfg.Composition) != 1 || cfg.Composition[0].ID != "zd-02" {
		t.Fatalf("конфиг после LoadConfig: %+v", cfg.Composition)
	}

	var table []rules.CompositionStatus
	handler := rules.NewCompositionHandler(cfg.Composition, path, &table)
	pkg := rules.Package{
		Root: t.TempDir(),
		Files: []rules.File{
			{RelPath: "1Опись.docx", Name: "1Опись.docx", Kind: "docx", Supported: true},
		},
	}
	findings, err := handler(cfg.Rules[0], pkg)
	if err != nil {
		t.Fatalf("обработчик состава: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("замечания при полном пакете: %+v", findings)
	}
	if len(table) != 1 || table[0].Item != "zd-02" || !table[0].Present ||
		strings.Join(table[0].Files, ",") != "1Опись.docx" {
		t.Errorf("таблица статусов: %+v", table)
	}
}
