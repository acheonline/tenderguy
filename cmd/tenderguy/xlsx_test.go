package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeXlsxBook сохраняет мини-книгу, собранную excelize, и возвращает путь:
// тесты CLI не зависят от документов заказчика и бинарных фикстур.
func writeXlsxBook(t *testing.T, dir, name string, build func(f *excelize.File)) string {
	t.Helper()
	f := excelize.NewFile()
	if build != nil {
		build(f)
	}
	path := filepath.Join(dir, name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("сохранение книги %s: %v", name, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("закрытие книги %s: %v", name, err)
	}
	return path
}

func fillSpec(f *excelize.File) {
	for ref, v := range map[string]string{
		"A1": "Наименование", "B1": "Кол-во",
		"A2": "Лицензия RedCheck", "B2": "10",
	} {
		if err := f.SetCellValue("Sheet1", ref, v); err != nil {
			panic(err)
		}
	}
	if err := f.SetCellFormula("Sheet1", "C2", "B2*2"); err != nil {
		panic(err)
	}
}

func TestRunParseXlsx(t *testing.T) {
	dir := t.TempDir()
	good := writeXlsxBook(t, dir, "спецификация.xlsx", fillSpec)
	junk := filepath.Join(dir, "битая.xlsx")
	if err := os.WriteFile(junk, []byte("это не книга вовсе"), 0o600); err != nil {
		t.Fatalf("запись фикстуры: %v", err)
	}

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		noOut      bool
	}{
		{
			name:     "одна книга, текстовый отчёт",
			args:     []string{"--command", "parse-xlsx", "--file", good},
			wantExit: exitOK,
			wantOut: []string{
				"Разобрано .xlsx: 1 — без потерь 1, с потерями 0, с ошибками 0",
				"спецификация.xlsx",
				"лист «Sheet1»: строк 2, колонок 3, ячеек 5, формул 1",
				"без потерь",
			},
		},
		{
			name:     "с содержимым",
			args:     []string{"--command", "parse-xlsx", "--file", good, "--with-text"},
			wantExit: exitOK,
			wantOut:  []string{"--- содержимое ---", "Наименование\tКол-во", "Лицензия RedCheck"},
		},
		{
			name:       "битая книга — ошибки, код 1",
			args:       []string{"--command", "parse-xlsx", "--file", junk},
			wantExit:   exitProblems,
			wantOut:    []string{"Ошибки разбора:"},
			wantErrOut: []string{"с ошибками 1"},
		},
		{
			name:     "каталог: обход находит книги, битая — в ошибки",
			args:     []string{"--command", "parse-xlsx", "--input-dir", dir},
			wantExit: exitProblems,
			wantOut: []string{
				"Разобрано .xlsx: 1 — без потерь 1, с потерями 0, с ошибками 1",
				"Ошибки разбора:",
				"битая.xlsx",
			},
		},
		{
			name:     "каталог без xlsx",
			args:     []string{"--command", "parse-xlsx", "--input-dir", t.TempDir()},
			wantExit: exitOK,
			wantOut:  []string{"В нём нет .xlsx — разбирать нечего"},
		},
		{
			name:       "не задан вход",
			args:       []string{"--command", "parse-xlsx"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан вход"},
			noOut:      true,
		},
		{
			name:       "file и input-dir вместе",
			args:       []string{"--command", "parse-xlsx", "--file", good, "--input-dir", dir},
			wantExit:   exitUsage,
			wantErrOut: []string{"выберите что-то одно"},
			noOut:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			got := run(tc.args, &out, &errOut)
			if got != tc.wantExit {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", got, tc.wantExit, out.String(), errOut.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("в stdout нет подстроки %q\n%s", want, out.String())
				}
			}
			for _, want := range tc.wantErrOut {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("в stderr нет подстроки %q\n%s", want, errOut.String())
				}
			}
			if tc.noOut && out.Len() > 0 {
				t.Errorf("stdout должен быть пуст, получили\n%s", out.String())
			}
		})
	}
}

func TestRunParseXlsxJSONDecodes(t *testing.T) {
	good := writeXlsxBook(t, t.TempDir(), "книга.xlsx", fillSpec)

	var out, errOut bytes.Buffer
	if code := run([]string{"--command", "parse-xlsx", "--file", good, "--format", "json"}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}

	var rep struct {
		Documents []struct {
			Name     string `json:"name"`
			Format   string `json:"format"`
			Cells    int    `json:"cells"`
			Formulas int    `json:"formulas"`
			Sheets   []struct {
				Name   string   `json:"name"`
				Cells  int      `json:"cells"`
				Merges []string `json:"merges"`
			} `json:"sheets"`
			Coverage struct {
				Complete     bool     `json:"complete"`
				MissingCells int      `json:"missing_cells"`
				Missing      []string `json:"missing"`
			} `json:"coverage"`
			Content string `json:"content"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("json не разбирается: %v\n%s", err, out.String())
	}
	if len(rep.Documents) != 1 {
		t.Fatalf("документов %d, хотели 1", len(rep.Documents))
	}
	doc := rep.Documents[0]
	if doc.Name != "книга.xlsx" || doc.Format != "xlsx" {
		t.Errorf("Name/Format = %q/%q", doc.Name, doc.Format)
	}
	if doc.Cells != 5 || doc.Formulas != 1 {
		t.Errorf("ячеек %d, формул %d, хотели 5 и 1", doc.Cells, doc.Formulas)
	}
	if !doc.Coverage.Complete || doc.Coverage.MissingCells != 0 {
		t.Errorf("coverage = %+v", doc.Coverage)
	}
	if doc.Content != "" {
		t.Errorf("без --with-text в json не должно быть поля content: %q", doc.Content)
	}
}

func TestUsageMentionsParseXlsx(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"parse-xlsx", "--file", "--with-text", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
