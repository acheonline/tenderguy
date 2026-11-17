package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/scanner"
	"tenderguy/internal/trace"
)

// Тесты команды trace (P1.6): каждый фрагмент и каждое значение обязаны иметь
// адрес в источнике.

func traceDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "заявка.docx", textParagraph("Участник: ИНН "+cliINN)+tableFixture())
	writeXlsxBook(t, dir, "спецификация.xlsx", fillSpec)
	writePdfFile(t, dir, "скан.pdf", cliPdfTextFixture)
	writeDocxParts(t, dir, "без-связей.docx", false, textParagraph("ИНН "+cliINN))
	return dir
}

func TestRunTrace(t *testing.T) {
	dir := traceDir(t)
	docx := filepath.Join(dir, "заявка.docx")
	xlsx := filepath.Join(dir, "спецификация.xlsx")
	pdf := filepath.Join(dir, "скан.pdf")

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		noOut      bool
	}{
		{
			name:     "один .docx: абзацы и клетки таблицы с адресами",
			args:     []string{"--command", "trace", "--file", docx},
			wantExit: exitOK,
			wantOut: []string{
				"без адреса 0",
				"Вердикт: 100 % трассируемость достигнута",
				"заявка.docx (docx)",
			},
		},
		{
			name:     "--with-text печатает список фрагментов с адресами",
			args:     []string{"--command", "trace", "--file", docx, "--with-text"},
			wantExit: exitOK,
			wantOut: []string{
				"абз. 1",
				"абз. 2",
				"яч. 1:1",
			},
		},
		{
			name:     ".xlsx адресуется листом и клеткой",
			args:     []string{"--command", "trace", "--file", xlsx, "--with-text"},
			wantExit: exitOK,
			wantOut: []string{
				"спецификация.xlsx (xlsx)",
				"Sheet1!A1",
			},
		},
		{
			name:     "pdf адресуется страницей",
			args:     []string{"--command", "trace", "--file", pdf, "--with-text"},
			wantExit: exitOK,
			wantOut: []string{
				"скан.pdf (pdf)",
				"стр. 1",
			},
		},
		{
			name:     "каталог: файл, который не открылся, объявлен отдельно",
			args:     []string{"--command", "trace", "--input-dir", dir},
			wantExit: exitProblems,
			wantOut: []string{
				"Ошибки разбора:",
				"без-связей.docx —",
			},
			wantErrOut: []string{"файлов без отчёта 1"},
		},
		{
			name:     "нет поддерживаемых документов",
			args:     []string{"--command", "trace", "--input-dir", t.TempDir()},
			wantExit: exitOK,
			wantOut:  []string{"трассировать нечего"},
		},
		{
			name:       "не задан вход",
			args:       []string{"--command", "trace"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан вход"},
			noOut:      true,
		},
		{
			name:       "справка перечисляет команды, включая trace",
			args:       []string{"--command", "nothing", "--file", docx},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --command "nothing"`, "trace"},
			noOut:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(tc.args, &out, &errOut); got != tc.wantExit {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", got, tc.wantExit, out.String(), errOut.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("в stdout нет %q\n%s", want, out.String())
				}
			}
			for _, want := range tc.wantErrOut {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("в stderr нет %q\n%s", want, errOut.String())
				}
			}
			if tc.noOut && out.Len() > 0 {
				t.Errorf("stdout должен быть пуст, получили\n%s", out.String())
			}
		})
	}
}

func TestRunTraceJSONDecodes(t *testing.T) {
	docx := writeDocxFile(t, t.TempDir(), "анкета.docx", textParagraph("ИНН "+cliINN+" и НМЦК "+cliMoney))

	var out, errOut bytes.Buffer
	if code := run([]string{"--command", "trace", "--file", docx, "--format", "json", "--with-text"}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}

	var rep struct {
		Files []struct {
			Name        string `json:"name"`
			Format      string `json:"format"`
			Values      int    `json:"values"`
			ValuesTrace int    `json:"values_traced"`
			Fragments   struct {
				Fragments int `json:"fragments"`
				Traced    int `json:"traced"`
			} `json:"fragments"`
			Content []struct {
				Ref struct {
					File   string `json:"file"`
					Format string `json:"format"`
					Page   int    `json:"page"`
					Para   int    `json:"para"`
				} `json:"ref"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"files"`
		Totals struct {
			Fragments int `json:"fragments"`
			Traced    int `json:"traced"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("отчёт не читается как json: %v\n%s", err, out.String())
	}
	if len(rep.Files) != 1 {
		t.Fatalf("файлов %d, хотим 1", len(rep.Files))
	}
	file := rep.Files[0]
	if file.Fragments.Traced != file.Fragments.Fragments || file.Fragments.Fragments == 0 {
		t.Errorf("фрагментов %d, с адресом %d", file.Fragments.Fragments, file.Fragments.Traced)
	}
	if file.Values == 0 || file.ValuesTrace != file.Values {
		t.Errorf("значений %d, с адресом %d", file.Values, file.ValuesTrace)
	}
	if rep.Totals.Fragments != file.Fragments.Fragments || rep.Totals.Traced != file.Fragments.Traced {
		t.Errorf("итоги %+v не сходятся с файлом %+v", rep.Totals, file.Fragments)
	}
	for _, frag := range file.Content {
		if frag.Ref.File != docx || frag.Ref.Para == 0 {
			t.Errorf("фрагмент %q без адреса: %+v", frag.Text, frag.Ref)
		}
	}
}

// Отчёт о неполной трассируемости проверяется напрямую: парсеры формата адрес
// дают всегда, а показать человек должен и обратный случай — иначе «100 %»
// было бы надписью, а не проверкой.
func TestWriteTraceTextReportsMissingRefs(t *testing.T) {
	rep := &traceReport{}
	untraced := trace.Fragment{Ref: trace.Ref{File: "/п/x.docx", Format: "docx"}, Text: "абзац без координат"}
	rep.Totals.Add(untraced)
	rep.Files = append(rep.Files, traceFile{
		Path: "/п/x.docx", Name: "x.docx", Format: string(scanner.KindDocx), Values: 2, ValuesTraced: 1,
	})
	rep.Files[0].Stats.Add(untraced)
	rep.Values, rep.ValuesTraced = 2, 1

	var out bytes.Buffer
	writeTraceText(&out, rep, false)
	got := out.String()

	for _, want := range []string{"трассируемость неполная: без адреса 2", "абзац без координат"} {
		if !strings.Contains(got, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "100 % трассируемость достигнута") {
		t.Errorf("неполная трассируемость объявлена как достигнутая:\n%s", got)
	}
}

func TestTraceUsageMentionedInHelp(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"trace", "--with-text", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
