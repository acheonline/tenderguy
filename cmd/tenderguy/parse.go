package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"tenderguy/internal/parser"
	"tenderguy/internal/scanner"
)

// Команды CLI. Значения совпадают с именами шагов P1: scan — обход (P1.1),
// parse-docx — извлечение текста и таблиц (P1.2), parse-xlsx — ячейки и
// формулы книг Excel (P1.3), parse-pdf — текстовый слой и OCR сканов (P1.4),
// extract-reqs — карточка реквизитов поверх парсеров (P1.5), trace — адреса
// фрагментов в источнике и проверка 100 % трассируемости (P1.6), dump-docs —
// сохранение всех извлечённых данных в docs.json (P1.8).
const (
	commandScan        = "scan"
	commandParseDocx   = "parse-docx"
	commandParseXlsx   = "parse-xlsx"
	commandParsePdf    = "parse-pdf"
	commandExtractReqs = "extract-reqs"
	commandTrace       = "trace"
	commandDumpDocs    = "dump-docs"
)

// allCommands — что принимает --command: список идёт и в справку, и в
// сообщение об ошибке, чтобы оно не разъезжалось с реальным набором команд.
var allCommands = []string{commandScan, commandParseDocx, commandParseXlsx, commandParsePdf, commandExtractReqs, commandTrace, commandDumpDocs}

// parseCommands — команды разбора содержимого (кроме scan): у них вход задаёт
// --file или --input-dir, а --with-text печатает извлечённый текст.
var parseCommands = []string{commandParseDocx, commandParseXlsx, commandParsePdf, commandTrace}

// parseDocx разбирает .docx: одним файлом (--file) или всеми .docx каталога
// (--input-dir), найденными обходом.
func parseDocx(p *params, stdout, stderr io.Writer) int {
	paths, err := docxPaths(p)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}
	if len(paths) == 0 {
		fmt.Fprintf(stdout, "Каталог: %s\nВ нём нет .docx — разбирать нечего.\n", p.inputDir)
		return exitOK
	}

	rep := &parseReport{}
	for _, path := range paths {
		doc, err := parser.ParseDocx(path)
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: path, Error: err.Error()})
			continue
		}
		item := parseDocument{Document: doc}
		if p.withText {
			item.Content = doc.Text()
		}
		rep.Documents = append(rep.Documents, item)
	}

	if p.format == "json" {
		if err := writeParseJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writeParseText(stdout, rep, p.withText)
	}

	lost := rep.lostCount()
	if len(rep.Failed) > 0 || lost > 0 {
		fmt.Fprintf(stderr, "tenderguy: разборов с ошибками %d, с потерями %d — см. отчёт\n", len(rep.Failed), lost)
		return exitProblems
	}
	return exitOK
}

// docxPaths собирает файлы для разбора: переданный явно или все .docx из обхода каталога.
func docxPaths(p *params) ([]string, error) {
	if p.file != "" {
		return []string{p.file}, nil
	}
	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range res.Entries {
		if e.Kind == scanner.KindDocx && e.Problem == "" {
			paths = append(paths, e.Path)
		}
	}
	return paths, nil
}

// parseReport — результат команды parse-docx.
type parseReport struct {
	Documents []parseDocument `json:"documents"`
	Failed    []failedDoc     `json:"failed,omitempty"`
}

// parseDocument — документ вместе с извлечённым текстом. Поле content появляется
// только при --with-text, поэтому обычный json-отчёт остаётся маленьким.
// Поле называется Content, а не Text: короткое имя перекрывало бы метод Text()
// вложенной модели.
type parseDocument struct {
	*parser.Document
	Content string `json:"content,omitempty"`
}

type failedDoc struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// lostCount — число документов, где извлечение оказалось с потерями.
func (r *parseReport) lostCount() int {
	n := 0
	for _, item := range r.Documents {
		if !item.Complete() {
			n++
		}
	}
	return n
}

func writeParseJSON(w io.Writer, rep *parseReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта разбора: %w", err)
	}
	return nil
}

func writeParseText(w io.Writer, rep *parseReport, withText bool) {
	fmt.Fprintf(w, "Разобрано .docx: %d — без потерь %d, с потерями %d, с ошибками %d\n",
		len(rep.Documents), len(rep.Documents)-rep.lostCount(), rep.lostCount(), len(rep.Failed))

	for _, item := range rep.Documents {
		doc := item.Document
		fmt.Fprintf(w, "\n%s\n", doc.Name)
		fmt.Fprintf(w, "  Абзацев %d, таблиц %d (строк %d, ячеек %d), символов %d\n",
			doc.Paragraphs, doc.Tables, doc.Rows, doc.Cells, doc.Runes)
		cov := doc.Coverage
		verdict := "без потерь"
		if !cov.Complete {
			verdict = fmt.Sprintf("ПОТЕРИ: фрагментов %d", cov.MissingPieces)
		}
		fmt.Fprintf(w, "  Фрагментов исходника %d (%d символов), вне порядка %d — %s\n",
			cov.SourcePieces, cov.SourceRunes, cov.OutOfOrder, verdict)
		if doc.Tables != cov.SourceTables || doc.Rows != cov.SourceRows || doc.Cells != cov.SourceCells {
			// Структура должна сходиться с исходником в обе стороны: расхождение
			// значит, что разбор потерял таблицу либо придумал её.
			fmt.Fprintf(w, "  Расхождение структуры: модель %d/%d/%d, файл %d/%d/%d\n",
				doc.Tables, doc.Rows, doc.Cells, cov.SourceTables, cov.SourceRows, cov.SourceCells)
		}
		for _, piece := range cov.Missing {
			fmt.Fprintf(w, "  Потеряно: %q\n", piece)
		}
		for _, note := range doc.Notes {
			fmt.Fprintf(w, "  Замечание: %s\n", note)
		}
		if withText {
			fmt.Fprintf(w, "\n--- текст ---\n%s\n--- конец текста ---\n", doc.Text())
		}
	}

	if len(rep.Failed) > 0 {
		fmt.Fprintln(w, "\nОшибки разбора:")
		for _, f := range rep.Failed {
			fmt.Fprintf(w, "  ! %s — %s\n", filepath.Base(f.Path), f.Error)
		}
	}
}
