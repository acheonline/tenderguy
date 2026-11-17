// Package parser извлекает содержимое документов (текст и таблицы) в единую
// модель, пригодную для шагов конвейера P1 дальше: извлечения реквизитов (P1.5),
// трассировки фрагментов (P1.6), замера качества (P1.7) и сохранения (P1.8).
//
// Задача P1.2 — разбор .docx: ParseDocx читает OOXML-документ Word 2007+ и
// возвращает абзацы и таблицы вместе с самопроверкой полноты извлечения
// (Coverage).
package parser

import (
	"strings"
	"unicode/utf8"

	"tenderguy/internal/trace"
)

// Форматы содержимого, которые умеет разбирать конвейер P1. Имена заданы в
// пакете trace: по ним адрес фрагмента выбирает правила показа.
const (
	FormatDocx = trace.FormatDocx
)

// BlockType — вид блочного элемента содержимого.
type BlockType string

const (
	BlockParagraph BlockType = "paragraph"
	BlockTable     BlockType = "table"
)

// Cell — ячейка таблицы.
type Cell struct {
	// Text — текст абзацев ячейки, объединённых переводом строки. Содержимое
	// вложенных таблиц здесь не дублируется: оно остаётся в Tables.
	Text string `json:"text"`
	// Tables — вложенные таблицы (в Word они легальны внутри ячейки).
	Tables []*Table `json:"tables,omitempty"`
	// ColSpan — сколько колонок сетки занимает ячейка (w:gridSpan), 0 означает 1.
	ColSpan int `json:"col_span,omitempty"`
	// RowSpan — сколько строк занимает ячейка при вертикальном слиянии (w:vMerge),
	// 0 — слияния нет.
	RowSpan int `json:"row_span,omitempty"`
	// Merged — ячейка продолжение вертикального слияния: Word показывает её пустой.
	// Текст, если он там всё-таки лежит, сохранён в Text и участвует в проверке полноты.
	Merged bool `json:"merged,omitempty"`
}

// Row — строка таблицы.
type Row struct {
	// Index — номер строки в таблице, начиная с 1.
	Index int    `json:"index"`
	Cells []Cell `json:"cells"`
	// Page — страница по последней отрисовке Word, на которой строка началась;
	// таблица может переходить через разрыв, поэтому у строк она своя.
	Page int `json:"page,omitempty"`
	// Header — строка размечена как заголовок таблицы (w:tblHeader), повторяется на каждой странице.
	Header bool `json:"header,omitempty"`
}

// Table — таблица, в том числе вложенная в ячейку другой таблицы.
type Table struct {
	Rows []Row `json:"rows"`
	// Columns — ширина сетки по самой длинной строке (с учётом w:gridSpan).
	Columns int `json:"columns"`
	// Depth — уровень вложенности: 0 у таблицы в теле документа.
	Depth int `json:"depth,omitempty"`
	// Style — стиль таблицы (w:tblStyle), например "TableGrid".
	Style string `json:"style,omitempty"`
}

// Block — элемент верхнего уровня содержимого: абзац или таблица.
type Block struct {
	// Index — порядковый номер блока в документе, начиная с 1. Это координата
	// «документ + параграф», на которую ссылается трассировка замечаний (P1.6).
	Index int       `json:"index"`
	Type  BlockType `json:"type"`
	// Page — страница по последней отрисовке Word (отметки w:lastRenderedPageBreak
	// и явные разрывы w:br type="page"). У .docx это свойство рендера, а не файла:
	// Word пересчитывает раскладку при открытии, поэтому надёжнее Index.
	// 0 — отметок разбивки в файле нет, страница неизвестна.
	Page int `json:"page,omitempty"`
	// Text — текст абзаца. Для таблицы пусто: её содержимое в Table.
	Text string `json:"text,omitempty"`
	// Style — стиль абзаца (w:pStyle): по нему видно, где заголовок, а где текст.
	Style string `json:"style,omitempty"`
	Table *Table `json:"table,omitempty"`
}

// Document — результат разбора одного файла.
type Document struct {
	Path   string  `json:"path"`
	Name   string  `json:"name"`
	Format string  `json:"format"`
	Blocks []Block `json:"blocks"`

	Paragraphs int `json:"paragraphs"`
	Tables     int `json:"tables"`
	Rows       int `json:"rows"`
	Cells      int `json:"cells"`
	// Runes — суммарный объём извлечённого текста в символах.
	Runes int `json:"runes"`

	Coverage Coverage `json:"coverage"`
	// Notes — замечания разбора: что в файле нашлось такого, что в модель не вошло.
	Notes []string `json:"notes,omitempty"`
}

// Coverage — самопроверка полноты извлечения.
//
// Критерий P1.2 сформулирован как «извлечено без потерь», поэтому он должен быть
// измерим по самому файлу, а не «на глаз». Эталон берётся из document.xml:
// это все текстовые фрагменты w:t и m:t в документном порядке. Каждый из них
// обязан найтись в извлечённой модели — иначе содержимое потеряно.
type Coverage struct {
	// SourcePieces — число текстовых фрагментов в document.xml.
	SourcePieces int `json:"source_pieces"`
	// SourceRunes — их суммарный объём в символах.
	SourceRunes int `json:"source_runes"`
	// TextRunes — объём извлечённого текста в модели.
	TextRunes int `json:"text_runes"`
	// MissingPieces — фрагменты исходника, которых нет в извлечённом тексте.
	MissingPieces int `json:"missing_pieces"`
	// OutOfOrder — фрагменты, найденные вне документного порядка: текст на месте,
	// последовательность разбира расходится (например таблица внутри абзаца).
	OutOfOrder int `json:"out_of_order"`
	// Missing — первые потерянные фрагменты, чтобы сразу понять, где искать.
	Missing []string `json:"missing,omitempty"`

	// SourceTables/SourceRows/SourceCells — структура исходника, для сверки с моделью.
	SourceTables int `json:"source_tables"`
	SourceRows   int `json:"source_rows"`
	SourceCells  int `json:"source_cells"`

	// Complete — текст извлечён без потерь.
	Complete bool `json:"complete"`
}

// paragraphBlocks — число абзацев в списке блоков.
func paragraphBlocks(blocks []Block) int {
	n := 0
	for _, b := range blocks {
		if b.Table == nil {
			n++
		}
	}
	return n
}

// Text возвращает извлечённое содержимое целиком: абзацы и таблицы в документном
// порядке, ячейки разделены табуляцией, строки — переводом строки.
// Ячейки, закрытые вертикальным слиянием, в вывод не входят (Word их не показывает);
// они сохранены в модели и учитываются проверкой полноты.
func (d *Document) Text() string {
	return renderBlocks(d.Blocks, false, true)
}

// extractedBlob — тот же текст, но со всеми сохранёнными фрагментами модели и
// без обрезки пробелов по краям ячеек. Служит «иглой» для проверки полноты:
// сверять нужно с тем, что парсер донёс, а не с тем, что он решил показать
// человеку. Обрезка граничных пробелов в показе законна, но фрагмент исходника
// вроде «…36-89 + неразрывный пробел» тогда не находится дословно и ложно
// считается потерянным.
func (d *Document) extractedBlob() string {
	return renderBlocks(d.Blocks, true, false)
}

// Complete — признак «потерь нет»: им же проверяется готовность задачи P1.2.
func (d *Document) Complete() bool {
	return d.Coverage.Complete
}

// renderBlocks собирает текст из блоков. includeMerged — включать ли ячейки,
// закрытые вертикальным слиянием; trim — обрезать ли пробелы по краям ячеек
// (показ человеку — да, сверка полноты — нет).
func renderBlocks(blocks []Block, includeMerged, trim bool) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Table != nil {
			sb.WriteString(renderTable(*b.Table, includeMerged, trim))
		} else {
			sb.WriteString(b.Text)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderTable(t Table, includeMerged, trim bool) string {
	lines := make([]string, 0, len(t.Rows))
	for _, row := range t.Rows {
		cells := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			if cell.Merged && !includeMerged {
				cells = append(cells, "")
				continue
			}
			text := cell.Text
			for _, nested := range cell.Tables {
				text += "\n" + renderTable(*nested, includeMerged, trim)
			}
			cells = append(cells, flattenLines(text, trim))
		}
		lines = append(lines, strings.Join(cells, "\t"))
	}
	return strings.Join(lines, "\n")
}

// flattenLines сводит многострочный текст ячейки в одну строку табличного вывода,
// чтобы строка таблицы всегда соответствовала одной строке текста. Обрезка краёв —
// только для показа: проверка полноты ищет фрагменты исходника дословно.
func flattenLines(text string, trim bool) string {
	if trim {
		text = strings.TrimSpace(text)
	}
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(text)
}

func runeCount(parts ...string) int {
	n := 0
	for _, p := range parts {
		n += utf8.RuneCountInString(p)
	}
	return n
}
