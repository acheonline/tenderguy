package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docx-фикстуры собираются здесь заново: пакет main не может пользоваться
// неэкспортируемыми помощниками тестов internal/parser.

const cliNamespaces = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

const cliContentTypes = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`</Types>`

const cliRootRels = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`</Relationships>`

const cliDocRels = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`

func writeDocxFile(t *testing.T, dir, name, body string) string {
	t.Helper()

	return writeDocxParts(t, dir, name, true, body)
}

// writeDocxParts пишет .docx. С withRels=false получается архив без части
// word/_rels/document.xml.rels: обход признаёт его документом Word, а библиотека —
// не откроет. Именно этот случай нужно отличать от «файла вообще не .docx».
func writeDocxParts(t *testing.T, dir, name string, withRels bool, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", cliContentTypes},
		{"_rels/.rels", cliRootRels},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document ` + cliNamespaces + `><w:body>` + body + `</w:body></w:document>`},
	}
	if withRels {
		parts = append(parts, struct{ name, body string }{"word/_rels/document.xml.rels", cliDocRels})
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range parts {
		w, err := zw.Create(part.name)
		if err != nil {
			t.Fatalf("создание части %s: %v", part.name, err)
		}
		if _, err := w.Write([]byte(part.body)); err != nil {
			t.Fatalf("запись части %s: %v", part.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("закрытие архива: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
	return path
}

func textParagraph(text string) string {
	return `<w:p><w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

func tableFixture() string {
	return `<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Заголовок</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>Значение</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`
}

// docxDir возвращает каталог с двумя разбираемыми .docx, одним неоткрываемым
// и файлом, который обход не считает документом Word.
func docxDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "заявка.docx", textParagraph("Поставка RedCheck")+tableFixture())
	writeDocxFile(t, dir, "пояснение.docx", textParagraph("Пояснения к спецификации"))
	writeDocxParts(t, dir, "без-связей.docx", false, textParagraph("Текст есть, связей нет"))
	writeFixture(t, filepath.Join(dir, "не docx.docx"), []byte("PK\x03\x04 мусор"))
	return dir
}

func TestRunParseDocx(t *testing.T) {
	dir := docxDir(t)
	good := filepath.Join(dir, "заявка.docx")
	broken := filepath.Join(dir, "не docx.docx")
	absent := filepath.Join(dir, "нет-такого.docx")

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		noOut      bool
	}{
		{
			name:     "один документ, текстовый отчёт",
			args:     []string{"--command", "parse-docx", "--file", good},
			wantExit: exitOK,
			wantOut: []string{
				"Разобрано .docx: 1 — без потерь 1, с потерями 0, с ошибками 0",
				"заявка.docx",
				"Абзацев 1, таблиц 1 (строк 2, ячеек 2), символов",
				"Фрагментов исходника 3 (",
				"вне порядка 0 — без потерь",
			},
		},
		{
			name:     "с текстом",
			args:     []string{"--command", "parse-docx", "--file", good, "--with-text"},
			wantExit: exitOK,
			wantOut:  []string{"--- текст ---", "Поставка RedCheck", "Заголовок\nЗначение"},
		},
		{
			name:     "json-отчёт",
			args:     []string{"--command", "parse-docx", "--file", good, "--format", "json"},
			wantExit: exitOK,
			wantOut: []string{
				`"format": "docx"`,
				`"text": "Поставка RedCheck"`,
				`"text": "Значение"`,
				`"complete": true`,
			},
		},
		{
			name:     "json с текстом",
			args:     []string{"--command", "parse-docx", "--file", good, "--format", "json", "--with-text"},
			wantExit: exitOK,
			wantOut:  []string{`"content": "Поставка RedCheck`},
		},
		{
			name:     "несколько документов из каталога",
			args:     []string{"--command", "parse-docx", "--input-dir", dir},
			wantExit: exitProblems,
			wantOut: []string{
				"Разобрано .docx: 2 — без потерь 2, с потерями 0, с ошибками 1",
				"Ошибки разбора:",
				"без-связей.docx —",
			},
			wantErrOut: []string{"разборов с ошибками 1, с потерями 0"},
		},
		{
			name:       "файл, который не открылся",
			args:       []string{"--command", "parse-docx", "--file", broken},
			wantExit:   exitProblems,
			wantOut:    []string{"Разобрано .docx: 0", "не открылся как .docx"},
			wantErrOut: []string{"разборов с ошибками 1"},
		},
		{
			name:       "файла нет",
			args:       []string{"--command", "parse-docx", "--file", absent},
			wantExit:   exitProblems,
			wantOut:    []string{"Ошибки разбора"},
			wantErrOut: []string{"с ошибками 1"},
		},
		{
			name:     "каталог без docx",
			args:     []string{"--command", "parse-docx", "--input-dir", t.TempDir()},
			wantExit: exitOK,
			wantOut:  []string{"В нём нет .docx — разбирать нечего"},
		},
		{
			name:       "не задан вход",
			args:       []string{"--command", "parse-docx"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан вход", "--file", "--input-dir"},
			noOut:      true,
		},
		{
			name:       "file и input-dir вместе",
			args:       []string{"--command", "parse-docx", "--file", good, "--input-dir", dir},
			wantExit:   exitUsage,
			wantErrOut: []string{"выберите что-то одно"},
			noOut:      true,
		},
		{
			name:       "неизвестная команда",
			args:       []string{"--command", "ocr", "--input-dir", dir},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --command "ocr"`},
			noOut:      true,
		},
		{
			name:       "--file не для scan",
			args:       []string{"--file", good},
			wantExit:   exitUsage,
			wantErrOut: []string{"--file работает только с"},
			noOut:      true,
		},
		{
			name:       "--with-text не для scan",
			args:       []string{"--input-dir", dir, "--with-text"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--with-text работает только с"},
			noOut:      true,
		},
		{
			name:       "неизвестный формат",
			args:       []string{"--command", "parse-docx", "--file", good, "--format", "xml"},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --format "xml"`},
			noOut:      true,
		},
		{
			name:       "лишние аргументы",
			args:       []string{"--command", "parse-docx", "--file", good, "/tmp"},
			wantExit:   exitUsage,
			wantErrOut: []string{"лишние аргументы"},
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

func TestRunParseDocxJSONDecodes(t *testing.T) {
	good := writeDocxFile(t, t.TempDir(), "заявка.docx", textParagraph("Поставка RedCheck")+tableFixture())

	var out, errOut bytes.Buffer
	if code := run([]string{"--command", "parse-docx", "--file", good, "--format", "json"}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}

	var rep struct {
		Documents []struct {
			Name       string `json:"name"`
			Format     string `json:"format"`
			Paragraphs int    `json:"paragraphs"`
			Tables     int    `json:"tables"`
			Coverage   struct {
				Complete bool `json:"complete"`
			} `json:"coverage"`
			Blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"blocks"`
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
	if doc.Name != "заявка.docx" || doc.Format != "docx" {
		t.Errorf("Name/Format = %q/%q", doc.Name, doc.Format)
	}
	if doc.Paragraphs != 1 || doc.Tables != 1 {
		t.Errorf("Paragraphs/Tables = %d/%d, хотели 1/1", doc.Paragraphs, doc.Tables)
	}
	if !doc.Coverage.Complete {
		t.Error("complete = false, хотели true")
	}
	if len(doc.Blocks) != 2 || doc.Blocks[0].Text != "Поставка RedCheck" {
		t.Errorf("blocks = %+v", doc.Blocks)
	}
	if doc.Content != "" {
		t.Errorf("без --with-text в json не должно быть поля content: %q", doc.Content)
	}
}

func TestUsageMentionsParseDocxFlags(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"--command", "parse-docx", "--file", "--with-text", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
