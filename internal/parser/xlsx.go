package parser

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Задача P1.3 — разбор .xlsx: спецификации, ведомости и коды ОКПД2 из
// документации и заявок. Значения ячеек читает `github.com/xuri/excelize/v2`
// (они уже отформатированы стилями числа), формулы — тоже excelize: он же
// переводит общие (shared) формулы в клетки-приёмники.
//
// Эталон полноты, как и в .docx, берётся из самого файла, а не из ответа
// библиотеки: zip-обход листового XML перечисляет все ячейки с содержимым
// (<v> или <f>) и их адреса. Критерий задачи — «все строки спецификаций
// прочитаны»: каждая содержательная клетка исходника обязана дойти до модели
// значением или формулой.

// FormatXlsx — формат содержимого книги Excel.
const FormatXlsx = "xlsx"

// XlsxCell — непустая ячейка листа: адрес, отформатированное значение и,
// если клетка формульная, текст формулы (без знака «=»).
type XlsxCell struct {
	// Ref — адрес A1 («B7»): координата для трассировки (P1.6).
	Ref string `json:"ref"`
	// Row, Col — номер строки и номер колонки, начиная с 1.
	Row int `json:"row"`
	Col int `json:"col"`
	// Value — отформатированное значение ячейки.
	Value string `json:"value,omitempty"`
	// Formula — текст формулы; пусто у ячеек-констант.
	Formula string `json:"formula,omitempty"`
}

// XlsxRow — строка листа. Ячейки разрежены: пустые клетки в модель не
// попадают, их координаты остаются в Ref/Col присутствующих.
type XlsxRow struct {
	Index int        `json:"index"`
	Cells []XlsxCell `json:"cells,omitempty"`
}

// XlsxSheet — один лист-таблица книги.
type XlsxSheet struct {
	// Index — порядковый номер листа в книге, начиная с 1.
	Index    int       `json:"index"`
	Name     string    `json:"name"`
	Hidden   bool      `json:"hidden,omitempty"`
	Rows     []XlsxRow `json:"rows,omitempty"`
	Columns  int       `json:"columns"`
	Cells    int       `json:"cells"`
	Formulas int       `json:"formulas"`

	// Merges — объединённые диапазоны («A1:C1»): значение стоит только в
	// верхней левой клетке, как и показывает Excel.
	Merges []string `json:"merges,omitempty"`

	// SourceCells/SourceFormulas — ячеек с содержимым и формульных ячеек
	// по листовому XML; по ним сверяется полнота.
	SourceCells    int `json:"source_cells"`
	SourceFormulas int `json:"source_formulas"`
}

// XlsxCoverage — самопроверка полноты извлечения книги.
type XlsxCoverage struct {
	SourceCells    int `json:"source_cells"`
	SourceFormulas int `json:"source_formulas"`
	// ModelCells — непустых ячеек, дошедших до модели.
	ModelCells int `json:"model_cells"`
	// MissingCells — содержательных ячеек исходника, которых нет в модели:
	// это потери.
	MissingCells int `json:"missing_cells"`
	// Missing — первые потерянные адреса с именем листа.
	Missing []string `json:"missing,omitempty"`

	// Complete — содержимое книги извлечено без потерь.
	Complete bool `json:"complete"`
}

// XlsxDocument — результат разбора одной книги.
type XlsxDocument struct {
	Path   string      `json:"path"`
	Name   string      `json:"name"`
	Format string      `json:"format"`
	Sheets []XlsxSheet `json:"sheets"`

	// Rows — непустых строк модели по всем листам.
	Rows int `json:"rows"`
	// Cells — непустые ячейки модели.
	Cells int `json:"cells"`
	// Formulas — ячейки с прочитанной формулой.
	Formulas int `json:"formulas"`
	// Runes — объём извлечённого текста (значения и формулы) без пробелов.
	Runes int `json:"runes"`

	Coverage XlsxCoverage `json:"coverage"`
	// Notes — замечания разбора: скрытые, пустые и пропущенные листы.
	Notes []string `json:"notes,omitempty"`
}

// Complete — признак «потерь нет»: критерий готовности задачи P1.3
// («все строки спецификаций прочитаны») как измеримое свойство разбора.
func (d *XlsxDocument) Complete() bool {
	return d.Coverage.Complete
}

// Text возвращает содержимое книги текстом: листы разделены пустой строкой,
// строки — переводом строки, колонки — табуляцией (как таблицы в .docx).
func (d *XlsxDocument) Text() string {
	parts := make([]string, 0, len(d.Sheets))
	for _, sh := range d.Sheets {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Лист: %s\n", sh.Name)
		sb.WriteString(renderXlsxGrid(sh))
		parts = append(parts, sb.String())
	}
	return strings.Join(parts, "\n\n")
}

// renderXlsxGrid разворачивает разреженные строки листа в сетку значений.
func renderXlsxGrid(sh XlsxSheet) string {
	lines := make([]string, 0, len(sh.Rows))
	for _, row := range sh.Rows {
		cells := make([]string, sh.Columns)
		for _, c := range row.Cells {
			if c.Col >= 1 && c.Col <= sh.Columns {
				cells[c.Col-1] = flattenLines(c.Value, true)
			}
		}
		lines = append(lines, strings.Join(cells, "\t"))
	}
	return strings.Join(lines, "\n")
}

// xlsxCoord — адрес ячейки для поиска по сетке значений.
type xlsxCoord struct{ row, col int }

// srcCell — ячейка с содержимым из листового XML: эталон проверки полноты.
type srcCell struct {
	ref      string
	row, col int
	// kind — атрибут t элемента <c>: s, inlineStr, str, b, e либо пусто (число).
	kind string
	// hasFormula — в клетке есть <f>.
	hasFormula bool
	// requireValue — содержимое обязано быть непустым в модели: значение
	// исходника разрешилось непустым текстом либо это формула.
	requireValue bool
}

// sheetIndex — лист книги по workbook.xml: имя, файл содержимого, скрытость.
type sheetIndex struct {
	name   string
	entry  string
	hidden bool
}

// sheetSource — XML-эталон одного листа: содержательные ячейки и слияния.
type sheetSource struct {
	cells  []srcCell
	merges []string
}

// bookSource — индекс книги: листы в порядке workbook.xml и их эталоны.
type bookSource struct {
	order  []sheetIndex
	byName map[string]*sheetSource
}

// ParseXlsx извлекает листы книги: значения и формулы через excelize,
// проверку полноты ведёт по XML-эталону zip-контейнера.
// Ошибкой считается файл, который вообще не удалось открыть как книгу;
// потерянная клетка — не ошибка вызова, а вердикт Coverage.
func ParseXlsx(path string) (*XlsxDocument, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("открытие xlsx-файла: %w", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("excelize не открывает книгу: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "tenderguy: книга %s не закрыта: %v\n", filepath.Base(path), closeErr)
		}
	}()

	src, err := scanBookZip(path)
	if err != nil {
		return nil, err
	}

	doc := &XlsxDocument{Path: path, Name: filepath.Base(path), Format: FormatXlsx}
	var missing []string
	for i, idx := range src.order {
		sheet, miss, err := parseXlsxSheet(f, i+1, idx, src.byName[idx.name])
		if err != nil {
			return nil, err
		}
		missing = append(missing, miss...)
		doc.Sheets = append(doc.Sheets, *sheet)

		if idx.hidden {
			doc.Notes = append(doc.Notes, fmt.Sprintf("лист «%s» скрыт в Excel — прочитан полностью", idx.name))
		}
		if len(sheet.Rows) == 0 && sheet.SourceCells == 0 {
			doc.Notes = append(doc.Notes, fmt.Sprintf("лист «%s» пуст", idx.name))
		}
	}

	known := make(map[string]bool, len(src.order))
	for _, idx := range src.order {
		known[idx.name] = true
	}
	for _, name := range f.GetSheetList() {
		if !known[name] {
			doc.Notes = append(doc.Notes, fmt.Sprintf("лист «%s» — не таблица (график или диалог), в модель не вошёл", name))
		}
	}
	if len(doc.Sheets) == 0 {
		return nil, fmt.Errorf("в книге нет листов-таблиц: %s", filepath.Base(path))
	}

	for _, sh := range doc.Sheets {
		doc.Rows += len(sh.Rows)
		doc.Cells += sh.Cells
		doc.Formulas += sh.Formulas
		doc.Coverage.SourceCells += sh.SourceCells
		doc.Coverage.SourceFormulas += sh.SourceFormulas
	}
	doc.Runes = xlsxRunes(doc)
	doc.Coverage.ModelCells = doc.Cells
	doc.Coverage.MissingCells = len(missing)
	doc.Coverage.Complete = len(missing) == 0
	if len(missing) > 0 {
		doc.Coverage.Missing = missing
		if len(doc.Coverage.Missing) > 10 {
			doc.Coverage.Missing = doc.Coverage.Missing[:10]
		}
	}
	return doc, nil
}

// xlsxRunes — объём текста значений и формул без пробельных символов.
func xlsxRunes(doc *XlsxDocument) int {
	n := 0
	for _, sh := range doc.Sheets {
		for _, row := range sh.Rows {
			for _, c := range row.Cells {
				n += nonSpaceRunes(c.Value) + nonSpaceRunes(c.Formula)
			}
		}
	}
	return n
}

// parseXlsxSheet читает один лист: сетку значений и формулы из excelize —
// и сверяет их с XML-эталоном. Возвращает лист и адреса потерянных клеток.
func parseXlsxSheet(f *excelize.File, pos int, idx sheetIndex, source *sheetSource) (*XlsxSheet, []string, error) {
	if source == nil {
		source = &sheetSource{}
	}
	grid, err := f.GetRows(idx.name)
	if err != nil {
		return nil, nil, fmt.Errorf("лист «%s»: %w", idx.name, err)
	}

	formulas := make(map[xlsxCoord]string, len(source.cells))
	for _, c := range source.cells {
		if !c.hasFormula {
			continue
		}
		text, err := f.GetCellFormula(idx.name, c.ref)
		if err != nil {
			return nil, nil, fmt.Errorf("лист «%s», формула %s: %w", idx.name, c.ref, err)
		}
		if text == "" {
			// Формула в исходнике есть, но excelize её не вернул: клетку
			// спасёт cached-значение; если и оно пусто — полнота отметит потерю.
			continue
		}
		formulas[xlsxCoord{c.row, c.col}] = text
	}

	sheet := &XlsxSheet{
		Index:          pos,
		Name:           idx.name,
		Hidden:         idx.hidden,
		Merges:         source.merges,
		SourceCells:    len(source.cells),
		SourceFormulas: countSrcFormulas(source.cells),
	}

	var missing []string
	for _, c := range source.cells {
		if !cellCovered(grid, formulas, c) {
			missing = append(missing, fmt.Sprintf("лист «%s» %s", idx.name, c.ref))
		}
	}

	for r, gridRow := range grid {
		if len(gridRow) > sheet.Columns {
			sheet.Columns = len(gridRow)
		}
		var cells []XlsxCell
		for c, value := range gridRow {
			coord := xlsxCoord{r + 1, c + 1}
			formula := formulas[coord]
			if value == "" && formula == "" {
				continue
			}
			ref, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				return nil, nil, fmt.Errorf("лист «%s»: адрес строки %d колонки %d: %w", idx.name, r+1, c+1, err)
			}
			cells = append(cells, XlsxCell{Ref: ref, Row: coord.row, Col: coord.col, Value: value, Formula: formula})
			sheet.Cells++
			if formula != "" {
				sheet.Formulas++
			}
		}
		if len(cells) > 0 {
			sheet.Rows = append(sheet.Rows, XlsxRow{Index: r + 1, Cells: cells})
		}
	}
	return sheet, missing, nil
}

// cellCovered — долетело ли содержимое клетки до модели. Клетка вне сетки
// потеряна, только если требовала непустого содержимого; пустая строковая
// клетка («Excel показывает пусто») потерей не считается.
func cellCovered(grid [][]string, formulas map[xlsxCoord]string, c srcCell) bool {
	if c.row >= 1 && c.row <= len(grid) && c.col >= 1 && c.col <= len(grid[c.row-1]) {
		if grid[c.row-1][c.col-1] != "" || formulas[xlsxCoord{c.row, c.col}] != "" {
			return true
		}
		return !c.requireValue
	}
	return !c.requireValue
}

func countSrcFormulas(cells []srcCell) int {
	n := 0
	for _, c := range cells {
		if c.hasFormula {
			n++
		}
	}
	return n
}

// scanBookZip читает эталон книги из zip-контейнера: порядок и свойства
// листов по workbook.xml, содержательные клетки по XML каждого листа.
func scanBookZip(path string) (*bookSource, error) {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("чтение zip-контейнера книги: %w", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "tenderguy: контейнер %s не закрыт: %v\n", filepath.Base(path), closeErr)
		}
	}()

	files := make(map[string]*zip.File, len(rc.File))
	for _, zf := range rc.File {
		files[zf.Name] = zf
	}
	wb, ok := files["xl/workbook.xml"]
	if !ok {
		return nil, fmt.Errorf("в файле нет xl/workbook.xml — это не .xlsx: %s", filepath.Base(path))
	}

	var shared []string
	if ss, ok := files["xl/sharedStrings.xml"]; ok {
		rd, err := ss.Open()
		if err != nil {
			return nil, fmt.Errorf("открытие словаря строк книги: %w", err)
		}
		shared, err = parseSharedStrings(rd)
		closeErr := rd.Close()
		if err != nil {
			return nil, fmt.Errorf("словарь строк книги: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("закрытие словаря строк: %w", closeErr)
		}
	}

	rels := map[string]string{}
	if rf, ok := files["xl/_rels/workbook.xml.rels"]; ok {
		rd, err := rf.Open()
		if err != nil {
			return nil, fmt.Errorf("открытие связей книги: %w", err)
		}
		rels, err = parseRels(rd)
		closeErr := rd.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, fmt.Errorf("закрытие связей книги: %w", closeErr)
		}
	}

	order, err := func() ([]sheetIndex, error) {
		rd, err := wb.Open()
		if err != nil {
			return nil, fmt.Errorf("открытие workbook.xml: %w", err)
		}
		defer func() {
			if closeErr := rd.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "tenderguy: workbook.xml не закрыт: %v\n", closeErr)
			}
		}()
		return parseWorkbookIndex(rd, rels)
	}()
	if err != nil {
		return nil, err
	}

	src := &bookSource{order: order, byName: make(map[string]*sheetSource, len(order))}
	for _, idx := range order {
		zf, ok := files[idx.entry]
		if !ok {
			return nil, fmt.Errorf("в книге нет файла листа «%s» (%s)", idx.name, idx.entry)
		}
		rd, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("открытие листа «%s»: %w", idx.name, err)
		}
		sheet, err := parseSheetSource(rd, shared)
		closeErr := rd.Close()
		if err != nil {
			return nil, fmt.Errorf("лист «%s»: %w", idx.name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("закрытие листа «%s»: %w", idx.name, closeErr)
		}
		src.byName[idx.name] = sheet
	}
	return src, nil
}

// parseWorkbookIndex читает workbook.xml по уже разобранному списку связей:
// листы в порядке книги, их файлы-содержимое и признак скрытости.
// Нетабличные листы (графики, диалоги) в индекс не попадают — о них сообщит
// заметкой ParseXlsx.
func parseWorkbookIndex(rd io.Reader, rels map[string]string) ([]sheetIndex, error) {
	var order []sheetIndex
	dec := xml.NewDecoder(rd)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return order, nil
		}
		if err != nil {
			return nil, fmt.Errorf("разбор workbook.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "sheet" {
			continue
		}
		var name, rid, state string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "name":
				name = a.Value
			case "id":
				rid = a.Value
			case "state":
				state = a.Value
			}
		}
		if name == "" {
			continue
		}
		target, ok := rels[rid]
		if !ok {
			continue
		}
		entry := normalizeRelTarget(target)
		if !strings.HasPrefix(entry, "xl/worksheets/") {
			continue
		}
		order = append(order, sheetIndex{
			name:   name,
			entry:  entry,
			hidden: state != "" && state != "visible",
		})
	}
}

// normalizeRelTarget переводит путь связи в имя элемента zip-контейнера.
func normalizeRelTarget(target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return "xl/" + target
}

// parseRels читает xl/_rels/workbook.xml.rels: Id -> Target.
func parseRels(rd io.Reader) (map[string]string, error) {
	rels := map[string]string{}
	dec := xml.NewDecoder(rd)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return rels, nil
		}
		if err != nil {
			return nil, fmt.Errorf("разбор связей книги: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			continue
		}
		var id, target string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "Id":
				id = a.Value
			case "Target":
				target = a.Value
			}
		}
		if id != "" && target != "" {
			rels[id] = target
		}
	}
}

// parseSharedStrings читает словарь строк книги: текст каждого <si>;
// фразы фонетики (<rPh>) пропускаются — Excel их не показывает как значение.
func parseSharedStrings(rd io.Reader) ([]string, error) {
	var out []string
	var sb strings.Builder
	inSi, inT, inRPh := false, false, false

	dec := xml.NewDecoder(rd)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("разбор словаря строк: %w", err)
		}
		switch ev := tok.(type) {
		case xml.StartElement:
			switch ev.Name.Local {
			case "si":
				inSi = true
				sb.Reset()
			case "rPh":
				inRPh = true
			case "t":
				inT = inSi && !inRPh
			}
		case xml.EndElement:
			switch ev.Name.Local {
			case "si":
				out = append(out, sb.String())
				inSi = false
			case "rPh":
				inRPh = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				sb.WriteString(string(ev))
			}
		}
	}
}

// parseSheetSource перечисляет содержательные ячейки листа и слияния по его
// XML. Клетки без значения и формулы (только стиль) в эталон не входят.
func parseSheetSource(rd io.Reader, shared []string) (*sheetSource, error) {
	src := &sheetSource{}
	dec := xml.NewDecoder(rd)

	var (
		cur    srcCell
		inC    bool
		inV    bool
		inT    bool
		curRow int
		seqCol int
		vText  strings.Builder
		inline strings.Builder
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("разбор XML листа: %w", err)
		}
		switch ev := tok.(type) {
		case xml.StartElement:
			switch ev.Name.Local {
			case "row":
				curRow++
				seqCol = 0
				for _, a := range ev.Attr {
					if a.Name.Local == "r" {
						if n, err := strconv.Atoi(a.Value); err == nil {
							curRow = n
						}
					}
				}
			case "c":
				inC = true
				cur = srcCell{}
				vText.Reset()
				inline.Reset()
				for _, a := range ev.Attr {
					switch a.Name.Local {
					case "r":
						cur.ref = a.Value
					case "t":
						cur.kind = a.Value
					}
				}
			case "f":
				if inC {
					cur.hasFormula = true
				}
			case "v":
				inV = inC
			case "t":
				inT = inC
			case "mergeCell":
				for _, a := range ev.Attr {
					if a.Name.Local == "ref" {
						src.merges = append(src.merges, a.Value)
					}
				}
			}
		case xml.EndElement:
			switch ev.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				if !inC {
					continue
				}
				if cur.ref == "" {
					ref, err := excelize.CoordinatesToCellName(seqCol+1, curRow)
					if err != nil {
						return nil, fmt.Errorf("адрес клетки строки %d: %w", curRow, err)
					}
					cur.ref = ref
				}
				colLetters, row, err := excelize.SplitCellName(cur.ref)
				if err != nil {
					return nil, fmt.Errorf("некорректный адрес клетки %q: %w", cur.ref, err)
				}
				col, err := excelize.ColumnNameToNumber(colLetters)
				if err != nil {
					return nil, fmt.Errorf("некорректная колонка клетки %q: %w", cur.ref, err)
				}
				cur.row, cur.col = row, col
				seqCol = col
				cur.requireValue = cellRequires(cur, vText.String(), inline.String(), shared)
				src.cells = append(src.cells, cur)
				inC = false
			}
		case xml.CharData:
			if inV {
				vText.WriteString(string(ev))
			} else if inT {
				inline.WriteString(string(ev))
			}
		}
	}
	return src, nil
}

// cellRequires — обязано ли содержимое клетки быть непустым в модели.
// Для t="s" строка разрешается по словарю: если индекс указывает на пустую
// строку, Excel показывает пусто — потери нет.
func cellRequires(c srcCell, v, inlineText string, shared []string) bool {
	if c.hasFormula {
		return true
	}
	switch c.kind {
	case "s":
		idx, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || idx < 0 || idx >= len(shared) {
			return true
		}
		return shared[idx] != ""
	case "inlineStr":
		return inlineText != ""
	case "b":
		return v != ""
	default: // n, str, e и прочее
		return v != ""
	}
}
