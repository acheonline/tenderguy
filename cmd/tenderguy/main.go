// Command tenderguy — входная точка PoC-конвейера анализа пакетов заявок.
//
// Задача P1.1: принять путь к каталогу с документами, найти в нём все файлы
// и классифицировать их по типу (.docx/.xlsx/.pdf и прочие). Задача P1.2:
// командой --command parse-docx извлечь из .docx текст и таблицы и показать,
// что извлечение прошло без потерь. Задача P1.3: командой --command
// parse-xlsx прочитали ячейки и формулы книг Excel (.xlsx), сверив их с
// XML-эталоном книги. Задача P1.5: командой --command extract-reqs поверх
// парсеров форматов собрать карточку реквизитов файла. Пути в коде не
// фиксируются — каталог задаётся только флагом --input-dir.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"tenderguy/internal/scanner"
)

// Коды выхода: по ним и CI, и человек видят, чем закончился прогон.
const (
	exitOK       = 0
	exitProblems = 1
	exitUsage    = 2
	exitBadPath  = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// params — разобранные флаги вызова.
type params struct {
	inputDir string
	file     string
	command  string
	format   string
	withText bool
}

// newFlagSet собирает флаги; отдельная функция нужна, чтобы справку можно было проверить в тесте.
func newFlagSet(out io.Writer) (*flag.FlagSet, *params) {
	p := &params{}
	fs := flag.NewFlagSet("tenderguy", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&p.command, "command", commandScan, "команда: scan (обход и классификация), parse-docx (текст и таблицы), parse-xlsx (ячейки и формулы), parse-pdf (текстовый слой и OCR) или extract-reqs (карточка реквизитов)")
	fs.StringVar(&p.inputDir, "input-dir", "", "каталог с документами для обхода (обязательный)")
	fs.StringVar(&p.file, "file", "", "один документ (.docx, .xlsx или .pdf) для разбора (вместо каталога)")
	fs.StringVar(&p.format, "format", "text", "формат отчёта: text или json")
	fs.BoolVar(&p.withText, "with-text", false, "вывести извлечённый текст документов целиком (parse-docx, parse-xlsx, parse-pdf)")
	return fs, p
}

// run выполняет команду и возвращает код выхода. Функция отделена от main,
// чтобы тесты вызывали CLI без запуска процесса.
func run(args []string, stdout, stderr io.Writer) int {
	fs, p := newFlagSet(stderr)
	fs.Usage = func() { usage(fs, stderr) }

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if extra := fs.Args(); len(extra) > 0 {
		fmt.Fprintf(stderr, "tenderguy: лишние аргументы %s — путь передаётся только флагом --input-dir\n",
			strings.Join(quoteAll(extra), " "))
		return exitUsage
	}
	if err := checkFlags(p); err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		usage(fs, stderr)
		return exitUsage
	}

	if p.command == commandParseDocx {
		return parseDocx(p, stdout, stderr)
	}
	if p.command == commandParseXlsx {
		return parseXlsx(p, stdout, stderr)
	}
	if p.command == commandParsePdf {
		return parsePdf(p, stdout, stderr)
	}
	if p.command == commandExtractReqs {
		return extractReqs(p, stdout, stderr)
	}

	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}

	if p.format == "json" {
		err = writeJSONReport(stdout, res)
	} else {
		writeTextReport(stdout, res)
	}
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
		return exitProblems
	}
	if res.HasProblems() {
		fmt.Fprintf(stderr, "tenderguy: замечаний %d — см. раздел «Замечания» отчёта\n", len(res.Problems))
		return exitProblems
	}
	return exitOK
}

// checkFlags проверяет сочетание флагов до работы с файлами: ошибку в аргументах
// дешевле показать сразу, чем в процессе разбора каталога.
func checkFlags(p *params) error {
	if !isCommand(p.command) {
		return fmt.Errorf("неизвестный --command %q, допустимы %s", p.command, strings.Join(allCommands, ", "))
	}
	if p.format != "text" && p.format != "json" {
		return fmt.Errorf("неизвестный --format %q, допустимы text и json", p.format)
	}
	if p.command == commandScan {
		if p.file != "" {
			return fmt.Errorf("--file работает только с командами разбора (%s)", strings.Join(parseCommands, ", "))
		}
		if p.withText {
			return fmt.Errorf("--with-text работает только с командами разбора (%s)", strings.Join(parseCommands, ", "))
		}
		if strings.TrimSpace(p.inputDir) == "" {
			return fmt.Errorf("не задан каталог с документами")
		}
		return nil
	}
	if p.command == commandExtractReqs && p.withText {
		return fmt.Errorf("--with-text не используется в %s: карточка строится по извлечённому тексту, сам текст в отчёт не попадает",
			commandExtractReqs)
	}
	switch {
	case p.file != "" && strings.TrimSpace(p.inputDir) != "":
		return fmt.Errorf("--file и --input-dir передают один и тот же вход — выберите что-то одно")
	case p.file == "" && strings.TrimSpace(p.inputDir) == "":
		return fmt.Errorf("не задан вход: передайте --file <документ> или --input-dir <каталог>")
	}
	return nil
}

// isCommand — из того ли списка allCommands значение --command.
func isCommand(command string) bool {
	for _, known := range allCommands {
		if known == command {
			return true
		}
	}
	return false
}

func usage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprint(w, `Использование:
  tenderguy --input-dir <каталог> [--format text|json]
  tenderguy --command parse-docx --file <документ.docx> [--with-text] [--format text|json]
  tenderguy --command parse-xlsx --file <книга.xlsx>   [--with-text] [--format text|json]
  tenderguy --command parse-pdf  --file <документ.pdf>   [--with-text] [--format text|json]
  tenderguy --command extract-reqs (--file <документ> | --input-dir <каталог>) [--format text|json]

Флаги:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Коды выхода: 0 — замечаний нет (у extract-reqs сюда же попадают подозрительные
значения: они помечены ✗ в отчёте и объявлены в stderr), 1 — есть замечания
(проблемные файлы, потери при извлечении или неразобранные документы),
2 — ошибка в аргументах, 3 — неверный путь (каталога нет или это не каталог).
`)
}

func writeJSONReport(w io.Writer, res *scanner.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("сериализация отчёта: %w", err)
	}
	return nil
}

// kindOrder — порядок вывода типов: сначала разбираемые форматы, затем прочие.
var kindOrder = []scanner.Kind{
	scanner.KindDocx, scanner.KindXlsx, scanner.KindPdf,
	scanner.KindOfficeLegacy, scanner.KindArchive, scanner.KindService, scanner.KindOther,
}

func writeTextReport(w io.Writer, res *scanner.Result) {
	fmt.Fprintf(w, "Каталог: %s\n", res.Root)
	fmt.Fprintf(w, "Файлов найдено: %d, из них поддерживается парсерами P1: %d\n", res.Total, res.Supported)

	fmt.Fprintln(w, "\nПо типам:")
	for _, kind := range kindOrder {
		if n := res.Counts[kind]; n > 0 {
			fmt.Fprintf(w, "  %-14s %d\n", kind, n)
		}
	}
	// Типы вне kindOrder (если появится новый) тоже должны попасть в отчёт.
	for kind, n := range res.Counts {
		if !kindInList(kind, kindOrder) {
			fmt.Fprintf(w, "  %-14s %d\n", kind, n)
		}
	}

	writeSection(w, "Документы для разбора (P1.2+)", res.Entries, func(e scanner.Entry) bool {
		return e.Supported
	})
	writeSection(w, "Прочие файлы (не входят в P1)", res.Entries, func(e scanner.Entry) bool {
		return !e.Supported
	})

	if len(res.SkippedDirs) > 0 {
		fmt.Fprintln(w, "\nПропущенные каталоги:")
		for _, dir := range res.SkippedDirs {
			fmt.Fprintf(w, "  %s\n", dir)
		}
	}

	// Файлы, где расширение спорит с содержимым, выводятся списком: их классифицировали
	// по содержимому, и такой случай человек должен видеть отдельно.
	var mismatches []scanner.Entry
	for _, e := range res.Entries {
		if e.Mismatch {
			mismatches = append(mismatches, e)
		}
	}
	if len(mismatches) > 0 {
		fmt.Fprintln(w, "\nРасхождение расширения и содержимого:")
		for _, e := range mismatches {
			fmt.Fprintf(w, "  %-13s %s — %s\n", e.Kind, e.RelPath, e.Note)
		}
	}

	if len(res.Problems) > 0 {
		fmt.Fprintln(w, "\nЗамечания:")
		for _, problem := range res.Problems {
			fmt.Fprintf(w, "  ! %s\n", problem)
		}
	}

	for _, note := range res.Notes {
		fmt.Fprintf(w, "\nВнимание: %s\n", note)
	}
}

func writeSection(w io.Writer, title string, entries []scanner.Entry, pick func(scanner.Entry) bool) {
	var lines []string
	for _, e := range entries {
		if !pick(e) {
			continue
		}
		remark := strings.TrimSpace(e.Note)
		if e.Problem != "" {
			remark = e.Problem
		}
		file := e.RelPath
		if remark != "" {
			file += " — " + remark
		}
		lines = append(lines, fmt.Sprintf("  %-13s %-12s %9s  %s",
			e.Kind, evidence(e.DetectedBy), humanSize(e.Size), file))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s: %d\n", title, len(lines))
	fmt.Fprintf(w, "  %-13s %-12s %9s  %s\n", "ТИП", "ПРИЗНАК", "РАЗМЕР", "ФАЙЛ")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// evidence — чем подтверждён тип: «и/или» сокращение способов определения из scanner.
func evidence(detector string) string {
	switch detector {
	case scanner.DetectorBoth:
		return "расш.+сигн."
	case scanner.DetectorSignature:
		return "сигнатура"
	case scanner.DetectorExtension:
		return "расширение"
	case scanner.DetectorName:
		return "имя"
	default:
		return "—"
	}
}

func kindInList(kind scanner.Kind, list []scanner.Kind) bool {
	for _, k := range list {
		if k == kind {
			return true
		}
	}
	return false
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d Б", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(size)/float64(div), binaryPrefixes[exp])
}

// binaryPrefixes — русские названия двоичных единиц (киби-, меби- и т.д.).
var binaryPrefixes = []string{"КиБ", "МиБ", "ГиБ", "ТиБ", "ПиБ", "ЭиБ"}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = fmt.Sprintf("%q", a)
	}
	return out
}
