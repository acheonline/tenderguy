package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeWorkbook собирает книгу кодом excelize и сохраняет её в тестовый
// каталог: фикстуры .xlsx генерируются на месте, бинарных файлов в репозитории
// нет (тот же подход, что с pdf-фикстурами, только источник — сама библиотека).
func writeWorkbook(t *testing.T, name string, build func(f *excelize.File)) string {
	t.Helper()
	f := excelize.NewFile()
	if build != nil {
		build(f)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("сохранение книги %s: %v", name, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("закрытие книги %s: %v", name, err)
	}
	return path
}

// setCells расставляет значения по адресам вида {"A1": "текст"}.
func setCells(f *excelize.File, sheet string, values map[string]string) {
	for ref, v := range values {
		if err := f.SetCellValue(sheet, ref, v); err != nil {
			panic(err)
		}
	}
}

// findCell ищет ячейку модели по имени листа и адресу.
func findCell(doc *XlsxDocument, sheetName, ref string) (XlsxCell, bool) {
	for _, sh := range doc.Sheets {
		if sh.Name != sheetName {
			continue
		}
		for _, row := range sh.Rows {
			for _, c := range row.Cells {
				if c.Ref == ref {
					return c, true
				}
			}
		}
	}
	return XlsxCell{}, false
}

func TestParseXlsxGrid(t *testing.T) {
	path := writeWorkbook(t, "сетка.xlsx", func(f *excelize.File) {
		setCells(f, "Sheet1", map[string]string{
			"A1": "Наименование", "B1": "Кол-во", "C1": "Цена",
			"A2": "Лицензия RedCheck", "B2": "10", "C2": "495000",
			"A3": "Сервер", "B3": "2", "C3": "120000",
		})
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if doc.Format != FormatXlsx {
		t.Errorf("Format = %q, хотели %q", doc.Format, FormatXlsx)
	}
	if !doc.Complete() {
		t.Errorf("полнота = %+v, хотели потерь без", doc.Coverage)
	}
	if len(doc.Sheets) != 1 || doc.Sheets[0].Name != "Sheet1" {
		t.Fatalf("листы = %+v", doc.Sheets)
	}
	if doc.Rows != 3 || doc.Cells != 9 || doc.Coverage.SourceCells != 9 {
		t.Errorf("строк %d (хотели 3), ячеек %d (хотели 9), в исходнике %d",
			doc.Rows, doc.Cells, doc.Coverage.SourceCells)
	}
	cell, ok := findCell(doc, "Sheet1", "A2")
	if !ok {
		t.Fatal("ячейка A2 не найдена в модели")
	}
	if cell.Value != "Лицензия RedCheck" || cell.Row != 2 || cell.Col != 1 {
		t.Errorf("A2 = %+v", cell)
	}
	text := doc.Text()
	for _, want := range []string{"Лист: Sheet1", "Наименование\tКол-во\tЦена"} {
		if !strings.Contains(text, want) {
			t.Errorf("в Text нет %q:\n%s", want, text)
		}
	}
}

func TestParseXlsxSheetsOrder(t *testing.T) {
	path := writeWorkbook(t, "листы.xlsx", func(f *excelize.File) {
		setCells(f, "Sheet1", map[string]string{"A1": "первый"})
		if _, err := f.NewSheet("Шаблон"); err != nil {
			panic(err)
		}
		setCells(f, "Шаблон", map[string]string{"B2": "второй"})
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if !doc.Complete() {
		t.Errorf("полнота = %+v", doc.Coverage)
	}
	if len(doc.Sheets) != 2 {
		t.Fatalf("листов %d, хотели 2: %+v", len(doc.Sheets), doc.Sheets)
	}
	if doc.Sheets[0].Index != 1 || doc.Sheets[1].Index != 2 {
		t.Errorf("порядок листов %+v", doc.Sheets)
	}
	if _, ok := findCell(doc, "Шаблон", "B2"); !ok {
		t.Error("ячейка второго листа не найдена")
	}
}

func TestParseXlsxFormulas(t *testing.T) {
	path := writeWorkbook(t, "формулы.xlsx", func(f *excelize.File) {
		setCells(f, "Sheet1", map[string]string{"A1": "1", "A2": "2", "B3": "итог"})
		if err := f.SetCellFormula("Sheet1", "B1", "SUM(A1:A2)"); err != nil {
			panic(err)
		}
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if !doc.Complete() {
		t.Errorf("полнота = %+v, хотели потерь без: формульная клетка учтена формулой", doc.Coverage)
	}
	if doc.Coverage.SourceFormulas != 1 || doc.Formulas != 1 {
		t.Errorf("формульных: в исходнике %d, в модели %d, хотели 1 и 1",
			doc.Coverage.SourceFormulas, doc.Formulas)
	}
	cell, ok := findCell(doc, "Sheet1", "B1")
	if !ok {
		t.Fatal("ячейка B1 с формулой не найдена")
	}
	if cell.Formula != "SUM(A1:A2)" {
		t.Errorf("формула B1 = %q, хотели SUM(A1:A2)", cell.Formula)
	}
}

func TestParseXlsxMergesAndHidden(t *testing.T) {
	path := writeWorkbook(t, "шапка.xlsx", func(f *excelize.File) {
		setCells(f, "Sheet1", map[string]string{"A1": "Спецификация", "A3": "позиция"})
		if err := f.MergeCell("Sheet1", "A1", "C1"); err != nil {
			panic(err)
		}
		if _, err := f.NewSheet("data"); err != nil {
			panic(err)
		}
		setCells(f, "data", map[string]string{"A1": "код", "A2": "26.20.40.140"})
		if err := f.SetSheetVisible("data", false); err != nil {
			panic(err)
		}
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if !doc.Complete() {
		t.Errorf("полнота = %+v", doc.Coverage)
	}
	if len(doc.Sheets) != 2 {
		t.Fatalf("листов %d, хотели 2", len(doc.Sheets))
	}
	head := doc.Sheets[0]
	if len(head.Merges) != 1 || head.Merges[0] != "A1:C1" {
		t.Errorf("слияния = %v, хотели [A1:C1]", head.Merges)
	}
	data := doc.Sheets[1]
	if !data.Hidden {
		t.Error("лист data должен быть помечен скрытым")
	}
	if _, ok := findCell(doc, "data", "A2"); !ok {
		t.Error("скрытый лист обязан читаться полностью")
	}
	joined := strings.Join(doc.Notes, "|")
	if !strings.Contains(joined, "лист «data» скрыт") {
		t.Errorf("в заметках нет предупреждения о скрытом листе: %v", doc.Notes)
	}
}

func TestParseXlsxEmptyWorkbook(t *testing.T) {
	path := writeWorkbook(t, "пусто.xlsx", nil)

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if !doc.Complete() {
		t.Errorf("пустая книга не «с потерями»: %+v", doc.Coverage)
	}
	if doc.Rows != 0 || doc.Cells != 0 {
		t.Errorf("строк %d, ячеек %d, хотели 0 и 0", doc.Rows, doc.Cells)
	}
	if len(doc.Notes) == 0 || !strings.Contains(doc.Notes[0], "пуст") {
		t.Errorf("хотели заметку о пустом листе, получили %v", doc.Notes)
	}
}

func TestParseXlsxZerosAndWraps(t *testing.T) {
	path := writeWorkbook(t, "нули.xlsx", func(f *excelize.File) {
		setCells(f, "Sheet1", map[string]string{"A1": "0", "B1": "строка1\nстрока2"})
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if !doc.Complete() {
		t.Errorf("полнота = %+v", doc.Coverage)
	}
	zero, ok := findCell(doc, "Sheet1", "A1")
	if !ok || zero.Value != "0" {
		t.Errorf("нулевое значение должно оставаться содержательным, получили %+v (ok=%v)", zero, ok)
	}
	wrapped, ok := findCell(doc, "Sheet1", "B1")
	if !ok || !strings.Contains(wrapped.Value, "строка1\nстрока2") {
		t.Errorf("перевод строки в ячейке потерян: %+v", wrapped)
	}
}

func TestParseXlsxErrors(t *testing.T) {
	badZip := filepath.Join(t.TempDir(), "не-книга.xlsx")
	if err := os.WriteFile(badZip, []byte("это не zip и не xlsx вовсе"), 0o600); err != nil {
		t.Fatalf("запись фикстуры: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"файла нет", filepath.Join(t.TempDir(), "нет.xlsx"), "открытие xlsx-файла"},
		{"не xlsx", badZip, "excelize не открывает книгу"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseXlsx(tc.path)
			if err == nil {
				t.Fatalf("хотели ошибку, получили документ %+v", doc)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ошибка %q, хотели подстроку %q", err, tc.wantErr)
			}
		})
	}
}

// Тесты чистой логики сверки — без файлов: именно они сторожат
// «ложные потери» и «пропущенные клетки».

func TestCellCovered(t *testing.T) {
	grid := [][]string{
		{"A1", "", "C1"},
		{"", "B2"},
	}
	formulas := map[xlsxCoord]string{{row: 2, col: 2}: "SUM(A1:A2)"}

	tests := []struct {
		name string
		c    srcCell
		want bool
	}{
		{"значение на месте", srcCell{row: 1, col: 1, requireValue: true}, true},
		{"пустая клетка без требований", srcCell{row: 1, col: 2}, true},
		{"пустая клетка с требованиями — потеря", srcCell{row: 2, col: 1, requireValue: true}, false},
		{"формула дошла", srcCell{row: 2, col: 2, hasFormula: true, requireValue: true}, true},
		{"за границей строк, требовательная — потеря", srcCell{row: 3, col: 1, requireValue: true}, false},
		{"за границей колонок, требовательная — потеря", srcCell{row: 1, col: 5, requireValue: true}, false},
		{"за границей, пустая по исходнику — не потеря", srcCell{row: 9, col: 9}, true},
		{"нулевой адрес — потеря", srcCell{row: 0, col: 0, requireValue: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellCovered(grid, formulas, tc.c); got != tc.want {
				t.Errorf("cellCovered(%+v) = %v, хотели %v", tc.c, got, tc.want)
			}
		})
	}
}

func TestCellRequires(t *testing.T) {
	shared := []string{"позиция", "", "  "}
	tests := []struct {
		name string
		c    srcCell
		v    string
		inl  string
		want bool
	}{
		{"число", srcCell{}, "12.5", "", true},
		{"нечисловая строка по словарю", srcCell{kind: "s"}, "0", "", true},
		{"пустая строка по словарю", srcCell{kind: "s"}, "1", "", false},
		{"пробелы — содержимое", srcCell{kind: "s"}, "2", "", true},
		{"индекс вне словаря", srcCell{kind: "s"}, "42", "", true},
		{"битый индекс", srcCell{kind: "s"}, "не-число", "", true},
		{"inline пусто", srcCell{kind: "inlineStr"}, "", "", false},
		{"inline непусто", srcCell{kind: "inlineStr"}, "", "текст", true},
		{"формула обязана дойти", srcCell{hasFormula: true}, "", "", true},
		{"ложь-флаг числа", srcCell{kind: "b"}, "0", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellRequires(tc.c, tc.v, tc.inl, shared); got != tc.want {
				t.Errorf("cellRequires = %v, хотели %v", got, tc.want)
			}
		})
	}
}
