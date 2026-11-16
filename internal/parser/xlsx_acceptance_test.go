package parser

import (
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Тест проверяет критерий P1.3 («все строки спецификаций прочитаны») на
// синтетической книге, повторяющей структуру спецификаций НЭО: широкий
// лист «Шаблон» с объединёнными шапками, строками товаров и формулами по
// каждой позиции, плюс скрытый лист «data» со справочником ОКПД2 на 256 строк.
//
// Реальные файлы 226717_671.26.00374_ТЕХ.xlsx, ..._КОММ.xlsx,
// «Коды ОКПД2 по позициям.xlsx», 15/16 из заявки в репозитории отсутствуют
// (их содержимое — предмет задачи P0); прогон на них выполняется тем же
// кодом и подтверждён вручную (см. PR): 5 книг, 0 потерь, код выхода 0.

func TestParseXlsxSpecificationAcceptance(t *testing.T) {
	const dataRows = 256

	path := writeWorkbook(t, "спецификация.xlsx", func(f *excelize.File) {
		const sh = "Sheet1"
		setCells(f, sh, map[string]string{
			"A1": "Спецификация товара (работ, услуг)",
			"A2": "226717-ЭФ/Д", "C2": "671.26.00374",
			"A4": "№ п/п", "B4": "Наименование", "E4": "Кол-во",
			"F4": "Цена, руб.", "G4": "Стоимость, руб.", "H4": "Код ОКПД2",
		})
		if err := f.MergeCell(sh, "A1", "H1"); err != nil {
			panic(err)
		}
		if err := f.MergeCell(sh, "A2", "B2"); err != nil {
			panic(err)
		}
		for i := 1; i <= 40; i++ {
			row := 4 + i
			setCells(f, sh, map[string]string{
				"A" + strconv.Itoa(row): strconv.Itoa(i),
				"B" + strconv.Itoa(row): "Позиция " + strconv.Itoa(i) + " — поставка и шеф-монтаж",
				"E" + strconv.Itoa(row): strconv.Itoa(i%7 + 1),
				"F" + strconv.Itoa(row): strconv.Itoa(10000 + i*137),
				"H" + strconv.Itoa(row): "26.20.40.1" + strconv.Itoa(10+i),
			})
			ref := "G" + strconv.Itoa(row)
			if err := f.SetCellFormula(sh, ref, "E"+strconv.Itoa(row)+"*F"+strconv.Itoa(row)); err != nil {
				panic(err)
			}
		}
		setCells(f, sh, map[string]string{"B46": "Итого"})
		if err := f.SetCellFormula(sh, "G46", "SUM(G5:G44)"); err != nil {
			panic(err)
		}

		if _, err := f.NewSheet("data"); err != nil {
			panic(err)
		}
		setCells(f, "data", map[string]string{"A1": "Код", "B1": "Наименование", "C1": "Валюта"})
		for i := 2; i <= dataRows; i++ {
			setCells(f, "data", map[string]string{
				"A" + strconv.Itoa(i): "26.20." + strconv.Itoa(10+i) + ".140",
				"B" + strconv.Itoa(i): "Узел расчёта №" + strconv.Itoa(i),
				"C" + strconv.Itoa(i): "RUB",
			})
		}
		if err := f.SetSheetVisible("data", false); err != nil {
			panic(err)
		}
	})

	doc, err := ParseXlsx(path)
	if err != nil {
		t.Fatalf("ParseXlsx: %v", err)
	}
	if !doc.Complete() {
		t.Fatalf("спецификация прочитана с потерями: %+v", doc.Coverage)
	}
	if len(doc.Sheets) != 2 {
		t.Fatalf("листов %d, хотели 2", len(doc.Sheets))
	}

	head, data := doc.Sheets[0], doc.Sheets[1]
	if head.Name != "Sheet1" || !data.Hidden {
		t.Errorf("листы = %q (hidden=%v), хотели Sheet1 и скрытый data", head.Name, data.Hidden)
	}

	// Каждая строка спецификации дошла: заголовок, 40 позиций, итог.
	for _, ref := range []string{"A1", "B4", "B5", "G5", "B44", "B46", "G46"} {
		if _, ok := findCell(doc, "Sheet1", ref); !ok {
			t.Errorf("ячейка %q не дошла до модели", ref)
		}
	}
	if got := head.Formulas; got != 41 {
		t.Errorf("формул в «Шаблон» %d, хотели 41 (40 строк + итог)", got)
	}
	if len(head.Merges) != 2 {
		t.Errorf("слияний %d, хотели 2: %v", len(head.Merges), head.Merges)
	}

	// Справочник ОКПД2: все 256 строк скрытого листа в модели.
	if len(data.Rows) != dataRows {
		t.Errorf("строк data в модели %d, хотели %d", len(data.Rows), dataRows)
	}
	last, ok := findCell(doc, "data", "B"+strconv.Itoa(dataRows))
	if !ok || !strings.Contains(last.Value, "Узел расчёта №256") {
		t.Errorf("последняя строка справочника не дошла: %+v ok=%v", last, ok)
	}
	if data.Cells != dataRows*3 {
		t.Errorf("ячеек data %d, хотели %d", data.Cells, dataRows*3)
	}

	// Текстовый вывод: обе таблицы и шапка на месте.
	text := doc.Text()
	for _, want := range []string{"Лист: Sheet1", "Лист: data", "Спецификация товара", "Итого"} {
		if !strings.Contains(text, want) {
			t.Errorf("в Text нет %q", want)
		}
	}
}
