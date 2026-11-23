package reqs

import (
	"strings"
	"testing"

	"tenderguy/internal/trace"
)

func TestNormalizeOrgName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"регистр и пробелы", "ООО   РОМАШКА", "ооо ромашка"},
		{"ё приводится к е", "ООО «ТехноЁлка»", "ооо техноелка"},
		{"полная ОПФ к сокращённой", "Общество с ограниченной ответственностью «Ромашка»", "ооо ромашка"},
		{"ПАО длиннее АО", "Публичное акционерное общество «Газпром»", "пао газпром"},
		{"АО без публичности", "Акционерное общество «Ромашка»", "ао ромашка"},
		{"индивидуальный предприниматель", "Индивидуальный предприниматель Иванов", "ип иванов"},
		{"ФГУП", "Федеральное государственное унитарное предприятие «Старт»", "фгуп старт"},
		{"кавычки разных видов", "ООО „Ромашка“", "ооо ромашка"},
		{"прямые кавычки", `ООО "Ромашка"`, "ооо ромашка"},
		{"кавычки без пробелов", "ООО«Ромашка»", "ооо ромашка"},
		{"скобки как кавычки", "Крестьянское (фермерское) хозяйство «Поле»", "кфх поле"},
		{"NBSP после сокращения", "ООО\u00A0«Ромашка»", "ооо ромашка"},
		{"пунктуация по краям", "  ООО «Ромашка». ", "ооо ромашка"},
		{"пустая строка", "", ""},
		{"только пунктуация", "..—..", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeOrgName(tt.in); got != tt.want {
				t.Errorf("NormalizeOrgName(%q) = %q, хотим %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSameOrgName(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want Match
	}{
		{
			"полная и сокращённая ОПФ — одно имя",
			"Общество с ограниченной ответственностью «Ромашка»", "ООО \"Ромашка\"",
			MatchEqual,
		},
		{"регистр и пробелы не важны", "ООО  Ромашка", "ооо ромашка", MatchEqual},
		{"разные организации", "ООО «Ромашка»", "ООО «Лютик»", MatchDifferent},
		{"одно значение пусто — не найдено", "ООО «Ромашка»", "", MatchMissing},
		{"оба пустые — не найдено", "", "   ", MatchMissing},
		{"вырожденное после нормализации", "ООО «Ромашка»", "—", MatchMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameOrgName(tt.a, tt.b); got != tt.want {
				t.Errorf("SameOrgName(%q, %q) = %v, хотим %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMatchString(t *testing.T) {
	tests := []struct {
		m    Match
		want string
	}{
		{MatchEqual, "эквивалентно"},
		{MatchDifferent, "не эквивалентно"},
		{MatchMissing, "не найдено"},
		{Match(42), "неизвестный исход"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("Match(%d).String() = %q, хотим %q", tt.m, got, tt.want)
		}
	}
}

func TestExtractOrgName(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		value string // "" — значение извлекаться не должно
		valid bool
		label string
	}{
		{
			"полное наименование с кавычками",
			"Полное наименование: ООО «Ромашка»",
			"ооо ромашка", true, "Полное наименование",
		},
		{
			"наименование участника закупки",
			"Наименование участника закупки — Общество с ограниченной ответственностью «Ромашка»",
			"ооо ромашка", true, "Наименование участника закупки",
		},
		{
			"значение упирается в следующий реквизит",
			"Полное наименование: ООО «Ромашка». ИНН " + testINN10 + " КПП " + testKPP,
			"ооо ромашка", true, "Полное наименование",
		},
		{
			"значение до первого таба",
			"Наименование\tООО «Ромашка»\t10\t100,00",
			"ооо ромашка", true, "Наименование",
		},
		{
			"творительный падеж «наименованием» меткой не является",
			"Дополнено наименованием ООО «Ромашка»",
			"", false, "",
		},
		{
			"наименование без ОПФ остаётся подозрительным",
			"Фирменное наименование: Ромашка",
			"ромашка", false, "Фирменное наименование",
		},
		{
			"голая метка без ОПФ — колонка спецификации, не организация",
			"Наименование\tБолт М6\t100",
			"", false, "",
		},
		{
			"товарная позиция под сильной меткой отсекается",
			"Полное наименование: Работы по ремонту кровли",
			"", false, "",
		},
		{
			"ИП Recognised как ОПФ",
			"Наименование поставщика: ИП Иванов Иван Иванович",
			"ип иванов иван иванович", true, "Наименование поставщика",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := extract("/п/x.docx", "docx", tt.line)
			if tt.value == "" {
				for _, it := range c.Items {
					if it.Kind == KindOrgName {
						t.Errorf("наименования не ждали, получили %+v", it)
					}
				}
				return
			}
			it := cardFind(t, c, KindOrgName, tt.value)
			if it.Valid != tt.valid {
				t.Errorf("Valid=%v (%s), хотим %v", it.Valid, it.Reason, tt.valid)
			}
			if !it.Valid && it.Reason == "" {
				t.Errorf("подозрительное значение без причины: %+v", it)
			}
			if it.Label != tt.label {
				t.Errorf("Label=%q, хотим %q", it.Label, tt.label)
			}
		})
	}
}

func TestExtractOrgNameCarriesAddress(t *testing.T) {
	// Критерий P2.2: у найденного наименования есть адрес в источнике, и
	// Card.Traced учитывает новый вид наравне с существующими.
	file := "/п/заявка.docx"
	fr := []trace.Fragment{
		{Ref: trace.Ref{File: file, Format: trace.FormatDocx, Page: 2, Para: 5},
			Text: "Полное наименование: ООО «Ромашка»"},
	}
	c := Extract(file, "docx", fr)

	it := cardFind(t, c, KindOrgName, "ооо ромашка")
	if len(it.Refs) != 1 || it.Refs[0].Page != 2 || it.Refs[0].Para != 5 {
		t.Errorf("адрес наименования %+v, хотим страница 2 абзац 5", it.Refs)
	}
	if c.Traced != len(c.Items) {
		t.Errorf("Traced=%d из %d", c.Traced, len(c.Items))
	}
}

func TestExtractOrgNameXlsxCellAddress(t *testing.T) {
	// Новый вид адресуется клеткой листа, как числовые реквизиты (P1.6).
	c := extract("/п/форма1.xlsx", "xlsx", "Наименование\tООО «Ромашка»")

	it := cardFind(t, c, KindOrgName, "ооо ромашка")
	if len(it.Refs) != 1 || it.Refs[0].Sheet != "Лист1" || it.Refs[0].Cell != "A1" {
		t.Errorf("адрес значения %+v, хотим Лист1!A1", it.Refs)
	}
}

func TestExtractOrgNameMergesRepeats(t *testing.T) {
	// Одно и то же имя в шапке и в подписи сливается: Hits считает повторы,
	// адреса объединяются (P1.6).
	text := strings.Join([]string{
		"Полное наименование: ООО «Ромашка»",
		"Наименование организации — ООО \"Ромашка\"",
	}, "\n")
	c := extract("/п/x.docx", "docx", text)

	it := cardFind(t, c, KindOrgName, "ооо ромашка")
	if it.Hits != 2 {
		t.Errorf("Hits=%d, хотим 2", it.Hits)
	}
	if it.RefTotal != 2 || len(it.Refs) != 2 {
		t.Errorf("адресов %d (сохранено %d), хотим 2", it.RefTotal, len(it.Refs))
	}
	if c.Total != 1 {
		t.Errorf("Total=%d, хотим 1 (повторы сливаются)", c.Total)
	}
}
