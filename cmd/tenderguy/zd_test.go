package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/rules"
)

// zdTable — таблица документа для фикстур extract-zd.
func zdTable(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(`<w:tbl><w:tblGrid>`)
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	for i := 0; i < cols; i++ {
		sb.WriteString(`<w:gridCol/>`)
	}
	sb.WriteString(`</w:tblGrid>`)
	for _, r := range rows {
		sb.WriteString(`<w:tr>`)
		for _, cell := range r {
			sb.WriteString(`<w:tc>` + textParagraph(cell) + `</w:tc>`)
		}
		sb.WriteString(`</w:tr>`)
	}
	sb.WriteString(`</w:tbl>`)
	return sb.String()
}

// zdFixtureDocx — минимальная ЗД с таблицей состава: один пункт «Опись».
func zdFixtureDocx(t *testing.T) string {
	t.Helper()

	body := textParagraph("ТРЕБОВАНИЯ К ЗАЯВКЕ НА УЧАСТИЕ В ЗАКУПКЕ") +
		zdTable([][]string{
			{
				"№ документа в томе",
				"Наименование документа/ссылка на пункт Закупочной документации",
				"Наименование файла в заявке",
				"Требования к формату и расширению файла",
			},
			{"1", "Опись заявки.", "«Опись»", "Doc, Pdf"},
		})
	return writeDocxFile(t, t.TempDir(), "ЗД ЭФ.docx", body)
}

func TestCheckFlagsExtractZD(t *testing.T) {
	tests := []struct {
		name    string
		params  params
		wantErr string
	}{
		{
			name:   "минимальный верный набор",
			params: params{command: commandExtractZD, file: "ЗД.docx", format: "text", emit: emitAll},
		},
		{
			name:   "emit composition",
			params: params{command: commandExtractZD, file: "ЗД.docx", format: "json", emit: emitComposition},
		},
		{
			name:    "вход каталогом",
			params:  params{command: commandExtractZD, inputDir: "docs", format: "text", emit: emitAll},
			wantErr: "--input-dir не используется в extract-zd",
		},
		{
			name:    "без файла",
			params:  params{command: commandExtractZD, format: "text", emit: emitAll},
			wantErr: "требует --file",
		},
		{
			name:    "файл правил",
			params:  params{command: commandExtractZD, file: "ЗД.docx", rulesFile: "r.json", format: "text", emit: emitAll},
			wantErr: "--rules используется только командой check",
		},
		{
			name:    "каталог вывода",
			params:  params{command: commandExtractZD, file: "ЗД.docx", outputDir: "out", format: "text", emit: emitAll},
			wantErr: "--output-dir используется только командами",
		},
		{
			name:    "текст документа",
			params:  params{command: commandExtractZD, file: "ЗД.docx", format: "text", emit: emitAll, withText: true},
			wantErr: "--with-text не используется в extract-zd",
		},
		{
			name:    "неизвестный emit",
			params:  params{command: commandExtractZD, file: "ЗД.docx", format: "text", emit: "everything"},
			wantErr: `неизвестный --emit "everything"`,
		},
		{
			name:    "emit у другой команды",
			params:  params{command: commandScan, inputDir: "docs", format: "text", emit: emitForms},
			wantErr: "--emit используется только командой extract-zd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.params
			err := checkFlags(&p)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("неожиданная ошибка: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("ошибка %q не возникла", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("ошибка = %q, хочу содержать %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRunExtractZD(t *testing.T) {
	fixture := zdFixtureDocx(t)

	t.Run("emit composition принимается движком правил", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// Состав full, но формы и требования не найдены → код выхода 1;
		// на содержимое массива пунктов это не влияет.
		code := run([]string{"--command", "extract-zd", "--file", fixture, "--emit", "composition"}, &stdout, &stderr)
		if code != exitProblems {
			t.Errorf("код выхода = %d, хочу %d (stderr: %s)", code, exitProblems, stderr.String())
		}
		var items []compositionConfigItem
		if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
			t.Fatalf("вывод --emit composition не JSON-массив: %v\n%s", err, stdout.String())
		}
		if len(items) != 1 || items[0].ID != "zd-02" || items[0].Match.By != "filename" || items[0].Match.Pattern != "*Опись*" {
			t.Fatalf("пункт состава извлечён неверно: %+v", items)
		}
		if !items[0].Required || items[0].Title != "Опись заявки." {
			t.Errorf("пункт: %+v", items[0])
		}

		// Критерий 3 #63: вывод команды без правок принимает строгий
		// rules.LoadConfig — замена источника не меняет движок правил.
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
		if len(cfg.Composition) != 1 || cfg.Composition[0].Match.Pattern != "*Опись*" {
			t.Errorf("конфиг после LoadConfig: %+v", cfg.Composition)
		}
	})

	t.Run("json-отчёт и коды выхода", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", fixture, "--format", "json"}, &stdout, &stderr)
		if code != exitProblems {
			t.Errorf("код выхода = %d, хочу %d", code, exitProblems)
		}
		var rep map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("отчёт не JSON: %v", err)
		}
		for _, key := range []string{"file", "pagination", "composition", "forms", "requirements", "instructions"} {
			if _, ok := rep[key]; !ok {
				t.Errorf("в отчёте нет раздела %q", key)
			}
		}
		file, _ := rep["file"].(map[string]any)
		if sha, _ := file["sha256"].(string); len(sha) != 64 {
			t.Errorf("sha256 не привязан к версии документа: %v", file["sha256"])
		}
		if !strings.Contains(stderr.String(), "формы и инструкции: not_found") {
			t.Errorf("в stderr нет причин: %s", stderr.String())
		}
	})

	t.Run("текстовый отчёт", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--command", "extract-zd", "--file", fixture}, &stdout, &stderr)
		if code != exitProblems {
			t.Errorf("код выхода = %d, хочу %d", code, exitProblems)
		}
		for _, want := range []string{"Требуемый состав пакета: full", "zd-02", "Упоминания инструкций"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("в отчёте нет %q:\n%s", want, stdout.String())
			}
		}
	})

	t.Run("не .docx и неверный путь", func(t *testing.T) {
		txt := filepath.Join(t.TempDir(), "файл.txt")
		if err := os.WriteFile(txt, []byte("текст"), 0o600); err != nil {
			t.Fatalf("запись %s: %v", txt, err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--command", "extract-zd", "--file", txt}, &stdout, &stderr); code != exitProblems {
			t.Errorf("не-.docx: код %d, хочу %d", code, exitProblems)
		}
		stdout.Reset()
		stderr.Reset()
		absent := filepath.Join(t.TempDir(), "нет.docx")
		if code := run([]string{"--command", "extract-zd", "--file", absent}, &stdout, &stderr); code != exitBadPath {
			t.Errorf("неверный путь: код %d, хочу %d", code, exitBadPath)
		}
	})
}
