package rules

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tenderguy/internal/reqs"
	"tenderguy/internal/trace"
)

// Тесты класса правил «сроки свежести» (P2.5). Table-driven покрытие
// критериев #60: каталог «тип документа × срок × точка отсчёта» как данные
// конфига (1), просрочка — блокирующее замечание с датой, сроком, точкой
// отсчёта и адресом (2), пустой каталог — пометка «не проверяется» (3),
// дата без пункта каталога — warning (4), несколько дат — одно замечание со
// всеми адресами (5), 100 % замечаний имеют адрес (6, проверяется в harness
// по каждому замечанию), «срок истекает сегодня» — не просрочка (7).
// Прогон идёт через Runner.Run, поэтому Finding.Validate (severity и адрес)
// применяется к каждому замечанию автоматически.

// freshRunTime — фиксированная «дата прогона»: время с часами/минутами,
// чтобы dateOnly (урезание до дня) тоже участвовал в проверке.
var freshRunTime = time.Date(2026, 2, 1, 15, 4, 5, 0, time.UTC)

// freshItem — пункт каталога сроков с точкой отсчёта «дата прогона»:
// сопоставление по имени файла.
func freshItem(id string, days int, pattern string) FreshnessItem {
	return FreshnessItem{
		ID: id, Title: "Справка ФНС", Days: days,
		Anchor: anchorRunDate,
		Match:  CompositionMatch{By: matchByFilename, Pattern: pattern},
	}
}

// freshItemProc — пункт каталога с точкой отсчёта «дата процедуры».
func freshItemProc(id string, days int, pattern, procDate string) FreshnessItem {
	item := freshItem(id, days, pattern)
	item.Anchor = anchorProcedureDate
	item.ProcedureDate = procDate
	return item
}

// freshPkg — пакет: на каждый документ — запись в Files (без неё
// сопоставление по имени файла не находит документ) и в Docs.
func freshPkg(docs ...Doc) Package {
	pkg := Package{Root: "/пакет"}
	for _, d := range docs {
		pkg.Files = append(pkg.Files, File{
			RelPath: d.RelPath, Name: filepath.Base(d.RelPath),
			Kind: "docx", Supported: true,
		})
		pkg.Docs = append(pkg.Docs, d)
	}
	return pkg
}

// freshNoCardDoc — разобранный файл без карточки реквизитов (Reqs == nil):
// docDates обязан вернуть пустой список без паники.
func freshNoCardDoc(rel string) Doc {
	return Doc{Path: "/пакет/" + rel, RelPath: rel, Format: "docx"}
}

// runFresh прогоняет пакет через движок с зарегистрированным классом
// «freshness» и возвращает отчёт вместе с пометкой обработчика.
func runFresh(t *testing.T, items []FreshnessItem, pkg Package) (Report, string) {
	t.Helper()

	var note string
	runner := &Runner{}
	runner.Register(ClassFreshness, NewFreshnessHandler(items, freshRunTime, &note))
	cfg := Config{
		Version:   SupportedVersion,
		Freshness: items,
		Rules:     []Rule{{ID: "fresh-1", Class: ClassFreshness, Severity: SeverityWarning}},
	}
	rep, err := runner.Run(pkg, cfg)
	if err != nil {
		t.Fatalf("Run: неожиданная ошибка: %v", err)
	}
	return rep, note
}

// hasPara — в адресах замечания есть абзац para файла file.
func hasPara(f Finding, file string, para int) bool {
	for _, ref := range f.Refs {
		if ref.File == file && ref.Para == para {
			return true
		}
	}
	return false
}

func TestClassFreshnessConstant(t *testing.T) {
	if ClassFreshness != "freshness" {
		t.Errorf("ClassFreshness = %q, хотели %q", ClassFreshness, "freshness")
	}
}

func TestFreshnessHandler(t *testing.T) {
	tests := []struct {
		name string
		// items — каталог сроков; nil — пустой каталог.
		items []FreshnessItem
		pkg   Package
		// wantNote — пометка обработчика (критерий 3).
		wantNote string
		// wantCount — ожидаемое число замечаний.
		wantCount int
		// wantSub, wantSev, wantParas — проверка единственного замечания.
		wantSub   string
		wantSev   Severity
		wantParas []int
		// verify — дополнительная проверка отчёта (несколько замечаний,
		// состав текста).
		verify func(t *testing.T, rep Report)
	}{
		{
			// Критерий 3: пустой каталог — явная пометка, ноль замечаний.
			name:      "пустой каталог — пометка «не проверяется» и ноль замечаний",
			items:     nil,
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2020-01-01", testRef("/пакет/справка.docx", 3)))),
			wantNote:  "не проверяется: каталог сроков отсутствует",
			wantCount: 0,
		},
		{
			// Критерии 1, 2: procedure_date — просрочка от фиксированной даты.
			// От даты прогона (2026-02-01) документ был бы ещё свеж:
			// истечение 2026-02-14 — значит, точка отсчёта именно из конфига.
			name:      "procedure_date — просрочка считается от фиксированной даты, не от даты прогона",
			items:     []FreshnessItem{freshItemProc("fns", 30, "справка*.docx", "2026-03-01")},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2026-01-15", testRef("/пакет/справка.docx", 3)))),
			wantCount: 1,
			wantSev:   SeverityBlocking,
			wantSub:   "просрочен",
			wantParas: []int{3},
			verify: func(t *testing.T, rep Report) {
				text := rep.Findings[0].Text
				for _, want := range []string{
					"дата документа 2026-01-15",
					"срок свежести 30 дн.",
					"Справка ФНС",
					"точка отсчёта — дата процедуры 2026-03-01",
					"срок истёк 2026-02-14",
				} {
					if !strings.Contains(text, want) {
						t.Errorf("в тексте замечания нет %q:\n%s", want, text)
					}
				}
				if rep.Blocking != 1 {
					t.Errorf("blocking = %d, хотели 1", rep.Blocking)
				}
			},
		},
		{
			// Критерий 1: та же дата отсчёта из конфига — документ свежий
			// относительно даты процедуры, хотя от даты прогона был бы просрочен.
			name:      "procedure_date в прошлом — свежо от неё, хотя от даты прогона была бы просрочка",
			items:     []FreshnessItem{freshItemProc("fns", 30, "справка*.docx", "2026-01-10")},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2025-12-20", testRef("/пакет/справка.docx", 3)))),
			wantCount: 0,
		},
		{
			// Критерии 1, 2, 7: run_date — истечение вчера относительно прогона.
			name:      "run_date — просрочка (срок истёк вчера), в тексте дата прогона",
			items:     []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2026-01-01", testRef("/пакет/справка.docx", 3)))),
			wantCount: 1,
			wantSev:   SeverityBlocking,
			wantSub:   "просрочен",
			wantParas: []int{3},
			verify: func(t *testing.T, rep Report) {
				if !strings.Contains(rep.Findings[0].Text, "точка отсчёта — дата прогона 2026-02-01") {
					t.Errorf("в тексте нет точки отсчёта «дата прогона»:\n%s", rep.Findings[0].Text)
				}
			},
		},
		{
			// Критерий 7: anchor == дата истечения — не просрочка.
			name:      "срок истекает сегодня — не просрочка",
			items:     []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2026-01-02", testRef("/пакет/справка.docx", 3)))),
			wantCount: 0,
		},
		{
			// Критерии 5, 6: две различные даты, одна просрочена — ОДНО
			// блокирующее замечание со всеми адресами.
			name:  "две даты, одна просрочена — блокирующая неоднозначность, перечислены оба адреса",
			items: []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				testItem(reqs.KindDate, "2025-12-01", testRef("/пакет/справка.docx", 3)),
				testItem(reqs.KindDate, "2026-01-30", testRef("/пакет/справка.docx", 9)))),
			wantCount: 1,
			wantSev:   SeverityBlocking,
			wantSub:   "неоднозначна",
			wantParas: []int{3, 9},
			verify: func(t *testing.T, rep Report) {
				f := rep.Findings[0]
				for _, want := range []string{"2025-12-01", "2026-01-30", "ПРОСРОЧЕН", "определяет специалист"} {
					if !strings.Contains(f.Text, want) {
						t.Errorf("в тексте неоднозначности нет %q:\n%s", want, f.Text)
					}
				}
				if !strings.Contains(f.Quote, "2025-12-01") || !strings.Contains(f.Quote, "2026-01-30") {
					t.Errorf("в цитате нет обеих дат: %q", f.Quote)
				}
			},
		},
		{
			// Критерий 5: две даты, обе свежие — ОДНО предупреждение.
			name:  "две даты, обе свежие — предупреждение со всеми адресами",
			items: []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				testItem(reqs.KindDate, "2026-01-20", testRef("/пакет/справка.docx", 3)),
				testItem(reqs.KindDate, "2026-01-25", testRef("/пакет/справка.docx", 9)))),
			wantCount: 1,
			wantSev:   SeverityWarning,
			wantSub:   "неоднозначна",
			wantParas: []int{3, 9},
			verify: func(t *testing.T, rep Report) {
				if strings.Contains(rep.Findings[0].Text, "ПРОСРОЧЕН") {
					t.Errorf("свежие даты помечены просрочкой:\n%s", rep.Findings[0].Text)
				}
				if rep.Warnings != 1 || rep.Blocking != 0 {
					t.Errorf("счётчики = blocking %d / warnings %d, хотели 0/1", rep.Blocking, rep.Warnings)
				}
			},
		},
		{
			// Критерий 4: дата есть, сопоставимого пункта каталога нет.
			name:      "дата есть, пункта каталога нет — warning «срок свежести не определён»",
			items:     []FreshnessItem{freshItem("egrul", 30, "выписка*.docx")},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2026-01-30", testRef("/пакет/справка.docx", 3)))),
			wantCount: 1,
			wantSev:   SeverityWarning,
			wantSub:   "срок свежести не определён",
			wantParas: []int{3},
			verify: func(t *testing.T, rep Report) {
				if !strings.Contains(rep.Findings[0].Text, "2026-01-30") {
					t.Errorf("в тексте не определена дата документа:\n%s", rep.Findings[0].Text)
				}
			},
		},
		{
			// Критерий 8: документ сопоставлен с пунктом, но дат нет —
			// ни просрочки, ни «не определён» (измерять нечего).
			name:      "документ матчит пункт, но дат нет — молча",
			items:     []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindINN10, "7707083893", testRef("/пакет/справка.docx", 2)))),
			wantCount: 0,
		},
		{
			// Критерий 8: невалидные и неразобранные даты пропускаются —
			// Valid=false (забота класса «reqs») и Value вне формата ГГГГ-ММ-ДД.
			name:  "невалидные даты пропускаются — замечаний нет",
			items: []FreshnessItem{freshItem("egrul", 30, "выписка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				reqs.Item{Kind: reqs.KindDate, Value: "2020-01-01", Raw: "2020-01-01", Valid: false,
					Refs: []trace.Ref{testRef("/пакет/справка.docx", 3)}},
				reqs.Item{Kind: reqs.KindDate, Value: "не дата", Raw: "не дата", Valid: true,
					Refs: []trace.Ref{testRef("/пакет/справка.docx", 4)}})),
			wantCount: 0,
		},
		{
			// Критерий 8: одна дата в нескольких местах документа — один
			// кандидат со всеми адресами (refs сливает reqs.Item).
			name:  "повтор одной даты — один кандидат со всеми адресами",
			items: []FreshnessItem{freshItem("egrul", 30, "выписка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				testItem(reqs.KindDate, "2026-01-15",
					testRef("/пакет/справка.docx", 3), testRef("/пакет/справка.docx", 7)))),
			wantCount: 1,
			wantSev:   SeverityWarning,
			wantSub:   "срок свежести не определён",
			wantParas: []int{3, 7},
			verify: func(t *testing.T, rep Report) {
				if n := strings.Count(rep.Findings[0].Text, "2026-01-15"); n != 1 {
					t.Errorf("дата перечислена %d раз, хотели один:\n%s", n, rep.Findings[0].Text)
				}
			},
		},
		{
			// Критерий 8: файл матчит пункт, но не разобран (нет в Docs) —
			// пропуск без ошибки: неполнота входа уже видна в Failed.
			name:  "файл матчит пункт, но не разобран — пропуск",
			items: []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg: Package{Root: "/пакет", Files: []File{
				{RelPath: "справка.docx", Name: "справка.docx", Kind: "docx", Supported: true},
			}},
			wantCount: 0,
		},
		{
			// Критерий 8: документ без карточки (Reqs == nil) — docDates
			// возвращает пусто без паники.
			name:      "документ без карточки реквизитов — молча",
			items:     []FreshnessItem{freshItem("fns", 30, "*.docx")},
			pkg:       freshPkg(freshNoCardDoc("справка.docx")),
			wantCount: 0,
		},
		{
			// Критерии 6, 8: у даты нет адресов — запасной адрес документа
			// (первый трассируемый реквизит карточки).
			name:  "просрочка без адресов даты — запасной адрес документа",
			items: []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				reqs.Item{Kind: reqs.KindDate, Value: "2026-01-01", Raw: "2026-01-01", Valid: true},
				testItem(reqs.KindINN10, "7707083893", testRef("/пакет/справка.docx", 5)))),
			wantCount: 1,
			wantSev:   SeverityBlocking,
			wantSub:   "просрочен",
			wantParas: []int{5},
		},
		{
			// Критерии 4, 6, 8: «не определён» без адресов даты — тот же
			// запасной адрес.
			name:  "«не определён» без адресов даты — запасной адрес документа",
			items: []FreshnessItem{freshItem("egrul", 30, "выписка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				reqs.Item{Kind: reqs.KindDate, Value: "2026-01-15", Raw: "2026-01-15", Valid: true},
				testItem(reqs.KindINN10, "7707083893", testRef("/пакет/справка.docx", 5)))),
			wantCount: 1,
			wantSev:   SeverityWarning,
			wantSub:   "срок свежести не определён",
			wantParas: []int{5},
		},
		{
			// Критерий 6: адресов нет ни у даты, ни у документа — замечание
			// пропускается (инвариант адресности), а не обрывает прогон.
			name:  "адресов нет совсем — пропуск вместо обрыва прогона",
			items: []FreshnessItem{freshItem("fns", 30, "справка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				reqs.Item{Kind: reqs.KindDate, Value: "2026-01-01", Raw: "2026-01-01", Valid: true})),
			wantCount: 0,
		},
		{
			// Критерии 4, 6: то же для «не определён» — документ не матчит
			// каталог, адресов нет ни у даты, ни у документа — пропуск.
			name:  "«не определён» без единого адреса — пропуск",
			items: []FreshnessItem{freshItem("egrul", 30, "выписка*.docx")},
			pkg: freshPkg(testDoc("справка.docx",
				reqs.Item{Kind: reqs.KindDate, Value: "2026-01-15", Raw: "2026-01-15", Valid: true})),
			wantCount: 0,
		},
		{
			// Критерий 8: один файл матчит несколько пунктов каталога —
			// замечание только по просроченному пункту.
			name: "два пункта на один файл — замечание только по просроченному",
			items: []FreshnessItem{
				freshItem("fns30", 30, "справка*.docx"),
				freshItem("fns365", 365, "справка*.docx"),
			},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2026-01-01", testRef("/пакет/справка.docx", 3)))),
			wantCount: 1,
			wantSev:   SeverityBlocking,
			wantSub:   "срок свежести 30 дн.",
			wantParas: []int{3},
		},
		{
			// Критерий 8: оба пункта просрочены — по замечанию на каждый.
			name: "два пункта на один файл — оба просрочены, два замечания",
			items: []FreshnessItem{
				freshItem("fns30", 30, "справка*.docx"),
				freshItem("fns365", 365, "справка*.docx"),
			},
			pkg:       freshPkg(testDoc("справка.docx", testItem(reqs.KindDate, "2025-01-01", testRef("/пакет/справка.docx", 3)))),
			wantCount: 2,
			verify: func(t *testing.T, rep Report) {
				joined := rep.Findings[0].Text + rep.Findings[1].Text
				for _, want := range []string{"срок свежести 30 дн.", "срок свежести 365 дн."} {
					if !strings.Contains(joined, want) {
						t.Errorf("в замечаниях нет %q:\n%s", want, joined)
					}
				}
				for _, f := range rep.Findings {
					if f.Severity != SeverityBlocking {
						t.Errorf("severity = %q, хотели blocking", f.Severity)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep, note := runFresh(t, tc.items, tc.pkg)
			if note != tc.wantNote {
				t.Errorf("note = %q, хотели %q", note, tc.wantNote)
			}
			if len(rep.Findings) != tc.wantCount {
				t.Fatalf("найдено %d замечаний, хотели %d: %+v", len(rep.Findings), tc.wantCount, rep.Findings)
			}
			for _, f := range rep.Findings {
				// Критерий 6: 100 % замечаний класса имеют адрес источника.
				if len(f.Refs) == 0 {
					t.Errorf("замечание без адреса фрагмента-источника: %+v", f)
				}
				if f.Rule != "fresh-1" || f.Class != ClassFreshness {
					t.Errorf("rule/class = %q/%q, хотели fresh-1/%q", f.Rule, f.Class, ClassFreshness)
				}
			}
			if tc.wantCount == 1 {
				f := rep.Findings[0]
				if tc.wantSub != "" && !strings.Contains(f.Text, tc.wantSub) {
					t.Errorf("в тексте нет %q:\n%s", tc.wantSub, f.Text)
				}
				if tc.wantSev != "" && f.Severity != tc.wantSev {
					t.Errorf("severity = %q, хотели %q", f.Severity, tc.wantSev)
				}
				for _, para := range tc.wantParas {
					if !hasPara(f, "/пакет/справка.docx", para) {
						t.Errorf("в адресах нет абзаца %d: %+v", para, f.Refs)
					}
				}
			}
			if tc.verify != nil {
				tc.verify(t, rep)
			}
		})
	}
}

// TestFreshnessHandlerErrors — ошибки обработчика, недостижимые через
// LoadConfig (конфиг их отвергает), но обязанные возвращаться явно: битый
// procedure_date и битый glob паттерн сопоставления.
func TestFreshnessHandlerErrors(t *testing.T) {
	tests := []struct {
		name       string
		items      []FreshnessItem
		wantErrSub string
	}{
		{
			name: "неверный procedure_date — ошибка, а не расчёт от нулевой даты",
			items: []FreshnessItem{{
				ID: "fns", Title: "Справка ФНС", Days: 30,
				Anchor: anchorProcedureDate, ProcedureDate: "01.02.2026",
				Match: CompositionMatch{By: matchByFilename, Pattern: "справка*.docx"},
			}},
			wantErrSub: "неверная дата",
		},
		{
			name: "битый glob паттерн — ErrConfig",
			items: []FreshnessItem{{
				ID: "fns", Title: "Справка ФНС", Days: 30, Anchor: anchorRunDate,
				Match: CompositionMatch{By: matchByFilename, Pattern: "*[a-]"},
			}},
			wantErrSub: "неверный glob",
		},
	}

	rule := Rule{ID: "fresh-1", Class: ClassFreshness, Severity: SeverityBlocking}
	// Пакет с файлом: ошибка filepath.Match в matchComposition возникает
	// только при сопоставлении с реальным файлом пакета.
	pkg := Package{Root: "/пакет", Files: []File{
		{RelPath: "справка.docx", Name: "справка.docx", Kind: "docx", Supported: true},
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var note string
			handler := NewFreshnessHandler(tc.items, freshRunTime, &note)
			findings, err := handler(rule, pkg)
			if err == nil {
				t.Fatalf("ожидали ошибку %q, получено nil (findings %+v)", tc.wantErrSub, findings)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("ошибка = %v, хотели содержащую %q", err, tc.wantErrSub)
			}
			if !errors.Is(err, ErrConfig) {
				t.Errorf("ошибка не несёт ErrConfig: %v", err)
			}
			if findings != nil {
				t.Errorf("при ошибке findings = %+v, хотели nil", findings)
			}
		})
	}
}

// TestFreshnessNoteReceiver — контракт приёмника пометки: обработчик
// ПЕРЕЗАПИСЫАЕТ note при каждом вызове, nil-приёмник допустим.
func TestFreshnessNoteReceiver(t *testing.T) {
	rule := Rule{ID: "fresh-1", Class: ClassFreshness, Severity: SeverityBlocking}
	items := []FreshnessItem{freshItem("fns", 30, "справка*.docx")}

	t.Run("пустой каталог перезаписывает прежнюю пометку", func(t *testing.T) {
		note := "старое значение"
		handler := NewFreshnessHandler(nil, freshRunTime, &note)
		findings, err := handler(rule, Package{Root: "/пакет"})
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		if note != freshEmptyNote {
			t.Errorf("note = %q, хотели %q", note, freshEmptyNote)
		}
		if len(findings) != 0 {
			t.Errorf("findings = %+v, хотели пусто", findings)
		}
	})

	t.Run("непустой каталог — пометка сбрасывается в пустую строку", func(t *testing.T) {
		note := "старое значение"
		handler := NewFreshnessHandler(items, freshRunTime, &note)
		if _, err := handler(rule, Package{Root: "/пакет"}); err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		if note != "" {
			t.Errorf("note = %q, хотели пустую строку", note)
		}
	})

	t.Run("nil-приёмник — без паники", func(t *testing.T) {
		for _, catalog := range [][]FreshnessItem{nil, items} {
			handler := NewFreshnessHandler(catalog, freshRunTime, nil)
			if _, err := handler(rule, Package{Root: "/пакет"}); err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
		}
	})
}

// TestParseFreshnessItem — каталог читается из JSON-конфига как данные
// (критерий 1): оба anchor'а, оба способа сопоставления.
func TestParseFreshnessItem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	body := `{
	  "version": 1,
	  "freshness": [
	    {"id": "fns", "title": "Справка ФНС", "days": 30,
	     "anchor": "procedure_date", "procedure_date": "2026-03-01",
	     "match": {"by": "filename", "pattern": "справка*.pdf"}},
	    {"id": "egrul", "title": "Выписка ЕГРЮЛ", "days": 60,
	     "anchor": "run_date", "match": {"by": "kind", "kind": "pdf"}}
	  ],
	  "rules": [{"id": "fresh", "class": "freshness", "severity": "blocking"}]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: неожиданная ошибка: %v", err)
	}
	want := []FreshnessItem{
		{
			ID: "fns", Title: "Справка ФНС", Days: 30,
			Anchor: anchorProcedureDate, ProcedureDate: "2026-03-01",
			Match: CompositionMatch{By: matchByFilename, Pattern: "справка*.pdf"},
		},
		{
			ID: "egrul", Title: "Выписка ЕГРЮЛ", Days: 60,
			Anchor: anchorRunDate,
			Match:  CompositionMatch{By: matchByKind, Kind: "pdf"},
		},
	}
	if len(cfg.Freshness) != len(want) {
		t.Fatalf("каталог из %d пунктов, хотели %d: %+v", len(cfg.Freshness), len(want), cfg.Freshness)
	}
	for i := range want {
		if cfg.Freshness[i] != want[i] {
			t.Errorf("пункт #%d = %+v, хотели %+v", i, cfg.Freshness[i], want[i])
		}
	}
}

// TestParseFreshnessItemErrors — негативные кейсы разбора пункта каталога:
// каждое сообщение несёт ErrConfig (CLI возвращает код 2).
func TestParseFreshnessItemErrors(t *testing.T) {
	tests := []struct {
		name string
		item string
	}{
		{"пустой id", `{"id": "  ", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "kind", "kind": "pdf"}}`},
		{"пустой title", `{"id": "x", "title": "", "days": 30, "anchor": "run_date", "match": {"by": "kind", "kind": "pdf"}}`},
		{"days = 0", `{"id": "x", "title": "T", "days": 0, "anchor": "run_date", "match": {"by": "kind", "kind": "pdf"}}`},
		{"days отрицательные", `{"id": "x", "title": "T", "days": -5, "anchor": "run_date", "match": {"by": "kind", "kind": "pdf"}}`},
		{"anchor отсутствует", `{"id": "x", "title": "T", "days": 30, "match": {"by": "kind", "kind": "pdf"}}`},
		{"anchor неизвестный", `{"id": "x", "title": "T", "days": 30, "anchor": "today", "match": {"by": "kind", "kind": "pdf"}}`},
		{"procedure_date при run_date", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "procedure_date": "2026-01-01", "match": {"by": "kind", "kind": "pdf"}}`},
		{"procedure_date отсутствует при procedure_date", `{"id": "x", "title": "T", "days": 30, "anchor": "procedure_date", "match": {"by": "kind", "kind": "pdf"}}`},
		{"procedure_date неверного формата", `{"id": "x", "title": "T", "days": 30, "anchor": "procedure_date", "procedure_date": "01.02.2026", "match": {"by": "kind", "kind": "pdf"}}`},
		{"неизвестное поле пункта", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "severity": "blocking", "match": {"by": "kind", "kind": "pdf"}}`},
		{"структура пункта не объект", `[1, 2]`},
		{"by = requisite", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "requisite", "kind": "inn"}}`},
		{"match отсутствует", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {}}`},
		{"filename без pattern", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "filename"}}`},
		{"filename с незакрытой группой", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "filename", "pattern": "справка*[.docx"}}`},
		{"kind вне перечня обхода", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "kind", "kind": "txt"}}`},
		{"kind вместе с pattern", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "kind", "kind": "pdf", "pattern": "x.pdf"}}`},
		{"filename вместе с kind", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "filename", "pattern": "x.pdf", "kind": "pdf"}}`},
		{"match.by неизвестный", `{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "content"}}`},
	}

	path := filepath.Join(t.TempDir(), "rules.json")
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"version": 1, "freshness": [` + tc.item + `]}`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("запись конфига: %v", err)
			}
			if _, err := LoadConfig(path); !errors.Is(err, ErrConfig) {
				t.Errorf("ошибка = %v, хотели ErrConfig (конфиг: %s)", err, body)
			}
		})
	}

	t.Run("дубликат id", func(t *testing.T) {
		dup := `{"version": 1, "freshness": [
			{"id": "x", "title": "T", "days": 30, "anchor": "run_date", "match": {"by": "kind", "kind": "pdf"}},
			{"id": "x", "title": "T2", "days": 40, "anchor": "run_date", "match": {"by": "kind", "kind": "docx"}}]}`
		if err := os.WriteFile(path, []byte(dup), 0o600); err != nil {
			t.Fatalf("запись конфига: %v", err)
		}
		if _, err := LoadConfig(path); !errors.Is(err, ErrConfig) {
			t.Errorf("дубликат id: ошибка = %v, хотели ErrConfig", err)
		}
	})
}
