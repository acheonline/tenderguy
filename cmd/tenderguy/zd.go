package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"tenderguy/internal/zd"
)

// extractZD извлекает из закупочной документации три структуры (P2.8):
// требуемый состав пакета, пары «форма ↔ инструкция по заполнению» и
// требования к участнику. Вход — один документ (--file), не каталог:
// извлечение делается из одной ЗД. Коды выхода: 0 — все три структуры full,
// 1 — любая partial или not_found (причины в отчёте и в stderr), либо
// документ не .docx / не разобран, 3 — неверный путь. Неразрешённые
// упоминания инструкций код выхода не меняют: это информация для эскалации
// A9 и знаменатель метрики полноты.
func extractZD(p *params, stdout, stderr io.Writer) int {
	if _, err := os.Stat(p.file); err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}
	res, err := zd.ExtractFile(p.file)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitProblems
	}

	reportErr := func(err error) int {
		fmt.Fprintf(stderr, "tenderguy: не удалось вывести отчёт: %v\n", err)
		return exitProblems
	}
	switch p.emit {
	case emitComposition:
		// Только массив пунктов в схеме раздела composition файла правил —
		// байт в байт то, что примет rules.LoadConfig: замена источника
		// состава не меняет движок правил (критерий #63).
		if err := writeCompositionConfig(stdout, res.Composition.Items); err != nil {
			return reportErr(err)
		}
	case emitForms:
		if p.format == "json" {
			if err := encodeJSON(stdout, res.Forms); err != nil {
				return reportErr(err)
			}
			break
		}
		fmt.Fprintln(stdout, "Пары «форма ↔ инструкция по заполнению»")
		writeFormsText(stdout, res.Forms)
	case emitRequirements:
		if p.format == "json" {
			if err := encodeJSON(stdout, res.Requirements); err != nil {
				return reportErr(err)
			}
			break
		}
		fmt.Fprintln(stdout, "Требования к участнику")
		writeRequirementsText(stdout, res.Requirements)
	default:
		if p.format == "json" {
			if err := writeZDJSON(stdout, res); err != nil {
				return reportErr(err)
			}
		} else {
			writeZDText(stdout, res)
		}
	}

	problems := res.Problems()
	for _, problem := range problems {
		fmt.Fprintf(stderr, "tenderguy: %s\n", problem)
	}
	if len(problems) > 0 {
		return exitProblems
	}
	return exitOK
}

// Значения флага --emit команды extract-zd.
const (
	emitAll          = "all"
	emitComposition  = "composition"
	emitForms        = "forms"
	emitRequirements = "requirements"
)

// allEmits — допустимые значения --emit: список идёт и в справку, и в
// сообщение об ошибке.
var allEmits = []string{emitAll, emitComposition, emitForms, emitRequirements}

// compositionConfigItem — пункт состава ровно в схеме раздела composition
// файла правил (rules.CompositionItem): никаких лишних полей, строгий
// декодер LoadConfig с DisallowUnknownFields обязан принять вывод байт в байт.
type compositionConfigItem struct {
	ID       string                 `json:"id"`
	Title    string                 `json:"title"`
	Required bool                   `json:"required"`
	Match    compositionConfigMatch `json:"match"`
}

type compositionConfigMatch struct {
	By      string `json:"by"`
	Pattern string `json:"pattern"`
}

// writeCompositionConfig печатает массив пунктов состава в схеме конфига правил.
func writeCompositionConfig(w io.Writer, items []zd.CompositionItem) error {
	out := make([]compositionConfigItem, 0, len(items))
	for _, it := range items {
		out = append(out, compositionConfigItem{
			ID:       it.ID,
			Title:    it.Title,
			Required: it.Required,
			Match:    compositionConfigMatch{By: "filename", Pattern: it.Pattern},
		})
	}
	return encodeJSON(w, out)
}

// encodeJSON сериализует значение в поток с отступами, как остальные отчёты CLI.
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("сериализация отчёта: %w", err)
	}
	return nil
}

func writeZDJSON(w io.Writer, res zd.Result) error {
	return encodeJSON(w, res)
}

// writeZDText печатает полный отчёт извлечения: привязка к версии
// документа, вердикт пагинации, три структуры с адресами и неразрешённые
// упоминания инструкций.
func writeZDText(w io.Writer, res zd.Result) {
	f := res.File
	fmt.Fprintf(w, "Документ: %s\n", f.Name)
	if f.SHA256 != "" {
		fmt.Fprintf(w, "Версия: sha256 %s, %s\n", f.SHA256, humanSize(f.Size))
	}
	fmt.Fprintf(w, "Блоков %d, абзацев %d, таблиц %d, символов %d\n", f.Blocks, f.Paragraphs, f.Tables, f.Runes)

	pg := res.Pagination
	verdict := "достоверна"
	if !pg.Trusted {
		verdict = "НЕДОСТОВЕРНА — страницы в адреса не включаются"
	}
	fmt.Fprintf(w, "Пагинация: %s (уникальных страниц %d, максимум %d", verdict, pg.UniquePages, pg.MaxPage)
	if pg.TocMaxPage > 0 {
		fmt.Fprintf(w, ", оглавление обещает %d", pg.TocMaxPage)
	}
	fmt.Fprintln(w, ")")
	if pg.Reason != "" {
		fmt.Fprintf(w, "  причина: %s\n", pg.Reason)
	}

	if len(res.Sections) > 0 {
		fmt.Fprintf(w, "\nРазделы по оглавлению: %d\n", len(res.Sections))
		for _, s := range res.Sections {
			fmt.Fprintf(w, "  %d. %s — абз. %d…%d\n", s.Number, s.Title, s.StartPara, s.EndPara-1)
		}
	}

	fmt.Fprintf(w, "\nТребуемый состав пакета: %s\n", statusLine(res.Composition.Status, res.Composition.Reason))
	writeCompositionText(w, res.Composition)

	fmt.Fprintf(w, "\nПары «форма ↔ инструкция по заполнению»: %s\n", statusLine(res.Forms.Status, res.Forms.Reason))
	writeFormsText(w, res.Forms)

	fmt.Fprintf(w, "\nТребования к участнику: %s\n", statusLine(res.Requirements.Status, res.Requirements.Reason))
	writeRequirementsText(w, res.Requirements)

	fmt.Fprintf(w, "\nУпоминания инструкций: всего %d, разрешено %d, неразрешённых %d\n",
		res.Instructions.Total, res.Instructions.Resolved, res.Instructions.Total-res.Instructions.Resolved)
	for _, m := range res.Instructions.Unresolved {
		fmt.Fprintf(w, "  %s\n    адрес: %s\n", m.Quote, m.Ref.String())
	}
}

// statusLine — статус структуры с причиной для текстового отчёта.
func statusLine(status, reason string) string {
	if reason == "" {
		return status
	}
	return status + " — " + reason
}

func writeCompositionText(w io.Writer, comp zd.CompositionResult) {
	for _, it := range comp.Items {
		required := "обязательный"
		if !it.Required {
			required = "условный"
		}
		fmt.Fprintf(w, "  %-8s %s [%s; форматы: %s]\n", it.ID, oneLine(it.Title), required, strings.Join(it.Formats, ", "))
		if it.Group != "" {
			fmt.Fprintf(w, "      подкаталог: %s\n", it.Group)
		}
		if it.Condition != "" {
			fmt.Fprintf(w, "      условие: %s\n", oneLine(it.Condition))
		}
		fmt.Fprintf(w, "      файл: %s, адрес: %s\n", it.Filename, it.Ref.String())
	}
	for _, c := range comp.Candidates {
		fmt.Fprintf(w, "  кандидат: %s\n", c.String())
	}
}

func writeFormsText(w io.Writer, forms zd.FormsResult) {
	if forms.Section != "" {
		fmt.Fprintf(w, "  раздел: %s\n", forms.Section)
	}
	for _, f := range forms.Forms {
		number := "б/н"
		if f.GroupNumber != "" {
			number = "форма " + f.GroupNumber
			if f.Variant > 1 {
				number = fmt.Sprintf("форма %s, вариант %d", f.GroupNumber, f.Variant)
			}
		}
		closed := "закрыта"
		if !f.Closed {
			closed = "БЕЗ маркера «конец формы»"
		}
		body := ""
		if f.EmptyBody {
			body = ", тело ПУСТОЕ"
		}
		fmt.Fprintf(w, "  %-18s %s — %s%s, инструкция: %s (пунктов %d)\n",
			number, oneLine(f.Title), closed, body, f.InstructionStatus, len(f.InstructionItems))
		if f.InstructionTitle != "" {
			fmt.Fprintf(w, "      заголовок инструкций: %s\n", oneLine(f.InstructionTitle))
		}
		if f.ExternalNote != "" {
			fmt.Fprintf(w, "      внешняя инструкция: %s\n", oneLine(f.ExternalNote))
		}
		fmt.Fprintf(w, "      адрес: %s\n", f.Ref.String())
	}
	for _, issue := range forms.Issues {
		fmt.Fprintf(w, "  дефект: %s\n      адрес: %s\n", issue.Reason, issue.Ref.String())
	}
}

func writeRequirementsText(w io.Writer, reqs zd.RequirementsResult) {
	if reqs.Section != "" {
		fmt.Fprintf(w, "  раздел: %s\n", reqs.Section)
	}
	for _, it := range reqs.Items {
		fmt.Fprintf(w, "  [%s] %s\n", it.Category, oneLine(it.Text))
		fmt.Fprintf(w, "      адрес: %s\n", it.Ref.String())
	}
}
