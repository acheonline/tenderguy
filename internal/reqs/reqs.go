// Package reqs извлекает реквизиты документов (ИНН, КПП, ОГРН, даты,
// суммы) из фрагментов, полученных парсерами P1.2–P1.4, и формирует карточку
// файла — вход для детерминированных сверок эпика P2.
//
// Задача P1.5: поиск регулярными выражениями с валидацией найденного —
// контрольные числа ИНН и ОГРН, структура КПП, календарная корректность дат,
// денежный формат. Значение, не прошедшее проверку, в карточке остаётся
// (Valid=false с причиной): расходящиеся реквизиты и есть предмет проверки
// пакета.
//
// Позиции фрагментов карточка носит с собой (задача P1.6): каждый Item знает
// адрес, где значение найдено — «файл + страница/абзац» для .docx, «лист +
// ячейка» для .xlsx, «страница» для PDF. Без этого замечание P2 нельзя
// показать человеку: он должен открыть место, а не искать его глазами.
// Метка (Label) — ближайшее ключевое слово перед значением — осталась как
// подсказка «это ИНН» или «это НМЦК», адрес на неё не опирается.
package reqs

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"tenderguy/internal/trace"
)

// Виды реквизитов в карточке.
const (
	KindINN10  = "inn10"  // ИНН юрлица — 10 цифр
	KindINN12  = "inn12"  // ИНН физлица/ИП — 12 цифр
	KindKPP    = "kpp"    // КПП — 9 цифр
	KindOGRN   = "ogrn"   // ОГРН юрлица — 13 цифр
	KindOGRNIP = "ogrnip" // ОГРНИП — 15 цифр
	KindDate   = "date"   // дата
	KindMoney  = "money"  // сумма с валютным маркером
)

// maxItemRefs — сколько адресов значения храним целиком: больше десяти
// означает «значение набрано по всему документу», и полный список только
// раздувает отчёт. RefTotal при этом считает все различные адреса.
const maxItemRefs = 10

// Item — одно значение реквизита в карточке. Одинаковые (вид + нормализованное
// значение) сливаются, Hits считает повторения в документе.
type Item struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Raw   string `json:"raw,omitempty"`
	// Valid — пройдена ли проверка (контрольное число, календарь, формат).
	Valid bool `json:"valid"`
	// Reason — почему не пройдена, если Valid=false.
	Reason string `json:"reason,omitempty"`
	// Label — ближайшее ключевое слово перед значением («ИНН», «НМЦК», …).
	Label string `json:"label,omitempty"`
	// Hits — сколько раз значение встретилось в документе.
	Hits int `json:"hits"`
	// Refs — адреса в источнике, где значение найдено (P1.6). У слиянного
	// значения их несколько: одно и то же «1234567890» может стоять в шапке,
	// в таблице и в подписи, и замечание обязано показывать все места.
	Refs []trace.Ref `json:"refs,omitempty"`
	// RefTotal — различных адресов всего; больше len(Refs) значит, что список
	// обрезан до maxItemRefs.
	RefTotal int `json:"ref_total,omitempty"`
}

// addRef запоминает адрес значения, не повторяя уже известный.
func (i *Item) addRef(r trace.Ref) {
	if !r.Traced() {
		return
	}
	for _, have := range i.Refs {
		if have == r {
			return
		}
	}
	i.RefTotal++
	if len(i.Refs) < maxItemRefs {
		i.Refs = append(i.Refs, r)
	}
}

// Card — карточка реквизитов одного файла.
type Card struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Format string `json:"format"`

	Items []Item `json:"items"`

	// Total — число различных значений всех видов.
	Total int `json:"total"`
	// Suspect — значений, не прошедших проверку: их видно сразу.
	Suspect int `json:"suspect"`
	// Traced — значений, у которых есть адрес в источнике. Критерий P1.6 —
	// Traced == Total; отставание ищет дыру не в документе, а в парсере.
	Traced int `json:"traced"`

	Notes []string `json:"notes,omitempty"`
}

// Сортировка и вывод карточки по порядку видов.
var kindOrder = []string{KindINN10, KindINN12, KindKPP, KindOGRN, KindOGRNIP, KindDate, KindMoney}

func kindRank(kind string) int {
	for i, k := range kindOrder {
		if k == kind {
			return i
		}
	}
	return len(kindOrder)
}

// add дописывает значение в карточку, сливая повторы по виду и значению.
func (c *Card) add(item Item) {
	if item.Hits == 0 {
		item.Hits = 1
	}
	for i := range c.Items {
		if c.Items[i].Kind == item.Kind && c.Items[i].Value == item.Value {
			c.Items[i].Hits += item.Hits
			if c.Items[i].Label == "" {
				c.Items[i].Label = item.Label
			}
			// Адреса повтора объединяются: значение могло стоять в трёх местах,
			// и замечание P2 должно вести во все, а не в первое.
			for _, r := range item.Refs {
				c.Items[i].addRef(r)
			}
			return
		}
	}
	c.Items = append(c.Items, item)
}

func (c *Card) finalize() {
	sort.SliceStable(c.Items, func(i, j int) bool {
		ki, kj := kindRank(c.Items[i].Kind), kindRank(c.Items[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return c.Items[i].Value < c.Items[j].Value
	})
	c.Total = len(c.Items)
	c.Suspect = 0
	c.Traced = 0
	for _, it := range c.Items {
		if !it.Valid {
			c.Suspect++
		}
		if len(it.Refs) > 0 {
			c.Traced++
		}
	}
}

// labelWords — ключевые слова, которыми человек помечает реквизит в форме.
// Порядок внутри строки не важен: ищем ближайшее перед значением.
var labelWords = []string{
	"ИНН", "КПП", "ОГРН", "ОГРНИП", "СНИЛС", "НМЦК", "НМЦ договора",
	"начальная цена", "обеспечение заявки", "обеспечение исполнения",
	"цена контракта", "цена ед", "стоимость", "сумма", "итого", "НДС",
	"валюта", "дата", "выдан", "зарегистрирован", "действителен",
}

// Extract строит карточку реквизитов по фрагментам содержимого: три вида
// числовых идентификаторов, даты (цифровые и с названием месяца) и суммы с
// валютным маркером. Фрагменты отдаёт парсер формата (docx/xlsx/pdf — P1.2…P1.4)
// вместе с адресом, поэтому найденное значение хранит, где именно оно стоит:
// строка в .docx, клетка листа в .xlsx, страница в PDF (P1.6).
func Extract(path, format string, frags []trace.Fragment) *Card {
	c := &Card{Path: path, Name: filepath.Base(path), Format: format}
	nonEmptyLines := 0
	for _, frag := range frags {
		// Смещение строки внутри фрагмента нужно, чтобы доехать до клетки:
		// адрес «строка таблицы» уточняется сегментами фрагмента.
		pos := 0
		for _, line := range strings.Split(frag.Text, "\n") {
			if strings.TrimSpace(line) != "" {
				nonEmptyLines++
				c.scanNumbers(frag, pos, line)
				c.scanDates(frag, pos, line)
				c.scanMoney(frag, pos, line)
			}
			pos += len(line) + 1
		}
	}
	c.finalize()
	c.addNotes(nonEmptyLines)
	return c
}

// addNotes поясняет пустые карточки: без пометки «текст не извлечён» и
// «реквизитов в документе нет» выглядят в отчёте одинаково, а для приёмки P1.5
// это разные случаи — первый чинят в парсере, второй нормален.
func (c *Card) addNotes(nonEmptyLines int) {
	switch {
	case nonEmptyLines == 0:
		c.Notes = append(c.Notes, "текст документа пуст — карточка не заполнена, потому что извлекать было нечего")
	case !c.hasAnyKind(KindINN10, KindINN12, KindOGRN, KindOGRNIP):
		c.Notes = append(c.Notes, "ИНН/ОГРН не найдены — проверьте, есть ли они в документе")
	}
}

// hasAnyKind — встречается в карточке хотя бы один из перечисленных видов.
func (c *Card) hasAnyKind(kinds ...string) bool {
	for _, kind := range kinds {
		for _, it := range c.Items {
			if it.Kind == kind {
				return true
			}
		}
	}
	return false
}

// located дописывает значению адрес, по которому оно найдено. Фрагмент без
// адреса значение не помечает: оно остаётся в карточке, но не попадает в
// трассированные, и статистика карточки честно покажет дыру в парсере.
func located(frag trace.Fragment, pos, off int, it Item) Item {
	it.addRef(frag.RefAt(pos + off))
	return it
}

// scanNumbers ищет ИНН/КПП/ОГРН: последовательности из 9, 10, 12, 13 и 15
// цифр, окружённые не-цифрами (границы \b в Go RE2 достаточно).
func (c *Card) scanNumbers(frag trace.Fragment, pos int, line string) {
	find := func(re *regexp.Regexp, kind string, validate func(string) (bool, string)) {
		for _, m := range re.FindAllStringIndex(line, -1) {
			raw := line[m[0]:m[1]]
			ok, reason := validate(raw)
			c.add(located(frag, pos, m[0], Item{
				Kind: kind, Value: raw, Raw: raw, Valid: ok, Reason: reason,
				Label: labelBefore(line, m[0]),
			}))
		}
	}
	find(reOgrnip, KindOGRNIP, validateOGRN15)
	find(reOgrn, KindOGRN, validateOGRN13)
	find(reINN12, KindINN12, validateINN12)
	find(reINN10, KindINN10, validateINN10)
	find(reKPP, KindKPP, validateKPP)
}

var (
	reINN10  = regexp.MustCompile(`\b\d{10}\b`)
	reINN12  = regexp.MustCompile(`\b\d{12}\b`)
	reKPP    = regexp.MustCompile(`\b\d{9}\b`)
	reOgrn   = regexp.MustCompile(`\b\d{13}\b`)
	reOgrnip = regexp.MustCompile(`\b\d{15}\b`)
)

// reDate — дата в цифровом виде: ДД.ММ.ГГГГ (разделители точка или слэш).
var reDate = regexp.MustCompile(`\b(\d{2})[./](\d{2})[./](\d{4})\b`)

// reDateRU — дата с названием месяца в родительном падеже: «12 мая 2026».
var reDateRU = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\s+(\d{4})\b`)

var monthByName = map[string]int{
	"января": 1, "февраля": 2, "марта": 3, "апреля": 4, "мая": 5, "июня": 6,
	"июля": 7, "августа": 8, "сентября": 9, "октября": 10, "ноября": 11, "декабря": 12,
}

func (c *Card) scanDates(frag trace.Fragment, pos int, line string) {
	for _, m := range reDate.FindAllStringSubmatchIndex(line, -1) {
		day := atoi(line[m[2]:m[3]])
		month := atoi(line[m[4]:m[5]])
		year := atoi(line[m[6]:m[7]])
		ok, reason := validateDate(year, month, day)
		c.add(located(frag, pos, m[0], Item{
			Kind:   KindDate,
			Value:  fmt.Sprintf("%04d-%02d-%02d", year, month, day),
			Raw:    line[m[0]:m[1]],
			Valid:  ok,
			Reason: reason,
			Label:  labelBefore(line, m[0]),
		}))
	}
	for _, m := range reDateRU.FindAllStringSubmatchIndex(line, -1) {
		day := atoi(line[m[2]:m[3]])
		month := monthByName[strings.ToLower(line[m[4]:m[5]])]
		year := atoi(line[m[6]:m[7]])
		ok, reason := validateDate(year, month, day)
		c.add(located(frag, pos, m[0], Item{
			Kind:   KindDate,
			Value:  fmt.Sprintf("%04d-%02d-%02d", year, month, day),
			Raw:    line[m[0]:m[1]],
			Valid:  ok,
			Reason: reason,
			Label:  labelBefore(line, m[0]),
		}))
	}
}

// reMoney — денежное значение рядом с валютным маркером. Маркер обязателен:
// число без «руб./₽/USD…» в таблицах спецификаций — это цена единицы или
// количество без контекста, их разбирает сверка P2, а не P1.5.
//
// Три формы числа, которые встречаются в пакетах:
//
//	2,617,844.26  — выгрузки из бухгалтерских систем (запятая в разрядах);
//	1 000 000,50  — русская запись (разряды через пробел, копейки через запятую);
//	5000000       — число без разделителей.
//
// Разряды разделяются обычным пробелом, NBSP (U+00A0) или узкий NBSP
// (U+202F); RE2 не знает побегов «\u», поэтому они заданы как «\x{…}». Между
// группами разрядов ровно один разделитель и ровно три цифры, поэтому
// прокладка из нескольких пробелов между ячейками таблицы в число не входит:
// «1.00    438,819.67» даёт одно значение, а не склеенное.
var reMoney = regexp.MustCompile(`(?i)\b(\d{1,3}(?:[,.]\d{3})+(?:[,.]\d{1,2})?|\d{1,3}(?:[\x{00A0}\x{202F} ]\d{3})*(?:[,.]\d{1,2})?|\d+(?:[,.]\d{1,2})?)[\x{00A0}\x{202F} ]{0,3}(рублей|руб\.?|RUB|₽|USD|\$|EUR|€|долларов(?:а)?)`)

func (c *Card) scanMoney(frag trace.Fragment, pos int, line string) {
	for _, m := range reMoney.FindAllStringSubmatchIndex(line, -1) {
		// Число сразу после запятой или точки — хвост другого числа, а не сумма:
		// в «1,2345 руб.» иначе родился бы результат 2345 руб.
		if m[0] > 0 {
			if prev := line[m[0]-1]; prev == ',' || prev == '.' {
				continue
			}
		}
		mark := line[m[4]:m[5]]
		// «руб» внутри «зарубежом» или «рублях» валютой не считается: после
		// маркера не должно стоять буквы (точка, слэш, пробел — нормальный
		// конец сокращения).
		if r, _ := utf8.DecodeRuneInString(line[m[5]:]); r != utf8.RuneError && unicode.IsLetter(r) {
			continue
		}
		value, ok := parseAmount(line[m[2]:m[3]])
		if !ok {
			continue
		}
		c.add(located(frag, pos, m[0], Item{
			Kind:  KindMoney,
			Value: value + " " + normalizeCurrency(mark),
			Raw:   strings.TrimSpace(line[m[0]:m[1]]),
			Valid: true,
			Label: labelBefore(line, m[0]),
		}))
	}
}

// reAmountGroup — целая часть суммы: цифры, после которых могут идти группы
// ровно по 3 цифры через запятую или точку (американская/европейская
// группировка разрядов). Число без разделителей («5000000») тоже подходит.
var reAmountGroup = regexp.MustCompile(`^\d+(?:[,.]\d{3})*$`)

// moneySpaces убирает пробельные разделители разрядов: к разбору остаются
// только цифры, запятые и точки.
var moneySpaces = strings.NewReplacer(" ", "", "\u00A0", "", "\u202F", "", "\t", "")

// parseAmount сводит денежное число к виду «1234.56» (или «1234», если
// дробной части нет). Правила выбора десятичного разделителя:
//
//   - последний разделитель — десятичный, если после него 1-2 цифры (копейки);
//   - после него ровно 3 цифры — это группировка разрядов («2,617,844»);
//     разделители убираются целиком;
//   - всё остальное («1,2345», «1.2.3») суммой не считается.
//
// Российские документы пишут копейки через запятую («5 000,00»), а выгрузки из
// бухгалтерских систем — через точку и с запятой в разрядах («5,000.00»): оба
// варианта дают одно и то же нормализованное значение.
func parseAmount(raw string) (string, bool) {
	s := moneySpaces.Replace(raw)
	if !allDigitsAndSeparators(s) {
		return "", false
	}
	last := strings.LastIndexAny(s, ",.")
	if last < 0 {
		return s, allDigits(s)
	}
	head, tail := s[:last], s[last+1:]
	if !allDigits(tail) || tail == "" {
		return "", false
	}
	switch {
	case len(tail) <= 2:
		if !reAmountGroup.MatchString(head) {
			return "", false
		}
		return trimSeparators(head) + "." + tail, true
	case len(tail) == 3:
		if !reAmountGroup.MatchString(head) {
			return "", false
		}
		return trimSeparators(head) + tail, true
	default:
		return "", false
	}
}

func trimSeparators(s string) string {
	return strings.NewReplacer(",", "", ".", "").Replace(s)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func allDigitsAndSeparators(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && c != ',' && c != '.' {
			return false
		}
	}
	return true
}

// normalizeCurrency сводит маркеры к коду валюты.
func normalizeCurrency(mark string) string {
	mark = strings.ToLower(strings.TrimSpace(mark))
	switch {
	case strings.HasPrefix(mark, "руб"), mark == "₽", mark == "rub":
		return "RUB"
	case mark == "usd", mark == "$", strings.HasPrefix(mark, "доллар"):
		return "USD"
	case mark == "eur", mark == "€":
		return "EUR"
	default:
		return strings.ToUpper(mark)
	}
}

// labelBefore — метка вида «ИНН»: ближайшее ключевое слово не далее 60
// символов перед найденным значением.
func labelBefore(line string, pos int) string {
	from := pos - 60
	if from < 0 {
		from = 0
	}
	head := strings.ToUpper(line[from:pos])
	best, bestPos := "", -1
	for _, w := range labelWords {
		if i := strings.LastIndex(head, strings.ToUpper(w)); i > bestPos {
			best, bestPos = w, i
		}
	}
	return best
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
