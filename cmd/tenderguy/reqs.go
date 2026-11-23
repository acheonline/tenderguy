package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"tenderguy/internal/reqs"
	"tenderguy/internal/scanner"
)

// extractReqs формирует карточку реквизитов (P1.5): разбирает документ своим
// парсером формата (docx/xlsx/pdf — P1.2/P1.3/P1.4) на фрагменты с адресами
// (P1.6), по ним ищет ИНН/КПП/ОГРН/даты/суммы, а с P2.2 — наименования и
// юридические адреса по меткам полей формы, проверяет найденное и запоминает,
// где в источнике стоит каждое значение.
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
		frags, err := documentFragments(t.path, t.kind)
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: t.path, Error: err.Error()})
			continue
		}
		rep.Cards = append(rep.Cards, reqs.Extract(t.path, t.kind, frags))
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
	if untraced := rep.untracedCount(); untraced > 0 {
		// Значение без адреса — это дыра в парсере, а не свойство документа:
		// замечание P2 по такому значению некуда показать.
		fmt.Fprintf(stderr, "tenderguy: значений без адреса в источнике %d — они помечены @ БЕЗ АДРЕСА в отчёте\n", untraced)
		return exitProblems
	}
	if suspects > 0 {
		fmt.Fprintf(stderr, "tenderguy: подозрительных значений %d — они помечены ✗ в отчёте\n", suspects)
	}
	return exitOK
}

// reqsTarget — файл и формат, из которого будем брать содержимое.
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

// untracedCount — значений без адреса в источнике. По критерию P1.6 их должно
// быть ноль: непустой счёт означает, что парсер формата отдал фрагмент без
// координат, то есть дыру в разборе, а не свойство документа.
func (r *reqsReport) untracedCount() int {
	n := 0
	for _, c := range r.Cards {
		n += c.Total - c.Traced
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
	{reqs.KindOrgName, "наименование"},
	{reqs.KindAddress, "адрес"},
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
	fmt.Fprintf(w, "Карточек: %d (с реквизитами %d), файлов без карточки %d, подозрительных значений %d, значений без адреса %d\n",
		len(rep.Cards), cardsWithReqs, len(rep.Failed), rep.suspectCount(), rep.untracedCount())

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
			line += refsNote(it)
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

// refsNote — адрес значения в источнике для строки отчёта. Файл назван в шапке
// карточки, поэтому показывается только место в нём; если адресов несколько,
// второй и далее сворачиваются в счётчик — строка отчёта обязана оставаться
// одной строкой.
func refsNote(it reqs.Item) string {
	if len(it.Refs) == 0 {
		return " @ БЕЗ АДРЕСА"
	}
	note := " @ " + it.Refs[0].Where()
	if extra := it.RefTotal - 1; extra > 0 {
		note += fmt.Sprintf(" (+%d)", extra)
	}
	return note
}
