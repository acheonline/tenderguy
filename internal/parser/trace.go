package parser

import (
	"strconv"

	"tenderguy/internal/trace"
)

// Задача P1.6 — адрес каждого извлечённого фрагмента. Координаты модели
// форматов (Page/Index у блоков .docx, Ref у ячеек .xlsx, Index у страниц PDF)
// здесь превращаются в единый trace.Ref: шаг P2 ссылается замечанием на
// страницу, абзац или ячейку, не переписывая парсеры.
//
// Fragment — то же содержимое, что отдаёт Text(), но по кусочкам с адресами:
// абзац .docx, строка таблицы (клетки — сегментами внутри строки, чтобы метка
// «ИНН» в соседней клетке продолжала находиться), страница PDF, непустая
// ячейка .xlsx — адресом A1.

// Fragments — содержимое .docx фрагментами с адресами «файл + страница + абзац».
// Таблица даётся построчно: у строк своя страница (таблица может переходить
// через разрыв), а клетка получает ещё и адрес «строка:колонка».
func (d *Document) Fragments() []trace.Fragment {
	out := make([]trace.Fragment, 0, len(d.Blocks))
	for _, b := range d.Blocks {
		base := trace.Ref{File: d.Path, Format: d.Format, Page: b.Page, Para: b.Index}
		if b.Table == nil {
			out = append(out, trace.Fragment{Ref: base, Text: b.Text})
			continue
		}
		out = append(out, tableFragments(base, b.Table)...)
	}
	return out
}

// tableFragments разворачивает таблицу в строчные фрагменты. base.Cell хранит
// путь к таблице: у таблицы в теле документа он пуст, у вложенной — адрес
// клетки-носителя, поэтому «3:2/1:4» читается как «четвёртая клетка второй
// строки таблицы, стоящей в клетке 3:2».
func tableFragments(base trace.Ref, t *Table) []trace.Fragment {
	var out []trace.Fragment
	for _, row := range t.Rows {
		rowBase := base
		if row.Page > 0 {
			rowBase.Page = row.Page
		}
		cells := make([]trace.CellAddr, len(row.Cells))
		var nested []trace.Fragment
		for i, c := range row.Cells {
			ref := rowBase
			ref.Cell = cellPath(base.Cell, row.Index, i+1)
			// Клетку, закрытую вертикальным слиянием, Word показывает пустой —
			// так же, как её отдаёт Text(). Её содержимое остаётся по адресу
			// клетки-носителя, повторять значение под двумя адресами нельзя:
			// свёрка P2 посчитала бы его дважды.
			text := ""
			if !c.Merged {
				text = flattenLines(c.Text, true)
			}
			cells[i] = trace.CellAddr{Ref: ref, Text: text}
			for _, nt := range c.Tables {
				nested = append(nested, tableFragments(ref, nt)...)
			}
		}
		out = append(out, trace.NewRow(rowBase, cells))
		out = append(out, nested...)
	}
	return out
}

// cellPath — адрес клетки таблицы .docx: «строка:колонка», для вложенной
// таблицы — путь от клетки-носителя через «/».
func cellPath(prefix string, row, col int) string {
	cell := strconv.Itoa(row) + ":" + strconv.Itoa(col)
	if prefix == "" {
		return cell
	}
	return prefix + "/" + cell
}

// Fragments — содержимое книги .xlsx фрагментами с адресами «файл + лист +
// ячейка». Строка остаётся одним фрагментом ради меток: значение ищется по всей
// строке, а принадлежит конкретной клетке — она и даёт сегмент внутри строки.
// Сетка разворачивается как в Text(), поэтому пропущенные пустые клетки адреса
// не получают.
func (d *XlsxDocument) Fragments() []trace.Fragment {
	out := make([]trace.Fragment, 0, d.Rows)
	for _, sh := range d.Sheets {
		base := trace.Ref{File: d.Path, Format: d.Format, Sheet: sh.Name}
		for _, row := range sh.Rows {
			cells := make([]trace.CellAddr, sh.Columns)
			for _, c := range row.Cells {
				if c.Col < 1 || c.Col > sh.Columns {
					continue
				}
				ref := base
				ref.Cell = c.Ref
				cells[c.Col-1] = trace.CellAddr{Ref: ref, Text: flattenLines(c.Value, true)}
			}
			out = append(out, trace.NewRow(base, cells))
		}
	}
	return out
}

// Fragments — содержимое PDF постранично: страница есть в самом файле, поэтому
// адрес точный и другого уровня детализации не требуется (критерий P1.6 —
// «номер страницы для PDF»).
func (d *PdfDocument) Fragments() []trace.Fragment {
	out := make([]trace.Fragment, 0, len(d.Pages))
	for _, p := range d.Pages {
		ref := trace.Ref{File: d.Path, Format: d.Format, Page: p.Index}
		out = append(out, trace.Fragment{Ref: ref, Text: p.Text})
	}
	return out
}
