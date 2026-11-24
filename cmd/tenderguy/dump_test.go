package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tenderguy/internal/reqs"
	"tenderguy/internal/scanner"
	"tenderguy/internal/trace"
)

// Тесты команды dump-docs (P1.8): docs.json — по одной записи на каждый файл
// обхода, схемы metadata/fragments без null, дедуп реквизитов, коды выхода
// 0/1/2/3 и запрет --output-dir для чужих команд.

// innIP12 — корректный ИНН физлица (12 цифр) для проверки, что inn собирает
// обе длины.
const innIP12 = "771456789041"

// dumpRun запускает CLI командой dump-docs и возвращает код выхода и вывод.
func dumpRun(t *testing.T, inputDir, outputDir string, extra ...string) (int, string, string) {
	t.Helper()

	args := append([]string{"--command", commandDumpDocs, "--input-dir", inputDir, "--output-dir", outputDir}, extra...)
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// readDumpJSON читает docs.json дважды: типизированно (nil-слайсы выдают null)
// и сырыми ключами (отличает отсутствие ключа от null).
func readDumpJSON(t *testing.T, outputDir string) ([]dumpRecord, []map[string]json.RawMessage) {
	t.Helper()

	path := filepath.Join(outputDir, dumpFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	var records []dumpRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("docs.json не читается как массив записей: %v\n%s", err, data)
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("docs.json не читается как массив объектов: %v\n%s", err, data)
	}
	return records, raw
}

// requireDumpSchema — обязательные ключи каждой записи: file_path, doc_type,
// text_content, fragments, metadata.{inn,kpp,dates}. Пустые списки обязаны
// быть [], а не null: приёмка различает «нет значений» от «поля забыли».
func requireDumpSchema(t *testing.T, raw []map[string]json.RawMessage) {
	t.Helper()

	for i, rec := range raw {
		for _, key := range []string{"file_path", "doc_type", "text_content", "fragments"} {
			val, ok := rec[key]
			if !ok {
				t.Errorf("запись %d: нет ключа %q", i, key)
				continue
			}
			if key == "fragments" && string(val) == "null" {
				t.Errorf("запись %d: fragments = null, хотели [] или список", i)
			}
		}
		metaRaw, ok := rec["metadata"]
		if !ok {
			t.Errorf("запись %d: нет ключа metadata", i)
			continue
		}
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			t.Errorf("запись %d: metadata не объект: %v", i, err)
			continue
		}
		for _, key := range []string{"inn", "kpp", "dates"} {
			val, ok := meta[key]
			if !ok {
				t.Errorf("запись %d: metadata без ключа %q", i, key)
				continue
			}
			if string(val) == "null" {
				t.Errorf("запись %d: metadata.%s = null, хотели []", i, key)
			}
		}
	}
}

func findDumpRecord(t *testing.T, records []dumpRecord, rel string) dumpRecord {
	t.Helper()

	for _, rec := range records {
		if rec.FilePath == rel {
			return rec
		}
	}
	t.Fatalf("в docs.json нет записи %q", rel)
	return dumpRecord{}
}

// dumpGoodDir — каталог успеха: разбираемые docx (в подкаталоге, с реквизитами,
// таблицей и HTML-спецсимволами), xlsx, pdf с текстовым слоем и неподдерживаемый
// .txt. Именно на нём проверяются «запись на каждый файл» и пустые поля у txt.
func dumpGoodDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	pack := filepath.Join(dir, "Пакет")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatalf("создание подкаталога: %v", err)
	}
	writeDocxFile(t, pack, "заявка.docx",
		textParagraph("Участник: ИНН "+cliINN+", КПП "+cliKPP)+
			textParagraph("Поставщик &lt;RedCheck&gt; и партнёр ABCDEF&amp;Co")+
			tableFixture())
	writeXlsxBook(t, dir, "спецификация.xlsx", fillSpec)
	writePdfFile(t, dir, "скан.pdf", cliPdfTextFixture)
	writeFixture(t, filepath.Join(dir, "памятка.txt"), []byte("текст"))
	return dir
}

func TestRunDumpDocs(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(t *testing.T) string
		prepareOut func(t *testing.T) string // иначе t.TempDir()/выгрузка
		wantExit   int
		wantOut    []string
		wantErrOut []string
		wantStdout bool // stdout обязан быть пуст
		noDump     bool // docs.json не создаётся
		verify     func(t *testing.T, records []dumpRecord, raw []map[string]json.RawMessage, inputDir, outputDir string)
	}{
		{
			name:     "выгрузка: по записи на каждый файл, текст и адреса у парсируемых",
			prepare:  dumpGoodDir,
			wantExit: exitOK,
			wantOut:  []string{"docs.json", ": 4 (поддерживаемых 3, не разобрано 0)"},
			verify: func(t *testing.T, records []dumpRecord, raw []map[string]json.RawMessage, inputDir, outputDir string) {
				requireDumpSchema(t, raw)
				if len(records) != 4 {
					t.Fatalf("записей %d, хотели 4", len(records))
				}
				docxPath := filepath.Join(inputDir, "Пакет", "заявка.docx")

				docx := findDumpRecord(t, records, "Пакет/заявка.docx")
				if docx.DocType != "docx" {
					t.Errorf("doc_type = %q, хотели docx", docx.DocType)
				}
				for _, want := range []string{"Участник: ИНН " + cliINN, "Заголовок", "Поставщик <RedCheck> и партнёр ABCDEF&Co"} {
					if !strings.Contains(docx.TextContent, want) {
						t.Errorf("в text_content нет %q:\n%s", want, docx.TextContent)
					}
				}
				if len(docx.Fragments) < 3 {
					t.Errorf("фрагментов %d, хотели не меньше 3", len(docx.Fragments))
				}
				if docx.Fragments[0].Ref.File != docxPath || docx.Fragments[0].Ref.Format != "docx" {
					t.Errorf("первый фрагмент без адреса источника: %+v", docx.Fragments[0].Ref)
				}
				if docx.Note != "" || docx.Failed != nil {
					t.Errorf("у разобранного документа note/failed должны отсутствовать: %+v", docx)
				}

				xlsx := findDumpRecord(t, records, "спецификация.xlsx")
				if xlsx.DocType != "xlsx" || !strings.Contains(xlsx.TextContent, "Лицензия RedCheck") {
					t.Errorf("xlsx-запись: %+v", xlsx)
				}
				if len(xlsx.Fragments) == 0 || xlsx.Fragments[0].Ref.Format != "xlsx" {
					t.Errorf("xlsx-фрагменты: %+v", xlsx.Fragments)
				}

				pdf := findDumpRecord(t, records, "скан.pdf")
				if pdf.DocType != "pdf" || !strings.Contains(pdf.TextContent, "CLI pdf fixture") {
					t.Errorf("pdf-запись: %+v", pdf)
				}
				if len(pdf.Fragments) == 0 || pdf.Fragments[0].Ref.Format != "pdf" {
					t.Errorf("pdf-фрагменты: %+v", pdf.Fragments)
				}

				txt := findDumpRecord(t, records, "памятка.txt")
				if txt.DocType != "other" || txt.TextContent != "" || len(txt.Fragments) != 0 {
					t.Errorf("неподдерживаемый файл обязан быть пуст по содержимому: %+v", txt)
				}
				if txt.Fragments == nil {
					t.Error("fragments = nil, хотели пустой [] без null")
				}
				if !strings.Contains(txt.Note, "не входит в P1") {
					t.Errorf("у неподдерживаемого нет причины в note: %q", txt.Note)
				}

				// Регресс SetEscapeHTML(false) и отступа в два пробела.
				data, err := os.ReadFile(filepath.Join(outputDir, dumpFileName))
				if err != nil {
					t.Fatalf("чтение docs.json: %v", err)
				}
				text := string(data)
				if !strings.Contains(text, "\n  {\n    \"file_path\"") {
					t.Errorf("отступ docs.json не в два пробела:\n%s", text[:min(len(text), 200)])
				}
				if strings.Contains(text, `\u003c`) || strings.Contains(text, `\u0026`) {
					t.Errorf("docs.json экранирует HTML, хотя SetEscapeHTML(false):\n%s", text)
				}
			},
		},
		{
			name:     "вложенный --output-dir создаётся автоматически",
			prepare:  dumpGoodDir,
			wantExit: exitOK,
			verify: func(t *testing.T, _ []dumpRecord, _ []map[string]json.RawMessage, _, outputDir string) {
				if _, err := os.Stat(filepath.Join(outputDir, dumpFileName)); err != nil {
					t.Errorf("docs.json в созданном вложенном каталоге: %v", err)
				}
			},
		},
		{
			name:     "пустой каталог — пустой массив и код 0",
			prepare:  func(t *testing.T) string { return t.TempDir() },
			wantExit: exitOK,
			wantOut:  []string{": 0 (поддерживаемых 0, не разобрано 0)"},
			verify: func(t *testing.T, records []dumpRecord, raw []map[string]json.RawMessage, _, outputDir string) {
				if len(records) != 0 {
					t.Fatalf("записей %d, хотели 0", len(records))
				}
				data, err := os.ReadFile(filepath.Join(outputDir, dumpFileName))
				if err != nil {
					t.Fatalf("чтение docs.json: %v", err)
				}
				if strings.TrimSpace(string(data)) != "[]" {
					t.Errorf("пустой каталог обязан дать массив [], получили %q", data)
				}
			},
		},
		{
			name: "каталог только с неподдерживаемыми — массив записей с note, код 0",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				writeFixture(t, filepath.Join(dir, "инструкция.txt"), []byte("читать"))
				writeFixture(t, filepath.Join(dir, "условия.md"), []byte("# условия"))
				return dir
			},
			wantExit: exitOK,
			wantOut:  []string{": 2 (поддерживаемых 0, не разобрано 0)"},
			verify: func(t *testing.T, records []dumpRecord, raw []map[string]json.RawMessage, _, _ string) {
				requireDumpSchema(t, raw)
				if len(records) != 2 {
					t.Fatalf("записей %d, хотели 2", len(records))
				}
				for _, rec := range records {
					if rec.Note == "" {
						t.Errorf("у %q пустой note: причина непарсинга обязательна", rec.FilePath)
					}
					if rec.TextContent != "" || rec.Fragments == nil || len(rec.Fragments) != 0 {
						t.Errorf("неподдерживаемый %q с содержимым: %+v", rec.FilePath, rec)
					}
				}
			},
		},
		{
			name: "битый поддерживаемый docx — note, failed и код 1, docs.json записан",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				writeDocxFile(t, dir, "хороший.docx", textParagraph("Поставка RedCheck"))
				// Корректный zip с word/document.xml, но без document.xml.rels:
				// обход признаёт .docx, библиотека документ не открывает.
				writeDocxParts(t, dir, "битый.docx", false, textParagraph("ИНН "+cliINN))
				return dir
			},
			wantExit:   exitProblems,
			wantOut:    []string{": 2 (поддерживаемых 2, не разобрано 1)"},
			wantErrOut: []string{"поддерживаемых документов не разобрано 1", "см. failed"},
			verify: func(t *testing.T, records []dumpRecord, raw []map[string]json.RawMessage, inputDir, _ string) {
				if len(records) != 2 {
					t.Fatalf("записей %d, хотели 2 — docs.json обязан быть записан даже с ошибкой разбора", len(records))
				}
				broken := findDumpRecord(t, records, "битый.docx")
				if broken.Note != "разбор не выполнен" {
					t.Errorf("note = %q, хотели «разбор не выполнен»", broken.Note)
				}
				if len(broken.Failed) != 1 {
					t.Fatalf("failed = %+v, хотели одну ошибку", broken.Failed)
				}
				if broken.Failed[0].Path != filepath.Join(inputDir, "битый.docx") || broken.Failed[0].Error == "" {
					t.Errorf("failed[0] = %+v, хотели путь к файлу и текст ошибки", broken.Failed[0])
				}
				if broken.TextContent != "" || len(broken.Fragments) != 0 || broken.Fragments == nil {
					t.Errorf("неразобранный документ с содержимым: %+v", broken)
				}
				good := findDumpRecord(t, records, "хороший.docx")
				if good.Note != "" || len(good.Fragments) == 0 {
					t.Errorf("соседний разборный документ пострадал: %+v", good)
				}
				// failed появляется только там, где была ошибка.
				for _, rec := range raw {
					_, has := rec["failed"]
					if rec["file_path"] != nil && has != (string(rec["file_path"]) == `"битый.docx"`) {
						t.Errorf("ключ failed есть/нет не там: %v", rec["file_path"])
					}
				}
			},
		},
		{
			name: "ссылка вместо файла — note берётся из problem обхода",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				target := writeDocxFile(t, t.TempDir(), "оригинал.docx", textParagraph("данные"))
				if err := os.Symlink(target, filepath.Join(dir, "ссылка.docx")); err != nil {
					t.Skipf("среда не поддерживает символьные ссылки: %v", err)
				}
				return dir
			},
			wantExit: exitOK,
			wantOut:  []string{": 1 (поддерживаемых 0, не разобрано 0)"},
			verify: func(t *testing.T, records []dumpRecord, _ []map[string]json.RawMessage, _, _ string) {
				if len(records) != 1 {
					t.Fatalf("записей %d, хотели 1", len(records))
				}
				if !strings.Contains(records[0].Note, "символическая ссылка") {
					t.Errorf("note = %q, хотели problem обхода про ссылку", records[0].Note)
				}
			},
		},
		{
			name:       "input-dir не существует — код 3, docs.json не создаётся",
			prepare:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "нет-такого-каталога") },
			wantExit:   exitBadPath,
			wantErrOut: []string{"каталог не найден"},
			wantStdout: true,
			noDump:     true,
		},
		{
			name:       "output-dir указывает на файл — ошибка записи даёт код 1",
			prepare:    dumpGoodDir,
			wantExit:   exitProblems,
			wantErrOut: []string{"не удалось создать каталог"},
			prepareOut: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "файл-а-не-каталог")
				writeFixture(t, path, []byte("уже файл"))
				return path
			},
		},
		{
			name:       "docs.json занят каталогом — ошибка создания файла, код 1",
			prepare:    dumpGoodDir,
			wantExit:   exitProblems,
			wantErrOut: []string{"не удалось создать"},
			prepareOut: func(t *testing.T) string {
				out := filepath.Join(t.TempDir(), "выгрузка")
				if err := os.MkdirAll(filepath.Join(out, dumpFileName), 0o755); err != nil {
					t.Fatalf("создание каталога %s: %v", dumpFileName, err)
				}
				return out
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputDir := tc.prepare(t)
			outputDir := filepath.Join(t.TempDir(), "выгрузка", "глубже")
			if tc.prepareOut != nil {
				outputDir = tc.prepareOut(t)
			}

			code, stdout, stderr := dumpRun(t, inputDir, outputDir)
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
			if tc.wantStdout && stdout != "" {
				t.Errorf("stdout должен быть пуст, получили\n%s", stdout)
			}
			if tc.noDump {
				if _, err := os.Stat(filepath.Join(outputDir, dumpFileName)); !os.IsNotExist(err) {
					t.Errorf("docs.json не должен создаваться при коде %d (err = %v)", tc.wantExit, err)
				}
				return
			}
			if tc.verify == nil {
				return
			}
			records, raw := readDumpJSON(t, outputDir)
			tc.verify(t, records, raw, inputDir, outputDir)
		})
	}
}

// Метаданные записей: inn/kpp/dates из карточки P1.5, повторы схлопнуты
// appendUnique, inn собирает обе длины ИНН, чужие виды (ОГРН, сумма) внутрь не попадают.
func TestRunDumpDocsMetadata(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantFile string
		want     dumpMetadata
	}{
		{
			name:     "ИНН, КПП и дата повторены — по одному значению",
			body:     textParagraph(cliTextDoc) + textParagraph(cliTextDoc),
			wantFile: "анкетаA.docx",
			want:     dumpMetadata{INN: []string{cliINN}, KPP: []string{cliKPP}, Dates: []string{"2026-02-01"}},
		},
		{
			name:     "ИНН физлица (12 цифр) попадает в inn",
			body:     textParagraph("ИП: ИНН " + innIP12),
			wantFile: "анкетаB.docx",
			want:     dumpMetadata{INN: []string{innIP12}, KPP: []string{}, Dates: []string{}},
		},
		{
			name:     "документ без реквизитов — пустые списки, не null",
			body:     textParagraph("Памятка участника: читать внимательно"),
			wantFile: "памятка.docx",
			want:     dumpMetadata{INN: []string{}, KPP: []string{}, Dates: []string{}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDocxFile(t, dir, tc.wantFile, tc.body)
			outputDir := filepath.Join(t.TempDir(), "выгрузка")

			code, stdout, stderr := dumpRun(t, dir, outputDir)
			if code != exitOK {
				t.Fatalf("код выхода = %d, хотели 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			records, raw := readDumpJSON(t, outputDir)
			requireDumpSchema(t, raw)
			rec := findDumpRecord(t, records, tc.wantFile)
			if !reflect.DeepEqual(rec.Metadata, tc.want) {
				t.Errorf("metadata = %+v, хотели %+v", rec.Metadata, tc.want)
			}
		})
	}
}

// Регресс чужих контрактов: --output-dir принадлежит только dump-docs и check.
func TestRunDumpDocsFlags(t *testing.T) {
	inputDir := dumpGoodDir(t)
	docsFile := filepath.Join(inputDir, "памятка.txt")

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantErrOut []string
	}{
		{
			name:       "dump-docs без --output-dir",
			args:       []string{"--command", "dump-docs", "--input-dir", "{in}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан каталог вывода", "dump-docs", "--output-dir"},
		},
		{
			name:       "dump-docs с пустым --output-dir",
			args:       []string{"--command", "dump-docs", "--input-dir", "{in}", "--output-dir", "  "},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан каталог вывода"},
		},
		{
			name:       "dump-docs запрещает --file",
			args:       []string{"--command", "dump-docs", "--input-dir", "{in}", "--output-dir", "{out}", "--file", docsFile},
			wantExit:   exitUsage,
			wantErrOut: []string{"--file не используется в dump-docs"},
		},
		{
			name:       "dump-docs запрещает --with-text",
			args:       []string{"--command", "dump-docs", "--input-dir", "{in}", "--output-dir", "{out}", "--with-text"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--with-text не используется в dump-docs"},
		},
		{
			name:       "dump-docs без --input-dir",
			args:       []string{"--command", "dump-docs", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан вход"},
		},
		{
			name:       "--output-dir вместе со scan",
			args:       []string{"--input-dir", "{in}", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--output-dir используется только командами dump-docs и check"},
		},
		{
			name:       "--output-dir вместе с trace",
			args:       []string{"--command", "trace", "--input-dir", "{in}", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--output-dir используется только командами dump-docs и check"},
		},
		{
			name:       "--output-dir вместе с extract-reqs",
			args:       []string{"--command", "extract-reqs", "--input-dir", "{in}", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--output-dir используется только командами dump-docs и check"},
		},
		{
			name:       "--output-dir вместе с parse-docx",
			args:       []string{"--command", "parse-docx", "--file", "{file}", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--output-dir используется только командами dump-docs и check"},
		},
		{
			name:       "--output-dir вместе с parse-xlsx",
			args:       []string{"--command", "parse-xlsx", "--file", "{file}", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--output-dir используется только командами dump-docs и check"},
		},
		{
			name:       "--output-dir вместе с parse-pdf",
			args:       []string{"--command", "parse-pdf", "--file", "{file}", "--output-dir", "{out}"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--output-dir используется только командами dump-docs и check"},
		},
		{
			name:     "--format=json для dump-docs принимается и не влияет",
			args:     []string{"--command", "dump-docs", "--input-dir", "{in}", "--output-dir", "{out}", "--format", "json"},
			wantExit: exitOK,
		},
		{
			name:       "неизвестный формат у dump-docs всё равно ошибка",
			args:       []string{"--command", "dump-docs", "--input-dir", "{in}", "--output-dir", "{out}", "--format", "xml"},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --format "xml"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "out")
			args := make([]string, len(tc.args))
			for i, a := range tc.args {
				a = strings.ReplaceAll(a, "{in}", inputDir)
				a = strings.ReplaceAll(a, "{out}", outDir)
				a = strings.ReplaceAll(a, "{file}", filepath.Join(inputDir, "Пакет", "заявка.docx"))
				args[i] = a
			}

			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != tc.wantExit {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantExit, stdout.String(), stderr.String())
			}
			for _, want := range tc.wantErrOut {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("в stderr нет %q\n%s", want, stderr.String())
				}
			}
			if tc.wantExit != exitOK && stdout.Len() > 0 {
				t.Errorf("при коде %d stdout должен быть пуст, получили\n%s", tc.wantExit, stdout.String())
			}
			if tc.wantExit == exitUsage {
				if _, err := os.Stat(filepath.Join(outDir, dumpFileName)); !os.IsNotExist(err) {
					t.Errorf("ошибка флагов не должна создавать docs.json (err = %v)", err)
				}
			}
		})
	}
}

func TestDumpAppendUnique(t *testing.T) {
	tests := []struct {
		name string
		have []string
		add  []string
		want []string
	}{
		{name: "первое значение", have: []string{}, add: []string{"7714567895"}, want: []string{"7714567895"}},
		{
			name: "повтор не дописывается, порядок первого появления сохраняется",
			have: []string{},
			add:  []string{"второе", "первое", "второе", "третье", "первое"},
			want: []string{"второе", "первое", "третье"},
		},
		{name: "пустой вход — ничего не добавлено", have: []string{"x"}, add: nil, want: []string{"x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.have
			for _, v := range tc.add {
				got = appendUnique(got, v)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("appendUnique = %v, хотели %v", got, tc.want)
			}
		})
	}
}

func TestDumpMetaOf(t *testing.T) {
	tests := []struct {
		name string
		card *reqs.Card
		want dumpMetadata
	}{
		{
			name: "пустая карточка — незамолчанные пустые списки",
			card: &reqs.Card{},
			want: dumpMetadata{INN: []string{}, KPP: []string{}, Dates: []string{}},
		},
		{
			name: "обе длины ИНН в inn, кпп и dates по видам, огРН и сумма мимо",
			card: &reqs.Card{Items: []reqs.Item{
				{Kind: reqs.KindINN10, Value: cliINN},
				{Kind: reqs.KindINN12, Value: innIP12},
				{Kind: reqs.KindKPP, Value: cliKPP},
				{Kind: reqs.KindOGRN, Value: cliOGRN},
				{Kind: reqs.KindDate, Value: "2026-02-01"},
				{Kind: reqs.KindMoney, Value: "5000000.00 RUB"},
			}},
			want: dumpMetadata{INN: []string{cliINN, innIP12}, KPP: []string{cliKPP}, Dates: []string{"2026-02-01"}},
		},
		{
			name: "повтор вида схлопывается в одно значение",
			card: &reqs.Card{Items: []reqs.Item{
				{Kind: reqs.KindINN10, Value: cliINN},
				{Kind: reqs.KindINN10, Value: cliINN},
				{Kind: reqs.KindDate, Value: "2026-02-01"},
				{Kind: reqs.KindDate, Value: "2026-02-01"},
			}},
			want: dumpMetadata{INN: []string{cliINN}, KPP: []string{}, Dates: []string{"2026-02-01"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dumpMetaOf(tc.card); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dumpMetaOf = %+v, хотели %+v", got, tc.want)
			}
		})
	}
}

func TestDumpNewMetadataNotEmpty(t *testing.T) {
	m := newDumpMetadata()
	if m.INN == nil || m.KPP == nil || m.Dates == nil {
		t.Errorf("newDumpMetadata обязан давать непустые (не nil) списки: %+v", m)
	}
}

func TestDumpSkipNote(t *testing.T) {
	tests := []struct {
		name  string
		entry scanner.Entry
		want  string
	}{
		{
			name:  "problem важнее note",
			entry: scanner.Entry{Problem: "символическая ссылка", Note: "формат .docx не входит в P1"},
			want:  "символическая ссылка",
		},
		{
			name:  "без problem — классификационное note",
			entry: scanner.Entry{Note: "архив вне перечня P1 (.docx/.xlsx/.pdf)"},
			want:  "архив вне перечня P1 (.docx/.xlsx/.pdf)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipNote(tc.entry); got != tc.want {
				t.Errorf("skipNote = %q, хотели %q", got, tc.want)
			}
		})
	}
}

func TestDumpUsageMentionedInHelp(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"dump-docs", "--output-dir", "--command", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}

// failingDumpWriter имитирует сбой записи (диск/поток), недостижимый на обычном
// t.TempDir(): именно он обязан превращаться в ошибку сериализации docs.json.
type failingDumpWriter struct{}

func (failingDumpWriter) Write([]byte) (int, error) {
	return 0, errors.New("диск кончился")
}

func TestDumpEncodeJSONWriteError(t *testing.T) {
	tests := []struct {
		name    string
		writer  io.Writer
		wantErr string
	}{
		{
			name:    "сбой записи оборачивается в ошибку сериализации",
			writer:  failingDumpWriter{},
			wantErr: "сериализация docs.json",
		},
		{
			name:    "успешная запись не даёт ошибки",
			writer:  io.Discard,
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			records := []dumpRecord{{
				FilePath: "Пакет/заявка.docx", DocType: "docx",
				Metadata: newDumpMetadata(), Fragments: []trace.Fragment{},
			}}
			err := encodeDumpJSON(tc.writer, records)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("encodeDumpJSON вернул ошибку: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("ожидали ошибку с %q, получили nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("ошибка = %q, хотели содержать %q", err, tc.wantErr)
			}
		})
	}
}
