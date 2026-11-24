package main

import (
	"strings"
	"testing"

	"tenderguy/internal/rules"
)

// Замер recall движка правил на синтетическом мини-эталоне (P2.7, #62).
//
// Генератор вносит в копию эталона дефекты трёх детерминированных классов —
// реквизиты (подмена ИНН, КПП, ОГРН, наименования, юр. адреса), состав пакета
// (удаление обязательного документа), свежесть (просрочка даты относительно
// каталога) — и проверяет, что движок находит каждый внесённый дефект.
// Требования к исходу:
//
//   - 100 % обнаружения внесённых искажений: пропуск любого — падение теста;
//   - recall ≥ 0,90 суммарно по детерминированным классам (страховочная
//     граница эпика P2, при нуле пропусков всегда 1,000);
//   - ложные срабатывания мерятся прогоном чистого эталона (ожидание — ноль
//     замечаний) и перечисляются поимённо: правило, класс, текст, адреса;
//   - таблица «внесено ↔ найдено человеком ↔ найдено системой ↔ пропущено ↔
//     ложное» печатается в формате, согласованном с таблицей P4 (#5), чтобы
//     слои сводились в одну. «Найдено человеком» здесь — истина генератора:
//     он знает каждое внесённое искажение.
//
// Обнаружение искажения — предикат по замечанию: класс + признак вида
// замечания в тексте + место (имя файла или пункт каталога). Сопутствующие
// замечания того же дефекта (например, внутренняя консистентность alongside
// сверки с эталоном при подмене ИНН) пропуском и ложным срабатыванием не
// считаются: движок вправе показать дефект с нескольких сторон.

// recallDistortion — одно искажение: как изменить эталон и по какому
// замечанию считать его обнаруженным.
type recallDistortion struct {
	name   string
	class  rules.Class
	mutate func(spec *recallSpec)
	match  func(f rules.Finding) bool
}

// recallRefMismatch — предикат сверки с эталоном (контракт P2.3): «<вид> в
// документе <файл> не совпадает с эталоном: в документе <значение>, …».
func recallRefMismatch(label, file, value string) func(rules.Finding) bool {
	return func(f rules.Finding) bool {
		return f.Class == rules.ClassReqs &&
			strings.Contains(f.Text, label) &&
			strings.Contains(f.Text, "не совпадает с эталоном") &&
			strings.Contains(f.Text, file) &&
			strings.Contains(f.Text, value)
	}
}

// recallConsistency — предикат внутренней консистентности (контракт P2.3):
// «<вид> различается между документами пакета: …».
func recallConsistency(label string) func(rules.Finding) bool {
	return func(f rules.Finding) bool {
		return f.Class == rules.ClassReqs &&
			strings.Contains(f.Text, label) &&
			strings.Contains(f.Text, "различается между документами")
	}
}

// recallMissing — предикат пропуска обязательного документа (контракт P2.4):
// «нет документа в составе: <наименование> (пункт <id>)».
func recallMissing(title, item string) func(rules.Finding) bool {
	return func(f rules.Finding) bool {
		return f.Class == rules.ClassComposition &&
			strings.Contains(f.Text, "нет документа в составе: "+title+" (пункт "+item+")")
	}
}

// recallOverdue — предикат просрочки (контракт P2.5): «документ <файл>
// просрочен: …».
func recallOverdue(file string) func(rules.Finding) bool {
	return func(f rules.Finding) bool {
		return f.Class == rules.ClassFreshness &&
			strings.Contains(f.Text, "просрочен") &&
			strings.Contains(f.Text, file)
	}
}

// recallDistortions — полный перечень искажений: пять реквизитных, два
// составных и одно по свежести.
func recallDistortions() []recallDistortion {
	return []recallDistortion{
		{
			name:  "подмена ИНН в договоре",
			class: rules.ClassReqs,
			mutate: func(s *recallSpec) {
				s.dogovor = recallReplaceLine(s.dogovor, "ИНН:", "ИНН: "+recallFakeINN)
			},
			match: recallRefMismatch("ИНН", recallFileDogovor, recallFakeINN),
		},
		{
			name:  "подмена КПП в смете",
			class: rules.ClassReqs,
			mutate: func(s *recallSpec) {
				recallSetCell(s.smeta, "КПП", recallFakeKPP)
			},
			match: recallRefMismatch("КПП", recallFileSmeta, recallFakeKPP),
		},
		{
			name:  "подмена ОГРН в заявке",
			class: rules.ClassReqs,
			mutate: func(s *recallSpec) {
				s.zayavka = recallReplaceLine(s.zayavka, "ОГРН:", "ОГРН: "+recallFakeOGRN)
			},
			match: recallRefMismatch("ОГРН", recallFileZayavka, recallFakeOGRN),
		},
		{
			name:  "подмена наименования в договоре",
			class: rules.ClassReqs,
			mutate: func(s *recallSpec) {
				s.dogovor = recallReplaceLine(s.dogovor, "Полное наименование организации:",
					"Полное наименование организации: "+recallFakeName)
			},
			match: recallConsistency("наименование"),
		},
		{
			name:  "подмена юридического адреса в заявке",
			class: rules.ClassReqs,
			mutate: func(s *recallSpec) {
				s.zayavka = recallReplaceLine(s.zayavka, "Юридический адрес:",
					"Юридический адрес: "+recallFakeAddr)
			},
			match: recallConsistency("юридический адрес"),
		},
		{
			name:  "удаление сметы",
			class: rules.ClassComposition,
			mutate: func(s *recallSpec) {
				s.drop[recallFileSmeta] = true
			},
			match: recallMissing("Смета", "c3"),
		},
		{
			name:  "удаление банковской гарантии",
			class: rules.ClassComposition,
			mutate: func(s *recallSpec) {
				s.drop[recallFileGarant] = true
			},
			match: recallMissing("Банковская гарантия", "c4"),
		},
		{
			name:  "просрочка даты заявки",
			class: rules.ClassFreshness,
			mutate: func(s *recallSpec) {
				s.zayavka = recallReplaceLine(s.zayavka, "Дата подписания:",
					"Дата подписания: "+recallFakeDate)
			},
			match: recallOverdue(recallFileZayavka),
		},
	}
}

// TestRecallCleanBaseline — ложные срабатывания на чистом эталоне: ноль
// замечаний и ноль ошибок разбора. Любое замечание перечисляется поимённо
// (критерий 4 #62) — по ним калибруются пороги классов. Дополнительно тест
// удостоверяется, что эталон не вырожден: все четыре файла разобранны и
// прикрыты пунктами состава.
func TestRecallCleanBaseline(t *testing.T) {
	rulesPath := writeRecallRules(t)
	rep := runRecallEngine(t, writeRecallPackage(t, recallBaseline()), rulesPath)

	if len(rep.Failed) != 0 {
		t.Errorf("эталонный пакет: ошибки разбора %+v — фикстура повреждена", rep.Failed)
	}
	for _, f := range rep.Findings {
		t.Errorf("ложное срабатывание на чистом эталоне: %s", recallFindingLine(f))
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("чистый эталон дал замечаний: %d, хотели 0", len(rep.Findings))
	}

	present := map[string]bool{}
	for _, st := range rep.Composition {
		present[st.Item] = st.Present
	}
	for _, item := range []string{"c1", "c2", "c3", "c4"} {
		if !present[item] {
			t.Errorf("пункт состава %s не прикрыт файлом в чистом эталоне — фикстура вырождена", item)
		}
	}
	if len(rep.Composition) != 4 {
		t.Errorf("строк состава %d, хотели 4: %+v", len(rep.Composition), rep.Composition)
	}
	if rep.Note != "" {
		t.Errorf("чистый эталон с полным конфигом дал примечание %q — хотели пустое", rep.Note)
	}
}

// TestRecallDistortions — основной замер: каждое искажение вносится в свежую
// копию эталона и обязано быть обнаруженным. Таблица результата печатается
// всегда (в том числе при успехе) — она прикладывается к PR (критерий 6 #62).
func TestRecallDistortions(t *testing.T) {
	rulesPath := writeRecallRules(t)

	// Ложные срабатывания — прогон чистого эталона: счётчики по классам
	// попадают в итоговую таблицу, замечания перечисляются поимённо.
	falseByClass := map[rules.Class]int{}
	clean := runRecallEngine(t, writeRecallPackage(t, recallBaseline()), rulesPath)
	for _, f := range clean.Findings {
		falseByClass[f.Class]++
		t.Errorf("ложное срабатывание на чистом эталоне: %s", recallFindingLine(f))
	}

	type recallCounter struct{ injected, found, missed int }
	stats := map[rules.Class]*recallCounter{}
	classes := []rules.Class{rules.ClassReqs, rules.ClassComposition, rules.ClassFreshness}
	for _, class := range classes {
		stats[class] = &recallCounter{}
	}

	for _, d := range recallDistortions() {
		t.Run(d.name, func(t *testing.T) {
			spec := recallBaseline()
			d.mutate(&spec)
			rep := runRecallEngine(t, writeRecallPackage(t, spec), rulesPath)

			stats[d.class].injected++
			hits := 0
			for _, f := range rep.Findings {
				if d.match(f) {
					hits++
				}
			}
			if hits == 0 {
				stats[d.class].missed++
				t.Errorf("искажение %q (класс %s) НЕ НАЙДЕНО. Замечания прогона:", d.name, d.class)
				for _, f := range rep.Findings {
					t.Errorf("  %s", recallFindingLine(f))
				}
				if len(rep.Findings) == 0 {
					t.Errorf("  (замечаний нет вовсе)")
				}
				return
			}
			stats[d.class].found++
		})
	}

	// Таблица результата в формате «система ↔ человек» из #5 (P4):
	// «найдено человеком» — истина генератора (он знает внесённые искажения).
	recall := func(c *recallCounter) float64 {
		if c.injected == 0 {
			return 0
		}
		return float64(c.found) / float64(c.injected)
	}
	t.Log("Замер recall на синтетическом мини-эталоне (P2.7), формат таблицы — #5 (P4):")
	t.Logf("%-14s | %8s | %19s | %16s | %9s | %6s | %s",
		"Класс", "Внесено", "Найдено человеком", "Найдено системой", "Пропущено", "Ложное", "Recall")
	total := recallCounter{}
	for _, class := range classes {
		c := stats[class]
		total.injected += c.injected
		total.found += c.found
		total.missed += c.missed
		t.Logf("%-14s | %8d | %19d | %16d | %9d | %6d | %.3f",
			class, c.injected, c.injected, c.found, c.missed, falseByClass[class], recall(c))
	}
	falseTotal := 0
	for _, n := range falseByClass {
		falseTotal += n
	}
	t.Logf("%-14s | %8d | %19d | %16d | %9d | %6d | %.3f",
		"ИТОГО", total.injected, total.injected, total.found, total.missed, falseTotal, recall(&total))

	// Пороги (критерий 3 #62): 100 % обнаружения внесённых искажений и
	// recall ≥ 0,90 суммарно по детерминированным классам.
	if total.missed > 0 {
		t.Errorf("пропущено внесённых искажений: %d из %d — требуется 100 %% обнаружения",
			total.missed, total.injected)
	}
	const recallThreshold = 0.90
	if r := recall(&total); r < recallThreshold {
		t.Errorf("recall = %.3f ниже порога %.2f", r, recallThreshold)
	}
	if falseTotal > 0 {
		t.Errorf("чистый эталон дал ложных срабатываний: %d — требуется 0", falseTotal)
	}
	if total.injected != len(recallDistortions()) {
		t.Errorf("внесено %d искажений, ожидалось %d — перечень искажений изменён посреди замера",
			total.injected, len(recallDistortions()))
	}
	// Замер не вырожден: каждый класс представлен хотя бы одним искажением.
	for _, class := range classes {
		if stats[class].injected == 0 {
			t.Errorf("класс %s не получил ни одного искажения — замер по классу вырожден", class)
		}
	}
}
