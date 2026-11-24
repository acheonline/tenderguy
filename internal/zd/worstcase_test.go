package zd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/parser"
	"tenderguy/internal/rules"
	"tenderguy/internal/trace"
)

// Тесты худших сценариев P2.8: ветки, которые основная фикстура
// (zd_test.go) не достигает — деградация пагинации, таблицы-кандидаты,
// квирки таблицы состава (колонки в другом порядке, подкаталог в колонке 1,
// glob-символы в имени файла), отклонения структуры форм (суффикс мимо
// окна, тело с таблицей, незакрытая форма перед закрытой, наследование
// номера группы) и метрика упоминаний инструкций. Фикстуры собираются из
// тех же частей, что и в zd_test.go (joinParts/para/tbl/writeFixtureDocx).

// extractBody собирает .docx из тела и извлекает структуры; ошибка — провал теста.
func extractBody(t *testing.T, body string) Result {
	t.Helper()
	path := writeFixtureDocx(t, "ЗД худший случай.docx", body)
	res, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

// allRefs — все адреса, которые Result несёт наружу: пункты и кандидаты
// состава, формы и дефекты, требования, неразрешённые упоминания.
func allRefs(res Result) []trace.Ref {
	var out []trace.Ref
	for _, it := range res.Composition.Items {
		out = append(out, it.Ref)
	}
	out = append(out, res.Composition.Candidates...)
	for _, f := range res.Forms.Forms {
		out = append(out, f.Ref)
	}
	for _, issue := range res.Forms.Issues {
		out = append(out, issue.Ref)
	}
	for _, it := range res.Requirements.Items {
		out = append(out, it.Ref)
	}
	for _, m := range res.Instructions.Unresolved {
		out = append(out, m.Ref)
	}
	return out
}

// compTable — таблица состава с заголовком в стандартном порядке колонок.
func compTable(rows ...[]string) string {
	all := [][]string{{
		"№ документа в томе",
		"Наименование документа/ссылка на пункт Закупочной документации",
		"Наименование файла в заявке",
		"Требования к формату и расширению файла",
	}}
	all = append(all, rows...)
	return tbl(all)
}

// TestDetectPaginationUnits — юнит-проверки детектора на синтетических
// документах: ветки «страница не определена» и «немонотонная
// последовательность» в .docx недостижимы (парсер всегда ставит Page ≥ 1 и
// счётчик страниц только растёт), поэтому проверяются напрямую.
func TestDetectPaginationUnits(t *testing.T) {
	paraBlock := func(idx, page int) parser.Block {
		return parser.Block{Index: idx, Type: parser.BlockParagraph, Page: page, Text: "текст"}
	}
	tableBlock := func(idx int, page int, rowPages ...int) parser.Block {
		rows := make([]parser.Row, 0, len(rowPages))
		for i, rp := range rowPages {
			rows = append(rows, parser.Row{Index: i + 1, Page: rp})
		}
		return parser.Block{Index: idx, Type: parser.BlockTable, Page: page,
			Table: &parser.Table{Rows: rows}}
	}

	tests := []struct {
		name       string
		doc        parser.Document
		tocMaxPage int
		wantTrust  bool
		wantReason string
		wantUnique int
		wantMax    int
	}{
		{
			name:       "страница не определена: отметок пагинации нет",
			doc:        parser.Document{Runes: 100, Blocks: []parser.Block{paraBlock(1, 1), paraBlock(2, 0)}},
			wantReason: "страница не определена",
			wantUnique: 1,
			wantMax:    1,
		},
		{
			name:       "немонотонная последовательность страниц",
			doc:        parser.Document{Runes: 100, Blocks: []parser.Block{paraBlock(1, 1), paraBlock(2, 3), paraBlock(3, 2)}},
			wantReason: "не монотонна",
			wantUnique: 2,
			wantMax:    3,
		},
		{
			name:       "объём текста больше порога при одной странице",
			doc:        parser.Document{Runes: 6001, Blocks: []parser.Block{paraBlock(1, 1)}},
			wantReason: "правдоподобный минимум 3",
			wantUnique: 1,
			wantMax:    1,
		},
		{
			name:       "max_page меньше страницы из оглавления",
			doc:        parser.Document{Runes: 100, Blocks: []parser.Block{paraBlock(1, 1), paraBlock(2, 3)}},
			tocMaxPage: 5,
			wantReason: "оглавление обещает страницу 5",
			wantUnique: 2,
			wantMax:    3,
		},
		{
			name:       "достоверная пагинация",
			doc:        parser.Document{Runes: 3000, Blocks: []parser.Block{paraBlock(1, 1), paraBlock(2, 2), paraBlock(3, 3)}},
			wantTrust:  true,
			wantUnique: 3,
			wantMax:    3,
		},
		{
			name: "таблица без страницы блока: берётся страница строки",
			doc: parser.Document{Runes: 100, Blocks: []parser.Block{
				paraBlock(1, 1), tableBlock(2, 0, 0, 2),
			}},
			wantTrust:  true,
			wantUnique: 2,
			wantMax:    2,
		},
		{
			name: "таблица без отметок вовсе: страница не определена",
			doc: parser.Document{Runes: 100, Blocks: []parser.Block{
				paraBlock(1, 1), tableBlock(2, 0, 0, 0),
			}},
			wantReason: "страница не определена",
			wantUnique: 1,
			wantMax:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.doc
			got := detectPagination(&doc, tt.tocMaxPage)
			if got.Trusted != tt.wantTrust {
				t.Errorf("Trusted = %v, хочу %v (%+v)", got.Trusted, tt.wantTrust, got)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, хочу содержать %q", got.Reason, tt.wantReason)
			}
			if tt.wantTrust && got.Reason != "" {
				t.Errorf("у достоверной пагинации причина не пуста: %q", got.Reason)
			}
			if got.UniquePages != tt.wantUnique || got.MaxPage != tt.wantMax {
				t.Errorf("счётчики = %d/%d, хочу %d/%d", got.UniquePages, got.MaxPage, tt.wantUnique, tt.wantMax)
			}
		})
	}
}

// TestExtractPaginationWorstCases — деградация пагинации на уровне файла:
// Page не попадает в адреса при недоверии, причина конкретна, а «страниц нет
// вовсе» для .docx недостижимо (парсер всегда ставит Page ≥ 1).
func TestExtractPaginationWorstCases(t *testing.T) {
	t.Run("все блоки на странице 1 при объёме больше порога", func(t *testing.T) {
		long := para(strings.Repeat("закупочная документация ", 200)) // ~4800 рун
		res := extractBody(t, joinParts(fxComposition, []string{long}))
		if res.Pagination.Trusted {
			t.Fatalf("вырожденная пагинация признана достоверной: %+v", res.Pagination)
		}
		if !strings.Contains(res.Pagination.Reason, "правдоподобный минимум") {
			t.Errorf("причина не названа: %q", res.Pagination.Reason)
		}
		if res.Composition.Status != StatusFull || len(res.Composition.Items) == 0 {
			t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
		}
		for _, it := range res.Composition.Items {
			if it.Ref.Page != 0 {
				t.Errorf("пункт %s: Page записан в адрес при недоверии: %+v", it.ID, it.Ref)
			}
			if !it.Ref.Traced() {
				t.Errorf("пункт %s: адрес не трассируется: %+v", it.ID, it.Ref)
			}
		}
	})

	t.Run("max_page меньше страницы из оглавления", func(t *testing.T) {
		res := extractBody(t, joinParts(fxTOC, []string{
			pageBreakPara("ТЕРМИНЫ И ОПРЕДЕЛЕНИЯ"),
			para("Промежуточный текст."),
			pageBreakPara("ТРЕБОВАНИЯ, ПРЕДЪЯВЛЯЕМЫЕ К УЧАСТНИКАМ ЗАКУПКИ"),
			para("Ещё один промежуточный текст."),
		}, fxComposition, fxForms))
		if res.Pagination.Trusted {
			t.Fatalf("пагинация доверена при max_page < toc_max: %+v", res.Pagination)
		}
		if !strings.Contains(res.Pagination.Reason, "оглавление обещает страницу 5") {
			t.Errorf("причина не про оглавление: %q", res.Pagination.Reason)
		}
		if res.Pagination.MaxPage != 3 {
			t.Errorf("MaxPage = %d, хочу 3 (две отметки разбивки)", res.Pagination.MaxPage)
		}
		for _, it := range res.Composition.Items {
			if it.Ref.Page != 0 {
				t.Errorf("пункт %s: Page записан в адрес при недоверии: %+v", it.ID, it.Ref)
			}
		}
	})

	t.Run("для .docx «страниц нет вовсе» недостижимо", func(t *testing.T) {
		// Файл без единой отметки w:lastRenderedPageBreak: парсер всё равно
		// ставит каждому блоку Page ≥ 1, поэтому ветка «страница не
		// определена» на уровне .docx не срабатывает — фиксируем как есть.
		res := extractBody(t, joinParts(fxTerms, fxComposition))
		if res.Pagination.MaxPage < 1 || res.Pagination.UniquePages < 1 {
			t.Errorf("счётчики страниц нулевые у разобранного .docx: %+v", res.Pagination)
		}
		if !strings.Contains(res.Pagination.Reason, "страница не определена") &&
			res.Pagination.Trusted == false && res.Pagination.Reason == "" {
			t.Errorf("недоверие без причины: %+v", res.Pagination)
		}
	})
}

// TestExtractCompositionCandidates — несколько таблиц-кандидатов: выбирается
// та, у которой в заголовке есть «Наименование файла»; если подходящей нет —
// not_found со списком всех таблиц и адресами.
func TestExtractCompositionCandidates(t *testing.T) {
	decoy := tbl([][]string{{"Произвольный заголовок", "Значение"}, {"а", "б"}})
	real := compTable([]string{"1", "Опись заявки.", "«Опись»", "Doc"})

	t.Run("выбирается таблица с заголовком «Наименование файла»", func(t *testing.T) {
		res := extractBody(t, joinParts(
			[]string{para("Структура заявки ниже."), decoy, para("Ниже вторая таблица."), real},
		))
		if res.Composition.Status != StatusFull {
			t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
		}
		if len(res.Composition.Items) != 1 {
			t.Fatalf("пунктов %d, хочу 1", len(res.Composition.Items))
		}
		// Блоки: 1 — абзац, 2 — таблица-приманка, 3 — абзац, 4 — таблица состава.
		if res.Composition.Items[0].Ref.Para != 4 {
			t.Errorf("пункт извлечён не из той таблицы: %+v", res.Composition.Items[0].Ref)
		}
	})

	t.Run("ни одна таблица не подходит: кандидаты с адресами", func(t *testing.T) {
		decoy2 := tbl([][]string{{"Ещё один заголовок", "Значение"}, {"в", "г"}})
		res := extractBody(t, joinParts([]string{para("Структура заявки ниже."), decoy, decoy2}))
		if res.Composition.Status != StatusNotFound {
			t.Fatalf("статус %s, хочу not_found", res.Composition.Status)
		}
		if !strings.Contains(res.Composition.Reason, "Наименование файла") {
			t.Errorf("причина не называет признак таблицы: %q", res.Composition.Reason)
		}
		if len(res.Composition.Candidates) != 2 {
			t.Fatalf("кандидатов %d, хочу 2: %+v", len(res.Composition.Candidates), res.Composition.Candidates)
		}
		for _, c := range res.Composition.Candidates {
			if !c.Traced() {
				t.Errorf("кандидат без адреса: %+v", c)
			}
		}
	})
}

// TestExtractCompositionQuirks — квирки таблицы состава из реальной ЗД:
// подкаталог в колонке 1 при пустой колонке 0, колонки в другом порядке
// (определение по заголовку, а не по индексу), имя файла без «ёлочек».
func TestExtractCompositionQuirks(t *testing.T) {
	stdHeader := []string{
		"№ документа в томе",
		"Наименование документа/ссылка на пункт Закупочной документации",
		"Наименование файла в заявке",
		"Требования к формату и расширению файла",
	}
	swappedHeader := []string{
		"Наименование файла в заявке",
		"Требования к формату и расширению файла",
		"№ п/п",
		"Наименование документа",
	}

	tests := []struct {
		name string
		rows [][]string // первая строка — заголовок, остальные — тело таблицы
		want func(t *testing.T, items []CompositionItem)
	}{
		{
			name: "подкаталог в колонке 1 при пустой колонке 0",
			rows: [][]string{
				stdHeader,
				{"", `Подкаталог «Дополнительные»`}, // ячеек меньше, чем колонок
				{"1", "Реестр документов.", "«Реестр»", "Xlsx"},
			},
			want: func(t *testing.T, items []CompositionItem) {
				if len(items) != 1 {
					t.Fatalf("пунктов %d, хочу 1", len(items))
				}
				if items[0].Group != "Дополнительные" {
					t.Errorf("group = %q, хочу «Дополнительные»", items[0].Group)
				}
			},
		},
		{
			name: "колонки в другом порядке: определение по заголовку",
			rows: [][]string{
				swappedHeader,
				{"«Реестр»", "Xlsx", "1", "Реестр документов."},
			},
			want: func(t *testing.T, items []CompositionItem) {
				if len(items) != 1 {
					t.Fatalf("пунктов %d, хочу 1", len(items))
				}
				it := items[0]
				if it.Filename != "Реестр" || it.Pattern != "*Реестр*" {
					t.Errorf("filename/pattern = %q/%q", it.Filename, it.Pattern)
				}
				if it.Title != "Реестр документов." {
					t.Errorf("title = %q", it.Title)
				}
				if strings.Join(it.Formats, ",") != "Xlsx" {
					t.Errorf("formats = %v", it.Formats)
				}
				if it.Ref.Cell != "2:1" {
					t.Errorf("адрес ячейки = %q, хочу 2:1 (колонка файла первая)", it.Ref.Cell)
				}
			},
		},
		{
			name: "имя файла без «ёлочек»",
			rows: [][]string{
				stdHeader,
				{"1", "Опись заявки.", "Опись", "Doc"},
			},
			want: func(t *testing.T, items []CompositionItem) {
				if len(items) != 1 {
					t.Fatalf("пунктов %d, хочу 1", len(items))
				}
				if items[0].Filename != "Опись" || items[0].Pattern != "*Опись*" {
					t.Errorf("filename/pattern = %q/%q", items[0].Filename, items[0].Pattern)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := extractBody(t, tbl(tt.rows))
			if res.Composition.Status != StatusFull {
				t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
			}
			tt.want(t, res.Composition.Items)
			for _, it := range res.Composition.Items {
				if !it.Ref.Traced() {
					t.Errorf("пункт %s без адреса: %+v", it.ID, it.Ref)
				}
			}
		})
	}
}

// TestExtractFormsWorstCases — отклонения структуры раздела образцов форм.
func TestExtractFormsWorstCases(t *testing.T) {
	tests := []struct {
		name string
		body string
		want func(t *testing.T, res Result)
	}{
		{
			name: "суффикс заголовка не совпал ни с одной формой окна: shared, а не потеря",
			body: joinParts([]string{
				para("Форма alpha"),
				para("начало формы"),
				para("Текст alpha."),
				para("конец формы"),
				para("Форма beta"),
				para("начало формы"),
				para("Текст beta."),
				para("конец формы"),
				para("Инструкции по заполнению Совершенно посторонней формы"),
				para("Пункт инструкции."),
			}),
			want: func(t *testing.T, res Result) {
				forms := res.Forms.Forms
				if len(forms) != 2 {
					t.Fatalf("форм %d, хочу 2", len(forms))
				}
				for i, f := range forms {
					if f.InstructionStatus != InstructionShared {
						t.Errorf("форма %d: статус %s, хочу %s", i, f.InstructionStatus, InstructionShared)
					}
					if f.InstructionTitle != "Совершенно посторонней формы" {
						t.Errorf("форма %d: instruction_title = %q", i, f.InstructionTitle)
					}
				}
				var shared int
				for _, issue := range res.Forms.Issues {
					if strings.Contains(issue.Reason, "один блок инструкций на 2 форм") {
						shared++
					}
				}
				if shared != 1 {
					t.Errorf("shared-Issue %d, хочу 1: %+v", shared, res.Forms.Issues)
				}
			},
		},
		{
			name: "тело с таблицей внутри не считается пустым",
			body: joinParts([]string{
				para("Форма с таблицей"),
				para("начало формы"),
				tbl([][]string{{"Колонка 1", "Колонка 2"}, {"значение", "значение"}}),
				para("конец формы"),
			}),
			want: func(t *testing.T, res Result) {
				if len(res.Forms.Forms) != 1 {
					t.Fatalf("форм %d, хочу 1", len(res.Forms.Forms))
				}
				f := res.Forms.Forms[0]
				if f.EmptyBody {
					t.Errorf("тело с таблицей помечено пустым: %+v", f)
				}
				for _, issue := range res.Forms.Issues {
					if strings.Contains(issue.Reason, "пустое тело") {
						t.Errorf("дефект «пустое тело» придуман: %+v", issue)
					}
				}
				if !f.Closed || res.Forms.Status != StatusFull {
					t.Errorf("форма/статус: closed=%v status=%s", f.Closed, res.Forms.Status)
				}
			},
		},
		{
			name: "незакрытая форма не забирает инструкцию следующей формы",
			body: joinParts([]string{
				para("Форма A"),
				para("начало формы"),
				para("Текст A без закрывающего маркера."),
				para("Форма B"),
				para("начало формы"),
				para("Текст B."),
				para("конец формы"),
				para("Инструкции по заполнению"),
				para("Пункт для B."),
			}),
			want: func(t *testing.T, res Result) {
				if res.Forms.Status != StatusPartial {
					t.Errorf("статус %s, хочу partial", res.Forms.Status)
				}
				forms := res.Forms.Forms
				if len(forms) != 2 {
					t.Fatalf("форм %d, хочу 2", len(forms))
				}
				if forms[0].Closed || forms[0].InstructionStatus != InstructionNotFound {
					t.Errorf("незакрытая форма: %+v", forms[0])
				}
				if !forms[1].Closed || forms[1].InstructionStatus != InstructionFound {
					t.Errorf("следующая форма: %+v", forms[1])
				}
				if len(forms[1].InstructionItems) != 1 || forms[1].InstructionItems[0] != "Пункт для B." {
					t.Errorf("инструкция ушла не той форме: %v", forms[1].InstructionItems)
				}
				var unclosed int
				for _, issue := range res.Forms.Issues {
					if strings.Contains(issue.Reason, "не найден до следующего") {
						unclosed++
					}
				}
				if unclosed != 1 {
					t.Errorf("дефектов незакрытого маркера %d, хочу 1: %+v", unclosed, res.Forms.Issues)
				}
			},
		},
		{
			name: "чужое название тела не наследует номер группы",
			body: joinParts([]string{
				para("Сведения о субподрядчиках (форма 5)"),
				para("Письмо о совершенно другом"),
				para("начало формы"),
				para("Текст письма."),
				para("конец формы"),
			}),
			want: func(t *testing.T, res Result) {
				f := res.Forms.Forms[0]
				if f.GroupNumber != "" || f.Variant != 0 {
					t.Errorf("номер группы выдуман: group=%q variant=%d", f.GroupNumber, f.Variant)
				}
				if f.Title != "Письмо о совершенно другом" {
					t.Errorf("title = %q", f.Title)
				}
			},
		},
		{
			name: "тело без собственного заголовка: название и номер из заголовка группы",
			body: joinParts([]string{
				para("Сведения о субподрядчиках (форма 5)"),
				para("начало формы"),
				para("Текст сведений."),
				para("конец формы"),
			}),
			want: func(t *testing.T, res Result) {
				f := res.Forms.Forms[0]
				if f.GroupNumber != "5" || f.Variant != 1 {
					t.Errorf("group=%q variant=%d, хочу 5/1", f.GroupNumber, f.Variant)
				}
				if f.Title != "Сведения о субподрядчиках" {
					t.Errorf("title = %q, хочу название группы без хвоста «(форма 5)»", f.Title)
				}
			},
		},
		{
			name: "словоизменение: «субподрядчиков» в группе и «субпоставщиков» в теле — эхо",
			body: joinParts([]string{
				para("Сведения о привлекаемых субподрядчиках (форма 6)"),
				para("Сведения о привлекаемых субпоставщиках"),
				para("начало формы"),
				para("Текст сведений."),
				para("конец формы"),
			}),
			want: func(t *testing.T, res Result) {
				f := res.Forms.Forms[0]
				if f.GroupNumber != "6" {
					t.Errorf("group = %q, хочу 6 (эхо по 5-буквенной приставке)", f.GroupNumber)
				}
				if f.Title != "Сведения о привлекаемых субпоставщиках" {
					t.Errorf("title = %q", f.Title)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := extractBody(t, tt.body)
			if len(res.Forms.Forms) == 0 {
				t.Fatalf("формы не извлечены: %s — %s", res.Forms.Status, res.Forms.Reason)
			}
			tt.want(t, res)
			for _, f := range res.Forms.Forms {
				if !f.Ref.Traced() {
					t.Errorf("форма %q без адреса: %+v", f.Title, f.Ref)
				}
			}
			for _, issue := range res.Forms.Issues {
				if !issue.Ref.Traced() {
					t.Errorf("дефект без адреса: %+v", issue)
				}
			}
		})
	}
}

// TestExtractInstructionMetrics — метрика упоминаний: shared-блок
// учитывается в разрешённых один раз, примечание external-формы считается
// разрешённым, неразрешённые упоминания несут адрес и цитату.
func TestExtractInstructionMetrics(t *testing.T) {
	t.Run("shared-блок учитывается в разрешённых один раз", func(t *testing.T) {
		res := extractBody(t, joinParts([]string{
			para("Форма один"),
			para("начало формы"),
			para("Текст один."),
			para("конец формы"),
			para("Форма два"),
			para("начало формы"),
			para("Текст два."),
			para("конец формы"),
			para("Инструкции по заполнению"),
			para("Общая инструкция для обеих форм."),
		}))
		if res.Instructions.Total != 2 || res.Instructions.Resolved != 2 {
			t.Errorf("всего %d, разрешено %d — хочу 2 и 2 (блок приписан двум формам, но учтён один раз)",
				res.Instructions.Total, res.Instructions.Resolved)
		}
		if len(res.Instructions.Unresolved) != 0 {
			t.Errorf("неразрешённые упоминания при полном разрешении: %+v", res.Instructions.Unresolved)
		}
		for i, f := range res.Forms.Forms {
			if f.InstructionStatus != InstructionShared {
				t.Errorf("форма %d: статус %s, хочу shared", i, f.InstructionStatus)
			}
		}
	})

	t.Run("примечание external-формы считается разрешённым", func(t *testing.T) {
		res := extractBody(t, joinParts([]string{
			para("Спецификация (форма 4)"),
			para(`[заполняется с указанием названия лота, в соответствии с «Инструкцией по заполнению» размещенной в указанном файле формата XLSX]`),
			para("начало формы"),
			para("конец формы"),
		}))
		f := res.Forms.Forms[0]
		if f.InstructionStatus != InstructionExternal {
			t.Fatalf("статус %s, хочу external: %+v", f.InstructionStatus, f)
		}
		if !strings.Contains(f.ExternalNote, "«Инструкцией по заполнению»") {
			t.Errorf("external_note не дословен: %q", f.ExternalNote)
		}
		if res.Instructions.Total != 1 || res.Instructions.Resolved != 1 {
			t.Errorf("всего %d, разрешено %d — хочу 1 и 1", res.Instructions.Total, res.Instructions.Resolved)
		}
		if len(res.Instructions.Unresolved) != 0 {
			t.Errorf("упоминание в примечании напечатано наружу: %+v", res.Instructions.Unresolved)
		}
	})

	t.Run("неразрешённое упоминание несёт адрес и цитату", func(t *testing.T) {
		res := extractBody(t, joinParts([]string{
			para("Общая инструкция электронной площадки размещена на сайте."),
		}))
		if res.Instructions.Total != 1 || res.Instructions.Resolved != 0 {
			t.Fatalf("всего %d, разрешено %d — хочу 1 и 0", res.Instructions.Total, res.Instructions.Resolved)
		}
		if len(res.Instructions.Unresolved) != 1 {
			t.Fatalf("неразрешённых %d, хочу 1", len(res.Instructions.Unresolved))
		}
		m := res.Instructions.Unresolved[0]
		if !m.Ref.Traced() || m.Count != 1 || !strings.Contains(strings.ToLower(m.Quote), "инструкция") {
			t.Errorf("упоминание без адреса/цитаты: %+v", m)
		}
	})
}

// TestAddressInvariant — инвариант адресности: во всех извлечённых
// структурах (состав, кандидаты, формы, дефекты, требования, неразрешённые
// упоминания) нет ни одного адреса с Traced() == false, при любой пагинации
// и любом наборе разделов.
func TestAddressInvariant(t *testing.T) {
	long := para(strings.Repeat("закупочная документация ", 200))
	bodies := map[string]string{
		"основная фикстура":       fixtureBody(),
		"группа форм":             joinParts(fxTOC, fxTerms, fxReqs, fxComposition, fxFormsGroup),
		"без оглавления":          joinParts(fxTerms, fxReqs, fxComposition, fxForms),
		"без таблицы состава":     joinParts(fxTOC, fxTerms, []string{tbl([][]string{{"Прочее", "Значение"}})}),
		"большой объём на стр. 1": joinParts(fxComposition, []string{long}),
		"незакрытая форма":        joinParts([]string{para("Форма"), para("начало формы"), para("Текст.")}),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			res := extractBody(t, body)
			refs := allRefs(res)
			if len(refs) == 0 {
				t.Fatalf("в результате нет ни одного адреса — инвариант нечем проверить: %+v", res)
			}
			for _, r := range refs {
				if !r.Traced() {
					t.Errorf("адрес не трассируется: %+v", r)
				}
			}
		})
	}
}

// TestCompositionGlobAcceptedByRulesConfig — сценарий 6(д): имя файла с
// glob-символами («*», «[») обязано оставаться принимаемым строгим
// rules.LoadConfig — критерий #63 «замена источника не меняет движок правил»
// для худшего входа.
//
// Pattern строится как "*"+escapeGlobLiteral(filename)+"*": каждый
// glob-метасимвол имени выражается символьной группой из одного символа
// («[» → «[[]», «*» → «[*]»), поэтому даже имя с незакрытой символьной
// группой («Справка[») даёт синтаксически верный паттерн. Все три подслучая
// ниже принимаются загрузчиком правил.
func TestCompositionGlobAcceptedByRulesConfig(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{"имя со звёздочкой", "Справка*полная"},
		{"имя с закрытой символьной группой", "Справка[1]"},
		{"имя с незакрытой символьной группой", "Справка["},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := extractBody(t, compTable(
				[]string{"1", "Документ.", "«" + tt.filename + "»", "Doc"},
			))
			if res.Composition.Status != StatusFull || len(res.Composition.Items) != 1 {
				t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
			}
			it := res.Composition.Items[0]
			if it.Filename != tt.filename {
				t.Fatalf("filename = %q, хочу %q", it.Filename, tt.filename)
			}
			// Тот же путь, что и --emit composition: массив пунктов в схеме
			// раздела composition файла правил → строгий LoadConfig.
			data, err := json.Marshal([]map[string]any{{
				"id":       it.ID,
				"title":    it.Title,
				"required": it.Required,
				"match":    map[string]any{"by": "filename", "pattern": it.Pattern},
			}})
			if err != nil {
				t.Fatalf("сериализация: %v", err)
			}
			config := `{"version":1,"composition":` + string(data) +
				`,"rules":[{"id":"c1","class":"composition","severity":"warning"}]}`
			path := filepath.Join(t.TempDir(), "rules.json")
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatalf("запись конфига: %v", err)
			}
			if _, err := rules.LoadConfig(path); err != nil {
				t.Errorf("LoadConfig отверг извлечённый пункт (pattern %q): %v", it.Pattern, err)
			}
		})
	}
}
