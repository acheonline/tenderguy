package rules

import (
	"fmt"
	"strings"
	"time"

	"tenderguy/internal/reqs"
	"tenderguy/internal/trace"
)

// Класс правил «сроки свежести» (P2.5): проверка дат документов пакета по
// каталогу «тип документа × срок (дней) × точка отсчёта» из раздела freshness
// конфигурации.
//
// Уровни замечаний фиксированы критериями #60 и не берутся из severity
// правила конфига (как в классе «composition»):
//
//   - просрочка относительно точки отсчёта — всегда blocking: подавать пакет
//     с несвежим документом нельзя;
//   - несколько различных дат в документе (дата выдачи и дата подписания) —
//     blocking, если хотя бы один кандидат просрочен, иначе warning: движок
//     не выбирает дату молча, решение принимает специалист;
//   - дата в документе есть, но сопоставимого пункта каталога нет — warning
//     «срок свежести не определён»: каталог по умолчанию пуст (реальные сроки
//     — данные #23), и молчаливый ноль замечаний означал бы «всё свежее».
//
// Пустой каталог — явная пометка «не проверяется» в отчёте (критерий 3 #60),
// а не отсутствие замечаний: приёмка обязана отличать «проверили, свежо» от
// «не проверяли».
const ClassFreshness Class = "freshness"

// freshEmptyNote — пометка отчёта для пустого каталога сроков: образец —
// «нечего измерять» у metric и «проверять нечего» у пустого конфига правил.
const freshEmptyNote = "не проверяется: каталог сроков отсутствует"

// freshDateFormat — формат даты в карточке reqs (KindDate) и в конфиге
// (procedure_date): сравнение дат идёт по дню, поэтому и точка отсчёта, и
// дата документа урезаются до даты.
const freshDateFormat = "2006-01-02"

// freshDate — одна различимая дата документа: значение для расчёта, исходный
// текст для человека и адреса, где дата встретилась.
type freshDate struct {
	value time.Time
	raw   string
	refs  []trace.Ref
}

// NewFreshnessHandler создаёт обработчик класса «freshness» (P2.5). Каталог
// сроков передаёт вызывающий код из раздела freshness конфигурации: Handler
// получает только правило и пакет, поэтому items замыкаются в обработчик при
// регистрации (образец — NewReqsHandler, NewCompositionHandler). now — точка
// отсчёта «дата прогона»; она внедряется извне, а не берётся time.Now() внутри,
// чтобы тест видел детерминированный расчёт. note — приёмник пометки отчёта:
// обработчик ПЕРЕЗАПИСЫВАЕТ его при каждом вызове (пустой каталог — пометка
// «не проверяется», иначе пустая строка), а cmd переносит значение в
// Report.Note после Run.
func NewFreshnessHandler(items []FreshnessItem, now time.Time, note *string) Handler {
	return func(rule Rule, pkg Package) ([]Finding, error) {
		if note != nil {
			*note = ""
			if len(items) == 0 {
				*note = freshEmptyNote
			}
		}
		if len(items) == 0 {
			// Каталога нет: сравнивать даты не с чем. Пометка «не проверяется»
			// уже записана в note — молча возвращать пустой список нельзя.
			return []Finding{}, nil
		}

		anchors := make([]time.Time, len(items))
		for i, item := range items {
			anchor, err := freshnessAnchor(item, dateOnly(now))
			if err != nil {
				return nil, err
			}
			anchors[i] = anchor
		}

		// Сопоставление документов с пунктами каталога переиспользует механизм
		// класса «composition» (filename glob или kind): смысл сопоставления
		// тот же — найти файлы пункта, а проверяются только разобранные
		// документы (нужна карточка реквизитов с датами).
		compItems := make([]CompositionItem, len(items))
		for i, item := range items {
			compItems[i] = CompositionItem{ID: item.ID, Match: item.Match}
		}
		matched, err := matchComposition(compItems, pkg)
		if err != nil {
			return nil, err
		}
		docsByRel := make(map[string]Doc, len(pkg.Docs))
		for _, doc := range pkg.Docs {
			docsByRel[doc.RelPath] = doc
		}

		out := []Finding{}
		checked := make(map[string]bool)
		for i, item := range items {
			for _, rel := range matched[i] {
				doc, ok := docsByRel[rel]
				if !ok {
					// Неразобранный файл (архив, служебный): карточки нет,
					// проверять нечего; неполнота входа уже видна в Failed.
					continue
				}
				checked[rel] = true
				dates := docDates(doc)
				if len(dates) == 0 {
					// Дат в документе нет: измерять нечего, это не просрочка.
					continue
				}
				if f, ok := freshnessFinding(rule, item, anchors[i], doc, dates); ok {
					out = append(out, f)
				}
			}
		}
		// Документ с датами, не попавший ни под один пункт каталога (критерий
		// 4 #60): в порядке обхода пакета.
		for _, doc := range pkg.Docs {
			if checked[doc.RelPath] {
				continue
			}
			dates := docDates(doc)
			if len(dates) == 0 {
				continue
			}
			if f, ok := undefinedFreshnessFinding(rule, doc, dates); ok {
				out = append(out, f)
			}
		}
		return out, nil
	}
}

// freshnessAnchor — точка отсчёта пункта: фиксированная дата процедуры или
// дата прогона, обе урезаны до дня. Ошибка разбора procedure_date после
// LoadConfig невозможна (parseFreshnessItem проверяет формат), но обработчик
// обязан вернуть её явно, а не считать от нулевой даты.
func freshnessAnchor(item FreshnessItem, today time.Time) (time.Time, error) {
	if item.Anchor == anchorRunDate {
		return today, nil
	}
	anchor, err := time.Parse(freshDateFormat, item.ProcedureDate)
	if err != nil {
		return time.Time{}, configErr("пункт каталога сроков %q: procedure_date = %q — неверная дата: %v",
			item.ID, item.ProcedureDate, err)
	}
	return anchor, nil
}

// dateOnly урезает момент времени до календарного дня (UTC): сравнение
// «просрочен ли срок» идёт по датам, и часовой пояс прогона не должен сдвигать
// границу дня.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// docDates — различные календарно корректные даты документа из карточки
// реквизитов: KindDate и Valid=true (некорректные даты — забота класса
// «reqs»), различие по Value, порядок — как в карточке. Повтор одной и той же
// даты в трёх местах документа — один кандидат, но со всеми адресами: их
// уже слил reqs.Item.
func docDates(doc Doc) []freshDate {
	if doc.Reqs == nil {
		return nil
	}
	var out []freshDate
	seen := make(map[string]bool)
	for _, it := range doc.Reqs.Items {
		if it.Kind != reqs.KindDate || !it.Valid || seen[it.Value] {
			continue
		}
		value, err := time.Parse(freshDateFormat, it.Value)
		if err != nil {
			// Value у валидной даты всегда «ГГГГ-ММ-ДД»; если формат разошёлся,
			// сравнивать нечего — кандидат пропускается, а не обрывает прогон.
			continue
		}
		seen[it.Value] = true
		out = append(out, freshDate{value: value, raw: displayRaw(it), refs: it.Refs})
	}
	return out
}

// anchorText — название точки отсчёта для текста замечания: специалист обязан
// видеть, от какой именно даты и какого значения считается просрочка
// (критерий 2 #60).
func anchorText(item FreshnessItem, anchor time.Time) string {
	label := "дата прогона"
	if item.Anchor == anchorProcedureDate {
		label = "дата процедуры"
	}
	return fmt.Sprintf("%s %s", label, anchor.Format(freshDateFormat))
}

// freshnessFinding — замечание по документу, сопоставленному с пунктом
// каталога. Одна дата: просрочка (точка отсчёта ПОСЛЕ даты истечения; «срок
// истекает сегодня» — не просрочка) даёт blocking. Несколько различных дат:
// ОДНО замечание перечисляет всех кандидатов с адресами (критерий 5 #60),
// уровень — blocking, если хотя бы один кандидат просрочен, иначе warning.
// Второе значение false означает «замечание не родилось»: свежо, дат нет или
// у замечания не набралось ни одного адреса (пропуск вместо обрыва прогона,
// образец — класс «reqs»).
func freshnessFinding(rule Rule, item FreshnessItem, anchor time.Time, doc Doc, dates []freshDate) (Finding, bool) {
	expiry := func(d freshDate) time.Time { return d.value.AddDate(0, 0, item.Days) }
	period := fmt.Sprintf("срок свежести %d дн. (%s)", item.Days, item.Title)
	where := anchorText(item, anchor)

	var severity Severity
	var text, quote string
	var refs []trace.Ref
	switch {
	case len(dates) == 1:
		exp := expiry(dates[0])
		if !anchor.After(exp) {
			return Finding{}, false
		}
		severity = SeverityBlocking
		text = fmt.Sprintf("документ %q просрочен: дата документа %s, %s, точка отсчёта — %s, срок истёк %s",
			doc.RelPath, dates[0].raw, period, where, exp.Format(freshDateFormat))
		quote = dates[0].raw
		refs = appendRefs(refs, dates[0].refs)
	default:
		parts := make([]string, 0, len(dates))
		raws := make([]string, 0, len(dates))
		overdue := false
		for _, d := range dates {
			exp := expiry(d)
			part := fmt.Sprintf("%s (истекает %s", d.raw, exp.Format(freshDateFormat))
			if anchor.After(exp) {
				overdue = true
				part += " — ПРОСРОЧЕН"
			}
			parts = append(parts, part+")")
			raws = append(raws, d.raw)
			refs = appendRefs(refs, d.refs)
		}
		quote = strings.Join(raws, ", ")
		severity = SeverityWarning
		if overdue {
			severity = SeverityBlocking
		}
		text = fmt.Sprintf("дата документа %q неоднозначна: несколько дат-кандидатов — %s; %s, точка отсчёта — %s. Какую дату считать датой документа, определяет специалист",
			doc.RelPath, strings.Join(parts, ", "), period, where)
	}
	if len(refs) == 0 {
		// У дат нет адресов (дыра парсера): запасной адрес документа, а без
		// него замечание некуда показать — пропуск, а не обрыв прогона.
		ref, ok := docRef(doc)
		if !ok {
			return Finding{}, false
		}
		refs = []trace.Ref{ref}
	}
	return Finding{
		Rule:     rule.ID,
		Class:    rule.Class,
		Severity: severity,
		Text:     text,
		Quote:    quote,
		Refs:     refs,
	}, true
}

// undefinedFreshnessFinding — предупреждение «срок свежести не определён»
// (критерий 4 #60): в документе есть даты, но сопоставимого пункта каталога
// нет. Перечисляет даты документа и их адреса. Второе значение false —
// у замечания не набралось ни одного адреса (пропуск вместо обрыва прогона).
func undefinedFreshnessFinding(rule Rule, doc Doc, dates []freshDate) (Finding, bool) {
	raws := make([]string, 0, len(dates))
	var refs []trace.Ref
	for _, d := range dates {
		raws = append(raws, d.raw)
		refs = appendRefs(refs, d.refs)
	}
	if len(refs) == 0 {
		ref, ok := docRef(doc)
		if !ok {
			return Finding{}, false
		}
		refs = []trace.Ref{ref}
	}
	return Finding{
		Rule:     rule.ID,
		Class:    rule.Class,
		Severity: SeverityWarning,
		Text: fmt.Sprintf("срок свежести не определён: документ %q содержит даты (%s), но не сопоставлен ни с одним пунктом каталога сроков",
			doc.RelPath, strings.Join(raws, ", ")),
		Refs: refs,
	}, true
}
