package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tenderguy/internal/rules"
	"tenderguy/internal/trace"
)

// Тесты формата отчёта check (P2.6, критерии #61):
//
//   - golden-сравнение ПОЛНОГО текста text-отчёта и ПОЛНОГО содержимого
//     report.md на синтетической фикстуре: замечания обоих классов (reqs и
//     composition), обеих критичностей, с цитатами и несколькими адресами,
//     таблица состава и непроверенный класс. Через CLI непроверенный класс
//     одновременно с замечаниями обоих классов недостижим (движок регистрирует
//     только reqs и composition, и оба получают правила), поэтому golden бьёт
//     напрямую по writeCheckText/writeCheckMarkdown — тем же приёмом, что
//     TestWriteCheckTextFindings, но сравнивает эталонную строку целиком,
//     а не Contains по кускам;
//   - 100 % трассируемость: сквозной CLI-прогон насыщенной фикстуры и цикл
//     по ВСЕМ Finding отчёта (severity, класс, id правила, текст, адрес с
//     Traced()==true, цитата у замечаний, рождённых из фрагмента-источника);
//   - стабильность порядка: перестановка правил в конфиге не меняет строки
//     отчёта, два прогона одной команды побайтово идентичны (text и markdown);
//   - внутри класса блокирующие замечания идут раньше предупреждений.

// sp — точная padding-подложка золотого эталона: ширины полей в writeCheckText
// заданы форматами %-16s/%-40s/%-14s/%-10s, и отступы эталона обязаны им
// соответствовать посимвольно.
func sp(n int) string { return strings.Repeat(" ", n) }

// goldenReport — синтетический отчёт со всеми элементами формата P2.6.
// Порядок Findings намеренно «перемешан» (предупреждения раньше блокирующих,
// классы вразнобой): группировка обязана собрать классы по алфавиту, а внутри
// класса — блокирующие раньше предупреждений, сохранив входной порядок внутри
// одной критичности (f3 раньше f4). Summary считается тем же вызовом, что
// и в продакшн-коде cmd, — эталон проверяет всю цепочку подготовки отчёта.
func goldenReport() rules.Report {
	docxRefContract := trace.Ref{File: "/пакет/договор.docx", Format: trace.FormatDocx, Page: 1, Para: 2}
	docxRefCopy := trace.Ref{File: "/пакет/договор-копия.docx", Format: trace.FormatDocx, Page: 1, Para: 1}
	docxRefRequest := trace.Ref{File: "/пакет/заявка.docx", Format: trace.FormatDocx, Page: 1, Para: 4}
	rulesItemRef := trace.Ref{File: "/пакет/rules.json", Item: "c2"}

	findings := []rules.Finding{
		{
			Rule:     "reqs-1",
			Class:    "reqs",
			Severity: rules.SeverityWarning,
			Text:     "наименование различается между документами пакета",
			Quote:    "ЗАО «Вектор»",
			Refs:     []trace.Ref{docxRefContract, docxRefRequest},
		},
		{
			Rule:     "comp-1",
			Class:    "composition",
			Severity: rules.SeverityWarning,
			Text:     "с пунктом c3 сопоставлено несколько файлов",
			Refs:     []trace.Ref{docxRefContract, docxRefCopy},
		},
		{
			Rule:     "reqs-1",
			Class:    "reqs",
			Severity: rules.SeverityBlocking,
			Text:     "ИНН в документе «договор.docx» не совпадает с эталоном",
			Quote:    "1234567894",
			Refs:     []trace.Ref{docxRefContract},
		},
		{
			Rule:     "reqs-1",
			Class:    "reqs",
			Severity: rules.SeverityBlocking,
			Text:     "наименование не совпадает с эталоном",
			Quote:    "ЗАО «Вектор»",
			Refs:     []trace.Ref{docxRefContract},
		},
		{
			Rule:     "comp-1",
			Class:    "composition",
			Severity: rules.SeverityBlocking,
			Text:     "нет документа в составе: Договор поставки (пункт c2)",
			Quote:    "Состав пакета: Договор поставки",
			Refs:     []trace.Ref{docxRefRequest, rulesItemRef},
		},
	}

	return rules.Report{
		InputDir:   "/пакет",
		RulesTotal: 3,
		Blocking:   3,
		Warnings:   2,
		Findings:   findings,
		Failed:     []rules.Failed{},
		Skipped:    []rules.Skipped{{Class: "freshness", Reason: "нет правил класса в конфиге"}},
		Summary:    rules.SummarizeClasses(findings),
		Composition: []rules.CompositionStatus{
			{Item: "c1", Title: "Заявка", Required: true, Present: true, Files: []string{"заявка.docx"}},
			{Item: "c2", Title: "Договор поставки", Required: true},
		},
	}
}

// goldenTextLines — ПОЛНЫЙ эталонный текст text-отчёта: каждая строка, включая
// пустые разделители секций и padding таблиц, посимвольно. Отступы строк
// таблицы состава выведены из формата "  %-16s %-40s обязательный: %-4s
// статус: %-5s %s": c1 → 2+«c1»+15 пробелов (16-2+1), «Заявка» (6) +35
// (40-6+1), «да» (2) +3 (4-2+1), «есть» (4) +2 (5-4+1); c2 → «Договор
// поставки» (16) +25 (40-16+1), «нет» (3) +3 (5-3+1) и пустой список файлов.
// Строка замечания: "  %-14s %-10s %s — %s" → «БЛОКИРУЮЩЕЕ» (11) +4,
// «предупреждение» (14) +1, «reqs» (4) +7, «composition» (11) +1.
func goldenTextLines() []string {
	return []string{
		"Каталог: /пакет",
		"Правил в конфиге: 3",
		"Сводка: всего замечаний 5, блокирующих 3, предупреждений 2",
		"  composition — всего 2, блокирующих 1, предупреждений 1",
		"  reqs — всего 3, блокирующих 2, предупреждений 1",
		"Вердикт: ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ: 3",
		"",
		"Состав пакета:",
		"  c1" + sp(15) + "Заявка" + sp(35) + "обязательный: да" + sp(3) + "статус: есть" + sp(2) + "заявка.docx",
		"  c2" + sp(15) + "Договор поставки" + sp(25) + "обязательный: да" + sp(3) + "статус: нет" + sp(3),
		"",
		"Непроверенные классы:",
		"  freshness — нет правил класса в конфиге",
		"",
		"Замечания [composition]:",
		"  БЛОКИРУЮЩЕЕ" + sp(4) + "composition comp-1 — нет документа в составе: Договор поставки (пункт c2)",
		"      цитата: Состав пакета: Договор поставки",
		"      адрес: /пакет/заявка.docx · стр. 1 · абз. 4",
		"      адрес: /пакет/rules.json · п. c2",
		"  предупреждение composition comp-1 — с пунктом c3 сопоставлено несколько файлов",
		"      адрес: /пакет/договор.docx · стр. 1 · абз. 2",
		"      адрес: /пакет/договор-копия.docx · стр. 1 · абз. 1",
		"",
		"Замечания [reqs]:",
		"  БЛОКИРУЮЩЕЕ" + sp(4) + "reqs" + sp(7) + "reqs-1 — ИНН в документе «договор.docx» не совпадает с эталоном",
		"      цитата: 1234567894",
		"      адрес: /пакет/договор.docx · стр. 1 · абз. 2",
		"  БЛОКИРУЮЩЕЕ" + sp(4) + "reqs" + sp(7) + "reqs-1 — наименование не совпадает с эталоном",
		"      цитата: ЗАО «Вектор»",
		"      адрес: /пакет/договор.docx · стр. 1 · абз. 2",
		"  предупреждение reqs" + sp(7) + "reqs-1 — наименование различается между документами пакета",
		"      цитата: ЗАО «Вектор»",
		"      адрес: /пакет/договор.docx · стр. 1 · абз. 2",
		"      адрес: /пакет/заявка.docx · стр. 1 · абз. 4",
	}
}

// TestWriteCheckTextGolden — критерий 1 #61: полный текст text-отчёта
// совпадает с эталоном посимвольно (не Contains по кускам).
func TestWriteCheckTextGolden(t *testing.T) {
	var buf strings.Builder
	writeCheckText(&buf, goldenReport())

	want := strings.Join(goldenTextLines(), "\n") + "\n"
	if got := buf.String(); got != want {
		t.Errorf("text-отчёт не совпадает с эталоном\n--- получили ---\n%s\n--- хотели ---\n%s\n--- побайтово ---\nполучили: %q\nхотели:   %q",
			got, want, got, want)
	}
}

// goldenMarkdownLines — ПОЛНЫЙ эталон report.md: та же структура, что и
// text-отчёт (вердикт и сводка, состав, непроверенные классы, замечания по
// классам и критичности), в markdown-разметке writeCheckMarkdown.
func goldenMarkdownLines() []string {
	return []string{
		"# Отчёт о проверке пакета",
		"",
		"- Каталог: `/пакет`",
		"- Правил в конфиге: 3",
		"- Вердикт: **ЕСТЬ БЛОКИРУЮЩИЕ ЗАМЕЧАНИЯ: 3**",
		"",
		"## Сводка",
		"",
		"Всего замечаний: 5, блокирующих: 3, предупреждений: 2.",
		"",
		"| Класс | Всего | Блокирующих | Предупреждений |",
		"|---|---|---|---|",
		"| composition | 2 | 1 | 1 |",
		"| reqs | 3 | 2 | 1 |",
		"",
		"## Состав пакета",
		"",
		"| Пункт | Наименование | Обязательный | Статус | Файлы |",
		"|---|---|---|---|---|",
		"| c1 | Заявка | да | есть | заявка.docx |",
		"| c2 | Договор поставки | да | нет |  |",
		"",
		"## Непроверенные классы",
		"",
		"- `freshness` — нет правил класса в конфиге",
		"",
		"## Замечания: класс `composition`",
		"",
		"- **БЛОКИРУЮЩЕЕ** · класс `composition` · правило `comp-1` — нет документа в составе: Договор поставки (пункт c2)",
		"  - цитата: Состав пакета: Договор поставки",
		"  - адрес: `/пакет/заявка.docx · стр. 1 · абз. 4`",
		"  - адрес: `/пакет/rules.json · п. c2`",
		"- **предупреждение** · класс `composition` · правило `comp-1` — с пунктом c3 сопоставлено несколько файлов",
		"  - адрес: `/пакет/договор.docx · стр. 1 · абз. 2`",
		"  - адрес: `/пакет/договор-копия.docx · стр. 1 · абз. 1`",
		"",
		"## Замечания: класс `reqs`",
		"",
		"- **БЛОКИРУЮЩЕЕ** · класс `reqs` · правило `reqs-1` — ИНН в документе «договор.docx» не совпадает с эталоном",
		"  - цитата: 1234567894",
		"  - адрес: `/пакет/договор.docx · стр. 1 · абз. 2`",
		"- **БЛОКИРУЮЩЕЕ** · класс `reqs` · правило `reqs-1` — наименование не совпадает с эталоном",
		"  - цитата: ЗАО «Вектор»",
		"  - адрес: `/пакет/договор.docx · стр. 1 · абз. 2`",
		"- **предупреждение** · класс `reqs` · правило `reqs-1` — наименование различается между документами пакета",
		"  - цитата: ЗАО «Вектор»",
		"  - адрес: `/пакет/договор.docx · стр. 1 · абз. 2`",
		"  - адрес: `/пакет/заявка.docx · стр. 1 · абз. 4`",
	}
}

// TestWriteCheckMarkdownGolden — критерий 2 #61: полное содержимое report.md
// на той же фикстуре совпадает с эталоном посимвольно.
func TestWriteCheckMarkdownGolden(t *testing.T) {
	path, err := writeCheckMarkdown(t.TempDir(), goldenReport())
	if err != nil {
		t.Fatalf("writeCheckMarkdown: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("отчёт не читается: %v", err)
	}

	want := strings.Join(goldenMarkdownLines(), "\n") + "\n"
	if got := string(data); got != want {
		t.Errorf("report.md не совпадает с эталоном\n--- получили ---\n%s\n--- хотели ---\n%s\n--- побайтово ---\nполучили: %q\nхотели:   %q",
			got, want, got, want)
	}
}

// ---------------------------------------------------------------------------
// Сквозная фикстура: пакет, дающий замечания обоих классов обеих критичностей
// через реальные обработчики (CLI-прогон).
// ---------------------------------------------------------------------------

// richCheckDir — пакет из трёх .docx: заявка с эталонными реквизитами и
// описью (в ней наименование отсутствующего документа — адрес и цитата
// блокирующего замечания состава), договор с чужими реквизитами (два
// блокирующих замечания reqs) и копия договора (второй файл того же пункта
// состава — предупреждение о неоднозначности с двумя адресами).
func richCheckDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "заявка.docx",
		textParagraph("Полное наименование организации: ООО «Ромашка»")+
			textParagraph("ИНН: 7707083893")+
			textParagraph("Состав пакета: Договор поставки"))
	writeDocxFile(t, dir, "договор.docx",
		textParagraph("Полное наименование организации: ЗАО «Вектор» ИНН: 1234567894"))
	writeDocxFile(t, dir, "договор-копия.docx",
		textParagraph("Полное наименование организации: ООО «Ромашка» ИНН: 7707083893"))
	return dir
}

// richConfig — конфиг с эталоном, требуемым составом из трёх пунктов и двумя
// правилами (reqs и composition). rulesBlock подставляется дословно: тест
// перестановки правил меняет только его.
func richConfig(rulesBlock string) string {
	return `{"version": 1,
		"orgs": [{"id": "supplier", "name": "ООО «Ромашка»", "inn": "7707083893"}],
		"composition": [
			{"id": "c1", "title": "Заявка", "required": true, "match": {"by": "filename", "pattern": "заявка.docx"}},
			{"id": "c2", "title": "Договор поставки", "required": true, "match": {"by": "filename", "pattern": "поставка.docx"}},
			{"id": "c3", "title": "Договор", "required": true, "match": {"by": "filename", "pattern": "договор*.docx"}}
		],
		"rules": [` + rulesBlock + `]}`
}

const richRulesReqFirst = `{"id": "reqs-1", "class": "reqs", "severity": "warning"},
	{"id": "comp-1", "class": "composition", "severity": "blocking"}`

const richRulesCompFirst = `{"id": "comp-1", "class": "composition", "severity": "blocking"},
	{"id": "reqs-1", "class": "reqs", "severity": "warning"}`

// writeRulesFile пишет конфиг заданным именем в заданный каталог (в отличие
// от writeCheckRules: тесту перестановки правил нужны два файла рядом).
func writeRulesFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
	return path
}

// TestRunCheckRichTraceability — критерий 3 #61: 100 % трассируемость.
// Цикл проходит по ВСЕМ Finding отчёта сквозного прогона и проверяет:
// severity ∈ {blocking, warning}, класс непустой, id правила непустой, текст
// непустой, хотя бы один адрес с Traced()==true. Цитата обязательна у
// замечаний, рождённых из фрагмента-источника (сверка с эталоном, внутренняя
// консистентность); у «нет документа в составе» цитаты может не быть
// (контракт P2.4) — но в этой фикстуре наименование пункта есть в описи
// заявки, поэтому цитата и адрес описи обязаны быть. Дополнительно
// проверяется, что фикстура не вырождена: представлены все ожидаемые виды
// замечаний, а у неоднозначности состава несколько адресов.
func TestRunCheckRichTraceability(t *testing.T) {
	dir := richCheckDir(t)
	rulesPath := writeRulesFile(t, t.TempDir(), "rules.json", richConfig(richRulesReqFirst))

	code, stdout, stderr := checkRun([]string{
		"--command", "check", "--input-dir", dir, "--rules", rulesPath, "--format", "json",
	})
	if code != exitProblems {
		t.Fatalf("код = %d, хотели %d (блокирующие замечания есть)\nstderr: %s", code, exitProblems, stderr)
	}

	var rep rules.Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("JSON отчёта не читается: %v\n%s", err, stdout)
	}
	if len(rep.Findings) == 0 {
		t.Fatalf("findings пусты — фикстура вырождена, тест трассируемости бессмыслен:\n%s", stdout)
	}

	seen := map[string]int{}
	for i, f := range rep.Findings {
		where := "замечание #" + strconv.Itoa(i+1) + " (" + f.Text + ")"
		if f.Severity != rules.SeverityBlocking && f.Severity != rules.SeverityWarning {
			t.Errorf("%s: severity = %q — допустимы blocking или warning", where, f.Severity)
		}
		if f.Class == "" {
			t.Errorf("%s: класс пуст", where)
		}
		if f.Rule == "" {
			t.Errorf("%s: id правила пуст", where)
		}
		if strings.TrimSpace(f.Text) == "" {
			t.Errorf("%s: текст пуст", where)
		}
		traced := 0
		for _, ref := range f.Refs {
			if ref.Traced() {
				traced++
			}
		}
		if traced == 0 {
			t.Errorf("%s: нет ни одного адреса с Traced()==true: %+v", where, f.Refs)
		}

		switch {
		case strings.Contains(f.Text, "не совпадает с эталоном"),
			strings.Contains(f.Text, "различается между документами"):
			// Замечание рождено из значения в фрагменте-источнике — цитата
			// этого фрагмента обязательна.
			if f.Quote == "" {
				t.Errorf("%s: у замечания из фрагмента-источника нет цитаты", where)
			}
			seen["сверка"]++
		case strings.Contains(f.Text, "нет документа в составе"):
			// Контракт P2.4: цитаты у «нет документа» может не быть, если
			// наименование пункта не встречается в описи. В этой фикстуре
			// опись есть — требуем цитату и адрес пункта в файле правил.
			seen["состав-пропуск"]++
			if f.Quote == "" {
				t.Errorf("%s: опись содержит наименование пункта, но цитаты нет", where)
			}
			var itemRef bool
			for _, ref := range f.Refs {
				if ref.File == rulesPath && ref.Item != "" {
					itemRef = true
				}
			}
			if !itemRef {
				t.Errorf("%s: нет адреса пункта требования в файле правил: %+v", where, f.Refs)
			}
		case strings.Contains(f.Text, "сопоставлено несколько файлов"):
			seen["неоднозначность"]++
			if len(f.Refs) < 2 {
				t.Errorf("%s: адресов %d, хотели по одному на каждый файл (≥2): %+v", where, len(f.Refs), f.Refs)
			}
		default:
			t.Errorf("%s: неожиданный вид замечания — фикстура изменилась, разбери случай явно", where)
		}
	}

	// Фикстура обязана давать все виды замечаний, иначе цикл выше проверял
	// не всё. Сверка с эталоном: наименование и ИНН договора дают два
	// блокирующих замечания; консистентность — два предупреждения (severity
	// правила reqs-1 = warning); состав — пропуск и неоднозначность.
	for kind, wantMin := range map[string]int{"сверка": 2, "состав-пропуск": 1, "неоднозначность": 1} {
		if seen[kind] < wantMin {
			t.Errorf("вид %q встретился %d раз, хотели не меньше %d:\n%s", kind, seen[kind], wantMin, stdout)
		}
	}
	if rep.Blocking < 3 || rep.Warnings < 3 {
		t.Errorf("blocking = %d, warnings = %d — хотели ≥3 и ≥3 (обе критичности, оба класса):\n%s",
			rep.Blocking, rep.Warnings, stdout)
	}
	// Сводка заполнена cmd (P2.6) и покрывает оба класса.
	summaryClasses := map[rules.Class]rules.ClassSummary{}
	for _, s := range rep.Summary {
		summaryClasses[s.Class] = s
	}
	for _, class := range []rules.Class{rules.ClassReqs, rules.ClassComposition} {
		s, ok := summaryClasses[class]
		if !ok {
			t.Errorf("в сводке нет класса %q: %+v", class, rep.Summary)
			continue
		}
		if s.Total != s.Blocking+s.Warnings {
			t.Errorf("сводка %q: total = %d ≠ blocking+warnings = %d", class, s.Total, s.Blocking+s.Warnings)
		}
		if s.Blocking == 0 || s.Warnings == 0 {
			t.Errorf("сводка %q: blocking = %d, warnings = %d — фикстура обязана дать обе критичности",
				class, s.Blocking, s.Warnings)
		}
	}
	// Оба класса зарегистрированы и имеют правила — непроверенных нет,
	// но поле в JSON обязано присутствовать пустым массивом, а не null.
	if strings.Contains(stdout, `"skipped": null`) {
		t.Errorf("skipped = null, хотели []:\n%s", stdout)
	}
}

// TestRunCheckRichBlockingBeforeWarning — критерий 5 #61: в каждой секции
// «Замечания [<класс>]» text-отчёта блокирующие строки идут раньше
// предупреждений (обработчики возвращают их в другом порядке — предупреждения
// внутренней консистентности reqs рождаются вместе с блокирующими сверкой).
func TestRunCheckRichBlockingBeforeWarning(t *testing.T) {
	dir := richCheckDir(t)
	rulesPath := writeRulesFile(t, t.TempDir(), "rules.json", richConfig(richRulesReqFirst))

	code, stdout, stderr := checkRun([]string{
		"--command", "check", "--input-dir", dir, "--rules", rulesPath,
	})
	if code != exitProblems {
		t.Fatalf("код = %d, хотели %d\nstderr: %s", code, exitProblems, stderr)
	}

	sections := 0
	blockingLines, warningLines := 0, 0
	inSection, sawWarning := false, false
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Замечания [") {
			sections++
			inSection, sawWarning = true, false
			continue
		}
		if !inSection {
			continue
		}
		switch {
		case strings.HasPrefix(line, "  БЛОКИРУЮЩЕЕ"):
			blockingLines++
			if sawWarning {
				t.Errorf("в секции блокирующее замечание напечатано после предупреждения:\n%s", stdout)
			}
		case strings.HasPrefix(line, "  предупреждение"):
			warningLines++
			sawWarning = true
		}
	}
	if sections != 2 {
		t.Errorf("секций замечаний = %d, хотели 2 (reqs и composition):\n%s", sections, stdout)
	}
	if blockingLines == 0 || warningLines == 0 {
		t.Errorf("блокирующих строк %d, предупреждений %d — хотели и те, и другие:\n%s",
			blockingLines, warningLines, stdout)
	}
}

// TestRunCheckRichDeterministic — критерий 4 #61 (побайтовый детерминизм):
// два прогона одной команды дают идентичный text-отчёт и идентичный
// report.md. Дат и времени в отчёте нет, обход каталога и группировка
// детерминированы.
func TestRunCheckRichDeterministic(t *testing.T) {
	dir := richCheckDir(t)
	rulesPath := writeRulesFile(t, t.TempDir(), "rules.json", richConfig(richRulesReqFirst))
	outDir := filepath.Join(t.TempDir(), "отчёт")

	t.Run("text: два прогона побайтово идентичны", func(t *testing.T) {
		args := []string{"--command", "check", "--input-dir", dir, "--rules", rulesPath}
		code1, first, _ := checkRun(args)
		code2, second, _ := checkRun(args)
		if code1 != exitProblems || code2 != exitProblems {
			t.Fatalf("коды прогонов = %d и %d, хотели 1 и 1", code1, code2)
		}
		if first != second {
			t.Errorf("повторный прогон не детерминирован:\n--- первый ---\n%s\n--- второй ---\n%s", first, second)
		}
	})

	t.Run("markdown: два прогона побайтово идентичны", func(t *testing.T) {
		args := []string{
			"--command", "check", "--input-dir", dir, "--rules", rulesPath,
			"--format", "markdown", "--output-dir", outDir,
		}
		if code, _, stderr := checkRun(args); code != exitProblems {
			t.Fatalf("код первого прогона = %d, stderr: %s", code, stderr)
		}
		path := filepath.Join(outDir, checkReportFileName)
		first, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("report.md не читается: %v", err)
		}
		if code, _, stderr := checkRun(args); code != exitProblems {
			t.Fatalf("код повторного прогона = %d, stderr: %s", code, stderr)
		}
		second, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("report.md не читается после повторного прогона: %v", err)
		}
		if string(first) != string(second) {
			t.Errorf("report.md не детерминирован:\n--- первый ---\n%s\n--- второй ---\n%s", first, second)
		}
	})
}

// TestRunCheckRuleOrderStable — критерий 4 #61 (перестановка правил): конфиг,
// где правила reqs и composition поменять местами, даёт байт-в-байт тот же
// text-отчёт (после нормализации пути к файлу правил: он входит в адрес
// пункта требования). Порядок строк задаёт группировка — классы по алфавиту,
// внутри класса блокирующие раньше предупреждений, — а не порядок правил;
// порядок правил влияет только на порядок Findings внутри одной критичности
// одного класса, а здесь правило каждого класса одно. Дополнительно тест
// удостоверяется, что перестановка реальна: порядок findings в JSON меняется.
func TestRunCheckRuleOrderStable(t *testing.T) {
	dir := richCheckDir(t)
	rulesDir := t.TempDir()
	rulesA := writeRulesFile(t, rulesDir, "rules-a.json", richConfig(richRulesReqFirst))
	rulesB := writeRulesFile(t, rulesDir, "rules-b.json", richConfig(richRulesCompFirst))

	runText := func(rulesPath string) (int, string) {
		code, stdout, stderr := checkRun([]string{
			"--command", "check", "--input-dir", dir, "--rules", rulesPath,
		})
		if code != exitProblems {
			t.Fatalf("код = %d, хотели 1\nstderr: %s", code, stderr)
		}
		normalized := strings.ReplaceAll(stdout, rulesPath, "<правила>")
		return code, normalized
	}

	_, textA := runText(rulesA)
	_, textB := runText(rulesB)
	if textA != textB {
		t.Errorf("перестановка правил изменила отчёт:\n--- правила reqs первым ---\n%s\n--- правила composition первым ---\n%s",
			textA, textB)
	}

	// Перестановка не вырождена: порядок findings в JSON следует порядку
	// правил конфига (Findings остаются в порядке конфига, P2.1).
	var firstClasses []string
	for _, rulesPath := range []string{rulesA, rulesB} {
		code, stdout, stderr := checkRun([]string{
			"--command", "check", "--input-dir", dir, "--rules", rulesPath, "--format", "json",
		})
		if code != exitProblems {
			t.Fatalf("код json-прогона = %d, хотели 1\nstderr: %s", code, stderr)
		}
		var rep rules.Report
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("JSON не читается: %v\n%s", err, stdout)
		}
		if len(rep.Findings) == 0 {
			t.Fatalf("findings пусты:\n%s", stdout)
		}
		firstClasses = append(firstClasses, string(rep.Findings[0].Class))
	}
	if firstClasses[0] == firstClasses[1] {
		t.Errorf("перестановка правил не изменила порядок findings в JSON (оба раза первый класс %q) — сравнение отчётов выше вырождено",
			firstClasses[0])
	}
	if firstClasses[0] != "reqs" || firstClasses[1] != "composition" {
		t.Errorf("порядок findings = %v, хотели [reqs composition]", firstClasses)
	}
}
