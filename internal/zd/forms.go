package zd

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"tenderguy/internal/trace"
)

// Извлечение пар «форма ↔ инструкция по заполнению» (P2.8). Реальный раздел
// образцов форм устроен как «группа (форма N) → один или несколько вариантов
// формы с маркерами «начало формы»/«конец формы» → блоки инструкций»: один
// блок инструкций может обслуживать несколько идущих подряд тел форм, а
// заголовок блока может нести суффикс с названием формы («Инструкции по
// заполнению Письма-согласия …»). Ассоциация обязана быть видимой:
//
//   - found — блок инструкций принадлежит одному телу формы;
//   - shared — один блок на несколько тел, все они видны, плюс Issue:
//     молчаливый выбор одной формы запрещён (философия P2.4);
//   - external — примечание формы явно ссылается на инструкцию вне формы
//     (например, в публикуемом XLSX-файле), текст ссылки сохраняется дословно;
//   - not_found — после тела форм до конца раздела нет ни одного блока
//     инструкций (эскалация A9).
//
// Заголовок блока инструкций определяется по ПРЕФИКСУ «Инструкции по
// заполнению»/«Инструкция по заполнению» (регистронезависимо): точное
// равенство в реальных ЗД не работает из-за суффиксов. Номер формы
// наследуется только из заголовка группы «<название> (форма N)» и только
// если название тела «эхом» повторяет название группы — номер не
// выдумывается. Отклонения структуры (незакрытое «конец формы», пустое тело)
// остаются видимыми: статус partial и Issues с адресами.

// Маркеры структуры раздела с образцами форм (сравниваются по
// нормализованному тексту абзаца).
const (
	markerFormStart = "начало формы"
	markerFormEnd   = "конец формы"
	// Префиксы заголовка блока инструкций: суффикс заголовка (название
	// формы) сохраняется в Form.InstructionTitle.
	instrPrefixPlural   = "инструкции по заполнению"
	instrPrefixSingular = "инструкция по заполнению"
)

var (
	// groupHeadingRe — заголовок группы форм: нормализованный текст целиком
	// заканчивается номером «(форма N)». Именные ссылки вида «… (форма 2) —
	// на ____ л.» внутри тел форм заголовками группы не являются.
	groupHeadingRe = regexp.MustCompile(`^(.*?)\s*\(форма\s*(\d+)\)$`)
	// groupTailRe — хвост «(форма N)» в исходном тексте заголовка группы.
	groupTailRe = regexp.MustCompile(`(?i)\s*\(форма\s*\d+\)\s*$`)
	// instrHeadingRe — заголовок блока инструкций в тексте с нормализованными
	// пробелами; группа 1 — суффикс (название формы), может быть пустым.
	instrHeadingRe = regexp.MustCompile(`(?i)^инструкци(?:и|я)\s+по\s+заполнению[\s—–-]*(.*)$`)
	// externalRefRe — явная ссылка на инструкцию в кавычках-«ёлочках»:
	// признак того, что инструкция лежит вне формы (в публикуемом файле).
	externalRefRe = regexp.MustCompile(`(?i)«[^»]*инструкци[^»]*»`)
	// wordSplitRe — разбиение нормализованного текста на слова для сравнения
	// заголовка группы и названия тела формы.
	wordSplitRe = regexp.MustCompile(`[^\p{L}]+`)
)

// Form — пара «форма ↔ инструкция по заполнению».
type Form struct {
	// GroupNumber — номер формы из заголовка группы «<название> (форма N)».
	// Пусто, если тело не принадлежит нумерованной группе: номер не
	// выдумывается.
	GroupNumber string `json:"group_number,omitempty"`
	// Variant — порядковый номер тела формы внутри группы (1, 2, …).
	Variant int       `json:"variant,omitempty"`
	Title   string    `json:"title"`
	Ref     trace.Ref `json:"ref"`
	// Closed — найден маркер «конец формы».
	Closed bool `json:"closed"`
	// EmptyBody — между «начало формы» и «конец формы» нет ни одного абзаца
	// или таблицы: содержимое формы находится во внешнем шаблоне (виден в Issue).
	EmptyBody bool `json:"empty_body,omitempty"`
	// InstructionStatus — found, shared, external или not_found: форма без
	// инструкции остаётся видимой, а не теряется.
	InstructionStatus string `json:"instruction_status"`
	// InstructionTitle — суффикс заголовка блока инструкций (название формы,
	// которому блок адресован); пусто у заголовка без суффикса.
	InstructionTitle string `json:"instruction_title,omitempty"`
	// InstructionItems — пункты инструкции по заполнению.
	InstructionItems []string `json:"instruction_items,omitempty"`
	// ExternalNote — дословное примечание со ссылкой на инструкцию вне формы
	// (статус external).
	ExternalNote string `json:"external_note,omitempty"`
}

// FormsResult — итог извлечения пар «форма ↔ инструкция».
type FormsResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Section — раздел, в котором искали пары; пусто — поиск по всему
	// документу (раздел с образцами форм не определён).
	Section string  `json:"section,omitempty"`
	Forms   []Form  `json:"forms,omitempty"`
	Issues  []Issue `json:"issues,omitempty"`
}

// formPara — блок в зоне поиска пар: позиция в срезе, номер блока, текст.
// Таблицы включаются в срез (text пуст): они замыкают тело формы и делают
// его непустым, но заголовком или пунктом инструкции быть не могут.
type formPara struct {
	pos   int
	idx   int
	page  int
	text  string
	norm  string
	table bool
}

// formBody — одно тело формы между «начало формы» и «конец формы».
type formBody struct {
	startPos     int // позиция «начало формы»
	endPos       int // позиция «конец формы»; -1, если тело не закрыто
	limit        int // конец зоны тела (исключительно): endPos+1 или следующее «начало формы»
	titlePos     int // позиция заголовка; -1, если заголовка нет
	title        string
	notePos      []int  // примечания в квадратных скобках между заголовком и «начало формы»
	groupNumber  string // номер из заголовка группы; пусто — тело вне нумерованной группы
	variant      int    // порядковый номер тела внутри группы
	closed       bool
	empty        bool
	external     bool
	extNote      string
	extNotePos   int
	instrIdx     int // индекс связанного блока инструкций в blocks; -1
	status       string
	titleIsGroup bool // заголовок тела — сам заголовок группы
}

// groupHead — заголовок группы форм «<название> (форма N)».
type groupHead struct {
	pos    int
	name   string // нормализованное название без хвоста «(форма N)»
	number string
}

// instrBlock — блок инструкций: заголовок с суффиксом и пункты.
type instrBlock struct {
	pos     int
	suffix  string // суффикс заголовка (название формы), может быть пустым
	items   []string
	itemPos []int
}

// extractForms проходит по блокам раздела с образцами форм (или по всему
// документу, если раздел не определён) и собирает пары «форма ↔ инструкция».
func extractForms(x *extractor) FormsResult {
	paras, section := x.formParagraphs()
	starts := markerPositions(paras, markerFormStart)
	if len(starts) == 0 {
		where := "в документе"
		if section != "" {
			where = "в разделе «" + section + "»"
		}
		return FormsResult{
			Status:  StatusNotFound,
			Reason:  fmt.Sprintf("маркеры «%s» не найдены %s", markerFormStart, where),
			Section: section,
		}
	}

	bodies := collectBodies(paras, starts)
	inBody := bodyMask(paras, bodies)
	groupHeads := collectGroupHeads(paras, inBody)
	blocks := collectInstrBlocks(paras, inBody, bodies, groupHeads)

	assignTitles(paras, bodies, blocks, groupHeads)
	assignGroups(paras, bodies, groupHeads)

	res := FormsResult{Status: StatusFull, Section: section}
	associate(x, paras, bodies, blocks, groupHeads, &res.Issues)

	unclosed := 0
	for _, b := range bodies {
		form := x.buildForm(paras, b, blocks, &res.Issues)
		if !b.closed {
			unclosed++
		}
		res.Forms = append(res.Forms, form)
		x.resolveBody(paras, b)
	}
	for _, blk := range blocks {
		// Блок инструкций учитывается в разрешённых упоминаниях ОДИН раз,
		// даже если он приписан нескольким формам (shared): повторный resolve
		// тех же абзацев ограничен общим числом вхождений в countInstructions.
		x.resolvePara(paras[blk.pos])
		for _, p := range blk.itemPos {
			x.resolvePara(paras[p])
		}
	}
	if unclosed > 0 {
		res.Status = StatusPartial
		res.Reason = fmt.Sprintf("форм без закрывающего маркера «%s»: %d из %d — адреса в issues", markerFormEnd, unclosed, len(res.Forms))
	}
	return res
}

// formParagraphs — блоки зоны поиска (включая таблицы) и имя раздела. Зона:
// раздел, чей заголовок похож на «Образцы … форм …», иначе весь документ.
func (x *extractor) formParagraphs() ([]formPara, string) {
	start, end := 0, len(x.doc.Blocks)+1
	section := ""
	for _, s := range x.sections {
		n := normText(s.Title)
		if strings.Contains(n, "форм") && strings.Contains(n, "образц") {
			start, end, section = s.StartPara, s.EndPara, s.Title
			break
		}
	}
	var out []formPara
	for _, b := range x.doc.Blocks {
		if b.Index < start || b.Index >= end {
			continue
		}
		out = append(out, formPara{
			pos:   len(out),
			idx:   b.Index,
			page:  b.Page,
			text:  b.Text,
			norm:  normText(b.Text),
			table: b.Table != nil,
		})
	}
	return out, section
}

// markerPositions — позиции блоков, чей нормализованный текст равен маркеру.
func markerPositions(paras []formPara, markers ...string) []int {
	var out []int
	for _, p := range paras {
		for _, m := range markers {
			if p.norm == m {
				out = append(out, p.pos)
				break
			}
		}
	}
	return out
}

// collectBodies собирает тела форм: зона каждого тела — до «конец формы», а
// если маркера нет — до следующего «начало формы» (незакрытое тело).
func collectBodies(paras []formPara, starts []int) []*formBody {
	var out []*formBody
	for i, s := range starts {
		next := len(paras)
		if i+1 < len(starts) {
			next = starts[i+1]
		}
		b := &formBody{startPos: s, endPos: -1, limit: next, instrIdx: -1, status: InstructionNotFound}
		if ends := markerPositions(paras[s+1:next], markerFormEnd); len(ends) > 0 {
			b.endPos = ends[0]
			b.closed = true
			b.limit = ends[0] + 1
		}
		b.empty = bodyEmpty(paras, b)
		out = append(out, b)
	}
	return out
}

// bodyEmpty — между «начало формы» и «конец формы» (или следующим «начало
// формы» у незакрытого тела) нет ни одного непустого абзаца или таблицы.
func bodyEmpty(paras []formPara, b *formBody) bool {
	to := b.limit
	if b.closed {
		to = b.endPos
	}
	for pos := b.startPos + 1; pos < to; pos++ {
		if paras[pos].table || strings.TrimSpace(paras[pos].text) != "" {
			return false
		}
	}
	return true
}

// bodyMask помечает блоки внутри тел форм: заголовки групп и блоки инструкций
// ищутся только снаружи тел — списки вида «… (форма 2) — на ___ л.» внутри
// письма о подаче оферты заголовками группы не являются.
func bodyMask(paras []formPara, bodies []*formBody) []bool {
	mask := make([]bool, len(paras))
	for _, b := range bodies {
		for pos := b.startPos; pos < b.limit; pos++ {
			mask[pos] = true
		}
	}
	return mask
}

// collectGroupHeads — заголовки групп «<название> (форма N)» вне тел форм.
func collectGroupHeads(paras []formPara, inBody []bool) []groupHead {
	var out []groupHead
	for _, p := range paras {
		if inBody[p.pos] || p.table {
			continue
		}
		m := groupHeadingRe.FindStringSubmatch(p.norm)
		if m == nil {
			continue
		}
		out = append(out, groupHead{pos: p.pos, name: strings.TrimSpace(m[1]), number: m[2]})
	}
	return out
}

// collectInstrBlocks — блоки инструкций: заголовок по префиксу «Инструкции по
// заполнению» вне тел форм, пункты — до следующего «начало формы», заголовка
// группы или другого блока инструкций. Если граница окна — «начало формы»,
// из конца пунктов вычищаются заголовок следующей формы и её примечания.
func collectInstrBlocks(paras []formPara, inBody []bool, bodies []*formBody, groupHeads []groupHead) []instrBlock {
	var heads []int
	for _, p := range paras {
		if !inBody[p.pos] && !p.table && isInstrHeading(p.norm) {
			heads = append(heads, p.pos)
		}
	}
	blocks := make([]instrBlock, 0, len(heads))
	for hi, h := range heads {
		boundary := len(paras)
		boundaryIsStart := false
		for _, b := range bodies {
			if b.startPos > h && b.startPos < boundary {
				boundary, boundaryIsStart = b.startPos, true
			}
		}
		for _, g := range groupHeads {
			if g.pos > h && g.pos < boundary {
				boundary, boundaryIsStart = g.pos, false
			}
		}
		if hi+1 < len(heads) && heads[hi+1] < boundary {
			boundary, boundaryIsStart = heads[hi+1], false
		}

		blk := instrBlock{pos: h, suffix: instrSuffix(paras[h].text)}
		var items []int
		for pos := h + 1; pos < boundary; pos++ {
			p := paras[pos]
			if p.table || strings.TrimSpace(p.text) == "" || isFormMarker(p.norm) {
				continue
			}
			items = append(items, pos)
		}
		if boundaryIsStart && len(items) > 0 {
			// Хвост окна — заголовок следующей формы и её примечания: они не
			// пункты инструкции.
			for len(items) > 0 && isNotePara(paras[items[len(items)-1]]) {
				items = items[:len(items)-1]
			}
			if len(items) > 0 {
				items = items[:len(items)-1]
			}
		}
		for _, pos := range items {
			blk.items = append(blk.items, strings.TrimSpace(paras[pos].text))
			blk.itemPos = append(blk.itemPos, pos)
		}
		blocks = append(blocks, blk)
	}
	return blocks
}

// isInstrHeading — нормализованный текст начинается с префикса заголовка
// блока инструкций. Точное равенство больше не признак: у заголовка бывает
// суффикс с названием формы («Инструкции по заполнению Письма-согласия …»).
func isInstrHeading(norm string) bool {
	return strings.HasPrefix(norm, instrPrefixPlural) || strings.HasPrefix(norm, instrPrefixSingular)
}

// instrSuffix — суффикс заголовка блока инструкций в тексте с нормализованными
// пробелами (для «Инструкции по заполнению Письма-согласия …» — «Письма-согласия …»).
func instrSuffix(text string) string {
	m := instrHeadingRe.FindStringSubmatch(strings.Join(strings.Fields(text), " "))
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// isNotePara — абзац ЦЕЛИКОМ заключён в квадратные скобки: это примечание
// («[заполняется с указанием названия лота…]»), а не название формы.
func isNotePara(p formPara) bool {
	t := strings.TrimSpace(p.text)
	return len(t) >= 2 && strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
}

// assignTitles заполняет заголовки и примечания тел форм. Заголовок —
// последний не-примечание абзац перед «начало формы»; если такого нет —
// название группы без хвоста «(форма N)». Поиск ограничен снизу концом
// предыдущего тела (или его «начало формы», если тело не закрыто) и
// заголовком предыдущего блока инструкций.
func assignTitles(paras []formPara, bodies []*formBody, blocks []instrBlock, groupHeads []groupHead) {
	groupAt := make(map[int]bool, len(groupHeads))
	for _, g := range groupHeads {
		groupAt[g.pos] = true
	}
	for i, b := range bodies {
		lower := -1
		if i > 0 {
			prev := bodies[i-1]
			if prev.closed {
				lower = prev.endPos
			} else {
				lower = prev.startPos
			}
		}
		for _, blk := range blocks {
			if blk.pos > lower && blk.pos < b.startPos {
				lower = blk.pos
			}
		}
		var notes []int
		for pos := b.startPos - 1; pos > lower; pos-- {
			p := paras[pos]
			if p.table || strings.TrimSpace(p.text) == "" || isFormMarker(p.norm) {
				continue
			}
			if isInstrHeading(p.norm) {
				break
			}
			if isNotePara(p) {
				notes = append(notes, pos)
				continue
			}
			b.titlePos = pos
			if groupAt[pos] {
				b.titleIsGroup = true
				b.title = strings.TrimSpace(groupTailRe.ReplaceAllString(p.text, ""))
			} else {
				b.title = strings.TrimSpace(p.text)
			}
			break
		}
		for i, j := 0, len(notes)-1; i < j; i, j = i+1, j-1 {
			notes[i], notes[j] = notes[j], notes[i]
		}
		b.notePos = notes
		for _, n := range notes {
			if externalRefRe.MatchString(paras[n].text) {
				b.external = true
				b.extNote = strings.TrimSpace(paras[n].text)
				b.extNotePos = n
				break
			}
		}
	}
}

// assignGroups наследует номер группы: тело получает group_number только если
// ближайший preceding заголовок группы «эхом» повторяется в его названии —
// иначе номер не выдумывается. Варианты нумеруются по порядку внутри группы.
func assignGroups(paras []formPara, bodies []*formBody, groupHeads []groupHead) {
	variants := make(map[int]int, len(groupHeads))
	for _, b := range bodies {
		gi := -1
		for i, g := range groupHeads {
			if g.pos >= b.startPos {
				break
			}
			gi = i
		}
		if gi < 0 {
			continue
		}
		g := groupHeads[gi]
		if !b.titleIsGroup && !echoMatch(g.name, normText(b.title)) {
			continue
		}
		variants[g.pos]++
		b.groupNumber = g.number
		b.variant = variants[g.pos]
	}
}

// echoMatch — название тела «эхом» повторяет заголовок группы: минимум два
// слова заголовка (длиной ≥ 5 букв) находятся в названии по 5-буквенной
// приставке, что учитывает русское словоизменение («письмо» → «письма»,
// «субподрядчиков» → «субпоставщиков»). Одиночное совпадение наследованием не
// считается: у формы без собственного заголовка группы номера нет.
func echoMatch(groupNorm, titleNorm string) bool {
	tWords := wordsOf(titleNorm)
	eligible, matched := 0, 0
	for _, w := range wordsOf(groupNorm) {
		if utf8.RuneCountInString(w) < 5 {
			continue
		}
		eligible++
		for _, t := range tWords {
			if commonPrefixRunes(w, t) >= 5 {
				matched++
				break
			}
		}
	}
	switch {
	case eligible == 0:
		return false
	case eligible == 1:
		return matched == 1
	default:
		return matched >= 2
	}
}

// wordsOf — слова нормализованного текста (буквы, без пунктуации и цифр).
func wordsOf(norm string) []string {
	return wordSplitRe.Split(norm, -1)
}

// commonPrefixRunes — длина общей приставки двух строк в рунах.
func commonPrefixRunes(a, b string) int {
	n := 0
	ra, rb := []rune(a), []rune(b)
	for n < len(ra) && n < len(rb) && ra[n] == rb[n] {
		n++
	}
	return n
}

// associate связывает блоки инструкций с телами форм. Окно претендентов —
// закрытые не-external тела от предыдущего блока инструкций и последнего
// заголовка группы до текущего заголовка. Суффикс заголовка, совпадающий
// ровно с одним телом окна, решает ассоциацию единолично; иначе одно тело —
// found, несколько — shared с Issue на группу (неоднозначность видна,
// молчаливого выбора нет). Незакрытые тела в окна не входят: их границы
// неизвестны, и следующий за ними блок инструкций им не приписывается.
func associate(x *extractor, paras []formPara, bodies []*formBody, blocks []instrBlock, groupHeads []groupHead, issues *[]Issue) {
	for k := range blocks {
		blk := &blocks[k]
		windowStart := -1
		if k > 0 {
			windowStart = blocks[k-1].pos
		}
		for _, g := range groupHeads {
			if g.pos > windowStart && g.pos < blk.pos {
				windowStart = g.pos
			}
		}
		var cand []*formBody
		for _, b := range bodies {
			if b.startPos > windowStart && b.startPos < blk.pos && b.closed && !b.external {
				cand = append(cand, b)
			}
		}
		chosen := cand
		if blk.suffix != "" {
			var bySuffix []*formBody
			sn := normText(blk.suffix)
			for _, b := range cand {
				tn := normText(b.title)
				if tn != "" && (strings.Contains(tn, sn) || strings.Contains(sn, tn)) {
					bySuffix = append(bySuffix, b)
				}
			}
			if len(bySuffix) == 1 {
				chosen = bySuffix
			}
		}
		switch len(chosen) {
		case 0:
			// Блок не принадлежит ни одному телу окна (например, все тела —
			// external): пункты остаются разрешёнными упоминаниями.
		case 1:
			chosen[0].instrIdx = k
			chosen[0].status = InstructionFound
		default:
			names := make([]string, 0, len(chosen))
			for _, b := range chosen {
				b.instrIdx = k
				b.status = InstructionShared
				names = append(names, fmt.Sprintf("«%s» (абз. %d)", quoteLine(b.title), paras[formRefPos(b)].idx))
			}
			*issues = append(*issues, Issue{
				Reason: fmt.Sprintf("один блок инструкций на %d форм: %s — какая из них имеется в виду, решает специалист",
					len(chosen), strings.Join(names, ", ")),
				Ref: x.ref(paras[blk.pos].idx, paras[blk.pos].page, ""),
			})
		}
	}
}

// formRefPos — позиция абзаца-заголовка тела формы (или «начало формы», если
// заголовка нет): адрес пары и перечней в Issue.
func formRefPos(b *formBody) int {
	if b.titlePos >= 0 {
		return b.titlePos
	}
	return b.startPos
}

// buildForm собирает итоговую пару и регистрирует дефекты тела: незакрытый
// маркер, пустое тело, внешняя инструкция.
func (x *extractor) buildForm(paras []formPara, b *formBody, blocks []instrBlock, issues *[]Issue) Form {
	refPos := formRefPos(b)
	ref := x.ref(paras[refPos].idx, paras[refPos].page, "")
	name := formName(b.groupNumber, b.title)

	if !b.closed {
		*issues = append(*issues, Issue{
			Reason: fmt.Sprintf("форма %s: маркер «%s» не найден до следующего «%s»", name, markerFormEnd, markerFormStart),
			Ref:    ref,
		})
	}
	if b.empty {
		where := fmt.Sprintf("абз. %d", paras[b.startPos].idx)
		if b.closed {
			where = fmt.Sprintf("абз. %d–%d", paras[b.startPos].idx, paras[b.endPos].idx)
		}
		*issues = append(*issues, Issue{
			Reason: fmt.Sprintf("форма %s: пустое тело (%s) — между «%s» и «%s» нет ни абзацев, ни таблиц, содержимое формы во внешнем шаблоне",
				name, where, markerFormStart, markerFormEnd),
			Ref: x.ref(paras[b.startPos].idx, paras[b.startPos].page, ""),
		})
	}
	status := b.status
	if status == InstructionNotFound && b.external {
		status = InstructionExternal
		*issues = append(*issues, Issue{
			Reason: fmt.Sprintf("форма %s: инструкция по заполнению вне формы — %s", name, quoteLine(b.extNote)),
			Ref:    x.ref(paras[b.extNotePos].idx, paras[b.extNotePos].page, ""),
		})
	}

	form := Form{
		GroupNumber:       b.groupNumber,
		Variant:           b.variant,
		Title:             b.title,
		Ref:               ref,
		Closed:            b.closed,
		EmptyBody:         b.empty,
		InstructionStatus: status,
	}
	if b.instrIdx >= 0 {
		blk := blocks[b.instrIdx]
		form.InstructionTitle = blk.suffix
		form.InstructionItems = blk.items
	}
	if status == InstructionExternal {
		form.ExternalNote = b.extNote
	}
	return form
}

// resolveBody помечает абзацы тела формы (заголовок, примечания, маркеры и
// содержимое) как разрешённые упоминания инструкций.
func (x *extractor) resolveBody(paras []formPara, b *formBody) {
	if b.titlePos >= 0 {
		x.resolvePara(paras[b.titlePos])
	}
	for _, n := range b.notePos {
		x.resolvePara(paras[n])
	}
	for pos := b.startPos; pos < b.limit; pos++ {
		x.resolvePara(paras[pos])
	}
}

// resolvePara помечает вхождения instructionNeedle одного абзаца как
// разрешённые: он попал в пару «форма ↔ инструкция» и наружу не печатается.
func (x *extractor) resolvePara(p formPara) {
	x.resolve(paraKey(p.idx), countInstr(p.text))
}

// isFormMarker — нормализованный текст абзаца является маркером границ тела.
func isFormMarker(norm string) bool {
	return norm == markerFormStart || norm == markerFormEnd
}

// formName — человекочитаемое имя формы для текста причины.
func formName(number, title string) string {
	if number != "" {
		return "форма " + number
	}
	if title != "" {
		return "«" + quoteLine(title) + "»"
	}
	return "без заголовка"
}
