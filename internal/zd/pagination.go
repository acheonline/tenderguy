package zd

import (
	"fmt"

	"tenderguy/internal/parser"
)

// Детектор достоверности пагинации .docx (P2.8). Отметки
// w:lastRenderedPageBreak — свойство последней отрисовки Word, а не файла:
// в реальной закупочной документации почти всё тело помечено одной страницей при
// оглавлении из 45 страниц, а файлы пакета заявки целиком «лежат» на
// странице 1. Проектируем под худший сценарий: страниц может не быть вовсе,
// а если они есть — они могут врать. Любое «нет» четырёх проверок даёт
// Trusted=false с конкретной причиной, и поле Page в адреса извлекаемых
// структур не записывается вовсе.

// minRunesPerPage — правдоподобный минимум объёма текста на одну страницу:
// меньше трёх тысяч символов на страницу в закупочной документации не
// встречается, поэтому документ объёмнее 3000·N символов обязан показать
// хотя бы N уникальных страниц.
const minRunesPerPage = 3000

// detectPagination проверяет пагинацию блоков документа и возвращает вердикт
// с причиной. tocMaxPage — максимальный номер страницы из оглавления (0 —
// оглавления нет, и сверять max_page не с чем).
func detectPagination(doc *parser.Document, tocMaxPage int) Pagination {
	p := Pagination{TocMaxPage: tocMaxPage}
	unique := map[int]bool{}
	maxPage, prev := 0, 0
	for _, b := range doc.Blocks {
		page := blockPage(b)
		if page <= 0 {
			return verdict(p, len(unique), maxPage,
				fmt.Sprintf("блок %d: страница не определена — отметок пагинации в файле нет", b.Index))
		}
		if page < prev {
			return verdict(p, len(unique), maxPage,
				fmt.Sprintf("блок %d: страница %d идёт после страницы %d — последовательность пагинации не монотонна", b.Index, page, prev))
		}
		prev = page
		unique[page] = true
		if page > maxPage {
			maxPage = page
		}
	}
	p.UniquePages = len(unique)
	p.MaxPage = maxPage

	need := (doc.Runes + minRunesPerPage - 1) / minRunesPerPage
	if need > 0 && p.UniquePages < need {
		return verdict(p, len(unique), maxPage, fmt.Sprintf(
			"уникальных страниц %d при объёме текста %d символов — правдоподобный минимум %d: пагинация недостоверна",
			p.UniquePages, doc.Runes, need))
	}
	if tocMaxPage > 0 && p.MaxPage < tocMaxPage {
		return verdict(p, len(unique), maxPage, fmt.Sprintf(
			"оглавление обещает страницу %d, а максимум по блокам — %d: пагинация недостоверна", tocMaxPage, p.MaxPage))
	}
	p.Trusted = true
	p.Reason = ""
	return p
}

// blockPage — эффективная страница блока: у таблицы со своей разметкой строк
// страница блока может отсутствовать, тогда берётся страница первой строки.
func blockPage(b parser.Block) int {
	if b.Page > 0 || b.Table == nil {
		return b.Page
	}
	for _, row := range b.Table.Rows {
		if row.Page > 0 {
			return row.Page
		}
	}
	return 0
}

// verdict оформляет провал проверки: счётчики заполняются тем, что удалось
// насчитать к моменту провала, — они часть отчёта о причине.
func verdict(p Pagination, uniqueCount, maxPage int, reason string) Pagination {
	p.UniquePages = uniqueCount
	p.MaxPage = maxPage
	p.Trusted = false
	p.Reason = reason
	return p
}
