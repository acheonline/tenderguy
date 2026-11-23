package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"tenderguy/internal/reqs"
	"tenderguy/internal/rules"
	"tenderguy/internal/scanner"
)

// checkEmptyNote — пометка отчёта для конфига без правил: проверять нечего,
// и это не провал (приём metric с его «нечего измерять»). В JSON попадает
// полем note, в text — строкой примечания, чтобы человек и вызывающий код
// видели одну причину.
const checkEmptyNote = "проверять нечего"

// checkCmd применяет детерминированные правила из файла --rules к пакету
// --input-dir (P2.1). Коды выхода согласованы с остальными командами:
// 0 — блокирующих замечаний нет (сюда же попадает пустой набор правил),
// 1 — есть блокирующие замечания, не разобран поддерживаемый документ или
// движок вернул внутреннюю ошибку, 2 — ошибка в флагах (проверяет checkFlags)
// или в содержимом конфига, 3 — неверный путь (каталог или файл правил
// недоступен). Предупреждения код не меняют: как подозрительные значения в
// extract-reqs, они объявлены в stderr и видны в отчёте.
func checkCmd(p *params, stdout, stderr io.Writer) int {
	cfg, err := rules.LoadConfig(p.rulesFile)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		if errors.Is(err, rules.ErrConfig) {
			return exitUsage
		}
		return exitBadPath
	}

	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}

	// Класс «реквизиты» (P2.3) регистрируется с эталонами из раздела orgs
	// конфигурации: Handler получает только правило и пакет, поэтому эталоны
	// замыкаются в обработчик здесь. Классы состава пакета и сроков добавят
	// P2.4—P2.5.
	runner := &rules.Runner{}
	runner.Register(rules.ClassReqs, rules.NewReqsHandler(cfg.Orgs))
	rep, err := runner.Run(checkPackage(res), cfg)
	if err != nil {
		if errors.Is(err, rules.ErrConfig) {
			fmt.Fprintf(stderr, "tenderguy: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stderr, "tenderguy: внутренняя ошибка движка правил: %v\n", err)
		return exitProblems
	}
	if len(cfg.Rules) == 0 {
		rep.Note = checkEmptyNote
	}

	if p.format == "json" {
		if err := writeCheckJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	} else {
		writeCheckText(stdout, rep)
	}

	if rep.Warnings > 0 {
		fmt.Fprintf(stderr, "tenderguy: предупреждений %d — пакет они не блокируют, см. отчёт\n", rep.Warnings)
	}
	blocked := rep.Blocking > 0
	if blocked {
		fmt.Fprintf(stderr, "tenderguy: блокирующих замечаний %d — пакет не проходит проверку\n", rep.Blocking)
	}
	// Неразобранный документ делает вход неполным: отсутствие замечаний по нему
	// означает «правила не видели документ», а не «всё в порядке».
	incomplete := len(rep.Failed) > 0
	if incomplete {
		fmt.Fprintf(stderr, "tenderguy: поддерживаемых документов не разобрано %d — вход неполон, результат проверки неполный\n",
			len(rep.Failed))
	}
	if blocked || incomplete {
		return exitProblems
	}
	return exitOK
}

// checkPackage собирает вход движка из обхода P1.1: разобранные документы с
// фрагментами и карточками, все файлы обхода и ошибки разбора. Поддерживаемый
// файл с problem обхода пробуем разобрать, как в dump-docs: причину он уже
// несёт, а ошибка разбора попадает в Failed и меняет код выхода.
func checkPackage(res *scanner.Result) rules.Package {
	pkg := rules.Package{Root: res.Root}
	for _, e := range res.Entries {
		pkg.Files = append(pkg.Files, rules.File{
			RelPath:   e.RelPath,
			Name:      e.Name,
			Kind:      string(e.Kind),
			Supported: e.Supported,
			Note:      e.Note,
			Problem:   e.Problem,
		})
		if !e.Supported {
			continue
		}
		frags, err := documentFragments(e.Path, string(e.Kind))
		if err != nil {
			pkg.Failed = append(pkg.Failed, rules.Failed{Path: e.Path, Error: err.Error()})
			continue
		}
		pkg.Docs = append(pkg.Docs, rules.Doc{
			Path:      e.Path,
			RelPath:   e.RelPath,
			Format:    string(e.Kind),
			Fragments: frags,
			Reqs:      reqs.Extract(e.Path, string(e.Kind), frags),
		})
	}
	return pkg
}

// severityLabel — название уровня критичности для человека: блокирующее
// замечание обязано бросаться в глаза, предупреждение — нет.
func severityLabel(s rules.Severity) string {
	switch s {
	case rules.SeverityBlocking:
		return "БЛОКИРУЮЩЕЕ"
	case rules.SeverityWarning:
		return "предупреждение"
	default:
		return string(s)
	}
}

func writeCheckJSON(w io.Writer, rep rules.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("сериализация отчёта проверки: %w", err)
	}
	return nil
}

// writeCheckText печатает отчёт проверки. Группировки по классам и разметки
// здесь сознательно нет — это P2.6, сейчас отчёт обязан быть читаемым списком.
func writeCheckText(w io.Writer, rep rules.Report) {
	fmt.Fprintf(w, "Каталог: %s\n", rep.InputDir)
	fmt.Fprintf(w, "Правил в конфиге: %d\n", rep.RulesTotal)
	fmt.Fprintf(w, "Замечаний: %d, блокирующих %d, предупреждений %d\n",
		len(rep.Findings), rep.Blocking, rep.Warnings)
	verdict := "блокирующих замечаний нет"
	if rep.Blocking > 0 {
		verdict = fmt.Sprintf("ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ: %d", rep.Blocking)
	}
	fmt.Fprintf(w, "Вердикт: %s\n", verdict)
	if rep.Note != "" {
		fmt.Fprintf(w, "Примечание: %s\n", rep.Note)
	}

	if len(rep.Findings) > 0 {
		fmt.Fprintln(w, "\nЗамечания:")
		for _, f := range rep.Findings {
			fmt.Fprintf(w, "  %-14s %-10s %s — %s\n", severityLabel(f.Severity), f.Class, f.Rule, oneLine(f.Text))
			if f.Quote != "" {
				fmt.Fprintf(w, "      цитата: %s\n", oneLine(f.Quote))
			}
			// Адрес показывается целиком, с именем файла: замечания одного
			// класса относятся к разным документам пакета.
			for _, ref := range f.Refs {
				fmt.Fprintf(w, "      адрес: %s\n", ref.String())
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
