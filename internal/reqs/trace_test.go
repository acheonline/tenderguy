package reqs

import (
	"strings"
	"testing"

	"tenderguy/internal/trace"
)

// Тесты P1.6: карточка обязана нести адрес каждого значения, иначе замечание
// P2 некуда показать.

func TestExtractValuesCarryAddresses(t *testing.T) {
	file := "/п/заявление.docx"
	frags := []trace.Fragment{
		{Ref: trace.Ref{File: file, Format: trace.FormatDocx, Page: 1, Para: 3}, Text: "Участник: ИНН " + testINN10},
		{Ref: trace.Ref{File: file, Format: trace.FormatDocx, Page: 2, Para: 18}, Text: "ОГРН " + testOGRN + " от 12.05.2016"},
	}

	c := Extract(file, "docx", frags)

	tests := []struct {
		kind, value string
		want        trace.Ref
	}{
		{KindINN10, testINN10, trace.Ref{File: file, Format: trace.FormatDocx, Page: 1, Para: 3}},
		{KindOGRN, testOGRN, trace.Ref{File: file, Format: trace.FormatDocx, Page: 2, Para: 18}},
		{KindDate, "2016-05-12", trace.Ref{File: file, Format: trace.FormatDocx, Page: 2, Para: 18}},
	}
	for _, tt := range tests {
		it := cardFind(t, c, tt.kind, tt.value)
		if len(it.Refs) != 1 || it.Refs[0] != tt.want {
			t.Errorf("%s=%s: адреса %+v, хотим %+v", tt.kind, tt.value, it.Refs, tt.want)
		}
	}
	if c.Traced != c.Total {
		t.Errorf("трассировано %d из %d", c.Traced, c.Total)
	}
}

func TestExtractRefPointsToCellNotRow(t *testing.T) {
	// Строка таблицы — один фрагмент (иначе метка «ИНН» в соседней клетке
	// перестала бы находиться), но значение принадлежит конкретной клетке.
	file := "/п/форма.xlsx"
	row := trace.NewRow(
		trace.Ref{File: file, Format: trace.FormatXlsx, Sheet: "Форма 2"},
		[]trace.CellAddr{
			{Ref: trace.Ref{File: file, Format: trace.FormatXlsx, Sheet: "Форма 2", Cell: "A1"}, Text: "ИНН"},
			{Ref: trace.Ref{File: file, Format: trace.FormatXlsx, Sheet: "Форма 2", Cell: "B1"}, Text: testINN10},
		},
	)

	it := cardFind(t, Extract(file, "xlsx", []trace.Fragment{row}), KindINN10, testINN10)

	want := trace.Ref{File: file, Format: trace.FormatXlsx, Sheet: "Форма 2", Cell: "B1"}
	if len(it.Refs) != 1 || it.Refs[0] != want {
		t.Errorf("адрес %+v, хотим %+v (клетка B1, а не вся строка)", it.Refs, want)
	}
	if got := it.Refs[0].Where(); got != "Форма 2!B1" {
		t.Errorf("адрес для человека %q, хотим %q", got, "Форма 2!B1")
	}
}

func TestExtractUntracedValueStaysVisible(t *testing.T) {
	// Фрагмент без адреса — ошибка парсера формата, а не свойство документа:
	// значение должно остаться в карточке (иначе потеря тихая), но не попасть
	// в трассированные.
	file := "/п/битый.docx"
	c := Extract(file, "docx", []trace.Fragment{{Ref: trace.Ref{File: file, Format: "docx"}, Text: "ИНН " + testINN10}})

	it := cardFind(t, c, KindINN10, testINN10)
	if len(it.Refs) != 0 || it.RefTotal != 0 {
		t.Errorf("адреса у нетрассированного значения: %+v (%d)", it.Refs, it.RefTotal)
	}
	if c.Total != 1 || c.Traced != 0 {
		t.Errorf("Total=%d Traced=%d, хотим 1 и 0", c.Total, c.Traced)
	}
}

func TestExtractRefsAreCapped(t *testing.T) {
	// Значение из шаблона формы повторяется по всему документу: список адресов
	// ограничиваем, но их число должно остаться известным.
	file := "/п/шаблон.docx"
	var frags []trace.Fragment
	for i := 1; i <= maxItemRefs+5; i++ {
		frags = append(frags, trace.Fragment{
			Ref:  trace.Ref{File: file, Format: trace.FormatDocx, Page: 1, Para: i},
			Text: "ИНН " + testINN10,
		})
	}

	it := cardFind(t, Extract(file, "docx", frags), KindINN10, testINN10)

	if len(it.Refs) != maxItemRefs {
		t.Errorf("сохранено адресов %d, хотим %d", len(it.Refs), maxItemRefs)
	}
	if it.RefTotal != maxItemRefs+5 {
		t.Errorf("RefTotal=%d, хотим %d", it.RefTotal, maxItemRefs+5)
	}
	if it.Hits != maxItemRefs+5 {
		t.Errorf("Hits=%d, хотим %d", it.Hits, maxItemRefs+5)
	}
}

func TestExtractMultiLineFragmentKeepsLineAddress(t *testing.T) {
	// Абзац с явными переносами (w:br) — один фрагмент: значение во второй
	// строке получает адрес того же абзаца, а не следующего блока.
	file := "/п/опись.docx"
	frags := []trace.Fragment{{
		Ref:  trace.Ref{File: file, Format: trace.FormatDocx, Page: 4, Para: 9},
		Text: "Приложение №1\nИНН " + testINN10,
	}}

	it := cardFind(t, Extract(file, "docx", frags), KindINN10, testINN10)

	if len(it.Refs) != 1 || it.Refs[0].Para != 9 || it.Refs[0].Page != 4 {
		t.Errorf("адрес %+v, хотим стр. 4 · абз. 9", it.Refs)
	}
}

func TestExtractScansEveryFragment(t *testing.T) {
	// Проверка на всякий: разбиение на фрагменты не должно терять значения на
	// границах — то, что читалось в сплошном тексте, читается и по кусочкам.
	file := "/п/смешанный.docx"
	text := "ИНН " + testINN10 + "\nНМЦК 5 000 000,00 руб.\nОГРНИП " + testOGRNIP
	whole := Extract(file, "docx", []trace.Fragment{{
		Ref:  trace.Ref{File: file, Format: trace.FormatDocx, Page: 1, Para: 1},
		Text: text,
	}})
	piecewise := Extract(file, "docx", frags(file, "docx", text))

	var kinds []string
	for _, it := range piecewise.Items {
		kinds = append(kinds, it.Kind+"="+it.Value)
	}
	for _, it := range whole.Items {
		if !strings.Contains(strings.Join(kinds, ","), it.Kind+"="+it.Value) {
			t.Errorf("по кусочкам потеряно %s=%s", it.Kind, it.Value)
		}
	}
	if piecewise.Total != whole.Total {
		t.Errorf("значений по кусочкам %d, сплошным текстом %d", piecewise.Total, whole.Total)
	}
}
