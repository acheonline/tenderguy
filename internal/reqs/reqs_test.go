package reqs

import (
	"fmt"
	"strings"
	"testing"

	"tenderguy/internal/trace"
)

// cardFind ищет в карточке значение указанного вида; тесты сверляют найденное,
// а не порядок строк в отчёте.
func cardFind(t *testing.T, c *Card, kind, value string) Item {
	t.Helper()

	for _, it := range c.Items {
		if it.Kind == kind && it.Value == value {
			return it
		}
	}
	t.Fatalf("в карточке нет значения %s=%s; содержимое: %+v", kind, value, c.Items)
	return Item{}
}

// frags — текст как набор абзацев с адресами. С P1.6 карточка строится по
// фрагментам, а не по сплошному тексту, поэтому тестам извлечения нужен
// источник, у которого адрес есть: правила показа адреса у форматов разные
// (.docx и PDF — страница и абзац, .xlsx — лист и клетка), и helper их
// воспроизводит.
func frags(file, format, text string) []trace.Fragment {
	var out []trace.Fragment
	for i, line := range strings.Split(text, "\n") {
		ref := trace.Ref{File: file, Format: format}
		switch format {
		case trace.FormatXlsx:
			ref.Sheet = "Лист1"
			ref.Cell = fmt.Sprintf("A%d", i+1)
		case trace.FormatPdf:
			ref.Page = 1
		default:
			ref.Page = 1
			ref.Para = i + 1
		}
		out = append(out, trace.Fragment{Ref: ref, Text: line})
	}
	return out
}

// extract — карточка по тексту: короткая запись для тестов, которым адрес не
// предмет проверки.
func extract(file, format, text string) *Card {
	return Extract(file, format, frags(file, format, text))
}

func TestExtractFindsAllKinds(t *testing.T) {
	text := strings.Join([]string{
		"Участник закупки: ИНН " + testINN10 + " КПП " + testKPP,
		"ОГРН " + testOGRN + ", регистрация 12.05.2016",
		"ИП Иванов, ИНН " + testINN12 + ", ОГРНИП " + testOGRNIP,
		"НМЦК 5 000 000,00 руб.",
		"Срок поставки — до 15 июня 2026",
	}, "\n")

	c := extract("/путь/заявка.docx", "docx", text)

	if c.Path != "/путь/заявка.docx" || c.Name != "заявка.docx" || c.Format != "docx" {
		t.Errorf("заголовок карточки: %q %q %q", c.Path, c.Name, c.Format)
	}
	for _, want := range []struct{ kind, value string }{
		{KindINN10, testINN10},
		{KindKPP, testKPP},
		{KindOGRN, testOGRN},
		{KindINN12, testINN12},
		{KindOGRNIP, testOGRNIP},
		{KindDate, "2016-05-12"},
		{KindDate, "2026-06-15"},
		{KindMoney, "5000000.00 RUB"},
	} {
		it := cardFind(t, c, want.kind, want.value)
		if !it.Valid {
			t.Errorf("%s=%s помечено подозрительным: %s", want.kind, want.value, it.Reason)
		}
	}
	if c.Suspect != 0 {
		t.Errorf("подозрительных значений %d, хотим 0", c.Suspect)
	}
	if len(c.Notes) != 0 {
		t.Errorf("заметок быть не должно, получили %v", c.Notes)
	}
}

func TestExtractInvalidValuesStayInCard(t *testing.T) {
	// Значение с несходящейся контрольной цифрой или несуществующим днём —
	// предмет сверки P2, поэтому оно должно остаться в карточке, а не молча
	// пропасть: иначе «подозрительный реквизит» стал бы «реквизита нет».
	c := extract("/путь/заявка.docx", "docx", "ИНН 7714567894, дата 31.04.2026, договор от 01.01.1899")

	bad := cardFind(t, c, KindINN10, "7714567894")
	if bad.Valid || bad.Reason == "" {
		t.Errorf("битый ИНН вошёл в карточку как корректный: %+v", bad)
	}
	badDate := cardFind(t, c, KindDate, "2026-04-31")
	if badDate.Valid || badDate.Reason == "" {
		t.Errorf("31 апреля вошло в карточку как корректная дата: %+v", badDate)
	}
	oldDate := cardFind(t, c, KindDate, "1899-01-01")
	if oldDate.Valid || oldDate.Reason == "" {
		t.Errorf("дата 1899 года вошла в карточку как корректная: %+v", oldDate)
	}
	if c.Total != 3 || c.Suspect != 3 {
		t.Errorf("Total=%d Suspect=%d, хотим 3 и 3 (карточка: %+v)", c.Total, c.Suspect, c.Items)
	}
}

func TestExtractNumberBoundaries(t *testing.T) {
	// Число из 14 цифр не должно порождать ни ИНН(10), ни КПП(9), ни ИНН(12):
	// \b слева и справа проверяет всю последовательность целиком.
	c := extract("/п/x.docx", "docx", "код 12345678901234\nИНН 12345678901234567")

	if len(c.Items) != 0 {
		t.Errorf("из длинных последовательностей цифр что-то извлечено: %+v", c.Items)
	}
}

func TestExtractMoneyFormats(t *testing.T) {
	// Пробелы в разрядах Word и Excel ставят как обычный пробел, NBSP (U+00A0)
	// или узкий NBSP (U+202F) — все три варианта должны читаться.
	tests := []struct {
		name  string
		line  string
		value string
		valid bool
	}{
		{"пробел в разрядах, запятая в копейках", "НМЦК 1 000 000,50 руб.", "1000000.50 RUB", true},
		{"NBSP между разрядами", "цена контракта 2\u00A0500\u00A0000 рублей", "2500000 RUB", true},
		{"узкий NBSP перед маркером", "итого 100\u202F₽", "100 RUB", true},
		{"доллар", "эквивалент 10 000 USD", "10000 USD", true},
		{"число без разделителей", "обеспечение заявки 5000000 руб.", "5000000 RUB", true},
		{"маркер после числа без пробела", "аванс 250000,00руб.", "250000.00 RUB", true},
		// Выгрузки из бухгалтерских систем группируют разряды запятой и пишут
		// дробь через точку — «2,617,844.26» это те же 2 617 844,26.
		{"американская группировка", "общая стоимость 2,617,844.26 руб.", "2617844.26 RUB", true},
		{"код валюты без точки", "цена 438,819.67 RUB", "438819.67 RUB", true},
		{"один разделитель и три цифры — группировка", "итого 1,000 руб.", "1000 RUB", true},
		// Число, которое не читается ни как копейки, ни как группы разрядов,
		// суммы не даёт: «1,2345» — это обрывок, а не 1.2345 рубля.
		{"число без маркера не сумма", "цена ед 12 345", "", false},
		{"нечитаемая дробь", "итого 1,2345 руб.", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := extract("/п/x.docx", "docx", tt.line)
			if tt.value == "" {
				if len(c.Items) != 0 {
					t.Errorf("%q: денег не ждали, получили %+v", tt.line, c.Items)
				}
				return
			}
			it := cardFind(t, c, KindMoney, tt.value)
			if it.Valid != tt.valid {
				t.Errorf("%q: Valid=%v (%s), хотим %v", tt.line, it.Valid, it.Reason, tt.valid)
			}
		})
	}
}

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
		valid bool
	}{
		{"целое без разделителей", "5000000", "5000000", true},
		{"пробел в разрядах и копейки", "1 000 000,50", "1000000.50", true},
		{"NBSP в разрядах", "2\u00A0500\u00A0000", "2500000", true},
		{"узкий NBSP", "10\u202F200", "10200", true},
		{"запятая в разрядах, точка в дроби", "2,617,844.26", "2617844.26", true},
		{"одна копейка", "25,5", "25.5", true},
		{"группировка из трёх цифр", "1,000", "1000", true},
		{"ноль", "0", "0", true},
		{"ноль с копейками", "0,00", "0.00", true},
		{"четыре цифры после разделителя", "1,2345", "", false},
		{"группа не из трёх цифр", "12,34,567", "", false},
		{"разделитель в начале", ",50", "", false},
		{"пустая строка", "", "", false},
		{"не число", "abc", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAmount(tt.raw)
			if ok != tt.valid || got != tt.want {
				t.Errorf("parseAmount(%q) = (%q, %v), хотим (%q, %v)", tt.raw, got, ok, tt.want, tt.valid)
			}
		})
	}
}

func TestExtractDateFormats(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		value string
		raw   string
	}{
		{"точками", "договор от 01.02.2026", "2026-02-01", "01.02.2026"},
		{"слэшами", "акт 01/02/2026", "2026-02-01", "01/02/2026"},
		{"названием месяца", "предложение действует до 5 декабря 2026", "2026-12-05", "5 декабря 2026"},
		{"название месяца с заглавной", "Дата выдачи: 12 Мая 2026", "2026-05-12", "12 Мая 2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := extract("/п/x.docx", "docx", tt.line)
			it := cardFind(t, c, KindDate, tt.value)
			if it.Raw != tt.raw {
				t.Errorf("Raw=%q, хотим %q", it.Raw, tt.raw)
			}
			if !it.Valid {
				t.Errorf("дата %q помечена подозрительной: %s", tt.line, it.Reason)
			}
		})
	}
}

func TestExtractMergesRepeatedValues(t *testing.T) {
	c := extract("/п/x.xlsx", "xlsx", "ИНН "+testINN10+"\nподпись: "+testINN10+"\nпечать: "+testINN10)

	it := cardFind(t, c, KindINN10, testINN10)
	if it.Hits != 3 {
		t.Errorf("Hits=%d, хотим 3", it.Hits)
	}
	if c.Total != 1 {
		t.Errorf("Total=%d, хотим 1 (повторы сливаются)", c.Total)
	}
	// Слияние повторов не должно терять адреса: все три места значения —
	// три разных адреса (P1.6).
	if it.RefTotal != 3 || len(it.Refs) != 3 {
		t.Errorf("адресов %d (сохранено %d), хотим 3", it.RefTotal, len(it.Refs))
	}
}

func TestExtractItemsSortedByKindOrder(t *testing.T) {
	c := extract("/п/x.docx", "docx", "итого 100 руб.\nдата 01.01.2026\nИНН "+testINN10+"\nОГРН "+testOGRN)

	var kinds []string
	for _, it := range c.Items {
		kinds = append(kinds, it.Kind)
	}
	want := []string{KindINN10, KindOGRN, KindDate, KindMoney}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("порядок видов %v, хотим %v", kinds, want)
	}
}

func TestExtractLabels(t *testing.T) {
	c := extract("/п/x.docx", "docx", "ИНН 7714567894")
	if got := cardFind(t, c, KindINN10, "7714567894").Label; got != "ИНН" {
		t.Errorf("метка %q, хотим «ИНН»", got)
	}

	// Метка дальше 60 символов от значения уже может относиться к другой
	// строке формы, поэтому она не должна попадать в карточку.
	far := "ИНН " + strings.Repeat("х", 70) + " 7714567894"
	if got := cardFind(t, extract("/п/x.docx", "docx", far), KindINN10, "7714567894").Label; got != "" {
		t.Errorf("метка %q найдена дальше контрольного расстояния", got)
	}
}

func TestExtractNotesOnEmptyCards(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantNote string
	}{
		{"текст не извлечён", "  \n\t\n", "текст документа пуст"},
		{"идентификаторов нет", "Поставка оборудования, итого 100 руб.", "ИНН/ОГРН не найдены"},
		{
			name:     "идентификатор есть — заметки не нужно",
			text:     "ИНН " + testINN10,
			wantNote: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := extract("/п/x.pdf", "pdf", tt.text)
			joined := strings.Join(c.Notes, "\n")
			if tt.wantNote == "" {
				if len(c.Notes) != 0 {
					t.Errorf("заметок не ждали, получили %v", c.Notes)
				}
				return
			}
			if !strings.Contains(joined, tt.wantNote) {
				t.Errorf("в заметках нет %q: %v", tt.wantNote, c.Notes)
			}
		})
	}
}

func TestCardAddCountsAndSorts(t *testing.T) {
	c := &Card{Path: "/п/x.docx", Name: "x.docx", Format: "docx"}
	c.add(Item{Kind: KindMoney, Value: "200 RUB", Valid: true})
	c.add(Item{Kind: KindMoney, Value: "100 RUB", Valid: false, Reason: "формат"})
	c.add(Item{Kind: KindMoney, Value: "200 RUB", Valid: true, Label: "НМЦК"})
	c.add(Item{Kind: KindINN10, Value: testINN10, Valid: true})
	c.finalize()

	if c.Total != 3 {
		t.Errorf("Total=%d, хотим 3", c.Total)
	}
	if c.Suspect != 1 {
		t.Errorf("Suspect=%d, хотим 1", c.Suspect)
	}
	if got := cardFind(t, c, KindMoney, "200 RUB").Hits; got != 2 {
		t.Errorf("Hits=%d, хотим 2", got)
	}
	// В пределах вида значения упорядочены по строке: 100 стоит перед 200.
	if c.Items[1].Value != "100 RUB" || c.Items[2].Value != "200 RUB" {
		t.Errorf("порядок внутри вида: %q, %q", c.Items[1].Value, c.Items[2].Value)
	}
	// Метка не перетирается пустой при слиянии повторов.
	if got := cardFind(t, c, KindINN10, testINN10).Label; got != "" {
		t.Errorf("метка у ИНН %q, хотим пустую", got)
	}
	// Значений с адресом здесь нет: TestExtractValuesCarryAddresses проверяет
	// обратное, когда адреса есть.
	if c.Traced != 0 {
		t.Errorf("Traced=%d, хотим 0", c.Traced)
	}
}
