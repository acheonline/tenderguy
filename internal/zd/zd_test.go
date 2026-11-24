package zd

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/rules"
)

// Синтетическая фикстура повторяет структуру реальной закупочной документации:
// оглавление из нумерованных разделов, заголовки разделов в теле без
// номеров, таблица «Структура заявки» с подкаталогом, обязательным и
// условным пунктами, раздел с образцами форм (маркеры «начало формы» /
// «конец формы», блоки «Инструкции по заполнению», одна форма без
// инструкции) и раздел требований к участнику. Документ заказчика в
// репозиторий не попадает — фикстура собирается в тесте через archive/zip
// (образец — internal/parser/docx_test.go).

const fxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`</Types>`

const fxRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`</Relationships>`

const fxDocRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`</Relationships>`

// para — абзац; табуляции в тексте становятся w:tab, как в строках оглавления.
func para(text string) string {
	var sb strings.Builder
	sb.WriteString(`<w:p>`)
	for i, part := range strings.Split(text, "\t") {
		if i > 0 {
			sb.WriteString(`<w:r><w:tab/></w:r>`)
		}
		if part != "" {
			sb.WriteString(`<w:r><w:t xml:space="preserve">` + part + `</w:t></w:r>`)
		}
	}
	sb.WriteString(`</w:p>`)
	return sb.String()
}

// pageBreakPara — абзац, начинающийся с отметки w:lastRenderedPageBreak:
// единственный способ задать пагинацию в синтетическом .docx.
func pageBreakPara(text string) string {
	return `<w:p><w:r><w:lastRenderedPageBreak/><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

// tbl — таблица: строки одинаковой структуры, ячейка может содержать
// несколько абзацев (разделитель — перевод строки).
func tbl(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(`<w:tbl><w:tblGrid>`)
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	for i := 0; i < cols; i++ {
		sb.WriteString(`<w:gridCol/>`)
	}
	sb.WriteString(`</w:tblGrid>`)
	for _, r := range rows {
		sb.WriteString(`<w:tr>`)
		for _, cell := range r {
			sb.WriteString(`<w:tc>`)
			lines := strings.Split(cell, "\n")
			for _, line := range lines {
				sb.WriteString(para(line))
			}
			sb.WriteString(`</w:tc>`)
		}
		sb.WriteString(`</w:tr>`)
	}
	sb.WriteString(`</w:tbl>`)
	return sb.String()
}

// writeFixtureDocx собирает минимальный пакет OOXML с переданным телом.
func writeFixtureDocx(t *testing.T, name, body string) string {
	t.Helper()

	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` + body + `</w:body></w:document>`
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("создание %s: %v", path, err)
	}
	w := zip.NewWriter(f)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", fxContentTypes},
		{"_rels/.rels", fxRootRels},
		{"word/_rels/document.xml.rels", fxDocRels},
		{"word/document.xml", document},
	}
	for _, part := range parts {
		fw, err := w.Create(part.name)
		if err != nil {
			f.Close()
			t.Fatalf("zip.Create(%s): %v", part.name, err)
		}
		if _, err := fw.Write([]byte(part.body)); err != nil {
			f.Close()
			t.Fatalf("запись %s: %v", part.name, err)
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

// Части основной фикстуры: тесты худших сценариев собирают из них варианты.
var fxTOC = []string{
	para("1.\tТЕРМИНЫ И ОПРЕДЕЛЕНИЯ\t2"),
	para("2.\tТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ\t3"),
	para("3.\tТРЕБОВАНИЯ К ЗАЯВКЕ НА УЧАСТИЕ В ЗАКУПКЕ\t4"),
	para("4.\tОбразцы основных форм документов, включаемых в заявку на участие в закупке\t5"),
}

var fxTerms = []string{
	para("ТЕРМИНЫ И ОПРЕДЕЛЕНИЯ"),
	para("Термин — слово, используемое в закупочной документации."),
	para("Работа ведётся в соответствии с Инструкциями и регламентом электронной площадки."),
}

var fxReqs = []string{
	para("ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ"),
	para("Участник закупки должен иметь лицензию на осуществление видов деятельности."),
	para("Участник не должен иметь задолженность по налогам и сборам."),
	para("Участник не находится в процессе ликвидации или банкротства."),
	para("Произвольный абзац без ключевых слов."),
}

var fxComposition = []string{
	para("ТРЕБОВАНИЯ К ЗАЯВКЕ НА УЧАСТИЕ В ЗАКУПКЕ"),
	para("Структура заявки приведена в таблице ниже."),
	tbl([][]string{
		{
			"№ документа в томе",
			"Наименование документа/ссылка на пункт Закупочной документации",
			"Наименование файла в заявке",
			"Требования к формату и расширению файла",
		},
		{`Подкаталог «Основные документы»`, ""},
		{
			"1",
			"Опись заявки. Типовая форма и инструкция по заполнению приведены в Разделе 4 «Образцы основных форм документов» настоящей Закупочной документации.",
			"«Опись»",
			"Doc, Pdf",
		},
		{
			"2",
			"Анкета участника (предоставляется Участником закупки только в случае наличия соответствующего требования в Разделе 6 «Техническая часть»). Типовая форма и инструкция по заполнению приведены в Разделе 4.",
			"«Анкета»",
			"Xlsx",
		},
	}),
}

var fxForms = []string{
	para("Образцы основных форм документов, включаемых в заявку на участие в закупке"),
	para("Опись заявки (форма 1)"),
	para("начало формы"),
	para("Таблица описи документов."),
	para("конец формы"),
	para("Инструкции по заполнению"),
	para("1. Заполните строки описи."),
	para("2. Подпишите опись."),
	para("Анкета участника (форма 2)"),
	para("начало формы"),
	para("Текст анкеты."),
	para("конец формы"),
	para("Инструкции по заполнению"),
	para("Заполните анкету полностью."),
	para("Согласие со сметой (форма 3)"),
	para("начало формы"),
	para("Текст согласия."),
	para("конец формы"),
}

// fixtureBody — основная фикстура: все части в документном порядке.
func fixtureBody() string {
	var sb strings.Builder
	for _, part := range [][]string{fxTOC, fxTerms, fxReqs, fxComposition, fxForms} {
		for _, p := range part {
			sb.WriteString(p)
		}
	}
	return sb.String()
}

// fxFormsGroup — раздел образцов форм новой модели: группа «форма 2» с двумя
// телами и ОДНИМ блоком инструкций на оба (shared), заголовок инструкций с
// суффиксом-названием формы (found + instruction_title) и форма с пустым
// телом и примечанием-ссылкой на внешнюю инструкцию (external + empty_body).
var fxFormsGroup = []string{
	para("Образцы основных форм документов, включаемых в заявку на участие в закупке"),
	para("Техническое предложение (форма 2)"),
	para("Форма технического предложения на поставку товара"),
	para("начало формы"),
	para("Текст предложения товара."),
	para("конец формы"),
	para("Форма технического предложения на поставку эквивалентного товара"),
	para("начало формы"),
	para("Текст предложения эквивалента."),
	para("конец формы"),
	para("Инструкции по заполнению"),
	para("Заполняется в случае поставки товаров."),
	para("В предложении эквивалентного товара описываются все позиции."),
	para("Письмо-согласие с проектом договора (форма 3)"),
	para("Форма письма-согласия с проектом договора"),
	para("начало формы"),
	para("Текст письма-согласия."),
	para("конец формы"),
	para("Инструкции по заполнению Письма-согласия с проектом договора"),
	para("Участник руководствуется опубликованным проектом договора."),
	para("Спецификация (Коммерческое предложение) (форма 4)"),
	para("[заполняется с указанием названия лота, в соответствии с «Инструкцией по заполнению» размещенной в указанном файле формата XLSX]"),
	para("начало формы"),
	para("конец формы"),
}

// joinParts — тело документа из перечисленных частей.
func joinParts(parts ...[]string) string {
	var sb strings.Builder
	for _, part := range parts {
		for _, p := range part {
			sb.WriteString(p)
		}
	}
	return sb.String()
}

func TestExtractFileFullFixture(t *testing.T) {
	path := writeFixtureDocx(t, "ЗД ЭФ тестовая.docx", fixtureBody())
	res, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	// Привязка к версии документа.
	if res.File.SHA256 == "" || len(res.File.SHA256) != 64 {
		t.Errorf("sha256 не заполнен: %q", res.File.SHA256)
	}
	if res.File.Size <= 0 || res.File.Blocks == 0 || res.File.Runes == 0 {
		t.Errorf("счётчики версии пусты: %+v", res.File)
	}

	// Пагинация: отметок разбивки нет, оглавление обещает страницу 5 —
	// вердикт «недостоверна» с конкретной причиной, Page в адреса не попал.
	if res.Pagination.Trusted {
		t.Errorf("пагинация признана достоверной вопреки оглавлению: %+v", res.Pagination)
	}
	if !strings.Contains(res.Pagination.Reason, "оглавление") {
		t.Errorf("причина недоверия не названа: %q", res.Pagination.Reason)
	}
	if res.Pagination.TocMaxPage != 5 {
		t.Errorf("toc_max_page = %d, хочу 5", res.Pagination.TocMaxPage)
	}

	// Разделы по оглавлению: все 4, границы — абзацы заголовков.
	if len(res.Sections) != 4 {
		t.Fatalf("разделов %d, хочу 4: %+v", len(res.Sections), res.Sections)
	}
	if res.Sections[1].Label != "Раздел 2 «ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ»" {
		t.Errorf("метка раздела 2 = %q", res.Sections[1].Label)
	}
	if res.Sections[0].EndPara != res.Sections[1].StartPara {
		t.Errorf("границы разделов не стыкуются: %+v", res.Sections[:2])
	}

	// Состав: два пункта, оба из подкаталога «Основные документы».
	if res.Composition.Status != StatusFull {
		t.Fatalf("состав: статус %s, причина %q", res.Composition.Status, res.Composition.Reason)
	}
	if len(res.Composition.Items) != 2 {
		t.Fatalf("пунктов состава %d, хочу 2: %+v", len(res.Composition.Items), res.Composition.Items)
	}
	first := res.Composition.Items[0]
	if first.ID != "zd-03" {
		t.Errorf("id первого пункта = %q, хочу zd-03 (номер строки таблицы)", first.ID)
	}
	if first.Title != "Опись заявки." {
		t.Errorf("title = %q — ссылка на раздел с формами обязана быть вырезана", first.Title)
	}
	if first.Filename != "Опись" || first.Pattern != "*Опись*" {
		t.Errorf("filename/pattern = %q/%q", first.Filename, first.Pattern)
	}
	if strings.Join(first.Formats, ",") != "Doc,Pdf" {
		t.Errorf("formats = %v", first.Formats)
	}
	if !first.Required || first.Condition != "" {
		t.Errorf("обязательный пункт помечен условным: required=%v condition=%q", first.Required, first.Condition)
	}
	if first.Group != "Основные документы" {
		t.Errorf("group = %q, хочу «Основные документы»", first.Group)
	}
	second := res.Composition.Items[1]
	if second.Required {
		t.Errorf("условный пункт остался обязательным: %+v", second)
	}
	if !strings.Contains(second.Condition, "только в случае") {
		t.Errorf("condition не сохранён дословно: %q", second.Condition)
	}
	if second.Title != "Анкета участника." {
		t.Errorf("title условного пункта = %q", second.Title)
	}
	for _, it := range res.Composition.Items {
		if !it.Ref.Traced() {
			t.Errorf("пункт %s: адрес не трассируется: %+v", it.ID, it.Ref)
		}
		if it.Ref.Page != 0 {
			t.Errorf("пункт %s: Page попал в адрес при недоверии к пагинации: %+v", it.ID, it.Ref)
		}
		if it.Ref.Item != "Раздел 3 «ТРЕБОВАНИЯ К ЗАЯВКЕ НА УЧАСТИЕ В ЗАКУПКЕ»" {
			t.Errorf("пункт %s: Item = %q", it.ID, it.Ref.Item)
		}
		if it.Ref.Cell == "" {
			t.Errorf("пункт %s: адрес ячейки пуст", it.ID)
		}
	}

	// Формы: три пары, третья без инструкции — штатный случай, видимый.
	if res.Forms.Status != StatusFull {
		t.Errorf("формы: статус %s, причина %q", res.Forms.Status, res.Forms.Reason)
	}
	if res.Forms.Section == "" {
		t.Errorf("раздел образцов форм не определён")
	}
	if len(res.Forms.Forms) != 3 {
		t.Fatalf("форм %d, хочу 3", len(res.Forms.Forms))
	}
	wantInstr := []string{InstructionFound, InstructionFound, InstructionNotFound}
	for i, f := range res.Forms.Forms {
		if f.GroupNumber != string(rune('1'+i)) {
			t.Errorf("форма %d: номер группы %q", i, f.GroupNumber)
		}
		if f.Variant != 1 {
			t.Errorf("форма %d: вариант %d, хочу 1", i, f.Variant)
		}
		if !f.Closed {
			t.Errorf("форма %d: нет маркера «конец формы»", i)
		}
		if f.InstructionStatus != wantInstr[i] {
			t.Errorf("форма %d: instruction_status = %s, хочу %s", i, f.InstructionStatus, wantInstr[i])
		}
		if !f.Ref.Traced() {
			t.Errorf("форма %d: адрес не трассируется: %+v", i, f.Ref)
		}
	}
	if len(res.Forms.Forms[0].InstructionItems) != 2 {
		t.Errorf("инструкция формы 1: пунктов %d, хочу 2: %v",
			len(res.Forms.Forms[0].InstructionItems), res.Forms.Forms[0].InstructionItems)
	}
	if len(res.Forms.Forms[1].InstructionItems) != 1 ||
		res.Forms.Forms[1].InstructionItems[0] != "Заполните анкету полностью." {
		t.Errorf("инструкция формы 2 захватила чужой заголовок: %v", res.Forms.Forms[1].InstructionItems)
	}

	// Требования к участнику: категории по карте ключевых слов, «прочее» не отброшено.
	if res.Requirements.Status != StatusFull {
		t.Fatalf("требования: статус %s, причина %q", res.Requirements.Status, res.Requirements.Reason)
	}
	wantCategories := []string{
		"лицензии и право на вид деятельности",
		"финансовая устойчивость",
		"правоспособность",
		categoryOther,
	}
	if len(res.Requirements.Items) != len(wantCategories) {
		t.Fatalf("требований %d, хочу %d", len(res.Requirements.Items), len(wantCategories))
	}
	for i, it := range res.Requirements.Items {
		if it.Category != wantCategories[i] {
			t.Errorf("требование %d: категория %q, хочу %q", i, it.Category, wantCategories[i])
		}
		if !it.Ref.Traced() {
			t.Errorf("требование %d: адрес не трассируется: %+v", i, it.Ref)
		}
	}

	// Упоминания инструкций: 5 всего, 4 разрешены (ссылки состава и маркеры
	// форм), одно общее упоминание напечатано наружу.
	if res.Instructions.Total != 5 || res.Instructions.Resolved != 4 {
		t.Errorf("упоминания: всего %d, разрешено %d — хочу 5 и 4", res.Instructions.Total, res.Instructions.Resolved)
	}
	if len(res.Instructions.Unresolved) != 1 {
		t.Fatalf("неразрешённых упоминаний %d, хочу 1: %+v", len(res.Instructions.Unresolved), res.Instructions.Unresolved)
	}
	m := res.Instructions.Unresolved[0]
	if !strings.Contains(m.Quote, "регламентом") || !m.Ref.Traced() {
		t.Errorf("неразрешённое упоминание: %+v", m)
	}

	// Все три full → проблем нет (код выхода 0).
	if problems := res.Problems(); len(problems) != 0 {
		t.Errorf("Problems при полном извлечении: %v", problems)
	}
}

func TestExtractWorstCases(t *testing.T) {
	tests := []struct {
		name string
		body string
		want func(t *testing.T, res Result)
	}{
		{
			name: "без оглавления: разделы не выдумываются, пагинация достоверна",
			body: joinParts(fxTerms, fxReqs, fxComposition, fxForms),
			want: func(t *testing.T, res Result) {
				if len(res.Sections) != 0 {
					t.Errorf("разделы придуманы без оглавления: %+v", res.Sections)
				}
				if !res.Pagination.Trusted {
					t.Errorf("пагинация маленького документа без оглавления обязана быть достоверной: %+v", res.Pagination)
				}
				if res.Composition.Status != StatusFull {
					t.Errorf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
				}
				// Пагинация достоверна → Page попал в адрес, Item — нет (разделов нет).
				ref := res.Composition.Items[0].Ref
				if ref.Page == 0 || ref.Para == 0 {
					t.Errorf("адрес без страницы при достоверной пагинации: %+v", ref)
				}
				if ref.Item != "" {
					t.Errorf("Item выдуман без оглавления: %q", ref.Item)
				}
				// Раздел образцов форм не определён → поиск по всему документу.
				if res.Forms.Status != StatusFull || res.Forms.Section != "" || len(res.Forms.Forms) != 3 {
					t.Errorf("формы: статус %s, раздел %q, форм %d", res.Forms.Status, res.Forms.Section, len(res.Forms.Forms))
				}
				// Раздел требований без оглавления не найти — честное not_found.
				if res.Requirements.Status != StatusNotFound || res.Requirements.Reason == "" {
					t.Errorf("требования: %s — %q", res.Requirements.Status, res.Requirements.Reason)
				}
			},
		},
		{
			name: "без таблицы состава: not_found с кандидатами",
			body: joinParts(fxTOC, fxTerms, fxReqs, []string{
				para("ТРЕБОВАНИЯ К ЗАЯВКЕ НА УЧАСТИЕ В ЗАКУПКЕ"),
				para("Структура заявки приведена в таблице ниже."),
				tbl([][]string{{"Произвольная таблица", "Значение"}, {"а", "б"}}),
			}, fxForms),
			want: func(t *testing.T, res Result) {
				if res.Composition.Status != StatusNotFound {
					t.Errorf("состав: статус %s, хочу not_found", res.Composition.Status)
				}
				if res.Composition.Reason == "" {
					t.Errorf("not_found без причины")
				}
				if len(res.Composition.Candidates) != 1 {
					t.Fatalf("кандидатов %d, хочу 1: %+v", len(res.Composition.Candidates), res.Composition.Candidates)
				}
				if !res.Composition.Candidates[0].Traced() {
					t.Errorf("кандидат без адреса: %+v", res.Composition.Candidates[0])
				}
			},
		},
		{
			name: "начало формы без конец формы: partial с адресом",
			body: joinParts(fxTOC, fxTerms, fxReqs, fxComposition, []string{
				para("Образцы основных форм документов, включаемых в заявку на участие в закупке"),
				para("Декларация о соответствии (форма 7)"),
				para("начало формы"),
				para("Текст декларации без закрывающего маркера."),
			}),
			want: func(t *testing.T, res Result) {
				if res.Forms.Status != StatusPartial {
					t.Errorf("формы: статус %s, хочу partial", res.Forms.Status)
				}
				if res.Forms.Reason == "" || len(res.Forms.Issues) != 1 {
					t.Fatalf("partial без причины/дефекта: %q, %+v", res.Forms.Reason, res.Forms.Issues)
				}
				if !res.Forms.Issues[0].Ref.Traced() {
					t.Errorf("дефект без адреса: %+v", res.Forms.Issues[0].Ref)
				}
				if len(res.Forms.Forms) != 1 || res.Forms.Forms[0].Closed || res.Forms.Forms[0].GroupNumber != "7" {
					t.Errorf("форма собрана неверно: %+v", res.Forms.Forms)
				}
				if res.Forms.Forms[0].InstructionStatus != InstructionNotFound {
					t.Errorf("инструкция «найдена» у незакрытой формы: %+v", res.Forms.Forms[0])
				}
			},
		},
		{
			name: "группа форм: shared на два тела, заголовок с суффиксом, external и пустое тело",
			body: joinParts(fxTOC, fxTerms, fxReqs, fxComposition, fxFormsGroup),
			want: func(t *testing.T, res Result) {
				if res.Forms.Status != StatusFull {
					t.Fatalf("формы: статус %s, причина %q", res.Forms.Status, res.Forms.Reason)
				}
				forms := res.Forms.Forms
				if len(forms) != 4 {
					t.Fatalf("форм %d, хочу 4: %+v", len(forms), forms)
				}
				// Группа «форма 2»: два варианта, один блок инструкций на оба —
				// shared с одним Issue на группу, молчаливого выбора нет.
				for i, want := range []struct {
					group   string
					variant int
				}{{"2", 1}, {"2", 2}} {
					if forms[i].GroupNumber != want.group || forms[i].Variant != want.variant {
						t.Errorf("форма %d: группа %q вариант %d, хочу %q/%d",
							i, forms[i].GroupNumber, forms[i].Variant, want.group, want.variant)
					}
					if forms[i].InstructionStatus != InstructionShared {
						t.Errorf("форма %d: статус %s, хочу %s", i, forms[i].InstructionStatus, InstructionShared)
					}
					if len(forms[i].InstructionItems) != 2 {
						t.Errorf("форма %d: пунктов инструкции %d, хочу 2", i, len(forms[i].InstructionItems))
					}
				}
				if !strings.Contains(forms[0].Title, "на поставку товара") ||
					!strings.Contains(forms[1].Title, "эквивалентного") {
					t.Errorf("названия вариантов перепутаны: %q, %q", forms[0].Title, forms[1].Title)
				}
				// Заголовок с суффиксом: found, суффикс сохранён.
				f3 := forms[2]
				if f3.GroupNumber != "3" || f3.InstructionStatus != InstructionFound {
					t.Errorf("форма 3: группа %q статус %s, хочу 3/%s", f3.GroupNumber, f3.InstructionStatus, InstructionFound)
				}
				if f3.InstructionTitle != "Письма-согласия с проектом договора" {
					t.Errorf("instruction_title = %q", f3.InstructionTitle)
				}
				if len(f3.InstructionItems) != 1 {
					t.Errorf("форма 3: пунктов %d, хочу 1: %v", len(f3.InstructionItems), f3.InstructionItems)
				}
				// Пустое тело + примечание-ссылка: external, а не not_found;
				// название — заголовок группы без хвоста «(форма N)», примечание
				// названием не стало.
				f4 := forms[3]
				if f4.GroupNumber != "4" {
					t.Errorf("форма 4: группа %q, хочу 4", f4.GroupNumber)
				}
				if f4.InstructionStatus != InstructionExternal || !f4.EmptyBody {
					t.Errorf("форма 4: статус %s empty_body %v, хочу %s/true",
						f4.InstructionStatus, f4.EmptyBody, InstructionExternal)
				}
				if f4.Title != "Спецификация (Коммерческое предложение)" {
					t.Errorf("форма 4: title = %q", f4.Title)
				}
				if !strings.Contains(f4.ExternalNote, "«Инструкцией по заполнению»") {
					t.Errorf("форма 4: external_note не сохранён дословно: %q", f4.ExternalNote)
				}
				// Issues: shared на группу, пустое тело, внешняя инструкция —
				// все с трассируемыми адресами.
				var shared, empty, external int
				for _, issue := range res.Forms.Issues {
					if !issue.Ref.Traced() {
						t.Errorf("дефект без адреса: %+v", issue)
					}
					switch {
					case strings.Contains(issue.Reason, "один блок инструкций на 2 форм"):
						shared++
						if !strings.Contains(issue.Reason, "эквивалентного") {
							t.Errorf("в shared-Issue нет перечня форм: %q", issue.Reason)
						}
					case strings.Contains(issue.Reason, "пустое тело"):
						empty++
					case strings.Contains(issue.Reason, "вне формы"):
						external++
					}
				}
				if shared != 1 || empty != 1 || external != 1 {
					t.Errorf("дефекты: shared %d, пустое тело %d, external %d — хочу по 1: %+v",
						shared, empty, external, res.Forms.Issues)
				}
			},
		},
		{
			name: "вырожденная пагинация: все блоки на странице 1 при оглавлении до 5",
			body: joinParts(fxTOC, fxTerms, fxReqs, fxComposition, fxForms),
			want: func(t *testing.T, res Result) {
				if res.Pagination.Trusted || res.Pagination.MaxPage != 1 {
					t.Errorf("вырожденная пагинация не распознана: %+v", res.Pagination)
				}
			},
		},
		{
			name: "пагинация с отметками и оглавлением: max_page меньше обещанного",
			body: joinParts(fxTOC, fxTerms, []string{pageBreakPara("ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ")}, fxReqs[1:], fxComposition, fxForms),
			want: func(t *testing.T, res Result) {
				if res.Pagination.Trusted {
					t.Errorf("пагинация доверена при max_page < toc_max: %+v", res.Pagination)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFixtureDocx(t, "ЗД.docx", tt.body)
			res, err := ExtractFile(path)
			if err != nil {
				t.Fatalf("ExtractFile: %v", err)
			}
			tt.want(t, res)
			// Инвариант адресности: у всех извлечённых структур либо нет
			// элементов, либо каждый несёт трассируемый адрес.
			for _, it := range res.Composition.Items {
				if !it.Ref.Traced() {
					t.Errorf("пункт %s без адреса", it.ID)
				}
			}
			for _, f := range res.Forms.Forms {
				if !f.Ref.Traced() {
					t.Errorf("форма %q без адреса", f.Title)
				}
			}
			for _, it := range res.Requirements.Items {
				if !it.Ref.Traced() {
					t.Errorf("требование без адреса: %q", it.Text)
				}
			}
		})
	}
}

// TestExtractFileErrors — ошибки входа: не .docx и nil-документ.
func TestExtractFileErrors(t *testing.T) {
	txt := filepath.Join(t.TempDir(), "файл.txt")
	if err := os.WriteFile(txt, []byte("текст"), 0o600); err != nil {
		t.Fatalf("запись %s: %v", txt, err)
	}
	if _, err := ExtractFile(txt); err == nil {
		t.Errorf("ExtractFile принял не-.docx без ошибки")
	}
	if _, err := ExtractFile(filepath.Join(t.TempDir(), "нет.docx")); err == nil {
		t.Errorf("ExtractFile принял несуществующий файл без ошибки")
	}
	if _, err := Extract(nil); err == nil {
		t.Errorf("Extract принял nil без ошибки")
	}
}

// TestCompositionFeedsRulesEngine — приёмка критерия 3 #63: извлечённый
// состав подаётся на вход движка правил P2.4 в том же формате конфига, что и
// заданный вручную. Если строгий LoadConfig (DisallowUnknownFields) отвергает
// извлечённое — формат извлечения неверен, чинить надо извлечение, не тест.
func TestCompositionFeedsRulesEngine(t *testing.T) {
	path := writeFixtureDocx(t, "ЗД ЭФ тестовая.docx", fixtureBody())
	res, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if res.Composition.Status != StatusFull {
		t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
	}

	// Тот же массив, что печатает --emit composition.
	items := make([]map[string]any, 0, len(res.Composition.Items))
	for _, it := range res.Composition.Items {
		items = append(items, map[string]any{
			"id":       it.ID,
			"title":    it.Title,
			"required": it.Required,
			"match":    map[string]any{"by": "filename", "pattern": it.Pattern},
		})
	}
	config := map[string]any{
		"version":     1,
		"composition": items,
		"rules":       []map[string]any{{"id": "c1", "class": "composition", "severity": "warning"}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("сериализация конфига: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}

	cfg, err := rules.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig отверг извлечённый состав: %v", err)
	}
	if len(cfg.Composition) != 2 {
		t.Fatalf("пунктов в конфиге %d, хочу 2", len(cfg.Composition))
	}

	var table []rules.CompositionStatus
	handler := rules.NewCompositionHandler(cfg.Composition, configPath, &table)
	pkg := rules.Package{
		Root: t.TempDir(),
		Files: []rules.File{
			{RelPath: "1Опись.docx", Name: "1Опись.docx", Kind: "docx", Supported: true},
			{RelPath: "23Анкета.docx", Name: "23Анкета.docx", Kind: "docx", Supported: true},
		},
	}
	findings, err := handler(cfg.Rules[0], pkg)
	if err != nil {
		t.Fatalf("обработчик состава: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("замечания при полном пакете: %+v", findings)
	}
	if len(table) != 2 {
		t.Fatalf("строк в таблице статусов %d, хочу 2", len(table))
	}
	want := []struct {
		item    string
		present bool
		files   []string
	}{
		{"zd-03", true, []string{"1Опись.docx"}},
		{"zd-04", true, []string{"23Анкета.docx"}},
	}
	for i, w := range want {
		row := table[i]
		if row.Item != w.item || row.Present != w.present || strings.Join(row.Files, ",") != strings.Join(w.files, ",") {
			t.Errorf("строка %d: %+v, хочу item=%s present=%v files=%v", i, row, w.item, w.present, w.files)
		}
	}
}
