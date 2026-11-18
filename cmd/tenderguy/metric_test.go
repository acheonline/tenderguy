package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/scanner"
)

// Тесты команды metric (P1.7): H1 — доля успешно разобранных поддерживаемых
// документов пакета, порог 80 %, коды выхода 0/1/2/3, отчёты text и json,
// справочный счётчик разборов с потерями и пометка, что у PDF потери не
// измеряются.

// metricRun запускает CLI командой metric и возвращает код выхода и вывод.
func metricRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer
	code := run(append([]string{"--command", commandMetric}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

// metricLossySdt — элемент w:sdt без w:sdtContent: сверка полноты считает
// текст внутри него эталоном исходника, а разбор его не извлекает —
// документ получается разобранным «с потерями».
func metricLossySdt(text string) string {
	return `<w:sdt><w:r><w:t>` + text + `</w:t></w:r></w:sdt>`
}

// metricGoodDir — каталог полного успеха: два docx (один с потерями), xlsx,
// pdf с текстовым слоем и неподдерживаемый .txt.
func metricGoodDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "заявка.docx", textParagraph("Поставка RedCheck"))
	writeDocxFile(t, dir, "архив.docx", textParagraph("Обычный текст")+metricLossySdt("спрятанный текст"))
	writeXlsxBook(t, dir, "спецификация.xlsx", fillSpec)
	writePdfFile(t, dir, "скан.pdf", cliPdfTextFixture)
	writeFixture(t, filepath.Join(dir, "памятка.txt"), []byte("текст"))
	return dir
}

// metricHalfDir — каталог ниже порога: один разбираемый .docx и один
// неоткрываемый (обход признаёт документ Word, библиотека его не открывает).
func metricHalfDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "хороший.docx", textParagraph("текст"))
	writeDocxParts(t, dir, "битый.docx", false, textParagraph("текст есть, связей нет"))
	return dir
}

func TestRunMetric(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(t *testing.T) string
		extra      []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		verifyJSON func(t *testing.T, rep metricReport)
	}{
		{
			name:     "все документы разобраны — H1 100 %, порог достигнут, код 0",
			prepare:  metricGoodDir,
			wantExit: exitOK,
			wantOut: []string{
				"Файлов: 5, поддерживаемых 4, прочих 1",
				"Разобрано успешно: 4 из 4 — H1 100.0 % (порог 80 %)",
				"С потерями: 1 (docx/xlsx); у PDF потери не измеряются",
				"Вердикт: порог 80 % достигнут",
				"docx  2 из 2 (100.0 %), с потерями 1",
				"pdf   1 из 1 (100.0 %), с потерями 0 — потери не измеряются",
			},
		},
		{
			name: "ровно 80 % — порог достигнут, код 0, неразобранный файл показан",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				for _, name := range []string{"a.docx", "b.docx", "c.docx", "d.docx"} {
					writeDocxFile(t, dir, name, textParagraph("текст "+name))
				}
				writeDocxParts(t, dir, "битый.docx", false, textParagraph("текст есть, связей нет"))
				return dir
			},
			wantExit: exitOK,
			wantOut:  []string{"H1 80.0 %", "порог 80 % достигнут", "Неразобранные файлы:", "битый.docx"},
		},
		{
			name:       "50 % ниже порога — код 1",
			prepare:    metricHalfDir,
			wantExit:   exitProblems,
			wantOut:    []string{"Разобрано успешно: 1 из 2 — H1 50.0 %", "Вердикт: НИЖЕ ПОРОГА 80 %"},
			wantErrOut: []string{"H1 50.0 % ниже порога 80 %", "не разобрано документов 1"},
		},
		{
			name: "75 % (3 из 4) чуть ниже порога — код 1",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				for _, name := range []string{"a.docx", "b.docx", "c.docx"} {
					writeDocxFile(t, dir, name, textParagraph("текст "+name))
				}
				writeDocxParts(t, dir, "битый.docx", false, textParagraph("текст есть, связей нет"))
				return dir
			},
			wantExit:   exitProblems,
			wantOut:    []string{"Разобрано успешно: 3 из 4 — H1 75.0 %", "Вердикт: НИЖЕ ПОРОГА 80 %"},
			wantErrOut: []string{"H1 75.0 % ниже порога 80 %", "не разобрано документов 1"},
		},
		{
			name: "все документы упали — H1 0 %, код 1",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				writeDocxParts(t, dir, "битый1.docx", false, textParagraph("один"))
				writeDocxParts(t, dir, "битый2.docx", false, textParagraph("два"))
				return dir
			},
			wantExit: exitProblems,
			wantOut: []string{
				"Разобрано успешно: 0 из 2 — H1 0.0 %",
				"Вердикт: НИЖЕ ПОРОГА 80 %",
				"Неразобранные файлы:",
				"битый1.docx",
				"битый2.docx",
			},
			wantErrOut: []string{"H1 0.0 % ниже порога 80 %", "не разобрано документов 2"},
		},
		{
			name: "--input-dir указывает на файл — код 3",
			prepare: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "заявка.docx")
				writeFixture(t, path, []byte("это файл, а не папка"))
				return path
			},
			wantExit:   exitBadPath,
			wantErrOut: []string{"файл, а не каталог", "--input-dir"},
		},
		{
			name:     "пустой каталог — нечего измерять, код 0",
			prepare:  func(t *testing.T) string { return t.TempDir() },
			wantExit: exitOK,
			wantOut:  []string{"нет документов .docx/.xlsx/.pdf", "нечего измерять"},
		},
		{
			name: "только неподдерживаемые файлы — нечего измерять, код 0",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				writeFixture(t, filepath.Join(dir, "инструкция.txt"), []byte("читать"))
				writeFixture(t, filepath.Join(dir, "условия.md"), []byte("# условия"))
				return dir
			},
			wantExit: exitOK,
			wantOut:  []string{"нечего измерять"},
		},
		{
			name:     "пустой каталог в json — нулевые счётчики, note и пустой список форматов",
			prepare:  func(t *testing.T) string { return t.TempDir() },
			extra:    []string{"--format", "json"},
			wantExit: exitOK,
			// `"formats": []` проверяется по сырому выводу: null при
			// unmarshal тоже дал бы пустой срез, а списки в отчёте
			// присутствуют всегда (стиль dump.go).
			wantOut: []string{`"formats": []`, `"note": "нечего измерять"`},
			verifyJSON: func(t *testing.T, rep metricReport) {
				if rep.Supported != 0 || rep.Parsed != 0 || rep.Total != 0 || rep.Other != 0 {
					t.Errorf("счётчики: %+v, хотели нули", rep)
				}
				if rep.Success != 0 || rep.Threshold != 80 || !rep.Reached {
					t.Errorf("H1/порог: %+v, хотели 0/80/true", rep)
				}
				if rep.Note != "нечего измерять" {
					t.Errorf("note = %q, хотели «нечего измерять»", rep.Note)
				}
				if len(rep.Formats) != 0 {
					t.Errorf("formats = %+v, хотели пустой список", rep.Formats)
				}
				if len(rep.Failed) != 0 {
					t.Errorf("failed = %+v, хотели отсутствие", rep.Failed)
				}
			},
		},
		{
			name: "только неподдерживаемые файлы в json — total/other реальные, supported 0",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				writeFixture(t, filepath.Join(dir, "инструкция.txt"), []byte("читать"))
				writeFixture(t, filepath.Join(dir, "условия.md"), []byte("# условия"))
				return dir
			},
			extra:    []string{"--format", "json"},
			wantExit: exitOK,
			verifyJSON: func(t *testing.T, rep metricReport) {
				if rep.Total != 2 || rep.Other != 2 {
					t.Errorf("total = %d, other = %d, хотели 2 и 2", rep.Total, rep.Other)
				}
				if rep.Supported != 0 || rep.Parsed != 0 || rep.Success != 0 || !rep.Reached {
					t.Errorf("отчёт: %+v, хотели supported/parsed/success 0 и reached true", rep)
				}
				if rep.Note != "нечего измерять" {
					t.Errorf("note = %q, хотели «нечего измерять»", rep.Note)
				}
				if len(rep.Formats) != 0 {
					t.Errorf("formats = %+v, хотели пустой список", rep.Formats)
				}
			},
		},
		{
			name:       "каталога нет — код 3",
			prepare:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "нет-такого-каталога") },
			wantExit:   exitBadPath,
			wantErrOut: []string{"каталог не найден"},
		},
		{
			name:     "json-отчёт: счётчики, форматы, потери и отсутствие failed",
			prepare:  metricGoodDir,
			extra:    []string{"--format", "json"},
			wantExit: exitOK,
			verifyJSON: func(t *testing.T, rep metricReport) {
				if rep.Total != 5 || rep.Supported != 4 || rep.Other != 1 || rep.Parsed != 4 {
					t.Errorf("счётчики: %+v", rep)
				}
				if rep.Success != 100 || rep.Threshold != 80 || !rep.Reached {
					t.Errorf("H1/порог: %+v", rep)
				}
				if rep.Lost != 1 {
					t.Errorf("с потерями = %d, хотели 1", rep.Lost)
				}
				if rep.PdfLossesMeasured {
					t.Error("pdf_losses_measured = true, а Complete() у PdfDocument нет")
				}
				if len(rep.Failed) != 0 {
					t.Errorf("failed = %+v, хотели пустой", rep.Failed)
				}
				want := map[string]metricFormat{
					"docx": {Format: "docx", Total: 2, Parsed: 2, Success: 100, Lost: 1, LossesMeasured: true},
					"xlsx": {Format: "xlsx", Total: 1, Parsed: 1, Success: 100, Lost: 0, LossesMeasured: true},
					"pdf":  {Format: "pdf", Total: 1, Parsed: 1, Success: 100, Lost: 0, LossesMeasured: false},
				}
				if len(rep.Formats) != len(want) {
					t.Fatalf("форматов %d: %+v", len(rep.Formats), rep.Formats)
				}
				for _, f := range rep.Formats {
					if w, ok := want[f.Format]; !ok || f != w {
						t.Errorf("формат %+v, хотели %+v", f, w)
					}
				}
			},
		},
		{
			name:       "json ниже порога — failed с причиной",
			prepare:    metricHalfDir,
			extra:      []string{"--format", "json"},
			wantExit:   exitProblems,
			wantErrOut: []string{"ниже порога"},
			verifyJSON: func(t *testing.T, rep metricReport) {
				if rep.Success != 50 || rep.Reached {
					t.Errorf("H1 = %v, reached = %v, хотели 50 и false", rep.Success, rep.Reached)
				}
				if len(rep.Failed) != 1 {
					t.Fatalf("failed = %+v, хотели одну запись", rep.Failed)
				}
				if !strings.HasSuffix(rep.Failed[0].Path, "битый.docx") || rep.Failed[0].Error == "" {
					t.Errorf("failed[0] = %+v, хотели путь и непустую причину", rep.Failed[0])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.prepare(t)
			code, stdout, stderr := metricRun(t, append([]string{"--input-dir", dir}, tc.extra...)...)
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
			if tc.verifyJSON == nil {
				return
			}
			var rep metricReport
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("stdout не json отчёта метрики: %v\n%s", err, stdout)
			}
			tc.verifyJSON(t, rep)
		})
	}
}

// Поддерживаемый файл с problem обхода (содержимое не читается) обязан попасть
// в failed с причиной сканера, без попытки разбора и без падения команды.
// Живой сценарий — файл без прав на чтение; под root права не проверяются,
// поэтому ветка problem дополнительно закрыта детерминированным
// TestCollectMetricProblemEntry на синтетическом результате обхода.
func TestRunMetricUnreadableSupportedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("эффективный uid 0: права доступа на чтение не проверяются")
	}
	dir := t.TempDir()
	writeDocxFile(t, dir, "хороший.docx", textParagraph("текст"))
	locked := writeDocxFile(t, dir, "закрытый.docx", textParagraph("нет доступа"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o600); err != nil {
			t.Errorf("возврат прав: %v", err)
		}
	})

	code, stdout, stderr := metricRun(t, "--input-dir", dir, "--format", "json")
	if code != exitProblems {
		t.Fatalf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", code, exitProblems, stdout, stderr)
	}
	var rep metricReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("stdout не json отчёта метрики: %v\n%s", err, stdout)
	}
	if rep.Supported != 2 || rep.Parsed != 1 {
		t.Errorf("supported = %d, parsed = %d, хотели 2 и 1", rep.Supported, rep.Parsed)
	}
	if len(rep.Failed) != 1 {
		t.Fatalf("failed = %+v, хотели одну запись", rep.Failed)
	}
	if !strings.HasSuffix(rep.Failed[0].Path, "закрытый.docx") {
		t.Errorf("failed[0].Path = %q, хотели путь к закрытый.docx", rep.Failed[0].Path)
	}
	if !strings.Contains(rep.Failed[0].Error, "не удалось прочитать содержимое") {
		t.Errorf("failed[0].Error = %q, хотели причину обхода про чтение", rep.Failed[0].Error)
	}
}

// Ветка problem в collectMetric: запись сканера с Supported=true и непустым
// Problem считается неудачей без попытки разбора — в failed попадает текст
// problem как есть. Path записи намеренно указывает на несуществующий файл:
// если бы реализация всё-таки вызвала парсер, ошибка была бы про открытие
// файла, а не про problem, и проверка причины не прошла бы.
func TestCollectMetricProblemEntry(t *testing.T) {
	dir := t.TempDir()
	good := writeDocxFile(t, dir, "хороший.docx", textParagraph("текст"))
	lossy := writeDocxFile(t, dir, "с-потерями.docx", textParagraph("текст")+metricLossySdt("спрятанный текст"))
	book := writeXlsxBook(t, dir, "книга.xlsx", fillSpec)
	pdf := writePdfFile(t, dir, "doc.pdf", cliPdfTextFixture)
	writeFixture(t, filepath.Join(dir, "памятка.txt"), []byte("текст"))

	const problem = "не удалось прочитать содержимое: permission denied"
	ghost := filepath.Join(dir, "призрак.docx") // файла нет на диске
	res := &scanner.Result{
		Root:  dir,
		Total: 6,
		Entries: []scanner.Entry{
			{RelPath: "хороший.docx", Path: good, Name: "хороший.docx", Kind: scanner.KindDocx, Supported: true},
			{RelPath: "с-потерями.docx", Path: lossy, Name: "с-потерями.docx", Kind: scanner.KindDocx, Supported: true},
			{RelPath: "книга.xlsx", Path: book, Name: "книга.xlsx", Kind: scanner.KindXlsx, Supported: true},
			{RelPath: "doc.pdf", Path: pdf, Name: "doc.pdf", Kind: scanner.KindPdf, Supported: true},
			{RelPath: "призрак.docx", Path: ghost, Name: "призрак.docx", Kind: scanner.KindDocx, Supported: true, Problem: problem},
			{RelPath: "памятка.txt", Path: filepath.Join(dir, "памятка.txt"), Name: "памятка.txt", Kind: scanner.KindOther, Supported: false},
		},
	}

	rep, stats := collectMetric(res)
	if rep.Total != 6 || rep.Supported != 5 || rep.Other != 1 || rep.Parsed != 4 {
		t.Errorf("счётчики: total=%d supported=%d other=%d parsed=%d, хотели 6/5/1/4",
			rep.Total, rep.Supported, rep.Other, rep.Parsed)
	}
	if rep.Lost != 1 {
		t.Errorf("с потерями = %d, хотели 1 (docx с w:sdt без w:sdtContent)", rep.Lost)
	}
	if len(rep.Failed) != 1 {
		t.Fatalf("failed = %+v, хотели одну запись", rep.Failed)
	}
	if rep.Failed[0].Path != ghost {
		t.Errorf("failed[0].Path = %q, хотели %q", rep.Failed[0].Path, ghost)
	}
	if rep.Failed[0].Error != problem {
		t.Errorf("failed[0].Error = %q, хотели текст problem без попытки разбора %q", rep.Failed[0].Error, problem)
	}
	if rep.PdfLossesMeasured {
		t.Error("pdf_losses_measured = true, а Complete() у PdfDocument нет")
	}

	formats := formatStats(stats)
	want := []metricFormat{
		{Format: "docx", Total: 3, Parsed: 2, Success: 66.7, Lost: 1, LossesMeasured: true},
		{Format: "xlsx", Total: 1, Parsed: 1, Success: 100, Lost: 0, LossesMeasured: true},
		{Format: "pdf", Total: 1, Parsed: 1, Success: 100, Lost: 0, LossesMeasured: false},
	}
	if len(formats) != len(want) {
		t.Fatalf("форматов %d: %+v", len(formats), formats)
	}
	for i, f := range formats {
		if f != want[i] {
			t.Errorf("формат[%d] = %+v, хотели %+v (порядок docx→xlsx→pdf)", i, f, want[i])
		}
	}
}

// Защита от nil-записи формата в collectMetric: scanner.Supported() сегодня
// признаёт только docx/xlsx/pdf, но если сканер начнёт поддерживать новый
// формат, метрика обязана не паниковать, а учесть запись как неудачу без
// попытки разбора — доля при этом остаётся консервативной (запись в
// знаменателе). Kind берётся настоящий, но вне трёх разбираемых.
func TestCollectMetricUnknownKind(t *testing.T) {
	dir := t.TempDir()
	good := writeDocxFile(t, dir, "хороший.docx", textParagraph("текст"))
	archive := filepath.Join(dir, "приложение.zip")
	writeFixture(t, archive, []byte("PK\x03\x04"))

	res := &scanner.Result{
		Root:  dir,
		Total: 2,
		Entries: []scanner.Entry{
			{RelPath: "хороший.docx", Path: good, Name: "хороший.docx", Kind: scanner.KindDocx, Supported: true},
			{RelPath: "приложение.zip", Path: archive, Name: "приложение.zip", Kind: scanner.KindArchive, Supported: true},
		},
	}

	rep, stats := collectMetric(res)
	if rep.Total != 2 || rep.Supported != 2 || rep.Parsed != 1 {
		t.Errorf("счётчики: total=%d supported=%d parsed=%d, хотели 2/2/1 (доля консервативна)",
			rep.Total, rep.Supported, rep.Parsed)
	}
	if len(rep.Failed) != 1 {
		t.Fatalf("failed = %+v, хотели одну запись", rep.Failed)
	}
	if rep.Failed[0].Path != archive {
		t.Errorf("failed[0].Path = %q, хотели %q", rep.Failed[0].Path, archive)
	}
	if want := `неизвестный формат "archive"`; rep.Failed[0].Error != want {
		t.Errorf("failed[0].Error = %q, хотели %q", rep.Failed[0].Error, want)
	}
	formats := formatStats(stats)
	want := []metricFormat{{Format: "docx", Total: 1, Parsed: 1, Success: 100, Lost: 0, LossesMeasured: true}}
	if len(formats) != len(want) {
		t.Fatalf("formats = %+v, хотели %+v", formats, want)
	}
	if formats[0] != want[0] {
		t.Errorf("formats[0] = %+v, хотели %+v", formats[0], want[0])
	}
}

// Флаги metric: команда измеряет пакет целиком, поэтому --file и --with-text
// отклоняются, --input-dir обязателен, а --output-dir остаётся у dump-docs.
func TestRunMetricFlags(t *testing.T) {
	inputDir := metricGoodDir(t)
	outDir := t.TempDir()
	docsFile := filepath.Join(inputDir, "заявка.docx")

	tests := []struct {
		name       string
		args       []string
		wantErrOut []string
	}{
		{
			name:       "без --input-dir",
			args:       []string{"--command", commandMetric},
			wantErrOut: []string{"не задан каталог с документами", "--input-dir"},
		},
		{
			name:       "--input-dir пустой строкой",
			args:       []string{"--command", commandMetric, "--input-dir", "  "},
			wantErrOut: []string{"не задан каталог с документами"},
		},
		{
			name:       "--file отклоняется",
			args:       []string{"--command", commandMetric, "--input-dir", inputDir, "--file", docsFile},
			wantErrOut: []string{"--file не используется в metric"},
		},
		{
			name:       "--with-text отклоняется",
			args:       []string{"--command", commandMetric, "--input-dir", inputDir, "--with-text"},
			wantErrOut: []string{"--with-text не используется в metric"},
		},
		{
			name:       "--output-dir отклоняется",
			args:       []string{"--command", commandMetric, "--input-dir", inputDir, "--output-dir", outDir},
			wantErrOut: []string{"--output-dir используется только командой dump-docs"},
		},
		{
			name:       "неизвестный --format",
			args:       []string{"--command", commandMetric, "--input-dir", inputDir, "--format", "xml"},
			wantErrOut: []string{`неизвестный --format "xml"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != exitUsage {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", code, exitUsage, stdout.String(), stderr.String())
			}
			for _, want := range tc.wantErrOut {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("в stderr нет %q\n%s", want, stderr.String())
				}
			}
			if stdout.Len() > 0 {
				t.Errorf("при ошибке флагов stdout должен быть пуст, получили\n%s", stdout.String())
			}
		})
	}
}

// metricParse — разбор одним вызовом парсера формата: успех, потери и ошибка.
func TestMetricParse(t *testing.T) {
	dir := t.TempDir()
	good := writeDocxFile(t, dir, "хороший.docx", textParagraph("текст"))
	lossy := writeDocxFile(t, dir, "с-потерями.docx", textParagraph("текст")+metricLossySdt("спрятанный текст"))
	broken := writeDocxParts(t, dir, "битый.docx", false, textParagraph("текст есть, связей нет"))
	book := writeXlsxBook(t, dir, "книга.xlsx", fillSpec)
	pdf := writePdfFile(t, dir, "doc.pdf", cliPdfTextFixture)

	tests := []struct {
		name     string
		path     string
		kind     string
		wantLost bool
		wantErr  bool
	}{
		{name: "docx без потерь", path: good, kind: "docx"},
		{name: "docx с потерями", path: lossy, kind: "docx", wantLost: true},
		{name: "битый docx — ошибка", path: broken, kind: "docx", wantErr: true},
		{name: "xlsx без потерь", path: book, kind: "xlsx"},
		{name: "pdf — потери не измеряются", path: pdf, kind: "pdf"},
		{name: "неизвестный формат — ошибка", path: good, kind: "txt", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lost, err := metricParse(tc.path, tc.kind)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку, получили lost = %v", lost)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if lost != tc.wantLost {
				t.Errorf("lost = %v, хотели %v", lost, tc.wantLost)
			}
		})
	}
}

func TestRound1(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "целое не меняется", in: 80, want: 80},
		{name: "треть округляется вверх", in: 200.0 / 3, want: 66.7},
		{name: "округление к порогу не влияет на сравнение — только показ", in: 79.96, want: 80},
		{name: "ноль", in: 0, want: 0},
		{name: "сто", in: 100, want: 100},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := round1(tc.in); got != tc.want {
				t.Errorf("round1(%v) = %v, хотели %v", tc.in, got, tc.want)
			}
		})
	}
}

// Справка обязана объявлять metric: список команд и коды выхода читают
// и человек, и CI-обвязка.
func TestMetricUsageMentionedInHelp(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"metric", "--input-dir", "H1"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
