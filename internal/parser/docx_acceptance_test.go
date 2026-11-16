package parser

import (
	"strings"
	"testing"
)

// Тест проверяет критерий P1.2 («текст и таблицы извлечены без потерь») на
// синтетическом документе, повторяющем структуру ЗД ЭФ: титульные абзацы,
// разделы с инструкциями по заполнению форм, таблица-форма со слияниями,
// спецификация вложенной таблицей, штамп в текстовом блоке, поля, правки и
// элементы управления содержимым.
//
// Реальный файл docs/НЭО поставка RedCheck/Тендерная документация/
// 226717 ЗД ЭФ (К, КП, ЗП).docx в репозитории отсутствует (его содержимое —
// предмет задачи P0), поэтому проверяется полнота извлечения как свойство
// разбора: прогон на файле заказчика выполняется тем же кодом.

// zdEfFragments — содержимое, которое обязано дойти до модели.
var zdEfFragments = []string{
	"226717-ЭФ/Д",
	"Закупочная документация",
	"Поставка программно-аппаратного комплекса RedCheck",
	"Начальная (максимальная) цена договора: 4 950 000,00 руб.",
	"1. Общие положения",
	"Закупка проводится на электронной площадке в соответствии с Положением о закупке.",
	"2. Инструкция по заполнению формы 2 «Заявка»",
	"Участник закупки обязан приложить выписку ЕГРЮЛ, полученную не ранее чем за 30 дней.",
	"Сведения об участнике приводятся по данным на дату подачи заявки.",
	"Форма 2. Заявка на участие",
	"Адрес участника",
	"Почтовый",
	"191023, г. Санкт-Петербург",
	"Служебная пометка скрытой ячейки",
	"191023, г. Санкт-Петербург, ул. Глинки, д. 3, лит. А",
	"Реквизиты",
	"ИНН 7801501111 КПП 780101001",
	"ОГРН 1027802769630",
	"+7 (812) 320-00-00",
	"Спецификация",
	"№ п/п",
	"Наименование товара",
	"Количество",
	"RedCheck WAF Appliances 3200",
	"12 шт",
	"Оттиск печати участника",
	"приложение 1 к закупочной документации",
	"17",
	"3. Требования к участнику",
	"Лицензия ФСТЭК России на деятельность по технической защите конфиденциальной информации.",
	"Обеспечение заявки — 1 % от НМЦД",
	"Срок действия предложения — 90 дней",
	"редакция от 19.10.2026",
	"Конец документа",
}

// zdEfAbsent — что в извлечённый текст попадать не должно.
var zdEfAbsent = []string{
	"PAGE",      // команда поля, на страницу не печатается
	"устарело",  // текст, удалённый правкой с отслеживанием
	"Нажмите",   // псевдоним элемента управления содержимым
	"zakupka",   // XPath привязки данных из w:sdtPr
	"Heading1",  // имя стиля, а не содержимое
	"TableGrid", // имя стиля таблицы
	"_GoBack",   // имя закладки
	"val=",      // имена атрибутов
	"Печать",    // дубль из mc:Fallback, когда взят mc:Choice
}

// zdEfParagraph, zdEfHeading, zdEfCell — сборка разметки тестового документа.
func zdEfParagraph(text string) string {
	return `<w:p><w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

func zdEfHeading(style, text string) string {
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func zdEfCell(text string, props string) string {
	if text == "" {
		return `<w:tc>` + props + `<w:p/></w:tc>`
	}
	return `<w:tc>` + props + zdEfParagraph(text) + `</w:tc>`
}

func zdEfDocument() string {
	var sb strings.Builder

	sb.WriteString(zdEfHeading("Title", "226717-ЭФ/Д"))
	sb.WriteString(zdEfParagraph("Закупочная документация"))
	sb.WriteString(zdEfParagraph("Поставка программно-аппаратного комплекса RedCheck"))
	sb.WriteString(zdEfParagraph("Начальная (максимальная) цена договора: 4 950 000,00 руб."))
	sb.WriteString(zdEfHeading("Heading1", "1. Общие положения"))
	sb.WriteString(zdEfParagraph("Закупка проводится на электронной площадке в соответствии с Положением о закупке."))
	sb.WriteString(zdEfHeading("Heading1", "2. Инструкция по заполнению формы 2 «Заявка»"))
	sb.WriteString(zdEfParagraph("Участник закупки обязан приложить выписку ЕГРЮЛ, полученную не ранее чем за 30 дней."))
	sb.WriteString(zdEfParagraph("Сведения об участнике приводятся по данным на дату подачи заявки."))

	// Форма со слияниями: заголовок на всю сетку, вертикальное слияние в первом
	// столбце, ячейка адреса на две колонки.
	sb.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/></w:tblPr>` +
		`<w:tblGrid><w:gridCol w:w="2000"/><w:gridCol w:w="2000"/><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid>`)
	sb.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>` +
		zdEfCell("Форма 2. Заявка на участие", `<w:tcPr><w:gridSpan w:val="4"/></w:tcPr>`) + `</w:tr>`)
	sb.WriteString(`<w:tr>` +
		zdEfCell("Адрес участника", `<w:tcPr><w:vMerge w:val="restart"/></w:tcPr>`) +
		zdEfCell("Почтовый", "") +
		zdEfCell("191023, г. Санкт-Петербург", `<w:tcPr><w:gridSpan w:val="2"/></w:tcPr>`) +
		`</w:tr>`)
	// Word хранит в закрытой слиянием ячейке текст, который на странице не виден.
	sb.WriteString(`<w:tr>` +
		zdEfCell("Служебная пометка скрытой ячейки", `<w:tcPr><w:vMerge w:val="continue"/></w:tcPr>`) +
		zdEfCell("Юридический", "") +
		zdEfCell("191023, г. Санкт-Петербург, ул. Глинки, д. 3, лит. А", `<w:tcPr><w:gridSpan w:val="2"/></w:tcPr>`) +
		`</w:tr>`)
	// Строка со вложенной таблицей — спецификацией.
	sb.WriteString(`<w:tr>` +
		zdEfCell("Спецификация", "") +
		`<w:tc><w:p><w:r><w:t xml:space="preserve">Спецификация</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:trPr><w:tblHeader/></w:trPr>` +
		zdEfCell("№ п/п", "") + zdEfCell("Наименование товара", "") + zdEfCell("Количество", "") +
		`</w:tr><w:tr>` +
		zdEfCell("1", "") + zdEfCell("RedCheck WAF Appliances 3200", "") + zdEfCell("12 шт", "") +
		`</w:tr></w:tbl></w:tc>` +
		zdEfCell("Реквизиты", "") +
		zdEfCell("ИНН 7801501111 КПП 780101001", "") +
		`</w:tr>`)
	sb.WriteString(`<w:tr>` +
		zdEfCell("Телефон", "") +
		zdEfCell("+7 (812) 320-00-00", "") +
		zdEfCell("ОГРН 1027802769630", "") +
		zdEfCell("", "") +
		`</w:tr>`)
	sb.WriteString(`</w:tbl>`)

	// Штамп в текстовом блоке: Word кладёт его и в mc:Choice, и в mc:Fallback.
	sb.WriteString(`<w:p><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:inline>` +
		`<a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
		zdEfParagraph("Оттиск печати участника") +
		`</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing>` +
		`</mc:Choice><mc:Fallback><w:pict><v:shape><v:textbox><w:txbxContent>` +
		zdEfParagraph("Печать") +
		`</w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback></mc:AlternateContent></w:p>`)

	sb.WriteString(`<w:p><w:bookmarkStart w:id="0" w:name="_GoBack"/>` +
		`<w:r><w:t xml:space="preserve">Подробности в </w:t></w:r>` +
		`<w:hyperlink r:id="rId2"><w:r><w:t>приложение 1 к закупочной документации</w:t></w:r></w:hyperlink>` +
		`<w:r><w:fldChar w:fldCharType="begin"/></w:r>` +
		`<w:r><w:instrText xml:space="preserve"> PAGE </w:instrText></w:r>` +
		`<w:r><w:fldChar w:fldCharType="separate"/></w:r>` +
		`<w:r><w:t>17</w:t></w:r>` +
		`<w:r><w:fldChar w:fldCharType="end"/></w:r>` +
		`<w:bookmarkEnd w:id="0"/></w:p>`)

	sb.WriteString(zdEfHeading("Heading1", "3. Требования к участнику"))
	sb.WriteString(zdEfParagraph("Лицензия ФСТЭК России на деятельность по технической защите конфиденциальной информации."))
	// Элемент управления содержимым: псевдоним и привязка — не текст документа.
	sb.WriteString(`<w:sdt><w:sdtPr><w:alias w:val="Нажмите, чтобы ввести значение"/>` +
		`<w:dataBinding w:xpath="/zakupka[1]"/></w:sdtPr><w:sdtContent>` +
		zdEfParagraph("Обеспечение заявки — 1 % от НМЦД") +
		`</w:sdtContent></w:sdt>`)
	sb.WriteString(`<w:p><w:r><w:t xml:space="preserve">Срок действия предложения — 90 дней</w:t></w:r>` +
		`<w:del w:id="7"><w:r><w:delText xml:space="preserve">устарело</w:delText></w:r></w:del>` +
		`<w:ins w:id="8"><w:r><w:t xml:space="preserve">, редакция от 19.10.2026</w:t></w:r></w:ins></w:p>`)
	sb.WriteString(zdEfParagraph("Конец документа"))
	sb.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgNumType w:fmt="decimal"/></w:sectPr>`)

	return sb.String()
}

func TestParseDocxZdEfLike(t *testing.T) {
	doc := parseBody(t, zdEfDocument())

	blob := doc.extractedBlob()
	for _, fragment := range zdEfFragments {
		if !strings.Contains(blob, fragment) {
			t.Errorf("фрагмент не дошёл до модели: %q", fragment)
		}
	}

	text := doc.Text()
	for _, fragment := range zdEfAbsent {
		if strings.Contains(text, fragment) {
			t.Errorf("в выводе не должно быть %q", fragment)
		}
	}
	// Текст, закрытый вертикальным слиянием: в модели он сохранён, в вывод не входит.
	if strings.Contains(text, "Служебная пометка") {
		t.Errorf("невидимый текст слияния попал в вывод:\n%s", text)
	}

	cov := doc.Coverage
	if !cov.Complete || cov.MissingPieces != 0 {
		t.Errorf("извлечение с потерями: %+v (образцы: %q)", cov, cov.Missing)
	}
	if cov.OutOfOrder != 0 {
		t.Errorf("фрагментов вне документного порядка: %d", cov.OutOfOrder)
	}
	if doc.Tables != cov.SourceTables || doc.Rows != cov.SourceRows || doc.Cells != cov.SourceCells {
		t.Errorf("структура расходится: модель %d/%d/%d, исходник %d/%d/%d",
			doc.Tables, doc.Rows, doc.Cells, cov.SourceTables, cov.SourceRows, cov.SourceCells)
	}
	if cov.SourcePieces < len(zdEfFragments) {
		t.Errorf("эталонных фрагментов %d, а в исходнике только %d", len(zdEfFragments), cov.SourcePieces)
	}
	if cov.SourceRunes == 0 || doc.Runes < cov.SourceRunes {
		t.Errorf("объём текста: модель %d, исходник %d", doc.Runes, cov.SourceRunes)
	}
	if len(doc.Notes) != 0 {
		t.Errorf("разбор дал неожиданные замечания: %v", doc.Notes)
	}
}

func TestParseDocxZdEfStructure(t *testing.T) {
	doc := parseBody(t, zdEfDocument())

	// Нумерация блоков непрерывна и идёт в документном порядке: на неё опирается
	// трассировка замечаний «файл + параграф» (P1.6).
	topTables := 0
	for i, b := range doc.Blocks {
		if b.Index != i+1 {
			t.Fatalf("Blocks[%d].Index = %d, хотели %d", i, b.Index, i+1)
		}
		if b.Table != nil {
			topTables++
		}
	}
	if doc.Paragraphs != len(doc.Blocks)-topTables {
		t.Errorf("Paragraphs = %d, блоков %d, таблиц верхнего уровня %d", doc.Paragraphs, len(doc.Blocks), topTables)
	}
	if doc.Tables != topTables+1 {
		t.Errorf("Tables = %d, хотели %d (вместе со вложенной спецификацией)", doc.Tables, topTables+1)
	}

	form := firstTopLevelTable(doc)
	if form == nil {
		t.Fatalf("в документе нет таблицы верхнего уровня: %+v", doc.Blocks)
	}
	if form.Depth != 0 {
		t.Errorf("Depth = %d, хотели 0", form.Depth)
	}
	if form.Columns != 4 || len(form.Rows) != 5 {
		t.Fatalf("форма: Columns/Rows = %d/%d, хотели 4/5", form.Columns, len(form.Rows))
	}
	if form.Style != "TableGrid" {
		t.Errorf("Style = %q, хотели TableGrid", form.Style)
	}
	if !form.Rows[0].Header {
		t.Error("первая строка формы не помечена заголовком")
	}
	if got := form.Rows[0].Cells[0].ColSpan; got != 4 {
		t.Errorf("шапка формы на %d колонки, хотели 4", got)
	}
	if got := form.Rows[1].Cells[0].RowSpan; got != 2 {
		t.Errorf("вертикальное слияние занимает %d строки, хотели 2", got)
	}
	if got := form.Rows[2].Index; got != 3 {
		t.Errorf("Index третьей строки = %d, хотели 3", got)
	}
	hidden := form.Rows[2].Cells[0]
	if !hidden.Merged || hidden.Text != "Служебная пометка скрытой ячейки" {
		t.Errorf("ячейка продолжения слияния = %+v, хотели сохранённый текст и флаг Merged", hidden)
	}

	spec := form.Rows[3].Cells[1].Tables
	if len(spec) != 1 {
		t.Fatalf("вложенных таблиц %d, хотели 1 (спецификация)", len(spec))
	}
	if len(spec[0].Rows) != 2 || spec[0].Columns != 3 || spec[0].Depth != 1 {
		t.Errorf("спецификация: %+v", spec[0])
	}
	if !spec[0].Rows[0].Header {
		t.Error("шапка спецификации не помечена заголовком")
	}
	if got := spec[0].Rows[1].Cells[1].Text; got != "RedCheck WAF Appliances 3200" {
		t.Errorf("позиция спецификации = %q", got)
	}
	if strings.Contains(form.Rows[3].Cells[1].Text, "RedCheck") {
		t.Error("вложенная таблица продублировалась в тексте ячейки")
	}
}

func TestParseDocxZdEfBlockAnchors(t *testing.T) {
	doc := parseBody(t, zdEfDocument())

	// содержимое обязано оставаться привязанным к номеру блока — это вход для
	// извлечения реквизитов (P1.5) и трассировки замечаний (P1.6).
	wanted := []string{
		"ИНН 7801501111 КПП 780101001",
		"Обеспечение заявки — 1 % от НМЦД",
		"Участник закупки обязан приложить выписку ЕГРЮЛ",
		"RedCheck WAF Appliances 3200",
	}
	for _, fragment := range wanted {
		block := 0
		for _, b := range doc.Blocks {
			text := b.Text
			if b.Table != nil {
				text = renderTable(*b.Table, false, true)
			}
			if strings.Contains(text, fragment) {
				block = b.Index
				break
			}
		}
		if block == 0 {
			t.Errorf("фрагмент %q не привязан ни к одному блоку", fragment)
			continue
		}
		if !strings.Contains(doc.Text(), fragment) {
			t.Errorf("фрагмент %q есть в блоке %d, но не в выводимом тексте", fragment, block)
		}
	}
}

// firstTopLevelTable отдаёт первую таблицу верхнего уровня: геометрию удобнее
// проверять по ней, чем по номеру блока, который зависит от набора абзацев.
func firstTopLevelTable(doc *Document) *Table {
	for _, b := range doc.Blocks {
		if b.Table != nil {
			return b.Table
		}
	}
	return nil
}
