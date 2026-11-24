package zd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/rules"
	"tenderguy/internal/trace"
)

// Регрессия ревью #63 (фикс-раунд 3): splitTitleCell резал текст по каждой
// точке и склеивал обратно через ". ", из-за чего внутренняя нумерация
// («пункта 5.2.1») превращалась в «пункта 5. 2. 1.», а в конце наименования
// появлялась лишняя точка. Title обязан быть дословным фрагментом исходной
// ячейки за вычетом ссылки на раздел с формами и условия предоставления —
// без добавленных и без потерянных знаков. Фикстуры синтетические: документ
// заказчика в тестах не используется.

// Обе реальные формы ссылки на раздел с образцами форм (реальная закупочная
// документация).
const (
	fxRefFull  = "Типовая форма и инструкция по заполнению приведены в Разделе 9 «Образцы основных форм документов, включаемых в заявку на участие в закупке» настоящей Закупочной документации"
	fxRefShort = "Типовая форма приведена в Разделе 9 «Образцы основных форм документов» настоящей Закупочной документации"
)

// TestSplitTitleCellVerbatim — юнит-проверка разбора ячейки наименования:
// дословность title/condition и число разрешённых упоминаний инструкций.
func TestSplitTitleCellVerbatim(t *testing.T) {
	tests := []struct {
		name          string
		cell          string
		wantTitle     string
		wantCondition string
		wantResolved  int
	}{
		{
			name:         "внутренняя нумерация и полная форма ссылки",
			cell:         "Документы, предусмотренные подпунктом а) пункта 5.2.1\n" + fxRefFull,
			wantTitle:    "Документы, предусмотренные подпунктом а) пункта 5.2.1",
			wantResolved: 1,
		},
		{
			name:         "внутренняя нумерация и короткая форма ссылки",
			cell:         "Документы, предусмотренные подпунктом а) пункта 5.2.1\n" + fxRefShort,
			wantTitle:    "Документы, предусмотренные подпунктом а) пункта 5.2.1",
			wantResolved: 0,
		},
		{
			name:      "без ссылки и условия: ячейка сохраняется дословно",
			cell:      "Проект договора поставки товара",
			wantTitle: "Проект договора поставки товара",
		},
		{
			name:          "условие в скобках изъято дословно, наименование не искажено",
			cell:          "Анкета участника (предоставляется только в случае наличия требования в Разделе 6 «Техническая часть»)\n" + fxRefFull,
			wantTitle:     "Анкета участника",
			wantCondition: "(предоставляется только в случае наличия требования в Разделе 6 «Техническая часть»)",
			wantResolved:  1,
		},
		{
			name:      "точки внутри чисел и кавычек-«ёлочек» не рвут наименование",
			cell:      "Сведения по позициям 5.2.1, 5.2.2 раздела «Техническая часть. Требования к товару»",
			wantTitle: "Сведения по позициям 5.2.1, 5.2.2 раздела «Техническая часть. Требования к товару»",
		},
		{
			name:         "собственная точка наименования не удваивается",
			cell:         "Опись заявки.\n" + fxRefFull,
			wantTitle:    "Опись заявки.",
			wantResolved: 1,
		},
		{
			name:         "ссылка прилипла к наименованию без точки и перевода строки",
			cell:         "Письмо о подаче оферты " + fxRefFull,
			wantTitle:    "Письмо о подаче оферты",
			wantResolved: 1,
		},
		{
			name:         "прилипшая ссылка начинается с инструкции без слова «типовая форма»",
			cell:         "Сведения о товаре Инструкция по заполнению приведена в Разделе 9 «Образцы основных форм документов»",
			wantTitle:    "Сведения о товаре",
			wantResolved: 1,
		},
		{
			name:      "инструкции без ссылки на раздел форм не теряют содержимое",
			cell:      "Сведения об инструкциях по эксплуатации оборудования",
			wantTitle: "Сведения об инструкциях по эксплуатации оборудования",
		},
		{
			// Регрессия ревью #63 (фикс-раунд 4): «инструкц» в самом
			// наименовании не может быть опорной точкой разреза — иначе
			// title обрезался бы до «Сведения об ». Разрешённым считается
			// только упоминание в отрезанном суффиксе-ссылке (1), а не в
			// префиксе-наименовании.
			name:         "наименование с «инструкциями по эксплуатации» и прилипшая ссылка",
			cell:         "Сведения об инструкциях по эксплуатации оборудования " + fxRefFull,
			wantTitle:    "Сведения об инструкциях по эксплуатации оборудования",
			wantResolved: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, condition, resolved := splitTitleCell(tt.cell)
			if title != tt.wantTitle {
				t.Errorf("title = %q, хочу %q", title, tt.wantTitle)
			}
			if condition != tt.wantCondition {
				t.Errorf("condition = %q, хочу %q", condition, tt.wantCondition)
			}
			if resolved != tt.wantResolved {
				t.Errorf("resolved = %d, хочу %d", resolved, tt.wantResolved)
			}
		})
	}
}

// TestSectionRefStart — маркеры начала ссылки не срабатывают на произвольном
// тексте: началом ссылки считается только позиция, хвост от которой сам
// является ссылкой на раздел с формами.
func TestSectionRefStart(t *testing.T) {
	tests := []struct {
		name string
		norm string
		want int
	}{
		{
			name: "фрагмент целиком — полная форма ссылки",
			norm: "типовая форма и инструкция по заполнению приведены в разделе 9",
			want: 0,
		},
		{
			name: "прилипшая ссылка начинается с типовой формы",
			norm: "письмо о подаче оферты типовая форма приведена в разделе 9",
			want: len("письмо о подаче оферты "),
		},
		{
			name: "прилипшая ссылка начинается с инструкции",
			norm: "сведения о товаре инструкция по заполнению приведена в разделе 9",
			want: len("сведения о товаре "),
		},
		{
			name: "инструкции без привязки к разделу форм — не начало ссылки",
			norm: "сведения по разделу 6 в соответствии с инструкциями и регламентом площадки",
			want: -1,
		},
		{
			name: "грубое «инструкц» в наименовании — не опорная точка разреза",
			norm: "сведения об инструкциях по эксплуатации оборудования типовая форма и инструкция по заполнению приведены в разделе 9",
			want: len("сведения об инструкциях по эксплуатации оборудования "),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sectionRefStart(tt.norm); got != tt.want {
				t.Errorf("sectionRefStart(%q) = %d, хочу %d", tt.norm, got, tt.want)
			}
		})
	}
}

// TestCompositionTitleVerbatimThroughExtract — те же ветки через полный
// конвейер извлечения (синтетический .docx → CompositionItem): Required и
// Condition формируются из результата splitTitleCell без искажений.
func TestCompositionTitleVerbatimThroughExtract(t *testing.T) {
	tests := []struct {
		name          string
		titleCell     string
		wantTitle     string
		wantCondition string
		wantRequired  bool
	}{
		{
			name:         "внутренняя нумерация и полная форма ссылки",
			titleCell:    "Документы, предусмотренные подпунктом а) пункта 5.2.1\n" + fxRefFull,
			wantTitle:    "Документы, предусмотренные подпунктом а) пункта 5.2.1",
			wantRequired: true,
		},
		{
			name:         "внутренняя нумерация и короткая форма ссылки",
			titleCell:    "Документы, предусмотренные подпунктом а) пункта 5.2.1\n" + fxRefShort,
			wantTitle:    "Документы, предусмотренные подпунктом а) пункта 5.2.1",
			wantRequired: true,
		},
		{
			name:         "без ссылки и условия",
			titleCell:    "Проект договора поставки товара",
			wantTitle:    "Проект договора поставки товара",
			wantRequired: true,
		},
		{
			name:          "условие в скобках",
			titleCell:     "Анкета участника (предоставляется только в случае наличия требования в Разделе 6 «Техническая часть»)\n" + fxRefFull,
			wantTitle:     "Анкета участника",
			wantCondition: "(предоставляется только в случае наличия требования в Разделе 6 «Техническая часть»)",
			wantRequired:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := extractBody(t, compTable(
				[]string{"1", tt.titleCell, "«Файл»", "Doc"},
			))
			if res.Composition.Status != StatusFull || len(res.Composition.Items) != 1 {
				t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
			}
			it := res.Composition.Items[0]
			if it.Title != tt.wantTitle {
				t.Errorf("title = %q, хочу %q", it.Title, tt.wantTitle)
			}
			if it.Condition != tt.wantCondition {
				t.Errorf("condition = %q, хочу %q", it.Condition, tt.wantCondition)
			}
			if it.Required != tt.wantRequired {
				t.Errorf("required = %v, хочу %v", it.Required, tt.wantRequired)
			}
		})
	}
}

// TestCompositionTitleFoundInInventory — регрессионная связка с P2.4:
// findTitleFragment в internal/rules ищет наименование пункта подстрокой в
// тексте описи. Искажённый title («пункта 5. 2. 1.») в описи («пункта
// 5.2.1») не находится, и замечание «нет документа в составе» теряет адрес
// «где применимо». Проверка идёт через публичный rules.NewCompositionHandler,
// как в приёмочном тесте TestCompositionFeedsRulesEngine.
func TestCompositionTitleFoundInInventory(t *testing.T) {
	const title = "Документы, предусмотренные подпунктом а) пункта 5.2.1"
	res := extractBody(t, compTable(
		[]string{"1", title + "\n" + fxRefFull, "«Справка»", "Doc"},
	))
	if res.Composition.Status != StatusFull || len(res.Composition.Items) != 1 {
		t.Fatalf("состав: %s — %s", res.Composition.Status, res.Composition.Reason)
	}
	it := res.Composition.Items[0]
	if it.Title != title {
		t.Fatalf("title = %q, хочу %q", it.Title, title)
	}

	// Тот же массив, что печатает --emit composition, → строгий LoadConfig.
	data, err := json.Marshal([]map[string]any{{
		"id":       it.ID,
		"title":    it.Title,
		"required": it.Required,
		"match":    map[string]any{"by": "filename", "pattern": it.Pattern},
	}})
	if err != nil {
		t.Fatalf("сериализация: %v", err)
	}
	config := `{"version":1,"composition":` + string(data) +
		`,"rules":[{"id":"comp","class":"composition","severity":"blocking"}]}`
	configPath := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	cfg, err := rules.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig отверг извлечённый пункт: %v", err)
	}

	// Синтетический пакет: опись упоминает пункт дословно, а самого файла
	// «Справка» в пакете нет — рождается замечание «нет документа в составе».
	inventory := rules.Doc{
		Path:    "/пакет/1Опись.docx",
		RelPath: "1Опись.docx",
		Format:  trace.FormatDocx,
		Fragments: []trace.Fragment{{
			Ref:  trace.Ref{File: "1Опись.docx", Format: trace.FormatDocx, Para: 5},
			Text: "1. " + title + " — 1 шт.",
		}},
	}
	pkg := rules.Package{
		Root:  "/пакет",
		Docs:  []rules.Doc{inventory},
		Files: []rules.File{{RelPath: "1Опись.docx", Name: "1Опись.docx", Kind: "docx", Supported: true}},
	}
	var table []rules.CompositionStatus
	handler := rules.NewCompositionHandler(cfg.Composition, configPath, &table)
	findings, err := handler(cfg.Rules[0], pkg)
	if err != nil {
		t.Fatalf("обработчик состава: %v", err)
	}

	var missing *rules.Finding
	for i := range findings {
		if strings.Contains(findings[i].Text, "нет документа в составе") {
			missing = &findings[i]
			break
		}
	}
	if missing == nil {
		t.Fatalf("замечание «нет документа в составе» не найдено: %+v", findings)
	}
	if len(missing.Refs) != 2 {
		t.Fatalf("адресов %d, хочу 2 (опись + пункт конфига): %+v", len(missing.Refs), missing.Refs)
	}
	if missing.Refs[0].File != "1Опись.docx" || !missing.Refs[0].Traced() {
		t.Errorf("адрес «где применимо» не опись или не трассируется: %+v", missing.Refs[0])
	}
	if !strings.Contains(missing.Quote, title) {
		t.Errorf("цитата не содержит наименование дословно: %q", missing.Quote)
	}
}
