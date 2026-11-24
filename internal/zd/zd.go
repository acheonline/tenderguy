// Package zd извлекает из закупочной документации (ЗД ЭФ 223-ФЗ) три
// структуры, которые дальше потребляются как данные (задача P2.8):
//
//   - требуемый состав пакета — на вход класса правил «composition» (P2.4)
//     в той же схеме, что и конфиг, заданный вручную;
//   - пары «форма ↔ инструкция по заполнению» — машиночитаемый вход для P3;
//   - требования к участнику закупки.
//
// Адресация построена по «адрес-лестнице»: нижняя ступень, которая есть
// всегда, — номер блока документа (абзаца или таблицы, parser.Block.Index);
// страница добавляется в адрес только если пагинация .docx признана
// достоверной детектором (pagination.go), потому что w:lastRenderedPageBreak —
// свойство последней отрисовки Word и в реальных ЗД оно врёт; раздел попадает
// в trace.Ref.Item и печатается как «п. Раздел N «…»».
//
// Пустой успех запрещён: каждая структура несёт статус full/partial/not_found
// с причиной и адресами, а «не найдено» включает список кандидатов, чтобы
// человек мог найти нужное место глазами.
package zd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tenderguy/internal/parser"
	"tenderguy/internal/trace"
)

// Статусы извлечённой структуры. not_found — это не пустой массив, а статус
// с причиной и кандидатами: молча потерянная структура опаснее честного
// «не найдено».
const (
	StatusFull     = "full"
	StatusPartial  = "partial"
	StatusNotFound = "not_found"
)

// Статусы инструкции по заполнению у пары «форма ↔ инструкция». found —
// блок инструкций принадлежит одному телу формы; shared — один блок на
// несколько тел (все видны, плюс Issue: молчаливый выбор запрещён);
// external — инструкция вне формы по явной ссылке в примечании; not_found —
// штатный случай реальных ЗД (эскалация A9): форма без инструкции обязана
// быть видимой в отчёте, а не потерянной.
const (
	InstructionFound    = "found"
	InstructionShared   = "shared"
	InstructionExternal = "external"
	InstructionNotFound = "not_found"
)

// instructionNeedle — подстрока для подсчёта знаменателя полноты: число её
// регистронезависимых вхождений во всём тексте ЗД (ориентир — ~90 в реальной
// закупочной документации). Упоминания, попавшие в пару «форма ↔ инструкция»
// или в ссылку строки состава, считаются разрешёнными; остальные печатаются
// наружу.
const instructionNeedle = "инструкц"

// FileInfo — привязка результата к конкретной версии документа: Para —
// координата версии, и специалист обязан видеть, к какой версии она относится.
type FileInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	// Blocks, Paragraphs, Tables, Runes — счётчики разбора parser.Document.
	Blocks     int `json:"blocks"`
	Paragraphs int `json:"paragraphs"`
	Tables     int `json:"tables"`
	Runes      int `json:"runes"`
}

// Pagination — вердикт детектора достоверности пагинации. При Trusted=false
// поле Page в адреса извлекаемых структур не записывается вовсе.
type Pagination struct {
	Trusted bool `json:"trusted"`
	// Reason — конкретная причина недоверия; пуста при Trusted=true.
	Reason string `json:"reason,omitempty"`
	// UniquePages — число различных номеров страниц по блокам.
	UniquePages int `json:"unique_pages"`
	// MaxPage — максимальный номер страницы по блокам.
	MaxPage int `json:"max_page"`
	// TocMaxPage — максимальный номер страницы из оглавления; 0 — оглавления нет.
	TocMaxPage int `json:"toc_max_page,omitempty"`
}

// Section — раздел документа, восстановленный по оглавлению. Границы:
// StartPara — блок заголовка, EndPara — блок следующего заголовка
// (исключительно); у последнего раздела EndPara — за последним блоком.
type Section struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Label     string `json:"label"`
	StartPara int    `json:"start_para"`
	EndPara   int    `json:"end_para"`
}

// Mention — неразрешённое упоминание инструкций: адрес и цитата. На код
// выхода такие упоминания не влияют — это информация для A9.
type Mention struct {
	Ref   trace.Ref `json:"ref"`
	Count int       `json:"count"`
	Quote string    `json:"quote"`
}

// InstructionStats — метрика полноты: разрешённые упоминания против всех.
type InstructionStats struct {
	// Total — все вхождения instructionNeedle в тексте документа, включая
	// ячейки таблиц (знаменатель метрики).
	Total int `json:"total"`
	// Resolved — вхождения, попавшие в пару «форма ↔ инструкция» или в ссылку
	// строки состава.
	Resolved int `json:"resolved"`
	// Unresolved — остальные вхождения: адрес и цитата каждого места.
	Unresolved []Mention `json:"unresolved,omitempty"`
}

// Issue — дефект структуры, который обязан быть видимым: причина и адрес.
type Issue struct {
	Reason string    `json:"reason"`
	Ref    trace.Ref `json:"ref"`
}

// Result — итог извлечения трёх структур из одного документа закупки.
type Result struct {
	File         FileInfo           `json:"file"`
	Pagination   Pagination         `json:"pagination"`
	Sections     []Section          `json:"sections,omitempty"`
	Composition  CompositionResult  `json:"composition"`
	Forms        FormsResult        `json:"forms"`
	Requirements RequirementsResult `json:"requirements"`
	Instructions InstructionStats   `json:"instructions"`
}

// Problems возвращает причины, по которым хотя бы одна из трёх структур не
// извлечена полностью. Список определяет код выхода CLI (0 — все три full)
// и объявляется в stderr. Неразрешённые упоминания инструкций сюда не входят:
// на код выхода они не влияют.
func (r Result) Problems() []string {
	var out []string
	add := func(name, status, reason string) {
		if status == StatusFull {
			return
		}
		msg := name + ": " + status
		if reason != "" {
			msg += " — " + reason
		}
		out = append(out, msg)
	}
	add("требуемый состав", r.Composition.Status, r.Composition.Reason)
	add("формы и инструкции", r.Forms.Status, r.Forms.Reason)
	add("требования к участнику", r.Requirements.Status, r.Requirements.Reason)
	return out
}

// Extract извлекает три структуры из разобранного документа закупки. Ошибка
// возвращается только на неверный вход (nil): содержательные проблемы
// извлечения — это статусы not_found/partial внутри Result, а не сбой.
func Extract(doc *parser.Document) (Result, error) {
	if doc == nil {
		return Result{}, errors.New("zd: документ не передан")
	}
	x := &extractor{doc: doc, resolved: map[string]int{}}
	toc := findTOC(doc.Blocks)
	tocMax := 0
	for _, e := range toc {
		if e.page > tocMax {
			tocMax = e.page
		}
	}
	x.sections = findSections(doc, toc)
	// Детектор пагинации считается до извлечения: от его вердикта зависит,
	// попадёт ли Page в адреса структур.
	x.pagination = detectPagination(doc, tocMax)

	res := Result{
		File: FileInfo{
			Path:       doc.Path,
			Name:       doc.Name,
			Blocks:     len(doc.Blocks),
			Paragraphs: doc.Paragraphs,
			Tables:     doc.Tables,
			Runes:      doc.Runes,
		},
		Pagination:   x.pagination,
		Sections:     x.sections,
		Composition:  extractComposition(x),
		Forms:        extractForms(x),
		Requirements: extractRequirements(x),
	}
	res.Instructions = x.countInstructions()
	return res, nil
}

// ExtractFile разбирает .docx и извлекает структуры вместе с привязкой к
// версии файла: sha256 и размер считаются до разбора, чтобы отчёт был
// воспроизводим для конкретной редакции документа.
func ExtractFile(path string) (Result, error) {
	if !strings.EqualFold(filepath.Ext(path), ".docx") {
		return Result{}, fmt.Errorf("zd: %s — не .docx: структуры закупочной документации извлекаются только из документов Word 2007+", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("zd: не удалось прочитать %s: %w", path, err)
	}
	doc, err := parser.ParseDocx(path)
	if err != nil {
		return Result{}, fmt.Errorf("zd: разбор %s: %w", filepath.Base(path), err)
	}
	res, err := Extract(doc)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(data)
	res.File.SHA256 = hex.EncodeToString(sum[:])
	res.File.Size = int64(len(data))
	return res, nil
}

// extractor — состояние одного извлечения: разделы, вердикт пагинации и
// учёт разрешённых упоминаний инструкций (ключ — место в документе).
type extractor struct {
	doc        *parser.Document
	sections   []Section
	pagination Pagination
	resolved   map[string]int
}

// ref собирает адрес фрагмента по «адрес-лестнице»: Para есть всегда, Page —
// только при достоверной пагинации, раздел — в Item.
func (x *extractor) ref(blockIdx, page int, cell string) trace.Ref {
	r := trace.Ref{File: x.doc.Path, Format: x.doc.Format, Para: blockIdx}
	if x.pagination.Trusted && page > 0 {
		r.Page = page
	}
	if cell != "" {
		r.Cell = cell
	}
	if label := x.sectionLabel(blockIdx); label != "" {
		r.Item = label
	}
	return r
}

// sectionLabel — значение trace.Ref.Item для блока: «Раздел N «заголовок»».
// Пусто, если блок не попал ни в один раздел (оглавления нет или заголовок
// раздела не нашёлся в теле) — номер раздела не выдумывается.
func (x *extractor) sectionLabel(blockIdx int) string {
	for _, s := range x.sections {
		if blockIdx >= s.StartPara && blockIdx < s.EndPara {
			return s.Label
		}
	}
	return ""
}

// resolve помечает count вхождений instructionNeedle в месте key как
// разрешённые: они попали в пару «форма ↔ инструкция» или в ссылку строки
// состава и наружу не печатаются.
func (x *extractor) resolve(key string, count int) {
	if count > 0 {
		x.resolved[key] += count
	}
}

// tocLineRe — строка оглавления: номер, заголовок и номер страницы, разделённые
// табуляцией и/или пробелами.
var tocLineRe = regexp.MustCompile(`^(\d+)\.\s*\t?\s*(.+?)\s*\t?\s*(\d+)$`)

// tocEntry — пункт оглавления.
type tocEntry struct {
	number int
	title  string
	page   int
	para   int
}

// findTOC находит оглавление: самую длинную серию идущих подряд абзацев,
// которые начинаются с «1.» и чьи номера возрастают на единицу. Одиночное
// совпадение оглавлением не считается: в теле документа так может выглядеть
// обычный нумерованный пункт со ссылкой на страницу.
func findTOC(blocks []parser.Block) []tocEntry {
	var best, run []tocEntry
	flush := func() {
		if len(run) > len(best) {
			best = run
		}
		run = nil
	}
	for _, b := range blocks {
		if b.Table != nil {
			flush()
			continue
		}
		e, ok := parseTOCLine(b)
		if !ok {
			flush()
			continue
		}
		if len(run) > 0 && e.number != run[len(run)-1].number+1 {
			flush()
		}
		if len(run) == 0 && e.number != 1 {
			continue
		}
		run = append(run, e)
	}
	flush()
	if len(best) < 2 {
		return nil
	}
	return best
}

// parseTOCLine разбирает абзац как строку оглавления.
func parseTOCLine(b parser.Block) (tocEntry, bool) {
	m := tocLineRe.FindStringSubmatch(strings.TrimSpace(b.Text))
	if m == nil {
		return tocEntry{}, false
	}
	number, err := strconv.Atoi(m[1])
	if err != nil {
		return tocEntry{}, false
	}
	page, err := strconv.Atoi(m[3])
	if err != nil {
		return tocEntry{}, false
	}
	title := strings.TrimSpace(m[2])
	if title == "" {
		return tocEntry{}, false
	}
	return tocEntry{number: number, title: title, page: page, para: b.Index}, true
}

// findSections находит в теле заголовки пунктов оглавления: первый следующий
// за оглавлением абзац, чей нормализованный текст совпадает с нормализованным
// заголовком. Нумерации в теле нет (парсер не читает w:numPr), поэтому
// совпадение ищется по тексту заголовка без номера. Нет оглавления — разделы
// не выдумываются.
func findSections(doc *parser.Document, toc []tocEntry) []Section {
	if len(toc) == 0 {
		return nil
	}
	var found []Section
	searchAfter := toc[len(toc)-1].para
	for _, e := range toc {
		want := normText(e.title)
		for _, b := range doc.Blocks {
			if b.Table != nil || b.Index <= searchAfter {
				continue
			}
			if normText(b.Text) != want {
				continue
			}
			found = append(found, Section{
				Number:    e.number,
				Title:     e.title,
				Label:     fmt.Sprintf("Раздел %d «%s»", e.number, e.title),
				StartPara: b.Index,
			})
			searchAfter = b.Index
			break
		}
	}
	for i := range found {
		if i+1 < len(found) {
			found[i].EndPara = found[i+1].StartPara
			continue
		}
		found[i].EndPara = doc.Blocks[len(doc.Blocks)-1].Index + 1
	}
	return found
}

// normText — нормализация сравнения текстов: lowercase, серии пробелов и
// табуляции схлопываются в один пробел (как normalizeFileName в rules).
func normText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// countInstr — число регистронезависимых вхождений instructionNeedle.
func countInstr(s string) int {
	return strings.Count(strings.ToLower(s), instructionNeedle)
}

// paraKey — ключ учёта упоминаний для абзаца.
func paraKey(blockIdx int) string { return "para:" + strconv.Itoa(blockIdx) }

// cellKey — ключ учёта упоминаний для ячейки таблицы (путь включает блок,
// чтобы одноимённые адреса разных таблиц не сливались).
func cellKey(blockIdx int, cell string) string {
	return "cell:" + strconv.Itoa(blockIdx) + ":" + cell
}

// cellAddr — адрес ячейки «строка:колонка»; для вложенной таблицы — путь от
// клетки-носителя через «/» (тот же формат, что в parser.Fragments).
func cellAddr(prefix string, row, col int) string {
	cell := strconv.Itoa(row) + ":" + strconv.Itoa(col)
	if prefix == "" {
		return cell
	}
	return prefix + "/" + cell
}

// sampleRunes — длина отсечки цитаты упоминания: строка отчёта обязана
// оставаться строкой, а искать причину по тысячам символов бесполезно.
const sampleRunes = 120

// quoteLine — цитата для отчёта: одна строка, не длиннее sampleRunes.
func quoteLine(text string) string {
	one := strings.TrimSpace(strings.NewReplacer("\n", " ", "\t", " ").Replace(text))
	runes := []rune(one)
	if len(runes) > sampleRunes {
		return string(runes[:sampleRunes]) + "…"
	}
	return one
}

// mentionPlace — место документа с вхождениями instructionNeedle.
type mentionPlace struct {
	key   string
	ref   trace.Ref
	quote string
	total int
}

// countInstructions считает знаменатель полноты (все вхождения) и печатает
// наружу неразрешённые: те, что не попали ни в пару «форма ↔ инструкция»,
// ни в ссылку строки состава.
func (x *extractor) countInstructions() InstructionStats {
	var stats InstructionStats
	for _, place := range x.collectMentions() {
		stats.Total += place.total
		resolved := x.resolved[place.key]
		if resolved > place.total {
			resolved = place.total
		}
		stats.Resolved += resolved
		if rest := place.total - resolved; rest > 0 {
			stats.Unresolved = append(stats.Unresolved, Mention{Ref: place.ref, Count: rest, Quote: place.quote})
		}
	}
	return stats
}

// collectMentions обходит все блоки документа: абзацы и ячейки таблиц,
// включая вложенные таблицы (текст в них участвует в знаменателе так же,
// как в doc.Text()).
func (x *extractor) collectMentions() []mentionPlace {
	var out []mentionPlace
	for _, b := range x.doc.Blocks {
		if b.Table == nil {
			if n := countInstr(b.Text); n > 0 {
				out = append(out, mentionPlace{
					key:   paraKey(b.Index),
					ref:   x.ref(b.Index, b.Page, ""),
					quote: quoteLine(b.Text),
					total: n,
				})
			}
			continue
		}
		out = append(out, x.tableMentions(b, b.Table, "")...)
	}
	return out
}

// tableMentions — упоминания в ячейках таблицы, рекурсивно по вложенным.
func (x *extractor) tableMentions(b parser.Block, t *parser.Table, prefix string) []mentionPlace {
	var out []mentionPlace
	for _, row := range t.Rows {
		page := b.Page
		if row.Page > 0 {
			page = row.Page
		}
		for ci, c := range row.Cells {
			path := cellAddr(prefix, row.Index, ci+1)
			if n := countInstr(c.Text); n > 0 {
				out = append(out, mentionPlace{
					key:   cellKey(b.Index, path),
					ref:   x.ref(b.Index, page, path),
					quote: quoteLine(c.Text),
					total: n,
				})
			}
			for _, nt := range c.Tables {
				out = append(out, x.tableMentions(b, nt, path)...)
			}
		}
	}
	return out
}
