package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"tenderguy/internal/parser"
	"tenderguy/internal/reqs"
	"tenderguy/internal/scanner"
)

// extractReqs формирует карточку реквизитов (P1.5): разбирает документ своим
// парсером формата (docx/xlsx/pdf — P1.2/P1.3/P1.4), по извлечённому тексту
// ищет ИНН/КПП/ОГРН/даты/суммы и проверяет контрольные числа.
func extractReqs(p *params, stdout, stderr io.Writer) int {
	targets, err := reqsTargets(p)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}
	if len(targets) == 0 {
		fmt.Fprintf(stdout, "Каталог: %s\nВ нём нет документов .docx/.xlsx/.pdf — карточек не будет.\n", p.inputDir)
		return exitOK
	}

	rep := &reqsReport{}
	for _, t := range targets {
		text, err := documentText(t.path, t.kind)
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: t.path, Error: err.Error()})
			continue
		}
		rep.Cards = append(rep.Cards, reqs.Extract(t.path, t.kind, text))
	}

	if p.format == "json" {
		if err := writeReqsJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writeReqsText(stdout, rep)
	}

	suspects := rep.suspectCount()
	if len(rep.Failed) > 0 {
		fmt.Fprintf(stderr, "tenderguy: файлов без карточки %d — см. «Ошибки разбора»; подозрительных значений %d\n",
			len(rep.Failed), suspects)
		return exitProblems
	}
	if suspects > 0 {
		fmt.Fprintf(stderr, "tenderguy: подозрительных значений %d — они помечены ✗ в отчёте\n", suspects)
	}
	return exitOK
}

// reqsTarget — файл и формат, из которого будем брать текст.
type reqsTarget struct {
	path string
	kind string
}

// reqsTargets собирает файлы для карточек: переданный явно (--file) или все
// поддерживаемые документы каталога (--input-dir, обход P1.1).
func reqsTargets(p *params) ([]reqsTarget, error) {
	if p.file != "" {
		kind := kindByExtension(p.file)
		if kind == "" {
			return nil, fmt.Errorf("формат файла %q не поддерживается — ожидаются .docx, .xlsx или .pdf",
				filepath.Base(p.file))
		}
		return []reqsTarget{{path: p.file, kind: kind}}, nil
	}
	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		return nil, err
	}
	var out []reqsTarget
	for _, e := range res.Entries {
		if e.Supported && e.Problem == "" {
			out = append(out, reqsTarget{path: e.Path, kind: string(e.Kind)})
		}
	}
	return out, nil
}

// kindByExtension определяет формат одного файла по расширению.
func kindByExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx":
		return string(scanner.KindDocx)
	case ".xlsx":
		return string(scanner.KindXlsx)
	case ".pdf":
		return string(scanner.KindPdf)
	default:
		return ""
	}
}

// documentText извлекает текст документа парсером его формата.
func documentText(path, kind string) (string, error) {
	switch kind {
	case string(scanner.KindDocx):
		doc, err := parser.ParseDocx(path)
		if err != nil {
			return "", err
		}
		return doc.Text(), nil
	case string(scanner.KindXlsx):
		doc, err := parser.ParseXlsx(path)
		if err != nil {
			return "", err
		}
		return doc.Text(), nil
	case string(scanner.KindPdf):
		doc, err := parser.ParsePdf(path)
		if err != nil {
			return "", err
		}
		return doc.Text(), nil
	default:
		return "", fmt.Errorf("неизвестный формат %q", kind)
	}
}

// reqsReport — результат команды extract-reqs.
type reqsReport struct {
	Cards  []*reqs.Card `json:"cards"`
	Failed []failedDoc  `json:"failed,omitempty"`
}

func (r *reqsReport) suspectCount() int {
	n := 0
	for _, c := range r.Cards {
		n += c.Suspect
	}
	return n
}

// kindLabels — человекочитаемые имена видов реквизитов для текстового отчёта.
var kindLabels = []struct{ kind, label string }{
	{reqs.KindINN10, "ИНН(10)"},
	{reqs.KindINN12, "ИНН(12)"},
	{reqs.KindKPP, "КПП"},
	{reqs.KindOGRN, "ОГРН"},
	{reqs.KindOGRNIP, "ОГРНИП"},
	{reqs.KindDate, "дата"},
	{reqs.KindMoney, "сумма"},
}

func kindLabel(kind string) string {
	for _, kl := range kindLabels {
		if kl.kind == kind {
			return kl.label
		}
	}
	return kind
}

func writeReqsJSON(w io.Writer, rep *reqsReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта реквизитов: %w", err)
	}
	return nil
}

func writeReqsText(w io.Writer, rep *reqsReport) {
	cardsWithReqs := 0
	for _, c := range rep.Cards {
		if c.Total > 0 {
			cardsWithReqs++
		}
	}
	fmt.Fprintf(w, "Карточек: %d (с реквизитами %d), файлов без карточки %d, подозрительных значений %d\n",
		len(rep.Cards), cardsWithReqs, len(rep.Failed), rep.suspectCount())

	for _, c := range rep.Cards {
		fmt.Fprintf(w, "\n%s (%s)\n", c.Name, c.Format)
		if c.Total == 0 {
			fmt.Fprintln(w, "  реквизитов не найдено")
		}
		for _, it := range c.Items {
			mark := " "
			if !it.Valid {
				mark = "✗"
			}
			line := fmt.Sprintf("  %s %s %s", mark, kindLabel(it.Kind), it.Value)
			if it.Hits > 1 {
				line += fmt.Sprintf(" ×%d", it.Hits)
			}
			if it.Label != "" {
				line += fmt.Sprintf(" [метка: %s]", it.Label)
			}
			if !it.Valid {
				line += fmt.Sprintf(" — %s", it.Reason)
			}
			fmt.Fprintln(w, line)
		}
		for _, note := range c.Notes {
			fmt.Fprintf(w, "  Замечание: %s\n", note)
		}
	}

	if len(rep.Failed) > 0 {
		fmt.Fprintln(w, "\nОшибки разбора:")
		for _, f := range rep.Failed {
			fmt.Fprintf(w, "  ! %s — %s\n", filepath.Base(f.Path), f.Error)
		}
	}
}
