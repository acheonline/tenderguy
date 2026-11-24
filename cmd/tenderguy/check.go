package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// --input-dir (P2.1). Отчёт (P2.6) выводится в трёх форматах: text в консоль,
// json в stdout, markdown — файлом report.md в каталог --output-dir (в консоль
// при этом печатается краткий итог). Коды выхода согласованы с остальными
// командами: 0 — блокирующих замечаний нет (сюда же попадает пустой набор
// правил), 1 — есть блокирующие замечания, не разобран поддерживаемый
// документ, движок вернул внутреннюю ошибку или отчёт не удалось записать,
// 2 — ошибка в флагах (проверяет checkFlags) или в содержимом конфига,
// 3 — неверный путь (каталог или файл правил недоступен). Предупреждения код
// не меняют: как подозрительные значения в extract-reqs, они объявлены в
// stderr и видны в отчёте.
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

	// Классы регистрируются с данными из конфига: Handler получает только
	// правило и пакет, поэтому эталоны orgs (P2.3), требуемый состав (P2.4)
	// и каталог сроков свежести (P2.5) замыкаются в обработчики здесь.
	// Побочные выходы обработчиков — таблица статусов состава и пометка
	// «каталог сроков отсутствует» — пишутся в приёмники при прогоне, а в
	// отчёт попадают после Run.
	runner := &rules.Runner{}
	runner.Register(rules.ClassReqs, rules.NewReqsHandler(cfg.Orgs))
	var compTable []rules.CompositionStatus
	runner.Register(rules.ClassComposition, rules.NewCompositionHandler(cfg.Composition, p.rulesFile, &compTable))
	var freshNote string
	runner.Register(rules.ClassFreshness, rules.NewFreshnessHandler(cfg.Freshness, time.Now(), &freshNote))
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
	// Пометка класса «freshness» (пустой каталог сроков) — побочный выход
	// обработчика; Runner о ней не знает, поэтому в отчёт её кладём здесь.
	// Коллизия с «проверять нечего» практически невозможна (без правил
	// обработчик не вызывается), но защищаемся: обе пометки важны.
	if freshNote != "" {
		if rep.Note != "" {
			rep.Note += "; " + freshNote
		} else {
			rep.Note = freshNote
		}
	}
	// Таблица составов заполняется обработчиком при прогоне; Runner о ней не
	// знает, поэтому в отчёт её кладём здесь.
	if len(compTable) > 0 {
		rep.Composition = compTable
	}

	// Сводку по классам (P2.6) считает cmd, как и таблицу Composition:
	// движок формы отчёта не знает.
	rep.Summary = rules.SummarizeClasses(rep.Findings)

	switch p.format {
	case "json":
		if err := writeCheckJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
			return exitProblems
		}
	case "markdown":
		path, err := writeCheckMarkdown(p.outputDir, rep)
		if err != nil {
			fmt.Fprintf(stderr, "tenderguy: %v\n", err)
			return exitProblems
		}
		// В консоль — только краткий итог: полный отчёт прочтут из файла.
		fmt.Fprintf(stdout, "Вердикт: %s\n", checkVerdict(rep))
		fmt.Fprintf(stdout, "Отчёт: %s\n", path)
	default:
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

// checkVerdict — вердикт отчёта для человека: есть ли блокирующие замечания.
// Общий для text и markdown, чтобы оба формата формулировали итог одинаково.
func checkVerdict(rep rules.Report) string {
	if rep.Blocking > 0 {
		return fmt.Sprintf("ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ: %d", rep.Blocking)
	}
	return "блокирующих замечаний нет"
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

// writeCheckText печатает отчёт проверки (P2.6): замечания сгруппированы по
// классам, внутри класса блокирующие идут первыми; непроверенные классы
// показаны с причиной; сводка содержит счётчики и разбивку по классам.
// Отчёт детерминирован: порядок строк задаёт rules.GroupFindings, дат и
// времени в нём нет — повторный прогон по тому же входу даёт тот же текст.
func writeCheckText(w io.Writer, rep rules.Report) {
	fmt.Fprintf(w, "Каталог: %s\n", rep.InputDir)
	fmt.Fprintf(w, "Правил в конфиге: %d\n", rep.RulesTotal)
	if len(rep.Findings) == 0 {
		fmt.Fprintln(w, "Сводка: замечаний нет — пакет проходит проверку")
	} else {
		fmt.Fprintf(w, "Сводка: всего замечаний %d, блокирующих %d, предупреждений %d\n",
			len(rep.Findings), rep.Blocking, rep.Warnings)
		for _, s := range rep.Summary {
			fmt.Fprintf(w, "  %s — всего %d, блокирующих %d, предупреждений %d\n",
				s.Class, s.Total, s.Blocking, s.Warnings)
		}
	}
	fmt.Fprintf(w, "Вердикт: %s\n", checkVerdict(rep))
	if rep.Note != "" {
		fmt.Fprintf(w, "Примечание: %s\n", rep.Note)
	}

	// Таблица требуемого состава (P2.4): статус «есть/нет» по каждому пункту
	// до списка замечаний — сначала состав, потом отклонения от него.
	if len(rep.Composition) > 0 {
		fmt.Fprintln(w, "\nСостав пакета:")
		for _, st := range rep.Composition {
			required := "нет"
			if st.Required {
				required = "да"
			}
			status := "нет"
			if st.Present {
				status = "есть"
			}
			fmt.Fprintf(w, "  %-16s %-40s обязательный: %-4s статус: %-5s %s\n",
				oneLine(st.Item), oneLine(st.Title), required, status, strings.Join(st.Files, ", "))
		}
	}

	// Класс без замечаний и без правил — не «всё в порядке», а «не проверяли»:
	// причина обязана быть видна (критерий P2.6).
	if len(rep.Skipped) > 0 {
		fmt.Fprintln(w, "\nНепроверенные классы:")
		for _, s := range rep.Skipped {
			fmt.Fprintf(w, "  %s — %s\n", s.Class, s.Reason)
		}
	}

	for _, g := range rules.GroupFindings(rep.Findings) {
		fmt.Fprintf(w, "\nЗамечания [%s]:\n", g.Class)
		for _, f := range g.Findings {
			// Класс дублируется в строке замечания намеренно: строку отчёта
			// цитируют и сверяют с таблицей P4 в отрыве от заголовка группы.
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

// checkReportFileName — имя markdown-отчёта о проверке в каталоге --output-dir.
const checkReportFileName = "report.md"

// writeCheckMarkdown сохраняет отчёт о проверке файлом report.md в каталоге
// outDir (каталог создаётся, если его нет) и возвращает путь к файлу.
// Структура отчёта та же, что у writeCheckText: вердикт и сводка, состав
// пакета, непроверенные классы с причиной, замечания по классам и
// критичности, ошибки разбора. Дат и времени в файле нет — повторный прогон
// по тому же входу даёт побайтово тот же отчёт.
func writeCheckMarkdown(outDir string, rep rules.Report) (string, error) {
	var b strings.Builder
	b.WriteString("# Отчёт о проверке пакета\n\n")
	fmt.Fprintf(&b, "- Каталог: `%s`\n", rep.InputDir)
	fmt.Fprintf(&b, "- Правил в конфиге: %d\n", rep.RulesTotal)
	fmt.Fprintf(&b, "- Вердикт: **%s**\n", checkVerdict(rep))
	if rep.Note != "" {
		fmt.Fprintf(&b, "- Примечание: %s\n", rep.Note)
	}

	b.WriteString("\n## Сводка\n\n")
	if len(rep.Findings) == 0 {
		b.WriteString("Замечаний нет — пакет проходит проверку.\n")
	} else {
		fmt.Fprintf(&b, "Всего замечаний: %d, блокирующих: %d, предупреждений: %d.\n\n",
			len(rep.Findings), rep.Blocking, rep.Warnings)
		b.WriteString("| Класс | Всего | Блокирующих | Предупреждений |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, s := range rep.Summary {
			fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", s.Class, s.Total, s.Blocking, s.Warnings)
		}
	}

	if len(rep.Composition) > 0 {
		b.WriteString("\n## Состав пакета\n\n")
		b.WriteString("| Пункт | Наименование | Обязательный | Статус | Файлы |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, st := range rep.Composition {
			required := "нет"
			if st.Required {
				required = "да"
			}
			status := "нет"
			if st.Present {
				status = "есть"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				st.Item, oneLine(st.Title), required, status, strings.Join(st.Files, ", "))
		}
	}

	if len(rep.Skipped) > 0 {
		b.WriteString("\n## Непроверенные классы\n\n")
		for _, s := range rep.Skipped {
			fmt.Fprintf(&b, "- `%s` — %s\n", s.Class, s.Reason)
		}
	}

	for _, g := range rules.GroupFindings(rep.Findings) {
		fmt.Fprintf(&b, "\n## Замечания: класс `%s`\n\n", g.Class)
		for _, f := range g.Findings {
			fmt.Fprintf(&b, "- **%s** · класс `%s` · правило `%s` — %s\n",
				severityLabel(f.Severity), f.Class, f.Rule, oneLine(f.Text))
			if f.Quote != "" {
				fmt.Fprintf(&b, "  - цитата: %s\n", oneLine(f.Quote))
			}
			for _, ref := range f.Refs {
				fmt.Fprintf(&b, "  - адрес: `%s`\n", ref.String())
			}
		}
	}

	if len(rep.Failed) > 0 {
		b.WriteString("\n## Ошибки разбора\n\n")
		for _, f := range rep.Failed {
			fmt.Fprintf(&b, "- `%s` — %s\n", filepath.Base(f.Path), f.Error)
		}
	}

	path := filepath.Join(outDir, checkReportFileName)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("не удалось создать каталог %s: %w", outDir, err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("не удалось записать %s: %w", path, err)
	}
	return path, nil
}
