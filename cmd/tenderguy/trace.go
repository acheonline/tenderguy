package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"tenderguy/internal/parser"
	"tenderguy/internal/reqs"
	"tenderguy/internal/scanner"
	"tenderguy/internal/trace"
)

// documentFragments извлекает содержимое документа фрагментами с адресами —
// общий вход команд extract-reqs (P1.5) и trace (P1.6). Парсер формата
// выбирается по классификации обхода P1.1, текст без координат наружу не
// отдаётся: именно это делает трассируемость свойством конвейера, а не отчёта.
func documentFragments(path, kind string) ([]trace.Fragment, error) {
	switch kind {
	case string(scanner.KindDocx):
		doc, err := parser.ParseDocx(path)
		if err != nil {
			return nil, err
		}
		return doc.Fragments(), nil
	case string(scanner.KindXlsx):
		doc, err := parser.ParseXlsx(path)
		if err != nil {
			return nil, err
		}
		return doc.Fragments(), nil
	case string(scanner.KindPdf):
		doc, err := parser.ParsePdf(path)
		if err != nil {
			return nil, err
		}
		return doc.Fragments(), nil
	default:
		return nil, fmt.Errorf("неизвестный формат %q", kind)
	}
}

// traceFile — трассируемость одного файла: сколько фрагментов и значений и у
// скольких из них есть адрес в источнике.
type traceFile struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Format string `json:"format"`

	Stats trace.Stats `json:"fragments"`

	// Values — различных реквизитов в карточке файла, ValuesTraced — из них с адресом.
	Values       int `json:"values"`
	ValuesTraced int `json:"values_traced"`

	// Content — список фрагментов с адресами; появляется только при --with-text,
	// иначе отчёт каталога раздувается до размера самих документов.
	Content []trace.Fragment `json:"content,omitempty"`
}

func (f traceFile) missing() int {
	return f.Stats.Missing() + (f.Values - f.ValuesTraced)
}

// traceReport — результат команды trace.
type traceReport struct {
	Files  []traceFile `json:"files"`
	Totals trace.Stats `json:"totals"`
	Failed []failedDoc `json:"failed,omitempty"`

	Values       int `json:"values"`
	ValuesTraced int `json:"values_traced"`
}

func (r *traceReport) missing() int {
	n := 0
	for _, f := range r.Files {
		n += f.missing()
	}
	return n
}

// traceCmd проверяет критерий P1.6 — «каждый фрагмент имеет ссылку
// „документ + страница/пункт“»: обходит вход, раскладывает каждый документ на
// фрагменты с адресами и считает, сколько из них адреса не получили. Значения
// реквизитов считаются тем же прогоном: замечание P2 строится по ним, и адрес
// обязан быть у каждого.
func traceCmd(p *params, stdout, stderr io.Writer) int {
	targets, err := reqsTargets(p)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}
	if len(targets) == 0 {
		fmt.Fprintf(stdout, "Каталог: %s\nВ нём нет документов .docx/.xlsx/.pdf — трассировать нечего.\n", p.inputDir)
		return exitOK
	}

	rep := &traceReport{}
	for _, t := range targets {
		frags, err := documentFragments(t.path, t.kind)
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: t.path, Error: err.Error()})
			continue
		}
		item := traceFile{Path: t.path, Name: filepath.Base(t.path), Format: t.kind}
		for _, frag := range frags {
			rep.Totals.Add(frag)
			item.Stats.Add(frag)
		}
		if p.withText {
			item.Content = frags
		}
		// Карточка строится тем же набором фрагментов: адрес значения должен
		// сойтись с адресом фрагмента, иначе сверка P2 укажет в никуда.
		card := reqs.Extract(t.path, t.kind, frags)
		item.Values, item.ValuesTraced = card.Total, card.Traced
		rep.Values += card.Total
		rep.ValuesTraced += card.Traced
		rep.Files = append(rep.Files, item)
	}

	if p.format == "json" {
		if err := writeTraceJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writeTraceText(stdout, rep, p.withText)
	}

	if len(rep.Failed) > 0 {
		fmt.Fprintf(stderr, "tenderguy: файлов без отчёта %d — см. «Ошибки разбора»\n", len(rep.Failed))
		return exitProblems
	}
	if missing := rep.missing(); missing > 0 {
		fmt.Fprintf(stderr, "tenderguy: фрагментов и значений без адреса %d — 100 %% трассируемость не достигнута\n", missing)
		return exitProblems
	}
	return exitOK
}

func writeTraceJSON(w io.Writer, rep *traceReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта трассировки: %w", err)
	}
	return nil
}

func writeTraceText(w io.Writer, rep *traceReport, withText bool) {
	fmt.Fprintf(w, "Файлов: %d, фрагментов с содержимым %d, с адресом %d (%.1f %%), без адреса %d\n",
		len(rep.Files), rep.Totals.Fragments, rep.Totals.Traced, rep.Totals.Share()*100, rep.Totals.Missing())
	fmt.Fprintf(w, "Значений реквизитов: %d, с адресом %d (%.1f %%)\n",
		rep.Values, rep.ValuesTraced, shareOf(rep.ValuesTraced, rep.Values))
	verdict := "100 % трассируемость достигнута"
	if rep.missing() > 0 {
		verdict = fmt.Sprintf("трассируемость неполная: без адреса %d", rep.missing())
	}
	fmt.Fprintf(w, "Вердикт: %s\n", verdict)

	for _, f := range rep.Files {
		fmt.Fprintf(w, "\n%s (%s)\n", f.Name, f.Format)
		fmt.Fprintf(w, "  Фрагментов %d (с адресом %d), значений %d (с адресом %d)\n",
			f.Stats.Fragments, f.Stats.Traced, f.Values, f.ValuesTraced)
		for _, s := range f.Stats.Untraced {
			fmt.Fprintf(w, "  Без адреса: %q\n", s)
		}
		if withText {
			for _, frag := range f.Content {
				// У строки таблицы адрес показывается по клеткам: именно так его
				// увидит значение из extract-reqs, а не «вся строка целиком».
				if len(frag.Segments) == 0 {
					fmt.Fprintf(w, "  %-26s %s\n", frag.Ref.Where(), oneLine(frag.Text))
					continue
				}
				for _, s := range frag.Segments {
					fmt.Fprintf(w, "  %-26s %s\n", s.Ref.Where(), oneLine(frag.Text[s.Start:s.End]))
				}
			}
		}
	}

	if len(rep.Failed) > 0 {
		fmt.Fprintln(w, "\nОшибки разбора:")
		for _, f := range rep.Failed {
			fmt.Fprintf(w, "  ! %s — %s\n", filepath.Base(f.Path), f.Error)
		}
	}
}

// traceSampleRunes — длина фрагмента в текстовом списке: дальше идут другие
// строки, а адрес и начало строки достаточно отличают её от соседних.
const traceSampleRunes = 72

// oneLine — фрагмент в одну строку отчёта: многострочный абзац или страница
// целиком в выводе бесполезны, а адрес от этого не меняется.
func oneLine(text string) string {
	r := []rune(text)
	for i, ch := range r {
		if ch == '\n' || ch == '\t' {
			r = r[:i]
			break
		}
	}
	if len(r) > traceSampleRunes {
		return string(r[:traceSampleRunes]) + "…"
	}
	return string(r)
}

func shareOf(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
