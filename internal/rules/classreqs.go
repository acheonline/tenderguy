package rules

import (
	"fmt"
	"strings"

	"tenderguy/internal/reqs"
	"tenderguy/internal/trace"
)

// Класс правил «реквизиты» (P2.3): сверка реквизитов документов пакета с
// эталонными реквизитами юрлиц из раздела orgs конфигурации и внутренняя
// консистентность пакета — одно и то же поле не вправе различаться между
// документами, если только это не разные законные стороны (заказчик и
// поставщик), чьи реквизиты заданы эталонами.
//
// Уровни замечаний распределены так (критерии #58):
//
//   - расхождение с эталоном — всегда blocking: подавать пакет с чужими
//     реквизитами нельзя, уровень не настраивается;
//   - внутреннее расхождение между документами — severity правила из конфига:
//     оператор решает, блокировать ли пакет, где черновик формы разошёлся с
//     подписанным договором;
//   - отсутствие поля — warning «реквизит не найден»: пустая форма законна
//     (свойство dump-docs), а «не найдено» обязано отличаться от «не
//     эквивалентно» (контракт Match из P2.2).
const ClassReqs Class = "reqs"

// maxFindingRefs — потолок адресов одного замечания сверки: значение может
// стоять в каждом абзаце договора, и полный список раздувает отчёт, не добавляя
// информации. Значения в карточке уже ограничены reqs.maxItemRefs, здесь
// ограничивается сумма по документам и значениям.
const maxFindingRefs = 20

// reqsGroup — один сверяемый вид реквизита: метка для человека, вид значения
// в карточке reqs, функция нормализации и эталонные значения всех юрлиц.
// Группа общая для всех эталонов: документ сверяется с каждым, и расхождение
// рождается только если значение не совпало ни с одним.
type reqsGroup struct {
	label string
	kind  string
	norm  func(string) string
	refs  []reqsRefValue
}

// reqsRefValue — эталонное значение одного юрлица: нормализованная форма для
// сравнения и исходная для текста замечания.
type reqsRefValue struct {
	norm string
	raw  string
	org  string
}

// reqsKindOrder — фиксированный порядок видов в замечаниях: сначала «кто»
// (наименование и идентификаторы), затем «где». Порядок задан явно, а не
// обходом карты, чтобы отчёт не «прыгал» между прогонами.
var reqsKindOrder = []string{
	reqs.KindOrgName, reqs.KindINN10, reqs.KindINN12, reqs.KindKPP,
	reqs.KindOGRN, reqs.KindOGRNIP, reqs.KindAddress,
}

// reqsKindLabel — имя вида для текста замечания.
func reqsKindLabel(kind string) string {
	switch kind {
	case reqs.KindOrgName:
		return "наименование"
	case reqs.KindINN10, reqs.KindINN12:
		return "ИНН"
	case reqs.KindKPP:
		return "КПП"
	case reqs.KindOGRN:
		return "ОГРН"
	case reqs.KindOGRNIP:
		return "ОГРНИП"
	case reqs.KindAddress:
		return "юридический адрес"
	default:
		return kind
	}
}

// reqsKindNorm — функция нормализации вида: наименование и адрес приводят
// нормализаторы P2.2 (регистр, кавычки, ОПФ; «ул.» и «улица»), числовые
// реквизиты — только цифры.
func reqsKindNorm(kind string) func(string) string {
	switch kind {
	case reqs.KindOrgName:
		return reqs.NormalizeOrgName
	case reqs.KindAddress:
		return reqs.NormalizeAddress
	default:
		return digitsOnly
	}
}

// orgINNKind и orgOGRNKind выбирают вид карточки по длине эталонного значения:
// ИНН юрлица и ИП, ОГРН и ОГРНИП хранятся в карточке разными видами.
func orgINNKind(digits string) string {
	switch len(digits) {
	case 10:
		return reqs.KindINN10
	case 12:
		return reqs.KindINN12
	}
	return ""
}

func orgOGRNKind(digits string) string {
	switch len(digits) {
	case 13:
		return reqs.KindOGRN
	case 15:
		return reqs.KindOGRNIP
	}
	return ""
}

// reqsGroups строит сверяемые виды: группа создаётся для каждого вида из
// reqsKindOrder, даже если эталонов для него нет — внутренняя консистентность
// (критерий 3 #58) обязана работать и в пакете без раздела orgs, а сверка с
// эталоном для пустой группы просто пропускается. Порядок групп фиксирован,
// чтобы отчёт не «прыгал» между прогонами.
func reqsGroups(orgs []Org) []reqsGroup {
	byKind := make(map[string][]reqsRefValue)
	for _, org := range orgs {
		label := org.Name
		if label == "" {
			label = org.ID
		}
		add := func(kind, raw string) {
			if kind == "" || strings.TrimSpace(raw) == "" {
				return
			}
			norm := reqsKindNorm(kind)(raw)
			if norm == "" {
				return
			}
			byKind[kind] = append(byKind[kind], reqsRefValue{norm: norm, raw: strings.TrimSpace(raw), org: label})
		}
		add(reqs.KindOrgName, org.Name)
		add(orgINNKind(org.INN), org.INN)
		add(reqs.KindKPP, org.KPP)
		add(orgOGRNKind(org.OGRN), org.OGRN)
		add(reqs.KindAddress, org.Address)
	}

	out := make([]reqsGroup, 0, len(reqsKindOrder))
	for _, kind := range reqsKindOrder {
		out = append(out, reqsGroup{label: reqsKindLabel(kind), kind: kind, norm: reqsKindNorm(kind), refs: byKind[kind]})
	}
	return out
}

// NewReqsHandler создаёт обработчик класса «reqs» (P2.3). Эталонные реквизиты
// передаёт вызывающий код из раздела orgs конфигурации: Handler получает
// только правило и пакет, поэтому эталоны замыкаются в обработчик при
// регистрации. Пустой список эталонов законен: проверка внутренней
// консистентности работает и без них, а сверка с эталоном просто не находит
// расхождений.
func NewReqsHandler(orgs []Org) Handler {
	groups := reqsGroups(orgs)
	return func(rule Rule, pkg Package) ([]Finding, error) {
		out := refFindings(rule, groups, pkg)
		out = append(out, consistencyFindings(rule, groups, pkg)...)
		return out, nil
	}
}

// cardItems — значения карточки документа данного вида.
func cardItems(doc Doc, kind string) []reqs.Item {
	if doc.Reqs == nil {
		return nil
	}
	var out []reqs.Item
	for _, it := range doc.Reqs.Items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// docRef — запасной адрес документа для замечания «реквизит не найден»:
// собственного адреса у ненайденного значения нет, поэтому показываем первое
// трассируемое место документа — первый адресованный реквизит карточки, а при
// пустой карточке первый фрагмент. Документ без единого адреса пропускается:
// замечание, которое некуда показать, нарушило бы инвариант адресности.
func docRef(doc Doc) (trace.Ref, bool) {
	if doc.Reqs != nil {
		for _, it := range doc.Reqs.Items {
			for _, ref := range it.Refs {
				if ref.Traced() {
					return ref, true
				}
			}
		}
	}
	for _, frag := range doc.Fragments {
		if ref := frag.RefAt(0); ref.Traced() {
			return ref, true
		}
	}
	return trace.Ref{}, false
}

// appendRefs дописывает адреса значения в список замечания, соблюдая потолок
// maxFindingRefs и не повторяя уже известные.
func appendRefs(dst []trace.Ref, src []trace.Ref) []trace.Ref {
	for _, ref := range src {
		if len(dst) >= maxFindingRefs {
			return dst
		}
		if !ref.Traced() {
			continue
		}
		dup := false
		for _, have := range dst {
			if have == ref {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, ref)
		}
	}
	return dst
}

// displayRaw — текст значения для замечания: карточка хранит в Value
// нормализованную форму (наименование и адрес нормализует уже P2.2), а
// человеку показывать нужно значение как оно напечатано в документе.
func displayRaw(it reqs.Item) string {
	if it.Raw != "" {
		return it.Raw
	}
	return it.Value
}

// docValue — значение реквизита документа: карточное Value для сравнения
// после нормализации, исходный текст для текста замечания и адреса, где
// значение встретилось.
type docValue struct {
	value string
	raw   string
	refs  []trace.Ref
}

// refFindings сверяет каждое значение документа с эталонами: значение, не
// эквивалентное ни одному эталону, порождает блокирующее замечание со значением
// из документа, значением эталона и всеми адресами, где оно встретилось
// (критерий 2 #58); отсутствие всех значений вида — предупреждение «реквизит не
// найден» (критерий 4).
//
// Сверка идёт по значению, а не по документу: документ с несколькими значениями
// одного вида (например, ИНН поставщика в шапке и ИНН субподрядчика в теле)
// проверяется значение за значением, и «чужое» значение не прячется за соседним
// корректным. Пакет нескольких законных сторон при этом не даёт ложных
// замечаний: каждое его значение совпадает с одним из эталонов orgs.
func refFindings(rule Rule, groups []reqsGroup, pkg Package) []Finding {
	out := []Finding{}
	for _, group := range groups {
		if len(group.refs) == 0 {
			// Эталон для вида не задан: сверять не с чем, но внутренняя
			// консистентность вида остаётся в силе (consistencyFindings).
			continue
		}
		refText := refsText(group.refs)
		for _, doc := range pkg.Docs {
			items := cardItems(doc, group.kind)
			// Значения, которые нормализуются в пустоту (вырожденный адрес или
			// наименование), считаются ненайденными: сравнивать нечего.
			var values, unmatched []docValue
			for _, it := range items {
				if group.norm(it.Value) == "" {
					continue
				}
				dv := docValue{value: it.Value, raw: displayRaw(it), refs: it.Refs}
				values = append(values, dv)
				if !matchesRef(dv.value, group) {
					unmatched = append(unmatched, dv)
				}
			}
			if len(values) == 0 {
				ref, ok := docRef(doc)
				if !ok {
					continue
				}
				out = append(out, Finding{
					Rule:     rule.ID,
					Class:    rule.Class,
					Severity: SeverityWarning,
					Text: fmt.Sprintf("%s: реквизит не найден в документе %q (эталон: %s)",
						group.label, doc.RelPath, refText),
					Refs: []trace.Ref{ref},
				})
				continue
			}
			// Все значения вида совпали с эталонами — документ чист по этому виду.
			if len(unmatched) == 0 {
				continue
			}
			var refs []trace.Ref
			for _, dv := range unmatched {
				refs = appendRefs(refs, dv.refs)
			}
			if len(refs) == 0 {
				// Значения есть, но адресов у них нет (дыра парсера): берём
				// запасной адрес документа, а без него замечание некуда
				// показать — пропускаем, а не роняем прогон.
				ref, ok := docRef(doc)
				if !ok {
					continue
				}
				refs = []trace.Ref{ref}
			}
			out = append(out, Finding{
				Rule:     rule.ID,
				Class:    rule.Class,
				Severity: SeverityBlocking,
				Text: fmt.Sprintf("%s в документе %q не совпадает с эталоном: в документе %s, по эталону %s",
					group.label, doc.RelPath, distinctList(unmatched), refText),
				Quote: unmatched[0].raw,
				Refs:  refs,
			})
		}
	}
	return out
}

// matchesRef — значение документа эквивалентно хотя бы одному эталону группы
// (сравнение по нормализованной форме).
func matchesRef(value string, group reqsGroup) bool {
	norm := group.norm(value)
	for _, ref := range group.refs {
		if norm == ref.norm {
			return true
		}
	}
	return false
}

// refsText — перечень эталонных значений для текста замечания:
// «7707083893 (ООО «Ромашка»)». Несколько эталонов перечисляются через запятую.
func refsText(refs []reqsRefValue) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, fmt.Sprintf("%s (%s)", ref.raw, ref.org))
	}
	return strings.Join(parts, ", ")
}

// distinctList — исходные тексты значений без повторов, через запятую: одно и
// то же ИНН может стоять в документе в трёх местах, а в тексте замечания нужно
// один раз.
func distinctList(values []docValue) string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v.raw] {
			seen[v.raw] = true
			out = append(out, v.raw)
		}
	}
	return strings.Join(out, ", ")
}

// distinctStrings — строки без повторов: список документов в тексте замечания
// внутренней консистентности.
func distinctStrings(values []string) string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

// occurrence — одно нормализованное значение реквизита в пакете: какие
// документы и адреса его несут.
type occurrence struct {
	raw  string
	docs []string
	refs []trace.Ref
}

// consistencyFindings — внутренняя консистентность пакета (критерий 3 #58):
// один и тот же реквизит различается между документами → замечание с адресами
// всех сторон и severity правила из конфига. Расхождение не рождается, когда
// все различные значения присутствуют в эталонах orgs: реквизиты заказчика и
// поставщика обязаны различаться, это разные законные стороны.
func consistencyFindings(rule Rule, groups []reqsGroup, pkg Package) []Finding {
	out := []Finding{}
	for _, group := range groups {
		byNorm := make(map[string]*occurrence)
		order := []string{}
		for _, doc := range pkg.Docs {
			for _, it := range cardItems(doc, group.kind) {
				norm := group.norm(it.Value)
				if norm == "" {
					continue
				}
				occ, ok := byNorm[norm]
				if !ok {
					occ = &occurrence{raw: displayRaw(it)}
					byNorm[norm] = occ
					order = append(order, norm)
				}
				occ.docs = append(occ.docs, doc.RelPath)
				occ.refs = appendRefs(occ.refs, it.Refs)
			}
		}
		if len(order) < 2 {
			continue
		}
		// Все значения покрыты эталонами — это разные законные стороны пакета.
		unexplained := 0
		for _, norm := range order {
			if !inRefs(norm, group.refs) {
				unexplained++
			}
		}
		if unexplained == 0 {
			continue
		}

		parts := make([]string, 0, len(order))
		var refs []trace.Ref
		quote := ""
		for _, norm := range order {
			occ := byNorm[norm]
			parts = append(parts, fmt.Sprintf("%s (%s)", occ.raw, distinctStrings(occ.docs)))
			refs = appendRefs(refs, occ.refs)
			if quote == "" {
				quote = occ.raw
			}
		}
		if len(refs) == 0 {
			// Ни у одного значения нет адреса: показывать нечего, пропуск
			// вместо обрыва прогона (инвариант адресности).
			continue
		}
		out = append(out, Finding{
			Rule:     rule.ID,
			Class:    rule.Class,
			Severity: rule.Severity,
			Text:     fmt.Sprintf("%s различается между документами пакета: %s", group.label, strings.Join(parts, " и ")),
			Quote:    quote,
			Refs:     refs,
		})
	}
	return out
}

// inRefs — нормализованное значение присутствует среди эталонов группы.
func inRefs(norm string, refs []reqsRefValue) bool {
	for _, ref := range refs {
		if ref.norm == norm {
			return true
		}
	}
	return false
}
