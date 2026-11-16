package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"tenderguy/internal/parser"
	"tenderguy/internal/scanner"
)

// parsePdf разбирает PDF: --file с одним файлом или все .pdf каталога
// (--input-dir), найденные обходом. Страницы с текстовым слоем читаются
// напрямую, скановые отдаются OCR (P1.4).
func parsePdf(p *params, stdout, stderr io.Writer) int {
	paths, err := pdfPaths(p)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}
	if len(paths) == 0 {
		fmt.Fprintf(stdout, "Каталог: %s\nВ нём нет .pdf — разбирать нечего.\n", p.inputDir)
		return exitOK
	}

	rep := &pdfReport{}
	for _, path := range paths {
		doc, err := parser.ParsePdf(path)
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: path, Error: err.Error()})
			continue
		}
		item := pdfDocument{PdfDocument: doc}
		if p.withText {
			item.Content = doc.Text()
		}
		rep.Documents = append(rep.Documents, item)
	}

	if p.format == "json" {
		if err := writePDFJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writePDFText(stdout, rep, p.withText)
	}

	lost := rep.lostCount()
	if len(rep.Failed) > 0 || lost > 0 {
		fmt.Fprintf(stderr, "tenderguy: разборов с ошибками %d, с потерями %d — см. отчёт\n", len(rep.Failed), lost)
		return exitProblems
	}
	return exitOK
}

// pdfPaths собирает .pdf для разбора: переданный явно или все из обхода каталога.
func pdfPaths(p *params) ([]string, error) {
	if p.file != "" {
		return []string{p.file}, nil
	}
	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range res.Entries {
		if e.Kind == scanner.KindPdf && e.Problem == "" {
			paths = append(paths, e.Path)
		}
	}
	return paths, nil
}

// pdfReport — результат команды parse-pdf.
type pdfReport struct {
	Documents []pdfDocument `json:"documents"`
	Failed    []failedDoc   `json:"failed,omitempty"`
}

// pdfDocument — документ вместе с извлечённым текстом (поле content появляется
// только при --with-text, как в parse-docx).
type pdfDocument struct {
	*parser.PdfDocument
	Content string `json:"content,omitempty"`
}

// lostCount — число документов, где хоть одна страница осталась пустой.
func (r *pdfReport) lostCount() int {
	n := 0
	for _, item := range r.Documents {
		if !item.Complete {
			n++
		}
	}
	return n
}

func writePDFJSON(w io.Writer, rep *pdfReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта разбора pdf: %w", err)
	}
	return nil
}

func writePDFText(w io.Writer, rep *pdfReport, withText bool) {
	complete := len(rep.Documents) - rep.lostCount()
	fmt.Fprintf(w, "Разобрано PDF: %d — без потерь %d, с потерями %d, с ошибками %d\n",
		len(rep.Documents), complete, rep.lostCount(), len(rep.Failed))

	for _, item := range rep.Documents {
		doc := item.PdfDocument
		fmt.Fprintf(w, "\n%s\n", doc.Name)
		fmt.Fprintf(w, "  Страниц %d (текстовый слой %d, OCR %d), символов %d\n",
			len(doc.Pages), doc.TextPages, doc.OcrPages, doc.Runes)
		verdict := "без потерь"
		if !doc.Complete {
			verdict = fmt.Sprintf("ПОТЕРИ: пустых страниц %d", doc.EmptyPages)
		}
		fmt.Fprintf(w, "  %s\n", verdict)
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
