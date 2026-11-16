package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"tenderguy/internal/parser"
	"tenderguy/internal/scanner"
)

// parseXlsx разбирает .xlsx: --file с одной книгой или все .xlsx каталога
// (--input-dir), найденные обходом. Значения ячеек и формулы читаются
// excelize, вердикт «без потерь» считается по XML-эталону книги (P1.3).
func parseXlsx(p *params, stdout, stderr io.Writer) int {
	paths, err := xlsxPaths(p)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}
	if len(paths) == 0 {
		fmt.Fprintf(stdout, "Каталог: %s\nВ нём нет .xlsx — разбирать нечего.\n", p.inputDir)
		return exitOK
	}

	rep := &xlsxReport{}
	for _, path := range paths {
		doc, err := parser.ParseXlsx(path)
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: path, Error: err.Error()})
			continue
		}
		item := xlsxDocument{XlsxDocument: doc}
		if p.withText {
			item.Content = doc.Text()
		}
		rep.Documents = append(rep.Documents, item)
	}

	if p.format == "json" {
		if err := writeXlsxJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writeXlsxText(stdout, rep, p.withText)
	}

	lost := rep.lostCount()
	if len(rep.Failed) > 0 || lost > 0 {
		fmt.Fprintf(stderr, "tenderguy: разборов с ошибками %d, с потерями %d — см. отчёт\n", len(rep.Failed), lost)
		return exitProblems
	}
	return exitOK
}

// xlsxPaths собирает книги для разбора: переданную явно или все .xlsx из обхода каталога.
func xlsxPaths(p *params) ([]string, error) {
	if p.file != "" {
		return []string{p.file}, nil
	}
	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range res.Entries {
		if e.Kind == scanner.KindXlsx && e.Problem == "" {
			paths = append(paths, e.Path)
		}
	}
	return paths, nil
}

// xlsxReport — результат команды parse-xlsx.
type xlsxReport struct {
	Documents []xlsxDocument `json:"documents"`
	Failed    []failedDoc    `json:"failed,omitempty"`
}

// xlsxDocument — книга вместе с извлечённым содержимым (поле content
// появляется только при --with-text, как в parse-docx).
type xlsxDocument struct {
	*parser.XlsxDocument
	Content string `json:"content,omitempty"`
}

// lostCount — число книг, где хотя бы одна содержательная ячейка исходника
// не дошла до модели.
func (r *xlsxReport) lostCount() int {
	n := 0
	for _, item := range r.Documents {
		if !item.Complete() {
			n++
		}
	}
	return n
}

func writeXlsxJSON(w io.Writer, rep *xlsxReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта разбора xlsx: %w", err)
	}
	return nil
}

func writeXlsxText(w io.Writer, rep *xlsxReport, withText bool) {
	complete := len(rep.Documents) - rep.lostCount()
	fmt.Fprintf(w, "Разобрано .xlsx: %d — без потерь %d, с потерями %d, с ошибками %d\n",
		len(rep.Documents), complete, rep.lostCount(), len(rep.Failed))

	for _, item := range rep.Documents {
		doc := item.XlsxDocument
		fmt.Fprintf(w, "\n%s\n", doc.Name)
		fmt.Fprintf(w, "  Листов %d, строк %d, ячеек %d (из исходника %d), формул %d, символов %d\n",
			len(doc.Sheets), doc.Rows, doc.Cells, doc.Coverage.SourceCells, doc.Formulas, doc.Runes)
		for _, sh := range doc.Sheets {
			flags := ""
			if sh.Hidden {
				flags = ", скрыт"
			}
			fmt.Fprintf(w, "  лист «%s»: строк %d, колонок %d, ячеек %d, формул %d, слияний %d%s\n",
				sh.Name, len(sh.Rows), sh.Columns, sh.Cells, sh.Formulas, len(sh.Merges), flags)
		}
		verdict := "без потерь"
		if !doc.Complete() {
			verdict = fmt.Sprintf("ПОТЕРИ: ячеек не дошло %d", doc.Coverage.MissingCells)
		}
		fmt.Fprintf(w, "  %s\n", verdict)
		for _, ref := range doc.Coverage.Missing {
			fmt.Fprintf(w, "    потеря: %s\n", ref)
		}
		for _, note := range doc.Notes {
			fmt.Fprintf(w, "  Замечание: %s\n", note)
		}
		if withText {
			fmt.Fprintf(w, "\n--- содержимое ---\n%s\n--- конец содержимого ---\n", doc.Text())
		}
	}

	if len(rep.Failed) > 0 {
		fmt.Fprintln(w, "\nОшибки разбора:")
		for _, f := range rep.Failed {
			fmt.Fprintf(w, "  ! %s — %s\n", filepath.Base(f.Path), f.Error)
		}
	}
}
