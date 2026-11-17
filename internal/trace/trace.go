// Package trace хранит адрес извлечённого фрагмента в исходнике — «файл +
// страница/пункт» (задача P1.6). Адрес обязан быть частью модели содержимого, а
// не отдельного отчёта: замечание шага P2 рождается из конкретного значения в
// конкретном месте, и по этому адресу специалист должен открыть страницу,
// абзац или ячейку, не перелистывая документ.
//
// Разница форматов сохранена, а не сглажена:
//
//   - PDF — страница есть в самом файле, она точная;
//   - .docx — пагинации в файле нет, Word перекладывает текст при каждом
//     открытии, поэтому надёжная координата — номер абзаца (блока), а страница
//     берётся из последней отрисовки Word (отметки w:lastRenderedPageBreak);
//   - .xlsx — страниц нет вовсе, адресуются лист и ячейка.
package trace

import (
	"strconv"
	"strings"
)

// Форматы содержимого, для которых заданы правила адреса. Константы живут здесь,
// а парсер на них ссылается: имя формата в модели и в адресе не расходится.
const (
	FormatDocx = "docx"
	FormatXlsx = "xlsx"
	FormatPdf  = "pdf"
)

// Ref — адрес фрагмента в источнике. Заполнены только поля, которые имеет данный
// формат: Traceability проверяется методом Traced, а не списком полей.
type Ref struct {
	// File — путь к файлу так, как его передал вызывающий. Без него ссылка
	// указывает в никуда: пакет заявки — это десятки файлов.
	File string `json:"file,omitempty"`
	// Format — docx/xlsx/pdf: определяет, какие части адреса показывать.
	Format string `json:"format,omitempty"`
	// Page — страница: настоящая у PDF, «как её в последний раз разложил Word» у
	// .docx. 0 — страница неизвестна (в файле нет отметок разбивки).
	Page int `json:"page,omitempty"`
	// Para — номер блочного элемента .docx (абзаца или таблицы) в документном
	// порядке. Устойчивая координата на случай, когда пагинация подвела.
	Para int `json:"para,omitempty"`
	// Sheet — имя листа книги .xlsx.
	Sheet string `json:"sheet,omitempty"`
	// Cell — адрес ячейки: «B7» на листе .xlsx, «строка:колонка» в таблице .docx.
	// Вложенная таблица дописывается в тот же путь — «3:2/1:4».
	Cell string `json:"cell,omitempty"`
}

// Where — адрес без имени файла: в карточке реквизитов файл уже назван, и
// повторять его в каждой строке отчёта — значит только раздувать вывод.
func (r Ref) Where() string {
	if r.Format == FormatXlsx {
		loc := r.Sheet
		if r.Cell != "" {
			if loc != "" {
				loc += "!"
			}
			loc += r.Cell
		}
		return loc
	}

	var parts []string
	if r.Page > 0 {
		parts = append(parts, "стр. "+strconv.Itoa(r.Page))
	}
	if r.Para > 0 {
		parts = append(parts, "абз. "+strconv.Itoa(r.Para))
	}
	if r.Cell != "" {
		parts = append(parts, "яч. "+r.Cell)
	}
	return strings.Join(parts, " · ")
}

// String — адрес целиком, как он выглядит в отчёте для человека.
func (r Ref) String() string {
	where := r.Where()
	switch {
	case r.File == "":
		return where
	case where == "":
		return r.File
	default:
		return r.File + " · " + where
	}
}

// Traced — адрес указывает на место в источнике, а не только на файл. По нему
// считается 100 % трассируемость из критерия готовности P1.6.
func (r Ref) Traced() bool {
	return r.File != "" && r.Where() != ""
}

// Segment — интервал внутри Text фрагмента с более точным адресом: строка
// таблицы запоминается одним фрагментом (иначе метка «ИНН» в соседней клетке
// перестала бы находиться), а значение в ней принадлежит конкретной клетке.
type Segment struct {
	Ref   Ref `json:"ref"`
	Start int `json:"start"`
	End   int `json:"end"`
}

// Fragment — кусок содержимого с адресом: абзац, строка таблицы или страница.
type Fragment struct {
	Ref  Ref    `json:"ref"`
	Text string `json:"text"`
	// Segments — части Text с уточнённым адресом, упорядоченные по Start.
	Segments []Segment `json:"segments,omitempty"`
}

// RefAt — адрес байтового смещения внутри Text. Смещение вне любого сегмента
// (пробелы, табуляция между клетками) остаётся на адресе всего фрагмента.
func (f Fragment) RefAt(offset int) Ref {
	for _, s := range f.Segments {
		if offset >= s.Start && offset < s.End {
			return s.Ref
		}
	}
	return f.Ref
}

// CellAddr — клетка строки таблицы: её адрес и текст, которым она вошла во фрагмент.
type CellAddr struct {
	Ref  Ref
	Text string
}

// NewRow собирает фрагмент-строку таблицы: клетки склеиваются табуляцией — ровно
// так, как их отдаёт Text() у парсеров, — и для каждой непустой клетки внутри
// этого текста запоминается адрес. Пустые клетки сегмента не получают: попадание
// в табуляцию между ними не должно указывать на клетку, где ничего нет.
func NewRow(base Ref, cells []CellAddr) Fragment {
	var sb strings.Builder
	var segs []Segment
	for _, c := range cells {
		start := sb.Len()
		sb.WriteString(c.Text)
		if c.Text != "" {
			segs = append(segs, Segment{Ref: c.Ref, Start: start, End: sb.Len()})
		}
		sb.WriteString("\t")
	}
	return Fragment{Ref: base, Text: strings.TrimRight(sb.String(), "\t"), Segments: segs}
}

// maxSamples — сколько фрагментов без адреса показываем: искать причину по
// десятому образцу бесполезно, а отчёт раздувается.
const maxSamples = 5

// sampleRunes — длина отсечки образца фрагмента.
const sampleRunes = 80

// Stats — сводка трассируемости: сколько фрагментов с содержимым и сколько из них
// имеют адрес. Пустые фрагменты в счёт не идут: замечание из пустой строки не
// родится, а их доля в реальных документах велика и маскировала бы результат.
type Stats struct {
	Fragments int `json:"fragments"`
	Traced    int `json:"traced"`
	// Untraced — образцы фрагментов без адреса.
	Untraced []string `json:"untraced_samples,omitempty"`
}

// Add учитывает фрагмент в сводке.
func (s *Stats) Add(f Fragment) {
	if strings.TrimSpace(f.Text) == "" {
		return
	}
	s.Fragments++
	if f.Ref.Traced() {
		s.Traced++
		return
	}
	if len(s.Untraced) < maxSamples {
		s.Untraced = append(s.Untraced, sample(f.Text))
	}
}

// Missing — число фрагментов без адреса.
func (s Stats) Missing() int { return s.Fragments - s.Traced }

// Share — доля трассированных фрагментов; 0 для пустого набора.
func (s Stats) Share() float64 {
	if s.Fragments == 0 {
		return 0
	}
	return float64(s.Traced) / float64(s.Fragments)
}

// Complete — выполнен критерий P1.6: ни одного фрагмента без адреса. Пустой
// документ считается завершённым: проверять в нём нечего, это не потеря содержимого.
func (s Stats) Complete() bool { return s.Missing() == 0 }

// sample — образец фрагмента для отчёта: одна строка, не длиннее sampleRunes.
func sample(text string) string {
	one := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	runes := []rune(one)
	if len(runes) > sampleRunes {
		return string(runes[:sampleRunes]) + "…"
	}
	return one
}
