package parser

import (
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"tenderguy/internal/trace"
)

// Тесты P1.6: у каждого извлечённого фрагмента должен быть адрес в источнике —
// «файл + страница/пункт». Проверяется на синтетических документах: реальных
// файлов заказчика в репозитории нет (это предмет P0), а свойство адресации от
// них не зависит.

// addresses — карта «адрес → содержимое» по фрагментам: и самому фрагменту, и
// каждому его сегменту (клетке строки таблицы).
func addresses(frags []trace.Fragment) map[string]string {
	out := map[string]string{}
	for _, f := range frags {
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		out[f.Ref.String()] = f.Text
		for _, s := range f.Segments {
			out[s.Ref.String()] = f.Text[s.Start:s.End]
		}
	}
	return out
}

func TestDocxBlockPageFollowsWordMarkers(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantPages []int
	}{
		{
			// Отметку разбивки Word ставит перед текстом новой страницы, поэтому
			// абзац, начинающийся с неё, относится уже к следующей странице.
			name: "отметка lastRenderedPageBreak",
			body: `<w:p><w:r><w:t>Первая</w:t></w:r></w:p>` +
				`<w:p><w:r><w:lastRenderedPageBreak/><w:t>Вторая</w:t></w:r></w:p>` +
				`<w:p><w:r><w:t>Тоже вторая</w:t></w:r></w:p>`,
			wantPages: []int{1, 2, 2},
		},
		{
			name: "явный разрыв страницы",
			body: `<w:p><w:r><w:t>До разрыва</w:t></w:r></w:p>` +
				`<w:p><w:r><w:br w:type="page"/></w:r><w:r><w:t>После разрыва</w:t></w:r></w:p>`,
			wantPages: []int{1, 2},
		},
		{
			name:      "обычный перенос строку, а не страницу",
			body:      `<w:p><w:r><w:t>А</w:t><w:br/><w:t>Б</w:t></w:r></w:p>`,
			wantPages: []int{1},
		},
		{
			// Файлы генераторов без отметок: страница неизвестна по факту, но
			// адрес блока остаётся — по номеру абзаца.
			name:      "разметки разбивки нет",
			body:      `<w:p><w:r><w:t>А</w:t></w:r></w:p><w:p><w:r><w:t>Б</w:t></w:r></w:p>`,
			wantPages: []int{1, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseBody(t, tt.body)
			if len(doc.Blocks) != len(tt.wantPages) {
				t.Fatalf("блоков %d, хотим %d", len(doc.Blocks), len(tt.wantPages))
			}
			for i, want := range tt.wantPages {
				if got := doc.Blocks[i].Page; got != want {
					t.Errorf("блок %d (%q): страница %d, хотим %d", i+1, doc.Blocks[i].Text, got, want)
				}
			}
		})
	}
}

func TestDocxFragmentsAddressParagraphsAndCells(t *testing.T) {
	body := `<w:p><w:r><w:t>Заголовок формы</w:t></w:r></w:p>` +
		`<w:tbl>` +
		`<w:tr><w:tc><w:p><w:r><w:t>ИНН</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:tbl><w:tr><w:tc><w:p><w:r><w:t>7714567895</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc></w:tr>` +
		`<w:tr><w:tc><w:tcPr><w:vMerge w:val="continue"/></w:tcPr><w:p><w:r><w:t>дубликат слияния</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>адрес участника</w:t></w:r></w:p></w:tc></w:tr>` +
		`</w:tbl>`

	doc := parseBody(t, body)
	got := addresses(doc.Fragments())

	wantAddr := func(s string) string { return doc.Path + " · стр. 1 · " + s }
	for addr, wantText := range map[string]string{
		wantAddr("абз. 1"):           "Заголовок формы",
		wantAddr("абз. 2 · яч. 1:1"): "ИНН",
		// Вложенная таблица адресуется путём от клетки-носителя.
		wantAddr("абз. 2 · яч. 1:2/1:1"): "7714567895",
		wantAddr("абз. 2 · яч. 2:2"):     "адрес участника",
	} {
		if got[addr] != wantText {
			t.Errorf("по адресу %q лежит %q, хотим %q (всё: %v)", addr, got[addr], wantText, got)
		}
	}

	// Клетку, закрытую вертикальным слиянием, Word показывает пустой — как и
	// Text(): значение остаётся по адресу клетки-носителя и не должно
	// встречаться под вторым адресом, иначе P2 посчитал бы его дважды.
	for addr, text := range got {
		if strings.Contains(text, "дубликат слияния") {
			t.Errorf("дубликат слияния ушёл во фрагмент по адресу %q", addr)
		}
	}
}

func TestXlsxFragmentsAddressCells(t *testing.T) {
	path := writeWorkbook(t, "спецификация.xlsx", func(f *excelize.File) {
		// Лист с русским именем: адрес «ТЗ!A1» должен читаться так же, как «Sheet1!A1».
		if _, err := f.NewSheet("ТЗ"); err != nil {
			panic(err)
		}
		setCells(f, "ТЗ", map[string]string{"A1": "Наименование", "B1": "Кол-во", "A2": "Лицензия", "B2": "10"})
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	got := addresses(doc.Fragments())

	for addr, wantText := range map[string]string{
		path + " · ТЗ!A1": "Наименование",
		path + " · ТЗ!B1": "Кол-во",
		path + " · ТЗ!B2": "10",
	} {
		if got[addr] != wantText {
			t.Errorf("по адресу %q лежит %q, хотим %q (всё: %v)", addr, got[addr], wantText, got)
		}
	}
}

func TestPdfFragmentsAddressPages(t *testing.T) {
	path := writePdfFixture(t, t.TempDir(), "заявка.pdf", pdfTextFixture)

	doc, err := ParsePdf(path)
	if err != nil {
		t.Fatalf("ParsePdf: %v", err)
	}
	frags := doc.Fragments()
	if len(frags) != len(doc.Pages) {
		t.Fatalf("фрагментов %d, страниц %d", len(frags), len(doc.Pages))
	}
	for i, f := range frags {
		if f.Ref.Page != i+1 {
			t.Errorf("фрагмент %d: страница %d, хотим %d", i, f.Ref.Page, i+1)
		}
		if f.Ref.Format != FormatPdf || f.Ref.File != path {
			t.Errorf("фрагмент %d: адрес %+v", i, f.Ref)
		}
	}
	if got := frags[0].Ref.String(); !strings.HasPrefix(got, path+" · стр. 1") {
		t.Errorf("адрес для человека %q", got)
	}
}

// Критерий P1.6 — 100 % трассируемость: ни одного фрагмента с содержимым и без
// адреса. Свойство проверяется по всем трём форматам сразу.
func TestEveryFragmentHasAddress(t *testing.T) {
	docx := parseBody(t, `<w:p><w:r><w:t>Абзац</w:t></w:r></w:p>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Клетка</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`)
	xlsxPath := writeWorkbook(t, "книга.xlsx", func(f *excelize.File) {
		setCells(f, "Sheet1", map[string]string{"A1": "Значение"})
	})
	xlsx, err := ParseXlsx(xlsxPath)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	pdfPath := writePdfFixture(t, t.TempDir(), "скан.pdf", pdfTextFixture)
	pdf, err := ParsePdf(pdfPath)
	if err != nil {
		t.Fatalf("ParsePdf: %v", err)
	}

	docs := map[string][]trace.Fragment{
		"docx": docx.Fragments(),
		"xlsx": xlsx.Fragments(),
		"pdf":  pdf.Fragments(),
	}
	for format, frags := range docs {
		withContent := 0
		for _, f := range frags {
			if strings.TrimSpace(f.Text) == "" {
				continue
			}
			withContent++
			if !f.Ref.Traced() {
				t.Errorf("%s: фрагмент %q без адреса (%+v)", format, f.Text, f.Ref)
			}
		}
		if withContent == 0 {
			t.Errorf("%s: ни одного фрагмента с содержимым — проверка пустая", format)
		}
	}
}
