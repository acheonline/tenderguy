package parser

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nguyenthenguyen/docx"
)

// maxMissingSamples — сколько потерянных фрагментов показываем в отчёте: больше
// не помогает найти причину, а вывод раздувает.
const maxMissingSamples = 5

// missingSampleRunes — длина отсечки образца потерянного фрагмента.
const missingSampleRunes = 120

// nonText — элементы, которые несут параметры вёрстки или служебную разметку:
// видимого текста в них нет. Их содержимое не извлекается и не участвует в
// проверке полноты — обе стороны сверки видят одни и те же правила.
var nonText = map[string]bool{
	"pPr": true, "rPr": true, "tblPr": true, "tblPrEx": true, "tblGrid": true,
	"trPr": true, "tcPr": true, "sectPr": true, "sdtPr": true, "sdtEndPr": true,
	"customXmlPr": true, "dataBinding": true, "proofErr": true,
	"bookmarkStart": true, "bookmarkEnd": true, "commentRangeStart": true,
	"commentRangeEnd": true, "commentReference": true, "permStart": true,
	"permEnd": true, "moveFromRangeStart": true, "moveFromRangeEnd": true,
	"moveToRangeStart": true, "moveToRangeEnd": true, "customXmlInsRangeStart": true,
	"customXmlInsRangeEnd": true, "customXmlDelRangeStart": true, "customXmlDelRangeEnd": true,
	"customXmlMoveFromRangeStart": true, "customXmlMoveFromRangeEnd": true,
	"customXmlMoveToRangeStart": true, "customXmlMoveToRangeEnd": true,
}

// Разбор идёт по локальным именам элементов, без проверки пространства имён:
// Word пишет «w:», а генераторы docx (LibreOffice, самописные выгрузки) иногда
// используют namespace по умолчанию. Локальные имена в OOXML уникальны внутри
// документа, а вот отсутствие префикса при строгом разборе ломало бы извлечение.

// node — узел дерева document.xml. Дерево строится один раз и обходится
// несколько раз (текст, структура, эталон полноты): так проще держать правила
// обхода согласованными, чем в одном проходе с xml.Decoder.
type node struct {
	name     xml.Name
	attr     []xml.Attr
	text     string
	children []*node
}

func (n *node) local() string {
	if n == nil {
		return ""
	}
	return n.name.Local
}

// attrVal отдаёт значение атрибута по локальному имени (w:val, w:w, r:id).
func (n *node) attrVal(local string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// intAttr — числовой атрибут; 0, если атрибута нет или он не число.
func (n *node) intAttr(local string) int {
	v := strings.TrimSpace(n.attrVal(local))
	if v == "" {
		return 0
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return i
}

func (n *node) child(local string) *node {
	if n == nil {
		return nil
	}
	for _, ch := range n.children {
		if ch.local() == local {
			return ch
		}
	}
	return nil
}

// descendant — первый элемент с таким именем в глубину. Нужен для поиска w:body:
// между корнем document.xml и телом документа бывает обёртка (фрагменты,
// сохранённые LibreOffice «как есть»), заваливать из-за этого разбор нельзя.
func (n *node) descendant(local string) *node {
	if n == nil {
		return nil
	}
	if n.local() == local {
		return n
	}
	for _, ch := range n.children {
		if found := ch.descendant(local); found != nil {
			return found
		}
	}
	return nil
}

// effective — дети узла с учётом политики AlternateContent.
func (n *node) effective() []*node {
	if n == nil {
		return nil
	}
	out := make([]*node, 0, len(n.children))
	for _, ch := range n.children {
		if ch.local() == "AlternateContent" {
			out = append(out, alternateContent(ch)...)
			continue
		}
		out = append(out, ch)
	}
	return out
}

// alternateContent разрешает дублирование содержимого в OOXML: Word кладёт одну и
// ту же начинку в mc:Choice (для новых клиентов) и mc:Fallback (для старых).
// Взять обе ветки — значит извлечь один и тот же текст дважды, поэтому ветка
// выбирается одна: сначала Choice с текстом Word, затем Fallback с текстом
// (WordArt: в Choice он только в надписях фигуры), затем любой Choice.
// Правило общее для извлечения текста и для эталона полноты, иначе сверка
// даёт ложные потери на легальных документах.
func alternateContent(n *node) []*node {
	var firstChoice, fallback *node
	for _, ch := range n.children {
		switch ch.local() {
		case "Choice":
			if hasWordText(ch) {
				return ch.children
			}
			if firstChoice == nil {
				firstChoice = ch
			}
		case "Fallback":
			if hasWordText(ch) {
				return ch.children
			}
			if fallback == nil {
				fallback = ch
			}
		}
	}
	if firstChoice != nil {
		return firstChoice.children
	}
	if fallback != nil {
		return fallback.children
	}
	return n.children
}

// hasWordText — есть ли внутри текст абзацев (w:t) или формул (m:t).
func hasWordText(n *node) bool {
	if n == nil {
		return false
	}
	if n.local() == "t" && n.text != "" {
		return true
	}
	for _, ch := range n.children {
		if hasWordText(ch) {
			return true
		}
	}
	return false
}

// items собирает дочерние элементы с именем name, раскрывая обёртки, которые Word
// ставит вокруг строк и ячеек таблиц: w:sdt (элементы управления содержимым) и
// w:customXml. Глубже не ходим: вложенная таблица — не строка внешней.
func items(n *node, name string) []*node {
	var out []*node
	for _, ch := range n.effective() {
		switch ch.local() {
		case name:
			out = append(out, ch)
		case "sdt":
			out = append(out, items(ch.child("sdtContent"), name)...)
		case "customXml":
			out = append(out, items(ch, name)...)
		}
	}
	return out
}

// docxParser — состояние одного прохода по document.xml.
type docxParser struct {
	tables int
	rows   int
	cells  int
	notes  map[string]int
}

func (p *docxParser) notef(format string, args ...any) {
	p.notes[fmt.Sprintf(format, args...)]++
}

func (p *docxParser) sortedNotes() []string {
	keys := make([]string, 0, len(p.notes))
	for k := range p.notes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if n := p.notes[k]; n > 1 {
			out = append(out, fmt.Sprintf("%s (повторов: %d)", k, n))
			continue
		}
		out = append(out, k)
	}
	return out
}

// ParseDocx извлекает из .docx текст и таблицы.
//
// Архив OOXML читает github.com/nguyenthenguyen/docx: она отдаёт содержимое части
// word/document.xml. Разбор структуры (абзацы, таблицы, слияния, вложения)
// выполняется здесь, потому что библиотека предназначена для подстановки текста и
// таблицы не разбирает.
func ParseDocx(path string) (*Document, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("не задан путь к .docx")
	}
	content, err := documentXML(path)
	if err != nil {
		return nil, err
	}
	root, lenient, err := buildTree(content)
	if err != nil {
		return nil, fmt.Errorf("разбор document.xml в %s: %w", filepath.Base(path), err)
	}
	body := root.descendant("body")
	if body == nil {
		return nil, fmt.Errorf("в %s нет тела документа (w:body) — это не документ Word 2007+", filepath.Base(path))
	}

	p := &docxParser{notes: map[string]int{}}
	if lenient {
		p.notef("document.xml разобрался только в нестрогом режиме XML: в разметке есть нарушения")
	}
	blocks := p.blocks(body, 0)
	for i := range blocks {
		blocks[i].Index = i + 1
	}

	doc := &Document{
		Path:       path,
		Name:       filepath.Base(path),
		Format:     FormatDocx,
		Blocks:     blocks,
		Paragraphs: paragraphBlocks(blocks),
		Tables:     p.tables,
		Rows:       p.rows,
		Cells:      p.cells,
	}
	doc.Runes = runeCount(doc.extractedBlob())
	doc.Coverage = checkCoverage(body, doc)
	doc.Notes = p.sortedNotes()
	return doc, nil
}

// documentXML читает часть word/document.xml.
//
// Ограничение библиотеки: ей нужны обе части — word/document.xml и
// word/_rels/document.xml.rels. В файле без связей (генераторы такое выдают редко)
// открытие не удастся; причину сообщение показывает явно.
func documentXML(path string) (content string, err error) {
	f, err := docx.ReadDocxFile(path)
	if err != nil {
		return "", fmt.Errorf("%s не открылся как .docx: %w (должны быть части word/document.xml и word/_rels/document.xml.rels)",
			filepath.Base(path), err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("закрытие %s: %w", filepath.Base(path), closeErr)
		}
	}()

	content = f.Editable().GetContent()
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("часть word/document.xml в %s пустая", filepath.Base(path))
	}
	return content, nil
}

// buildTree разбирает document.xml в дерево узлов; второе значение — признак того,
// что понадобился нестрогий режим.
func buildTree(content string) (root *node, lenient bool, err error) {
	root, err = decodeTree(content, true)
	if err == nil {
		return root, false, nil
	}
	strictErr := err

	// Реальные выгрузки Word и LibreOffice иногда содержат undeclared-сущности и
	// несопоставленные закрывающие теги. Терять из-за этого весь документ — значит
	// потерять текст, которого в этих узлах нет.
	root, lenientErr := decodeTree(content, false)
	if lenientErr != nil {
		return nil, false, fmt.Errorf("%w (нестрогий разбор тоже не удался: %v)", strictErr, lenientErr)
	}
	return root, true, nil
}

func decodeTree(content string, strict bool) (*node, error) {
	dec := xml.NewDecoder(strings.NewReader(content))
	dec.Strict = strict

	root := &node{name: xml.Name{Local: "#document"}}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name, attr: t.Attr}
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			// В нестрогом режиме декодер сам дописывает пропущенные закрытия, поэтому
			// здесь достаточно подняться на уровень вверх; ниже корня не уходим.
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			stack[len(stack)-1].text += string(t)
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("разметка несбалансирована: незакрытых элементов %d", len(stack)-1)
	}
	return root, nil
}

// blocks разбирает блочные элементы контейнера (тело документа, ячейка, текстовый
// блок) в порядке их следования в файле.
func (p *docxParser) blocks(parent *node, depth int) []Block {
	var out []Block
	for _, ch := range parent.effective() {
		switch ch.local() {
		case "p":
			out = append(out, Block{Type: BlockParagraph, Style: paragraphStyle(ch), Text: p.paragraph(ch)})
		case "tbl":
			out = append(out, Block{Type: BlockTable, Table: p.table(ch, depth)})
		case "sdt":
			out = append(out, p.blocks(ch.child("sdtContent"), depth)...)
		case "customXml":
			out = append(out, p.blocks(ch, depth)...)
		case "altChunk":
			p.notef("w:altChunk: встроенная внешняя часть OOXML не разобрана — её содержимое осталось в отдельном файле архива")
		default:
			if nonText[ch.local()] {
				continue
			}
			// Неизвестный контейнер: вытаскиваем из него текст, чтобы не потерять
			// содержимое из-за элемента, которого нет в списке разбираемых.
			var sb strings.Builder
			p.inline(ch, &sb)
			text := strings.TrimRight(sb.String(), "\n")
			if strings.TrimSpace(text) == "" {
				continue
			}
			p.notef("неизвестный блочный элемент <%s>: содержимое извлечено как текст", ch.local())
			out = append(out, Block{Type: BlockParagraph, Text: text})
		}
	}
	return out
}

func paragraphStyle(p *node) string {
	return p.child("pPr").child("pStyle").attrVal("val")
}

// paragraph собирает текст абзаца, обходя его строчки и вложения.
func (p *docxParser) paragraph(n *node) string {
	var sb strings.Builder
	p.inlineChildren(n, &sb)
	return sb.String()
}

func (p *docxParser) inlineChildren(n *node, sb *strings.Builder) {
	for _, ch := range n.effective() {
		if nonText[ch.local()] {
			continue
		}
		p.inline(ch, sb)
	}
}

// inline разбирает содержимое строчного (run-level) элемента.
//
// Вложенность произвольная: w:hyperlink, w:ins, w:smartTag, w:sdt, w:oMath,
// w:drawing — всё это элементы-контейнеры со строчками внутри, поэтому неизвестный
// элемент обходится рекурсивно, а не отбрасывается.
func (p *docxParser) inline(n *node, sb *strings.Builder) {
	switch n.local() {
	case "t":
		// w:t — текст абзаца, m:t — формула, a:t — надпись в фигуре.
		sb.WriteString(n.text)
	case "tab":
		sb.WriteString("\t")
	case "br", "cr":
		sb.WriteString("\n")
	case "noBreakHyphen":
		sb.WriteString("-")
	case "instrText":
		// команда поля (PAGE, NUMPAGES, ссылка) — на страницу не печатается
	case "delText":
		// текст, удалённый правкой с отслеживанием: виден только в режиме правок
	case "softHyphen", "sym", "footnoteReference", "endnoteReference", "fldChar":
		// невидимый разделитель, глиф спецшрифта или ссылка на сноску: текста здесь нет
	case "txbxContent":
		// Текст надписи попадает и в показ, и в сверку полноты, поэтому ячейки
		// внутри него рендерятся без обрезки краёв — как extractedBlob.
		text := strings.TrimRight(renderBlocks(p.blocks(n, 0), true, false), "\n")
		if text != "" {
			sb.WriteString(text)
			sb.WriteString("\n")
		}
	case "sdt":
		p.inlineChildren(n.child("sdtContent"), sb)
	default:
		p.inlineChildren(n, sb)
	}
}

// table разбирает таблицу, включая вложенные (в Word таблица легальна внутри ячейки).
func (p *docxParser) table(n *node, depth int) *Table {
	p.tables++
	tbl := &Table{
		Depth: depth,
		Style: n.child("tblPr").child("tblStyle").attrVal("val"),
	}
	// anchors — ячейки, начавшие вертикальное слияние, по номеру колонки сетки.
	anchors := map[int]*Cell{}
	width := 0

	for _, tr := range items(n, "tr") {
		p.rows++
		row := Row{Index: len(tbl.Rows) + 1, Header: isHeaderRow(tr)}
		for _, tc := range items(tr, "tc") {
			p.cells++
			row.Cells = append(row.Cells, p.cell(tc, depth+1))
		}
		// Разметку слияний разбираем после того, как строка собрана целиком:
		// append следующей ячейки переложил бы массив, и указатели в anchors
		// остались бы смотреть в старую копию — RowSpan терялся бы молча.
		gridCol := 0
		for i := range row.Cells {
			cell := &row.Cells[i]
			span := cell.ColSpan
			if span < 1 {
				span = 1
			}
			for k := 0; k < span; k++ {
				if cell.Merged {
					if anchor, ok := anchors[gridCol+k]; ok {
						anchor.RowSpan++
					}
					continue
				}
				anchors[gridCol+k] = cell
			}
			gridCol += span
		}
		if gridCol > width {
			width = gridCol
		}
		tbl.Rows = append(tbl.Rows, row)
	}
	tbl.Columns = width
	return tbl
}

func isHeaderRow(tr *node) bool {
	header := tr.child("trPr").child("tblHeader")
	if header == nil {
		return false
	}
	switch header.attrVal("val") {
	case "false", "0":
		return false
	default:
		return true
	}
}

// cell разбирает ячейку: свойства слияния и её блоки. Текст вложенных таблиц в
// Cell.Text не входит — они остаются в Cell.Tables, иначе при выводе текст
// повторился бы дважды.
func (p *docxParser) cell(n *node, depth int) Cell {
	cell := Cell{}
	props := n.child("tcPr")
	cell.ColSpan = props.child("gridSpan").intAttr("val")
	if merge := props.child("vMerge"); merge != nil {
		// Значение отсутствует либо равно continue — это продолжение слияния сверху.
		cell.Merged = merge.attrVal("val") != "restart"
		if !cell.Merged {
			cell.RowSpan = 1
		}
	}

	var parts []string
	for _, b := range p.blocks(n, depth) {
		if b.Table != nil {
			cell.Tables = append(cell.Tables, b.Table)
			continue
		}
		parts = append(parts, b.Text)
	}
	cell.Text = strings.Join(parts, "\n")
	return cell
}

// sourceStats — эталон полноты: что в document.xml является текстом и структурой.
type sourceStats struct {
	pieces       []string
	tables, rows int
	cells        int
}

func collectSource(n *node, st *sourceStats) {
	for _, ch := range n.effective() {
		if nonText[ch.local()] {
			continue
		}
		switch ch.local() {
		case "t":
			if ch.text != "" {
				st.pieces = append(st.pieces, ch.text)
			}
			continue
		case "instrText", "delText":
			continue
		case "tbl":
			st.tables++
		case "tr":
			st.rows++
		case "tc":
			st.cells++
		}
		collectSource(ch, st)
	}
}

// checkCoverage сверяет извлечённую модель с эталоном из document.xml.
//
// Каждый фрагмент исходника ищется с монотонно движущейся позицией: так
// проверяются и наличие, и порядок. Фрагмент, найденный раньше текущей позиции,
// считается донесённым, но помечается как OutOfOrder — расхождение порядка возможно
// там, где структура модели и обход расходятся (таблица между абзацами ячейки).
func checkCoverage(body *node, doc *Document) Coverage {
	st := &sourceStats{}
	collectSource(body, st)
	blob := doc.extractedBlob()

	cov := Coverage{
		SourcePieces: len(st.pieces),
		SourceRunes:  runeCount(st.pieces...),
		TextRunes:    doc.Runes,
		SourceTables: st.tables,
		SourceRows:   st.rows,
		SourceCells:  st.cells,
	}

	offset := 0
	for _, piece := range st.pieces {
		if strings.TrimSpace(piece) == "" {
			continue
		}
		if idx := strings.Index(blob[offset:], piece); idx >= 0 {
			offset += idx + len(piece)
			continue
		}
		if strings.Contains(blob, piece) {
			cov.OutOfOrder++
			continue
		}
		cov.MissingPieces++
		if len(cov.Missing) < maxMissingSamples {
			cov.Missing = append(cov.Missing, sample(piece))
		}
	}
	cov.Complete = cov.MissingPieces == 0
	return cov
}

func sample(piece string) string {
	runes := []rune(piece)
	if len(runes) > missingSampleRunes {
		return string(runes[:missingSampleRunes]) + "…"
	}
	return piece
}
