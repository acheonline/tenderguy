package trace

import (
	"strings"
	"testing"
)

func TestRefWhere(t *testing.T) {
	tests := []struct {
		name string
		ref  Ref
		want string
	}{
		{
			name: "pdf — страница",
			ref:  Ref{File: "/п/заявка.pdf", Format: FormatPdf, Page: 7},
			want: "стр. 7",
		},
		{
			name: "docx — страница и абзац",
			ref:  Ref{File: "/п/форма.docx", Format: FormatDocx, Page: 2, Para: 14},
			want: "стр. 2 · абз. 14",
		},
		{
			name: "docx без пагинации — только абзац",
			ref:  Ref{File: "/п/выгрузка.docx", Format: FormatDocx, Para: 3},
			want: "абз. 3",
		},
		{
			name: "docx — клетка таблицы",
			ref:  Ref{File: "/п/форма.docx", Format: FormatDocx, Page: 2, Para: 14, Cell: "3:1"},
			want: "стр. 2 · абз. 14 · яч. 3:1",
		},
		{
			name: "xlsx — лист и клетка",
			ref:  Ref{File: "/п/спецификация.xlsx", Format: FormatXlsx, Sheet: "ТЗ", Cell: "B7"},
			want: "ТЗ!B7",
		},
		{
			name: "xlsx — лист без клетки (вся строка)",
			ref:  Ref{File: "/п/спецификация.xlsx", Format: FormatXlsx, Sheet: "ТЗ"},
			want: "ТЗ",
		},
		{
			name: "поля формата не из его правил",
			ref:  Ref{File: "/п/книга.xlsx", Format: FormatXlsx, Page: 3, Para: 5},
			want: "",
		},
		{
			name: "пустой адрес",
			ref:  Ref{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.Where(); got != tt.want {
				t.Errorf("Where() = %q, хотим %q", got, tt.want)
			}
		})
	}
}

func TestRefStringAndTraced(t *testing.T) {
	full := Ref{File: "/п/ф.docx", Format: FormatDocx, Page: 1, Para: 2}
	if got, want := full.String(), "/п/ф.docx · стр. 1 · абз. 2"; got != want {
		t.Errorf("String() = %q, хотим %q", got, want)
	}
	if !full.Traced() {
		t.Error("адрес с файлом и абзацем признан нетрассированным")
	}

	// Файл без места в нём: значение найдено, но показать его человеку некуда.
	if (Ref{File: "/п/ф.docx", Format: FormatDocx}).Traced() {
		t.Error("адрес без координат признан трассированным")
	}
	// Координата без файла не открывает источник.
	if (Ref{Format: FormatPdf, Page: 1}).Traced() {
		t.Error("адрес без файла признан трассированным")
	}
	if got := (Ref{File: "/п/ф.docx", Format: FormatDocx}).String(); got != "/п/ф.docx" {
		t.Errorf("String() для адреса без координат = %q", got)
	}
}

func TestNewRowSegmentsLocateCells(t *testing.T) {
	base := Ref{File: "ф.docx", Format: FormatDocx, Page: 1, Para: 4}
	cell := func(col string) Ref {
		r := base
		r.Cell = col
		return r
	}
	f := NewRow(base, []CellAddr{
		{Ref: cell("1:1"), Text: "ИНН"},
		{Ref: cell("1:2"), Text: ""},
		{Ref: cell("1:3"), Text: "7714567895"},
	})

	if f.Text != "ИНН\t\t7714567895" {
		t.Errorf("текст строки %q", f.Text)
	}
	if len(f.Segments) != 2 {
		t.Fatalf("сегментов %d, хотим 2 (пустая клетка адреса не получает)", len(f.Segments))
	}
	// Смещение внутри первого слова — клетка 1:1, начало третьего — клетка 1:3.
	if got := f.RefAt(1); got.Cell != "1:1" {
		t.Errorf("RefAt(1) = %+v, хотим клетку 1:1", got)
	}
	if got := f.RefAt(len("ИНН\t\t")); got.Cell != "1:3" {
		t.Errorf("RefAt(%d) = %+v, хотим клетку 1:3", len("ИНН\t\t"), got)
	}
	// Смещение в табуляции между клетками остаётся адресом всей строки.
	if got := f.RefAt(len("ИНН")); got.Cell != "" || got.Para != 4 {
		t.Errorf("RefAt в разделителе = %+v, хотим адрес строки", got)
	}
	if got := f.RefAt(len(f.Text) + 10); got.Para != 4 {
		t.Errorf("RefAt за концом текста = %+v, хотим адрес строки", got)
	}
}

func TestStatsCountsContentFragments(t *testing.T) {
	traced := Ref{File: "ф.docx", Format: FormatDocx, Page: 1, Para: 2}

	var s Stats
	s.Add(Fragment{Ref: traced, Text: "  \n\t "}) // пустой: не считается
	s.Add(Fragment{Ref: traced, Text: "ИНН 7714567895"})
	s.Add(Fragment{Ref: Ref{File: "ф.docx", Format: FormatDocx}, Text: "абзац без координат"})

	if s.Fragments != 2 || s.Traced != 1 {
		t.Errorf("Fragments=%d Traced=%d, хотим 2 и 1", s.Fragments, s.Traced)
	}
	if s.Missing() != 1 || s.Complete() {
		t.Errorf("Missing=%d Complete=%v, хотим 1 и false", s.Missing(), s.Complete())
	}
	if s.Share() != 0.5 {
		t.Errorf("Share() = %v, хотим 0.5", s.Share())
	}
	if len(s.Untraced) != 1 || s.Untraced[0] != "абзац без координат" {
		t.Errorf("образцы нетрассированных: %v", s.Untraced)
	}

	// Пустой документ: проверять нечего, а не «потеряно всё».
	var empty Stats
	if !empty.Complete() || empty.Share() != 0 {
		t.Errorf("пустой набор: Complete=%v Share=%v", empty.Complete(), empty.Share())
	}
}

func TestSampleCutsLongFragment(t *testing.T) {
	long := make([]rune, sampleRunes+30)
	for i := range long {
		long[i] = 'ы'
	}
	got := sample(string(long))
	if len([]rune(got)) != sampleRunes+1 {
		t.Errorf("длина образца %d, хотим %d", len([]rune(got)), sampleRunes+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("образец не помечен обрезанным: %q", got)
	}
}
