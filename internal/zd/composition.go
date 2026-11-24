package zd

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"tenderguy/internal/parser"
	"tenderguy/internal/trace"
)

// Извлечение требуемого состава пакета из таблицы «Структура заявки» (P2.8).
// Таблица узнаётся по ячейке заголовка «Наименование файла»; колонки
// определяются по заголовку, а не по индексам: в реальной ЗД ЭФ строка-
// подкаталог может нести текст в колонке 0 или 1 и иметь меньше ячеек, чем
// колонок в сетке, поэтому обращение к несуществующей колонке недопустимо.

// Состав пункта повторяет схему раздела composition файла правил (P2.4):
// id, title, required и match{by:"filename", pattern} — их печатает
// --emit composition, и их принимает rules.LoadConfig без изменений.
// Остальные поля (имя файла, форматы, условие, группа, адрес) — данные для
// человека и будущих шагов.

// Маркеры заголовка таблицы состава (сравниваются по нормализованному тексту).
const (
	headerFileColumn  = "наименование файла"
	headerTitleColumn = "наименование документа"
	headerFormatWords = "формат"
	subcategoryMark   = "подкаталог"
)

// optionalMarkers — маркеры необязательности/условности предоставления
// документа. Найденный маркер переводит пункт в required=false и сохраняется
// дословно в поле Condition.
var optionalMarkers = []string{
	"только в случае",
	"не требуется предоставлять",
	"не является обязательной",
	"является рекомендуемой",
	"по мнению участника",
}

// CompositionItem — один пункт требуемого состава пакета.
type CompositionItem struct {
	// ID — «zd-» + номер строки таблицы с ведущим нулём: стабилен и уникален
	// в пределах документа, на него ссылается trace.Ref.Item замечаний P2.4.
	ID string `json:"id"`
	// Title — наименование требуемого документа: колонка 2 с вырезанной
	// ссылкой на раздел с формами и без условия предоставления.
	Title string `json:"title"`
	// Filename — требуемое имя файла в заявке без кавычек «ёлочек».
	Filename string `json:"filename"`
	// Formats — допустимые форматы файла из колонки требований к формату.
	Formats []string `json:"formats,omitempty"`
	// Pattern — glob сопоставления файлов пакета: реальные файлы называются
	// «1Опись.docx», «23Анкета.docx», то есть имя файла — подстрока имени.
	Pattern string `json:"pattern"`
	// Required — признак обязательности; false, когда в описании пункта
	// найден маркер условности (Condition).
	Required bool `json:"required"`
	// Condition — дословный текст условия предоставления.
	Condition string `json:"condition,omitempty"`
	// Group — подкаталог тома заявки, к которому относится пункт.
	Group string `json:"group,omitempty"`
	// Ref — адрес строки таблицы в документе; Traced() обязан быть истинным.
	Ref trace.Ref `json:"ref"`
}

// CompositionResult — итог извлечения требуемого состава.
type CompositionResult struct {
	Status string            `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Items  []CompositionItem `json:"items,omitempty"`
	// Candidates — адреса таблиц-кандидатов при not_found: человек ищет
	// нужную таблицу глазами, а не гадает, где она.
	Candidates []trace.Ref `json:"candidates,omitempty"`
}

// extractComposition находит таблицу состава и разбирает её строки.
func extractComposition(x *extractor) CompositionResult {
	for _, b := range x.doc.Blocks {
		if b.Table == nil {
			continue
		}
		cols, headerRow, ok := compositionColumns(b.Table)
		if !ok {
			continue
		}
		if cols.title < 0 {
			return CompositionResult{
				Status: StatusNotFound,
				Reason: fmt.Sprintf("таблица состава найдена (блок %d), но в её заголовке нет колонки «Наименование документа» — наименования пунктов не извлечь", b.Index),
			}
		}
		items := x.compositionItems(b, cols, headerRow)
		if len(items) == 0 {
			return CompositionResult{
				Status: StatusPartial,
				Reason: fmt.Sprintf("таблица состава найдена (блок %d), но ни одна строка не несёт имени файла", b.Index),
			}
		}
		return CompositionResult{Status: StatusFull, Items: items}
	}
	return CompositionResult{
		Status:     StatusNotFound,
		Reason:     "таблица с ячейкой заголовка «Наименование файла» не найдена",
		Candidates: x.tableCandidates(),
	}
}

// compColumns — номера колонок таблицы состава, найденные по заголовку.
type compColumns struct {
	file   int
	title  int
	format int
}

// compositionColumns ищет строку-заголовок с ячейкой «Наименование файла» и
// определяет по ней колонки. Возвращает номер строки-заголовка (row.Index).
func compositionColumns(t *parser.Table) (compColumns, int, bool) {
	for _, row := range t.Rows {
		cols := compColumns{file: -1, title: -1, format: -1}
		for i, c := range row.Cells {
			n := normText(c.Text)
			switch {
			case cols.file < 0 && strings.Contains(n, headerFileColumn):
				cols.file = i
			case cols.title < 0 && strings.Contains(n, headerTitleColumn):
				cols.title = i
			case cols.format < 0 && strings.Contains(n, headerFormatWords):
				cols.format = i
			}
		}
		if cols.file >= 0 {
			return cols, row.Index, true
		}
	}
	return compColumns{}, 0, false
}

// tableCandidates — адреса всех таблиц документа: список кандидатов для
// not_found, чтобы человек нашёл нужную таблицу глазами.
func (x *extractor) tableCandidates() []trace.Ref {
	var out []trace.Ref
	for _, b := range x.doc.Blocks {
		if b.Table == nil {
			continue
		}
		out = append(out, x.ref(b.Index, b.Page, ""))
	}
	return out
}

// compositionItems разбирает строки таблицы состава ниже заголовка.
// Строка-подкаталог (текст содержит «Подкаталог» при пустом имени файла)
// задаёт текущую группу; строка с непустым именем файла — пункт.
func (x *extractor) compositionItems(b parser.Block, cols compColumns, headerRow int) []CompositionItem {
	var items []CompositionItem
	group := ""
	for _, row := range b.Table.Rows {
		if row.Index <= headerRow {
			continue
		}
		filename := stripQuotes(cellText(row, cols.file))
		if filename == "" {
			if name, ok := subcategoryName(row); ok {
				group = name
			}
			continue
		}
		page := b.Page
		if row.Page > 0 {
			page = row.Page
		}
		cell := cellAddr("", row.Index, cols.file+1)
		titleCellText := cellText(row, cols.title)
		title, condition, resolved := splitTitleCell(titleCellText)
		// Разрешённые упоминания инструкций учитываются по ячейке с
		// наименованием (колонка title): именно из её текста вырезана ссылка.
		if cols.title >= 0 {
			x.resolve(cellKey(b.Index, cellAddr("", row.Index, cols.title+1)), resolved)
		}
		if title == "" {
			title = filename
		}
		items = append(items, CompositionItem{
			ID:        fmt.Sprintf("zd-%02d", row.Index),
			Title:     title,
			Filename:  filename,
			Formats:   splitFormats(cellText(row, cols.format)),
			Pattern:   "*" + escapeGlobLiteral(filename) + "*",
			Required:  condition == "",
			Condition: condition,
			Group:     group,
			Ref:       x.ref(b.Index, page, cell),
		})
	}
	return items
}

// cellText — текст ячейки по номеру колонки; обращение к несуществующей
// колонке недопустимо (в строке-подкаталоге ячеек меньше, чем колонок).
func cellText(row parser.Row, col int) string {
	if col < 0 || col >= len(row.Cells) {
		return ""
	}
	return row.Cells[col].Text
}

// escapeGlobLiteral экранирует glob-метасимволы имени файла для паттерна
// сопоставления. filepath.Match не поддерживает обратное экранирование
// метасимволов, поэтому каждый из них выражается символьной группой из
// одного символа: «[» → «[[]», «*» → «[*]», «?» → «[?]». Закрывающая скобка
// «]» намеренно не экранируется: вне символьной группы path.Match трактует её
// как литерал, а вариант «[]]» невозможен — первая «]» сразу после «[» входит
// в группу как литерал, и паттерн становится синтаксически неверным.
func escapeGlobLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '[', '*', '?':
			b.WriteRune('[')
			b.WriteRune(r)
			b.WriteRune(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripQuotes снимает кавычки «ёлочки» (и прямые кавычки) по краям значения:
// требуемое имя файла задано в ЗД как «Опись».
func stripQuotes(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "«»\""))
}

// subcategoryName — имя подкаталога из строки-подкаталога: текст любой
// ячейки содержит «Подкаталог», имя берётся из «ёлочек», иначе — весь текст.
func subcategoryName(row parser.Row) (string, bool) {
	for _, c := range row.Cells {
		n := normText(c.Text)
		if !strings.Contains(n, subcategoryMark) {
			continue
		}
		const openQuote, closeQuote = "«", "»"
		if start := strings.Index(c.Text, openQuote); start >= 0 {
			inner := c.Text[start+len(openQuote):]
			if end := strings.Index(inner, closeQuote); end >= 0 {
				if name := strings.TrimSpace(inner[:end]); name != "" {
					return name, true
				}
			}
		}
		return strings.TrimSpace(c.Text), true
	}
	return "", false
}

// splitTitleCell разбирает текст колонки 2 на наименование, условие
// предоставления и ссылку на раздел с формами. Условие в реальных ЗД
// заключено в скобки внутри предложения с наименованием
// («Анкета (предоставляется … только в случае …)»), поэтому сначала
// вырезаются скобочные группы с маркером условности — они попадают в
// condition дословно. Затем текст делится на предложения (см.
// splitSentences): предложение-ссылка на раздел с формами вырезается, а её
// упоминания инструкций считаются разрешёнными; если ссылка «прилипла» к
// наименованию без точки и перевода строки, от фрагмента отрезается только
// суффикс-ссылка, а префикс-наименование сохраняется дословно (см.
// cutSectionRef). Предложение с маркером вне скобок тоже уходит в condition.
// Остальные предложения сохраняются в title дословно, вместе с их
// собственными точками: текст не режется по каждой точке и не склеивается
// заново, поэтому внутренняя нумерация («пункта 5.2.1») не рвётся и лишняя
// точка в конце не появляется. Пробелы и переводы строк внутри title
// схлопываются в один пробел намеренно — так же, как это делает
// normalizeFileName в классе «composition» файла правил, иначе подстрочный
// поиск наименования в описи пакета не сработает.
// Возвращает title, condition и число разрешённых упоминаний instructionNeedle.
func splitTitleCell(text string) (title, condition string, resolved int) {
	rest, conditions := extractParenConditions(text)
	var keep []string
	for _, s := range splitSentences(rest) {
		frag := strings.TrimSpace(s.text)
		if frag == "" {
			continue
		}
		if s.dot {
			frag += "."
		}
		n := normText(frag)
		if isSectionRefSentence(n) {
			// Разрешёнными считаются упоминания только в отрезанной
			// ссылке: префикс-наименование остаётся видимым в title.
			prefix, suffix := cutSectionRef(frag)
			resolved += countInstr(suffix)
			if prefix == "" {
				continue
			}
			frag = prefix
			n = normText(frag)
		}
		if hasOptionalMarker(n) {
			conditions = append(conditions, frag)
			continue
		}
		keep = append(keep, frag)
	}
	title = strings.Join(strings.Fields(strings.Join(keep, " ")), " ")
	condition = strings.Join(conditions, " ")
	return title, condition, resolved
}

// cutSectionRef отделяет ссылку на раздел с формами, «прилипшую» к
// наименованию внутри одного фрагмента (без точки и перевода строки между
// ними). Возвращает префикс-наименование до начала ссылки и суффикс-ссылку.
// Если ссылка занимает весь фрагмент, префикс пуст; если начало ссылки не
// находится однозначно, фрагмент считается ссылкой целиком — как до
// появления обрезки, содержимое при этом не теряется молча: вызывающий
// считает разрешённые упоминания по суффиксу.
func cutSectionRef(frag string) (prefix, suffix string) {
	norm, offsets := normTextMapped(frag)
	i := sectionRefStart(norm)
	if i < 0 {
		return "", frag
	}
	pos := offsets[i]
	return strings.TrimSpace(frag[:pos]), frag[pos:]
}

// sectionRefStart — позиция в нормализованном тексте, с которой начинается
// ссылка на раздел с формами, либо -1. Кандидат на начало ссылки — вхождение
// узкого маркера («типовая форма», «типовые формы», «инструкция по
// заполнению», «инструкции по заполнению»), хвост от которого сам является
// ссылкой на раздел (isSectionRefSentence). Грубый instructionNeedle
// («инструкц») здесь намеренно не используется: он подходит для подсчёта
// упоминаний, но как опорная точка разреза резал бы наименование, само
// содержащее «инструкц» («Сведения об инструкциях по эксплуатации…»), если
// ссылка «прилипла» к нему без точки. Проверка хвоста не даёт принять за
// начало ссылки любое упоминание инструкций вне раздела с формами:
// «в соответствии с Инструкциями и регламентом площадки» не режет
// наименование. Возвращается самая ранняя подходящая позиция.
func sectionRefStart(norm string) int {
	best := -1
	for _, m := range []string{"типовая форма", "типовые формы", "инструкция по заполнению", "инструкции по заполнению"} {
		for off := 0; off+len(m) <= len(norm); {
			i := strings.Index(norm[off:], m)
			if i < 0 {
				break
			}
			pos := off + i
			if isSectionRefSentence(norm[pos:]) && (best < 0 || pos < best) {
				best = pos
			}
			off = pos + len(m)
		}
	}
	return best
}

// normTextMapped нормализует текст как normText (нижний регистр, серии
// пробельных символов схлопываются в один пробел) и возвращает для каждого
// байта результата смещение соответствующего символа в исходной строке.
// Отображение позволяет перенести позицию, найденную в нормализованном
// тексте, обратно на исходный текст без потери его собственных знаков.
func normTextMapped(s string) (string, []int) {
	var b strings.Builder
	var offsets []int
	pendingSpace := false
	for i, r := range s {
		if unicode.IsSpace(r) {
			if b.Len() > 0 {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			offsets = append(offsets, i)
			pendingSpace = false
		}
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], unicode.ToLower(r))
		for k := 0; k < n; k++ {
			offsets = append(offsets, i)
		}
		b.Write(buf[:n])
	}
	return b.String(), offsets
}

// splitSentence — предложение текста ячейки: фрагмент и признак того, что
// в исходном тексте он заканчивался точкой.
type splitSentence struct {
	text string
	dot  bool
}

// splitSentences делит текст на предложения. Граница предложения — перевод
// строки (в ячейках реальной ЗД наименование и ссылка на раздел с формами —
// разные абзацы без точки между ними) либо точка, после которой следует
// пробел, табуляция или конец текста. Точки внутри чисел («5.2.1»), внутри
// скобок и внутри кавычек-«ёлочек» границей не считаются: внутренняя
// нумерация и цитируемые названия разделов не должны рвать наименование.
func splitSentences(text string) []splitSentence {
	var out []splitSentence
	start, quote, paren := 0, 0, 0
	for i, r := range text {
		switch r {
		case '«':
			quote++
		case '»':
			if quote > 0 {
				quote--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case '\n':
			out = append(out, splitSentence{text: text[start:i]})
			start = i + len("\n")
		case '.':
			if quote > 0 || paren > 0 {
				break
			}
			if i+1 < len(text) && text[i+1] != ' ' && text[i+1] != '\t' {
				break
			}
			out = append(out, splitSentence{text: text[start:i], dot: true})
			start = i + len(".")
		}
	}
	if start < len(text) {
		out = append(out, splitSentence{text: text[start:]})
	}
	return out
}

// extractParenConditions вырезает из текста скобочные группы, содержащие
// маркер необязательности, и возвращает остаток текста и дословные условия.
// Скобочные группы без маркера остаются в тексте.
func extractParenConditions(text string) (string, []string) {
	var sb strings.Builder
	var conditions []string
	for i := 0; i < len(text); {
		if text[i] != '(' {
			sb.WriteByte(text[i])
			i++
			continue
		}
		depth, j := 0, i
		for ; j < len(text); j++ {
			switch text[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if j >= len(text) {
			// Незакрытая скобка — остаётся в тексте как есть.
			sb.WriteByte(text[i])
			i++
			continue
		}
		group := text[i : j+1]
		if hasOptionalMarker(normText(group)) {
			conditions = append(conditions, group)
		} else {
			sb.WriteString(group)
		}
		i = j + 1
	}
	return sb.String(), conditions
}

// hasOptionalMarker — в нормализованном тексте есть маркер необязательности.
func hasOptionalMarker(norm string) bool {
	for _, m := range optionalMarkers {
		if strings.Contains(norm, m) {
			return true
		}
	}
	return false
}

// isSectionRefSentence — предложение нормализованного текста является ссылкой
// на раздел закупочной документации с образцами форм. В реальной ЗД ЭФ ссылка
// встречается в двух вариантах: «Типовая форма и инструкция по заполнению
// приведены в Разделе 9 …» и «Типовая форма приведена в Разделе 9 …» — второй
// слова «инструкция» не содержит, но в наименовании пункта ему не место.
func isSectionRefSentence(norm string) bool {
	if !strings.Contains(norm, "раздел") {
		return false
	}
	return strings.Contains(norm, instructionNeedle) || strings.Contains(norm, "типовая форма") ||
		strings.Contains(norm, "типовые формы")
}

// splitFormats — список форматов из колонки требований: «Doc, Pdf» → [Doc Pdf].
func splitFormats(text string) []string {
	var out []string
	for _, part := range strings.Split(strings.ReplaceAll(text, "\n", " "), ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
