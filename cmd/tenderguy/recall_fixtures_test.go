package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"tenderguy/internal/rules"
	"tenderguy/internal/scanner"
)

// Синтетический мини-эталон P2.7 (#62): чистый пакет документов и эталонный
// конфиг, на которых движок правил обязан давать ноль замечаний, плюс
// генератор искажений (recall_test.go). Всё собирается кодом и живёт только
// в тестовом слое — документы заказчика (docs/) не используются.
//
// Состав эталона: заявка и договор (.docx), смета (.xlsx), банковская
// гарантия (PDF с текстовым слоем, генерируется кодом). Все четыре документа
// несут один и тот же набор числовых реквизитов (ИНН, КПП, ОГРН) — эталон
// orgs задан только ими, поэтому чистый пакет не даёт даже предупреждений
// «реквизит не найден» (контракт P2.3 требует их для каждого документа без
// значения вида). Наименование и юридический адрес в эталон не входят: их
// подмена ловится внутренней консистентностью пакета — значение, изменённое
// в одном документе, расходится с остальными. PDF собран из ASCII: текстовый
// слой базового шрифта Helvetica (WinAnsi) кириллицу не несёт, поэтому
// кириллические реквизиты в нём не размещаются осознанно.
//
// Прогон идёт мимо CLI намеренно: checkCmd регистрирует класс «freshness» с
// time.Now(), а замер обязан быть детерминированным, поэтому движок
// собирается здесь с фиксированной точкой отсчёта (образец — checkCmd).

// Эталонные и подменные значения реквизитов. Все числа валидны по
// валидаторам reqs (контрольные цифры ИНН и ОГРН сходятся): подмена обязана
// выглядеть как опечатка в реальном документе, а не как мусор, который
// отфильтровался бы ещё на уровне карточки. Часть значений взята из
// internal/reqs/validate_test.go, остальные проверены по методикам ФНС;
// подменный КПП — нейтральный (не из пакетов заказчика).
const (
	recallBaseINN  = "7707083893"
	recallFakeINN  = "4823006703"
	recallBaseKPP  = "773601001"
	recallFakeKPP  = "482301001"
	recallBaseOGRN = "1027700132190"
	recallFakeOGRN = "1027700014831"

	recallBaseName = "ООО «Ромашка»"
	recallFakeName = "ЗАО «Вектор»"
	recallBaseAddr = "121099, г. Москва, ул. Смоленская, д. 5"
	recallFakeAddr = "190000, г. Санкт-Петербург, ул. Морская, д. 1"

	// Дата заявки: базовая — свежая относительно точки отсчёта, подменная —
	// просроченная (срок 30 дней истекает задолго до даты процедуры).
	recallBaseDate = "20.11.2026"
	recallFakeDate = "01.06.2026"
)

// recallProcedureDate — точка отсчёта каталога сроков свежести и одновременно
// фиксированная «дата прогона» движка: оба значения совпадают, чтобы расчёт
// свежести не зависел от дня запуска теста.
const recallProcedureDate = "2026-12-01"

// recallNow — фиксированное «сейчас» для NewFreshnessHandler (детерминизм
// P2.5): все пункты каталога используют anchor = procedure_date, поэтому
// значение влияет только на run_date-пункты, которых в эталоне нет.
var recallNow = time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)

// Имена файлов эталонного пакета: на них ссылаются пункты состава, каталог
// сроков и предикаты обнаружения искажений.
const (
	recallFileZayavka = "заявка.docx"
	recallFileDogovor = "договор.docx"
	recallFileSmeta   = "смета.xlsx"
	recallFileGarant  = "гарантия.pdf"
)

// recallSpec — эталонный пакет как данные: содержимое каждого файла и
// список удалённых файлов. Искажение — функция, меняющая копию spec, после
// чего пакет записывается заново; так генератор не зависит от порядка
// применений и каждый замер начинается с чистого эталона.
type recallSpec struct {
	zayavka []string    // абзацы заявки (.docx)
	dogovor []string    // абзацы договора (.docx)
	smeta   [][2]string // строки сметы: метка, значение (.xlsx)
	pdfText string      // текст банковской гарантии (PDF)
	drop    map[string]bool
}

// recallBaseline собирает чистый эталон. Заявка содержит дату подписания и
// опись пакета (строку «Состав пакета» — в ней наименования пунктов ищутся
// для цитаты замечания состава, контракт P2.4); остальные документы дат не
// имеют, иначе класс «freshness» дал бы предупреждение «дата неоднозначна»
// или «срок не определён» на чистом пакете.
func recallBaseline() recallSpec {
	requisites := []string{
		"Полное наименование организации: " + recallBaseName,
		"ИНН: " + recallBaseINN,
		"КПП: " + recallBaseKPP,
		"ОГРН: " + recallBaseOGRN,
		"Юридический адрес: " + recallBaseAddr,
	}
	zayavka := []string{"Заявка на участие в закупке"}
	zayavka = append(zayavka, requisites...)
	zayavka = append(zayavka,
		"Дата подписания: "+recallBaseDate,
		"Состав пакета: Заявка, Договор, Смета, Банковская гарантия")
	dogovor := append([]string{"Договор поставки"}, requisites...)
	return recallSpec{
		zayavka: zayavka,
		dogovor: dogovor,
		smeta: [][2]string{
			{"Наименование", recallBaseName},
			{"ИНН", recallBaseINN},
			{"КПП", recallBaseKPP},
			{"ОГРН", recallBaseOGRN},
			{"Юридический адрес", recallBaseAddr},
			{"Итого", "1 500 000,00 руб."},
		},
		pdfText: "Bankovskaya garantiya. INN: " + recallBaseINN +
			" KPP: " + recallBaseKPP + " OGRN: " + recallBaseOGRN,
		drop: map[string]bool{},
	}
}

// recallReplaceLine заменяет в срезе строк первую с заданным префиксом и
// возвращает новый срез. Отсутствие строки — поломка самого эталона, а не
// тестируемого кода: паника уместна (образец — fillSpec в xlsx_test.go).
func recallReplaceLine(lines []string, prefix, replacement string) []string {
	out := append([]string(nil), lines...)
	for i, line := range out {
		if strings.HasPrefix(line, prefix) {
			out[i] = replacement
			return out
		}
	}
	panic("эталонная строка с префиксом " + prefix + " не найдена — эталон изменился")
}

// recallSetCell меняет значение в строке сметы с заданной меткой (in place:
// spec для каждого искажения собирается заново).
func recallSetCell(rows [][2]string, label, value string) {
	for i := range rows {
		if rows[i][0] == label {
			rows[i][1] = value
			return
		}
	}
	panic("строка сметы " + label + " не найдена — эталон изменился")
}

// writeRecallPackage записывает пакет по spec во временный каталог и
// возвращает путь к нему. Файлы из drop не записываются — так моделируется
// искажение «удаление обязательного документа».
func writeRecallPackage(t *testing.T, spec recallSpec) string {
	t.Helper()

	dir := t.TempDir()
	writeDocx := func(name string, paras []string) {
		if spec.drop[name] {
			return
		}
		var body strings.Builder
		for _, p := range paras {
			body.WriteString(textParagraph(p))
		}
		writeDocxFile(t, dir, name, body.String())
	}
	writeDocx(recallFileZayavka, spec.zayavka)
	writeDocx(recallFileDogovor, spec.dogovor)
	if !spec.drop[recallFileSmeta] {
		rows := spec.smeta
		writeXlsxBook(t, dir, recallFileSmeta, func(f *excelize.File) {
			for i, row := range rows {
				ref := strconv.Itoa(i + 1)
				if err := f.SetCellValue("Sheet1", "A"+ref, row[0]); err != nil {
					panic(err)
				}
				if err := f.SetCellValue("Sheet1", "B"+ref, row[1]); err != nil {
					panic(err)
				}
			}
		})
	}
	if !spec.drop[recallFileGarant] {
		path := filepath.Join(dir, recallFileGarant)
		if err := os.WriteFile(path, buildRecallPDF(spec.pdfText), 0o600); err != nil {
			t.Fatalf("запись %s: %v", path, err)
		}
	}
	return dir
}

// buildRecallPDF собирает минимальный одностраничный PDF с текстовым слоем
// (тот же набор объектов, что и в cliPdfTextFixture, но с произвольным
// текстом и честно посчитанной таблицей xref). Текст — только ASCII:
// Helvetica с WinAnsiEncoding кириллицу не несёт.
func buildRecallPDF(text string) []byte {
	for _, r := range text {
		if r > 126 || r == '(' || r == ')' || r == '\\' {
			panic("buildRecallPDF: текст обязан быть ASCII без скобок и обратного слэша")
		}
	}
	stream := "BT /F1 12 Tf 36 700 Td (" + text + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length " + strconv.Itoa(len(stream)) + " >>\nstream\n" + stream + "\nendstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	startxref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, startxref)
	return buf.Bytes()
}

// recallConfigText — эталонный конфиг: эталон реквизитов (ИНН, КПП, ОГРН),
// требуемый состав из четырёх обязательных пунктов (каждый файл пакета
// прикрыт пунктом — иначе класс «composition» дал бы предупреждение
// «файл не сопоставлен» на чистом эталоне), каталог сроков на заявку и три
// правила — по одному на класс.
func recallConfigText() string {
	return fmt.Sprintf(`{"version": 1,
	"orgs": [{"id": "supplier", "inn": "%s", "kpp": "%s", "ogrn": "%s"}],
	"composition": [
		{"id": "c1", "title": "Заявка", "required": true, "match": {"by": "filename", "pattern": "%s"}},
		{"id": "c2", "title": "Договор", "required": true, "match": {"by": "filename", "pattern": "%s"}},
		{"id": "c3", "title": "Смета", "required": true, "match": {"by": "filename", "pattern": "%s"}},
		{"id": "c4", "title": "Банковская гарантия", "required": true, "match": {"by": "filename", "pattern": "%s"}}
	],
	"freshness": [
		{"id": "f1", "title": "заявка", "days": 30, "anchor": "procedure_date", "procedure_date": "%s",
		 "match": {"by": "filename", "pattern": "%s"}}
	],
	"rules": [
		{"id": "reqs-1", "class": "reqs", "severity": "blocking"},
		{"id": "comp-1", "class": "composition", "severity": "blocking"},
		{"id": "fresh-1", "class": "freshness", "severity": "blocking"}
	]}`,
		recallBaseINN, recallBaseKPP, recallBaseOGRN,
		recallFileZayavka, recallFileDogovor, recallFileSmeta, recallFileGarant,
		recallProcedureDate, recallFileZayavka)
}

// writeRecallRules записывает эталонный конфиг и возвращает путь к нему.
func writeRecallRules(t *testing.T) string {
	t.Helper()

	return writeCheckRules(t, recallConfigText())
}

// runRecallEngine прогоняет пакет через движок правил так же, как checkCmd,
// но с фиксированной точкой отсчёта свежести: scanner → checkPackage →
// Runner.Run. Возвращает отчёт с заполненными Composition и Note (в CLI их
// кладёт cmd — здесь то же делает тест).
func runRecallEngine(t *testing.T, dir, rulesPath string) rules.Report {
	t.Helper()

	cfg, err := rules.LoadConfig(rulesPath)
	if err != nil {
		t.Fatalf("эталонный конфиг не читается: %v", err)
	}
	res, err := scanner.Scan(dir)
	if err != nil {
		t.Fatalf("обход пакета: %v", err)
	}

	runner := &rules.Runner{}
	runner.Register(rules.ClassReqs, rules.NewReqsHandler(cfg.Orgs))
	var compTable []rules.CompositionStatus
	runner.Register(rules.ClassComposition, rules.NewCompositionHandler(cfg.Composition, rulesPath, &compTable))
	var freshNote string
	runner.Register(rules.ClassFreshness, rules.NewFreshnessHandler(cfg.Freshness, recallNow, &freshNote))
	rep, err := runner.Run(checkPackage(res), cfg)
	if err != nil {
		t.Fatalf("прогон движка правил: %v", err)
	}
	rep.Composition = compTable
	rep.Note = freshNote
	return rep
}

// recallFindingLine — замечание одной строкой для поимённого перечисления
// ложных срабатываний и пропусков (критерий 4 #62: правило, файл, адрес).
func recallFindingLine(f rules.Finding) string {
	refs := make([]string, 0, len(f.Refs))
	for _, ref := range f.Refs {
		refs = append(refs, ref.String())
	}
	return fmt.Sprintf("%s [%s] %s — %s (адреса: %s)",
		f.Rule, f.Class, f.Severity, f.Text, strings.Join(refs, "; "))
}
