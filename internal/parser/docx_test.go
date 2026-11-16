package parser

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Пакет OOXML объявляет пространства имён в корне document.xml. Тестовые
// документы используют те же префиксы, что пишет Word, — разбор по локальным
// именам не должен зависеть от того, объявлен префикс или namespace по умолчанию.
const testNamespaces = `xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math" ` +
	`xmlns:v="urn:schemas-microsoft-com:vml" ` +
	`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
	`xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" ` +
	`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`

const testContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`</Types>`

const testRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`</Relationships>`

const testDocRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="http://example.ru" TargetMode="External"/>` +
	`</Relationships>`

// zipPart — элемент тестового docx-архива.
type zipPart struct {
	Name string
	Body string
}

func wmlDocument(body string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` + testNamespaces + `>` +
		`<w:body>` + body + `</w:body></w:document>`
}

func writeZipDocx(t *testing.T, name string, parts []zipPart) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("создание %s: %v", path, err)
	}
	w := zip.NewWriter(f)
	for _, part := range parts {
		fw, err := w.Create(part.Name)
		if err != nil {
			f.Close()
			t.Fatalf("zip.Create(%s): %v", part.Name, err)
		}
		if _, err := fw.Write([]byte(part.Body)); err != nil {
			f.Close()
			t.Fatalf("запись %s: %v", part.Name, err)
		}
	}
	if err := w.Close(); err != nil {
		f.Close()
		t.Fatalf("закрытие zip-писателя: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("закрытие %s: %v", path, err)
	}
	return path
}

// writeDocx собирает минимальный, но полный пакет OOXML с переданным телом документа.
func writeDocx(t *testing.T, name, body string) string {
	t.Helper()

	return writeZipDocx(t, name, []zipPart{
		{Name: "[Content_Types].xml", Body: testContentTypes},
		{Name: "_rels/.rels", Body: testRootRels},
		{Name: "word/_rels/document.xml.rels", Body: testDocRels},
		{Name: "word/document.xml", Body: wmlDocument(body)},
	})
}

// parseBody разбирает тело документа и возвращает готовую модель.
func parseBody(t *testing.T, body string) *Document {
	t.Helper()

	doc, err := ParseDocx(writeDocx(t, "документ.docx", body))
	if err != nil {
		t.Fatalf("ParseDocx: непредвиденная ошибка %v", err)
	}
	return doc
}

func TestParseDocxText(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantText   string
		wantBlocks int
		wantStyle  string
		wantNote   string
		wantCount  []string // подстроки, которые должны встретиться ровно один раз
		wantAbsent []string
	}{
		{
			name:       "два абзаца",
			body:       `<w:p><w:r><w:t>Абзац первый</w:t></w:r></w:p><w:p><w:r><w:t>Абзац второй</w:t></w:r></w:p>`,
			wantText:   "Абзац первый\nАбзац второй",
			wantBlocks: 2,
		},
		{
			name:       "пустой абзац остаётся в структуре",
			body:       `<w:p/><w:p><w:r><w:t>Текст</w:t></w:r></w:p>`,
			wantText:   "\nТекст",
			wantBlocks: 2,
		},
		{
			name:       "строки одного абзаца склеиваются",
			body:       `<w:p><w:r><w:t>Red</w:t></w:r><w:r><w:t>Check</w:t></w:r></w:p>`,
			wantText:   "RedCheck",
			wantBlocks: 1,
		},
		{
			name:       "пробелы внутри w:t сохраняются",
			body:       `<w:p><w:r><w:t xml:space="preserve">  два пробела  </w:t></w:r></w:p>`,
			wantText:   "  два пробела  ",
			wantBlocks: 1,
		},
		{
			name: "табуляция и перенос строки",
			body: `<w:p><w:r><w:t>КП</w:t></w:r><w:r><w:tab/><w:t>значение</w:t></w:r>` +
				`<w:r><w:br/><w:t>вторая строка</w:t></w:r></w:p>`,
			wantText:   "КП\tзначение\nвторая строка",
			wantBlocks: 1,
		},
		{
			name:       "мягкий дефис и непереводимый дефис",
			body:       `<w:p><w:r><w:t>226717</w:t></w:r><w:r><w:noBreakHyphen/></w:r><w:r><w:t>1</w:t></w:r></w:p>`,
			wantText:   "226717-1",
			wantBlocks: 1,
		},
		{
			name:       "кириллица в числовых сущностях",
			body:       `<w:p><w:r><w:t>&#1047;&#1072;&#1103;&#1074;&#1082;&#1072;</w:t></w:r></w:p>`,
			wantText:   "Заявка",
			wantBlocks: 1,
		},
		{
			name:       "экранированные символы разворачиваются",
			body:       `<w:p><w:r><w:t>П &amp; М &lt; 100%</w:t></w:r></w:p>`,
			wantText:   "П & М < 100%",
			wantBlocks: 1,
		},
		{
			name: "текст гиперссылки и вставки правкой",
			body: `<w:p><w:r><w:t>См. </w:t></w:r>` +
				`<w:hyperlink r:id="rId2"><w:r><w:t>приложение 1</w:t></w:r></w:hyperlink>` +
				`<w:ins w:id="1"><w:r><w:t>, редакция 3</w:t></w:r></w:ins></w:p>`,
			wantText:   "См. приложение 1, редакция 3",
			wantBlocks: 1,
		},
		{
			name: "команды поля и удалённый текст не попадают в вывод",
			body: `<w:p><w:r><w:instrText>PAGE</w:instrText></w:r>` +
				`<w:fldSimple w:instr=" NUMPAGES "><w:r><w:t>12</w:t></w:r></w:fldSimple>` +
				`<w:del w:id="2"><w:r><w:delText>устаревшая формулировка</w:delText></w:r></w:del></w:p>`,
			wantText:   "12",
			wantBlocks: 1,
		},
		{
			name: "служебная разметка текста не добавляет",
			body: `<w:p><w:bookmarkStart w:id="0" w:name="_GoBack"/><w:proofErr w:type="gramStart"/>` +
				`<w:r><w:t>Текст</w:t></w:r><w:footnoteReference w:id="1"/><w:commentReference w:id="2"/>` +
				`<w:bookmarkEnd w:id="0"/></w:p>`,
			wantText:   "Текст",
			wantBlocks: 1,
		},
		{
			name:       "формула из m:t входит в текст",
			body:       `<w:p><w:r><w:t>НМЦ = </w:t></w:r><m:oMath><m:r><m:t>1 200 000</m:t></m:r></m:oMath></w:p>`,
			wantText:   "НМЦ = 1 200 000",
			wantBlocks: 1,
		},
		{
			name: "текст фигуры извлекается один раз (Choice и Fallback дублируют)",
			body: `<w:p><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:inline>` +
				`<a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
				`<w:p><w:r><w:t>Печать</w:t></w:r></w:p>` +
				`</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing>` +
				`</mc:Choice><mc:Fallback><w:pict><v:shape><v:textbox><w:txbxContent>` +
				`<w:p><w:r><w:t>Печать</w:t></w:r></w:p>` +
				`</w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback></mc:AlternateContent></w:p>`,
			wantText:   "Печать",
			wantBlocks: 1,
			wantCount:  []string{"Печать"},
		},
		{
			name: "текст фигуры из DrawingML тоже берётся один раз",
			body: `<w:p><mc:AlternateContent><mc:Choice Requires="wpg"><w:drawing><wp:inline>` +
				`<a:graphic><a:graphicData><a:wsp><a:txBody><a:p><a:r><a:t>Скидка</a:t></a:r></a:p></a:txBody></a:wsp>` +
				`</a:graphicData></a:graphic></wp:inline></w:drawing></mc:Choice>` +
				`<mc:Fallback><w:pict><v:shape><v:textbox><w:txbxContent>` +
				`<w:p><w:r><w:t>Скидка</w:t></w:r></w:p>` +
				`</w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback></mc:AlternateContent></w:p>`,
			wantText:   "Скидка",
			wantBlocks: 1,
			wantCount:  []string{"Скидка"},
			// Choice с a:t содержит текст, поэтому берётся он, а не копия из Fallback.
			wantAbsent: []string{"СкидкаСкидка"},
		},
		{
			name: "псевдоним элемента управления содержимым не считается текстом",
			body: `<w:sdt><w:sdtPr><w:alias w:val="Нажмите, чтобы ввести"/>` +
				`<w:dataBinding w:xpath="/zakupka[1]"/></w:sdtPr>` +
				`<w:sdtContent><w:p><w:r><w:t>226717</w:t></w:r></w:p></w:sdtContent></w:sdt>`,
			wantText:   "226717",
			wantBlocks: 1,
			wantAbsent: []string{"Нажмите", "zakupka"},
		},
		{
			name:       "заголовок со стилем",
			body:       `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>3. Требования к участнику</w:t></w:r></w:p>`,
			wantText:   "3. Требования к участнику",
			wantBlocks: 1,
			wantStyle:  "Heading1",
		},
		{
			name:       "неизвестный блочный элемент сохраняется как текст",
			body:       `<w:customXmlFoo><w:p><w:r><w:t>Содержимое необычного контейнера</w:t></w:r></w:p></w:customXmlFoo>`,
			wantText:   "Содержимое необычного контейнера",
			wantBlocks: 1,
			wantNote:   "неизвестный блочный элемент <customXmlFoo>",
		},
		{
			name:       "встроенная внешняя часть помечается как не разобранная",
			body:       `<w:altChunk r:id="chunk1"/>`,
			wantText:   "",
			wantBlocks: 0,
			wantNote:   "w:altChunk",
		},
		{
			name: "document.xml с необъявленной сущностью разбирается в нестрогом режиме",
			body: `<w:p><w:r><w:t>Заявка&nbsp;№1</w:t></w:r></w:p>`,
			// Символ неразрывного пробела остаётся как есть: выкинуть его — значит
			// потерять текст, которого в файле нет в разложенном виде.
			wantText:   "Заявка&nbsp;№1",
			wantBlocks: 1,
			wantNote:   "нестрогом режиме",
		},
		{
			name:       "раздел страницы не создаёт блока",
			body:       `<w:p><w:r><w:t>Текст</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr>`,
			wantText:   "Текст",
			wantBlocks: 1,
		},
		{
			name:       "документ без текста",
			body:       `<w:p><w:pPr><w:rPr><w:b/></w:rPr></w:pPr></w:p>`,
			wantText:   "",
			wantBlocks: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseBody(t, tc.body)

			if got := doc.Text(); got != tc.wantText {
				t.Errorf("Text() = %q, хотели %q", got, tc.wantText)
			}
			if len(doc.Blocks) != tc.wantBlocks {
				t.Errorf("блоков %d, хотели %d: %+v", len(doc.Blocks), tc.wantBlocks, doc.Blocks)
			}
			if tc.wantStyle != "" {
				if len(doc.Blocks) == 0 || doc.Blocks[0].Style != tc.wantStyle {
					t.Errorf("Style = %+v, хотели %q", doc.Blocks, tc.wantStyle)
				}
			}
			for _, s := range tc.wantCount {
				if n := strings.Count(doc.Text(), s); n != 1 {
					t.Errorf("подстрока %q встретилась %d раз, хотели 1 (дублирование Choice/Fallback)", s, n)
				}
			}
			for _, s := range tc.wantAbsent {
				if strings.Contains(doc.Text(), s) {
					t.Errorf("в тексте не должно быть %q: %q", s, doc.Text())
				}
			}
			if tc.wantNote != "" && !strings.Contains(strings.Join(doc.Notes, "; "), tc.wantNote) {
				t.Errorf("в замечаниях нет %q, получили %v", tc.wantNote, doc.Notes)
			}
			if !doc.Coverage.Complete {
				t.Errorf("извлечение неполное: %+v", doc.Coverage)
			}
			if doc.Coverage.MissingPieces != 0 {
				t.Errorf("потеряно фрагментов %d, хотели 0", doc.Coverage.MissingPieces)
			}
			if doc.Format != FormatDocx || doc.Name != "документ.docx" {
				t.Errorf("Format/Name = %q/%q", doc.Format, doc.Name)
			}
			if !filepath.IsAbs(doc.Path) {
				t.Errorf("Path = %q, хотели абсолютный", doc.Path)
			}
		})
	}
}

func TestParseDocxTables(t *testing.T) {
	simpleTable := `<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/></w:tblPr>` +
		`<w:tblGrid><w:gridCol w:w="100"/><w:gridCol w:w="100"/><w:gridCol w:w="100"/></w:tblGrid>` +
		`<w:tr><w:trPr><w:tblHeader/></w:trPr>` +
		`<w:tc><w:tcPr><w:gridSpan w:val="3"/></w:tcPr><w:p><w:r><w:t>Форма 2 «Заявка»</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr>` +
		`<w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>Адрес</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>ИНН</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>7801501111</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr>` +
		`<w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc>` +
		`<w:tc><w:p><w:r><w:t>КПП</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>780101001</w:t></w:r></w:p></w:tc></w:tr>` +
		`</w:tbl>`

	tests := []struct {
		name string
		body string
		// wantText — вывод Document.Text() целиком.
		wantText string
		check    func(t *testing.T, tbl *Table)
	}{
		{
			name: "таблица: сетка, слияния, заголовок",
			body: simpleTable,
			// Ячейка на всю сетку (w:gridSpan=3) занимает всю строку:
			// пустых колонок в выводе нет.
			wantText: "Форма 2 «Заявка»\n" +
				"Адрес\tИНН\t7801501111\n" +
				"\tКПП\t780101001",
			check: func(t *testing.T, tbl *Table) {
				t.Helper()
				if tbl.Columns != 3 || len(tbl.Rows) != 3 {
					t.Fatalf("Columns/Rows = %d/%d, хотели 3/3", tbl.Columns, len(tbl.Rows))
				}
				if tbl.Style != "TableGrid" {
					t.Errorf("Style = %q, хотели TableGrid", tbl.Style)
				}
				if !tbl.Rows[0].Header {
					t.Error("строка 1 не помечена как заголовок")
				}
				if tbl.Rows[1].Header {
					t.Error("строка 2 ошибочно помечена как заголовок")
				}
				if got := tbl.Rows[0].Cells[0].ColSpan; got != 3 {
					t.Errorf("ColSpan = %d, хотели 3", got)
				}
				if got := tbl.Rows[1].Cells[0].RowSpan; got != 2 {
					t.Errorf("RowSpan = %d, хотели 2", got)
				}
				if !tbl.Rows[2].Cells[0].Merged {
					t.Error("строка 3 ячейка 1 должна быть продолжением слияния")
				}
				if got := tbl.Rows[2].Cells[0].Text; got != "" {
					t.Errorf("текст продолжения слияния = %q, хотели пустое", got)
				}
				for i, row := range tbl.Rows {
					if row.Index != i+1 {
						t.Errorf("Index строки %d = %d, хотели %d", i, row.Index, i+1)
					}
				}
			},
		},
		{
			name: "текст в закрытой слиянием ячейке сохраняется в модели, но не в выводе",
			body: `<w:tbl><w:tr>` +
				`<w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>Сверху</w:t></w:r></w:p></w:tc>` +
				`</w:tr><w:tr>` +
				`<w:tc><w:tcPr><w:vMerge w:val="continue"/></w:tcPr><w:p><w:r><w:t>Дублират</w:t></w:r></w:p></w:tc>` +
				`</w:tr></w:tbl>`,
			wantText: "Сверху",
			check: func(t *testing.T, tbl *Table) {
				t.Helper()
				if got := tbl.Rows[1].Cells[0].Text; got != "Дублират" {
					t.Errorf("Cell.Text = %q, хотели сохранённый текст скрытой ячейки", got)
				}
				if got := tbl.Rows[0].Cells[0].RowSpan; got != 2 {
					t.Errorf("RowSpan = %d, хотели 2", got)
				}
			},
		},
		{
			name: "вложенная таблица остаётся структурой ячейки",
			body: `<w:tbl><w:tr>` +
				`<w:tc><w:p><w:r><w:t>Позиция</w:t></w:r></w:p></w:tc>` +
				`<w:tc><w:p><w:r><w:t>Спецификация</w:t></w:r></w:p>` +
				`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>WAF Juniper</w:t></w:r></w:p></w:tc>` +
				`<w:tc><w:p><w:r><w:t>12 шт</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc>` +
				`</w:tr></w:tbl>`,
			wantText: "Позиция\tСпецификация WAF Juniper 12 шт",
			check: func(t *testing.T, tbl *Table) {
				t.Helper()
				nested := tbl.Rows[0].Cells[1].Tables
				if len(nested) != 1 {
					t.Fatalf("вложенных таблиц %d, хотели 1", len(nested))
				}
				if nested[0].Depth != 1 {
					t.Errorf("Depth вложенной = %d, хотели 1", nested[0].Depth)
				}
				if got := nested[0].Rows[0].Cells[0].Text; got != "WAF Juniper" {
					t.Errorf("ячейка вложенной таблицы = %q", got)
				}
				if strings.Contains(tbl.Rows[0].Cells[1].Text, "WAF") {
					t.Error("текст вложенной таблицы не должен дублироваться в Cell.Text")
				}
			},
		},
		{
			name: "несколько абзацев в ячейке",
			body: `<w:tbl><w:tr><w:tc><w:p><w:r><w:t>строка 1</w:t></w:r></w:p>` +
				`<w:p><w:r><w:t>строка 2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`,
			wantText: "строка 1 строка 2",
			check: func(t *testing.T, tbl *Table) {
				t.Helper()
				if got := tbl.Rows[0].Cells[0].Text; got != "строка 1\nстрока 2" {
					t.Errorf("Cell.Text = %q, хотели абзацы через перевод строки", got)
				}
			},
		},
		{
			name: "строки и ячейки под обёрткой sdt",
			body: `<w:tbl><w:sdt><w:sdtContent>` +
				`<w:tr><w:sdt><w:sdtContent><w:tc><w:p><w:r><w:t>Значение</w:t></w:r></w:p></w:tc></w:sdtContent></w:sdt></w:tr>` +
				`</w:sdtContent></w:sdt></w:tbl>`,
			wantText: "Значение",
			check: func(t *testing.T, tbl *Table) {
				t.Helper()
				if len(tbl.Rows) != 1 || len(tbl.Rows[0].Cells) != 1 {
					t.Fatalf("структура таблицы %+v, хотели 1x1", tbl.Rows)
				}
			},
		},
		{
			name: "несколько таблиц в теле",
			body: `<w:tbl><w:tr><w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
				`<w:p><w:r><w:t>между таблицами</w:t></w:r></w:p>` +
				`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`,
			wantText: "A\nмежду таблицами\nB",
			check: func(t *testing.T, tbl *Table) {
				t.Helper()
				if tbl.Columns != 1 {
					t.Errorf("Columns = %d, хотели 1", tbl.Columns)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseBody(t, tc.body)
			if got := doc.Text(); got != tc.wantText {
				t.Errorf("Text() = %q, хотели %q", got, tc.wantText)
			}
			if !doc.Coverage.Complete {
				t.Errorf("извлечение неполное: %+v", doc.Coverage)
			}
			if doc.Coverage.OutOfOrder != 0 {
				t.Errorf("фрагментов вне порядка %d, хотели 0", doc.Coverage.OutOfOrder)
			}
			var first *Table
			for _, b := range doc.Blocks {
				if b.Table != nil {
					first = b.Table
					break
				}
			}
			if first == nil {
				t.Fatal("в модели нет ни одной таблицы")
			}
			tc.check(t, first)
		})
	}
}

// TestParseDocxStructure проверяет нумерацию блоков и счётчики: на них опирается
// трассировка «документ + параграф» (P1.6) и замер доли разобранных (P1.7).
func TestParseDocxStructure(t *testing.T) {
	doc := parseBody(t, `<w:p><w:r><w:t>1</w:t></w:r></w:p>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+
		`<w:p><w:r><w:t>3</w:t></w:r></w:p>`)

	wantIndexes := []int{1, 2, 3}
	if len(doc.Blocks) != len(wantIndexes) {
		t.Fatalf("блоков %d, хотели %d", len(doc.Blocks), len(wantIndexes))
	}
	for i, want := range wantIndexes {
		if doc.Blocks[i].Index != want {
			t.Errorf("Blocks[%d].Index = %d, хотели %d", i, doc.Blocks[i].Index, want)
		}
	}
	if doc.Blocks[0].Type != BlockParagraph || doc.Blocks[1].Type != BlockTable {
		t.Errorf("типы блоков = %q/%q", doc.Blocks[0].Type, doc.Blocks[1].Type)
	}
	if doc.Paragraphs != 2 || doc.Tables != 1 || doc.Rows != 1 || doc.Cells != 1 {
		t.Errorf("Paragraphs/Tables/Rows/Cells = %d/%d/%d/%d, хотели 2/1/1/1",
			doc.Paragraphs, doc.Tables, doc.Rows, doc.Cells)
	}
	if doc.Coverage.SourceTables != 1 || doc.Coverage.SourceCells != 1 {
		t.Errorf("эталон структуры = %d/%d, хотели 1/1", doc.Coverage.SourceTables, doc.Coverage.SourceCells)
	}
	if want := "1\n2\n3"; doc.Text() != want {
		t.Errorf("Text() = %q, хотели %q", doc.Text(), want)
	}
}

// TestParseDocxKeepsEveryFragment — прямая проверка критерия «без потерь»:
// каждый текстовый фрагмент document.xml присутствует в извлечённой модели.
func TestParseDocxKeepsEveryFragment(t *testing.T) {
	fragments := []string{
		"226717-ЭФ/Д",
		"Извещение о проведении закупки",
		"Начальная (максимальная) цена договора: 4 950 000,00 руб.",
		"Инструкция по заполнению формы 2 «Заявка»",
		"Участник закупки обязан приложить выписку ЕГРЮЛ",
		"ИНН 7801501111 КПП 780101001",
		"Срок подачи заявки: 19.11.2026 10:00",
		"Требования к участнику: лицензия ФСТЭК",
		"Обеспечение заявки — 1 % от НМЦД",
		"Приложение 1. Техническое задание",
	}

	var sb strings.Builder
	for _, f := range fragments {
		sb.WriteString("<w:p><w:r><w:t>" + f + "</w:t></w:r></w:p>")
	}
	doc := parseBody(t, sb.String())

	if !doc.Coverage.Complete {
		t.Fatalf("есть потери: %+v", doc.Coverage)
	}
	if doc.Coverage.SourcePieces != len(fragments) {
		t.Errorf("фрагментов в эталоне %d, хотели %d", doc.Coverage.SourcePieces, len(fragments))
	}
	text := doc.Text()
	for _, f := range fragments {
		if !strings.Contains(text, f) {
			t.Errorf("в извлечённом тексте нет фрагмента %q", f)
		}
	}
	// Модель не может быть короче исходника: в неё добавлены только разделители блоков.
	if doc.Runes < doc.Coverage.SourceRunes {
		t.Errorf("Runes = %d, эталон %d", doc.Runes, doc.Coverage.SourceRunes)
	}
}

// TestCheckCoverageDetectsLoss проверяет сам механизм: модель с намеренно
// выброшенным абзацем должна быть признана неполной.
func TestCheckCoverageDetectsLoss(t *testing.T) {
	body := `<w:p><w:r><w:t>Абзац сохранён</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Абзац потерян полностью</w:t></w:r></w:p>`
	root, _, err := buildTree(wmlDocument(body))
	if err != nil {
		t.Fatalf("buildTree: %v", err)
	}
	doc := &Document{Blocks: []Block{{Type: BlockParagraph, Text: "Абзац сохранён"}}}
	doc.Runes = runeCount(doc.extractedBlob())

	cov := checkCoverage(root.descendant("body"), doc)
	if cov.Complete {
		t.Fatalf("проверка полноты пропущена, хотя фрагмент потерян: %+v", cov)
	}
	if cov.MissingPieces != 1 {
		t.Errorf("MissingPieces = %d, хотели 1", cov.MissingPieces)
	}
	if len(cov.Missing) != 1 || cov.Missing[0] != "Абзац потерян полностью" {
		t.Errorf("Missing = %q", cov.Missing)
	}
}

func TestSampleTruncatesLongFragment(t *testing.T) {
	long := strings.Repeat("а", missingSampleRunes+10)
	got := sample(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("длинный фрагмент не отсечён: %q", got)
	}
	if n := len([]rune(got)); n != missingSampleRunes+1 {
		t.Errorf("длина отсечки = %d, хотели %d", n, missingSampleRunes+1)
	}
	if short := sample("короткий"); short != "короткий" {
		t.Errorf("короткий фрагмент изменён: %q", short)
	}
}

func TestParseDocxErrors(t *testing.T) {
	dir := t.TempDir()
	notZip := filepath.Join(dir, "мусор.docx")
	if err := os.WriteFile(notZip, []byte("PK\x03\x04 тут не архив"), 0o600); err != nil {
		t.Fatalf("запись %s: %v", notZip, err)
	}
	noParts := writeZipDocx(t, "без-частей.docx", []zipPart{{Name: "readme.txt", Body: "не docx"}})
	noBody := writeZipDocx(t, "без-тела.docx", []zipPart{
		{Name: "[Content_Types].xml", Body: testContentTypes},
		{Name: "_rels/.rels", Body: testRootRels},
		{Name: "word/_rels/document.xml.rels", Body: testDocRels},
		{Name: "word/document.xml", Body: `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:tbl/></w:document>`},
	})
	emptyDocument := writeZipDocx(t, "пустой-document.docx", []zipPart{
		{Name: "[Content_Types].xml", Body: testContentTypes},
		{Name: "_rels/.rels", Body: testRootRels},
		{Name: "word/_rels/document.xml.rels", Body: testDocRels},
		{Name: "word/document.xml", Body: "   "},
	})

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "пустой путь", path: "   ", wantErr: "не задан путь к .docx"},
		{name: "файла нет", path: filepath.Join(dir, "нет-такого.docx"), wantErr: "не открылся как .docx"},
		{name: "не OOXML", path: notZip, wantErr: "не открылся как .docx"},
		{name: "архив без частей OOXML", path: noParts, wantErr: "не открылся как .docx"},
		{name: "документ без w:body", path: noBody, wantErr: "нет тела документа"},
		{name: "пустая часть document.xml", path: emptyDocument, wantErr: "пустая"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseDocx(tc.path)
			if err == nil {
				t.Fatalf("ожидали ошибку, получили модель %+v", doc)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("в тексте ошибки нет %q: %v", tc.wantErr, err)
			}
			if doc != nil {
				t.Errorf("при ошибке модель должна быть nil, получили %+v", doc)
			}
		})
	}
}

func TestParseDocxJSONRoundTrip(t *testing.T) {
	doc := parseBody(t, `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Заголовок</w:t></w:r></w:p>`+
		`<w:tbl>`+
		`<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>Ячейка</w:t></w:r></w:p></w:tc></w:tr>`+
		`<w:tr><w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>Слияние</w:t></w:r></w:p></w:tc>`+
		`<w:tc><w:p><w:r><w:t>Обычная</w:t></w:r></w:p></w:tc></w:tr>`+
		`<w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc><w:tc><w:p/></w:tc></w:tr>`+
		`</w:tbl>`)

	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("сериализация модели: %v", err)
	}
	var got Document
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("модель не возвращается из JSON: %v", err)
	}

	if got.Name != doc.Name || got.Format != doc.Format || len(got.Blocks) != len(doc.Blocks) {
		t.Fatalf("верхний уровень потерялся: %+v", got)
	}
	if got.Blocks[0].Style != "Heading1" || got.Blocks[0].Text != "Заголовок" {
		t.Errorf("абзац после JSON = %+v", got.Blocks[0])
	}
	if got.Blocks[1].Index != 2 || got.Blocks[1].Type != BlockTable {
		t.Errorf("нумерация блоков после JSON = %+v", got.Blocks[1])
	}
	tbl := got.Blocks[1].Table
	if tbl == nil {
		t.Fatal("таблица после JSON пропала")
	}
	if got := tbl.Rows[0].Cells[0]; got.Text != "Ячейка" || got.ColSpan != 2 {
		t.Errorf("ячейка после JSON = %+v", got)
	}
	if got := tbl.Rows[1].Cells[0]; got.Text != "Слияние" || got.RowSpan != 2 {
		t.Errorf("ячейка слияния после JSON = %+v", got)
	}
	if got := tbl.Rows[2].Cells[0]; !got.Merged {
		t.Errorf("продолжение слияния после JSON = %+v", got)
	}
	if !got.Coverage.Complete || got.Coverage.SourcePieces != 4 {
		t.Errorf("полнота после JSON = %+v", got.Coverage)
	}
	for _, key := range []string{`"blocks"`, `"coverage"`, `"complete"`, `"col_span"`, `"row_span"`, `"merged"`, `"source_pieces"`, `"text_runes"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("в JSON нет поля %q:\n%s", key, raw)
		}
	}
}

func TestBuildTreeRejectsBrokenXML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		// wantLenient — разбор возможен только во втором проходе.
		wantLenient bool
		wantErr     string
	}{
		{name: "корректный xml", content: wmlDocument(`<w:p><w:r><w:t>Текст</w:t></w:r></w:p>`)},
		{name: "необъявленная сущность", content: wmlDocument(`<w:p><w:r><w:t>А&nbsp;Б</w:t></w:r></w:p>`), wantLenient: true},
		{name: "обрывок посередине тега", content: `<w:document><w:body><w:p><w:r><w:t>А`, wantErr: "нестрогий разбор тоже не удался"},
		{name: "закрывающий тег не открытого элемента", content: `<w:document><w:body></w:oops></w:body></w:document>`, wantErr: "нестрогий разбор тоже не удался"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, lenient, err := buildTree(tc.content)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ожидали ошибку, получили дерево %+v", root)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("в тексте ошибки нет %q: %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildTree: %v", err)
			}
			if lenient != tc.wantLenient {
				t.Errorf("lenient = %v, хотели %v", lenient, tc.wantLenient)
			}
			if root == nil {
				t.Fatal("дерево не построено")
			}
		})
	}
}

func TestNodeHelpers(t *testing.T) {
	root, _, err := buildTree(wmlDocument(
		`<w:p w:rsidR="00A1"><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Т</w:t></w:r></w:p>`))
	if err != nil {
		t.Fatalf("buildTree: %v", err)
	}
	body := root.descendant("body")
	para := body.child("p")

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"локальное имя", para.local(), "p"},
		{"атрибут без префикса", para.attrVal("rsidR"), "00A1"},
		{"атрибута нет", para.attrVal("нет-такого"), ""},
		{"дочерний элемент", para.child("pPr").child("pStyle").attrVal("val"), "Heading1"},
		{"несуществующий ребёнок", para.child("nope"), (*node)(nil)},
		{"nil-получатель", para.child("nope").attrVal("val"), ""},
		{"числовой атрибут", body.child("p").child("pPr").intAttr("nope"), 0},
		{"поиск тела в глубину", root.descendant("body").local(), "body"},
		{"тела нет", root.descendant("nope"), (*node)(nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("= %#v, хотели %#v", tc.got, tc.want)
			}
		})
	}

	if got := body.effective(); len(got) != 1 {
		t.Errorf("effective дало %d элементов, хотели 1", len(got))
	}
	if !hasWordText(para) {
		t.Error("hasWordText не увидел текст в абзаце")
	}
	if hasWordText(body.child("p").child("pPr")) {
		t.Error("hasWordText нашёл текст в свойствах абзаца")
	}
}
