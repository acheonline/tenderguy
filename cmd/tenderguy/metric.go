package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"

	"tenderguy/internal/parser"
	"tenderguy/internal/scanner"
)

// metricThreshold — критерий H1 (poc.md): доля документов пакета, пригодных
// для проверок после парсинга, обязана быть не ниже 80 %. Ниже — код выхода 1.
const metricThreshold = 80.0

// metricFormat — результат H1 по одному формату.
type metricFormat struct {
	Format  string  `json:"format"`
	Total   int     `json:"total"`
	Parsed  int     `json:"parsed"`
	Success float64 `json:"success_percent"`
	Lost    int     `json:"lost"`
	// LossesMeasured — измеряются ли потери содержимого: у *parser.PdfDocument
	// нет Complete(), поэтому для pdf всегда false. Ноль потерь у pdf не значит
	// «потерь нет», и отчёт обязан показывать это явно, а не молча.
	LossesMeasured bool `json:"losses_measured"`
}

// metricReport — результат команды metric (P1.7).
type metricReport struct {
	InputDir  string `json:"input_dir"`
	Total     int    `json:"total"`
	Supported int    `json:"supported"`
	Other     int    `json:"other"`
	Parsed    int    `json:"parsed"`

	// Success — метрика H1: доля успешно разобранных поддерживаемых
	// документов пакета, в процентах с одной десятичной цифрой.
	Success   float64 `json:"success_percent"`
	Threshold float64 `json:"threshold_percent"`
	Reached   bool    `json:"threshold_reached"`

	// Lost — справочно: разборы с потерями содержимого (docx/xlsx). На H1 не
	// влияет: успех — это разбор без ошибки, а потери pdf не измеряются.
	Lost              int            `json:"lost"`
	PdfLossesMeasured bool           `json:"pdf_losses_measured"`
	Formats           []metricFormat `json:"formats"`
	Failed            []failedDoc    `json:"failed,omitempty"`
}

// metricCmd считает метрику H1 (P1.7): обходит каталог --input-dir, разбирает
// каждый поддерживаемый документ (.docx/.xlsx/.pdf) парсером его формата и
// показывает долю успешных разборов против порога 80 %. Знаменатель — все
// поддерживаемые записи обхода, включая найденные с problem (недоступный
// файл): он считается неудачей без попытки разбора, как в dump-docs (P1.8).
// Коды выхода: 0 — порог достигнут (или измерять нечего), 1 — H1 ниже порога,
// 2 — ошибка в флагах (проверяет checkFlags), 3 — неверный путь.
func metricCmd(p *params, stdout, stderr io.Writer) int {
	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}

	rep, stats := collectMetric(res)
	if rep.Supported == 0 {
		// Пустой пакет — не провал метрики: измерять нечего, порог не нарушен.
		fmt.Fprintf(stdout, "Каталог: %s\nВ нём нет документов .docx/.xlsx/.pdf — нечего измерять.\n", rep.InputDir)
		return exitOK
	}

	success := shareOf(rep.Parsed, rep.Supported)
	rep.Success = round1(success)
	// Порог сравнивается с неокруглённым значением: 79.96 % округлились бы
	// к «80.0» и ложно прошли критерий.
	rep.Reached = success >= metricThreshold
	rep.Formats = formatStats(stats)

	if p.format == "json" {
		if err := writeMetricJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writeMetricText(stdout, rep)
	}

	if !rep.Reached {
		fmt.Fprintf(stderr, "tenderguy: H1 %.1f %% ниже порога %.0f %% — не разобрано документов %d\n",
			rep.Success, metricThreshold, rep.Supported-rep.Parsed)
		return exitProblems
	}
	return exitOK
}

// collectMetric обходит записи сканера и наполняет отчёт: счётчики H1 и
// справочный счётчик потерь. Форматы заводятся сразу тремя ключами, чтобы
// порядок строк отчёта не зависел от порядка файлов в каталоге.
func collectMetric(res *scanner.Result) (*metricReport, map[scanner.Kind]*metricFormat) {
	stats := map[scanner.Kind]*metricFormat{
		scanner.KindDocx: {Format: string(scanner.KindDocx), LossesMeasured: true},
		scanner.KindXlsx: {Format: string(scanner.KindXlsx), LossesMeasured: true},
		// pdf: LossesMeasured остаётся false — Complete() у PdfDocument нет.
		scanner.KindPdf: {Format: string(scanner.KindPdf)},
	}
	rep := &metricReport{
		InputDir:          res.Root,
		Total:             res.Total,
		Threshold:         metricThreshold,
		PdfLossesMeasured: false,
	}
	for _, e := range res.Entries {
		if !e.Supported {
			rep.Other++
			continue
		}
		rep.Supported++
		st := stats[e.Kind]
		st.Total++
		// Файл с problem обхода (содержимое не читается) — неудача без попытки
		// разбора: причина уже известна сканеру.
		if e.Problem != "" {
			rep.Failed = append(rep.Failed, failedDoc{Path: e.Path, Error: e.Problem})
			continue
		}
		lost, err := metricParse(e.Path, string(e.Kind))
		if err != nil {
			rep.Failed = append(rep.Failed, failedDoc{Path: e.Path, Error: err.Error()})
			continue
		}
		rep.Parsed++
		st.Parsed++
		if lost {
			rep.Lost++
			st.Lost++
		}
	}
	return rep, stats
}

// metricParse разбирает документ парсером его формата и сообщает, был ли
// разбор с потерями содержимого. Фрагменты с адресами метрике не нужны,
// поэтому результат разбора дальше нигде не используется. Для pdf потеря
// всегда false: измерять её нечем (см. metricFormat.LossesMeasured).
func metricParse(path, kind string) (bool, error) {
	switch kind {
	case string(scanner.KindDocx):
		doc, err := parser.ParseDocx(path)
		if err != nil {
			return false, err
		}
		return !doc.Complete(), nil
	case string(scanner.KindXlsx):
		doc, err := parser.ParseXlsx(path)
		if err != nil {
			return false, err
		}
		return !doc.Complete(), nil
	case string(scanner.KindPdf):
		if _, err := parser.ParsePdf(path); err != nil {
			return false, err
		}
		return false, nil
	default:
		return false, fmt.Errorf("неизвестный формат %q", kind)
	}
}

// metricFormatOrder — порядок форматов в отчёте: как в справке и в kindOrder.
var metricFormatOrder = []scanner.Kind{scanner.KindDocx, scanner.KindXlsx, scanner.KindPdf}

// formatStats разворачивает счётчики форматов в список отчёта: только
// форматы, которые реально встретились в пакете, в фиксированном порядке.
func formatStats(stats map[scanner.Kind]*metricFormat) []metricFormat {
	out := make([]metricFormat, 0, len(metricFormatOrder))
	for _, kind := range metricFormatOrder {
		st := stats[kind]
		if st.Total == 0 {
			continue
		}
		st.Success = round1(shareOf(st.Parsed, st.Total))
		out = append(out, *st)
	}
	return out
}

// round1 — процент с одной десятичной цифрой: отчёт читает человек, а
// 66.66666666666666 в строке ничего не значит.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func writeMetricJSON(w io.Writer, rep *metricReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта метрики: %w", err)
	}
	return nil
}

func writeMetricText(w io.Writer, rep *metricReport) {
	fmt.Fprintf(w, "Каталог: %s\n", rep.InputDir)
	fmt.Fprintf(w, "Файлов: %d, поддерживаемых %d, прочих %d\n", rep.Total, rep.Supported, rep.Other)
	fmt.Fprintf(w, "Разобрано успешно: %d из %d — H1 %.1f %% (порог %.0f %%)\n",
		rep.Parsed, rep.Supported, rep.Success, rep.Threshold)
	fmt.Fprintf(w, "С потерями: %d (docx/xlsx); у PDF потери не измеряются\n", rep.Lost)
	verdict := fmt.Sprintf("порог %.0f %% достигнут", rep.Threshold)
	if !rep.Reached {
		verdict = fmt.Sprintf("НИЖЕ ПОРОГА %.0f %%", rep.Threshold)
	}
	fmt.Fprintf(w, "Вердикт: %s\n", verdict)

	fmt.Fprintln(w, "\nПо форматам:")
	for _, f := range rep.Formats {
		note := ""
		if !f.LossesMeasured {
			note = " — потери не измеряются"
		}
		fmt.Fprintf(w, "  %-5s %d из %d (%.1f %%), с потерями %d%s\n",
			f.Format, f.Parsed, f.Total, f.Success, f.Lost, note)
	}

	if len(rep.Failed) > 0 {
		fmt.Fprintln(w, "\nНеразобранные файлы:")
		for _, f := range rep.Failed {
			fmt.Fprintf(w, "  ! %s — %s\n", filepath.Base(f.Path), f.Error)
		}
	}
}
