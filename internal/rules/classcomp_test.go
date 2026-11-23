package rules

import (
	"errors"
	"strings"
	"testing"

	"tenderguy/internal/reqs"
	"tenderguy/internal/trace"
)

// Тесты класса правил «состав пакета» (P2.4): статус «есть/нет» по пунктам
// требуемого состава, блокирующий пропуск обязательного документа,
// предупреждения на неожиданный файл и неоднозначность, устойчивость
// сопоставления к регистру и пробелам. Пакет синтетический — документы и
// файлы собираются вручную, как в classreqs_test.go.

// compFile — файл обхода пакета; RelPath одновременно играет роль имени.
func compFile(rel, kind string, supported bool) File {
	return File{RelPath: rel, Name: rel, Kind: kind, Supported: supported}
}

// compDoc — разобранный документ с фрагментами и карточкой реквизитов.
func compDoc(rel string, frags []trace.Fragment, items ...reqs.Item) Doc {
	doc := testDoc(rel, items...)
	doc.Fragments = frags
	return doc
}

// compItem — пункт требуемого состава: способ сопоставления и его значение.
func compItem(id, title string, required bool, by, value string) CompositionItem {
	m := CompositionMatch{By: by}
	switch by {
	case matchByFilename:
		m.Pattern = value
	default:
		m.Kind = value
	}
	return CompositionItem{ID: id, Title: title, Required: required, Match: m}
}

// runComp прогоняет пакет через движок с зарегистрированным классом
// «composition». Прогон идёт через Runner.Run, поэтому Finding.Validate
// (инвариант адресности, критерий 6 #59) проверяется на каждом замечании
// автоматически: замечание без адреса обернулось бы ошибкой прогона.
func runComp(t *testing.T, items []CompositionItem, pkg Package) (Report, []CompositionStatus) {
	t.Helper()

	var table []CompositionStatus
	runner := &Runner{}
	runner.Register(ClassComposition, NewCompositionHandler(items, "rules.json", &table))
	cfg := Config{
		Version:     SupportedVersion,
		Composition: items,
		Rules:       []Rule{{ID: "comp-1", Class: ClassComposition, Severity: SeverityWarning}},
	}
	rep, err := runner.Run(pkg, cfg)
	if err != nil {
		t.Fatalf("Run: неожиданная ошибка: %v", err)
	}
	return rep, table
}

// compRef — адрес замечания с данным файлом.
func hasRefWithFile(f Finding, file string) bool {
	for _, ref := range f.Refs {
		if ref.File == file && ref.Traced() {
			return true
		}
	}
	return false
}

func TestClassCompositionConstant(t *testing.T) {
	if ClassComposition != "composition" {
		t.Errorf("ClassComposition = %q, хотели %q", ClassComposition, "composition")
	}
}

func TestCompositionMissingRequired(t *testing.T) {
	tests := []struct {
		name      string
		pkg       Package
		wantQuote bool
		wantRefs  int
	}{
		{
			name: "опись упоминает пункт — адрес описи и цитата",
			pkg: Package{
				Root: "/пакет",
				Docs: []Doc{compDoc("опись.docx", []trace.Fragment{{
					Ref:  testRef("/пакет/опись.docx", 5),
					Text: "1.  ЗАЯВКА   ПО ФОРМЕ 2 — 1 шт.\n2. Договор поставки",
				}})},
				Files: []File{compFile("опись.docx", "docx", true)},
			},
			wantQuote: true,
			wantRefs:  2,
		},
		{
			name: "описи нет — только адрес пункта в конфиге",
			pkg: Package{
				Root:  "/пакет",
				Files: []File{compFile("договор.pdf", "pdf", true)},
			},
			wantRefs: 1,
		},
	}

	items := []CompositionItem{
		compItem("form-2", "Заявка по форме 2", true, matchByFilename, "заявка*.docx"),
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep, table := runComp(t, items, tc.pkg)
			found := findText(rep, "нет документа в составе")
			if len(found) != 1 {
				t.Fatalf("найдено %d замечаний «нет документа в составе»: %+v", len(found), rep.Findings)
			}
			f := found[0]
			if f.Severity != SeverityBlocking {
				t.Errorf("severity = %q, хотели blocking", f.Severity)
			}
			if !strings.Contains(f.Text, "Заявка по форме 2") || !strings.Contains(f.Text, "form-2") {
				t.Errorf("текст замечания не называет пункт: %q", f.Text)
			}
			if len(f.Refs) != tc.wantRefs {
				t.Errorf("адресов %d, хотели %d: %+v", len(f.Refs), tc.wantRefs, f.Refs)
			}
			if !hasRefWithFile(f, "rules.json") {
				t.Errorf("нет адреса пункта в файле правил: %+v", f.Refs)
			}
			if tc.wantQuote {
				if !strings.Contains(f.Quote, "ЗАЯВКА") {
					t.Errorf("цитата не содержит строку описи: %q", f.Quote)
				}
				if !hasRefWithFile(f, "/пакет/опись.docx") {
					t.Errorf("нет адреса описи: %+v", f.Refs)
				}
			}
			if len(table) != 1 || table[0].Present {
				t.Errorf("таблица = %+v, хотели один пункт со статусом «нет»", table)
			}
		})
	}
}

func TestCompositionOptionalMissingIsSilent(t *testing.T) {
	// Необязательный пункт без файлов — только статус «нет», замечания нет.
	items := []CompositionItem{
		compItem("cover", "Письмо сопроводительное", false, matchByFilename, "письмо*.docx"),
	}
	pkg := Package{
		Root:  "/пакет",
		Files: []File{compFile("заявка.docx", "docx", true)},
	}
	rep, table := runComp(t, items, pkg)
	if len(findText(rep, "Письмо сопроводительное")) != 0 {
		t.Errorf("необязательный пункт дал замечание: %+v", rep.Findings)
	}
	if len(table) != 1 || table[0].Present || table[0].Title != "Письмо сопроводительное" {
		t.Errorf("таблица = %+v, хотели один необязательный пункт со статусом «нет»", table)
	}
}

func TestCompositionUnexpectedFile(t *testing.T) {
	// Лишний файл — предупреждение, но не блокирующее замечание (критерий 4).
	items := []CompositionItem{
		compItem("form-2", "Заявка", true, matchByFilename, "заявка.docx"),
	}
	tests := []struct {
		name     string
		pkg      Package
		wantFile string
	}{
		{
			name: "неразобранный файл — адрес «файл пакета»",
			pkg: Package{
				Root: "/пакет",
				Files: []File{
					compFile("заявка.docx", "docx", true),
					compFile("архив.zip", "archive", false),
				},
			},
			wantFile: "архив.zip",
		},
		{
			name: "разобранный файл — адрес первого трассируемого фрагмента",
			pkg: Package{
				Root: "/пакет",
				Docs: []Doc{compDoc("лишний.docx", []trace.Fragment{{
					Ref: testRef("/пакет/лишний.docx", 1), Text: "текст",
				}})},
				Files: []File{
					compFile("заявка.docx", "docx", true),
					compFile("лишний.docx", "docx", true),
				},
			},
			wantFile: "/пакет/лишний.docx",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep, _ := runComp(t, items, tc.pkg)
			found := findText(rep, "файл не сопоставлен")
			if len(found) != 1 {
				t.Fatalf("найдено %d замечаний о неожиданном файле: %+v", len(found), rep.Findings)
			}
			if found[0].Severity != SeverityWarning {
				t.Errorf("severity = %q, хотели warning", found[0].Severity)
			}
			if rep.Blocking != 0 {
				t.Errorf("лишний файл заблокировал пакет: %+v", rep.Findings)
			}
			if !hasRefWithFile(found[0], tc.wantFile) {
				t.Errorf("нет адреса %q: %+v", tc.wantFile, found[0].Refs)
			}
		})
	}
}

func TestCompositionAmbiguity(t *testing.T) {
	// Два файла претендуют на один пункт — предупреждение с адресами обоих,
	// движок не выбирает первый молча (критерий 5).
	items := []CompositionItem{
		compItem("form-2", "Заявка", true, matchByFilename, "заявка*.docx"),
	}
	pkg := Package{
		Root: "/пакет",
		Files: []File{
			compFile("заявка.docx", "docx", true),
			compFile("заявка-копия.docx", "docx", true),
		},
	}
	rep, table := runComp(t, items, pkg)
	found := findText(rep, "сопоставлено несколько файлов")
	if len(found) != 1 {
		t.Fatalf("найдено %d замечаний о неоднозначности: %+v", len(found), rep.Findings)
	}
	f := found[0]
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, хотели warning", f.Severity)
	}
	if rep.Blocking != 0 {
		t.Errorf("неоднозначность заблокировала пакет: %+v", rep.Findings)
	}
	if !strings.Contains(f.Text, "заявка.docx") || !strings.Contains(f.Text, "заявка-копия.docx") {
		t.Errorf("текст не перечисляет оба файла: %q", f.Text)
	}
	if !hasRefWithFile(f, "заявка.docx") || !hasRefWithFile(f, "заявка-копия.docx") {
		t.Errorf("нет адресов обоих файлов: %+v", f.Refs)
	}
	if len(table) != 1 || !table[0].Present || len(table[0].Files) != 2 {
		t.Errorf("таблица = %+v, хотели статус «есть» с двумя файлами", table)
	}
}

func TestCompositionFilenameNormalization(t *testing.T) {
	// Сопоставление устойчиво к регистру и пробелам (критерий 5): имя файла
	// «ЗАЯВКА   Final.DOCX» обязано совпасть с паттерном «заявка final.docx».
	items := []CompositionItem{
		compItem("final", "Итоговая заявка", true, matchByFilename, "заявка final.docx"),
	}
	pkg := Package{
		Root:  "/пакет",
		Files: []File{compFile("ЗАЯВКА   Final.DOCX", "docx", true)},
	}
	rep, table := runComp(t, items, pkg)
	if len(rep.Findings) != 0 {
		t.Errorf("нормализация не сработала, замечания: %+v", rep.Findings)
	}
	if len(table) != 1 || !table[0].Present || len(table[0].Files) != 1 {
		t.Errorf("таблица = %+v, хотели статус «есть»", table)
	}
}

func TestCompositionKindAndRequisite(t *testing.T) {
	tests := []struct {
		name        string
		item        CompositionItem
		pkg         Package
		wantPresent bool
		wantFiles   []string
	}{
		{
			name:        "kind находит все файлы типа, включая неразобранные",
			item:        compItem("arc", "Архив пакета", false, matchByKind, "archive"),
			pkg:         Package{Files: []File{compFile("a.zip", "archive", false), compFile("b.7z", "archive", false), compFile("c.docx", "docx", true)}},
			wantPresent: true,
			wantFiles:   []string{"a.zip", "b.7z"},
		},
		{
			name: "requisite находит документ с значением вида в карточке",
			item: compItem("card", "Карточка реквизитов", true, matchByRequisite, reqs.KindINN10),
			pkg: Package{
				Docs: []Doc{
					compDoc("пусто.docx", nil),
					testDoc("карточка.docx", testItem(reqs.KindINN10, "7707083893", testRef("/пакет/карточка.docx", 1))),
				},
				Files: []File{compFile("пусто.docx", "docx", true), compFile("карточка.docx", "docx", true)},
			},
			wantPresent: true,
			wantFiles:   []string{"карточка.docx"},
		},
		{
			name:        "requisite без значения вида — статус «нет»",
			item:        compItem("card", "Карточка реквизитов", false, matchByRequisite, reqs.KindKPP),
			pkg:         Package{Docs: []Doc{testDoc("карточка.docx", testItem(reqs.KindINN10, "7707083893", testRef("/пакет/карточка.docx", 1)))}},
			wantPresent: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, table := runComp(t, []CompositionItem{tc.item}, tc.pkg)
			if len(table) != 1 {
				t.Fatalf("таблица = %+v, хотели одну строку", table)
			}
			if table[0].Present != tc.wantPresent {
				t.Errorf("Present = %v, хотели %v", table[0].Present, tc.wantPresent)
			}
			if strings.Join(table[0].Files, ",") != strings.Join(tc.wantFiles, ",") {
				t.Errorf("Files = %v, хотели %v", table[0].Files, tc.wantFiles)
			}
		})
	}
}

func TestCompositionTableOrderAndFullCoverage(t *testing.T) {
	// Порядок строк таблицы равен порядку пунктов конфига; когда все пункты
	// покрыты и лишних файлов нет, замечаний нет вовсе.
	items := []CompositionItem{
		compItem("price", "Спецификация цены", true, matchByKind, "xlsx"),
		compItem("form-2", "Заявка", true, matchByFilename, "заявка.docx"),
	}
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{compDoc("заявка.docx", []trace.Fragment{{
			Ref: testRef("/пакет/заявка.docx", 1), Text: "Заявка",
		}})},
		Files: []File{
			compFile("цена.xlsx", "xlsx", true),
			compFile("заявка.docx", "docx", true),
		},
	}
	rep, table := runComp(t, items, pkg)
	if len(rep.Findings) != 0 {
		t.Errorf("полностью покрытый пакет дал замечания: %+v", rep.Findings)
	}
	if len(table) != 2 || table[0].Item != "price" || table[1].Item != "form-2" {
		t.Fatalf("порядок строк таблицы нарушен: %+v", table)
	}
	for _, st := range table {
		if !st.Present || !st.Required || len(st.Files) != 1 {
			t.Errorf("строка %+v: хотели статус «есть» с одним файлом", st)
		}
	}
}

func TestCompositionBadGlobIsConfigErr(t *testing.T) {
	// badGlobPattern в LoadConfig ловит только незакрытую символьную группу
	// «[», поэтому битый диапазон («[a-]», «[-a]») доходит до сопоставления.
	// Ошибка filepath.Match обязана нести ErrConfig: это опечатка оператора в
	// конфиге, и CLI (checkCmd) возвращает код 2 (exitUsage), а не 1. Кейс
	// «заявка*[]» через CLI недостижим (LoadConfig отклоняет его как
	// незакрытую группу) — здесь это защитная проверка самой обёртки.
	tests := []struct {
		name    string
		pattern string
	}{
		{name: "диапазон без правой границы", pattern: "заявка*[a-].docx"},
		{name: "диапазон без левой границы", pattern: "заявка*[-a].docx"},
		{name: "пустая символьная группа — защитный кейс обёртки", pattern: "заявка*[]"},
	}
	pkg := Package{
		Root:  "/пакет",
		Files: []File{compFile("заявка-1.docx", "docx", true)},
	}
	rule := Rule{ID: "comp-1", Class: ClassComposition, Severity: SeverityWarning}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewCompositionHandler([]CompositionItem{
				compItem("form-2", "Заявка", true, matchByFilename, tc.pattern),
			}, "rules.json", nil)
			_, err := handler(rule, pkg)
			if err == nil {
				t.Fatalf("паттерн %q: хотели ошибку, получено nil", tc.pattern)
			}
			if !errors.Is(err, ErrConfig) {
				t.Errorf("ошибка обязана нести ErrConfig (код выхода 2): %v", err)
			}
			if !strings.Contains(err.Error(), "form-2") {
				t.Errorf("сообщение не называет пункт состава: %v", err)
			}
		})
	}
}

func TestCompositionEmptyItems(t *testing.T) {
	// Требуемый состав не задан: ни замечаний, ни строк таблицы — каждый файл
	// пакета не объявляется «неожиданным».
	pkg := Package{
		Root:  "/пакет",
		Files: []File{compFile("что-то.docx", "docx", true)},
	}
	rep, table := runComp(t, nil, pkg)
	if len(rep.Findings) != 0 {
		t.Errorf("пустой состав дал замечания: %+v", rep.Findings)
	}
	if len(table) != 0 {
		t.Errorf("таблица = %+v, хотели пустую", table)
	}
}
